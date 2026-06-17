package store

import (
	"path/filepath"
	"testing"

	"github.com/agentspend/ai-agent-spend/internal/event"
)

// MemStore runs the full Store + Sink contract.
func TestMemStore_Contract(t *testing.T) {
	storeContract(t, func(t *testing.T) storeSink { return NewMemStore() })
}

func newFileStore(t *testing.T) storeSink {
	t.Helper()
	s, err := OpenFileStore(filepath.Join(t.TempDir(), "events.json"))
	if err != nil {
		t.Fatalf("OpenFileStore: %v", err)
	}
	return s
}

// FileStore (the default 0A backend) runs the SAME contract as MemStore.
func TestFileStore_Contract(t *testing.T) {
	storeContract(t, newFileStore)
}

func TestFileStore_PersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.json")

	s1, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Upsert([]event.AgentEvent{sample("evt_1", "claude_code", "payments", base)}); err != nil {
		t.Fatal(err)
	}
	if err := s1.SetLastScan("claude_code", base); err != nil {
		t.Fatal(err)
	}

	// reopen from disk — scan/week run as separate processes, so this is the real path
	s2, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Get("evt_1"); err != nil {
		t.Errorf("event not persisted across reopen: %v", err)
	}
	if got, _ := s2.LastScan("claude_code"); !got.Equal(base) {
		t.Errorf("last_scan not persisted: got %v, want %v", got, base)
	}
}
