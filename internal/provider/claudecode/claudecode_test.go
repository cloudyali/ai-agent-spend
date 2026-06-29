package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/platform"
)

func noEnv(string) string { return "" }

// writeSession creates <home>/.claude/projects/<proj>/<name>.jsonl with body.
func writeSession(t *testing.T, home, proj, name, body string) string {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", proj)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestName(t *testing.T) {
	if got := New(platform.Resolver{}).Name(); got != "claude_code" {
		t.Errorf("Name() = %q, want claude_code", got)
	}
}

func TestSources_EmptyWhenNoSessions(t *testing.T) {
	home := t.TempDir()
	// the projects root exists (so Detect is true) but holds no .jsonl files
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := New(platform.Resolver{GOOS: "linux", Home: home, Env: noEnv})
	srcs, err := p.Sources()
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 0 {
		t.Errorf("Sources = %d, want 0 for a root with no sessions", len(srcs))
	}
}

// Cowork desktop sessions live under <home>/Library/Application Support/Claude/
// local-agent-mode-sessions/.../.claude/projects/<mangled>/<uuid>.jsonl. The
// walker must pick those up but skip the big outputs/ (and uploads/) trees that
// sit beside them — a stray .jsonl artifact there is not a transcript.
func TestSources_CoworkTreeFoundButOutputsSkipped(t *testing.T) {
	home := t.TempDir()
	base := filepath.Join(home, "Library", "Application Support", "Claude",
		"local-agent-mode-sessions", "ws", "conv", "local_x")
	txDir := filepath.Join(base, ".claude", "projects", "-mangled-cwd")
	if err := os.MkdirAll(txDir, 0o755); err != nil {
		t.Fatal(err)
	}
	txFile := filepath.Join(txDir, "sess.jsonl")
	if err := os.WriteFile(txFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(base, "outputs")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "junk.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := New(platform.Resolver{GOOS: "darwin", Home: home, Env: noEnv})
	srcs, err := p.Sources()
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 1 || srcs[0].RawPath != txFile {
		t.Errorf("Sources = %+v, want only the transcript %s (outputs/ skipped)", srcs, txFile)
	}
}

func TestDetect(t *testing.T) {
	t.Run("present when projects dir exists", func(t *testing.T) {
		home := t.TempDir()
		writeSession(t, home, "payments", "s.jsonl", "{}\n")
		p := New(platform.Resolver{GOOS: "linux", Home: home, Env: noEnv})
		if ok, err := p.Detect(); err != nil || !ok {
			t.Errorf("Detect = %v, %v; want true", ok, err)
		}
	})
	t.Run("absent on a clean home", func(t *testing.T) {
		p := New(platform.Resolver{GOOS: "linux", Home: t.TempDir(), Env: noEnv})
		if ok, _ := p.Detect(); ok {
			t.Error("Detect = true on clean home; want false")
		}
	})
}

func TestSourcesAndRead(t *testing.T) {
	home := t.TempDir()
	body := "{\"type\":\"user\"}\n" +
		"{\"type\":\"assistant\",\"message\":{\"usage\":{\"input_tokens\":1}}}\n" +
		"\n" // trailing blank line is ignored
	path := writeSession(t, home, "payments", "sess.jsonl", body)
	p := New(platform.Resolver{GOOS: "linux", Home: home, Env: noEnv})

	srcs, err := p.Sources()
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 1 {
		t.Fatalf("Sources = %d, want 1", len(srcs))
	}
	if srcs[0].RawPath != path || srcs[0].PathHash == "" || srcs[0].Kind != "session_jsonl" {
		t.Errorf("source malformed: %+v", srcs[0])
	}

	recs, err := p.Read(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 { // two non-empty lines
		t.Fatalf("Read = %d records, want 2", len(recs))
	}
	if recs[0].Line != 1 || recs[1].Line != 2 {
		t.Errorf("line numbers = %d,%d; want 1,2", recs[0].Line, recs[1].Line)
	}
	if recs[0].Provider != "claude_code" || recs[0].Source.PathHash != srcs[0].PathHash {
		t.Errorf("record provenance wrong: %+v", recs[0])
	}
}

func TestRead_HandlesVeryLongLines(t *testing.T) {
	home := t.TempDir()
	// Real Claude Code lines embed large tool outputs / file contents on a single
	// line, far past bufio.Scanner's limits. This 2 MiB line must read cleanly.
	big := strings.Repeat("x", 2<<20)
	line := `{"type":"assistant","message":{"usage":{"input_tokens":1}},"blob":"` + big + "\"}\n"
	writeSession(t, home, "payments", "big.jsonl", line)

	p := New(platform.Resolver{GOOS: "linux", Home: home, Env: noEnv})
	recs, err := p.Read(time.Time{})
	if err != nil {
		t.Fatalf("Read failed on a long line: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
}

func TestRead_SkipsFilesNotModifiedSince(t *testing.T) {
	home := t.TempDir()
	writeSession(t, home, "payments", "sess.jsonl", "{\"type\":\"user\"}\n")
	p := New(platform.Resolver{GOOS: "linux", Home: home, Env: noEnv})

	future := time.Now().Add(time.Hour)
	if recs, err := p.Read(future); err != nil || len(recs) != 0 {
		t.Errorf("Read(future) = %d recs, %v; want 0", len(recs), err)
	}
	past := time.Now().Add(-time.Hour)
	if recs, _ := p.Read(past); len(recs) != 1 {
		t.Errorf("Read(past) = %d recs; want 1", len(recs))
	}
}
