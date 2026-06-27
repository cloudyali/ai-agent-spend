package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/pricing"
)

// The header carries a freshness stamp — how long ago the ledger was last synced (the
// incremental-scan watermark) — so the user can see the in-process sync is alive.
func TestModel_SyncStatus_ShowsAge(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine()).WithNow(now).
		WithSyncStatus(func() time.Time { return now.Add(-5 * time.Minute) })
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = nm.(Model)
	if v := m.View(); !strings.Contains(v, "synced 5m ago") {
		t.Errorf("header should show the last-sync age:\n%s", v)
	}
}

// A zero sync time (never scanned) renders no "synced" segment rather than a bogus age.
func TestModel_SyncStatus_ZeroHidden(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine()).WithNow(now).
		WithSyncStatus(func() time.Time { return time.Time{} })
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = nm.(Model)
	if strings.Contains(m.View(), "synced") {
		t.Errorf("zero sync time → no synced segment:\n%s", m.View())
	}
}

// Without WithSyncStatus the header stays exactly as before — no "synced" segment.
func TestModel_SyncStatus_OffByDefault(t *testing.T) {
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine())
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = nm.(Model)
	if strings.Contains(m.View(), "synced") {
		t.Errorf("no sync status wired → no synced segment:\n%s", m.View())
	}
}

// The freshness stamp must AGE between syncs: a cheap clock heartbeat advances the model's clock
// and repaints WITHOUT re-scanning, so "synced …" grows minute by minute and only resets when a
// real sync moves the watermark. Regression for the "perpetual just now" bug — the clock used to
// advance only on the 15m sync tick, which also reset the watermark, so the age never grew.
func TestModel_ClockTickAgesSyncStamp(t *testing.T) {
	base := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	nowv := base
	synced := base // last sync == base, so the stamp starts at "just now"
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine()).WithNow(base).
		WithSyncStatus(func() time.Time { return synced }).
		WithWatch(0, func() time.Time { return nowv }, nil). // nowFn wired; no reload tick
		WithClockTick(30 * time.Second)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = nm.(Model)
	if v := m.View(); !strings.Contains(v, "synced just now") {
		t.Fatalf("at t0 the stamp should read just now:\n%s", v)
	}

	// 5 minutes pass with NO sync (watermark unchanged); a clock heartbeat advances the clock.
	nowv = base.Add(5 * time.Minute)
	nm, cmd := m.Update(clockMsg(nowv))
	if cmd == nil {
		t.Fatal("the clock heartbeat should re-arm itself")
	}
	m = nm.(Model)
	if v := m.View(); !strings.Contains(v, "synced 5m ago") {
		t.Errorf("after 5m with no sync the stamp should read 5m ago, not a frozen just now:\n%s", v)
	}
}

// The clock heartbeat is off by default (zero interval) → no command scheduled.
func TestModel_ClockTick_OffByDefault(t *testing.T) {
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine())
	if m.clockCmd() != nil {
		t.Error("no clock interval wired → clockCmd should schedule nothing")
	}
}

// A completed in-process sync must refresh the explorer in place: the watch tick drives the
// reload (the sync) off the UI loop, and folding its result back re-reads BOTH the data and the
// freshness stamp and re-renders — so newly-imported turns appear and "synced …" snaps to "just
// now" the moment the ledger is brought current, never a stale screen waiting out the cadence.
func TestModel_SyncRefreshesViewInPlace(t *testing.T) {
	eng := pricing.NewEngine()
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	synced := now.Add(-10 * time.Minute) // stale until the sync runs
	reloads := 0
	m := New([]Period{{Label: "today"}}, 0, eng).WithNow(now).
		WithSyncStatus(func() time.Time { return synced }).
		WithWatch(15*time.Minute, func() time.Time { return now }, func() []Period {
			reloads++
			synced = now // the sync advanced the scan watermark
			return []Period{{Label: "today", Events: []event.AgentEvent{
				priced(t, eng, "evt_sync0001", "s1", "payments", "claude-opus-4-8", now, event.Tokens{Input: 100_000}),
			}}}
		})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = nm.(Model)

	// Before the sync: a stale stamp and no session rows yet.
	if v := m.View(); !strings.Contains(v, "synced 10m ago") || strings.Contains(v, "payments") {
		t.Fatalf("pre-sync header should be stale with no rows:\n%s", v)
	}

	// A watch tick kicks the reload (the sync); run the command and fold the result back —
	// exactly the runtime's tick → reloadCmd → reloadDoneMsg cycle, but driven by hand.
	nm, cmd := m.Update(tickMsg(now))
	if cmd == nil {
		t.Fatal("a watch tick should schedule a reload command")
	}
	done := cmd()                   // executes reload() — the sync — as Bubble Tea would, off-thread
	nm, _ = nm.(Model).Update(done) // apply reloadDoneMsg on the UI loop
	m = nm.(Model)

	if reloads == 0 {
		t.Fatal("the tick should have driven a reload (the in-process sync)")
	}
	v := m.View()
	if !strings.Contains(v, "synced just now") {
		t.Errorf("after the sync the stamp should refresh to 'just now':\n%s", v)
	}
	if !strings.Contains(v, "payments") {
		t.Errorf("after the sync the newly-imported session should appear in place:\n%s", v)
	}
}
