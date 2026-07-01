package quota

import (
	"testing"
	"time"
)

func TestParseClaudeUsageAPI(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	raw := []byte(`{
		"five_hour":       {"utilization": 23, "resets_at": "2026-07-01T12:25:00Z"},
		"seven_day":       {"utilization": 40, "resets_at": 1782000000},
		"seven_day_opus":  {"utilization": 12, "resets_at": "2026-07-05T00:00:00Z"},
		"extra_usage":     {"is_enabled": true}
	}`)
	got := ParseClaudeUsageAPI(raw, now)
	if len(got) != 3 {
		t.Fatalf("want 3 windows, got %d: %+v", len(got), got)
	}
	var fh *Sample
	for i := range got {
		if got[i].Window == Window5h {
			fh = &got[i]
		}
	}
	if fh == nil || fh.UsedPercent != 23 || fh.Provider != "claude" {
		t.Fatalf("five_hour sample wrong: %+v", fh)
	}
	if !fh.ObservedAt.Equal(now) {
		t.Errorf("observedAt should be the fetch time, got %v", fh.ObservedAt)
	}
	if fh.ResetsAt.IsZero() {
		t.Errorf("five_hour should carry a reset instant")
	}
}

func TestParseClaudeUsageAPI_Sonnet(t *testing.T) {
	got := ParseClaudeUsageAPI([]byte(`{"seven_day_sonnet": {"utilization": 55, "resets_at": 1782000000}}`), time.Now())
	if len(got) != 1 || got[0].Window != WindowWeeklySonnet || got[0].UsedPercent != 55 {
		t.Fatalf("want one weekly-sonnet sample, got %+v", got)
	}
}

func TestParseClaudeUsageAPI_Garbage(t *testing.T) {
	if got := ParseClaudeUsageAPI([]byte("not json"), time.Now()); got != nil {
		t.Errorf("garbage should yield nil, got %+v", got)
	}
}

func TestParseCodexUsageAPI(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	raw := []byte(`{
		"plan_type": "pro",
		"rate_limit": {
			"primary_window":   {"used_percent": 1,  "reset_after_seconds": 17820, "limit_window_seconds": 18000},
			"secondary_window": {"used_percent": 60, "reset_at": 1782000000, "limit_window_seconds": 604800}
		}
	}`)
	got := ParseCodexUsageAPI(raw, now)
	if len(got) != 2 {
		t.Fatalf("want 2 windows, got %d: %+v", len(got), got)
	}
	var five, week *Sample
	for i := range got {
		switch got[i].Window {
		case Window5h:
			five = &got[i]
		case WindowWeekly:
			week = &got[i]
		}
	}
	if five == nil || five.UsedPercent != 1 || five.PlanType != "pro" {
		t.Fatalf("primary/5h wrong: %+v", five)
	}
	if !five.ResetsAt.Equal(now.Add(17820 * time.Second)) {
		t.Errorf("reset_after_seconds should be relative to observedAt, got %v", five.ResetsAt)
	}
	if week == nil || week.UsedPercent != 60 {
		t.Errorf("secondary/weekly wrong: %+v", week)
	}
}

func TestParseCodexUsageAPI_NoRateLimit(t *testing.T) {
	if got := ParseCodexUsageAPI([]byte(`{"plan_type":"pro"}`), time.Now()); got != nil {
		t.Errorf("no rate_limit block → nil, got %+v", got)
	}
}
