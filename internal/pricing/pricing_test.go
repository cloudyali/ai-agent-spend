package pricing

import (
	"math"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/event"
)

// A crafted session log (or a poisoned pricing rate) can drive the cost multiply
// past int64. Go integer overflow is silent wraparound (CWE-190), so a naive
// tokens*perMTok would fabricate a small or negative "trusted" cost. micros must
// preserve every result that fits in int64 and saturate (never wrap) the rest.
func TestMicros_PreservesLargeResultWithoutWrapping(t *testing.T) {
	// tokens*perMTok overflows int64, but the /1e6 result fits exactly.
	got := micros(4_600_000_000_000_000_000, 1_000_000)
	if got != 4_600_000_000_000_000_000 {
		t.Errorf("micros wrapped: got %d, want 4600000000000000000", got)
	}
}

func TestMicros_SaturatesWhenResultExceedsInt64(t *testing.T) {
	if got := micros(10_000_000_000_000, 1_000_000_000_000); got != math.MaxInt64 {
		t.Errorf("micros = %d, want MaxInt64 (saturated, not wrapped)", got)
	}
}

func TestMicros_ClampsNegativeTokens(t *testing.T) {
	if got := micros(-1, 5_000_000); got != 0 {
		t.Errorf("micros(negative tokens) = %d, want 0", got)
	}
}

// WithoutCache sums three token classes before the multiply; the sum must not wrap
// to a negative input count and produce a negative "no-cache" bill.
func TestWithoutCache_NoNegativeOnHugeTokens(t *testing.T) {
	e := NewEngine()
	huge := int64(math.MaxInt64 / 2)
	tk := event.Tokens{Input: huge, CacheRead: huge, CacheWrite: huge}
	got, ok := e.WithoutCache("claude-opus-4-8", tk)
	if !ok {
		t.Fatal("known model should compute")
	}
	if got.Micros < 0 {
		t.Errorf("WithoutCache wrapped negative: %d", got.Micros)
	}
}

func fixedClock() *Engine {
	e := NewEngine()
	e.now = func() time.Time { return time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC) }
	return e
}

func TestEngine_TableVersion(t *testing.T) {
	if v := NewEngine().TableVersion(); v != "pricing-2026-06" {
		t.Errorf("TableVersion() = %q, want pricing-2026-06", v)
	}
}

func TestPrice_APIEquivalent_Opus(t *testing.T) {
	e := fixedClock()
	ev := &event.AgentEvent{
		Model:  "claude-opus-4",
		Tokens: event.Tokens{Input: 12_400, Output: 3_100, CacheRead: 8_900},
	}
	if err := e.Price(ev, Plan{Kind: "api"}); err != nil {
		t.Fatal(err)
	}
	// 12400*15 + 3100*75 + 8900*1.5  (micros, per-MTok rates) = 431_850 micros
	want := event.USD(431_850)
	if ev.CostViews.APIEquivalent == nil || *ev.CostViews.APIEquivalent != want {
		t.Errorf("APIEquivalent = %v, want %v", ev.CostViews.APIEquivalent, want)
	}
	if ev.CostViews.Estimated == nil || *ev.CostViews.Estimated != want {
		t.Errorf("Estimated = %v, want %v (mirrors api-equivalent when no plan)", ev.CostViews.Estimated, want)
	}
	if ev.Evidence.CostMethod != "token_priced" {
		t.Errorf("CostMethod = %q, want token_priced", ev.Evidence.CostMethod)
	}
	if ev.Evidence.ConfidenceScore != 0.95 {
		t.Errorf("ConfidenceScore = %v, want 0.95", ev.Evidence.ConfidenceScore)
	}
	if ev.Evidence.PricingTableVersion != "pricing-2026-06" || ev.Evidence.Currency != "USD" {
		t.Errorf("pricing provenance missing: %+v", ev.Evidence)
	}
	if ev.Evidence.PricedAt.IsZero() {
		t.Error("PricedAt not stamped")
	}
}

func TestPrice_CurrentModel_Opus48(t *testing.T) {
	e := fixedClock()
	ev := &event.AgentEvent{Model: "claude-opus-4-8", Tokens: event.Tokens{Input: 1_000_000}}
	if err := e.Price(ev, Plan{Kind: "api"}); err != nil {
		t.Fatal(err)
	}
	if ev.CostViews.APIEquivalent == nil || *ev.CostViews.APIEquivalent != event.USD(5_000_000) {
		t.Errorf("opus-4-8 @ 1M input = %v, want $5.00 (current rate)", ev.CostViews.APIEquivalent)
	}
}

// Opus 4.6 / 4.7 / 4.8 share the $5/M input list price (verified June 2026).
// Every contemporaneous snapshot must price explicitly: a missing table entry
// leaves api_equivalent nil, and a nil view is silently dropped from reports
// (the real-world 4-7 gap that hid ~7.5k turns). It must also NOT fall back to
// the legacy claude-opus-4 $15/M rate, which would overcharge 3×.
func TestPrice_Opus4xSnapshotsPriceAtCurrentRate(t *testing.T) {
	for _, m := range []string{"claude-opus-4-6", "claude-opus-4-7", "claude-opus-4-8"} {
		e := fixedClock()
		ev := &event.AgentEvent{Model: m, Tokens: event.Tokens{Input: 1_000_000}}
		if err := e.Price(ev, Plan{Kind: "api"}); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		if ev.CostViews.APIEquivalent == nil || *ev.CostViews.APIEquivalent != event.USD(5_000_000) {
			t.Errorf("%s @ 1M input = %v, want $5.00 (must price, not drop or use legacy rate)", m, ev.CostViews.APIEquivalent)
		}
	}
}

// claude-fable-5 — a frontier model Cowork uses under the hood — surfaced via the
// unpriced footnote on real data (2,214 events). List rate $10/M in, $50/M out
// (verified June 2026). Must price, not silently drop.
func TestPrice_Fable5(t *testing.T) {
	e := fixedClock()
	in := &event.AgentEvent{Model: "claude-fable-5", Tokens: event.Tokens{Input: 1_000_000}}
	out := &event.AgentEvent{Model: "claude-fable-5", Tokens: event.Tokens{Output: 1_000_000}}
	if err := e.Price(in, Plan{Kind: "api"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Price(out, Plan{Kind: "api"}); err != nil {
		t.Fatal(err)
	}
	if in.CostViews.APIEquivalent == nil || *in.CostViews.APIEquivalent != event.USD(10_000_000) {
		t.Errorf("fable-5 @ 1M input = %v, want $10.00", in.CostViews.APIEquivalent)
	}
	if out.CostViews.APIEquivalent == nil || *out.CostViews.APIEquivalent != event.USD(50_000_000) {
		t.Errorf("fable-5 @ 1M output = %v, want $50.00", out.CostViews.APIEquivalent)
	}
}

func TestPrice_TotalOnlyIsLowConfidenceEstimate(t *testing.T) {
	e := fixedClock()
	ev := &event.AgentEvent{
		Model:    "gpt-5.3-codex",
		Tokens:   event.Tokens{Input: 1771}, // Codex subscription: only total, mapped to Input
		Evidence: event.Evidence{KnownMissingFields: []string{"token_breakdown"}},
	}
	if err := e.Price(ev, Plan{Kind: "api"}); err != nil {
		t.Fatal(err)
	}
	if ev.Evidence.CostMethod != "inferred" || ev.Evidence.ConfidenceScore != 0.40 {
		t.Errorf("total-only should be inferred/0.40, got %q/%v", ev.Evidence.CostMethod, ev.Evidence.ConfidenceScore)
	}
	if ev.CostViews.APIEquivalent == nil || ev.CostViews.APIEquivalent.Micros != 3099 {
		t.Errorf("api_equivalent = %v, want 3099 micros (1771 × $1.75/Mtok)", ev.CostViews.APIEquivalent)
	}
}

func TestPrice_CacheReadIsCheaperThanInput(t *testing.T) {
	e := fixedClock()
	in := &event.AgentEvent{Model: "claude-sonnet-4", Tokens: event.Tokens{Input: 1_000_000}}
	cr := &event.AgentEvent{Model: "claude-sonnet-4", Tokens: event.Tokens{CacheRead: 1_000_000}}
	_ = e.Price(in, Plan{Kind: "api"})
	_ = e.Price(cr, Plan{Kind: "api"})
	if !(cr.CostViews.APIEquivalent.Micros < in.CostViews.APIEquivalent.Micros) {
		t.Errorf("cache-read (%v) should price below input (%v)", cr.CostViews.APIEquivalent, in.CostViews.APIEquivalent)
	}
}

// TestPrice_PrefersReportedCost: when the tool wrote its own cost to disk, that
// number is authoritative (reported-else-computed) — cost_method=reported, high
// confidence — while the computed api-equivalent is still filled for comparison.
func TestPrice_PrefersReportedCost(t *testing.T) {
	e := fixedClock()
	rep := event.Money{Micros: 432_500, Currency: "USD"}
	ev := &event.AgentEvent{
		Model:     "claude-opus-4",
		Tokens:    event.Tokens{Input: 12_400, Output: 3_100, CacheRead: 8_900},
		CostViews: event.CostViews{Reported: &rep},
	}
	if err := e.Price(ev, Plan{Kind: "api"}); err != nil {
		t.Fatal(err)
	}
	if ev.Evidence.CostMethod != "reported" {
		t.Errorf("CostMethod = %q, want reported", ev.Evidence.CostMethod)
	}
	if ev.Evidence.ConfidenceScore < 0.95 {
		t.Errorf("reported cost should be high confidence, got %v", ev.Evidence.ConfidenceScore)
	}
	if ev.CostViews.APIEquivalent == nil {
		t.Error("api-equivalent should still be computed alongside reported, for comparison")
	}
	if ev.CostViews.Reported == nil || ev.CostViews.Reported.Micros != 432_500 {
		t.Error("Reported view must be preserved")
	}
}

// Even when the model is unknown (uncomputable), a real on-disk cost still stands.
func TestPrice_ReportedCostStandsWhenModelUnknown(t *testing.T) {
	e := fixedClock()
	rep := event.Money{Micros: 100_000, Currency: "USD"}
	ev := &event.AgentEvent{Model: "mystery", Tokens: event.Tokens{Input: 10}, CostViews: event.CostViews{Reported: &rep}}
	if err := e.Price(ev, Plan{Kind: "api"}); err != nil {
		t.Fatal(err)
	}
	if ev.Evidence.CostMethod != "reported" || ev.Evidence.ConfidenceScore < 0.95 {
		t.Errorf("reported cost should stand with an unknown model, got %q/%v", ev.Evidence.CostMethod, ev.Evidence.ConfidenceScore)
	}
}

func TestPrice_UnknownModelIsFlaggedNotErrored(t *testing.T) {
	e := fixedClock()
	ev := &event.AgentEvent{Model: "mystery-model", Tokens: event.Tokens{Input: 10}}
	if err := e.Price(ev, Plan{Kind: "api"}); err != nil {
		t.Fatalf("unknown model should not error, got %v", err)
	}
	if ev.CostViews.APIEquivalent != nil {
		t.Error("unknown model must leave APIEquivalent nil (not computable, not zero)")
	}
	if ev.Evidence.CostMethod != "inferred" || ev.Evidence.ConfidenceScore != 0 {
		t.Errorf("expected inferred/0 confidence, got %q/%v", ev.Evidence.CostMethod, ev.Evidence.ConfidenceScore)
	}
	if !contains(ev.Evidence.KnownMissingFields, "model_rate") {
		t.Errorf("expected known_missing_fields to include model_rate, got %v", ev.Evidence.KnownMissingFields)
	}
	// provenance is still stamped even when unpriceable
	if ev.Evidence.PricingTableVersion == "" || ev.Evidence.PricedAt.IsZero() {
		t.Error("table version / priced_at should be stamped even when unpriceable")
	}
}

func TestPrice_SubscriptionDefersAllocation(t *testing.T) {
	e := fixedClock()
	fee := event.USD(200_000_000) // $200/mo
	ev := &event.AgentEvent{Model: "claude-opus-4", Tokens: event.Tokens{Input: 1000}}
	if err := e.Price(ev, Plan{Kind: "subscription", MonthlyFee: &fee}); err != nil {
		t.Fatal(err)
	}
	// api-equivalent is still computed (useful for comparison)...
	if ev.CostViews.APIEquivalent == nil {
		t.Error("APIEquivalent should still be computed under a subscription plan")
	}
	// ...but per-event amortization is a period-level operation, deferred here.
	if ev.CostViews.Amortized != nil {
		t.Error("Amortized must be nil at per-event time (computed during aggregation)")
	}
	if !contains(ev.Evidence.KnownMissingFields, "amortized") {
		t.Errorf("expected amortized noted as missing, got %v", ev.Evidence.KnownMissingFields)
	}
}

func TestProratedFee(t *testing.T) {
	fee := event.USD(200_000_000) // $200/mo
	sub := Plan{Kind: "subscription", MonthlyFee: &fee}

	if got, ok := ProratedFee(sub, 30); !ok || got != event.USD(200_000_000) {
		t.Errorf("30 days = %v, %v; want full $200", got, ok)
	}
	if got, _ := ProratedFee(sub, 15); got != event.USD(100_000_000) {
		t.Errorf("15 days = %v; want $100", got)
	}
	if _, ok := ProratedFee(Plan{Kind: "api"}, 7); ok {
		t.Error("api plan should not prorate")
	}
	if _, ok := ProratedFee(sub, 0); ok {
		t.Error("zero days should not prorate")
	}
}

func TestAmortizeSubscription(t *testing.T) {
	d := func(y int, m time.Month, day int) time.Time {
		return time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
	}
	fee := event.USD(200_000_000) // $200/mo
	sub := func(start time.Time) Plan {
		return Plan{Kind: "subscription", MonthlyFee: &fee, StartDate: start}
	}

	t.Run("a full billing cycle bills the whole fee", func(t *testing.T) {
		// Jun 12 → Jul 12 is one complete 30-day cycle.
		got, ok := AmortizeSubscription(sub(d(2026, 6, 12)), d(2026, 6, 12), d(2026, 7, 12))
		if !ok || got != event.USD(200_000_000) {
			t.Errorf("full cycle = %v, %v; want $200", got, ok)
		}
	})

	t.Run("only days on/after the start date are billed", func(t *testing.T) {
		// Window is all of June; the plan starts Jun 12, mid-cycle (Jun12–Jul12, 30 days).
		// Active days Jun 12..Jun 30 = 19 of 30 → 200 * 19/30.
		got, ok := AmortizeSubscription(sub(d(2026, 6, 12)), d(2026, 6, 1), d(2026, 7, 1))
		want := event.USD(200_000_000 * 19 / 30)
		if !ok || got != want {
			t.Errorf("mid-cycle start = %v, %v; want %v", got, ok, want)
		}
	})

	t.Run("an ancient window start is clamped to the plan start (robust to a corrupt timestamp)", func(t *testing.T) {
		// A stray/corrupt event timestamp (e.g. ~1997) must NOT bill ~29 years of
		// fees: billing starts at the plan's StartDate, not the window's since.
		got, ok := AmortizeSubscription(sub(d(2026, 6, 12)), d(1997, 3, 1), d(2026, 7, 12))
		if !ok || got != event.USD(200_000_000) {
			t.Errorf("ancient since should clamp to plan start (one $200 cycle), got %v, %v", got, ok)
		}
	})

	t.Run("a window entirely before the start date bills nothing", func(t *testing.T) {
		if _, ok := AmortizeSubscription(sub(d(2026, 6, 12)), d(2026, 6, 1), d(2026, 6, 10)); ok {
			t.Error("window before the plan started should not bill")
		}
	})

	t.Run("the daily rate follows the real cycle length", func(t *testing.T) {
		// Anchor on the 31st. Jan 31 → Feb 28 is a 28-day cycle; Feb 28 → Mar 31 is 31 days.
		p := sub(d(2026, 1, 31))
		feb, okF := AmortizeSubscription(p, d(2026, 2, 1), d(2026, 2, 2)) // 1 day in the 28-day cycle
		mar, okM := AmortizeSubscription(p, d(2026, 3, 1), d(2026, 3, 2)) // 1 day in the 31-day cycle
		if !okF || !okM {
			t.Fatalf("expected both single-day windows to bill: feb=%v mar=%v", okF, okM)
		}
		if feb != event.USD(200_000_000/28) || mar != event.USD(200_000_000/31) {
			t.Errorf("per-cycle daily rate wrong: feb=%v (want %d) mar=%v (want %d)",
				feb, 200_000_000/28, mar, 200_000_000/31)
		}
		if !(feb.Micros > mar.Micros) {
			t.Error("a day in a 28-day February cycle should cost more than a day in a 31-day March cycle")
		}
	})

	t.Run("a window spanning two cycles sums both cycles' shares", func(t *testing.T) {
		// Start Jun 12. Window Jun 12 → Aug 12 = cycle1 (Jun12–Jul12, 30d) + cycle2 (Jul12–Aug12, 31d),
		// each fully covered → the full fee twice.
		got, ok := AmortizeSubscription(sub(d(2026, 6, 12)), d(2026, 6, 12), d(2026, 8, 12))
		if !ok || got != event.USD(400_000_000) {
			t.Errorf("two full cycles = %v, %v; want $400", got, ok)
		}
	})

	t.Run("not amortizable without subscription, fee, or start date", func(t *testing.T) {
		if _, ok := AmortizeSubscription(Plan{Kind: "api", StartDate: d(2026, 6, 1)}, d(2026, 6, 1), d(2026, 7, 1)); ok {
			t.Error("api plan should not amortize")
		}
		if _, ok := AmortizeSubscription(Plan{Kind: "subscription", MonthlyFee: &fee}, d(2026, 6, 1), d(2026, 7, 1)); ok {
			t.Error("a subscription with no start date should not use the cycle path")
		}
	})
}

func TestAllocate(t *testing.T) {
	t.Run("splits by share and sums exactly to total", func(t *testing.T) {
		total := event.USD(46_670_000) // ~$46.67
		out := Allocate(total, map[string]int64{"opus": 431850, "sonnet": 17250})
		var sum int64
		for _, m := range out {
			sum += m.Micros
		}
		if sum != total.Micros {
			t.Errorf("parts sum %d != total %d (rounding remainder lost)", sum, total.Micros)
		}
		if out["opus"].Micros <= out["sonnet"].Micros {
			t.Error("opus (larger basis) should receive the larger share")
		}
	})
	t.Run("zero basis yields zeros", func(t *testing.T) {
		out := Allocate(event.USD(100), map[string]int64{"a": 0, "b": 0})
		for k, m := range out {
			if m.Micros != 0 {
				t.Errorf("%s = %v, want 0", k, m)
			}
		}
	})
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// TestEngine_Components decomposes a known model's tokens into the four
// api-equivalent cost lines; the four must sum to APIEquivalent, and an unknown
// model must report !ok rather than silently zero.
func TestEngine_Components(t *testing.T) {
	e := NewEngine()
	// opus-4-8 rates ($/Mtok): in 5, out 25, cache-read 0.50, cache-write 6.25.
	tk := event.Tokens{Input: 1_000_000, Output: 1_000_000, CacheRead: 10_000_000, CacheWrite: 1_000_000}
	c, ok := e.Components("claude-opus-4-8", tk)
	if !ok {
		t.Fatal("known model should decompose")
	}
	if c.Input.Micros != 5_000_000 || c.Output.Micros != 25_000_000 ||
		c.CacheRead.Micros != 5_000_000 || c.CacheWrite.Micros != 6_250_000 {
		t.Errorf("components = %+v", c)
	}
	if c.Total().Micros != 41_250_000 {
		t.Errorf("Total = %d, want 41250000", c.Total().Micros)
	}
	if c.Input.Currency != "USD" {
		t.Errorf("currency = %q", c.Input.Currency)
	}
	if _, ok := e.Components("no-such-model", tk); ok {
		t.Error("unknown model should report !ok")
	}
}

// TestEngine_OneHourCacheTier verifies the 1-hour cache-creation tier is priced
// at 2× input (vs 1.25× for the 5-minute default), split out as its own
// component, and that Price() agrees with the component sum.
func TestEngine_OneHourCacheTier(t *testing.T) {
	e := NewEngine()
	// opus-4-8 input $5/Mtok → 5-min write $6.25/Mtok (1.25×), 1-hour write $10/Mtok (2×).
	// 2M total cache-creation, 1M of it 1-hour → 1M five-minute + 1M one-hour.
	tk := event.Tokens{CacheWrite: 2_000_000, CacheWrite1h: 1_000_000}

	c, ok := e.Components("claude-opus-4-8", tk)
	if !ok {
		t.Fatal("known model should decompose")
	}
	if c.CacheWrite.Micros != 6_250_000 {
		t.Errorf("5-min cache-write = %d, want 6250000 (1M × $6.25)", c.CacheWrite.Micros)
	}
	if c.CacheWrite1h.Micros != 10_000_000 {
		t.Errorf("1-hour cache-write = %d, want 10000000 (1M × $10)", c.CacheWrite1h.Micros)
	}
	if c.Total().Micros != 16_250_000 {
		t.Errorf("Total = %d, want 16250000", c.Total().Micros)
	}

	ev := event.AgentEvent{Model: "claude-opus-4-8", Tokens: tk}
	if err := e.Price(&ev, Plan{Kind: "api"}); err != nil {
		t.Fatal(err)
	}
	if ev.CostViews.APIEquivalent == nil || ev.CostViews.APIEquivalent.Micros != 16_250_000 {
		t.Errorf("api-equivalent = %v, want 16250000", ev.CostViews.APIEquivalent)
	}

	// Defensive: a malformed 1h > total clamps the 5-minute portion to 0.
	c2, _ := e.Components("claude-opus-4-8", event.Tokens{CacheWrite: 1_000_000, CacheWrite1h: 5_000_000})
	if c2.CacheWrite.Micros != 0 || c2.CacheWrite1h.Micros != 10_000_000 {
		t.Errorf("clamp wrong: 5m=%d 1h=%d, want 0 / 10000000", c2.CacheWrite.Micros, c2.CacheWrite1h.Micros)
	}
}

// TestWithoutCache is the arbitrage primitive: the hypothetical bill if prompt
// caching were off — every cache-read and cache-write token charged as a fresh
// input token (1× input). The `without cache ≈ $X · saved Y%` line compares the
// real, cache-aware api-equivalent against this number.
func TestWithoutCache(t *testing.T) {
	e := NewEngine()
	// opus-4-8: in $5, out $25, cache-read $0.50, cache-write $6.25/Mtok.
	tk := event.Tokens{Input: 1_000_000, Output: 1_000_000, CacheRead: 10_000_000, CacheWrite: 1_000_000}
	got, ok := e.WithoutCache("claude-opus-4-8", tk)
	if !ok {
		t.Fatal("known model should compute a no-cache equivalent")
	}
	// (input + cache_read + cache_write) all at $5/Mtok input, + output at $25/Mtok:
	// (1M+10M+1M)×5 + 1M×25 = 60_000_000 + 25_000_000 = 85_000_000.
	if got.Micros != 85_000_000 {
		t.Errorf("WithoutCache = %d, want 85000000", got.Micros)
	}
	if got.Currency != "USD" {
		t.Errorf("currency = %q, want USD", got.Currency)
	}
	// For this read-heavy event the no-cache bill must exceed the cache-aware total
	// — that gap is the saving caching bought.
	c, _ := e.Components("claude-opus-4-8", tk)
	if !(got.Micros > c.Total().Micros) {
		t.Errorf("no-cache (%d) should exceed with-cache (%d) for a read-heavy event", got.Micros, c.Total().Micros)
	}
	// A pure 1-hour cache write (2× input) costs MORE than not caching (1× input):
	// the no-cache hypothetical is cheaper, so savings can be negative — the honest
	// result the receipt must tolerate without claiming a phantom "saving".
	w1h := event.Tokens{CacheWrite: 1_000_000, CacheWrite1h: 1_000_000}
	nc, _ := e.WithoutCache("claude-opus-4-8", w1h) // 1M × $5 = 5_000_000
	cc, _ := e.Components("claude-opus-4-8", w1h)   // 1M × $10 (2× input) = 10_000_000
	if !(nc.Micros < cc.Total().Micros) {
		t.Errorf("a pure 1h cache-write should be cheaper without cache: nc=%d cc=%d", nc.Micros, cc.Total().Micros)
	}
	if _, ok := e.WithoutCache("no-such-model", tk); ok {
		t.Error("unknown model should report !ok (never a false zero)")
	}
}

// OpenAI caches input at ~50% of the input rate, not the 10% Anthropic heuristic.
func TestPrice_CodexCacheReadIsHalfInput(t *testing.T) {
	e := NewEngine()
	for _, m := range []string{"gpt-5.3-codex", "gpt-5-codex"} {
		c, ok := e.Components(m, event.Tokens{Input: 1_000_000, CacheRead: 1_000_000})
		if !ok {
			t.Fatalf("%s should be in the table", m)
		}
		if c.CacheRead.Micros*2 != c.Input.Micros {
			t.Errorf("%s cache-read %d should be half of input %d", m, c.CacheRead.Micros, c.Input.Micros)
		}
	}
}
