package cli

import (
	"testing"

	"github.com/cloudyali/ai-agent-spend/internal/lines"
)

// severityCode is the bridge from the shared policy (lines.Severity) to the
// hand-rolled ANSI palette; OK must stay neutral so healthy gauges aren't tinted.
func TestSeverityCode(t *testing.T) {
	if got := severityCode(lines.SevOK); got != "" {
		t.Errorf("OK should be neutral, got %q", got)
	}
	if got := severityCode(lines.SevWarn); got != cWarn {
		t.Errorf("warn → %q, want %q", got, cWarn)
	}
	if got := severityCode(lines.SevCrit); got != cCrit {
		t.Errorf("crit → %q, want %q", got, cCrit)
	}
}
