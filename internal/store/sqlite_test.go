//go:build sqlite

package store

import (
	"path/filepath"
	"testing"

	"github.com/cloudyali/ai-agent-spend/internal/event"
)

func newSQLite(t *testing.T) storeSink {
	t.Helper()
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// SQLiteStore runs the SAME contract suite as MemStore — same behavior, two backends.
func TestSQLiteStore_Contract(t *testing.T) {
	storeContract(t, newSQLite)
}

func TestSQLiteStore_PersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persist.db")

	s1, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Upsert([]event.AgentEvent{sample("evt_1", "claude_code", "payments", base)}); err != nil {
		t.Fatal(err)
	}
	_ = s1.Close()

	s2, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if _, err := s2.Get("evt_1"); err != nil {
		t.Errorf("event not persisted across reopen: %v", err)
	}
}
