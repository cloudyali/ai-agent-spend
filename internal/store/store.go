// Package store persists AgentEvents idempotently and answers queries over them.
// The Store interface is the seam: an in-memory implementation (proving the contract
// under TDD), the default JSON FileStore, and the -tags sqlite SQLiteStore all satisfy
// the SAME interface and the SAME contract test suite, so nothing above this package
// knows which implementation it holds.
//
// See design-documents/DESIGN.md (storage) and
// design-documents/DESIGN.md (interfaces).
package store

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/event"
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

// Compile-time proof the in-memory store satisfies the Store seam.
var _ Store = (*MemStore)(nil)

// MemStore is a goroutine-safe in-memory Store.
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
