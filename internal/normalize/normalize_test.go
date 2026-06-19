package normalize

import (
	"errors"
	"strings"
	"testing"
	"time"

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

// A subagent transcript (.../<parent>/subagents/agent-<worker>.jsonl) rolls up under
// its parent session, derived from the in-memory RawPath at scan time.
func TestNormalize_SubagentRollsUpToParent(t *testing.T) {
	n := ClaudeCode{GOOS: "linux", IdentityHash: "id"}
	r := provider.RawRecord{
		Provider: "claude_code",
		Source:   provider.Source{PathHash: "ph", RawPath: "/h/.claude/projects/proj/PARENT123/subagents/agent-w1.jsonl", Kind: "session_jsonl"},
		Line:     1,
		Raw:      []byte(opusLine),
	}
	ev, err := n.Normalize(r)
	if err != nil {
		t.Fatal(err)
	}
	if ev.SessionID != "PARENT123" {
		t.Errorf("subagent turn should roll up to its parent session, got SessionID=%q (raw was sess_1)", ev.SessionID)
	}
	if ev.SubagentID != "w1" {
		t.Errorf("SubagentID = %q, want w1", ev.SubagentID)
	}
}

func TestNormalize_NonSubagentKeepsItsSession(t *testing.T) {
	n := ClaudeCode{GOOS: "linux", IdentityHash: "id"}
	r := provider.RawRecord{
		Provider: "claude_code",
		Source:   provider.Source{PathHash: "ph", RawPath: "/h/.claude/projects/proj/SESS9.jsonl", Kind: "session_jsonl"},
		Line:     1,
		Raw:      []byte(opusLine),
	}
	ev, _ := n.Normalize(r)
	if ev.SessionID != "sess_1" || ev.SubagentID != "" {
		t.Errorf("a normal session record keeps its own session, got SessionID=%q sub=%q", ev.SessionID, ev.SubagentID)
	}
}

// Files touched: file_path/notebook_path from the turn's tool_use inputs, made
// repo-relative (no absolute/home leak), deduped + sorted. Bash (command, no
// file) is ignored; a path outside the repo degrades to its base name.
func TestNormalize_FilesTouched(t *testing.T) {
	const editLine = `{"type":"assistant","uuid":"a2","sessionId":"sess_1","timestamp":"2026-06-14T10:00:05Z","cwd":"/Users/dev/payments","message":{"id":"msg_2","model":"claude-opus-4","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/Users/dev/payments/src/app.go"}},{"type":"tool_use","name":"Read","input":{"file_path":"/Users/dev/payments/README.md"}},{"type":"tool_use","name":"Edit","input":{"file_path":"/Users/dev/payments/src/app.go"}},{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}},{"type":"tool_use","name":"Read","input":{"file_path":"/etc/hosts"}}],"usage":{"input_tokens":100,"output_tokens":10}}}`
	n := ClaudeCode{GOOS: "linux", IdentityHash: "id"}
	ev, err := n.Normalize(rec(editLine, 3))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ev.Files, ","); got != "README.md,hosts,src/app.go" {
		t.Errorf("Files = %q, want \"README.md,hosts,src/app.go\" (repo-relative, deduped, sorted; Bash skipped; /etc/hosts → base)", got)
	}
	if strings.Contains(strings.Join(ev.Files, ","), "/Users/") {
		t.Errorf("absolute/home path leaked into Files: %v", ev.Files)
	}
}

// gitBranch rides on every Claude Code line; we capture it verbatim (GitSHA is
// resolved later, at scan time, from the repo's reflog — never here).
func TestNormalize_CapturesGitBranch(t *testing.T) {
	n := ClaudeCode{GOOS: "linux", IdentityHash: "id_test"}

	withBranch := `{"type":"assistant","uuid":"a1","sessionId":"s","timestamp":"2026-06-14T10:00:05Z","cwd":"/Users/dev/payments","gitBranch":"feature/payments-retry","message":{"id":"m1","role":"assistant","model":"claude-opus-4","content":[],"usage":{"input_tokens":1}}}`
	ev, err := n.Normalize(rec(withBranch, 1))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.GitBranch != "feature/payments-retry" {
		t.Errorf("GitBranch = %q, want feature/payments-retry", ev.GitBranch)
	}
	if ev.GitSHA != "" {
		t.Errorf("GitSHA must stay empty in normalize (resolved at scan), got %q", ev.GitSHA)
	}

	// No gitBranch on the line → empty, not asserted.
	ev2, err := n.Normalize(rec(opusLine, 2))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev2.GitBranch != "" {
		t.Errorf("GitBranch = %q, want empty when absent", ev2.GitBranch)
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

// EnrichVCS stamps GitSHA best-effort by asking the injected HeadAt what commit was
// HEAD at each turn's timestamp, resolving the repo root from the raw records (cwd
// for terminal sessions, dominant edited-file root for Cowork/no-cwd turns).
func TestEnrichVCS_StampsSHAPerTurnTimestamp(t *testing.T) {
	noon := mustTime(t, "2026-06-14T12:00:00Z")
	// HEAD was shaOld before noon, shaNew at/after noon — proves per-turn resolution.
	headAt := func(root string, ts time.Time) (string, bool) {
		if root != "/repo/payments" {
			return "", false
		}
		if ts.Before(noon) {
			return "sha_old", true
		}
		return "sha_new", true
	}
	repoRoot := underRoots("/repo/payments", "/repo/web")
	n := ClaudeCode{GOOS: "linux", RepoRoot: repoRoot, HeadAt: headAt}

	mk := func(ph, ts string) (event.AgentEvent, provider.RawRecord) {
		raw := `{"type":"assistant","sessionId":"S","timestamp":"` + ts + `","cwd":"/repo/payments","message":{"id":"m` + ph + `","role":"assistant","model":"claude-opus-4","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/repo/payments/x.go"}}],"usage":{"input_tokens":1}}}`
		r := provider.RawRecord{Provider: "claude_code", Source: provider.Source{PathHash: ph}, Raw: []byte(raw)}
		ev, err := n.Normalize(r)
		if err != nil {
			t.Fatalf("normalize: %v", err)
		}
		return ev, r
	}
	evEarly, rEarly := mk("early", "2026-06-14T09:00:00Z")
	evLate, rLate := mk("late", "2026-06-14T15:00:00Z")

	out := n.EnrichVCS([]event.AgentEvent{evEarly, evLate}, []provider.RawRecord{rEarly, rLate})
	if out[0].GitSHA != "sha_old" {
		t.Errorf("early turn GitSHA = %q, want sha_old", out[0].GitSHA)
	}
	if out[1].GitSHA != "sha_new" {
		t.Errorf("late turn GitSHA = %q, want sha_new", out[1].GitSHA)
	}
}

func TestEnrichVCS_CoworkResolvesViaEditedFiles(t *testing.T) {
	// Cowork cwd is the desktop outputs dir (no repo); the SHA must resolve via the
	// dominant edited-file root, same signal AttributeProjects uses for the project.
	headAt := func(root string, _ time.Time) (string, bool) {
		if root == "/repo/web" {
			return "sha_web", true
		}
		return "", false
	}
	n := ClaudeCode{GOOS: "linux", RepoRoot: underRoots("/repo/web"), HeadAt: headAt}
	cwd := "/Users/x/Library/Application Support/Claude/local-agent-mode-sessions/ws/conv/local_s/outputs"
	raw := `{"type":"assistant","sessionId":"S","timestamp":"2026-06-14T10:00:00Z","cwd":"` + cwd + `","message":{"id":"m","role":"assistant","model":"claude-opus-4-8","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/repo/web/a.tsx"}}],"usage":{"input_tokens":1}}}`
	r := provider.RawRecord{Provider: "claude_code", Source: provider.Source{PathHash: "fileC"}, Raw: []byte(raw)}
	ev, err := n.Normalize(r)
	if err != nil {
		t.Fatal(err)
	}
	out := n.EnrichVCS([]event.AgentEvent{ev}, []provider.RawRecord{r})
	if out[0].GitSHA != "sha_web" {
		t.Errorf("Cowork GitSHA = %q, want sha_web (via edited-file root)", out[0].GitSHA)
	}
}

func TestEnrichVCS_BestEffortNoOps(t *testing.T) {
	raw := `{"type":"assistant","sessionId":"S","timestamp":"2026-06-14T10:00:00Z","cwd":"/repo/payments","message":{"id":"m","role":"assistant","model":"claude-opus-4","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/repo/payments/x.go"}}],"usage":{"input_tokens":1}}}`
	r := provider.RawRecord{Provider: "claude_code", Source: provider.Source{PathHash: "fileD"}, Raw: []byte(raw)}

	t.Run("no HeadAt hook → untouched", func(t *testing.T) {
		n := ClaudeCode{GOOS: "linux", RepoRoot: underRoots("/repo/payments")}
		ev, _ := n.Normalize(r)
		out := n.EnrichVCS([]event.AgentEvent{ev}, []provider.RawRecord{r})
		if out[0].GitSHA != "" {
			t.Errorf("GitSHA = %q, want empty without a HeadAt hook", out[0].GitSHA)
		}
	})

	t.Run("repo not resolvable / HeadAt misses → empty, not asserted", func(t *testing.T) {
		n := ClaudeCode{GOOS: "linux", RepoRoot: underRoots("/somewhere/else"),
			HeadAt: func(string, time.Time) (string, bool) { return "", false }}
		ev, _ := n.Normalize(r)
		out := n.EnrichVCS([]event.AgentEvent{ev}, []provider.RawRecord{r})
		if out[0].GitSHA != "" {
			t.Errorf("GitSHA = %q, want empty when unresolved", out[0].GitSHA)
		}
	})
}

// Churn is captured once per session, over the commit range the session spanned
// (first turn's SHA → last turn's SHA), and stamped on the representative (earliest)
// event so a per-file rollup never double-counts it.
func TestEnrichVCS_StampsSessionChurnOncePerSession(t *testing.T) {
	noon := mustTime(t, "2026-06-14T12:00:00Z")
	headAt := func(root string, ts time.Time) (string, bool) {
		if root != "/repo/pay" {
			return "", false
		}
		if ts.Before(noon) {
			return "sha1", true
		}
		return "sha2", true
	}
	var gotFrom, gotTo string
	var gotFiles []string
	churn := func(root, from, to string, files []string) []event.FileChurn {
		gotFrom, gotTo, gotFiles = from, to, files
		return []event.FileChurn{{Path: "x.go", Added: 5, Removed: 2}}
	}
	n := ClaudeCode{GOOS: "linux", RepoRoot: underRoots("/repo/pay"), HeadAt: headAt, Churn: churn}
	mk := func(ph, ts string) (event.AgentEvent, provider.RawRecord) {
		raw := `{"type":"assistant","sessionId":"S","timestamp":"` + ts + `","cwd":"/repo/pay","message":{"id":"m` + ph + `","role":"assistant","model":"claude-opus-4","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/repo/pay/x.go"}}],"usage":{"input_tokens":1}}}`
		r := provider.RawRecord{Provider: "claude_code", Source: provider.Source{PathHash: ph}, Raw: []byte(raw)}
		ev, err := n.Normalize(r)
		if err != nil {
			t.Fatal(err)
		}
		return ev, r
	}
	e1, r1 := mk("a", "2026-06-14T09:00:00Z")
	e2, r2 := mk("b", "2026-06-14T15:00:00Z")

	out := n.EnrichVCS([]event.AgentEvent{e1, e2}, []provider.RawRecord{r1, r2})
	if len(out[0].SessionChurn) != 1 || out[0].SessionChurn[0].Added != 5 {
		t.Errorf("churn should be stamped on the earliest event, got %+v", out[0].SessionChurn)
	}
	if out[1].SessionChurn != nil {
		t.Errorf("churn must be stamped once per session, not on later turns: %+v", out[1].SessionChurn)
	}
	if gotFrom != "sha1" || gotTo != "sha2" {
		t.Errorf("churn range = %s..%s, want sha1..sha2", gotFrom, gotTo)
	}
	if len(gotFiles) != 1 || gotFiles[0] != "x.go" {
		t.Errorf("churn files = %v, want [x.go] (repo-relative union)", gotFiles)
	}
}

func TestEnrichVCS_NoChurnWhenNoCommitDuringSession(t *testing.T) {
	headAt := func(string, time.Time) (string, bool) { return "samesha", true }
	called := false
	churn := func(string, string, string, []string) []event.FileChurn { called = true; return nil }
	n := ClaudeCode{GOOS: "linux", RepoRoot: underRoots("/repo/pay"), HeadAt: headAt, Churn: churn}
	mk := func(ph, ts string) (event.AgentEvent, provider.RawRecord) {
		raw := `{"type":"assistant","sessionId":"S","timestamp":"` + ts + `","cwd":"/repo/pay","message":{"id":"m` + ph + `","role":"assistant","model":"claude-opus-4","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/repo/pay/x.go"}}],"usage":{"input_tokens":1}}}`
		r := provider.RawRecord{Provider: "claude_code", Source: provider.Source{PathHash: ph}, Raw: []byte(raw)}
		ev, err := n.Normalize(r)
		if err != nil {
			t.Fatal(err)
		}
		return ev, r
	}
	e1, r1 := mk("a", "2026-06-14T09:00:00Z")
	e2, r2 := mk("b", "2026-06-14T15:00:00Z")
	out := n.EnrichVCS([]event.AgentEvent{e1, e2}, []provider.RawRecord{r1, r2})
	if called {
		t.Error("Churn must not run when the session stayed on one commit (fromSHA==toSHA)")
	}
	if out[0].SessionChurn != nil {
		t.Errorf("no churn expected, got %+v", out[0].SessionChurn)
	}
}

// A session that committed but edited no files must NOT get churn: with no files to
// scope to, a numstat would return the whole repo's range churn — unrelated work
// wrongly attributed to this sitting. So Churn is skipped entirely.
func TestEnrichVCS_NoChurnWhenSessionTouchedNoFiles(t *testing.T) {
	noon := mustTime(t, "2026-06-14T12:00:00Z")
	headAt := func(_ string, ts time.Time) (string, bool) {
		if ts.Before(noon) {
			return "sha1", true
		}
		return "sha2", true
	}
	called := false
	churn := func(string, string, string, []string) []event.FileChurn {
		called = true
		return []event.FileChurn{{Path: "unrelated.go", Added: 99}}
	}
	n := ClaudeCode{GOOS: "linux", RepoRoot: underRoots("/repo/pay"), HeadAt: headAt, Churn: churn}
	mk := func(ph, ts string) (event.AgentEvent, provider.RawRecord) {
		raw := `{"type":"assistant","sessionId":"S","timestamp":"` + ts + `","cwd":"/repo/pay","message":{"id":"m` + ph + `","role":"assistant","model":"claude-opus-4","content":[],"usage":{"input_tokens":1}}}`
		r := provider.RawRecord{Provider: "claude_code", Source: provider.Source{PathHash: ph}, Raw: []byte(raw)}
		ev, err := n.Normalize(r)
		if err != nil {
			t.Fatal(err)
		}
		return ev, r
	}
	e1, r1 := mk("a", "2026-06-14T09:00:00Z")
	e2, r2 := mk("b", "2026-06-14T15:00:00Z")
	out := n.EnrichVCS([]event.AgentEvent{e1, e2}, []provider.RawRecord{r1, r2})
	if called {
		t.Error("Churn must not run for a session that edited no files (would attribute unrelated repo churn)")
	}
	if out[0].SessionChurn != nil {
		t.Errorf("no churn expected, got %+v", out[0].SessionChurn)
	}
}

// underRoots returns a fake RepoRoot hook: a path resolves to the first known root
// that contains it (mirrors walking up to a .git dir).
func underRoots(roots ...string) func(string) string {
	return func(p string) string {
		for _, r := range roots {
			if p == r || strings.HasPrefix(p, r+"/") {
				return r
			}
		}
		return ""
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
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
