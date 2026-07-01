// Package webui renders the menu-bar popover as a self-contained HTML document from the
// shared snapshot model — the rich, mockup-matching surface the macOS WKWebView popover
// loads (cmd/aispend-bar). It is pure Go (html/template, so untrusted provider text is
// auto-escaped), which keeps the exact markup unit-testable without a Mac; the AppKit +
// WebKit glue that hosts the document is the only part that can't be tested off-device.
package webui

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/lines"
)

type gaugeVM struct {
	Label string
	Pct   int
	Width int
	Color template.CSS
	Reset string
	Pace  string
}

type providerVM struct {
	Name, Plan string
	ROI        string
	CacheSaved string
	Today      string
	Spark      string
	Idle       bool
	Gauges     []gaugeVM
}

type pageVM struct {
	Providers []providerVM
	Empty     bool
}

// Render returns a complete HTML document for the popover with the snapshot data inlined,
// so the glue simply reloads this string on each refresh — no JS data bridge. Action
// links use the aispend:// scheme for the WKWebView navigation delegate to intercept.
func Render(snaps []lines.Snapshot, now time.Time) string {
	vm := pageVM{Empty: len(snaps) == 0}
	for _, s := range snaps {
		p := providerVM{Name: s.DisplayName, Plan: s.Plan, Idle: s.Idle, Spark: spark(s.Trend)}
		for i := range s.Lines {
			ln := s.Lines[i]
			switch {
			case ln.Type == "progress" && ln.Used != nil:
				p.Gauges = append(p.Gauges, gaugeVM{
					Label: ln.Label,
					Pct:   clampPct(*ln.Used),
					Width: clampPct(*ln.Used),
					Color: safeColor(ln.Color),
					Reset: resetText(ln, now),
					Pace:  paceText(ln),
				})
			case ln.Label == "ROI":
				p.ROI = ln.Value
			case ln.Label == "Cache saved":
				p.CacheSaved = ln.Value
			case ln.Label == "Today":
				p.Today = ln.Value
			}
		}
		vm.Providers = append(vm.Providers, p)
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, vm); err != nil {
		return "<!doctype html><body>render error</body>"
	}
	return b.String()
}

// clampPct rounds a 0..100 usage value to an int, clamped at both ends.
func clampPct(v float64) int {
	switch {
	case v < 0:
		return 0
	case v > 100:
		return 100
	default:
		return int(v + 0.5)
	}
}

// safeColor passes a "#rrggbb" hex through as trusted CSS; anything else falls back to a
// neutral so a stray value can't inject into the style attribute.
func safeColor(c string) template.CSS {
	ok := len(c) == 7 && c[0] == '#'
	for i := 1; ok && i < len(c); i++ {
		h := c[i]
		if !((h >= '0' && h <= '9') || (h >= 'a' && h <= 'f') || (h >= 'A' && h <= 'F')) {
			ok = false
		}
	}
	if !ok {
		return template.CSS("#8a8a8e")
	}
	return template.CSS(c)
}

func resetText(ln lines.Line, now time.Time) string {
	if ln.ResetsAt == nil {
		return ""
	}
	return "resets in " + humanDur(ln.ResetsAt.Sub(now))
}

func paceText(ln lines.Line) string {
	p := ln.Projection
	if p == nil {
		return ""
	}
	switch {
	case p.Breaches:
		return "on pace to run out in " + humanDur(time.Duration(p.ETASeconds)*time.Second)
	case p.ProjectedUsed > 0:
		return fmt.Sprintf("on pace ~%.0f%% by reset", p.ProjectedUsed)
	}
	return ""
}

// spark renders a series as a unicode sparkline scaled to its own max; it returns "" for
// a trivial series (fewer than two days, or all zero) so the template omits the row.
func spark(vals []int64) string {
	var peak int64
	for _, v := range vals {
		if v > peak {
			peak = v
		}
	}
	if len(vals) < 2 || peak == 0 {
		return ""
	}
	ramp := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	for _, v := range vals {
		b.WriteRune(ramp[int(float64(v)/float64(peak)*float64(len(ramp)-1)+0.5)])
	}
	return b.String()
}

// humanDur renders a coarse countdown ("1d 2h", "1h 30m", "5m", "<1m", "now").
func humanDur(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	days := int(d / (24 * time.Hour))
	hours := int((d % (24 * time.Hour)) / time.Hour)
	mins := int((d % time.Hour) / time.Minute)
	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case days > 0:
		return fmt.Sprintf("%dd", days)
	case hours > 0 && mins > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	case mins > 0:
		return fmt.Sprintf("%dm", mins)
	default:
		return "<1m"
	}
}

var tmpl = template.Must(template.New("popover").Parse(`<!doctype html><html><head><meta charset="utf-8"><style>
:root{--bg:#fff;--fg:#1d1d1f;--dim:#8a8a8e;--line:#e5e5ea;--track:#e9e9ec;--roi-bg:#e1f5ee;--roi-fg:#0f6e56}
@media (prefers-color-scheme:dark){:root{--bg:#1e1e1e;--fg:#f2f2f7;--dim:#98989d;--line:#3a3a3c;--track:#3a3a3c;--roi-bg:#0f3b30;--roi-fg:#5dcaa5}}
*{box-sizing:border-box}
body{margin:0;padding:6px 0;width:320px;background:var(--bg);color:var(--fg);font:13px/1.35 -apple-system,system-ui,sans-serif}
.prov{padding:10px 12px}
.head{display:flex;align-items:center;justify-content:space-between;gap:8px}
.name{font-weight:600}
.roi{font-size:12px;font-weight:600;padding:2px 8px;border-radius:6px;background:var(--roi-bg);color:var(--roi-fg);white-space:nowrap}
.sub{font-size:12px;color:var(--dim);margin-top:2px}
.hr{height:1px;background:var(--line);margin:8px 0}
.gauge{margin:7px 0}
.grow{display:flex;align-items:center;justify-content:space-between;gap:8px}
.meta{color:var(--dim);font-size:11px;white-space:nowrap}
.bar{height:6px;border-radius:3px;background:var(--track);overflow:hidden;margin-top:4px}
.bar>i{display:block;height:100%;border-radius:3px}
.pace{color:var(--dim);font-size:11px;margin-top:3px}
.today{font-size:12px;color:var(--dim);margin-top:7px}
.trend{display:flex;justify-content:space-between;align-items:center;margin-top:7px}
.spark{font-family:ui-monospace,monospace;letter-spacing:1px;color:var(--fg)}
.idle{padding:10px 12px;color:var(--dim);font-weight:600}
.sep{border-top:1px solid var(--line)}
.foot{display:flex;gap:16px;padding:9px 12px;border-top:1px solid var(--line);font-size:12px}
.foot a{color:var(--fg);text-decoration:none}
</style></head><body>
{{if .Empty}}<div class="prov"><div class="sub">No AI-coding spend today yet.</div></div>{{else}}{{range $i, $p := .Providers}}{{if $i}}<div class="sep"></div>{{end}}{{if $p.Idle}}<div class="idle">{{$p.Name}}{{if $p.Plan}} · {{$p.Plan}}{{end}} — idle today</div>{{else}}<div class="prov">
<div class="head"><span class="name">{{$p.Name}}{{if $p.Plan}} · {{$p.Plan}}{{end}}</span>{{if $p.ROI}}<span class="roi">{{$p.ROI}}</span>{{end}}</div>
{{if $p.CacheSaved}}<div class="sub">Cache saved · {{$p.CacheSaved}}</div>{{end}}
{{if $p.Gauges}}<div class="hr"></div>{{end}}{{range $p.Gauges}}<div class="gauge">
<div class="grow"><span>{{.Label}}</span><span class="meta">{{.Pct}}% · {{.Reset}}</span></div>
<div class="bar"><i style="width:{{.Width}}%;background:{{.Color}}"></i></div>
{{if .Pace}}<div class="pace">{{.Pace}}</div>{{end}}
</div>{{end}}
{{if $p.Today}}<div class="today">{{$p.Today}}</div>{{end}}
{{if $p.Spark}}<div class="trend"><span class="meta">Trend</span><span class="spark">{{$p.Spark}}</span></div>{{end}}
</div>{{end}}{{end}}{{end}}
<div class="foot"><a href="aispend://refresh">Refresh</a><a href="aispend://quit">Quit</a></div>
</body></html>`))
