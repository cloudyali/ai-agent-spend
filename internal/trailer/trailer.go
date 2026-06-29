// Package trailer is the write path for commit cost-trailers: it formats the
// per-commit trailer lines (AI-Cost: 0.42 …), applies them to a commit message
// idempotently, folds duplicates carried in by a squash, and drives the per-branch
// watermark state machine that decides what each commit owes (see state.go).
//
// It is the engine the installed git hooks call (`aispend trailer` / `consume`);
// the hooks themselves are fail-open, so any error here logs and the commit still
// succeeds. Pure-local: no network — the offline build and `doctor --network`
// promise are unaffected. See design-documents/DESIGN.md.
package trailer

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/event"
)

// Config selects which trailers attach and how they render. It mirrors the
// proposed `.aispend.toml [trailers]` block; parsing that file is a later
// increment — for now the CLI passes DefaultConfig().
type Config struct {
	Cost         bool   // AI-Cost: total api-equivalent
	CostModels   bool   // AI-Cost-Models: per-model
	Tokens       bool   // AI-Tokens: per-bucket breakdown (input/output/cache_read/cache_write/cache_write_1h)
	Interactions bool   // AI-Interactions: deduped request count
	Precision    int    // decimals on cost values
	CostName     string // trailer key for cost (default "AI-Cost"; rename-safe)
}

// DefaultConfig is the conservative default: only the cost line, named AI-Cost,
// two decimals. Multi-provider-honest naming (AI-, not Claude-) per doc 11.
func DefaultConfig() Config {
	return Config{Cost: true, Precision: 2, CostName: "AI-Cost"}
}

// Usage is the priced, deduped activity a commit will be stamped with. PerModel is
// micros per model; MaxTS is how far to advance the branch watermark once consumed.
type Usage struct {
	Cost     event.Money
	Tokens   event.Tokens
	Requests int
	PerModel map[string]int64
	MaxTS    time.Time
}

// Mode selects how Apply treats the message.
type Mode int

const (
	// ModeNormal appends the trailer block once (idempotent).
	ModeNormal Mode = iota
	// ModeSquash folds duplicate cost trailers (carried in from squashed commits)
	// into a single summed line and attaches nothing new.
	ModeSquash
)

// modeForSource routes on git's prepare-commit-msg $2 source hint. "merge" and
// "commit" (the latter covers -c/-C reuse and --amend, whose message is already
// stamped) skip entirely — re-scanning would double-count. "squash" folds.
func modeForSource(source string) (Mode, bool) {
	switch source {
	case "merge", "commit":
		return ModeNormal, true
	case "squash":
		return ModeSquash, false
	default:
		return ModeNormal, false
	}
}

// oneLine strips CR/LF so a crafted value (e.g. a model name from the logs, or a
// renamed trailer key) can't inject an extra line — or a forged trailer — into the
// commit message. Per design-documents/DESIGN.md (sanitize the write).
func oneLine(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

// formatCost renders micros as a fixed-precision decimal string ("0.42").
func formatCost(micros int64, precision int) string {
	if precision < 0 {
		precision = 0
	}
	return strconv.FormatFloat(float64(micros)/1e6, 'f', precision, 64)
}

// formatTokens renders the per-bucket token breakdown as "input=…,output=…,…",
// omitting zero buckets (so a turn with no cache-write doesn't carry cache_write=0).
// Canonical order matches the five token classes the engine prices. Empty when all
// buckets are zero.
func formatTokens(tk event.Tokens) string {
	buckets := []struct {
		name string
		n    int64
	}{
		{"input", tk.Input},
		{"output", tk.Output},
		{"cache_read", tk.CacheRead},
		{"cache_write", tk.CacheWrite},
		{"cache_write_1h", tk.CacheWrite1h},
	}
	var parts []string
	for _, b := range buckets {
		if b.n > 0 {
			parts = append(parts, b.name+"="+strconv.FormatInt(b.n, 10))
		}
	}
	return strings.Join(parts, ",")
}

// FormatTrailers renders the configured trailer lines for u. A zero-cost / zero-
// usage turn yields no lines — we never assert "AI-Cost: 0.00".
func FormatTrailers(u Usage, cfg Config) []string {
	var out []string
	if cfg.Cost && u.Cost.Micros > 0 {
		out = append(out, oneLine(cfg.CostName)+": "+formatCost(u.Cost.Micros, cfg.Precision))
	}
	if cfg.CostModels && len(u.PerModel) > 0 {
		models := make([]string, 0, len(u.PerModel))
		for m := range u.PerModel {
			models = append(models, m)
		}
		sort.Strings(models)
		parts := make([]string, 0, len(models))
		for _, m := range models {
			parts = append(parts, oneLine(m)+"="+formatCost(u.PerModel[m], cfg.Precision))
		}
		out = append(out, "AI-Cost-Models: "+strings.Join(parts, ","))
	}
	if cfg.Tokens {
		if line := formatTokens(u.Tokens); line != "" {
			out = append(out, "AI-Tokens: "+line)
		}
	}
	if cfg.Interactions && u.Requests > 0 {
		out = append(out, "AI-Interactions: "+strconv.Itoa(u.Requests))
	}
	return out
}

// Apply returns msg with trailers applied per mode. ModeNormal is idempotent: if a
// cost trailer is already present (prepare-commit-msg can fire twice for one
// message) it returns msg unchanged. ModeSquash folds duplicate cost lines.
func Apply(msg string, u Usage, cfg Config, mode Mode) string {
	if mode == ModeSquash {
		return foldCost(msg, cfg)
	}
	if hasTrailer(msg, cfg.CostName) {
		return msg
	}
	lines := FormatTrailers(u, cfg)
	if len(lines) == 0 {
		return msg
	}
	return appendBlock(msg, lines)
}

// foldCost sums every existing "<CostName>: <n>" line into one. Folding at micro
// precision (not on the rendered string) keeps the total exact across rewrites.
func foldCost(msg string, cfg Config) string {
	prefix := cfg.CostName + ": "
	var sumMicros int64
	found := false
	var kept []string
	for _, ln := range strings.Split(strings.TrimRight(msg, "\n"), "\n") {
		if strings.HasPrefix(ln, prefix) {
			found = true
			if v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(ln, prefix)), 64); err == nil {
				sumMicros += int64(math.Round(v * 1e6))
			}
			continue
		}
		kept = append(kept, ln)
	}
	if !found {
		return msg
	}
	return appendBlock(strings.Join(kept, "\n")+"\n", []string{cfg.CostName + ": " + formatCost(sumMicros, cfg.Precision)})
}

// hasTrailer reports whether msg already carries a "<name>: " trailer line.
func hasTrailer(msg, name string) bool {
	pfx := name + ": "
	for _, ln := range strings.Split(msg, "\n") {
		if strings.HasPrefix(ln, pfx) {
			return true
		}
	}
	return false
}

// appendBlock inserts the trailer lines as their own paragraph, after the message
// body and before any trailing comment block (git strips comments, but keeping
// trailers above them matches the git-trailer convention and reads correctly).
func appendBlock(msg string, lines []string) string {
	all := strings.Split(strings.TrimRight(msg, "\n"), "\n")
	cut := len(all)
	for cut > 0 {
		s := strings.TrimSpace(all[cut-1])
		if s == "" || strings.HasPrefix(s, "#") {
			cut--
			continue
		}
		break
	}
	head, tail := all[:cut], all[cut:]

	var b strings.Builder
	if len(head) > 0 {
		b.WriteString(strings.Join(head, "\n"))
		b.WriteString("\n\n")
	}
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n")
	if tailContent := strings.TrimLeft(strings.Join(tail, "\n"), "\n"); strings.TrimSpace(tailContent) != "" {
		b.WriteString("\n")
		b.WriteString(strings.TrimRight(tailContent, "\n"))
		b.WriteString("\n")
	}
	return b.String()
}
