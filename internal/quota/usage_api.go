package quota

import (
	"encoding/json"
	"time"
)

// This file maps the providers' authenticated usage APIs (what OpenUsage reads after
// loading the local OAuth token) into the same Sample the on-disk snapshots produce.
// Parsing only — the network fetch that supplies `raw` lives in the net-gated package
// internal/quotaonline, so package quota stays net-free and offline-buildable.

// clUsageAPI is the Anthropic OAuth usage response (GET /api/oauth/usage): the same
// window objects as the on-disk snapshot, but at the top level (no rate_limits wrapper).
type clUsageAPI struct {
	FiveHour       *clWindow `json:"five_hour"`
	SevenDay       *clWindow `json:"seven_day"`
	SevenDayOpus   *clWindow `json:"seven_day_opus"`
	SevenDaySonnet *clWindow `json:"seven_day_sonnet"`
}

// ParseClaudeUsageAPI maps the Anthropic OAuth usage response to samples, reusing the
// same window handling (used_percentage/utilization, epoch/RFC3339 reset) as the on-disk
// snapshot. observedAt is when the response was fetched. Best-effort: unparseable bytes,
// or a window without a usable reading, are skipped rather than guessed.
func ParseClaudeUsageAPI(raw []byte, observedAt time.Time) []Sample {
	var u clUsageAPI
	if err := json.Unmarshal(raw, &u); err != nil {
		return nil
	}
	var out []Sample
	add := func(w *clWindow, win Window, key string) {
		if w == nil {
			return
		}
		used, ok := claudeUsed(w)
		if !ok {
			return
		}
		reset, ok := parseResetsAt(w.ResetsAt)
		if !ok {
			return
		}
		out = append(out, Sample{
			Provider:    "claude",
			Window:      win,
			UsedPercent: used,
			ResetsAt:    reset,
			ObservedAt:  observedAt,
			Source:      "claude:oauth_usage." + key,
		})
	}
	add(u.FiveHour, Window5h, "five_hour")
	add(u.SevenDay, WindowWeekly, "seven_day")
	add(u.SevenDayOpus, WindowWeeklyOpus, "seven_day_opus")
	add(u.SevenDaySonnet, WindowWeeklySonnet, "seven_day_sonnet")
	return out
}

// cxAPIWindow is one window in the Codex (ChatGPT backend) usage response rate_limit
// block: used_percent plus either an absolute reset_at or a relative reset_after_seconds,
// and the window length in seconds.
type cxAPIWindow struct {
	UsedPercent        float64         `json:"used_percent"`
	ResetAt            json.RawMessage `json:"reset_at"`
	ResetAfterSeconds  *int64          `json:"reset_after_seconds"`
	LimitWindowSeconds int             `json:"limit_window_seconds"`
}

type cxUsageAPI struct {
	RateLimit *struct {
		Primary   *cxAPIWindow `json:"primary_window"`
		Secondary *cxAPIWindow `json:"secondary_window"`
	} `json:"rate_limit"`
	PlanType *string `json:"plan_type"`
}

// ParseCodexUsageAPI maps the Codex usage response (GET wham/usage) to samples. Windows
// are classified by limit_window_seconds (weekly when ≥ a day, else the 5-hour window),
// matching the rollout-log classifier so both sources land in the same slots. observedAt
// is when it was fetched. Best-effort: no rate_limit block, or a window without a usable
// reset, yields nothing.
func ParseCodexUsageAPI(raw []byte, observedAt time.Time) []Sample {
	var u cxUsageAPI
	if err := json.Unmarshal(raw, &u); err != nil || u.RateLimit == nil {
		return nil
	}
	planType := ""
	if u.PlanType != nil {
		planType = *u.PlanType
	}
	var out []Sample
	add := func(w *cxAPIWindow, slot string) {
		if w == nil {
			return
		}
		reset, ok := codexAPIResetsAt(w, observedAt)
		if !ok {
			return
		}
		out = append(out, Sample{
			Provider:      "codex",
			Window:        codexWindowKind(w.LimitWindowSeconds / 60),
			UsedPercent:   w.UsedPercent,
			WindowMinutes: w.LimitWindowSeconds / 60,
			ResetsAt:      reset,
			ObservedAt:    observedAt,
			Source:        "codex:wham_usage." + slot,
			PlanType:      planType,
		})
	}
	add(u.RateLimit.Primary, "primary_window")
	add(u.RateLimit.Secondary, "secondary_window")
	return out
}

// codexAPIResetsAt resolves a window's reset instant: the absolute reset_at (epoch or
// RFC3339) when present, else reset_after_seconds added to observedAt.
func codexAPIResetsAt(w *cxAPIWindow, observedAt time.Time) (time.Time, bool) {
	if t, ok := parseResetsAt(w.ResetAt); ok {
		return t, true
	}
	if w.ResetAfterSeconds != nil {
		return observedAt.Add(time.Duration(*w.ResetAfterSeconds) * time.Second), true
	}
	return time.Time{}, false
}
