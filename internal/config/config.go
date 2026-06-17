// Package config loads .aispend.toml (repo-level attribution: project, cost_tag,
// env) and ~/.aispend/config.toml (plan configuration). To keep the binary
// dependency-free, it parses the small, flat config subset AgentSpend defines
// itself rather than pulling a full TOML library. Credentials are never read or
// written here and never logged.
//
// See design-documents/02-data-model.md §5 and
// design-documents/phase-0A-trusted-explainable-ledger.md §Attribution.
package config

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Repo is the attribution from a repo's .aispend.toml.
type Repo struct {
	Project string
	CostTag string
	Env     string
}

// Plan is the billing configuration from ~/.aispend/config.toml.
type Plan struct {
	Name          string  // free-form label, e.g. "max"
	Kind          string  // "subscription" | "api"
	MonthlyFeeUSD float64 // for subscription amortization
	Currency      string
	StartDate     time.Time // subscription start; its day-of-month is the billing anchor (zero = unknown)
}

// planStartLayout is the date format accepted for `plan_start` keys — the same
// YYYY-MM-DD the `--since`/`--until` report flags use.
const planStartLayout = "2006-01-02"

// LoadRepo walks up from startDir to find the nearest .aispend.toml.
func LoadRepo(startDir string) (Repo, bool, error) {
	dir := startDir
	for {
		b, err := os.ReadFile(filepath.Join(dir, ".aispend.toml"))
		if err == nil {
			m, perr := parseTOML(b)
			if perr != nil {
				return Repo{}, false, fmt.Errorf("config: %s/.aispend.toml: %w", dir, perr)
			}
			return Repo{Project: m["project"], CostTag: m["cost_tag"], Env: m["env"]}, true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return Repo{}, false, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Repo{}, false, nil // reached filesystem root
		}
		dir = parent
	}
}

// PlanSet maps providers to subscription plans, with a default. This is how a
// developer with two subscriptions (e.g. Claude Max + ChatGPT Pro) expresses that
// Claude Code usage bills against one plan and Codex against another.
type PlanSet struct {
	Default    Plan
	ByProvider map[string]Plan
}

// For returns the plan covering a provider — its specific plan if set, else the default.
func (s PlanSet) For(provider string) Plan {
	if p, ok := s.ByProvider[provider]; ok {
		return p
	}
	return s.Default
}

// LoadAppConfig returns the default plan from ~/.aispend/config.toml (back-compat).
func LoadAppConfig(appHome string) (Plan, error) {
	m, err := loadConfigMap(appHome)
	if err != nil {
		return Plan{}, err
	}
	return resolvePlan(m["plan"], m["monthly_fee_usd"], m["currency"], m["plan_start"])
}

// LoadPlanSet reads the default plan (`plan`/`monthly_fee_usd`) plus any
// per-provider overrides (`<provider>_plan`, e.g. `codex_plan = "chatgpt-pro"`).
func LoadPlanSet(appHome string) (PlanSet, error) {
	m, err := loadConfigMap(appHome)
	if err != nil {
		return PlanSet{}, err
	}
	def, err := resolvePlan(m["plan"], m["monthly_fee_usd"], m["currency"], m["plan_start"])
	if err != nil {
		return PlanSet{}, err
	}
	set := PlanSet{Default: def, ByProvider: map[string]Plan{}}
	for k, v := range m {
		prov, ok := strings.CutSuffix(k, "_plan")
		if !ok || prov == "" {
			continue
		}
		p, err := resolvePlan(v, m[prov+"_monthly_fee_usd"], m[prov+"_currency"], m[prov+"_plan_start"])
		if err != nil {
			return PlanSet{}, err
		}
		set.ByProvider[prov] = p
	}
	return set, nil
}

func loadConfigMap(appHome string) (map[string]string, error) {
	b, err := os.ReadFile(filepath.Join(appHome, "config.toml"))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	m, err := parseTOML(b)
	if err != nil {
		return nil, fmt.Errorf("config: config.toml: %w", err)
	}
	return m, nil
}

// resolvePlan turns a plan name + optional fee/currency into a Plan, seeding the
// fee from the known-plans table when the user named a plan but didn't override it.
func resolvePlan(name, feeStr, currency, startStr string) (Plan, error) {
	p := Plan{Name: name, Currency: currency}
	if p.Currency == "" {
		p.Currency = "USD"
	}
	if feeStr != "" {
		f, err := strconv.ParseFloat(feeStr, 64)
		if err != nil {
			return Plan{}, fmt.Errorf("config: monthly_fee_usd %q: %w", feeStr, err)
		}
		p.MonthlyFeeUSD = f
	}
	if startStr != "" {
		t, err := time.Parse(planStartLayout, startStr)
		if err != nil {
			return Plan{}, fmt.Errorf("config: plan_start %q (want YYYY-MM-DD): %w", startStr, err)
		}
		p.StartDate = t
	}
	if p.MonthlyFeeUSD == 0 && name != "" {
		if sp, ok := seededPlans()[name]; ok {
			p.MonthlyFeeUSD = sp.MonthlyFeeUSD
			if sp.Currency != "" {
				p.Currency = sp.Currency
			}
		}
	}
	if p.MonthlyFeeUSD > 0 || (name != "" && name != "api") {
		p.Kind = "subscription"
	} else {
		p.Kind = "api"
	}
	return p, nil
}

// SetDefaultPlan writes the default `plan = "<id>"` into ~/.aispend/config.toml,
// preserving every other line. For a real subscription it also writes
// `plan_start` (the billing-cycle anchor used for monthly-cycle amortization) when
// a non-zero start is given. An empty/"api" id sets `plan = ""`-equivalent (no
// subscription) and leaves the date untouched. Backs the interactive plan picker
// so a user never has to hand-edit TOML.
func SetDefaultPlan(appHome, planID string, start time.Time) error {
	if err := setConfigKey(appHome, "plan", strconv.Quote(planID)); err != nil {
		return err
	}
	if planID != "" && planID != "api" && !start.IsZero() {
		return setConfigKey(appHome, "plan_start", strconv.Quote(start.Format(planStartLayout)))
	}
	return nil
}

// setConfigKey sets one flat top-level key in config.toml (replacing it in place if
// present, else appending), leaving all other content untouched. rawValue is the
// already-formatted right-hand side (e.g. a quoted string).
func setConfigKey(appHome, key, rawValue string) error {
	path := filepath.Join(appHome, "config.toml")
	var lines []string
	if b, err := os.ReadFile(path); err == nil {
		if s := strings.TrimRight(string(b), "\n"); s != "" {
			lines = strings.Split(s, "\n")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	newLine := key + " = " + rawValue
	found := false
	for i, ln := range lines {
		if k, _, ok := strings.Cut(ln, "="); ok && strings.TrimSpace(k) == key {
			lines[i] = newLine
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, newLine)
	}
	if err := os.MkdirAll(appHome, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

// SeededPlan is a known subscription plan with a default monthly price.
type SeededPlan struct {
	ID            string  `json:"-"`
	Provider      string  `json:"provider"`
	MonthlyFeeUSD float64 `json:"monthly_fee_usd"`
	Label         string  `json:"label"`
	Currency      string  `json:"-"`
}

//go:embed plans.json
var plansJSON []byte

var plansCache struct {
	sync.Once
	m map[string]SeededPlan
}

func seededPlans() map[string]SeededPlan {
	plansCache.Do(func() {
		var t struct {
			Currency string                `json:"currency"`
			Plans    map[string]SeededPlan `json:"plans"`
		}
		if err := json.Unmarshal(plansJSON, &t); err != nil {
			panic("config: embedded plans.json is corrupt: " + err.Error())
		}
		for id, sp := range t.Plans {
			sp.ID = id
			sp.Currency = t.Currency
			t.Plans[id] = sp
		}
		plansCache.m = t.Plans
	})
	return plansCache.m
}

// Plans returns the seeded subscription plans, sorted by id — for `aispend plans`.
func Plans() []SeededPlan {
	m := seededPlans()
	out := make([]SeededPlan, 0, len(m))
	for _, sp := range m {
		out = append(out, sp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// parseTOML parses the flat config subset AgentSpend uses: blank lines and
// # comments are ignored, [section] headers are skipped (our files are flat), and
// each `key = value` yields a string (quotes stripped; inline # comments removed
// on unquoted values).
func parseTOML(data []byte) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			return nil, fmt.Errorf("invalid line %q (want key = value)", line)
		}
		key := strings.TrimSpace(line[:eq])
		val := unquote(stripInlineComment(strings.TrimSpace(line[eq+1:])))
		if key == "" {
			return nil, fmt.Errorf("empty key in %q", line)
		}
		out[key] = val
	}
	return out, sc.Err()
}

// stripInlineComment removes a trailing # comment, but not inside a quoted value.
func stripInlineComment(v string) string {
	if len(v) > 0 && (v[0] == '"' || v[0] == '\'') {
		if i := strings.IndexByte(v[1:], v[0]); i >= 0 {
			return v[:i+2] // keep through the closing quote
		}
		return v
	}
	if i := strings.IndexByte(v, '#'); i >= 0 {
		return strings.TrimSpace(v[:i])
	}
	return v
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}
