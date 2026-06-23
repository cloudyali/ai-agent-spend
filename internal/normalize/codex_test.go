package normalize

import (
	"errors"
	"testing"

	"github.com/cloudyali/ai-agent-spend/internal/provider"
)

func cxRec(raw string, line int, pathHash string) provider.RawRecord {
	return provider.RawRecord{
		Provider: "codex",
		Source:   provider.Source{PathHash: pathHash, Kind: "rollout_jsonl"},
		Line:     line,
		Raw:      []byte(raw),
	}
}

// Real-shape fixtures (verified against ~/.codex rollout data, 2026-06).
const (
	cxTurnCtx = `{"timestamp":"2026-06-15T10:00:00Z","type":"turn_context","cwd":"/x/payments","model":"gpt-5.3-codex"}`

	// API-key mode: the input/output breakdown is present.
	cxTokBreakdown = `{"timestamp":"2026-06-15T10:00:05Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":12000,"cached_input_tokens":8000,"output_tokens":3000,"reasoning_output_tokens":500,"total_tokens":15500}}}}`

	// Subscription mode: only total_tokens recorded (the real-world case).
	cxTokTotalOnly = `{"timestamp":"2026-06-15T10:00:06Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":0,"cached_input_tokens":0,"output_tokens":0,"reasoning_output_tokens":0,"total_tokens":1771},"last_token_usage":{"input_tokens":0,"cached_input_tokens":0,"output_tokens":0,"reasoning_output_tokens":0,"total_tokens":1771},"model_context_window":null},"rate_limits":null}}`

	cxTokInfoNull = `{"timestamp":"2026-06-15T10:00:07Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"limit_id":"codex"}}}`
)

func TestCodex_BreakdownMode(t *testing.T) {
	n := &Codex{GOOS: "linux", IdentityHash: "id"}
	if _, err := n.Normalize(cxRec(cxTurnCtx, 1, "ph1")); !errors.Is(err, ErrNotBillable) {
		t.Fatalf("turn_context should be ErrNotBillable, got %v", err)
	}
	ev, err := n.Normalize(cxRec(cxTokBreakdown, 2, "ph1"))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Provider != "codex" || ev.Model != "gpt-5.3-codex" || ev.Repo != "payments" {
		t.Errorf("identity/model/repo wrong: %+v", ev)
	}
	if ev.Tokens.Input != 4000 || ev.Tokens.CacheRead != 8000 || ev.Tokens.Output != 3500 {
		t.Errorf("tokens = %+v, want input 4000 / cacheRead 8000 / output 3500", ev.Tokens)
	}
	if len(ev.Evidence.KnownMissingFields) != 0 {
		t.Errorf("breakdown mode should have no missing fields, got %v", ev.Evidence.KnownMissingFields)
	}
}

func TestCodex_TotalOnlyMode(t *testing.T) {
	n := &Codex{GOOS: "linux", IdentityHash: "id"}
	_, _ = n.Normalize(cxRec(cxTurnCtx, 1, "ph1"))
	ev, err := n.Normalize(cxRec(cxTokTotalOnly, 2, "ph1"))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Tokens.Input != 1771 || ev.Tokens.Output != 0 {
		t.Errorf("total-only tokens = %+v, want input 1771", ev.Tokens)
	}
	if !contains(ev.Evidence.KnownMissingFields, "token_breakdown") {
		t.Errorf("total-only should flag token_breakdown, got %v", ev.Evidence.KnownMissingFields)
	}
}

func TestCodex_NonBillable(t *testing.T) {
	n := &Codex{GOOS: "linux"}
	for name, raw := range map[string]string{
		"info null":          cxTokInfoNull,
		"agent_message":      `{"timestamp":"2026-06-15T10:00:00Z","type":"event_msg","payload":{"type":"agent_message"}}`,
		"response_item":      `{"timestamp":"2026-06-15T10:00:00Z","type":"response_item","payload":{"type":"message","role":"user"}}`,
		"zero-total session": `{"timestamp":"2026-06-15T10:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":0}}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := n.Normalize(cxRec(raw, 1, "ph")); !errors.Is(err, ErrNotBillable) {
				t.Errorf("want ErrNotBillable, got %v", err)
			}
		})
	}
	if _, err := n.Normalize(cxRec("{bad json", 1, "ph")); err == nil || errors.Is(err, ErrNotBillable) {
		t.Error("malformed line should be a parse error, not ErrNotBillable")
	}
}

func TestCodex_StateResetsPerFile(t *testing.T) {
	n := &Codex{GOOS: "linux", IdentityHash: "id"}
	_, _ = n.Normalize(cxRec(cxTurnCtx, 1, "phA"))
	ev, err := n.Normalize(cxRec(cxTokBreakdown, 1, "phB")) // different file, no turn_context
	if err != nil {
		t.Fatal(err)
	}
	// model must NOT leak from phA; with none seen it falls back to the flagged default
	if ev.Model != "gpt-5.3-codex" || !contains(ev.Evidence.KnownMissingFields, "model_assumed") {
		t.Errorf("after reset want flagged default model, got %q missing=%v", ev.Model, ev.Evidence.KnownMissingFields)
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
