// Package normalize converts a provider's RawRecord into a versioned
// event.AgentEvent with source provenance filled in. Pricing is applied
// separately by internal/pricing, so a re-price never requires a re-read.
//
// See design-documents/phase-0A-trusted-explainable-ledger.md §Normalization.
package normalize

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/agentspend/ai-agent-spend/internal/event"
	"github.com/agentspend/ai-agent-spend/internal/platform"
	"github.com/agentspend/ai-agent-spend/internal/provider"
)

// ErrNotBillable signals a well-formed record that is not a billable assistant
// turn (a user message, a summary, an assistant turn with no usage). Callers skip
// these — they are not events and not errors.
var ErrNotBillable = errors.New("normalize: record is not a billable assistant turn")

// Normalizer converts a RawRecord into an AgentEvent (pricing applied separately).
type Normalizer interface {
	Normalize(provider.RawRecord) (event.AgentEvent, error)
}

// ClaudeCode normalizes Claude Code session JSONL. GOOS and IdentityHash are
// injected so output is deterministic (golden tests) and platform-correct.
// Attribute (optional) resolves a working directory to (project, cost_tag) from
// the nearest .aispend.toml; nil falls back to the directory's base name.
type ClaudeCode struct {
	GOOS         string                                     // for path hashing; runtime.GOOS in production
	IdentityHash string                                     // hashed identity, computed once per scan
	Attribute    func(cwd string) (project, costTag string) // optional; from internal/config
	RepoRoot     func(filePath string) string               // optional; resolves a file to its repo root dir (.git/.aispend.toml). Attributes Cowork sessions whose cwd is the desktop outputs dir.
	// HeadAt (optional) reconstructs the commit that was HEAD of a repo at an
	// instant, from its reflog — the seam EnrichVCS uses to recover GitSHA (the
	// session log has none). nil disables SHA enrichment, keeping the golden/unit
	// tests filesystem-free.
	HeadAt func(repoRoot string, t time.Time) (string, bool)
	// Churn (optional) returns per-file line churn for a commit range — the one
	// git-binary dependency. nil disables churn capture; EnrichVCS then leaves
	// SessionChurn empty and the heatmap degrades to cost-only.
	Churn func(repoRoot, fromSHA, toSHA string, files []string) []event.FileChurn
	// CurrentBranch (optional) resolves a repo root to the branch HEAD points to now,
	// reading .git/HEAD. EnrichVCS uses it to rewrite the literal "HEAD" that detached
	// or Cowork sessions log (the symbolic ref, never resolved) into a real branch name,
	// so per-branch facets and the commit trailer don't see "HEAD". nil leaves it as-is.
	CurrentBranch func(repoRoot string) (string, bool)
}

const (
	parserName    = "claude_code"
	parserVersion = "v1"
)

// dateSuffix matches a trailing model snapshot date, e.g. "-20250514".
var dateSuffix = regexp.MustCompile(`-[0-9]{8}$`)

// ccContent is one item in a message's content array (text, tool_use, ...).
type ccContent struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Input struct {
		FilePath     string `json:"file_path"`     // Edit / Write / Read / MultiEdit
		NotebookPath string `json:"notebook_path"` // NotebookEdit
	} `json:"input"`
}

// ccLine is the subset of a Claude Code JSONL record we read.
type ccLine struct {
	Type      string    `json:"type"`
	UUID      string    `json:"uuid"`
	SessionID string    `json:"sessionId"`
	RequestID string    `json:"requestId"` // pairs with message.id for the dedupe key
	CostUSD   *float64  `json:"costUSD"`   // a cost Claude Code sometimes writes itself; pointer so absent ≠ 0
	Timestamp time.Time `json:"timestamp"`
	CWD       string    `json:"cwd"`
	GitBranch string    `json:"gitBranch"` // branch the turn ran on; the SHA is reconstructed later
	Message   struct {
		ID      string      `json:"id"`
		Role    string      `json:"role"`
		Model   string      `json:"model"`
		Content []ccContent `json:"content"`
		Usage   *struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			// Claude's per-TTL cache-creation split. When present it equals the
			// legacy flat total above; the 1-hour tier is priced at 2× input.
			CacheCreation *struct {
				Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
				Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

// Normalize implements Normalizer for Claude Code records.
func (n ClaudeCode) Normalize(rec provider.RawRecord) (event.AgentEvent, error) {
	var line ccLine
	if err := json.Unmarshal(rec.Raw, &line); err != nil {
		return event.AgentEvent{}, fmt.Errorf("normalize: unrecognized record format: %w", err)
	}
	if line.Type != "assistant" || line.Message.Usage == nil {
		return event.AgentEvent{}, ErrNotBillable
	}

	u := line.Message.Usage
	tools, mcp := toolsAndServers(line.Message.Content)
	files := filesTouched(line.Message.Content, line.CWD)

	var fiveMin, oneHour int64
	if cc := u.CacheCreation; cc != nil {
		fiveMin, oneHour = cc.Ephemeral5m, cc.Ephemeral1h
	}
	cacheWriteTotal, cacheWrite1h := cacheCreation(u.CacheCreationInputTokens, fiveMin, oneHour)

	// The dedupe key is the semantic identity of one API response:
	// message.id + requestId. Claude Code streams a response as several JSONL
	// lines that share this pair (most carrying input_tokens of 0 or 1), so the
	// EventID is derived from the key — not the source line — letting the keep-max
	// Dedupe pass collapse the placeholders into the one true turn. With no
	// message.id we fall back to a per-line key so unrelated turns never merge.
	key := line.Message.ID + "|" + line.RequestID
	if line.Message.ID == "" {
		key = fmt.Sprintf("%s|%d|%s", rec.Source.PathHash, rec.Line, line.SessionID)
	}
	id := eventIDFromKey(key)
	repo, project, costTag := "", "", ""
	if line.CWD != "" {
		repo = filepath.Base(line.CWD)
		project = repo // default; .aispend.toml may override
		if n.Attribute != nil {
			if p, c := n.Attribute(line.CWD); p != "" {
				project, costTag = p, c
			} else {
				costTag = c
			}
		}
	}

	// Roll a subagent transcript up under its parent session: the parent id is the
	// directory two levels up from a .../<parent>/subagents/agent-*.jsonl path,
	// available here as the in-memory RawPath (never persisted). Non-subagent records
	// pass through unchanged.
	sid, subagent := line.SessionID, ""
	if parent, worker, ok := subagentParent(rec.Source.RawPath); ok {
		if parent != "" {
			sid = parent
		}
		subagent = worker
	}

	ev := event.AgentEvent{
		SchemaVersion: event.SchemaVersion,
		EventID:       id,
		SessionID:     sid,
		SubagentID:    subagent,
		PromptID:      line.Message.ID,
		Provider:      parserName,
		Surface:       "coding_agent",
		IdentityHash:  n.IdentityHash,
		Project:       project,
		Repo:          repo,
		CostTag:       costTag,
		GitBranch:     line.GitBranch,
		CWDHash:       hashCWD(line.CWD, n.GOOS),
		Model:         canonicalModel(line.Message.Model),
		Mode:          "agent",
		Tokens: event.Tokens{
			Input:        u.InputTokens,
			Output:       u.OutputTokens,
			CacheRead:    u.CacheReadInputTokens,
			CacheWrite:   cacheWriteTotal,
			CacheWrite1h: cacheWrite1h,
		},
		Tools:      tools,
		MCPServers: mcp,
		Files:      files,
		TSStart:    line.Timestamp,
		TSEnd:      line.Timestamp,
		Evidence: event.Evidence{
			SourceType:           "local_file",
			SourceRecordID:       line.UUID,
			SourcePathHash:       rec.Source.PathHash,
			SourceLine:           rec.Line,
			ParserName:           parserName,
			ParserVersion:        parserVersion,
			DedupeKey:            key,
			ReconciliationStatus: "local_only",
		},
	}

	// A cost the tool itself wrote to disk is captured as the Reported view (the
	// pricing engine then prefers it — ccusage's "Auto"). Only when present and
	// positive: a nil/zero costUSD must never become a misleading $0.
	if line.CostUSD != nil && *line.CostUSD > 0 {
		ev.CostViews.Reported = &event.Money{
			Micros:   int64(math.Round(*line.CostUSD * 1e6)),
			Currency: "USD",
		}
	}
	return ev, nil
}

func hashCWD(cwd, goos string) string {
	if cwd == "" {
		return ""
	}
	return platform.HashPath(cwd, goos)
}

// canonicalModel strips a trailing snapshot date so "claude-opus-4-20250514"
// groups and prices as "claude-opus-4".
func canonicalModel(m string) string { return dateSuffix.ReplaceAllString(m, "") }

// cacheCreation resolves total cache-creation tokens and the 1-hour-tier subset
// from Claude's two representations: the legacy flat cache_creation_input_tokens
// and the per-TTL split (ephemeral_5m + ephemeral_1h). A valid record reports the
// two as equal; we keep the larger so a partial split never drops tokens, and
// clamp the 1-hour count to the total. Mirrors CodeBurn's extractClaudeCacheCreation.
func cacheCreation(legacy, fiveMin, oneHour int64) (total, oneHourTotal int64) {
	total = legacy
	if split := fiveMin + oneHour; split > 0 {
		if split > total {
			total = split
		}
		oneHourTotal = oneHour
		if oneHourTotal > total {
			oneHourTotal = total
		}
	}
	return total, oneHourTotal
}

// toolsAndServers collects tool-use names and the MCP servers among them
// (mcp__<server>__<tool>), de-duplicating servers and preserving order.
func toolsAndServers(content []ccContent) (tools, servers []string) {
	seen := map[string]bool{}
	for _, c := range content {
		if c.Type != "tool_use" || c.Name == "" {
			continue
		}
		tools = append(tools, c.Name)
		if strings.HasPrefix(c.Name, "mcp__") {
			if parts := strings.Split(c.Name, "__"); len(parts) >= 2 && !seen[parts[1]] {
				seen[parts[1]] = true
				servers = append(servers, parts[1])
			}
		}
	}
	return tools, servers
}

// filesTouched collects the repo-relative paths a turn operated on, from its
// file tools' inputs (Edit/Write/Read/MultiEdit → file_path, NotebookEdit →
// notebook_path). Paths are made relative to the session cwd (the repo root) so
// no absolute/home path leaks; a path outside the repo degrades to its base name.
// Deduped and sorted for a stable event.
func filesTouched(content []ccContent, cwd string) []string {
	seen := map[string]bool{}
	var files []string
	for _, c := range content {
		if c.Type != "tool_use" {
			continue
		}
		p := c.Input.FilePath
		if p == "" {
			p = c.Input.NotebookPath
		}
		rel := relativizeFile(cwd, p)
		if rel == "" || rel == "." || seen[rel] {
			continue
		}
		seen[rel] = true
		files = append(files, rel)
	}
	sort.Strings(files)
	return files
}

// relativizeFile renders a tool's file path relative to the repo (cwd), using
// forward slashes. Absolute paths under cwd become repo-relative; absolute paths
// outside it degrade to the base name (never leak an absolute/home path); already-
// relative paths pass through cleaned.
func relativizeFile(cwd, p string) string {
	if p == "" {
		return ""
	}
	p = filepath.Clean(p)
	if filepath.IsAbs(p) {
		if cwd != "" {
			if rel, err := filepath.Rel(cwd, p); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
				return filepath.ToSlash(rel)
			}
		}
		return filepath.Base(p)
	}
	return filepath.ToSlash(p)
}

// eventIDFromKey hashes a dedupe key into a stable id. 16 hex chars (64 bits)
// keeps collisions negligible across a large local ledger while staying readable
// for `explain`. Entries sharing a key share an EventID, so the idempotent Upsert
// and the keep-max Dedupe agree on identity.
func eventIDFromKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "evt_" + hex.EncodeToString(sum[:])[:16]
}

// eventID derives an id from a source reference (the no-message-id fallback).
// It is eventIDFromKey over the same "pathHash|line|sessionID" bytes, so this
// fallback id is stable and line-sensitive.
func eventID(pathHash string, line int, sessionID string) string {
	return eventIDFromKey(fmt.Sprintf("%s|%d|%s", pathHash, line, sessionID))
}

// Deduper collapses normalized events that share a dedupe key. A Normalizer may
// implement it; the scan pipeline applies it per provider before pricing. Keeping
// the dedup strategy inside each adapter is deliberate — every agent double-counts
// differently, so there is no single global rule (see design-documents §1.5).
type Deduper interface {
	Dedupe(events []event.AgentEvent) []event.AgentEvent
}

// Dedupe implements the Claude Code keep-max rule. The CLI writes a response as
// several JSONL lines sharing one (message.id, requestId); roughly 75% carry
// input_tokens of 0 or 1 (streaming placeholders), and the same message can also
// reappear across files during branch/resume. Summing them overcounts wildly, so
// among entries sharing a dedupe key we keep the single one with the largest token
// total. First-appearance order is preserved for determinism.
//
// subagentParent recognizes a Claude Code subagent transcript by its path shape
// (.../<parentSessionId>/subagents/agent-<worker>.jsonl) and returns the parent
// session id (the directory two levels up) and the worker id, so the turn can roll up
// under its parent. ok is false for any non-subagent path — including the empty
// RawPath that unit tests pass — leaving the record unchanged.
func subagentParent(rawPath string) (parent, worker string, ok bool) {
	if rawPath == "" {
		return "", "", false
	}
	dir := filepath.Dir(rawPath) // .../<parent>/subagents
	if filepath.Base(dir) != "subagents" {
		return "", "", false
	}
	parent = filepath.Base(filepath.Dir(dir)) // <parent>
	worker = strings.TrimSuffix(filepath.Base(rawPath), filepath.Ext(rawPath))
	worker = strings.TrimPrefix(worker, "agent-")
	if parent == "" || parent == "." || worker == "" {
		return "", "", false
	}
	return parent, worker, true
}

// Sidechain/subagent replay (a subagent re-emitting a parent's usage under a new
// request id) is a separate double-count handled when subagent attribution lands
// in 0B; 0A has one provider and the placeholder undercount is the live bug.
func (ClaudeCode) Dedupe(events []event.AgentEvent) []event.AgentEvent {
	pos := make(map[string]int, len(events)) // dedupe key -> index in out
	out := make([]event.AgentEvent, 0, len(events))
	for _, ev := range events {
		if i, seen := pos[ev.Evidence.DedupeKey]; seen {
			if tokenTotal(ev.Tokens) > tokenTotal(out[i].Tokens) {
				out[i] = ev
			}
			continue
		}
		pos[ev.Evidence.DedupeKey] = len(out)
		out = append(out, ev)
	}
	return out
}

// tokenTotal is the keep-max yardstick: the same sum ccusage uses to pick the
// winning entry (fresh input + output + cache read + cache write).
func tokenTotal(t event.Tokens) int64 {
	return t.Input + t.Output + t.CacheRead + t.CacheWrite
}

// Attributor (optional) refines project/repo using signal that isn't on a single
// line. ClaudeCode implements it for Cowork desktop sessions, whose cwd is the
// app's outputs dir (no project) — the real project is inferred from the files the
// session edited. The scan pipeline applies it after dedup; providers without it
// pass through unchanged.
type Attributor interface {
	AttributeProjects(events []event.AgentEvent, recs []provider.RawRecord) []event.AgentEvent
}

// AttributeProjects fills in project/repo that normalize could not resolve. Cowork
// desktop turns arrive as "outputs" (cwd is the app's outputs dir) and many turns
// carry neither cwd nor sessionId, so attribution is keyed on the transcript FILE
// (one .jsonl = one session's log = one project), NOT sessionId — keying on a blank
// sessionId would collapse every session-less turn into one bucket (the real-data
// bug: 34,737 MISSING-session turns dumped into a single repo). Per file: tally tool
// file paths by repo root (RepoRoot), pick the dominant root, stamp its base name on
// that file's placeholder events, and let .aispend.toml there override. A real repo
// from a genuine cwd is never overwritten; a file with no inferable edits is left as-is.
func (n ClaudeCode) AttributeProjects(events []event.AgentEvent, recs []provider.RawRecord) []event.AgentEvent {
	// A defensive, attribution-only shape: only this pass needs tool file paths, so
	// parsing them here (not in the shared ccContent) keeps the main Normalize
	// unmarshal strict and unaffected. A record that doesn't fit is simply skipped.
	type attrLine struct {
		Message struct {
			Content []struct {
				Type  string `json:"type"`
				Input struct {
					FilePath     string `json:"file_path"`
					Path         string `json:"path"`
					NotebookPath string `json:"notebook_path"`
				} `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	roots := map[string]map[string]int{} // transcript file (pathHash) -> repoRoot -> hits
	for _, r := range recs {
		key := r.Source.PathHash
		if key == "" {
			continue
		}
		var line attrLine
		if json.Unmarshal(r.Raw, &line) != nil {
			continue
		}
		for _, c := range line.Message.Content {
			if c.Type != "tool_use" {
				continue
			}
			p := c.Input.FilePath
			if p == "" {
				p = c.Input.Path
			}
			if p == "" {
				p = c.Input.NotebookPath
			}
			if p == "" {
				continue
			}
			root := n.repoRootOf(p)
			if root == "" {
				continue
			}
			if roots[key] == nil {
				roots[key] = map[string]int{}
			}
			roots[key][root]++
		}
	}
	if len(roots) == 0 {
		return events
	}

	// Dominant root per transcript file (ties broken by path for determinism).
	dominant := map[string]string{}
	for file, tally := range roots {
		best, bestN := "", 0
		for root, hits := range tally {
			if hits > bestN || (hits == bestN && root < best) {
				best, bestN = root, hits
			}
		}
		if best != "" {
			dominant[file] = best
		}
	}

	for i := range events {
		// Only fill placeholders normalize left behind — never overwrite a real repo
		// resolved from a genuine cwd. Cowork turns arrive as "outputs"; cwd/session-
		// less turns arrive empty.
		if r := events[i].Repo; r != "" && r != "outputs" {
			continue
		}
		root, ok := dominant[events[i].Evidence.SourcePathHash]
		if !ok {
			continue
		}
		name := filepath.Base(root)
		events[i].Project, events[i].Repo = name, name
		if n.Attribute != nil {
			if p, c := n.Attribute(root); p != "" {
				events[i].Project, events[i].CostTag = p, c
			} else if c != "" {
				events[i].CostTag = c
			}
		}
	}
	return events
}

// repoRootOf resolves a file to its repo root via the injected RepoRoot hook. With
// no hook (or no repo found) it returns "", leaving the session's attribution as-is
// rather than guessing from a bare directory.
func (n ClaudeCode) repoRootOf(filePath string) string {
	if n.RepoRoot == nil {
		return ""
	}
	return n.RepoRoot(filePath)
}

// gitProbe is a sentinel filename joined onto a cwd so the RepoRoot hook (which
// walks up from a path's parent dir) starts its search at the cwd itself. The file
// need not exist — RepoRoot only stats .git/.aispend.toml up the tree.
const gitProbe = ".aispend-git-probe"

// VCSEnricher (optional) stamps git provenance that isn't on a single line. ClaudeCode
// implements it to recover GitSHA: the session log carries no commit, so the SHA is
// reconstructed from the repo's reflog (the injected HeadAt) at each turn's timestamp.
// The scan pipeline applies it after dedup/attribution, before pricing; providers
// without it pass through unchanged.
type VCSEnricher interface {
	EnrichVCS(events []event.AgentEvent, recs []provider.RawRecord) []event.AgentEvent
}

// EnrichVCS resolves each event's repo root from the raw records (the real paths,
// which the hashed ledger no longer holds) and stamps GitSHA = HeadAt(root, turn
// time), best-effort and per turn — two turns in one session can land on different
// commits. It is a no-op without a HeadAt hook; a turn whose repo can't be resolved,
// or whose commit predates the reflog, keeps GitSHA empty (never a guessed SHA).
// GitBranch is set in Normalize; here a literal "HEAD" (logged by detached or Cowork
// sessions) is resolved to the repo's real current branch via CurrentBranch, so
// per-branch facets and the trailer never see an unresolved ref. A real branch name
// is left untouched.
func (n ClaudeCode) EnrichVCS(events []event.AgentEvent, recs []provider.RawRecord) []event.AgentEvent {
	if n.HeadAt == nil {
		return events
	}
	roots := n.repoRootsByFile(recs)
	if len(roots) == 0 {
		return events
	}
	branchOf := map[string]string{} // resolved current branch per root; "" = unresolved
	for i := range events {
		root := roots[events[i].Evidence.SourcePathHash]
		if root == "" {
			continue
		}
		if sha, ok := n.HeadAt(root, events[i].TSStart); ok {
			events[i].GitSHA = sha
		}
		if n.CurrentBranch != nil && events[i].GitBranch == "HEAD" {
			br, cached := branchOf[root]
			if !cached {
				if b, ok := n.CurrentBranch(root); ok {
					br = b
				}
				branchOf[root] = br
			}
			if br != "" {
				events[i].GitBranch = br
			}
		}
	}
	if n.Churn != nil {
		n.stampSessionChurn(events, roots)
	}
	return events
}

// stampSessionChurn records per-file line churn once per session, on the earliest
// turn (the representative event), over the commit range the session spanned (first
// turn's SHA → last turn's SHA). It runs only when both endpoints resolved to
// different commits — i.e. a commit landed during the session — so churn is shown
// only where git can prove it; otherwise SessionChurn is left empty (cost-only
// heatmap). Sessionless turns are skipped, and stamping once avoids any per-turn
// double count in a per-file rollup.
func (n ClaudeCode) stampSessionChurn(events []event.AgentEvent, roots map[string]string) {
	type span struct {
		firstIdx, lastIdx int
		first, last       time.Time
	}
	spans := map[string]*span{}
	var order []string
	for i := range events {
		sid := events[i].SessionID
		if sid == "" {
			continue
		}
		sp := spans[sid]
		if sp == nil {
			spans[sid] = &span{firstIdx: i, lastIdx: i, first: events[i].TSStart, last: events[i].TSStart}
			order = append(order, sid)
			continue
		}
		if events[i].TSStart.Before(sp.first) {
			sp.first, sp.firstIdx = events[i].TSStart, i
		}
		if !events[i].TSStart.Before(sp.last) {
			sp.last, sp.lastIdx = events[i].TSStart, i
		}
	}
	for _, sid := range order {
		sp := spans[sid]
		from, to := events[sp.firstIdx].GitSHA, events[sp.lastIdx].GitSHA
		if from == "" || to == "" || from == to {
			continue
		}
		root := roots[events[sp.firstIdx].Evidence.SourcePathHash]
		if root == "" {
			continue
		}
		// Scope the diff to the files the session actually touched; with no files an
		// unfiltered numstat would return the whole range's churn — unrelated work
		// wrongly attributed to this sitting.
		files := sessionFilesUnion(events, sid)
		if len(files) == 0 {
			continue
		}
		if churn := n.Churn(root, from, to, files); len(churn) > 0 {
			events[sp.firstIdx].SessionChurn = churn
		}
	}
}

// sessionFilesUnion returns the sorted, de-duplicated repo-relative files a session
// touched across its turns — the scope for its churn diff.
func sessionFilesUnion(events []event.AgentEvent, sid string) []string {
	seen := map[string]bool{}
	var files []string
	for i := range events {
		if events[i].SessionID != sid {
			continue
		}
		for _, f := range events[i].Files {
			if !seen[f] {
				seen[f] = true
				files = append(files, f)
			}
		}
	}
	sort.Strings(files)
	return files
}

// repoRootsByFile resolves the real repo root for each transcript file (keyed by
// source path hash): the cwd's repo when the cwd is itself inside one (terminal
// sessions), else the dominant root among the files the session edited (Cowork's
// placeholder cwd, or cwd-less subagent turns). Mirrors AttributeProjects' signal so
// SHA and project attribution agree. Returns nil without a RepoRoot hook.
func (n ClaudeCode) repoRootsByFile(recs []provider.RawRecord) map[string]string {
	if n.RepoRoot == nil {
		return nil
	}
	type attrLine struct {
		CWD     string `json:"cwd"`
		Message struct {
			Content []struct {
				Type  string `json:"type"`
				Input struct {
					FilePath     string `json:"file_path"`
					Path         string `json:"path"`
					NotebookPath string `json:"notebook_path"`
				} `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	cwdRoot := map[string]string{}
	fileTally := map[string]map[string]int{}
	for _, r := range recs {
		key := r.Source.PathHash
		if key == "" {
			continue
		}
		var line attrLine
		if json.Unmarshal(r.Raw, &line) != nil {
			continue
		}
		if _, seen := cwdRoot[key]; !seen && line.CWD != "" {
			if root := n.RepoRoot(filepath.Join(line.CWD, gitProbe)); root != "" {
				cwdRoot[key] = root
			}
		}
		for _, c := range line.Message.Content {
			if c.Type != "tool_use" {
				continue
			}
			p := c.Input.FilePath
			if p == "" {
				p = c.Input.Path
			}
			if p == "" {
				p = c.Input.NotebookPath
			}
			if p == "" {
				continue
			}
			if root := n.repoRootOf(p); root != "" {
				if fileTally[key] == nil {
					fileTally[key] = map[string]int{}
				}
				fileTally[key][root]++
			}
		}
	}
	out := map[string]string{}
	for k, r := range cwdRoot {
		out[k] = r
	}
	for k, tally := range fileTally {
		if out[k] == "" {
			out[k] = dominantRoot(tally)
		}
	}
	return out
}

// dominantRoot returns the most-hit repo root, ties broken by path for determinism.
func dominantRoot(tally map[string]int) string {
	best, bestN := "", 0
	for root, hits := range tally {
		if hits > bestN || (hits == bestN && root < best) {
			best, bestN = root, hits
		}
	}
	return best
}
