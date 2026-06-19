// Package event defines the core contract every surface reads: AgentEvent, the
// versioned schema, and Money (integer micro-units, never a float). Nothing in
// AgentSpend reads a raw file directly — it reads AgentEvent.
//
// See design-documents/02-data-model.md. A change here is a versioned-contract
// change: bump SchemaVersion and update the golden fixtures in the same change-set.
package event

import (
	"fmt"
	"time"
)

// SchemaVersion is stamped on every event so a future server can ingest old
// shapes. Additive changes do not bump it; breaking changes do.
const SchemaVersion = 1

// AgentEvent is the normalized, priced, provenance-carrying unit of AI-coding spend.
type AgentEvent struct {
	SchemaVersion int    `json:"schema_version"`
	EventID       string `json:"event_id"`
	SessionID     string `json:"session_id"`
	PromptID      string `json:"prompt_id,omitempty"`
	// SubagentID is the Claude Code subagent worker id when this turn came from a
	// subagents/agent-*.jsonl transcript. The turn's SessionID is rolled up to the
	// parent session (resolved from the path at scan time, since paths are hashed in
	// the ledger), so subagent spend folds into the parent's total. Empty otherwise.
	// Additive; does not bump SchemaVersion.
	SubagentID   string `json:"subagent_id,omitempty"`
	Provider     string `json:"provider"`
	Surface      string `json:"surface"`
	IdentityHash string `json:"identity_hash"`
	Project      string `json:"project,omitempty"`
	Repo         string `json:"repo,omitempty"`
	CWDHash      string `json:"cwd_hash,omitempty"`
	// GitBranch is the branch on the session line (Claude Code logs it per turn);
	// durable, stored as-is. GitSHA is the commit that was HEAD at the turn's
	// timestamp, reconstructed best-effort at scan time from the repo's reflog (the
	// log itself carries no SHA) — empty when unresolvable. Both are additive and do
	// not bump SchemaVersion. See design-documents/02-data-model.md §1.
	GitBranch  string    `json:"git_branch,omitempty"`
	GitSHA     string    `json:"git_sha,omitempty"`
	CostTag    string    `json:"cost_tag,omitempty"`
	Model      string    `json:"model"`
	Mode       string    `json:"mode,omitempty"`
	Tokens     Tokens    `json:"tokens"`
	CostViews  CostViews `json:"cost_views"`
	Evidence   Evidence  `json:"evidence"`
	Tools      []string  `json:"tools,omitempty"`
	MCPServers []string  `json:"mcp_servers,omitempty"`
	Files      []string  `json:"files,omitempty"` // repo-relative paths the turn operated on (Edit/Write/Read/…)
	// SessionChurn is per-file line churn (added/removed) for the whole session,
	// recovered best-effort at scan from `git diff` between the session's first and
	// last commit. It is stamped once per session (on the representative event), nil
	// elsewhere and whenever git/commits are unavailable — never a fabricated count.
	SessionChurn []FileChurn `json:"session_churn,omitempty"`
	Activity     string      `json:"activity,omitempty"` // classifier deferred to 0B
	TSStart      time.Time   `json:"ts_start"`
	TSEnd        time.Time   `json:"ts_end"`
	ActiveMS     int64       `json:"active_ms,omitempty"`
}

// Tokens is the usage breakdown; cache reads/writes are priced differently.
// CacheWrite is the total cache-creation; CacheWrite1h is the 1-hour-TTL subset
// of it (Anthropic prices the 1-hour tier at 2× input vs 1.25× for the 5-minute
// default), so the 5-minute portion is CacheWrite − CacheWrite1h.
type Tokens struct {
	Input        int64 `json:"input"`
	Output       int64 `json:"output"`
	CacheRead    int64 `json:"cache_read"`
	CacheWrite   int64 `json:"cache_write"`
	CacheWrite1h int64 `json:"cache_write_1h,omitempty"`
}

// FileChurn is the net line delta a session made to one repo-relative file
// (added/removed), from git. It pairs with the per-file cost in the session
// receipt's heatmap; the cost says how much was spent, the churn how much changed.
type FileChurn struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// Money is an integer count of millionths of a currency unit. 1 USD = 1e6 micros.
// Money is never represented as a float — token pricing is sub-cent.
type Money struct {
	Micros   int64  `json:"micros"`
	Currency string `json:"currency"`
}

// USD is a convenience constructor for US-dollar amounts in micros.
func USD(micros int64) Money { return Money{Micros: micros, Currency: "USD"} }

// Add sums two amounts of the same currency; mixing currencies is an error.
func (m Money) Add(o Money) (Money, error) {
	if m.Currency != o.Currency {
		return Money{}, fmt.Errorf("money: currency mismatch %q vs %q", m.Currency, o.Currency)
	}
	return Money{Micros: m.Micros + o.Micros, Currency: m.Currency}, nil
}

// String renders the exact micro value with 6 decimal places. USD (and the empty
// zero value) render with a leading "$"; other currencies suffix their code.
func (m Money) String() string {
	v := m.Micros
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	num := fmt.Sprintf("%d.%06d", v/1_000_000, v%1_000_000)
	if m.Currency == "USD" || m.Currency == "" {
		return sign + "$" + num
	}
	return sign + num + " " + m.Currency
}

// CostViews models spend through several lenses. A nil pointer means "not
// computable from available evidence" — never zero. See design-documents/02-data-model.md.
type CostViews struct {
	Billed             *Money `json:"billed,omitempty"`
	Reported           *Money `json:"reported,omitempty"` // a cost the tool itself wrote to disk (e.g. Claude costUSD, OpenCode/Pi cost) — present & >0 only
	EffectiveAllocated *Money `json:"effective_allocated,omitempty"`
	Marginal           *Money `json:"marginal,omitempty"`
	APIEquivalent      *Money `json:"api_equivalent,omitempty"`
	CreditConsumption  *int64 `json:"credit_consumption,omitempty"`
	Estimated          *Money `json:"estimated,omitempty"`
}

// Evidence is the provenance ledger rendered by `aispend explain`. It is a
// product feature, not an internal field.
type Evidence struct {
	SourceType           string    `json:"source_type"`
	SourceRecordID       string    `json:"source_record_id"`
	SourcePathHash       string    `json:"source_path_hash"`
	SourceLine           int       `json:"source_line,omitempty"`
	ParserName           string    `json:"parser_name"`
	ParserVersion        string    `json:"parser_version"`
	PricingTableVersion  string    `json:"pricing_table_version"`
	PricedAt             time.Time `json:"priced_at"`
	Currency             string    `json:"currency"`
	DiscountBasis        string    `json:"discount_basis,omitempty"`
	CostMethod           string    `json:"cost_method"`
	ConfidenceScore      float64   `json:"confidence_score"`
	ConfidenceReason     string    `json:"confidence_reason"`
	KnownMissingFields   []string  `json:"known_missing_fields,omitempty"`
	DedupeKey            string    `json:"dedupe_key"`
	ReconciliationStatus string    `json:"reconciliation_status"`
	InvoiceReference     string    `json:"invoice_reference,omitempty"`
}
