// Period parsing for the `report` command. One grammar, always anchored to
// calendar boundaries — never a rolling, instant-anchored window. A spend number
// you can't pin to a calendar ("the last 168 hours, ending whenever you ran it")
// is a number you can't reconcile against an invoice; every window here snaps to
// a day, week, month, quarter, or year edge so two people running the same
// `--period` on the same day see the same span.
package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// window is a resolved reporting span: an inclusive [Since, Until] range in the
// reference clock's location, plus a human label for the report header. Inclusive
// on both ends to match the store's filter (TSStart >= Since && TSStart <= Until).
type window struct {
	Since, Until time.Time
	Label        string
}

// parsePeriod resolves a --period spec into a calendar-aligned window.
//
// In-progress periods end at now (today, this week/month/quarter/year, since,
// "N days", all). Completed periods end at the last instant of their final day
// (yesterday, last week, last month, an explicit YYYY-MM-DD..YYYY-MM-DD range).
// "N days" is the last N calendar days *including today*, day-aligned to midnight
// — not now minus N×24h. That is the whole point: calendar time, no rolling.
func parsePeriod(spec string, now time.Time) (window, error) {
	loc := now.Location()
	// Light normalization: lowercase, trim, collapse internal whitespace. Hyphens
	// are preserved here because dates contain them; keyword forms get a second,
	// hyphen-folding pass below.
	light := strings.Join(strings.Fields(strings.ToLower(spec)), " ")
	if light == "" {
		return window{}, fmt.Errorf("empty --period (try: today, week, month, \"last week\", quarter, \"this year\", \"90 days\", \"since 2026-01-01\", or 2026-01-01..2026-03-31)")
	}

	// Custom inclusive range: YYYY-MM-DD..YYYY-MM-DD
	if before, after, ok := strings.Cut(light, ".."); ok {
		from, to := strings.TrimSpace(before), strings.TrimSpace(after)
		start, err := dayIn(from, loc)
		if err != nil {
			return window{}, fmt.Errorf("bad range start %q (want YYYY-MM-DD)", from)
		}
		end, err := dayIn(to, loc)
		if err != nil {
			return window{}, fmt.Errorf("bad range end %q (want YYYY-MM-DD)", to)
		}
		if end.Before(start) {
			return window{}, fmt.Errorf("range end %s is before start %s", to, from)
		}
		return window{Since: start, Until: lastInstantBefore(end.AddDate(0, 0, 1)), Label: from + " → " + to}, nil
	}

	// since YYYY-MM-DD
	if light == "since" || strings.HasPrefix(light, "since ") {
		d := strings.TrimSpace(strings.TrimPrefix(light, "since"))
		day, err := dayIn(d, loc)
		if err != nil {
			return window{}, fmt.Errorf("bad since date %q (want YYYY-MM-DD)", d)
		}
		return window{Since: day, Until: now, Label: "since " + d}, nil
	}

	// N days / N day / Nd — calendar-aligned, including today
	if n, ok := parseDayCount(light); ok {
		if n < 1 {
			return window{}, fmt.Errorf("--period %q: day count must be >= 1", spec)
		}
		unit := "days"
		if n == 1 {
			unit = "day"
		}
		return window{
			Since: startOfDay(now).AddDate(0, 0, -(n - 1)),
			Until: now,
			Label: fmt.Sprintf("last %d %s", n, unit),
		}, nil
	}

	// Fixed keywords (hyphen-folded so "last-week" == "last week").
	switch strings.Join(strings.Fields(strings.ReplaceAll(light, "-", " ")), " ") {
	case "today":
		return window{Since: startOfDay(now), Until: now, Label: "today"}, nil
	case "yesterday":
		start := startOfDay(now).AddDate(0, 0, -1)
		return window{Since: start, Until: lastInstantBefore(startOfDay(now)), Label: "yesterday"}, nil
	case "week", "this week":
		return window{Since: startOfWeek(now), Until: now, Label: "this week"}, nil
	case "last week":
		return window{Since: startOfWeek(now).AddDate(0, 0, -7), Until: lastInstantBefore(startOfWeek(now)), Label: "last week"}, nil
	case "month", "this month":
		return window{Since: startOfMonth(now), Until: now, Label: "this month"}, nil
	case "last month":
		return window{Since: startOfMonth(now).AddDate(0, -1, 0), Until: lastInstantBefore(startOfMonth(now)), Label: "last month"}, nil
	case "quarter", "this quarter":
		return window{Since: startOfQuarter(now), Until: now, Label: "this quarter"}, nil
	case "year", "this year":
		return window{Since: startOfYear(now), Until: now, Label: "this year"}, nil
	case "all", "all time":
		return window{Since: time.Time{}, Until: now, Label: "all time"}, nil
	}

	return window{}, fmt.Errorf("unknown --period %q (try: today, yesterday, week, month, \"last week\", \"last month\", quarter, \"this year\", \"90 days\", \"since 2026-01-01\", 2026-01-01..2026-03-31, or all)", spec)
}

// parseDayCount recognizes "N days", "N day", and "Nd"; returns (n, true) on a
// match so the caller can reject n < 1 with a precise message.
func parseDayCount(s string) (int, bool) {
	if rest, ok := strings.CutSuffix(s, "d"); ok {
		if n, err := strconv.Atoi(rest); err == nil {
			return n, true
		}
	}
	if f := strings.Fields(s); len(f) == 2 && (f[1] == "days" || f[1] == "day") {
		if n, err := strconv.Atoi(f[0]); err == nil {
			return n, true
		}
	}
	return 0, false
}

// dayIn parses YYYY-MM-DD and anchors it at midnight in loc (the reference clock's
// zone), so an explicit date lines up with the day boundaries today/week/month use.
func dayIn(s string, loc *time.Location) (time.Time, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc), nil
}

// lastInstantBefore turns a half-open [start, boundary) calendar span into the
// inclusive Until the store filters on: the final representable instant before
// the exclusive boundary.
func lastInstantBefore(boundary time.Time) time.Time { return boundary.Add(-time.Nanosecond) }

// startOfQuarter returns the first day (00:00) of the calendar quarter containing
// t — Jan/Apr/Jul/Oct 1 — in t's own location.
func startOfQuarter(t time.Time) time.Time {
	startMonth := time.Month(((int(t.Month())-1)/3)*3 + 1)
	return time.Date(t.Year(), startMonth, 1, 0, 0, 0, 0, t.Location())
}

// startOfYear returns Jan 1 00:00 of t's year, in t's own location.
func startOfYear(t time.Time) time.Time {
	return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, t.Location())
}
