// Package quota models a provider's plan-limit window (the weekly/5-hour wall that
// disables work on Claude Code / Codex subscriptions) as a point-in-time REPORTED
// SNAPSHOT — deliberately separate from the priced evidence ledger in package event.
//
// The distinction is the whole design: the ledger is *computed from evidence* (every
// number opens to its source); a quota Sample is a reading lifted verbatim from a
// provider's local logs/cache and is never priced, never summed into a cost. It is
// rendered as its own gauge, always with its as-of, and degrades to "unknown" rather
// than guessing. See design-documents/10-session-explorer-budgets-quota.md.
//
// Offline by design: parsing reads bytes we already opened during scan — no net/*.
package quota

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Window names the limit window a Sample describes.
type Window string

const (
	// Window5h is the rolling 5-hour activity window (Codex "primary").
	Window5h Window = "5h"
	// WindowWeekly is the 7-day subscription window (Codex "secondary" / Claude
	// "seven_day").
	WindowWeekly Window = "weekly"
	// WindowWeeklyOpus is Claude's separate 7-day Opus cap ("seven_day_opus").
	WindowWeeklyOpus Window = "weekly-opus"
)

// Sample is a point-in-time plan-limit reading. It is NOT part of the ledger and
// carries no Money — only what the provider reported plus when we saw it.
type Sample struct {
	Provider      string
	Window        Window
	UsedPercent   float64
	WindowMinutes int
	ResetsAt      time.Time
	ObservedAt    time.Time
	Source        string // provenance, e.g. "codex:rate_limits.secondary"
}

// --- Codex parsing -------------------------------------------------------------

type cxWindow struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int     `json:"window_minutes"`
	// Reset is given as an absolute resets_at (epoch seconds or RFC3339 — the shape
	// real Codex writes, verified 2026-02) or, in older/doc shapes, a relative
	// resets_in_seconds. Both are accepted; resets_at wins.
	ResetsInSeconds *int64          `json:"resets_in_seconds"`
	ResetsAt        json.RawMessage `json:"resets_at"`
}

type cxLine struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Payload   struct {
		Type string `json:"type"`
		// rate_limits is null in exec mode and a thin {"limit_id":"codex"} on some
		// lines; a pointer lets all those cases fall out naturally.
		RateLimits *struct {
			Primary   *cxWindow `json:"primary"`
			Secondary *cxWindow `json:"secondary"`
		} `json:"rate_limits"`
	} `json:"payload"`
}

// ParseCodex extracts quota samples from one Codex rollout JSONL line. It returns nil
// when the line is not a token_count carrying a populated rate_limits block (exec-mode
// null, thin lines, other event types) or when the bytes don't parse — quota is a
// best-effort secondary signal that must never break a scan.
//
// The window kind is classified by window_minutes (weekly ≈ 10080, else the 5-hour
// window) rather than the primary/secondary slot: real Codex has been seen to put the
// weekly window in the primary slot with secondary null, so the slot name isn't
// trustworthy. The reset instant comes from resets_at (absolute) when present, else
// resets_in_seconds (relative to the line's timestamp).
func ParseCodex(raw []byte) []Sample {
	var l cxLine
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil
	}
	if l.Type != "event_msg" || l.Payload.Type != "token_count" || l.Payload.RateLimits == nil {
		return nil
	}
	var out []Sample
	add := func(w *cxWindow, slot string) {
		if w == nil {
			return
		}
		reset, ok := codexResetsAt(w, l.Timestamp)
		if !ok { // a window with no resolvable reset isn't a usable reading
			return
		}
		out = append(out, Sample{
			Provider:      "codex",
			Window:        codexWindowKind(w.WindowMinutes),
			UsedPercent:   w.UsedPercent,
			WindowMinutes: w.WindowMinutes,
			ResetsAt:      reset,
			ObservedAt:    l.Timestamp,
			Source:        "codex:rate_limits." + slot,
		})
	}
	add(l.Payload.RateLimits.Primary, "primary")
	add(l.Payload.RateLimits.Secondary, "secondary")
	return out
}

// codexWindowKind classifies a window by its length: a day or more is the weekly
// window; anything shorter is the 5-hour window.
func codexWindowKind(windowMinutes int) Window {
	if windowMinutes >= 1440 {
		return WindowWeekly
	}
	return Window5h
}

// codexResetsAt resolves a window's reset instant — the absolute resets_at (epoch or
// RFC3339) when present, else resets_in_seconds added to the line's timestamp. ok is
// false when neither is given.
func codexResetsAt(w *cxWindow, observedAt time.Time) (time.Time, bool) {
	if t, ok := parseResetsAt(w.ResetsAt); ok {
		return t, true
	}
	if w.ResetsInSeconds != nil {
		return observedAt.Add(time.Duration(*w.ResetsInSeconds) * time.Second), true
	}
	return time.Time{}, false
}

// --- Claude parsing ------------------------------------------------------------

type clWindow struct {
	UsedPercentage *float64        `json:"used_percentage"`
	Utilization    *float64        `json:"utilization"`
	ResetsAt       json.RawMessage `json:"resets_at"`
}

type clUsage struct {
	RateLimits *struct {
		FiveHour     *clWindow `json:"five_hour"`
		SevenDay     *clWindow `json:"seven_day"`
		SevenDayOpus *clWindow `json:"seven_day_opus"`
	} `json:"rate_limits"`
}

// ParseClaudeRateLimits extracts quota samples from a Claude Code usage snapshot (the
// documented rate_limits shape it emits to status lines and caches at
// ~/.claude/usage-exact.json). observedAt is when the snapshot was read — its
// freshness. Returns nil when there's no rate_limits block or the bytes don't parse.
//
// Quirks handled: seven_day_opus reports `utilization` where the others report
// `used_percentage`; resets_at is epoch seconds on some windows and an RFC3339 string
// on others; and a known Claude Code bug can put an epoch timestamp in used_percentage
// when a window has no data yet — out-of-range values are skipped, never shown as 100%.
func ParseClaudeRateLimits(raw []byte, observedAt time.Time) []Sample {
	var u clUsage
	if err := json.Unmarshal(raw, &u); err != nil || u.RateLimits == nil {
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
			Source:      "claude:rate_limits." + key,
		})
	}
	add(u.RateLimits.FiveHour, Window5h, "five_hour")
	add(u.RateLimits.SevenDay, WindowWeekly, "seven_day")
	add(u.RateLimits.SevenDayOpus, WindowWeeklyOpus, "seven_day_opus")
	return out
}

// claudeUsed reads a window's used percentage from either used_percentage or
// utilization; an out-of-range value (the epoch-as-percentage CC bug) is no data.
func claudeUsed(w *clWindow) (float64, bool) {
	v := w.UsedPercentage
	if v == nil {
		v = w.Utilization
	}
	if v == nil || *v < 0 || *v > 100 {
		return 0, false
	}
	return *v, true
}

// parseResetsAt accepts resets_at as epoch seconds (number) or an RFC3339 string,
// returning the absolute reset instant in UTC.
func parseResetsAt(raw json.RawMessage) (time.Time, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}, false
	}
	var secs float64
	if err := json.Unmarshal(raw, &secs); err == nil {
		return time.Unix(int64(secs), 0).UTC(), true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// --- Tracker -------------------------------------------------------------------

// Tracker reduces a stream of samples to the freshest reading per (provider, window).
//
// Not safe for concurrent use: scan feeds it from a single goroutine (records arrive
// in per-file order). If ingestion is ever parallelized, guard Observe/Active with a
// mutex — the internal map must not be written from multiple goroutines.
type Tracker struct {
	latest map[string]Sample
}

// NewTracker returns an empty Tracker.
func NewTracker() *Tracker { return &Tracker{latest: map[string]Sample{}} }

func trackerKey(provider string, w Window) string { return provider + "|" + string(w) }

// Observe records a sample, keeping the one with the latest ObservedAt for its
// (provider, window) so out-of-order records can't clobber a fresher reading. It is
// total: a zero-value Tracker (no NewTracker) is safe — the map is lazily created
// rather than nil-dereferenced.
func (t *Tracker) Observe(s Sample) {
	if t.latest == nil {
		t.latest = map[string]Sample{}
	}
	k := trackerKey(s.Provider, s.Window)
	if cur, ok := t.latest[k]; ok && cur.ObservedAt.After(s.ObservedAt) {
		return
	}
	t.latest[k] = s
}

// ObserveCodex parses a Codex line and observes any samples it carries (no-op when
// the line has none).
func (t *Tracker) ObserveCodex(raw []byte) {
	for _, s := range ParseCodex(raw) {
		t.Observe(s)
	}
}

// Active returns the still-valid samples (those whose reset is in the future at
// now), sorted by provider then window for deterministic rendering. A sample whose
// reset has already passed is stale — the window turned over since we saw it — so it
// is dropped rather than shown.
func (t *Tracker) Active(now time.Time) []Sample {
	out := make([]Sample, 0, len(t.latest))
	for _, s := range t.latest {
		if now.After(s.ResetsAt) {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Window < out[j].Window
	})
	return out
}

// --- Rendering (presentation-agnostic plain text; callers add color) ------------

// Bar renders an ASCII intensity bar ('#' filled, '-' empty) for pct in [0,100],
// clamping out-of-range input. Width <= 0 yields the empty string.
func Bar(pct float64, width int) string {
	if width <= 0 {
		return ""
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	fill := int(pct/100*float64(width) + 0.5)
	if fill > width {
		fill = width
	}
	if fill < 0 {
		fill = 0
	}
	return strings.Repeat("#", fill) + strings.Repeat("-", width-fill)
}

// HumanDuration renders a coarse, human countdown ("3d 4h", "52m", "now"). It drops
// the finer unit once a coarser one is present (days→hours, hours→minutes) so a
// glanceable gauge never reads "3d 4h 28m". Non-positive durations read "now".
func HumanDuration(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	days := int(d / (24 * time.Hour))
	hours := int((d % (24 * time.Hour)) / time.Hour)
	mins := int((d % time.Hour) / time.Minute)
	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case days > 0:
		return fmt.Sprintf("%dd", days)
	case hours > 0 && mins > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	case mins > 0:
		return fmt.Sprintf("%dm", mins)
	default:
		return "now"
	}
}

// Line renders the gauge body for a sample as of now: window label, intensity bar,
// percent, and reset countdown. Plain text only — color and block glyphs are the
// caller's job, keeping this package free of the ANSI layer.
func (s Sample) Line(now time.Time) string {
	return fmt.Sprintf("%-6s %s %3.0f%% · resets in %s",
		string(s.Window), Bar(s.UsedPercent, 10), s.UsedPercent, HumanDuration(s.ResetsAt.Sub(now)))
}

// Freshness renders the snapshot's age ("as of 2m ago") so a reported gauge never
// masquerades as live.
func (s Sample) Freshness(now time.Time) string {
	return "as of " + HumanDuration(now.Sub(s.ObservedAt)) + " ago"
}
