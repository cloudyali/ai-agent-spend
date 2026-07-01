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
	Logo       template.HTML
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
		p := providerVM{Name: s.DisplayName, Plan: s.Plan, Idle: s.Idle, Spark: spark(s.Trend), Logo: providerLogo(s.ProviderID)}
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

// providerLogo returns a small inline brand mark for a provider id (canonicalized upstream:
// claude, codex, gemini, …), used to identify the service at a glance. The SVGs are original,
// static renditions — no user data flows in, so template.HTML is safe here. Unknown providers
// get a neutral dot. To swap in an official path later, replace the matching builder's body.
func providerLogo(id string) template.HTML {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "claude", "claude_code", "anthropic":
		return template.HTML(markSVG("Anthropic", "#D97757", claudeMarkPath)) // Claude "clay"
	case "codex", "openai":
		return template.HTML(markSVG("OpenAI", "currentColor", codexMarkPath)) // monochrome → tracks theme
	case "gemini", "google":
		return template.HTML(sparkMark("Gemini", "#4285F4"))
	default:
		return template.HTML(`<svg viewBox="0 0 24 24" role="img" aria-label="AI"><circle cx="12" cy="12" r="5" fill="#8a8a8e"/></svg>`)
	}
}

// markSVG wraps a 100×100 provider glyph path in an accessible <svg>. fill may be a hex or
// "currentColor" (to track the surrounding text); label doubles as the accessible name.
func markSVG(label, fill, path string) string {
	return `<svg viewBox="0 0 100 100" role="img" aria-label="` + label + `" fill="` + fill +
		`"><path d="` + path + `"/></svg>`
}

// claudeMarkPath and codexMarkPath are the Anthropic and OpenAI brand glyphs, vendored from
// OpenUsage (github.com/robinebers/openusage — Sources/OpenUsage/Resources/ProviderIcons,
// MIT) with coordinates rounded to the 100×100 grid (imperceptible at menu-bar size). The
// marks are the respective companies' trademarks, used only to identify each service. See NOTICE.
const (
	claudeMarkPath = `M26 63L41 54L42 54L41 53H41L38 53L29 53L21 52L14 52L12 52L10 49L10 48L12 47L14 47L19 48L27 48L32 48L40 49H42L42 49L41 48L41 48L33 43L25 37L20 34L18 32L17 31L16 27L18 25L21 25L22 25L25 28L31 33L40 39L41 40L41 39L41 39L41 38L36 30L32 22L29 18L29 16C29 15 28 15 28 14L31 10L32 10L36 10L37 12L39 16L42 24L48 34L49 37L50 40L50 41H51V40L51 34L52 27L53 18L53 16L54 13L57 11L59 12L60 14L60 16L59 22L57 32L56 38H57L58 37L61 33L66 26L69 23L72 20L73 19H77L79 23L78 27L75 31L72 35L68 41L65 45L65 46L66 45L75 43L80 42L87 41L89 43L90 44L89 47L82 48L74 50L63 53L63 53L63 53L68 53L70 54H76L86 54L88 56L90 58L90 60L86 62L80 61L67 57L63 56H62V57L66 60L73 66L81 74L82 76L80 78L79 77L72 72L69 69L63 64H62V65L64 67L72 78L72 82L71 83L69 84L67 84L63 77L58 70L54 63L54 64L51 88L50 89L48 90L46 88L45 86L46 81L47 75L48 70L49 63L50 61L50 61L49 61L44 68L37 77L31 84L30 84L28 83L28 81L29 79L37 69L42 62L45 59L45 58H45L24 72L20 72L18 71L19 68L19 68L26 63L26 63Z`
	codexMarkPath  = `M84 43C85 40 85 37 85 34C84 32 83 29 82 26C78 19 69 15 60 17C58 14 55 12 52 11C48 10 45 10 41 11C38 11 34 13 32 15C29 18 27 21 26 24C23 25 21 26 18 27C16 29 14 31 13 34C8 41 9 51 15 57C14 60 14 63 14 66C15 69 16 71 17 74C21 81 30 85 39 83C41 85 43 87 45 88C48 89 51 90 54 90C62 90 70 84 73 76C76 75 78 74 81 73C83 71 85 69 86 66C91 59 90 49 84 43ZM54 85C50 85 47 84 44 81L45 81L61 72C61 72 61 71 61 71C62 71 62 70 62 70V47L69 51C69 51 69 51 69 51V70C69 78 62 85 54 85ZM21 71C20 68 19 64 20 61L20 61L36 71C37 71 37 71 37 71C38 71 38 71 39 71L58 59V67C58 67 58 67 58 67C58 67 58 67 58 67L42 77C35 81 26 78 21 71ZM17 36C19 33 22 31 25 30V49C25 49 25 50 26 50C26 50 26 51 26 51L46 62L39 66C39 66 39 66 39 66C39 66 39 66 39 66L23 57C16 53 13 43 17 36V36ZM73 49L53 38L60 34C60 34 60 34 60 34C60 34 60 34 60 34L76 43C79 45 81 47 82 49C83 52 84 55 84 58C83 60 82 63 81 65C79 68 77 69 74 70V51C74 51 74 51 74 50C73 50 73 49 73 49ZM79 39L79 39L63 30C63 29 62 29 62 29C61 29 61 29 60 30L41 41V33C41 33 41 33 41 33C41 33 41 33 41 33L57 24C60 22 62 21 65 22C68 22 71 23 73 24C75 26 77 28 78 31C79 33 80 36 79 39V39H79ZM37 53L30 49C30 49 30 49 30 49C30 49 30 49 30 49V30C30 27 31 25 33 22C34 20 36 18 39 17C42 16 44 15 47 15C50 16 53 17 55 19L54 19L39 28C38 29 38 29 38 29C37 30 37 30 37 31L37 53V53ZM41 45L50 40L58 45V55L50 60L41 55L41 45Z`
)

// sparkMark renders a four-point Gemini-style spark (concave star) in a brand color. Kept as
// a rendition — OpenUsage ships no Gemini icon (github.com/robinebers/openusage).
func sparkMark(label, color string) string {
	return `<svg viewBox="0 0 24 24" role="img" aria-label="` + label + `"><path fill="` + color +
		`" d="M12 2c.6 5.2 4.8 9.4 10 10-5.2.6-9.4 4.8-10 10-.6-5.2-4.8-9.4-10-10 5.2-.6 9.4-4.8 10-10Z"/></svg>`
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
.name{display:flex;align-items:center;gap:6px;font-weight:600;min-width:0}
.logo{width:15px;height:15px;flex:none;display:inline-flex}
.logo svg{width:100%;height:100%;display:block}
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
.idle{display:flex;align-items:center;gap:6px;padding:10px 12px;color:var(--dim);font-weight:600}
.sep{border-top:1px solid var(--line)}
.foot{display:flex;gap:16px;padding:9px 12px;border-top:1px solid var(--line);font-size:12px}
.foot a{color:var(--fg);text-decoration:none}
</style></head><body>
{{if .Empty}}<div class="prov"><div class="sub">No AI-coding spend today yet.</div></div>{{else}}{{range $i, $p := .Providers}}{{if $i}}<div class="sep"></div>{{end}}{{if $p.Idle}}<div class="idle"><span class="logo">{{$p.Logo}}</span><span>{{$p.Name}}{{if $p.Plan}} · {{$p.Plan}}{{end}} — idle today</span></div>{{else}}<div class="prov">
<div class="head"><span class="name"><span class="logo">{{$p.Logo}}</span><span>{{$p.Name}}{{if $p.Plan}} · {{$p.Plan}}{{end}}</span></span>{{if $p.ROI}}<span class="roi">{{$p.ROI}}</span>{{end}}</div>
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
