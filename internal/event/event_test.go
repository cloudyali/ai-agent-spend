package event

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMoney_String(t *testing.T) {
	cases := []struct {
		name string
		m    Money
		want string
	}{
		{"zero value", Money{}, "$0.000000"},
		{"usd sub-dollar", USD(420_000), "$0.420000"},
		{"usd whole", USD(27_080_000), "$27.080000"},
		{"usd negative", USD(-1_500_000), "-$1.500000"},
		{"non-usd suffixes code", Money{Micros: 1_500_000, Currency: "EUR"}, "1.500000 EUR"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.m.String(); got != c.want {
				t.Errorf("String() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestMoney_Add(t *testing.T) {
	t.Run("same currency sums micros", func(t *testing.T) {
		got, err := USD(420_000).Add(USD(80_000))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != USD(500_000) {
			t.Errorf("Add = %v, want $0.50", got)
		}
	})
	t.Run("currency mismatch errors", func(t *testing.T) {
		_, err := USD(1).Add(Money{Micros: 1, Currency: "EUR"})
		if err == nil {
			t.Error("expected currency-mismatch error, got nil")
		}
	})
}

func TestMoney_JSONIsIntegerAndRoundTrips(t *testing.T) {
	m := USD(420_000)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	// money must serialize as an integer count of micros — never a float.
	if !strings.Contains(string(b), `"micros":420000`) {
		t.Errorf("expected integer micros in JSON, got %s", b)
	}
	var back Money
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back != m {
		t.Errorf("round-trip = %v, want %v", back, m)
	}
}

func newSampleEvent() AgentEvent {
	ts := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	api := USD(420_000)
	return AgentEvent{
		SchemaVersion: SchemaVersion,
		EventID:       "evt_8a31",
		SessionID:     "sess_1",
		Provider:      "claude_code",
		Surface:       "coding_agent",
		IdentityHash:  "abcd",
		Repo:          "payments",
		Model:         "claude-opus-4",
		Mode:          "agent",
		Tokens:        Tokens{Input: 12400, Output: 3100, CacheRead: 8900},
		CostViews:     CostViews{APIEquivalent: &api, Estimated: &api},
		Evidence: Evidence{
			SourceType:           "local_file",
			ParserName:           "claude_code",
			ParserVersion:        "v1",
			PricingTableVersion:  "anthropic-2026-05",
			PricedAt:             ts,
			Currency:             "USD",
			CostMethod:           "token_priced",
			ConfidenceScore:      0.95,
			ReconciliationStatus: "local_only",
			DedupeKey:            "evt_8a31",
		},
		TSStart: ts,
		TSEnd:   ts.Add(2 * time.Minute),
	}
}

func TestAgentEvent_JSONRoundTrips(t *testing.T) {
	e := newSampleEvent()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var back AgentEvent
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	bb, _ := json.Marshal(back)
	if string(b) != string(bb) {
		t.Errorf("round-trip changed JSON:\n first=%s\n back =%s", b, bb)
	}
	if back.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", back.SchemaVersion)
	}
}

func TestAgentEvent_NilCostViewsAreOmitted(t *testing.T) {
	e := newSampleEvent()
	e.CostViews = CostViews{} // everything not computable
	b, _ := json.Marshal(e)
	s := string(b)
	if strings.Contains(s, "api_equivalent") || strings.Contains(s, "billed") {
		t.Errorf("nil cost views should be omitted, got %s", s)
	}
	// the cost_views object itself is always present (a contract field).
	if !strings.Contains(s, `"cost_views":{}`) {
		t.Errorf("expected empty cost_views object, got %s", s)
	}
}

func TestSchemaVersion_IsStable(t *testing.T) {
	if SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1 (bump intentionally + update docs/goldens)", SchemaVersion)
	}
}
