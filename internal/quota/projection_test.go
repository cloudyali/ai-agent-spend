package quota

import (
	"strings"
	"testing"
	"time"
)

func TestSampleProject_BreachesByReset(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	// 90% of a weekly window used with only 24h left → on pace to hit the wall.
	s := Sample{Provider: "claude", Window: WindowWeekly, UsedPercent: 90, ResetsAt: now.Add(24 * time.Hour)}
	p := s.Project(now)
	if !p.Breaches {
		t.Fatalf("90%% weekly with 24h left should breach: %+v", p)
	}
	if p.ETASeconds <= 0 {
		t.Errorf("a future breach should carry a positive ETA, got %d", p.ETASeconds)
	}
}

func TestSampleProject_UsesWindowMinutesWhenSet(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	// 50% of a 5h window (window_minutes=300) with half the window left → projects
	// exactly to the limit at reset (borderline breach).
	s := Sample{Provider: "codex", Window: Window5h, UsedPercent: 50, WindowMinutes: 300, ResetsAt: now.Add(150 * time.Minute)}
	p := s.Project(now)
	if !p.Breaches {
		t.Errorf("50%% at the half-way point of the window should be on pace to breach: %+v", p)
	}
}

func TestSampleProject_UnderPaceDoesNotBreach(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	// 10% used, 24h left in the week (most of it elapsed) → nowhere near the wall.
	s := Sample{Provider: "claude", Window: WindowWeekly, UsedPercent: 10, ResetsAt: now.Add(24 * time.Hour)}
	if p := s.Project(now); p.Breaches {
		t.Errorf("10%% with the week nearly over should not breach: %+v", p)
	}
}

func TestSamplePaceNote_RunOutVsForecastVsEmpty(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)

	breaching := Sample{Window: WindowWeekly, UsedPercent: 90, ResetsAt: now.Add(24 * time.Hour)}
	if note := breaching.PaceNote(now); !strings.Contains(note, "on pace to run out") {
		t.Errorf("a breaching sample should warn about running out, got %q", note)
	}

	under := Sample{Window: WindowWeekly, UsedPercent: 10, ResetsAt: now.Add(24 * time.Hour)}
	if note := under.PaceNote(now); !strings.Contains(note, "on pace: ~") {
		t.Errorf("an under-pace sample should still forecast, got %q", note)
	}

	atWall := Sample{Window: WindowWeekly, UsedPercent: 100, ResetsAt: now.Add(24 * time.Hour)}
	if note := atWall.PaceNote(now); !strings.Contains(note, "limit reached") {
		t.Errorf("a maxed sample should say the limit is reached, got %q", note)
	}

	unknown := Sample{Window: Window("mystery"), UsedPercent: 10, ResetsAt: now.Add(time.Hour)}
	if note := unknown.PaceNote(now); note != "" {
		t.Errorf("an unknown window can't be extrapolated → no note, got %q", note)
	}
}
