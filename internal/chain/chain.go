// Package chain builds the chronological "prompt chain" of a work session: the
// conversation replayed turn-by-turn with a cumulative-cost gutter, grouped by the
// user prompt (PromptID) that triggered each run of turns.
//
// It is the data layer for the receipt's chain view (design-documents/DESIGN.md). Rendering
// (cursor, tab, color) lives in the surface layers; this package is pure and depends
// only on package event, so it is cheap to test and reuse from both the static ANSI
// surface and the deferred interactive TUI.
//
// Honesty: cost is the api-equivalent view; a turn whose api-equivalent is not
// computable (nil) contributes 0 to the running total and flips the chain to
// not-Confident, so the gutter is never silently overstated.
package chain

import (
	"sort"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/event"
)

// Turn is one billable turn in the chain, carrying its own cost and the running
// cumulative total through it (the gutter).
type Turn struct {
	EventID    string
	PromptID   string
	TS         time.Time
	Model      string
	CostMicros int64 // api-equivalent micros; 0 when not computable
	HasCost    bool  // false when api-equivalent was nil (render faint)
	CumMicros  int64 // running cumulative micros through this turn
	Files      []string
}

// Group is the run of turns triggered by one user prompt. PromptID == "" is the
// single bucket for turns whose prompt boundary the logs didn't record.
type Group struct {
	PromptID    string
	Turns       []Turn
	TotalMicros int64
	Start       time.Time
}

// Chain is a session's turns in time order, plus their grouping by prompt.
type Chain struct {
	Turns       []Turn
	Groups      []Group
	TotalMicros int64
	Confident   bool // false if any turn's cost was not computable
}

// Build orders evs chronologically (stable, tie-broken by EventID), threads the
// cumulative-cost gutter, and groups turns by PromptID in first-seen order. Input
// need not be pre-sorted and is not mutated.
func Build(evs []event.AgentEvent) Chain {
	sorted := make([]event.AgentEvent, len(evs))
	copy(sorted, evs)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].TSStart.Equal(sorted[j].TSStart) {
			return sorted[i].TSStart.Before(sorted[j].TSStart)
		}
		return sorted[i].EventID < sorted[j].EventID
	})

	ch := Chain{Confident: true}
	groupIdx := map[string]int{}
	var cum int64
	for _, e := range sorted {
		cost, has := apiMicros(e)
		cum += cost
		if !has {
			ch.Confident = false
		}
		tn := Turn{
			EventID:    e.EventID,
			PromptID:   e.PromptID,
			TS:         e.TSStart,
			Model:      e.Model,
			CostMicros: cost,
			HasCost:    has,
			CumMicros:  cum,
			Files:      e.Files,
		}
		ch.Turns = append(ch.Turns, tn)
		ch.TotalMicros = cum

		gi, ok := groupIdx[e.PromptID]
		if !ok {
			ch.Groups = append(ch.Groups, Group{PromptID: e.PromptID, Start: e.TSStart})
			gi = len(ch.Groups) - 1
			groupIdx[e.PromptID] = gi
		}
		ch.Groups[gi].Turns = append(ch.Groups[gi].Turns, tn)
		ch.Groups[gi].TotalMicros += cost
	}
	return ch
}

// apiMicros reads the api-equivalent cost view; (0,false) means not computable.
func apiMicros(e event.AgentEvent) (int64, bool) {
	if e.CostViews.APIEquivalent != nil {
		return e.CostViews.APIEquivalent.Micros, true
	}
	return 0, false
}
