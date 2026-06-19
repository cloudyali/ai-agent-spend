package budget

import (
	"testing"
	"time"
)

func TestComputePace_OnTrack(t *testing.T) {
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC) // ~half the month
	// half the budget spent at half the month → run-rate lands on budget.
	p := ComputePace(500_000_000, 250_000_000, start, now, end)
	if p.OverPace() {
		t.Errorf("half spent at half the month should not project over: %+v", p)
	}
	if got := p.Status(); got != "on track" {
		t.Errorf("status = %q, want on track", got)
	}
}

func TestComputePace_OverPace(t *testing.T) {
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC) // 6/30 ≈ 20% elapsed
	// 43% spent at 20% elapsed → run-rate ≈ 2.15× the budget.
	p := ComputePace(500_000_000, 215_000_000, start, now, end)
	if !p.OverPace() {
		t.Fatalf("43%% spent at 20%% elapsed should project over budget: %+v", p)
	}
	if r := p.PaceRatio(); r < 2.0 || r > 2.3 {
		t.Errorf("pace ratio = %.2f, want ≈2.15", r)
	}
	if got := p.Status(); got == "on track" || got == "under" {
		t.Errorf("status = %q, want an over-pace verdict", got)
	}
}

func TestComputePace_Under(t *testing.T) {
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC) // 20/30 ≈ 67% elapsed
	p := ComputePace(500_000_000, 100_000_000, start, now, end)
	if got := p.Status(); got != "under" {
		t.Errorf("20%% spent at 67%% elapsed should read under, got %q", got)
	}
}

func TestUsedFraction(t *testing.T) {
	if got := (Pace{Spent: 250_000_000, Limit: 500_000_000}).UsedFraction(); got != 0.5 {
		t.Errorf("UsedFraction = %v, want 0.5", got)
	}
	if got := (Pace{Spent: 1, Limit: 0}).UsedFraction(); got != 0 {
		t.Errorf("no budget → UsedFraction 0, got %v", got)
	}
}

func TestComputePace_Edges(t *testing.T) {
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// before the period → 0 elapsed → projection degrades to spend (no blow-up).
	before := ComputePace(500_000_000, 40_000_000, start, start.AddDate(0, 0, -1), end)
	if before.ElapsedFraction != 0 || before.Projected != 40_000_000 {
		t.Errorf("pre-start → 0 elapsed, projected=spend; got %+v", before)
	}
	// past the end → elapsed clamps to 1.
	after := ComputePace(500_000_000, 600_000_000, start, end.AddDate(0, 0, 1), end)
	if after.ElapsedFraction != 1 {
		t.Errorf("post-end → elapsed clamps to 1, got %v", after.ElapsedFraction)
	}
	// zero-length period → projected=spend (no divide-by-zero).
	if z := ComputePace(500_000_000, 10_000_000, start, start, start); z.Projected != 10_000_000 {
		t.Errorf("zero-length period → projected=spend, got %d", z.Projected)
	}
}

func TestMonthBounds(t *testing.T) {
	now := time.Date(2026, 6, 16, 14, 30, 0, 0, time.UTC)
	s, e := MonthBounds(now)
	if !s.Equal(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("month start = %v, want Jun 1", s)
	}
	if !e.Equal(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("month end = %v, want Jul 1", e)
	}
}
