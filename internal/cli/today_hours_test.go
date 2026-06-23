package cli

import (
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/event"
)

// The hourly bar buckets in the DISPLAY zone, so the same UTC ledger produces a
// different peak in IST vs UTC — which is exactly why the CLI (now local) lines up
// with the TUI (already local). The ledger instants stay UTC.
func TestHourlyBuckets_LocalTZ(t *testing.T) {
	ist := time.FixedZone("IST", 5*3600+30*60) // UTC+5:30 (half-hour offset)
	winStart := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 21, 8, 0, 0, 0, time.UTC) // 13:30 IST

	ev := func(h, mi int, micros int64) event.AgentEvent {
		m := event.USD(micros)
		return event.AgentEvent{TSStart: time.Date(2026, 6, 21, h, mi, 0, 0, time.UTC), CostViews: event.CostViews{APIEquivalent: &m}}
	}
	events := []event.AgentEvent{
		ev(2, 0, 100), // 07:30 IST → IST 07
		ev(7, 0, 500), // 12:30 IST → IST 12  (peak in IST)
		ev(7, 45, 50), // 13:15 IST → IST 13  (but 07:00 UTC bucket with the one above)
	}

	hours, peak := hourlyBuckets(events, winStart, now, ist)
	if h := truncHour(winStart, ist).Add(time.Duration(peak) * time.Hour).Hour(); h != 12 || hours[peak] != 500 {
		t.Errorf("IST peak = %02d:00/%d, want 12:00/500", h, hours[peak])
	}

	hoursU, peakU := hourlyBuckets(events, winStart, now, time.UTC)
	if h := truncHour(winStart, time.UTC).Add(time.Duration(peakU) * time.Hour).Hour(); h != 7 || hoursU[peakU] != 550 {
		t.Errorf("UTC peak = %02d:00/%d, want 07:00/550 (07:00+07:45 share the bucket)", h, hoursU[peakU])
	}
}
