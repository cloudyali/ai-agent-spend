//go:build darwin

// Command aispend-bar is the macOS menu-bar client for aispend. It is **self-contained**:
// every few seconds it brings the ledger current (a bounded, offline incremental scan)
// and renders the worst gauge into the menu-bar title, with each provider's windows and
// the dollarized wall in the dropdown — reading the engine directly, no separate process,
// no port. (This replaced an earlier HTTP-client design that needed `aispend serve`.)
//
// The logic (snapshot assembly + render) is pure and tested (internal/cli, internal/menubar);
// this file is only the macOS glue behind a darwin build tag, linking the Cocoa-based
// menuet library via cgo. Build on macOS with cmd/aispend-bar/build-app.sh.
package main

import (
	"flag"
	"io"
	"sync/atomic"
	"time"

	"github.com/caseymrm/menuet"

	"github.com/cloudyali/ai-agent-spend/internal/cli"
	"github.com/cloudyali/ai-agent-spend/internal/menubar"
	"github.com/cloudyali/ai-agent-spend/internal/platform"
)

// current holds the latest rendered menu State so menuet's Children callback paints
// without doing I/O on the UI thread.
var current atomic.Pointer[menubar.State]

func main() {
	interval := flag.Duration("interval", 30*time.Second, "how often to re-scan and refresh")
	flag.Parse()

	// A read-only App wired to the real environment — the same construction the CLI uses
	// (cli.Run) — with all output discarded, since the menu bar is the UI.
	app := &cli.App{
		Resolver: platform.Detect(),
		Now:      func() time.Time { return time.Now().UTC() },
		Out:      io.Discard,
		Err:      io.Discard,
	}

	refresh := func() {
		now := app.Now()
		st := menubar.Render(app.RefreshSnapshots(now), now)
		current.Store(&st)
		menuet.App().SetMenuState(&menuet.MenuState{Title: st.Title})
		menuet.App().MenuChanged()
	}

	go func() {
		refresh()
		for range time.Tick(*interval) {
			refresh()
		}
	}()

	a := menuet.App()
	a.Name = "AiSpend"
	a.Label = "io.cloudyali.aispend-bar"
	a.Children = menuChildren(refresh)
	a.RunApplication()
}

// menuChildren builds the dropdown from the latest State, appending a Refresh action.
// menuet appends its own "Start at Login" / "Quit" footer, so we don't.
func menuChildren(refresh func()) func() []menuet.MenuItem {
	return func() []menuet.MenuItem {
		var out []menuet.MenuItem
		if st := current.Load(); st != nil {
			for _, it := range st.Items {
				out = append(out, toMenuItem(it))
			}
		}
		out = append(out,
			menuet.MenuItem{Type: menuet.Separator},
			menuet.MenuItem{Text: "Refresh now", Clicked: refresh},
		)
		return out
	}
}

// toMenuItem paints one menubar.Item with the After hierarchy: the provider Header is
// bold, the ROI Hero is semibold, Dim (secondary) rows shrink, a Separator is a divider,
// and Children become a submenu (a collapsed idle provider's detail, or the Trend spark).
func toMenuItem(it menubar.Item) menuet.MenuItem {
	if it.Separator {
		return menuet.MenuItem{Type: menuet.Separator}
	}
	mi := menuet.MenuItem{Text: it.Text}
	switch {
	case it.Header:
		mi.FontWeight = menuet.WeightBold
		mi.FontSize = 15
	case it.Hero:
		mi.FontWeight = menuet.WeightSemibold
	case it.Dim:
		mi.FontSize = 12
	}
	if len(it.Children) > 0 {
		kids := make([]menuet.MenuItem, len(it.Children))
		for i, c := range it.Children {
			kids[i] = toMenuItem(c)
		}
		mi.Children = func() []menuet.MenuItem { return kids }
	}
	return mi
}
