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
			t.Precision = clampInt(n, 0, 8)
		}
	}
	if name := strings.TrimSpace(secs["trailers.rename"]["cost"]); name != "" {
		t.CostName = name
	}
	return t, nil
}

func setBool(m map[string]string, key string, dst *bool) {
	switch strings.ToLower(strings.TrimSpace(m[key])) {
	case "true":
		*dst = true
	case "false":
		*dst = false
	}
}

func clampInt(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
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
