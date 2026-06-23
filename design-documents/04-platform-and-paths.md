# 04 — Platform & Paths (OS-awareness)

_Last updated: 2026-06-14 · Stable. Cross-cutting: every provider depends on this._

aispend sources most of its data from **local files**, and those files live in
different places on macOS, Linux, and Windows. A single static cross-platform Go
binary has to find them correctly on each OS. So OS-awareness is not an
afterthought sprinkled through providers — it is **centralized in one package,
`internal/platform`**, and everything that touches a path goes through it.

> The mental model: a provider never *knows* where its files are. It *asks* the
> platform layer — "where might Claude Code keep sessions on this machine?" — and
> gets back the OS-correct, env-override-aware, existence-checked answer. Move to
> a new OS and you change one package, not twelve.

---

## 1. Principles

- **Never hardcode `~/.claude` with `/`.** Use `os.UserHomeDir`, and join with
  `path/filepath` (OS-aware separators) — **never** the `path` package, which is
  slash-only and silently wrong on Windows.
- **One resolver, injected, fully testable.** The resolver takes `GOOS`, a home
  directory, and an env lookup as inputs, so Windows path logic is unit-tested
  from a Linux CI runner by injecting `GOOS="windows"` — no Windows machine
  needed.
- **Env overrides win.** `AISPEND_HOME`, `CLAUDE_CONFIG_DIR`, etc. take
  precedence over defaults, so CI, fixtures, containers, and non-standard installs
  all work, and tests can point the resolver at a temp dir.
- **Candidates, then existence.** A provider asks for an *ordered list of
  candidate roots*; the platform layer returns the subset that exists. Missing
  roots become *reported* (not crashed) — feeding the "unsupported source
  detected, never silently dropped" promise (PRD G1).
- **Path hashing normalizes for the platform.** `source_path_hash` is computed
  from a cleaned, platform-normalized path (case-folded on case-insensitive
  filesystems — macOS default and Windows — separators unified) so the *same*
  file yields the *same* hash however it was referenced. Raw paths are never
  stored or exported (see [02-data-model.md](02-data-model.md)).

## 2. Known locations (the map the resolver encodes)

| Data | macOS | Linux | Windows | Env override |
|---|---|---|---|---|
| **Claude Code** sessions (0A) | `~/.claude/projects` | `~/.claude/projects` (XDG `$XDG_CONFIG_HOME/claude` fallback) | `%USERPROFILE%\.claude\projects` | `CLAUDE_CONFIG_DIR` (comma-sep; dir or its `projects/`) |
| **App home** `~/.aispend` (db, config) | `~/.aispend` | `~/.aispend` | `%USERPROFILE%\.aispend` | `AISPEND_HOME` |
| **Cursor** state (0B) | `~/Library/Application Support/Cursor` | `~/.config/Cursor` | `%APPDATA%\Cursor` | `CURSOR_CONFIG_DIR` |
| **Codex** sessions (0B) | `~/.codex` | `~/.codex` | `%USERPROFILE%\.codex` | `CODEX_HOME` (comma-sep; scans `sessions/` + `archived_sessions/`) |

Two deliberate choices:

- **App home stays a single visible `~/.aispend`** (with a documented Windows
  equivalent and an `AISPEND_HOME` override) rather than being buried in
  OS-specific app-data dirs — because the README promise is literally "stored
  locally in `~/.aispend/`," and a promise the user can `ls` is worth keeping
  legible. The Windows path is the `%USERPROFILE%` analog.
- **Provider data dirs follow each vendor's real convention per OS** (Cursor uses
  Application Support / `.config` / `%APPDATA%`; Claude Code and Codex use a
  dotdir in home). We encode what the vendor actually does, not a uniform guess.

### Resolution nuances per agent (verified against ccusage)

The env overrides are richer than a single path, and the discovery rules below are
how ccusage actually resolves them — a parser that only globs `~/.claude` misses
real installs:

- **Claude Code.** `CLAUDE_CONFIG_DIR` is **comma-separated** (multiple roots), and
  each entry may be the config dir **or** the `projects/` dir itself. Resolution
  order: `CLAUDE_CONFIG_DIR` entries → `$XDG_CONFIG_HOME/claude` (i.e. `~/.config/claude`)
  → `~/.claude`; a root only counts if it contains a `projects/` dir. Recurse the
  `projects/` tree for `*.jsonl` and derive the session id from the path shape —
  the layout can be flat (`projects/<proj>/<session>.jsonl`), nested
  (`<session>/chat.jsonl`), or hold `subagents/<worker>.jsonl`.
- **Cowork (Claude desktop app).** Built on Claude Code, but it writes transcripts
  to its **own per-session config dir**, not `~/.claude` — on macOS under
  `~/Library/Application Support/Claude/local-agent-mode-sessions/<ws>/<conv>/local_<id>/.claude/projects/<mangled-cwd>/<uuid>.jsonl`
  (Windows: `%APPDATA%\Claude\…`). It sets `CLAUDE_CONFIG_DIR` per session, so a
  **terminal** scan (where that env is unset) sees none of it. Add the
  `local-agent-mode-sessions` base as a default `claude_code` root and recurse it,
  **skipping `outputs/`, `uploads/`, `node_modules/`** (artifact trees beside the
  transcripts, not session logs). Format is identical to terminal Claude Code, so the
  same normalizer/dedup apply; cross-root double-counting can't happen because
  `EventID` is per-response. Found the hard way (Session 15): a terminal-only scan
  undercounted by the **entire** desktop footprint. Caveat: the session cwd is the
  Cowork outputs dir, so project attribution needs the connected-folder/session
  metadata, not cwd (the open half of the Cowork task).
- **Codex.** `CODEX_HOME` is **comma-separated**; under each home scan **both**
  `sessions/` and `archived_sessions/`, and when the same session appears in both,
  let the active `sessions/` copy win (dedupe by relative path).
- **Cursor.** No single session log: read **both** the global
  `globalStorage/state.vscdb` and every per-workspace `workspaceStorage/<hash>/state.vscdb`,
  opened **read-only/immutable**. (Details in [phase-0B](phase-0B-provider-coverage-and-findings.md).)

## 3. The interface

```go
package platform

// Resolver is constructed from injectable inputs so every OS is testable anywhere.
type Resolver struct {
    GOOS string                   // "darwin" | "linux" | "windows"
    Home string                   // resolved once (os.UserHomeDir), or injected in tests
    Env  func(string) string      // os.Getenv, or a fake map in tests
}

func Detect() Resolver                                  // real OS, real env, real home

func (r Resolver) AppHome() string                      // ~/.aispend (or $AISPEND_HOME)
func (r Resolver) AppDBPath() string                    // <AppHome>/aispend.db
func (r Resolver) ProviderRoots(provider string) []string   // ordered candidates, OS-correct
func (r Resolver) ExistingRoots(provider string) []string   // candidates that exist on disk

// HashPath normalizes p for the platform (clean, separators, case-fold where the
// filesystem is case-insensitive), then returns the stable source_path_hash.
func HashPath(p, goos string) string
```

Providers consume only `ExistingRoots(name)` and `HashPath`. `AppHome` /
`AppDBPath` back the store and config loaders. The thin `Detect()` wires real
inputs; everything else is pure and unit-tested.

## 4. Demonstratable output

OS-awareness is itself made auditable — `doctor` will grow a `--paths` view so a
user (and a security reviewer) can see exactly where the tool looked:

```console
$ aispend doctor --paths
os: darwin
app home: /Users/you/.aispend            (override: AISPEND_HOME unset)
db:       /Users/you/.aispend/aispend.db
claude_code roots:
  /Users/you/.claude/projects            ✓ exists
  $CLAUDE_CONFIG_DIR/projects            – unset
```

## 5. Test plan (this is why the resolver is injectable)

- `AppHome` honors `AISPEND_HOME`, else falls back to `<home>/.aispend`, on each `GOOS`.
- `ProviderRoots("claude_code")` returns the OS-correct candidate list for `darwin`, `linux`, and `windows` — all asserted from one CI runner via injected `GOOS`.
- `CLAUDE_CONFIG_DIR` override appears ahead of the default candidate.
- `ExistingRoots` filters to real directories (tested against a temp dir).
- `HashPath` is stable across separator/case variants of the same path on case-insensitive platforms, and distinct paths never collide.

## 6. How later phases use it

- **0A:** the Claude Code provider discovers sessions via `ExistingRoots("claude_code")`; the store/config use `AppHome`/`AppDBPath`.
- **0B:** Cursor and Codex providers add their rows to the table above — no change to call sites.
- **1A/1B:** the self-host collector resolves its own data/working dirs through the same layer; OTel/admin-API sources register non-filesystem origins but still report through the same provenance.
