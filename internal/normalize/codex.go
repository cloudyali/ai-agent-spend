package normalize

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/agentspend/ai-agent-spend/internal/event"
	"github.com/agentspend/ai-agent-spend/internal/provider"
)

const (
	codexParserName    = "codex"
	codexParserVersion = "v1"
	// codexDefaultModel is used when no session_meta/turn_context named the model
	// (Codex doesn't always record it locally); flagged via "model_assumed".
	codexDefaultModel = "gpt-5.3-codex"
)

// Codex normalizes OpenAI Codex rollout JSONL. Each line is a flattened
// RolloutItem: {"timestamp", "type", "payload", ...}. A turn's token usage (an
// "event_msg" line whose payload.type == "token_count") is separate from the
// model/cwd (a "session_meta"/"turn_context" line), so this normalizer is
// STATEFUL: it remembers the latest model/cwd for the current session and applies
// them to the next token_count. State resets when the source file changes, so
// records must arrive in per-file order (the provider guarantees this).
//
// Verified against real ~/.codex rollout data (2026-06): on a ChatGPT
// subscription only `total_tokens` is recorded (no input/output split), so those
// turns are priced as a flagged estimate (see internal/pricing). When the split is
// present (API-key mode) it is used directly.
type Codex struct {
	GOOS         string
	IdentityHash string
	Attribute    func(cwd string) (project, costTag string)

	curPath  string
	curModel string
	curCWD   string
}

var _ Normalizer = (*Codex)(nil)

type cxLine struct {
	Timestamp time.Time       `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	Model     string          `json:"model"` // session_meta/turn_context may carry these top-level
	CWD       string          `json:"cwd"`
}

type cxPayload struct {
	Type  string  `json:"type"`
	Info  *cxInfo `json:"info"` // pointer: token_count.info can be null
	Model string  `json:"model"`
	CWD   string  `json:"cwd"`
}

type cxInfo struct {
	LastTokenUsage *cxUsage `json:"last_token_usage"`
}

type cxUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

// Normalize implements Normalizer for Codex rollout records.
func (n *Codex) Normalize(rec provider.RawRecord) (event.AgentEvent, error) {
	if rec.Source.PathHash != n.curPath {
		n.curPath, n.curModel, n.curCWD = rec.Source.PathHash, "", ""
	}

	var line cxLine
	if err := json.Unmarshal(rec.Raw, &line); err != nil {
		return event.AgentEvent{}, fmt.Errorf("normalize: unrecognized codex record: %w", err)
	}
	var pl cxPayload
	if len(line.Payload) > 0 {
		_ = json.Unmarshal(line.Payload, &pl)
	}

	// model/cwd may appear top-level or inside the payload (session_meta/turn_context).
	if m := firstNonEmpty(line.Model, pl.Model); m != "" {
		n.curModel = canonicalModel(m)
	}
	if c := firstNonEmpty(line.CWD, pl.CWD); c != "" {
		n.curCWD = c
	}

	if line.Type != "event_msg" || pl.Type != "token_count" || pl.Info == nil || pl.Info.LastTokenUsage == nil {
		return event.AgentEvent{}, ErrNotBillable
	}

	u := pl.Info.LastTokenUsage
	tokens, missing, ok := codexTokens(u)
	if !ok {
		return event.AgentEvent{}, ErrNotBillable // zero-usage turn
	}

	model := n.curModel
	if model == "" {
		model = codexDefaultModel
		missing = append(missing, "model_assumed")
	}

	id := eventID(rec.Source.PathHash, rec.Line, n.curPath)
	repo, project, costTag := "", "", ""
	if n.curCWD != "" {
		repo = filepath.Base(n.curCWD)
		project = repo
		if n.Attribute != nil {
			if p, c := n.Attribute(n.curCWD); p != "" {
				project, costTag = p, c
			} else {
				costTag = c
			}
		}
	}

	return event.AgentEvent{
		SchemaVersion: event.SchemaVersion,
		EventID:       id,
		Provider:      codexParserName,
		Surface:       "coding_agent",
		IdentityHash:  n.IdentityHash,
		Project:       project,
		Repo:          repo,
		CostTag:       costTag,
		CWDHash:       hashCWD(n.curCWD, n.GOOS),
		Model:         model,
		Mode:          "agent",
		Tokens:        tokens,
		TSStart:       line.Timestamp,
		TSEnd:         line.Timestamp,
		Evidence: event.Evidence{
			SourceType:           "local_file",
			SourcePathHash:       rec.Source.PathHash,
			SourceLine:           rec.Line,
			ParserName:           codexParserName,
			ParserVersion:        codexParserVersion,
			KnownMissingFields:   missing,
			DedupeKey:            id,
			ReconciliationStatus: "local_only",
		},
	}, nil
}

// codexTokens maps a Codex usage record to Tokens. If the input/output breakdown
// is present (API-key mode) it is used directly. If only total_tokens is recorded
// (subscription mode), the total goes to Input as a lower-bound basis, flagged via
// known-missing "token_breakdown" so pricing marks it a low-confidence estimate.
func codexTokens(u *cxUsage) (event.Tokens, []string, bool) {
	if u.InputTokens > 0 || u.OutputTokens > 0 || u.ReasoningOutputTokens > 0 || u.CachedInputTokens > 0 {
		nonCached := u.InputTokens - u.CachedInputTokens
		if nonCached < 0 {
			nonCached = u.InputTokens
		}
		return event.Tokens{
			Input:     nonCached,
			Output:    u.OutputTokens + u.ReasoningOutputTokens,
			CacheRead: u.CachedInputTokens,
		}, nil, true
	}
	if u.TotalTokens > 0 {
		return event.Tokens{Input: u.TotalTokens}, []string{"token_breakdown"}, true
	}
	return event.Tokens{}, nil, false
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
