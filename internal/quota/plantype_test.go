package quota

import "testing"

func TestParseCodex_PlanType(t *testing.T) {
	// Codex's rate_limits block carries the account plan tier in `plan_type`.
	withPlan := `{"timestamp":"2026-06-19T11:59:00Z","type":"event_msg","payload":{"type":"token_count","info":{},"rate_limits":{"secondary":{"used_percent":42,"window_minutes":10080,"resets_in_seconds":200000},"plan_type":"pro"}}}`
	ss := ParseCodex([]byte(withPlan))
	if len(ss) == 0 {
		t.Fatal("expected a sample")
	}
	if ss[0].PlanType != "pro" {
		t.Errorf("PlanType = %q, want pro", ss[0].PlanType)
	}

	// A null plan_type (seen in exec mode) yields no tier — never a guess.
	nullPlan := `{"timestamp":"2026-06-19T11:59:00Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"secondary":{"used_percent":42,"window_minutes":10080,"resets_in_seconds":200000},"plan_type":null}}}`
	ss2 := ParseCodex([]byte(nullPlan))
	if len(ss2) == 0 || ss2[0].PlanType != "" {
		t.Errorf("null plan_type should be empty, got %+v", ss2)
	}
}
