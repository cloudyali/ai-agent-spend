package config

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Trailers is the [trailers] config from a repo's .aispend.toml — which commit
// cost-trailers attach and how they render. See
// design-documents/11-commit-cost-trailers.md.
type Trailers struct {
	Enabled      bool   // repo-wide gate; false suppresses trailers even with hooks installed
	Cost         bool   // AI-Cost line
	CostModels   bool   // AI-Cost-Models line
	Tokens       bool   // AI-Tokens line
	Interactions bool   // AI-Interactions line
	Precision    int    // decimals on cost (clamped 0–8)
	CostName     string // cost trailer key, from [trailers.rename] cost (default "AI-Cost")
}

// DefaultTrailers is the conservative default: enabled, cost-only, two decimals,
// named AI-Cost — used when no [trailers] section is present.
func DefaultTrailers() Trailers {
	return Trailers{Enabled: true, Cost: true, Precision: 2, CostName: "AI-Cost"}
}

// LoadTrailers reads [trailers] / [trailers.rename] from repoDir's .aispend.toml,
// starting from DefaultTrailers and overriding only the keys present. An absent file
// yields the defaults. An invalid value for a key is ignored (left at default)
// rather than erroring — this feeds a fail-open commit hook, so a typo in config
// must never block a commit.
func LoadTrailers(repoDir string) (Trailers, error) {
	t := DefaultTrailers()
	b, err := os.ReadFile(filepath.Join(repoDir, ".aispend.toml"))
	if errors.Is(err, os.ErrNotExist) {
		return t, nil
	}
	if err != nil {
		return t, err
	}
	secs, err := sectionedTOML(b)
	if err != nil {
		return DefaultTrailers(), fmt.Errorf("config: %s/.aispend.toml: %w", repoDir, err)
	}
	tr := secs["trailers"]
	setBool(tr, "enabled", &t.Enabled)
	setBool(tr, "cost", &t.Cost)
	setBool(tr, "cost_models", &t.CostModels)
	setBool(tr, "tokens", &t.Tokens)
	setBool(tr, "interactions", &t.Interactions)
	if v, ok := tr["precision"]; ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			t.Precision = max(0, min(n, 8))
		}
	}
	if name := strings.TrimSpace(secs["trailers.rename"]["cost"]); name != "" {
		t.CostName = name
	}
	return t, nil
}

// SetTrailers writes the [trailers] (and [trailers.rename]) config into repoDir's
// .aispend.toml, replacing any existing trailers sections and preserving every other
// line. Used by the in-explorer trailers editor.
func SetTrailers(repoDir string, t Trailers) error {
	path := filepath.Join(repoDir, ".aispend.toml")
	var lines []string
	if b, err := os.ReadFile(path); err == nil {
		if s := strings.TrimRight(string(b), "\n"); s != "" {
			lines = strings.Split(s, "\n")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lines = dropSections(lines, "trailers", "trailers.rename")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	var b strings.Builder
	if len(lines) > 0 {
		b.WriteString(strings.Join(lines, "\n"))
		b.WriteString("\n\n")
	}
	b.WriteString("[trailers]\n")
	b.WriteString("enabled = " + strconv.FormatBool(t.Enabled) + "\n")
	b.WriteString("cost = " + strconv.FormatBool(t.Cost) + "\n")
	b.WriteString("cost_models = " + strconv.FormatBool(t.CostModels) + "\n")
	b.WriteString("tokens = " + strconv.FormatBool(t.Tokens) + "\n")
	b.WriteString("interactions = " + strconv.FormatBool(t.Interactions) + "\n")
	b.WriteString("precision = " + strconv.Itoa(t.Precision) + "\n")
	if t.CostName != "" && t.CostName != "AI-Cost" {
		b.WriteString("\n[trailers.rename]\ncost = " + strconv.Quote(t.CostName) + "\n")
	}
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// dropSections returns lines with the named [section] blocks removed (the header
// through the line before the next [section] or EOF), preserving all other content.
func dropSections(lines []string, names ...string) []string {
	drop := map[string]bool{}
	for _, n := range names {
		drop[n] = true
	}
	var out []string
	skipping := false
	for _, ln := range lines {
		s := strings.TrimSpace(ln)
		if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
			skipping = drop[strings.TrimSpace(s[1:len(s)-1])]
			if skipping {
				continue
			}
		} else if skipping {
			continue
		}
		out = append(out, ln)
	}
	return out
}

func setBool(m map[string]string, key string, dst *bool) {
	switch strings.ToLower(strings.TrimSpace(m[key])) {
	case "true":
		*dst = true
	case "false":
		*dst = false
	}
}

// sectionedTOML parses the same flat key=value subset as parseTOML but tracks
// [section] headers, so a caller can read section-scoped keys (e.g. [trailers]).
// Top-level keys live under the "" section. It reuses the flat parser's unquote /
// inline-comment helpers, so the two stay consistent.
func sectionedTOML(data []byte) (map[string]map[string]string, error) {
	out := map[string]map[string]string{"": {}}
	section := ""
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			if out[section] == nil {
				out[section] = map[string]string{}
			}
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			return nil, fmt.Errorf("invalid line %q (want key = value)", line)
		}
		key := strings.TrimSpace(line[:eq])
		if key == "" {
			return nil, fmt.Errorf("empty key in %q", line)
		}
		out[section][key] = unquote(stripInlineComment(strings.TrimSpace(line[eq+1:])))
	}
	return out, sc.Err()
}
