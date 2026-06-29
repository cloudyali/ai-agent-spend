//go:build !offline

package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/platform"
)

// promptResolver snapshots the user's Claude Code and Codex logs and, for a turn,
// re-reads the human prompt behind it — dispatching by provider and resolving only by
// content hash (never a path from the event), so a foreign/forged hash can't coerce
// an out-of-tree file read.
func TestPromptResolver(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.jsonl")
	content := `{"type":"user","message":{"role":"user","content":"wire the resolver"}}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[],"usage":{"input_tokens":2}}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	// A Codex rollout too, so the resolver must dispatch by provider.
	cxDir := filepath.Join(home, ".codex", "sessions", "s")
	if err := os.MkdirAll(cxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cxPath := filepath.Join(cxDir, "rollout.jsonl")
	cxContent := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"codex prompt here"}]}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":50}}}}` + "\n"
	if err := os.WriteFile(cxPath, []byte(cxContent), 0o600); err != nil {
		t.Fatal(err)
	}

	res := platform.Resolver{GOOS: "linux", Home: home, Env: func(string) string { return "" }}
	a := &App{Resolver: res}
	resolve := a.promptResolver()
	if resolve == nil {
		t.Fatal("promptResolver should be non-nil when sources exist")
	}

	hash := platform.HashPath(path, res.GOOS)
	if got, ok := resolve(event.AgentEvent{Provider: "claude_code", Evidence: event.Evidence{SourcePathHash: hash, SourceLine: 2}}); !ok || got != "wire the resolver" {
		t.Errorf("resolve(claude_code turn) = %q,%v want %q,true", got, ok, "wire the resolver")
	}
	// A Codex turn resolves through the codex source map (the billable turn is the
	// token_count line 2; the prompt is the response_item on line 1).
	cxHash := platform.HashPath(cxPath, res.GOOS)
	if got, ok := resolve(event.AgentEvent{Provider: "codex", Evidence: event.Evidence{SourcePathHash: cxHash, SourceLine: 2}}); !ok || got != "codex prompt here" {
		t.Errorf("resolve(codex turn) = %q,%v want %q,true", got, ok, "codex prompt here")
	}
	// Providers never cross maps: a codex event carrying a claude_code hash misses.
	if _, ok := resolve(event.AgentEvent{Provider: "codex", Evidence: event.Evidence{SourcePathHash: hash, SourceLine: 2}}); ok {
		t.Errorf("a codex event must not resolve via the claude_code source map")
	}
	// A hash that matches no enumerated source resolves to nothing — no path is ever
	// taken from the event itself.
	if _, ok := resolve(event.AgentEvent{Provider: "claude_code", Evidence: event.Evidence{SourcePathHash: "deadbeef", SourceLine: 2}}); ok {
		t.Errorf("an unknown source hash must not resolve to a file")
	}
}

// With no Claude Code logs on the machine, the resolver is nil and the explain view
// simply shows no prompt section (never a crash).
func TestPromptResolver_NoSources(t *testing.T) {
	a := &App{Resolver: platform.Resolver{GOOS: "linux", Home: t.TempDir(), Env: func(string) string { return "" }}}
	if a.promptResolver() != nil {
		t.Error("promptResolver should be nil when no Claude Code sources exist")
	}
}
