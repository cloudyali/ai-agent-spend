package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/pricing"
)

// keyS is the on-demand sync keypress.
var keyS = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}

// Pressing `s` in the list kicks an on-demand sync: the same background reload the watch
// tick runs, off the UI loop, landing as a syncDoneMsg that folds fresh data + a snapped
// "synced just now" stamp back in — without waiting out the periodic cadence. Crucially
// the result must NOT re-arm the watch tick (the periodic cadence already owns its tick).
func TestModel_OnDemandSync_KeyKicksSync(t *testing.T) {
	eng := pricing.NewEngine()
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	synced := now.Add(-9 * time.Minute) // stale until the sync runs
	reloads := 0
	m := New([]Period{{Label: "today"}}, 0, eng).WithNow(now).
		WithSyncStatus(func() time.Time { return synced }).
		WithWatch(15*time.Minute, func() time.Time { return now }, func() []Period {
			reloads++
			synced = now // the sync advanced the scan watermark
			return []Period{{Label: "today", Events: []event.AgentEvent{
				priced(t, eng, "evt_ods0001", "s1", "payments", "claude-opus-4-8", now, event.Tokens{Input: 100_000}),
			}}}
		})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = nm.(Model)

	// Pre-sync: a stale stamp and no session rows yet.
	if v := m.View(); !strings.Contains(v, "synced 9m ago") || strings.Contains(v, "payments") {
		t.Fatalf("pre-sync the header should be stale with no rows:\n%s", v)
	}

	nm, cmd := m.Update(keyS)
	if cmd == nil {
		t.Fatal("`s` should kick a background sync command")
	}
	m = nm.(Model)
	if !m.reloading {
		t.Error("`s` should mark a sync in flight (the single-flight guard)")
	}
	done, ok := cmd().(syncDoneMsg)
	if !ok {
		t.Fatalf("the sync cmd should yield a syncDoneMsg, got %T", cmd())
	}
	nm, cmd = m.Update(done)
	m = nm.(Model)

	if reloads != 1 {
		t.Fatalf("`s` should have driven exactly one reload, got %d", reloads)
	}
	if m.reloading {
		t.Error("a landed sync should clear the in-flight guard")
	}
	if cmd != nil {
		t.Error("an on-demand sync must NOT re-arm the watch tick (the cadence owns its own tick)")
	}
	v := m.View()
	if !strings.Contains(v, "synced just now") {
		t.Errorf("after the sync the stamp should refresh to 'just now':\n%s", v)
	}
	if !strings.Contains(v, "payments") {
		t.Errorf("after the sync the newly-imported session should appear in place:\n%s", v)
	}
}

// Pressing `s` gives immediate feedback: the freshness stamp flips to an in-progress
// "syncing…" the instant the sync is kicked — the very next frame, before the reload
// returns — then resumes the "synced …" stamp, snapped to "just now", once the sync
// lands. So the user sees the action took, not a screen that looks frozen.
func TestModel_OnDemandSync_ShowsSyncingThenSynced(t *testing.T) {
	eng := pricing.NewEngine()
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	synced := now.Add(-8 * time.Minute) // stale until the sync runs
	m := New([]Period{{Label: "today"}}, 0, eng).WithNow(now).
		WithSyncStatus(func() time.Time { return synced }).
		WithWatch(15*time.Minute, func() time.Time { return now }, func() []Period {
			synced = now // the sync advanced the scan watermark
			return []Period{{Label: "today", Events: []event.AgentEvent{
				priced(t, eng, "evt_ui0001", "s1", "payments", "claude-opus-4-8", now, event.Tokens{Input: 100_000}),
			}}}
		})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = nm.(Model)

	// Before `s`: the steady freshness stamp, no in-progress text.
	if v := m.View(); !strings.Contains(v, "synced 8m ago") || strings.Contains(v, "syncing") {
		t.Fatalf("pre-sync the header should show the steady stamp, not 'syncing':\n%s", v)
	}

	// Press `s`: the very next frame announces the sync started, before the reload returns.
	nm, cmd := m.Update(keyS)
	if cmd == nil {
		t.Fatal("`s` should kick a sync")
	}
	m = nm.(Model)
	if v := m.View(); !strings.Contains(v, "syncing") {
		t.Errorf("the frame right after `s` should announce the sync started ('syncing'):\n%s", v)
	}
	if v := m.View(); strings.Contains(v, "synced 8m ago") {
		t.Errorf("the stale stamp should give way to the in-progress message:\n%s", v)
	}

	// The sync lands: resume the freshness stamp at 'just now', no lingering 'syncing'.
	done := cmd()
	nm, _ = m.Update(done)
	m = nm.(Model)
	v := m.View()
	if strings.Contains(v, "syncing") {
		t.Errorf("a landed sync must clear the in-progress message:\n%s", v)
	}
	if !strings.Contains(v, "synced just now") {
		t.Errorf("after the sync the stamp should resume at 'just now':\n%s", v)
	}
}

// A second on-demand sync requested while one is already in flight does nothing — the
// single-flight guard ("if a sync is already running, don't do anything") means `s`
// never stacks a second store writer.
func TestModel_OnDemandSync_NoopWhileInFlight(t *testing.T) {
	eng := pricing.NewEngine()
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	reloads := 0
	m := New([]Period{{Label: "today"}}, 0, eng).WithNow(now).
		WithWatch(15*time.Minute, func() time.Time { return now }, func() []Period {
			reloads++
			return []Period{{Label: "today"}}
		})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = nm.(Model)

	nm, cmd := m.Update(keyS) // first sync: in flight now
	if cmd == nil {
		t.Fatal("the first `s` should kick a sync")
	}
	m = nm.(Model)

	nm, cmd2 := m.Update(keyS) // second `s` while the first is still in flight
	if cmd2 != nil {
		t.Error("a second `s` while a sync is in flight must be a no-op (nil command)")
	}
	m = nm.(Model)
	if !m.reloading {
		t.Error("the in-flight guard should remain set after the ignored second `s`")
	}

	// Executing the FIRST command runs exactly one reload; the ignored second produced none.
	_ = cmd()
	if reloads != 1 {
		t.Errorf("only the first `s` should reload; got %d reloads", reloads)
	}
}

// While an on-demand sync is in flight, a watch tick must skip its beat (kick no second
// reload) yet keep the cadence alive by re-arming the tick — so the periodic sync isn't
// lost and no second store writer is stacked.
func TestModel_OnDemandSync_TickSkipsWhileInFlight(t *testing.T) {
	eng := pricing.NewEngine()
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	reloads := 0
	// A tiny interval so the re-armed tick command is cheap to execute in the assertion.
	m := New([]Period{{Label: "today"}}, 0, eng).WithNow(now).
		WithWatch(time.Millisecond, func() time.Time { return now }, func() []Period {
			reloads++
			return []Period{{Label: "today"}}
		})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = nm.(Model)

	nm, _ = m.Update(keyS) // a sync is now in flight (its cmd is deliberately not executed)
	m = nm.(Model)

	nm, cmd := m.Update(tickMsg(now)) // a watch tick arrives mid-sync
	if cmd == nil {
		t.Fatal("a tick during an in-flight sync should still re-arm the cadence")
	}
	m = nm.(Model)
	if !m.reloading {
		t.Error("the tick must not clear the in-flight guard")
	}
	// The re-armed command is the next tick, NOT a reload — executing it yields a tickMsg.
	if _, isReload := cmd().(reloadDoneMsg); isReload {
		t.Error("a tick during an in-flight sync must not kick a second reload")
	}
	if reloads != 0 {
		t.Errorf("the skipped tick must not run a reload; got %d", reloads)
	}
}

// The list legend advertises the on-demand sync key only when a reload is wired (the cli
// always wires one); a static model leaves `s` unadvertised and inert.
func TestModel_OnDemandSync_LegendGated(t *testing.T) {
	eng := pricing.NewEngine()
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)

	wired := New([]Period{{Label: "today"}}, 0, eng).WithNow(now).
		WithWatch(15*time.Minute, func() time.Time { return now }, func() []Period { return []Period{{Label: "today"}} })
	nm, _ := wired.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	wired = nm.(Model)
	if !strings.Contains(wired.View(), "s sync") {
		t.Errorf("a wired model should advertise `s sync` in the legend:\n%s", wired.View())
	}

	static := New([]Period{{Label: "today"}}, 0, eng).WithNow(now)
	nm2, _ := static.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	static = nm2.(Model)
	if strings.Contains(static.View(), "s sync") {
		t.Errorf("a static model (no reload wired) should not advertise `s sync`:\n%s", static.View())
	}
}
