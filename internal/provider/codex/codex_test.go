package codex

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentspend/ai-agent-spend/internal/platform"
)

func noEnv(string) string { return "" }

func writeRollout(t *testing.T, home, sub, name, body string) string {
	t.Helper()
	dir := filepath.Join(home, ".codex", "sessions", sub)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestName(t *testing.T) {
	if New(platform.Resolver{}).Name() != "codex" {
		t.Error("Name should be codex")
	}
}

func TestDetectSourcesRead(t *testing.T) {
	home := t.TempDir()
	// date-based layout
	path := writeRollout(t, home, filepath.Join("2026", "06", "15"), "rollout-abc.jsonl", "{\"a\":1}\n{\"b\":2}\n")
	p := New(platform.Resolver{GOOS: "linux", Home: home, Env: noEnv})

	if ok, err := p.Detect(); err != nil || !ok {
		t.Fatalf("Detect = %v, %v; want true", ok, err)
	}
	srcs, err := p.Sources()
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 1 || srcs[0].RawPath != path || srcs[0].Kind != "rollout_jsonl" {
		t.Fatalf("sources = %+v", srcs)
	}
	recs, err := p.Read(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].Provider != "codex" || recs[1].Line != 2 {
		t.Fatalf("recs = %+v", recs)
	}
}

func TestDetect_AbsentOnCleanHome(t *testing.T) {
	p := New(platform.Resolver{GOOS: "linux", Home: t.TempDir(), Env: noEnv})
	if ok, _ := p.Detect(); ok {
		t.Error("Detect should be false on a clean home")
	}
}

func TestRead_SkipsFilesNotModifiedSince(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home, "s", "r.jsonl", "{\"a\":1}\n")
	p := New(platform.Resolver{GOOS: "linux", Home: home, Env: noEnv})

	if recs, err := p.Read(time.Now().Add(time.Hour)); err != nil || len(recs) != 0 {
		t.Errorf("Read(future) = %d recs, %v; want 0", len(recs), err)
	}
	if recs, _ := p.Read(time.Now().Add(-time.Hour)); len(recs) != 1 {
		t.Errorf("Read(past) = %d recs; want 1", len(recs))
	}
}
