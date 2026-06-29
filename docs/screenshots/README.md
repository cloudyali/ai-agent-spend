# Screenshots

The images in this folder are **placeholders**. Replace them with real captures
from your own machine — your actual `~/.claude` / `~/.codex` data makes far more
convincing screenshots than seeded demo numbers, and the colored output is the
whole point.

Capture them in a real terminal (colors only render on an interactive TTY — piped
or redirected output is intentionally plain). A retina terminal at ~120 columns on
a dark theme looks best.

| File | Command to capture | What it shows |
|---|---|---|
| `today.png` | `aispend today` | The daily glance: api-equivalent spend, subscription ROI, cache savings, the hourly spike bar. |
| `tui.png` | `aispend` | The interactive explorer: day-grouped session list with the live badge + legend. |
| `receipt.png` | `aispend` → `↵` on a session | The session receipt: `branch · SHA` line and the per-file cost+churn heatmap. |

Optional extras worth adding: the turn-level **evidence** view (drill `↵` again
into a turn) and a `report --by file` table.

## How to take a clean terminal shot

- **macOS:** run your command, then `⌘⇧4` then `Space` to capture just the window.
- **Any OS:** record with [`asciinema`](https://asciinema.org) and export a frame,
  or use [`vhs`](https://github.com/charmbracelet/vhs) to script a deterministic
  capture (great for keeping screenshots reproducible across releases).
- Keep `NO_COLOR` unset and `TERM` a real value (e.g. `xterm-256color`) so the
  token-class colors (cache-read blue, cache-write amber, output teal, input
  purple) come through.

Then drop the PNGs in this folder with the same names and the README will pick
them up.
