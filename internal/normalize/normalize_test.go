package normalize

import (
	"errors"
	"strings"
	"testing"

	"github.com/agentspend/ai-agent-spend/internal/event"
	"github.com/agentspend/ai-agent-spend/internal/provider"
)

const opusLine = `{"type":"assistant","uuid":"a1","sessionId":"sess_1","timestamp":"2026-06-14T10:00:05Z","cwd":"/Users/dev/payments","message":{"id":"msg_1","role":"assistant","model":"claude-opus-4-20250514","content":[{"type":"text","text":"hi"},{"type":"tool_use","id":"t1","name":"Edit"},{"type":"tool_use","id":"t2","name":"mcp__github__create_issue"}],"usage":{"input_tokens":12400,"output_tokens":3100,"cache_read_input_tokens":8900,"cache_creation_input_tokens":0}}}`

func rec(raw string, line int) provider.RawRecord {
	return provider.RawRecord{
		Provider: "claude_code",
		Source:   provider.Source{PathHash: "ph_test", Kind: "session_jsonl"},
		Line:     line,
		Raw:      []byte(raw),
	}
}

func TestNormalize_MapsAssistantTurn(t *testing.T) {
	n := ClaudeCode{GOOS: "linux", IdentityHash: "id_test"}
	ev, err := n.Normalize(rec(opusLine, 2))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(ev.EventID, "evt_") {
		t.Errorf("EventID = %q, want evt_ prefix", ev.EventID)
	}
	if ev.Provider != "claude_code" || ev.Surface != "coding_agent" || ev.Mode != "agent" {
		t.Errorf("identity fields wrong: %+v", ev)
	}
	if ev.Repo != "payments" || ev.Project != "payments" {
		t.Errorf("repo/project = %q/%q, want payments", ev.Repo, ev.Project)
	}
	if ev.Model != "claude-opus-4" {
		t.Errorf("Model = %q, want canonical claude-opus-4", ev.Model)
	}
	if ev.Tokens.Input != 12400 || ev.Tokens.Output != 3100 || ev.Tokens.CacheRead != 8900 {
		t.Errorf("tokens wrong: %+v", ev.Tokens)
	}
	if len(ev.Tools) != 2 || ev.Tools[0] != "Edit" {
		t.Errorf("Tools = %v, want [Edit mcp__github__create_issue]", ev.Tools)
	}
	if len(ev.MCPServers) != 1 || ev.MCPServers[0] != "github" {
		t.Errorf("MCPServers = %v, want [github]", ev.MCPServers)
	}
	// provenance
	if ev.Evidence.SourcePathHash != "ph_test" || ev.Evidence.SourceLine != 2 {
		t.Errorf("source provenance wrong: %+v", ev.Evidence)
	}
	if ev.Evidence.ParserName != "claude_code" || ev.Evidence.ParserVersion != "v1" {
		t.Errorf("parser provenance wrong: %+v", ev.Evidence)
	}
	// DedupeKey is now the semantic identity of the turn (message.id|requestId),
	// not the per-line EventID. opusLine has message id "msg_1" and no requestId.
	if ev.Evidence.DedupeKey != "msg_1|" || ev.Evidence.ReconciliationStatus != "local_only" {
		t.Errorf("dedupe/recon wrong: %+v", ev.Evidence)
	}
	// cwd is hashed, never raw
	if ev.CWDHash == "" || strings.Contains(ev.CWDHash, "/") {
		t.Errorf("CWDHash should be a non-empty hash, got %q", ev.CWDHash)
	}
}

func TestNormalize_AttributeStampsProjectAndCostTag(t *testing.T) {
	n := ClaudeCode{GOOS: "linux", IdentityHash: "id", Attribute: func(string) (string, string) {
		return "payments-service", "team-payments"
	}}
	ev, err := n.Normalize(rec(opusLine, 2))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Project != "payments-service" || ev.CostTag != "team-payments" {
		t.Errorf("attribution not stamped: project=%q cost_tag=%q", ev.Project, ev.CostTag)
	}
	if ev.Repo != "payments" { // repo stays the directory base, independent of .aispend.toml
		t.Errorf("repo = %q, want payments", ev.Repo)
	}
}

// TestNormalize_CapturesReportedCostUSD: when Claude Code writes a costUSD on the
// line, we capture it as the Reported cost view (USD micros). This is the
// reported-else-computed (ccusage "Auto") path's input.
func TestNormalize_CapturesReportedCostUSD(t *testing.T) {
	n := ClaudeCode{GOOS: "linux"}
	raw := `{"type":"assistant","uuid":"a1","sessionId":"s","requestId":"r1","timestamp":"2026-06-14T10:00:05Z","costUSD":0.4325,"message":{"id":"m1","model":"claude-opus-4","content":[],"usage":{"input_tokens":12400,"output_tokens":3100}}}`
	ev, err := n.Normalize(rec(raw, 2))
	if err != nil {
		t.Fatal(err)
	}
	if ev.CostViews.Reported == nil {
		t.Fatal("Reported cost view should be set from costUSD")
	}
	if ev.CostViews.Reported.Micros != 432_500 || ev.CostViews.Reported.Currency != "USD" {
		t.Errorf("Reported = %+v, want 432500 micros USD ($0.4325)", *ev.CostViews.Reported)
	}
}

// A zero or absent costUSD must NOT create a Reported view (nil = not computable,
// never a misleading $0).
func TestNormalize_ZeroOrAbsentCostUSDLeavesReportedNil(t *testing.T) {
	n := ClaudeCode{GOOS: "linux"}
	zero := `{"type":"assistant","uuid":"a1","sessionId":"s","message":{"id":"m1","model":"claude-opus-4","content":[],"usage":{"input_tokens":10},"costUSD":0}}`
	for name, raw := range map[string]string{"absent": opusLine, "zero": zero} {
		t.Run(name, func(t *testing.T) {
			ev, err := n.Normalize(rec(raw, 1))
			if err != nil {
				t.Fatal(err)
			}
			if ev.CostViews.Reported != nil {
				t.Errorf("Reported should be nil for %s costUSD, got %+v", name, *ev.CostViews.Reported)
			}
		})
	}
}

func TestNormalize_SkipsNonBillable(t *testing.T) {
	n := ClaudeCode{GOOS: "linux"}
	user := `{"type":"user","sessionId":"s","message":{"role":"user","content":[]}}`
	summary := `{"type":"summary","summary":"x"}`
	assistantNoUsage := `{"type":"assistant","message":{"role":"assistant","model":"claude-opus-4"}}`
	for name, raw := range map[string]string{"user": user, "summary": summary, "assistant-no-usage": assistantNoUsage} {
		t.Run(name, func(t *testing.T) {
			_, err := n.Normalize(rec(raw, 1))
			if !errors.Is(err, ErrNotBillable) {
				t.Errorf("expected ErrNotBillable, got %v", err)
			}
		})
	}
}

func TestNormalize_UnrecognizedFormatErrors(t *testing.T) {
	n := ClaudeCode{GOOS: "linux"}
	_, err := n.Normalize(rec(`{not valid json`, 1))
	if err == nil || errors.Is(err, ErrNotBillable) {
		t.Errorf("expected a parse error (not ErrNotBillable), got %v", err)
	}
}

func TestCanonicalModel(t *testing.T) {
	cases := map[string]string{
		"claude-opus-4-20250514":   "claude-opus-4",
		"claude-sonnet-4-20250514": "claude-sonnet-4",
		"claude-opus-4":            "claude-opus-4",
		"gpt-5":                    "gpt-5",
	}
	for in, want := range cases {
		if got := canonicalModel(in); got != want {
			t.Errorf("canonicalModel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestNormalize_SemanticDedupeKey is the core of the streaming-placeholder fix:
// Claude Code writes several JSONL lines for one API response, sharing a
// (message.id, requestId). They must produce the SAME dedupe key and EventID so
// the keep-max pass can collapse them into one true event.
func TestNormalize_SemanticDedupeKey(t *testing.T) {
	n := ClaudeCode{GOOS: "linux", IdentityHash: "id"}
	l1 := `{"type":"assistant","uuid":"a1","sessionId":"s","requestId":"req_9","timestamp":"2026-06-14T10:00:05Z","message":{"id":"msg_1","model":"claude-opus-4","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`
	l2 := `{"type":"assistant","uuid":"a2","sessionId":"s","requestId":"req_9","timestamp":"2026-06-14T10:00:06Z","message":{"id":"msg_1","model":"claude-opus-4","content":[],"usage":{"input_tokens":12400,"output_tokens":3100}}}`
	e1, err := n.Normalize(rec(l1, 2))
	if err != nil {
		t.Fatal(err)
	}
	e2, err := n.Normalize(rec(l2, 3))
	if err != nil {
		t.Fatal(err)
	}
	if e1.Evidence.DedupeKey != "msg_1|req_9" {
		t.Errorf("DedupeKey = %q, want msg_1|req_9", e1.Evidence.DedupeKey)
	}
	if e1.EventID != e2.EventID {
		t.Errorf("streaming lines of one response got different EventIDs: %s vs %s", e1.EventID, e2.EventID)
	}
}

// TestNormalize_DedupeKeyFallsBackWhenNoMessageID guards the fallback: a line
// without a message id must stay per-line unique so unrelated turns never merge.
func TestNormalize_DedupeKeyFallsBackWhenNoMessageID(t *testing.T) {
	n := ClaudeCode{GOOS: "linux"}
	raw := `{"type":"assistant","sessionId":"s","timestamp":"2026-06-14T10:00:05Z","message":{"model":"claude-opus-4","content":[],"usage":{"input_tokens":10}}}`
	a, err := n.Normalize(rec(raw, 2))
	if err != nil {
		t.Fatal(err)
	}
	b, err := n.Normalize(rec(raw, 3))
	if err != nil {
		t.Fatal(err)
	}
	if a.EventID == b.EventID {
		t.Error("unkeyed lines must not collapse — distinct EventIDs by source line")
	}
}

// TestDedupe_KeepsMaxTokenEntryPerKey proves the keep-max rule: among entries
// sharing a dedupe key, the one with the largest token total wins (so the 0/1
// streaming placeholders lose to the full response), first-appearance order kept.
func TestDedupe_KeepsMaxTokenEntryPerKey(t *testing.T) {
	mk := func(key string, in int64) event.AgentEvent {
		return event.AgentEvent{
			EventID:  "evt_" + key,
			Tokens:   event.Tokens{Input: in},
			Evidence: event.Evidence{DedupeKey: key},
		}
	}
	in := []event.AgentEvent{
		mk("k1", 1),     // streaming placeholder
		mk("k1", 0),     // streaming placeholder
		mk("k1", 12400), // the real response
		mk("k2", 500),
	}
	out := ClaudeCode{}.Dedupe(in)
	if len(out) != 2 {
		t.Fatalf("Dedupe returned %d events, want 2", len(out))
	}
	if out[0].Evidence.DedupeKey != "k1" || out[0].Tokens.Input != 12400 {
		t.Errorf("k1 kept %+v, want the input=12400 entry", out[0].Tokens)
	}
	if out[1].Evidence.DedupeKey != "k2" {
		t.Errorf("first-appearance order not preserved: %+v", out)
	}
}

// Cowork sessions carry cwd = the desktop app's outputs dir, so the project must
// be inferred from the files the session edited (the chosen approach). The
// dominant edited repo wins; minority repos do not.
func TestAttributeProjects_CoworkInfersFromEditedFiles(t *testing.T) {
	cwd := "/Users/x/Library/Application Support/Claude/local-agent-mode-sessions/ws/conv/local_s/outputs"
	line := func(file string) string {
		return `{"type":"assistant","sessionId":"S1","cwd":"` + cwd + `","message":{"id":"m","role":"assistant","model":"claude-opus-4-8","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"` + file + `"}}],"usage":{"input_tokens":1}}}`
	}
	recs := []provider.RawRecord{
		rec(line("/work/myrepo/internal/a.go"), 1),
		rec(line("/work/myrepo/internal/b.go"), 2),
		rec(line("/work/other/z.go"), 3), // minority repo must not win
	}
	n := ClaudeCode{GOOS: "linux", RepoRoot: func(p string) string {
		if strings.HasPrefix(p, "/work/myrepo") {
			return "/work/myrepo"
		}
		return "/work/other"
	}}
	events := []event.AgentEvent{{Project: "outputs", Repo: "outputs", Evidence: event.Evidence{SourcePathHash: "ph_test"}}}
	got := n.AttributeProjects(events, recs)
	if got[0].Project != "myrepo" || got[0].Repo != "myrepo" {
		t.Errorf("project/repo = %q/%q, want myrepo (dominant edited repo)", got[0].Project, got[0].Repo)
	}
}

// A terminal Claude Code session (cwd is a real repo) keeps its normalize-time
// attribution untouched — inference only applies to Cowork's placeholder cwd.
func TestAttributeProjects_TerminalSessionUntouched(t *testing.T) {
	raw := `{"type":"assistant","sessionId":"T1","cwd":"/home/x/payments","message":{"id":"m","role":"assistant","model":"claude-opus-4-8","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/somewhere/else/x.go"}}],"usage":{"input_tokens":1}}}`
	n := ClaudeCode{GOOS: "linux", RepoRoot: func(p string) string { return "/somewhere/else" }}
	events := []event.AgentEvent{{SessionID: "T1", Project: "payments", Repo: "payments"}}
	got := n.AttributeProjects(events, []provider.RawRecord{rec(raw, 1)})
	if got[0].Project != "payments" {
		t.Errorf("terminal project = %q, want untouched 'payments'", got[0].Project)
	}
}

// Subagent/sidechain turns carry no cwd, so normalize leaves repo empty — the
// biggest unattributed bucket on real data. They must still be inferred from the
// files they edited, even though cwd is absent (no Cowork marker to gate on).
func TestAttributeProjects_NoCwdTurnInferredFromFiles(t *testing.T) {
	raw := `{"type":"assistant","sessionId":"SUB","message":{"id":"m","role":"assistant","model":"claude-opus-4-8","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/work/myrepo/x.go"}}],"usage":{"input_tokens":1}}}`
	n := ClaudeCode{GOOS: "linux", RepoRoot: func(p string) string { return "/work/myrepo" }}
	events := []event.AgentEvent{{Repo: "", Project: "", Evidence: event.Evidence{SourcePathHash: "ph_test"}}}
	got := n.AttributeProjects(events, []provider.RawRecord{rec(raw, 1)})
	if got[0].Repo != "myrepo" || got[0].Project != "myrepo" {
		t.Errorf("no-cwd event = %q/%q, want inferred 'myrepo'", got[0].Project, got[0].Repo)
	}
}

// Two session-less transcript files editing different repos must EACH get their own
// repo — never collapse into one bucket. Guards the real-data bug where 34,737
// no-cwd turns all had MISSING sessionId and were dumped into a single repo.
func TestAttributeProjects_KeyedPerFileNotSession(t *testing.T) {
	rr := func(p string) string { return p[:strings.LastIndex(p, "/")] } // file → its dir
	mk := func(ph, file string) provider.RawRecord {
		raw := `{"type":"assistant","message":{"id":"m","role":"assistant","model":"claude-opus-4-8","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"` + file + `"}}],"usage":{"input_tokens":1}}}`
		return provider.RawRecord{Provider: "claude_code", Source: provider.Source{PathHash: ph}, Raw: []byte(raw)}
	}
	recs := []provider.RawRecord{mk("fileA", "/work/alpha/a.go"), mk("fileB", "/work/beta/b.go")}
	n := ClaudeCode{GOOS: "linux", RepoRoot: rr}
	events := []event.AgentEvent{
		{Repo: "", Evidence: event.Evidence{SourcePathHash: "fileA"}},
		{Repo: "", Evidence: event.Evidence{SourcePathHash: "fileB"}},
	}
	got := n.AttributeProjects(events, recs)
	if got[0].Repo != "alpha" || got[1].Repo != "beta" {
		t.Errorf("per-file attribution = %q,%q; want alpha,beta (no cross-file collapse)", got[0].Repo, got[1].Repo)
	}
}

// A real repo set at normalize time is never overwritten by inference, even when
// the session's edits point elsewhere — only empty/"outputs" placeholders are.
func TestAttributeProjects_RealRepoNeverOverwritten(t *testing.T) {
	raw := `{"type":"assistant","sessionId":"K","cwd":"/home/x/payments","message":{"id":"m","role":"assistant","model":"claude-opus-4-8","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/elsewhere/other/z.go"}}],"usage":{"input_tokens":1}}}`
	n := ClaudeCode{GOOS: "linux", RepoRoot: func(p string) string { return "/elsewhere/other" }}
	events := []event.AgentEvent{{SessionID: "K", Repo: "payments", Project: "payments"}}
	got := n.AttributeProjects(events, []provider.RawRecord{rec(raw, 1)})
	if got[0].Repo != "payments" {
		t.Errorf("real repo = %q, want left as 'payments'", got[0].Repo)
	}
}

func TestEventID_StableAndLineSensitive(t *testing.T) {
	a := eventID("ph", 2, "s1")
	b := eventID("ph", 2, "s1")
	c := eventID("ph", 3, "s1")
	if a != b {
		t.Error("eventID should be stable for identical inputs")
	}
	if a == c {
		t.Error("eventID should differ when the line differs")
	}
	if !strings.HasPrefix(a, "evt_") || len(a) != len("evt_")+16 {
		t.Errorf("eventID format = %q", a)
	}
}

// One-hour cache tier: the normalizer must read the split cache_creation object
// and record the 1-hour subset separately so pricing can charge it at 2× input.
func TestNormalize_OneHourCacheTier(t *testing.T) {
	line := `{"type":"assistant","uuid":"a9","sessionId":"s","timestamp":"2026-06-14T10:00:05Z","cwd":"/x/p","message":{"id":"m9","role":"assistant","model":"claude-opus-4-20250514","content":[],"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":0,"cache_creation_input_tokens":100000,"cache_creation":{"ephemeral_5m_input_tokens":30000,"ephemeral_1h_input_tokens":70000}}}}`
	n := ClaudeCode{GOOS: "linux"}
	ev, err := n.Normalize(rec(line, 1))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Tokens.CacheWrite != 100000 {
		t.Errorf("CacheWrite (total) = %d, want 100000", ev.Tokens.CacheWrite)
	}
	if ev.Tokens.CacheWrite1h != 70000 {
		t.Errorf("CacheWrite1h = %d, want 70000", ev.Tokens.CacheWrite1h)
	}
}

func TestCacheCreation(t *testing.T) {
	cases := []struct{ legacy, fm, oh, wantTotal, wantOH int64 }{
		{100000, 30000, 70000, 100000, 70000}, // legacy == split
		{0, 30000, 70000, 100000, 70000},      // no legacy → use split
		{50000, 0, 0, 50000, 0},               // flat cache, no split
		{0, 0, 0, 0, 0},                       // nothing
		{60000, 30000, 70000, 100000, 70000},  // legacy < split → keep larger
	}
	for _, c := range cases {
		tot, oh := cacheCreation(c.legacy, c.fm, c.oh)
		if tot != c.wantTotal || oh != c.wantOH {
			t.Errorf("cacheCreation(%d,%d,%d) = (%d,%d), want (%d,%d)", c.legacy, c.fm, c.oh, tot, oh, c.wantTotal, c.wantOH)
		}
	}
}
