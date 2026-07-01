//go:build darwin

// Command aispend-bar is the macOS menu-bar client: a status-bar item whose popover is a
// WKWebView rendering the rich HTML from internal/webui, refreshed from the engine every
// interval. It talks to the system Cocoa/WebKit frameworks directly via cgo (bar.h +
// bar_darwin.m) — no external Go UI dependency. The pure pieces (snapshot assembly, the
// menu-bar title, the popover HTML) live and are tested in internal/{cli,menubar,webui};
// this file is only the glue that pushes them into AppKit.
package main

/*
#cgo darwin CFLAGS: -x objective-c -fobjc-arc
#cgo darwin LDFLAGS: -framework Cocoa -framework WebKit
#include <stdlib.h>
#include "bar.h"
*/
import "C"

import (
	"flag"
	"io"
	"runtime"
	"time"
	"unsafe"

	"github.com/cloudyali/ai-agent-spend/internal/cli"
	"github.com/cloudyali/ai-agent-spend/internal/menubar"
	"github.com/cloudyali/ai-agent-spend/internal/platform"
	"github.com/cloudyali/ai-agent-spend/internal/webui"
)

// app is the read-only engine handle: set in main before the run loop starts, then read
// by the refresh goroutine and the exported action callback.
var app *cli.App

func init() { runtime.LockOSThread() } // AppKit's run loop must own the main OS thread

func main() {
	interval := flag.Duration("interval", 30*time.Second, "how often to re-scan and refresh")
	flag.Parse()

	app = &cli.App{
		Resolver: platform.Detect(),
		Now:      func() time.Time { return time.Now().UTC() },
		Out:      io.Discard,
		Err:      io.Discard,
	}

	// Refresh off the main thread (the scan + best-effort quota fetch do I/O); the C
	// helpers marshal the UI updates back onto the main queue.
	go func() {
		doRefresh()
		for range time.Tick(*interval) {
			doRefresh()
		}
	}()

	C.RunBar() // enters [NSApp run]; blocks until QuitBar
}

// doRefresh rebuilds the title and popover HTML from a fresh scan and pushes them to the
// status item and web view. The C strings are copied into NSStrings synchronously inside
// the helpers, so freeing them here is safe.
func doRefresh() {
	now := app.Now()
	snaps := app.RefreshSnapshots(now)
	title := C.CString(menubar.Render(snaps, now).Title)
	html := C.CString(webui.Render(snaps, now))
	C.SetTitle(title)
	C.SetHTML(html)
	C.free(unsafe.Pointer(title))
	C.free(unsafe.Pointer(html))
}

//export goAction
func goAction(action *C.char) {
	switch C.GoString(action) {
	case "refresh":
		go doRefresh()
	case "quit":
		C.QuitBar()
	}
}
