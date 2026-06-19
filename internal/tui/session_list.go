package tui

import (
	"sort"
	"time"
)

// liveWindow is how recently a session's last turn must have landed for it to read
// as "live" in the list. Recency-only here (the scanner can later AND-in file mtime);
// it needs a reference now, so a zero now never yields a live session.
const liveWindow = 10 * time.Minute

// dayKey is the sortable yyyymmdd bucket a session falls in (by its given time, in
// loc). Lexicographic order on the key matches chronological order, so "later day
// first" is a simple string compare.
func dayKey(t time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	return t.In(loc).Format("20060102")
}

// dayLabel renders a day-group header: "Today"/"Yesterday" when now is known and the
// day matches, otherwise the absolute date ("Mon Jun 15"). A zero now always renders
// absolute, keeping list output deterministic in tests and off-clock contexts.
func dayLabel(t, now time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	abs := t.In(loc).Format("Mon Jan 2")
	if now.IsZero() {
		return abs
	}
	switch dayKey(t, loc) {
	case dayKey(now, loc):
		return "Today"
	case dayKey(now.AddDate(0, 0, -1), loc):
		return "Yesterday"
	default:
		return abs
	}
}

// isLive reports whether last activity is recent enough (within liveWindow) relative
// to now. A zero now means we can't tell — never live. A small negative slack absorbs
// clock skew when a turn's timestamp is marginally ahead of now.
func isLive(last, now time.Time, window time.Duration) bool {
	if now.IsZero() {
		return false
	}
	d := now.Sub(last)
	return d <= window && d >= -time.Minute
}

// sessionSpan is the wall-clock span of a session (first turn → last turn).
func sessionSpan(s sessionStat) time.Duration {
	if s.last.Before(s.first) {
		return 0
	}
	return s.last.Sub(s.first)
}

// sessionActiveMS sums the per-turn active milliseconds across a session — the real
// hands-on time, distinct from the wall-clock span.
func sessionActiveMS(s sessionStat) int64 {
	var total int64
	for _, e := range s.evs {
		total += e.ActiveMS
	}
	return total
}

// orderForDayList sorts sessions for the day-grouped list: most-recent day first,
// and within a day the live session leads, then priciest-first. It returns a new
// slice (input is not mutated). For a single day with no reference now this reduces
// to the legacy priciest-first ordering, so existing single-day behavior is intact.
func orderForDayList(rows []sessionStat, now time.Time, loc *time.Location, window time.Duration) []sessionStat {
	out := append([]sessionStat(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool {
		ki, kj := dayKey(out[i].last, loc), dayKey(out[j].last, loc)
		if ki != kj {
			return ki > kj // later day first
		}
		li, lj := isLive(out[i].last, now, window), isLive(out[j].last, now, window)
		if li != lj {
			return li // live session leads its day
		}
		return out[i].micros > out[j].micros // then priciest-first
	})
	return out
}

// distinctSessionDays counts the distinct calendar days (in loc) the rows' last
// activity falls on — used to budget day-group header lines against the terminal
// height. loc is the display zone (nil ⇒ local), so the count matches the headers.
func distinctSessionDays(rows []sessionStat, loc *time.Location) int {
	if loc == nil {
		loc = time.Local
	}
	seen := map[string]struct{}{}
	for _, r := range rows {
		seen[dayKey(r.last, loc)] = struct{}{}
	}
	return len(seen)
}

// clockTime renders just the time-of-day for a list row, in the display zone (nil ⇒
// local); the calendar day is carried by the group header above it. The instant
// itself stays UTC in the ledger — this only chooses how it's shown.
func clockTime(t time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	return t.In(loc).Format("3:04pm")
}

// anyLive reports whether any row is currently live — gates the list's live legend
// and the viewport line it reserves. A zero now is never live.
func anyLive(rows []sessionStat, now time.Time, window time.Duration) bool {
	for _, r := range rows {
		if isLive(r.last, now, window) {
			return true
		}
	}
	return false
}

// liveLegendText explains the live badge, naming the recency window it stands for so
// the marker is self-describing (shown only when a session is actually live).
func liveLegendText(window time.Duration) string {
	return "live — active in the last " + elapsed(window)
}
