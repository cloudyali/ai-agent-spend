package store

import (
	"testing"
	"time"

	"github.com/agentspend/ai-agent-spend/internal/event"
)

// storeSink is the full contract: persistence (Store) + the egress seam (Sink).
type storeSink interface {
	Store
	Sink
}

var base = time.Date(2026, 6, 14, 9, 0, 0, 0, time.UTC)

func sample(id, provider, repo string, ts time.Time) event.AgentEvent {
	return event.AgentEvent{
		SchemaVersion: event.SchemaVersion,
		EventID:       id,
		Provider:      provider,
		Repo:          repo,
		Project:       repo,
		Model:         "claude-opus-4",
		Tokens:        event.Tokens{Input: 100},
		TSStart:       ts,
		TSEnd:         ts,
	}
}

// storeContract exercises the Store + Sink behavior against ANY implementation.
// Both MemStore and SQLiteStore are run through it — same suite, two backends.
func storeContract(t *testing.T, newStore func(t *testing.T) storeSink) {
	t.Run("upsert is idempotent and replaces in place", func(t *testing.T) {
		s := newStore(t)
		e := sample("evt_1", "claude_code", "payments", base)
		mustUpsert(t, s, e)
		mustUpsert(t, s, e) // re-scan: must not duplicate
		if got := queryAll(t, s); len(got) != 1 {
			t.Fatalf("idempotency broken: %d events, want 1", len(got))
		}
		e2 := e
		e2.Repo = "search"
		mustUpsert(t, s, e2)
		if got := queryAll(t, s); len(got) != 1 || got[0].Repo != "search" {
			t.Fatalf("upsert should replace in place: %+v", got)
		}
	})

	t.Run("empty EventID errors", func(t *testing.T) {
		s := newStore(t)
		if err := s.Upsert([]event.AgentEvent{sample("", "claude_code", "x", base)}); err == nil {
			t.Error("expected error upserting empty EventID")
		}
	})

	t.Run("query filters and orders by ts", func(t *testing.T) {
		s := newStore(t)
		mustUpsert(t, s,
			sample("a", "claude_code", "payments", base),
			sample("b", "claude_code", "search", base.Add(time.Hour)),
			sample("c", "codex", "payments", base.Add(2*time.Hour)),
		)
		if got := query(t, s, Filter{Provider: "claude_code"}); len(got) != 2 {
			t.Errorf("by provider: %d, want 2", len(got))
		}
		if got := query(t, s, Filter{Repo: "payments"}); len(got) != 2 {
			t.Errorf("by repo: %d, want 2", len(got))
		}
		win := query(t, s, Filter{Since: base.Add(30 * time.Minute), Until: base.Add(90 * time.Minute)})
		if len(win) != 1 || win[0].EventID != "b" {
			t.Errorf("time window: %+v, want only b", win)
		}
		all := queryAll(t, s)
		if len(all) != 3 || all[0].EventID != "a" || all[2].EventID != "c" {
			t.Errorf("expected order a,b,c; got %v", ids(all))
		}
	})

	t.Run("get hit and miss", func(t *testing.T) {
		s := newStore(t)
		mustUpsert(t, s, sample("evt_1", "claude_code", "payments", base))
		if e, err := s.Get("evt_1"); err != nil || e.Repo != "payments" {
			t.Errorf("get hit: %+v err=%v", e, err)
		}
		if _, err := s.Get("missing"); err == nil {
			t.Error("get miss should error")
		}
	})

	t.Run("lastscan roundtrip", func(t *testing.T) {
		s := newStore(t)
		if got, _ := s.LastScan("claude_code"); !got.IsZero() {
			t.Errorf("default LastScan = %v, want zero", got)
		}
		when := base.Add(5 * time.Hour)
		if err := s.SetLastScan("claude_code", when); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.LastScan("claude_code"); !got.Equal(when) {
			t.Errorf("LastScan = %v, want %v", got, when)
		}
	})

	t.Run("Write equals Upsert (Sink)", func(t *testing.T) {
		s := newStore(t)
		if err := s.Write([]event.AgentEvent{sample("w1", "claude_code", "r", base)}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get("w1"); err != nil {
			t.Errorf("Write should persist like Upsert: %v", err)
		}
	})

	t.Run("lossless round-trip of nested fields", func(t *testing.T) {
		s := newStore(t)
		e := sample("rt", "claude_code", "payments", base)
		e.Tools = []string{"Edit", "mcp__github__create_issue"}
		e.MCPServers = []string{"github"}
		api := event.USD(123)
		e.CostViews.APIEquivalent = &api
		e.Evidence.ParserVersion = "v1"
		mustUpsert(t, s, e)

		got, err := s.Get("rt")
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Tools) != 2 || len(got.MCPServers) != 1 || got.MCPServers[0] != "github" ||
			got.CostViews.APIEquivalent == nil || *got.CostViews.APIEquivalent != api ||
			got.Evidence.ParserVersion != "v1" {
			t.Errorf("nested fields not preserved: %+v", got)
		}
	})
}

// --- helpers (shared by both backends) ---

func mustUpsert(t *testing.T, s storeSink, evs ...event.AgentEvent) {
	t.Helper()
	if err := s.Upsert(evs); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
}

func query(t *testing.T, s storeSink, f Filter) []event.AgentEvent {
	t.Helper()
	got, err := s.Query(f)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	return got
}

func queryAll(t *testing.T, s storeSink) []event.AgentEvent { return query(t, s, Filter{}) }

func ids(evs []event.AgentEvent) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.EventID
	}
	return out
}
