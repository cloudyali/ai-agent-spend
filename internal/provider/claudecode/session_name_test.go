package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSessionName_SummaryWins(t *testing.T) {
	body := `{"type":"user","sessionId":"s1","message":{"role":"user","content":[{"type":"text","text":"fix the failing test"}]}}
{"type":"assistant","sessionId":"s1","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":1}}}
{"type":"summary","summary":"Fixed the failing test in payments","leafUuid":"a2"}
`
	got, ok := SessionName(writeFile(t, body))
	if !ok || got != "Fixed the failing test in payments" {
		t.Errorf("SessionName = %q,%v; want the summary title", got, ok)
	}
}

func TestSessionName_FirstPromptFallback(t *testing.T) {
	// No summary line → fall back to the first human prompt.
	body := `{"type":"user","sessionId":"s1","message":{"role":"user","content":[{"type":"text","text":"add a churn column"}]}}
{"type":"assistant","sessionId":"s1","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1}}}
`
	got, ok := SessionName(writeFile(t, body))
	if !ok || got != "add a churn column" {
		t.Errorf("SessionName = %q,%v; want the first prompt as fallback", got, ok)
	}
}

func TestSessionName_NoneAndUnreadable(t *testing.T) {
	// Only assistant/tool lines, no summary and no human prompt → not ok.
	body := `{"type":"assistant","sessionId":"s1","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":1}}}
`
	if got, ok := SessionName(writeFile(t, body)); ok {
		t.Errorf("no summary/prompt should be not-ok, got %q", got)
	}
	if _, ok := SessionName(filepath.Join(t.TempDir(), "missing.jsonl")); ok {
		t.Error("a missing file should be not-ok, never an error")
	}
}
