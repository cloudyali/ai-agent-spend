package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

// userPromptText pulls the human-typed text out of one Claude Code `type:"user"`
// record — a plain string content, or the joined "text" blocks of an array
// content. tool_result blocks (tool output, not a typed prompt) and non-user lines
// yield no text, so the caller keeps walking back to the real prompt.
func TestUserPromptText(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{"string content", `{"type":"user","message":{"role":"user","content":"just fix the bug"}}`, "just fix the bug", true},
		{"text block", `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"refactor the handler"}]}}`, "refactor the handler", true},
		{"multiple text blocks", `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"line one"},{"type":"text","text":"line two"}]}}`, "line one\nline two", true},
		{"tool_result only is not a prompt", `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"exit 0"}]}}`, "", false},
		{"assistant line is not a prompt", `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"sure"}]}}`, "", false},
		{"empty string content", `{"type":"user","message":{"role":"user","content":"   "}}`, "", false},
		{"malformed json", `{"type":"user" ...`, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := userPromptText([]byte(c.raw))
			if ok != c.ok || got != c.want {
				t.Errorf("userPromptText = %q,%v want %q,%v", got, ok, c.want, c.ok)
			}
		})
	}
}

// promptBefore walks the lines preceding an assistant turn backward and returns the
// nearest human prompt, skipping intervening assistant + tool_result lines.
func TestPromptBefore_WalksBack(t *testing.T) {
	lines := [][]byte{
		[]byte(`{"type":"user","message":{"role":"user","content":"the real prompt"}}`),
		[]byte(`{"type":"assistant","message":{"role":"assistant","content":[]}}`),
		[]byte(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ran"}]}}`),
	}
	got, ok := promptBefore(lines)
	if !ok || got != "the real prompt" {
		t.Fatalf("promptBefore = %q,%v want %q,true", got, ok, "the real prompt")
	}

	if _, ok := promptBefore([][]byte{
		[]byte(`{"type":"assistant","message":{"content":[]}}`),
	}); ok {
		t.Errorf("promptBefore with no user line should be false")
	}
	if _, ok := promptBefore(nil); ok {
		t.Errorf("promptBefore over no lines should be false")
	}
}

// PromptBefore reads the session file and returns the prompt before the assistant
// turn at the given 1-indexed physical line; missing file / out-of-range → false.
func TestPromptBefore_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	content := `{"type":"user","message":{"role":"user","content":"implement retries"}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"build ok"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":2}}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	// The assistant turn is physical line 3; the prompt is line 1 (line 2 is a
	// tool_result and must be skipped).
	if got, ok := PromptBefore(path, 3); !ok || got != "implement retries" {
		t.Errorf("PromptBefore(line 3) = %q,%v want %q,true", got, ok, "implement retries")
	}
	// Nothing precedes line 1.
	if _, ok := PromptBefore(path, 1); ok {
		t.Errorf("PromptBefore(line 1) should be false")
	}
	// A path that doesn't exist degrades to false, never an error/panic.
	if _, ok := PromptBefore(filepath.Join(dir, "gone.jsonl"), 3); ok {
		t.Errorf("PromptBefore(missing file) should be false")
	}
}
