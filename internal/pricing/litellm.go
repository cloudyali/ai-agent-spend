package pricing

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// providerPrefix matches a leading "vendor/" namespace LiteLLM/models.dev attach
// (e.g. "anthropic/", "openai/", "vertex_ai/").
var providerPrefix = regexp.MustCompile(`^[a-z0-9_]+/`)

// snapshotDate matches a trailing model snapshot date (-YYYYMMDD or -YYYY-MM-DD),
// mirroring normalize.canonicalModel so overlay keys land on the engine's ids.
var snapshotDate = regexp.MustCompile(`-\d{8}$|-\d{4}-\d{2}-\d{2}$`)

// modelAliases maps known LiteLLM/models.dev ids to aispend's canonical ids for
// cases the structural rules below can't reach (vendor nicknames, non-family
// models). Keys are already lowercased + prefix/date-stripped, and are consulted
// before the Claude family normalizer so they act as explicit overrides.
var modelAliases = map[string]string{
	"claude-opus-4.8":   "claude-opus-4-8",
	"claude-opus-4.7":   "claude-opus-4-7",
	"claude-opus-4.6":   "claude-opus-4-6",
	"claude-sonnet-4.6": "claude-sonnet-4-6",
	"claude-haiku-4.5":  "claude-haiku-4-5",
}

// Claude ids appear in two orders and two separators across sources:
// family-last ("claude-4-6-sonnet", "claude-4.6-sonnet") and family-first
// ("claude-sonnet-4-6", "claude-sonnet-4.6"). Both must land on the canonical
// family-first dashed form the engine prices by. Bounded to the opus/sonnet/haiku
// families so non-version dots elsewhere (e.g. "gpt-5.3-codex") are untouched.
var (
	claudeFamilyLast  = regexp.MustCompile(`^claude-(\d+)[.-](\d+)-(opus|sonnet|haiku)$`)
	claudeFamilyFirst = regexp.MustCompile(`^claude-(opus|sonnet|haiku)-(\d+)[.-](\d+)$`)
)

func normalizeClaudeVersion(s string) string {
	if m := claudeFamilyLast.FindStringSubmatch(s); m != nil {
		return "claude-" + m[3] + "-" + m[1] + "-" + m[2]
	}
	if m := claudeFamilyFirst.FindStringSubmatch(s); m != nil {
		return "claude-" + m[1] + "-" + m[2] + "-" + m[3]
	}
	return s
}

// canonicalizeModelID normalizes a LiteLLM/models.dev id toward aispend's canonical
// ids: lowercase, strip a vendor prefix and snapshot date, apply an explicit alias,
// then normalize Claude family order/separator. Dots are NOT blanket-converted to
// dashes — "gpt-5.3-codex" keeps its dot — only Claude version numbers are.
func canonicalizeModelID(id string) string {
	s := strings.ToLower(strings.TrimSpace(id))
	s = providerPrefix.ReplaceAllString(s, "")
	s = snapshotDate.ReplaceAllString(s, "")
	if alias, ok := modelAliases[s]; ok {
		return alias
	}
	return normalizeClaudeVersion(s)
}

// LiteLLM (github.com/BerriAI/litellm) publishes a community-maintained price
// table — model_prices_and_context_window.json — which aispend uses as its live
// rate source. We parse the subset we price on and convert its per-token
// USD costs into our micro-USD-per-1M-token rates, then overlay it on the embedded
// table so unknown models still fall back to a shipped floor.

// liteLLMEntry is the subset of a LiteLLM model record we consume. Absent fields
// stay zero (the JSON omits, e.g., cache_creation_input_token_cost for providers
// that don't charge for cache writes).
type liteLLMEntry struct {
	InputCostPerToken           float64 `json:"input_cost_per_token"`
	OutputCostPerToken          float64 `json:"output_cost_per_token"`
	CacheCreationInputTokenCost float64 `json:"cache_creation_input_token_cost"`
	CacheReadInputTokenCost     float64 `json:"cache_read_input_token_cost"`
}

// ParseLiteLLM maps a LiteLLM price table (model → per-token USD) into our rate
// table (micro-USD per 1M tokens). Entries with no positive input cost are skipped
// (e.g. the "sample_spec" placeholder). Missing cache costs map to 0 rather than a
// fabricated heuristic — a visible gap beats a wrong number, and the 1-hour
// cache-write tier is still derived at price time as 2× input.
func ParseLiteLLM(data []byte) (map[string]rate, error) {
	var raw map[string]liteLLMEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("litellm: parse table: %w", err)
	}
	// Sort for deterministic collision resolution: when several upstream ids
	// canonicalize to the same aispend id, the first sorted writer wins.
	models := make([]string, 0, len(raw))
	for m := range raw {
		models = append(models, m)
	}
	sort.Strings(models)

	out := make(map[string]rate, len(raw))
	for _, model := range models {
		e := raw[model]
		if e.InputCostPerToken <= 0 {
			continue // placeholder / zero-priced stub — excluded so a lookup can't resolve to a silent $0
		}
		canon := canonicalizeModelID(model)
		if _, exists := out[canon]; exists {
			continue
		}
		out[canon] = rate{
			InputPerMTok:      perTokenToMicrosPerMTok(e.InputCostPerToken),
			OutputPerMTok:     perTokenToMicrosPerMTok(e.OutputCostPerToken),
			CacheReadPerMTok:  perTokenToMicrosPerMTok(e.CacheReadInputTokenCost),
			CacheWritePerMTok: perTokenToMicrosPerMTok(e.CacheCreationInputTokenCost),
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("litellm: no priced models in table")
	}
	return out, nil
}

// perTokenToMicrosPerMTok converts $/token to micro-USD per 1,000,000 tokens:
// $/token × 1e6 tokens/Mtok × 1e6 micros/$ = ×1e12. Rounded, never negative.
func perTokenToMicrosPerMTok(costPerToken float64) int64 {
	if costPerToken <= 0 {
		return 0
	}
	v := math.Round(costPerToken * 1e12)
	// An out-of-range float→int64 conversion is implementation-defined in Go (it can
	// yield MinInt64 — a large negative rate), so a poisoned/absurd rate is clamped to
	// MaxInt64 deterministically rather than trusted. micros saturates downstream.
	if v >= math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}

// NewEngineWithRates returns an engine whose table is the embedded one with extra
// rates overlaid (extra wins per model) and, when non-empty, version stamped as
// the source. A nil/empty overlay yields the plain embedded engine unchanged — so
// callers can pass a refreshed LiteLLM map while keeping the embedded floor for
// any model LiteLLM doesn't list.
func NewEngineWithRates(version string, extra map[string]rate) *Engine {
	e := NewEngine() // fresh embedded table (own Models map) per call — safe to mutate
	if len(extra) == 0 {
		return e
	}
	for m, r := range extra {
		e.t.Models[m] = r
	}
	if version != "" {
		e.t.Version = version
	}
	return e
}
