package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// SetDefaultPlan writes/replaces the `plan` key without disturbing other lines,
// and the result resolves to a subscription plan (fee seeded from the table).
func TestSetDefaultPlan(t *testing.T) {
	home := t.TempDir()
	start := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	// Fresh: creates config.toml and resolves to a subscription with a seeded fee
	// and the start date (the billing-cycle anchor) persisted.
	if err := SetDefaultPlan(home, "claude-max-20x", start); err != nil {
		t.Fatal(err)
	}
	p, err := LoadAppConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != "subscription" || p.Name != "claude-max-20x" || p.MonthlyFeeUSD <= 0 {
		t.Fatalf("expected a seeded subscription, got %+v", p)
	}
	if !p.StartDate.Equal(start) {
		t.Errorf("plan_start should be persisted, got %v want %v", p.StartDate, start)
	}

	// Replace in place, preserving an unrelated key.
	path := filepath.Join(home, "config.toml")
	if err := os.WriteFile(path, []byte("currency = \"USD\"\nplan = \"old-plan\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetDefaultPlan(home, "claude-max-20x", start); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	got := string(b)
	if !strings.Contains(got, "currency = \"USD\"") {
		t.Errorf("must preserve unrelated keys:\n%s", got)
	}
	if !strings.Contains(got, `plan = "claude-max-20x"`) || strings.Contains(got, "old-plan") {
		t.Errorf("must replace the plan in place:\n%s", got)
	}

	// Empty id → api / no subscription.
	if err := SetDefaultPlan(home, "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if p, _ := LoadAppConfig(home); p.Kind != "api" {
		t.Errorf("empty plan should resolve to api, got %+v", p)
	}
}

// Different providers each carry their own plan (one per provider), coexisting
// with the default — via <provider>_plan / <provider>_plan_start keys.
func TestSetProviderPlan_MultiProvider(t *testing.T) {
	home := t.TempDir()
	start := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	if err := SetProviderPlan(home, "", "claude-max-20x", start); err != nil { // default
		t.Fatal(err)
	}
	if err := SetProviderPlan(home, "codex", "chatgpt-pro", start); err != nil { // per-provider
		t.Fatal(err)
	}

	set, err := LoadPlanSet(home)
	if err != nil {
		t.Fatal(err)
	}
	if set.Default.Name != "claude-max-20x" || set.Default.Kind != "subscription" {
		t.Errorf("default plan wrong: %+v", set.Default)
	}
	cx, ok := set.ByProvider["codex"]
	if !ok || cx.Name != "chatgpt-pro" || cx.Kind != "subscription" || cx.MonthlyFeeUSD <= 0 || !cx.StartDate.Equal(start) {
		t.Errorf("codex plan wrong: %+v (ok=%v)", cx, ok)
	}
	// a provider without its own plan falls back to the default
	if set.For("claude_code").Name != "claude-max-20x" {
		t.Errorf("claude_code should fall back to default, got %+v", set.For("claude_code"))
	}
	if set.For("codex").Name != "chatgpt-pro" {
		t.Errorf("codex should use its own plan, got %+v", set.For("codex"))
	}
}

func TestParseTOML(t *testing.T) {
	in := []byte(`
# a comment
project   = "payments-service"
cost_tag  = 'team-payments'
env       = "prod"
monthly_fee_usd = 200   # inline comment
enabled = true
[section]
ignored = "yes"
`)
	m, err := parseTOML(in)
	if err != nil {
		t.Fatal(err)
	}
	if m["project"] != "payments-service" || m["cost_tag"] != "team-payments" || m["env"] != "prod" {
		t.Errorf("strings wrong: %v", m)
	}
	if m["monthly_fee_usd"] != "200" {
		t.Errorf("inline comment not stripped: %q", m["monthly_fee_usd"])
	}
	if m["enabled"] != "true" {
		t.Errorf("bool: %q", m["enabled"])
	}
}

func TestLoadRepo_NearestAncestor(t *testing.T) {
	root := t.TempDir()
	// .aispend.toml at <root>/repo, event cwd at <root>/repo/sub/dir
	repoDir := filepath.Join(root, "repo")
	deep := filepath.Join(repoDir, "sub", "dir")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "project = \"payments\"\ncost_tag = \"team-payments\"\nenv = \"prod\"\n"
	if err := os.WriteFile(filepath.Join(repoDir, ".aispend.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}

	r, found, err := LoadRepo(deep)
	if err != nil || !found {
		t.Fatalf("expected to find config: found=%v err=%v", found, err)
	}
	if r.Project != "payments" || r.CostTag != "team-payments" || r.Env != "prod" {
		t.Errorf("repo config = %+v", r)
	}

	// a directory with no .aispend.toml anywhere up to its temp root
	if _, found, _ := LoadRepo(t.TempDir()); found {
		t.Error("expected no config in a clean tree")
	}
}

func TestParseTOML_Errors(t *testing.T) {
	if _, err := parseTOML([]byte("novalue\n")); err == nil {
		t.Error("want error for a line without '='")
	}
	if _, err := parseTOML([]byte("= x\n")); err == nil {
		t.Error("want error for an empty key")
	}
}

func TestParseTOML_QuotedHashPreserved(t *testing.T) {
	m, err := parseTOML([]byte("cost_tag = \"team#1\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m["cost_tag"] != "team#1" {
		t.Errorf("'#' inside quotes mangled: %q", m["cost_tag"])
	}
}

func TestLoadRepo_ReadErrorPropagates(t *testing.T) {
	dir := t.TempDir()
	// a directory named .aispend.toml makes ReadFile fail with a non-not-exist error
	if err := os.MkdirAll(filepath.Join(dir, ".aispend.toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadRepo(dir); err == nil {
		t.Error("want error when .aispend.toml is unreadable")
	}
}

func TestLoadAppConfig_BadFee(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("monthly_fee_usd = notanumber\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAppConfig(home); err == nil {
		t.Error("want error for an unparseable monthly_fee_usd")
	}
}

func TestLoadAppConfig_Plan(t *testing.T) {
	t.Run("subscription with fee", func(t *testing.T) {
		home := t.TempDir()
		cfg := "plan = \"max\"\nmonthly_fee_usd = 200\ncurrency = \"USD\"\n"
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		p, err := LoadAppConfig(home)
		if err != nil {
			t.Fatal(err)
		}
		if p.Kind != "subscription" || p.MonthlyFeeUSD != 200 || p.Name != "max" || p.Currency != "USD" {
			t.Errorf("plan = %+v", p)
		}
	})
	t.Run("missing file defaults to api", func(t *testing.T) {
		p, err := LoadAppConfig(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if p.Kind != "api" {
			t.Errorf("default plan kind = %q, want api", p.Kind)
		}
	})
	t.Run("seeded plan resolves fee without monthly_fee_usd", func(t *testing.T) {
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("plan = \"claude-max-20x\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		p, err := LoadAppConfig(home)
		if err != nil {
			t.Fatal(err)
		}
		if p.Kind != "subscription" || p.MonthlyFeeUSD != 200 {
			t.Errorf("seeded plan = %+v, want subscription $200", p)
		}
	})
	t.Run("monthly_fee_usd overrides the seeded fee", func(t *testing.T) {
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("plan = \"claude-max-20x\"\nmonthly_fee_usd = 150\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		p, _ := LoadAppConfig(home)
		if p.MonthlyFeeUSD != 150 {
			t.Errorf("override = %v, want 150", p.MonthlyFeeUSD)
		}
	})
}

func TestLoadPlanSet_PerProvider(t *testing.T) {
	home := t.TempDir()
	cfg := "plan = \"claude-max-20x\"\ncodex_plan = \"chatgpt-pro\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := LoadPlanSet(home)
	if err != nil {
		t.Fatal(err)
	}
	// claude_code (no override) → default
	if p := set.For("claude_code"); p.Name != "claude-max-20x" || p.MonthlyFeeUSD != 200 {
		t.Errorf("claude_code plan = %+v, want claude-max-20x $200", p)
	}
	// codex → its override
	if p := set.For("codex"); p.Name != "chatgpt-pro" || p.MonthlyFeeUSD != 200 || p.Kind != "subscription" {
		t.Errorf("codex plan = %+v, want chatgpt-pro $200", p)
	}
	// unknown provider → default
	if set.For("cursor").Name != "claude-max-20x" {
		t.Errorf("unknown provider should fall back to default, got %+v", set.For("cursor"))
	}
}

func TestLoadAppConfig_PlanStart(t *testing.T) {
	t.Run("parses plan_start into StartDate", func(t *testing.T) {
		home := t.TempDir()
		cfg := "plan = \"max\"\nmonthly_fee_usd = 200\nplan_start = \"2026-06-12\"\n"
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		p, err := LoadAppConfig(home)
		if err != nil {
			t.Fatal(err)
		}
		want := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
		if !p.StartDate.Equal(want) {
			t.Errorf("StartDate = %v, want %v", p.StartDate, want)
		}
	})

	t.Run("absent plan_start leaves a zero StartDate", func(t *testing.T) {
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("plan = \"max\"\nmonthly_fee_usd = 200\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		p, err := LoadAppConfig(home)
		if err != nil {
			t.Fatal(err)
		}
		if !p.StartDate.IsZero() {
			t.Errorf("StartDate = %v, want zero", p.StartDate)
		}
	})

	t.Run("malformed plan_start is an error", func(t *testing.T) {
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("plan = \"max\"\nplan_start = \"06/12/2026\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadAppConfig(home); err == nil {
			t.Error("expected an error for a non-YYYY-MM-DD plan_start")
		}
	})
}

func TestLoadPlanSet_PerProviderStart(t *testing.T) {
	home := t.TempDir()
	cfg := "plan = \"claude-max-20x\"\nplan_start = \"2026-06-01\"\n" +
		"codex_plan = \"chatgpt-pro\"\ncodex_plan_start = \"2026-06-10\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := LoadPlanSet(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := set.For("claude_code").StartDate; !got.Equal(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("default StartDate = %v, want 2026-06-01", got)
	}
	if got := set.For("codex").StartDate; !got.Equal(time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("codex StartDate = %v, want 2026-06-10", got)
	}
}

func TestPlans(t *testing.T) {
	ps := Plans()
	if len(ps) < 5 {
		t.Fatalf("expected several seeded plans, got %d", len(ps))
	}
	// sorted by id, and the known Max 20x is $200
	var found bool
	for i, p := range ps {
		if i > 0 && ps[i-1].ID > p.ID {
			t.Error("plans not sorted by id")
		}
		if p.ID == "claude-max-20x" {
			found = true
			if p.MonthlyFeeUSD != 200 || p.Provider != "anthropic" {
				t.Errorf("max-20x = %+v", p)
			}
		}
	}
	if !found {
		t.Error("claude-max-20x not in seeded plans")
	}
}
