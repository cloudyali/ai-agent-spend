package chain

import (
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/event"
)

func ev(id, promptID string, ts time.Time, micros int64, hasCost bool) event.AgentEvent {
	e := event.AgentEvent{EventID: id, PromptID: promptID, TSStart: ts, Model: "opus"}
	if hasCost {
		m := event.USD(micros)
		e.CostViews.APIEquivalent = &m
	}
	return e
}

func TestBuild_OrdersAndAccumulates(t *testing.T) {
	t0 := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	evs := []event.AgentEvent{
		ev("c", "p2", t0.Add(2*time.Minute), 300, true),
		ev("a", "p1", t0, 100, true),
		ev("b", "p1", t0.Add(time.Minute), 200, true),
	}
	ch := Build(evs)
	if len(ch.Turns) != 3 {
		t.Fatalf("turns = %d, want 3", len(ch.Turns))
	}
	if ch.Turns[0].EventID != "a" || ch.Turns[1].EventID != "b" || ch.Turns[2].EventID != "c" {
		t.Errorf("not chronological: %v %v %v", ch.Turns[0].EventID, ch.Turns[1].EventID, ch.Turns[2].EventID)
	}
	if ch.Turns[0].CumMicros != 100 || ch.Turns[1].CumMicros != 300 || ch.Turns[2].CumMicros != 600 {
		t.Errorf("cumulative gutter wrong: %d %d %d", ch.Turns[0].CumMicros, ch.Turns[1].CumMicros, ch.Turns[2].CumMicros)
	}
	if ch.TotalMicros != 600 {
		t.Errorf("total = %d, want 600", ch.TotalMicros)
	}
	if !ch.Confident {
		t.Error("all turns priced → chain should be confident")
	}
}

func TestBuild_GroupsByPromptFirstSeenOrder(t *testing.T) {
	t0 := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	evs := []event.AgentEvent{
		ev("a", "p1", t0, 100, true),
		ev("b", "p1", t0.Add(time.Minute), 200, true),
		ev("c", "p2", t0.Add(2*time.Minute), 300, true),
	}
	ch := Build(evs)
	if len(ch.Groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(ch.Groups))
	}
	if ch.Groups[0].PromptID != "p1" || len(ch.Groups[0].Turns) != 2 || ch.Groups[0].TotalMicros != 300 {
		t.Errorf("group0 = %+v", ch.Groups[0])
	}
	if ch.Groups[1].PromptID != "p2" || len(ch.Groups[1].Turns) != 1 {
		t.Errorf("group1 = %+v", ch.Groups[1])
	}
}

func TestBuild_NilCostFlagsLowConfidence(t *testing.T) {
	t0 := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	evs := []event.AgentEvent{
		ev("a", "p1", t0, 100, true),
		ev("b", "p1", t0.Add(time.Minute), 0, false), // APIEquivalent nil → not computable
	}
	ch := Build(evs)
	if ch.Confident {
		t.Error("a not-computable turn must drop chain confidence")
	}
	if ch.Turns[1].HasCost {
		t.Error("turn b should report HasCost=false")
	}
	if ch.Turns[1].CumMicros != 100 {
		t.Errorf("nil-cost turn contributes 0; cum = %d, want 100", ch.Turns[1].CumMicros)
	}
	if ch.TotalMicros != 100 {
		t.Errorf("total = %d, want 100", ch.TotalMicros)
	}
}

func TestBuild_EmptyPromptIDBucketsTogether(t *testing.T) {
	t0 := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	evs := []event.AgentEvent{
		ev("a", "", t0, 100, true),
		ev("b", "", t0.Add(time.Minute), 50, true),
	}
	ch := Build(evs)
	if len(ch.Groups) != 1 || ch.Groups[0].PromptID != "" {
		t.Fatalf("groups = %+v, want one ungrouped bucket", ch.Groups)
	}
	if len(ch.Groups[0].Turns) != 2 {
		t.Errorf("bucket turns = %d, want 2", len(ch.Groups[0].Turns))
	}
}

func TestBuild_Empty(t *testing.T) {
	ch := Build(nil)
	if len(ch.Turns) != 0 || len(ch.Groups) != 0 || ch.TotalMicros != 0 {
		t.Errorf("empty input should yield zero chain, got %+v", ch)
	}
	if !ch.Confident {
		t.Error("empty chain is vacuously confident")
	}
}

func TestBuild_StableTieBreakOnEqualTimestamp(t *testing.T) {
	t0 := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	evs := []event.AgentEvent{
		ev("b", "p1", t0, 10, true),
		ev("a", "p1", t0, 20, true), // same TS → deterministic tie-break by EventID
	}
	ch := Build(evs)
	if ch.Turns[0].EventID != "a" || ch.Turns[1].EventID != "b" {
		t.Errorf("equal-timestamp order not stable by id: %v, %v", ch.Turns[0].EventID, ch.Turns[1].EventID)
	}
}
