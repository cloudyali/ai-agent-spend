package codex

import (
	"os"
	"path/filepath"
	"testing"
)

// userPromptText pulls the human-typed text out of one Codex rollout record: a
// response_item whose payload is a user message. Codex user content is "input_text"
// blocks (assistant output is "output_text"); function_call / event_msg / assistant
// lines carry no typed prompt, so the caller keeps walking back.
func TestUserPromptText(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{"input_text block", `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"add a retry"}]}}`, "add a retry", true},
		{"multiple blocks", `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"one"},{"type":"input_text","text":"two"}]}}`, "one\ntwo", true},
		{"string content", `{"type":"response_item","payload":{"type":"message","role":"user","content":"just a string"}}`, "just a string", true},
		{"assistant message is not a prompt", `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"sure"}]}}`, "", false},
		{"function_call is not a prompt", `{"type":"response_item","payload":{"type":"function_call","name":"shell"}}`, "", false},
		{"event_msg is not a prompt", `{"type":"event_msg","payload":{"type":"token_count","info":{}}}`, "", false},
		{"empty user content", `{"type":"response_item","payload":{"type":"message","role":"user","content":[]}}`, "", false},
		{"malformed json", `{"type":"response_item" ...`, "", false},
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

// promptBefore walks the records before a turn backward and returns the nearest
// human prompt, skipping assistant and tool (function_call) response_items.
func TestPromptBefore_WalksBack(t *testing.T) {
	lines := [][]byte{
		[]byte(`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"the real prompt"}]}}`),
		[]byte(`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"working"}]}}`),
		[]byte(`{"type":"response_item","payload":{"type":"function_call","name":"shell"}}`),
	}
	if got, ok := promptBefore(lines); !ok || got != "the real prompt" {
		t.Fatalf("promptBefore = %q,%v want %q,true", got, ok, "the real prompt")
	}
	if _, ok := promptBefore([][]byte{[]byte(`{"type":"event_msg","payload":{"type":"token_count"}}`)}); ok {
		t.Errorf("no user line should be false")
	}
}

// PromptBefore reads the rollout file and returns the prompt before the assistant
// turn at the given 1-indexed line (the token_count line); missing file → false.
func TestPromptBefore_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	content := `{"timestamp":"2026-06-15T10:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"implement backoff"}]}}
{"timestamp":"2026-06-15T10:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"on it"}]}}
{"timestamp":"2026-06-15T10:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":120}}}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	// The billable turn is the token_count at physical line 3; the prompt is line 1
	// (line 2 is the assistant message and must be skipped).
	if got, ok := PromptBefore(path, 3); !ok || got != "implement backoff" {
		t.Errorf("PromptBefore(line 3) = %q,%v want %q,true", got, ok, "implement backoff")
	}
	if _, ok := PromptBefore(path, 1); ok {
		t.Errorf("PromptBefore(line 1) should be false")
	}
	if _, ok := PromptBefore(filepath.Join(dir, "gone.jsonl"), 3); ok {
		t.Errorf("PromptBefore(missing file) should be false")
	}
}
