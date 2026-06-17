// Package scan orchestrates the 0A pipeline: a Provider's raw records are
// normalized, priced, and written to the sink, with a summary of what happened.
// It is the seam the `aispend scan` command drives. Re-scanning is safe because
// EventIDs are stable, so the idempotent Upsert collapses any re-read.
//
// See design-documents/phase-0A-trusted-explainable-ledger.md §Commands.
package scan

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentspend/ai-agent-spend/internal/event"
	"github.com/agentspend/ai-agent-spend/internal/normalize"
	"github.com/agentspend/ai-agent-spend/internal/pricing"
	"github.com/agentspend/ai-agent-spend/internal/provider"
	"github.com/agentspend/ai-agent-spend/internal/store"
)

// maxSkipSamples bounds how many skipped-record details we retain for `--verbose`.
const maxSkipSamples = 50

// Skip is a record that could not be parsed — surfaced, never silently dropped.
type Skip struct {
	PathHash string // hashed source path (never the raw path)
	Line     int
	Reason   string
	Sample   string // first ~80 printable chars of the raw line, for diagnosis
}

// Summary is what `scan` reports to the user.
type Summary struct {
	Provider     string
	Imported     int
	Skipped      int // records that could not be parsed (reported, never dropped silently)
	NotBillable  int // valid records that are not billable turns (user msgs, summaries)
	Deduped      int // duplicate records collapsed by the per-adapter keep-max dedup
	Since, Until time.Time
	Skips        []Skip // capped sample of skipped records, for `scan --verbose`
}

// Scanner wires the pipeline stages. The Store is used for scan-state; the Sink
// receives events (a FileStore satisfies both).
type Scanner struct {
	Provider   provider.Provider
	Normalizer normalize.Normalizer
	Pricing    *pricing.Engine
	Plan       pricing.Plan
	Store      store.Store
	Sink       store.Sink
	Now        func() time.Time // injectable clock; defaults to time.Now
	Full       bool             // re-read all sessions, ignoring the last-scan watermark
}

// Run executes one scan: read new records, normalize, price, persist.
func (s *Scanner) Run() (Summary, error) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}

	var last time.Time
	if !s.Full {
		l, err := s.Store.LastScan(s.Provider.Name())
		if err != nil {
			return Summary{}, fmt.Errorf("scan: last-scan: %w", err)
		}
		last = l
	}
	recs, err := s.Provider.Read(last)
	if err != nil {
		return Summary{}, fmt.Errorf("scan: read: %w", err)
	}

	sum := Summary{Provider: s.Provider.Name()}
	var normalized []event.AgentEvent
	for _, r := range recs {
		ev, err := s.Normalizer.Normalize(r)
		if errors.Is(err, normalize.ErrNotBillable) {
			sum.NotBillable++
			continue
		}
		if err != nil {
			sum.Skipped++ // unrecognized format — counted, surfaced, never silently dropped
			if len(sum.Skips) < maxSkipSamples {
				sum.Skips = append(sum.Skips, Skip{
					PathHash: r.Source.PathHash,
					Line:     r.Line,
					Reason:   err.Error(),
					Sample:   sampleOf(r.Raw),
				})
			}
			continue
		}
		normalized = append(normalized, ev)
	}

	// Per-adapter dedup runs before pricing so we never price (or store) the same
	// turn twice. Claude Code collapses streaming placeholders here (keep-max);
	// providers without a Deduper pass through unchanged. Pricing is a pure
	// function of the event, so deferring it past dedup changes no number.
	if d, ok := s.Normalizer.(normalize.Deduper); ok {
		before := len(normalized)
		normalized = d.Dedupe(normalized)
		sum.Deduped = before - len(normalized)
	}

	// Project attribution that needs cross-line signal runs after dedup: Cowork
	// desktop sessions have a placeholder cwd, so their project is inferred from the
	// files the session edited (raw records carry the tool paths). Providers without
	// an Attributor pass through unchanged.
	if a, ok := s.Normalizer.(normalize.Attributor); ok {
		normalized = a.AttributeProjects(normalized, recs)
	}

	var events []event.AgentEvent
	for i := range normalized {
		ev := normalized[i]
		if err := s.Pricing.Price(&ev, s.Plan); err != nil {
			return Summary{}, fmt.Errorf("scan: price %s: %w", ev.EventID, err)
		}
		if sum.Since.IsZero() || ev.TSStart.Before(sum.Since) {
			sum.Since = ev.TSStart
		}
		if ev.TSEnd.After(sum.Until) {
			sum.Until = ev.TSEnd
		}
		events = append(events, ev)
	}

	if len(events) > 0 {
		if err := s.Sink.Write(events); err != nil {
			return Summary{}, fmt.Errorf("scan: write: %w", err)
		}
	}
	sum.Imported = len(events)

	if err := s.Store.SetLastScan(s.Provider.Name(), now()); err != nil {
		return sum, fmt.Errorf("scan: set last-scan: %w", err)
	}
	return sum, nil
}

// sampleOf returns the first ~80 printable chars of a raw line, for diagnosing a
// skip. Control characters become spaces. Content stays local — shown only to the
// user via `scan --verbose`, never stored or exported.
func sampleOf(b []byte) string {
	const n = 80
	s := string(b)
	if len(s) > n {
		s = s[:n] + "…"
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 {
			return ' '
		}
		return r
	}, s)
}
