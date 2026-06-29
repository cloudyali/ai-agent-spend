// FileStore is the default 0A persistent Store: a single JSON file, zero external
// dependencies, which keeps the binary a pure-Go static artifact. It is ample for a
// single developer's local ledger (thousands of events); the -tags sqlite SQLiteStore
// is the multi-writer-safe drop-in (cross-process locking) behind the same interface,
// for when a background daemon shares the ledger with manual scans.
// See design-documents/DESIGN.md
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/event"
)

var _ Store = (*FileStore)(nil)

// FileStore persists events to a JSON file, written atomically (temp + rename).
type FileStore struct {
	mu       sync.RWMutex
	path     string
	events   map[string]event.AgentEvent
	lastScan map[string]int64 // provider → unix-nanos
}

type fileData struct {
	Events   map[string]event.AgentEvent `json:"events"`
	LastScan map[string]int64            `json:"last_scan"`
}

// OpenFileStore loads the store at path (creating its parent dir), or starts empty.
func OpenFileStore(path string) (*FileStore, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("store: mkdir %s: %w", dir, err)
		}
	}
	s := &FileStore{
		path:     path,
		events:   make(map[string]event.AgentEvent),
		lastScan: make(map[string]int64),
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: read %s: %w", path, err)
	}
	if len(b) > 0 {
		var d fileData
		if err := json.Unmarshal(b, &d); err != nil {
			return nil, fmt.Errorf("store: parse %s: %w", path, err)
		}
		if d.Events != nil {
			s.events = d.Events
		}
		if d.LastScan != nil {
			s.lastScan = d.LastScan
		}
	}
	return s, nil
}

// persist writes the whole store atomically. Caller holds the write lock.
func (s *FileStore) persist() error {
	b, err := json.MarshalIndent(fileData{Events: s.events, LastScan: s.lastScan}, "", " ")
	if err != nil {
		return fmt.Errorf("store: marshal: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("store: write temp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("store: rename: %w", err)
	}
	return nil
}

// Upsert writes events idempotently (keyed on EventID) and persists.
func (s *FileStore) Upsert(events []event.AgentEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range events {
		if e.EventID == "" {
			return fmt.Errorf("store: refusing to persist event with empty EventID")
		}
		s.events[e.EventID] = e
	}
	return s.persist()
}

// Query returns events matching the filter, ordered by TSStart ascending.
func (s *FileStore) Query(f Filter) ([]event.AgentEvent, error) {
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
func (s *FileStore) Get(eventID string) (event.AgentEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.events[eventID]
	if !ok {
		return event.AgentEvent{}, fmt.Errorf("store: event %q not found", eventID)
	}
	return e, nil
}

// LastScan returns the last scan time for a provider (zero if never scanned).
func (s *FileStore) LastScan(provider string) (time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	nanos, ok := s.lastScan[provider]
	if !ok || nanos == 0 {
		return time.Time{}, nil
	}
	return time.Unix(0, nanos).UTC(), nil
}

// SetLastScan records the last scan time for a provider and persists.
func (s *FileStore) SetLastScan(provider string, t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastScan[provider] = t.UTC().UnixNano()
	return s.persist()
}
