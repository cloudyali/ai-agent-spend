package scan

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/normalize"
	"github.com/cloudyali/ai-agent-spend/internal/pricing"
	"github.com/cloudyali/ai-agent-spend/internal/provider"
	"github.com/cloudyali/ai-agent-spend/internal/store"
)

type fakeProvider struct {
	recs    []provider.RawRecord
	readErr error
}

func (f *fakeProvider) Name() string                                 { return "claude_code" }
func (f *fakeProvider) Detect() (bool, error)                        { return true, nil }
func (f *fakeProvider) Sources() ([]provider.Source, error)          { return nil, nil }
func (f *fakeProvider) Read(time.Time) ([]provider.RawRecord, error) { return f.recs, f.readErr }

func rec(line int, raw string) provider.RawRecord {
	return provider.RawRecord{
		Provider: "claude_code",
		Source:   provider.Source{PathHash: "ph", Kind: "session_jsonl"},
		Line:     line,
		Raw:      []byte(raw),
	}
}

const (
	userLine    = `{"type":"user","sessionId":"s","message":{"role":"user","content":[]}}`
	opusLine    = `{"type":"assistant","uuid":"a1","sessionId":"s","timestamp":"2026-06-14T10:00:05Z","cwd":"/x/payments","message":{"id":"m1","model":"claude-opus-4-20250514","content":[],"usage":{"input_tokens":12400,"output_tokens":3100,"cache_read_input_tokens":8900}}}`
	sonnetLine  = `{"type":"assistant","uuid":"a2","sessionId":"s","timestamp":"2026-06-14T10:01:00Z","cwd":"/x/payments","message":{"id":"m2","model":"claude-sonnet-4","content":[],"usage":{"input_tokens":2000,"output_tokens":500}}}`
	summaryLine = `{"type":"summary","summary":"done"}`
	brokenLine  = `{not json`
)

func newScanner(s store.Store, sink store.Sink, recs []provider.RawRecord) *Scanner {
	return &Scanner{
		Provider:   &fakeProvider{recs: recs},
		Normalizer: normalize.ClaudeCode{GOOS: "linux", IdentityHash: "id"},
		Pricing:    pricing.NewEngine(),
		Plan:       pricing.Plan{Kind: "api"},
		Store:      s,
		Sink:       sink,
		Now:        func() time.Time { return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC) },
	}
}

func TestScan_CountsAndStores(t *testing.T) {
	st := store.NewMemStore()
	sc := newScanner(st, st, []provider.RawRecord{
		rec(1, userLine), rec(2, opusLine), rec(3, sonnetLine), rec(4, summaryLine), rec(5, brokenLine),
	})

	sum, err := sc.Run()
	if err != nil {
		t.Fatal(err)
	}
	if sum.Imported != 2 || sum.NotBillable != 2 || sum.Skipped != 1 {
		t.Errorf("summary = %+v, want imported 2, notbillable 2, skipped 1", sum)
	}
	if len(sum.Skips) != 1 || sum.Skips[0].Line != 5 || !strings.Contains(sum.Skips[0].Sample, "not json") {
		t.Errorf("expected one captured skip with sample, got %+v", sum.Skips)
	}
	got, _ := st.Query(store.Filter{})
	if len(got) != 2 {
		t.Fatalf("stored %d events, want 2", len(got))
	}
	// priced on the way through
	if got[0].CostViews.APIEquivalent == nil {
		t.Error("events should be priced after scan")
	}
	if sum.Since.IsZero() || sum.Until.IsZero() {
		t.Error("summary period not set")
	}
	if last, _ := st.LastScan("claude_code"); last.IsZero() {
		t.Error("last-scan not recorded")
	}
}

// The pipeline stamps GitSHA best-effort: when the normalizer has a reflog hook,
// the enrich stage resolves each turn's repo and records the commit that was HEAD.
func TestScan_EnrichesGitSHABestEffort(t *testing.T) {
	st := store.NewMemStore()
	sc := &Scanner{
		Provider: &fakeProvider{recs: []provider.RawRecord{rec(2, opusLine)}},
		Normalizer: normalize.ClaudeCode{
			GOOS: "linux", IdentityHash: "id",
			RepoRoot: func(p string) string {
				if strings.HasPrefix(p, "/x/payments") {
					return "/x/payments"
				}
				return ""
			},
			HeadAt: func(root string, _ time.Time) (string, bool) {
				if root == "/x/payments" {
					return "deadbeefcafe1234", true
				}
				return "", false
			},
		},
		Pricing: pricing.NewEngine(),
		Plan:    pricing.Plan{Kind: "api"},
		Store:   st, Sink: st,
		Now: func() time.Time { return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC) },
	}
	if _, err := sc.Run(); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Query(store.Filter{})
	if len(got) != 1 {
		t.Fatalf("stored %d events, want 1", len(got))
	}
	if got[0].GitSHA != "deadbeefcafe1234" {
		t.Errorf("GitSHA = %q, want deadbeefcafe1234 (enriched in pipeline)", got[0].GitSHA)
	}
}

// Two JSONL lines for one Claude Code response (same message.id + requestId):
// a streaming placeholder (input 1) and the full turn (input 12400). The scan
// must keep only the full one — the bug this guards against is summing them.
const (
	placeholderLine = `{"type":"assistant","uuid":"a1","sessionId":"s","requestId":"r1","timestamp":"2026-06-14T10:00:05Z","cwd":"/x/payments","message":{"id":"m1","model":"claude-opus-4","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`
	fullTurnLine    = `{"type":"assistant","uuid":"a2","sessionId":"s","requestId":"r1","timestamp":"2026-06-14T10:00:06Z","cwd":"/x/payments","message":{"id":"m1","model":"claude-opus-4","content":[],"usage":{"input_tokens":12400,"output_tokens":3100,"cache_read_input_tokens":8900}}}`
)

func TestScan_DedupesStreamingPlaceholders(t *testing.T) {
	st := store.NewMemStore()
	sc := newScanner(st, st, []provider.RawRecord{
		rec(1, placeholderLine), rec(2, fullTurnLine),
	})
	sum, err := sc.Run()
	if err != nil {
		t.Fatal(err)
	}
	if sum.Imported != 1 || sum.Deduped != 1 {
		t.Errorf("summary = %+v, want imported 1, deduped 1", sum)
	}
	got, _ := st.Query(store.Filter{})
	if len(got) != 1 {
		t.Fatalf("stored %d events, want 1 (placeholders collapsed)", len(got))
	}
	if got[0].Tokens.Input != 12400 {
		t.Errorf("kept Input %d, want 12400 — the 0/1 placeholder must lose to the full turn", got[0].Tokens.Input)
	}
}

type errSink struct{}

func (errSink) Write([]event.AgentEvent) error { return errors.New("sink boom") }

func TestScan_ReadErrorPropagates(t *testing.T) {
	st := store.NewMemStore()
	sc := newScanner(st, st, nil)
	sc.Provider = &fakeProvider{readErr: errors.New("read boom")}
	if _, err := sc.Run(); err == nil {
		t.Error("expected read error to propagate")
	}
}

func TestScan_WriteErrorPropagates(t *testing.T) {
	st := store.NewMemStore()
	sc := newScanner(st, errSink{}, []provider.RawRecord{rec(2, opusLine)})
	if _, err := sc.Run(); err == nil {
		t.Error("expected write error to propagate")
	}
}

func TestScan_IsIdempotent(t *testing.T) {
	st := store.NewMemStore()
	recs := []provider.RawRecord{rec(2, opusLine), rec(3, sonnetLine)}

	if _, err := newScanner(st, st, recs).Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := newScanner(st, st, recs).Run(); err != nil { // re-scan
		t.Fatal(err)
	}
	got, _ := st.Query(store.Filter{})
	if len(got) != 2 {
		t.Errorf("re-scan produced %d events, want 2 (idempotent)", len(got))
	}
}
