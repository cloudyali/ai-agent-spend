// Package store persists AgentEvents idempotently and answers queries over them.
// The Store and Sink interfaces are the seam: Phase 0A ships an in-memory
// implementation (proving the contract under TDD) and a SQLite-backed LocalSink
// that satisfies the SAME interface and the SAME test suite. Nothing above this
// package knows which implementation it holds.
//
// See design-documents/02-data-model.md (storage) and
// design-documents/phase-0A-trusted-explainable-ledger.md (interfaces).
package store

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/agentspend/ai-agent-spend/internal/event"
)

// Filter selects and groups events for a query.
type Filter struct {
	Since, Until time.Time
	Provider     string
	Repo         string
	GroupBy      string // "model" | "repo" | "provider" | "cost_tag" — applied by the CLI aggregation layer
}

// Store is idempotent persistence keyed on EventID, plus per-provider scan state.
type Store interface {
	Upsert(events []event.AgentEvent) error
	Query(Filter) ([]event.AgentEvent, error)
	Get(eventID string) (event.AgentEvent, error)
	LastScan(provider string) (time.Time, error)
	SetLastScan(provider string, t time.Time) error
}

// Sink is the single egress seam. In the default build the only implementation
// is local (in-memory now, SQLite next) — the cloud sink lives behind a build tag.
type Sink interface {
	Write(events []event.AgentEvent) error
}

// Compile-time proof the in-memory store satisfies both seams.
var (
	_ Store = (*MemStore)(nil)
	_ Sink  = (*MemStore)(nil)
)

// MemStore is a goroutine-safe in-memory Store + Sink.
type MemStore struct {
	mu       sync.RWMutex
	events   map[string]event.AgentEvent
	lastScan map[string]time.Time
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{
		events:   make(map[string]event.AgentEvent),
		lastScan: make(map[string]time.Time),
	}
}

// Upsert writes events idempotently, keyed on EventID, so re-scanning is safe.
func (s *MemStore) Upsert(events []event.AgentEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range events {
		if e.EventID == "" {
			return fmt.Errorf("store: refusing to persist event with empty EventID")
		}
		s.events[e.EventID] = e
	}
	return nil
}

// Write implements Sink; it is the same idempotent operation as Upsert.
func (s *MemStore) Write(events []event.AgentEvent) error { return s.Upsert(events) }

// Query returns events matching the filter, ordered by TSStart ascending.
func (s *MemStore) Query(f Filter) ([]event.AgentEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []event.AgentEvent
	for _, e := range s.events {
		if f.Provider != "" && e.Provider != f.Provider {
			continue
		}
		if f.Repo != "" && e.Repo != f.Repo {
			continue
		}
		if !f.Since.IsZero() && e.TSStart.Before(f.Since) {
			continue
		}
		if !f.Until.IsZero() && e.TSStart.After(f.Until) {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TSStart.Before(out[j].TSStart) })
	return out, nil
}

// Get returns one event by ID, or an error if absent.
func (s *MemStore) Get(eventID string) (event.AgentEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.events[eventID]
	if !ok {
		return event.AgentEvent{}, fmt.Errorf("store: event %q not found", eventID)
	}
	return e, nil
}

// LastScan returns the last scan time for a provider (zero if never scanned).
func (s *MemStore) LastScan(provider string) (time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastScan[provider], nil
}

// SetLastScan records the last scan time for a provider.
func (s *MemStore) SetLastScan(provider string, t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastScan[provider] = t
	return nil
}
