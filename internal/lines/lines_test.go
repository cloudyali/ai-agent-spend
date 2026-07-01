package lines

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func pf(v float64) *float64 { return &v }

// --- Project: pace / forecast math --------------------------------------------

func TestProject_UnderPace(t *testing.T) {
	// 42% used, half the week elapsed → projects ~84% by reset, no breach.
	p := Project(42, 100, 84*time.Hour, 168*time.Hour)
	if p.Breaches {
		t.Errorf("42%% at half-window should not breach: %+v", p)
	}
	if p.ProjectedUsed < 83.9 || p.ProjectedUsed > 84.1 {
		t.Errorf("projectedUsed = %.2f, want ~84", p.ProjectedUsed)
	}
}

func TestProject_BreachesBeforeReset(t *testing.T) {
	// 60% used at half-window → projects 120%, will cross 100% before reset.
	p := Project(60, 100, 84*time.Hour, 168*time.Hour)
	if !p.Breaches {
		t.Fatalf("60%% at half-window should breach: %+v", p)
	}
	if p.ProjectedUsed < 119 || p.ProjectedUsed > 121 {
		t.Errorf("projectedUsed = %.2f, want ~120", p.ProjectedUsed)
	}
	// ETA to 100%: (100-60) / (60/84h) = 56h.
	wantETA := int64((56 * time.Hour).Seconds())
	if p.ETASeconds < wantETA-120 || p.ETASeconds > wantETA+120 {
		t.Errorf("etaSeconds = %d, want ~%d", p.ETASeconds, wantETA)
	}
}

func TestProject_NoUsageNeverBreaches(t *testing.T) {
	p := Project(0, 100, 10*time.Hour, 168*time.Hour)
	if p.Breaches || p.ProjectedUsed != 0 {
		t.Errorf("zero usage should never breach: %+v", p)
	}
}

func TestProject_ZeroElapsedIsSafe(t *testing.T) {
	// No elapsed time → we cannot extrapolate; must not divide-by-zero or guess a breach.
	p := Project(42, 100, 0, 168*time.Hour)
	if p.Breaches {
		t.Errorf("zero elapsed must not breach: %+v", p)
	}
}

func TestProject_AlreadyOverLimit(t *testing.T) {
	p := Project(150, 100, 84*time.Hour, 168*time.Hour)
	if !p.Breaches {
		t.Errorf("already over limit should report breach: %+v", p)
	}
	if p.ETASeconds != 0 {
		t.Errorf("already-breached ETA should be 0 (now), got %d", p.ETASeconds)
	}
}

// --- Snapshot / Line serialization (OpenUsage-superset contract) --------------

func TestSnapshotJSON_OpenUsageSupersetKeys(t *testing.T) {
	reset := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	snap := Snapshot{
		ProviderID:  "codex",
		DisplayName: "Codex",
		Plan:        "Plus",
		FetchedAt:   time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC),
		Lines: []Line{{
			Type:       "progress",
			Label:      "Weekly",
			Used:       pf(63),
			Limit:      pf(100),
			Format:     &Format{Kind: Percent},
			ResetsAt:   &reset,
			PeriodMs:   int64((168 * time.Hour) / time.Millisecond),
			Projection: &Projection{ProjectedUsed: 84, Breaches: false},
		}},
	}
	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		`"providerId":"codex"`, `"displayName":"Codex"`, `"plan":"Plus"`,
		`"type":"progress"`, `"format":{"kind":"percent"}`,
		`"periodDurationMs":604800000`, `"resetsAt":"2026-06-21T09:00:00Z"`,
		`"used":63`, `"projection":`, `"fetchedAt":`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("snapshot JSON missing %s:\n%s", want, s)
		}
	}
	var back Snapshot
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.ProviderID != "codex" || len(back.Lines) != 1 || back.Lines[0].Used == nil || *back.Lines[0].Used != 63 {
		t.Errorf("round-trip mismatch: %+v", back)
	}
}

func TestTextLine_OmitsProgressOnlyFields(t *testing.T) {
	b, _ := json.Marshal(Line{Type: "text", Label: "Today", Value: "$5.17 · 9.2M tokens"})
	s := string(b)
	for _, bad := range []string{`"used"`, `"limit"`, `"format"`, `"resetsAt"`, `"projection"`} {
		if strings.Contains(s, bad) {
			t.Errorf("text line should omit %s: %s", bad, s)
		}
	}
	if !strings.Contains(s, `"value":"$5.17 · 9.2M tokens"`) {
		t.Errorf("text line should carry value: %s", s)
	}
}
