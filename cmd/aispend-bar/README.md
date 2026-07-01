# aispend-bar — macOS menu-bar client

A lean menu-bar app that shows your AI-coding spend at a glance. It is **self-contained**: it
links the aispend engine, brings the ledger current (a bounded, offline scan), and reads it
directly — no server, no port. It paints the worst gauge (or today's spend) into the menu-bar
title, with today's $, ROI, and cache savings in the dropdown.

```
┌──────────────────────────────┐
│ Claude $32.56                 │   ← menu-bar title (worst gauge, or today's spend)
├──────────────────────────────┤
│ Claude · Max 20x              │
│ Today: ≈ $32.56 · 28.3M tokens│
│ ROI: 4.9× vs plan ($6.67/day) │
│ Cache saved: ≈ $111.61 (77%)  │
│ ──────────────               │
│ Refresh now                   │
└──────────────────────────────┘
```

## Why a separate binary

The menu bar uses [`caseymrm/menuet`](https://github.com/caseymrm/menuet), which links Cocoa via
cgo and is **macOS-only**. Keeping it in its own `cmd/aispend-bar` binary means the main `aispend`
tool stays pure-Go, cross-platform, and provably offline — `menuet` never enters `cmd/aispend`'s
dependency graph.

The render logic is the pure, unit-tested `internal/menubar`; the snapshot assembly is
`cli.App.RefreshSnapshots` (shared with the CLI/TUI engine). This file is only the macOS glue
(`main.go`, `//go:build darwin`); `main_other.go` is a stub so `go build ./...` stays green on
Linux/CI.

## Build & run (macOS)

**1. Add the macOS-only dependency** (once, on a Mac):

```bash
go get github.com/caseymrm/menuet
go mod vendor          # this repo vendors deps (see CLAUDE.md)
```

**2. Build and run it as a `.app` bundle.** menuet initializes macOS notifications at startup,
which *crashes* a loose executable (`NSInternalInconsistencyException: bundleProxyForCurrentProcess
is nil`). Use the bundler:

```bash
cmd/aispend-bar/build-app.sh    # → ./aispend-bar.app (ad-hoc signed, menu-bar agent)
open ./aispend-bar.app          # appears in your menu bar
# …or run the inner binary to watch logs (it still resolves the bundle from its path):
./aispend-bar.app/Contents/MacOS/aispend-bar
```

**No `aispend serve` needed** — the bar reads the ledger itself.

Flag: `-interval` — refresh cadence (default `30s`).

## Notes

- An empty menu ("No AI-coding spend today yet.") just means no priced turns today — use Claude
  Code / Codex, or run `aispend scan`.
- Reads local logs only (`~/.claude/projects`, `~/.codex/sessions`) — no network, no credentials.
- For auto-start, use the menu's built-in **Start at Login** (menuet), or add it to Login Items.
- Running the bare `go build ./cmd/aispend-bar` binary outside the `.app` will crash (the macOS
  notifications requirement) — always run it from the bundle.
