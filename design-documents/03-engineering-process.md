# 03 — Engineering Process

_Last updated: 2026-06-14 · Stable. These are the rules the build is held to,
independent of phase._

Three commitments govern every change-set in this repo, and they are not
negotiable per the project charter: **t-wada–style TDD**, a **85–90% coverage
floor**, and **code-review + security-review gates** before anything is
considered done. This doc says exactly what each one means here.

---

## 1. t-wada–style TDD

We follow the discipline Takuto Wada teaches (classic Beck TDD, small steps).
The loop is **Red → Green → Refactor**, one test at a time, and it is driven off
a visible **test TODO list** that we keep at the top of each cycle.

The rhythm:

1. **Write the test list.** Before code, enumerate the behaviors as a checklist
   (e.g. "Money: zero value is `$0.000000`; `Add` sums micros; mixing currencies
   errors; JSON round-trips"). The list is the plan; we work it top to bottom.
2. **Red.** Write *one* small failing test for the next behavior. Run it. See it
   fail for the *right* reason (a wrong assertion is not a red).
3. **Green.** Write the *minimum* code to pass — and we genuinely mean minimum.
   Three tactics, in order of preference as confidence grows:
   - **Fake it** — return the constant the test expects.
   - **Triangulate** — add a second example that forces the real generalization.
   - **Obvious implementation** — only when the code is truly obvious.
4. **Refactor.** With the bar green, remove duplication and improve names. Tests
   stay green throughout. Refactoring without a green bar is not refactoring.
5. **Commit on green.** Each commit is a passing state with a message naming the
   behavior added ("event: Money.Add sums micros, rejects currency mismatch").

Two working rules that keep the loop honest:

- **Tests are the specification.** A behavior that isn't in a test isn't a
  promise. The golden fixtures (`testdata/golden/`) are executable spec for the
  `AgentEvent` contract.
- **Don't test what you don't own naively.** We wrap the SQLite driver behind the
  `Store` interface and TDD an in-memory `Store` first; the SQLite implementation
  then has to satisfy the *same* test suite. Same contract, two implementations.

## 2. Coverage floor: 85–90%

- Every package with logic carries **≥ 85%** statement coverage; we aim for the
  **85–90%** band on the cost/normalize/store core where the numbers live.
- Measured with `go test ./... -coverprofile=coverage.out` and reported per
  package; `go tool cover -func=coverage.out` gives the breakdown.
- Coverage is a floor, not a target to game. The point is that every branch that
  produces a *number a user might `explain`* is exercised — especially the
  `nil`-cost-view and low-confidence paths, which are exactly where trust is won
  or lost.
- Pure scaffolding (a `package x` doc stub, the thin `main`) is exempt until it
  gains logic. The CLI render layer is covered by golden-output tests.

## 3. Review gates (before a phase is "done")

Two reviews run on the change-set, using the installed tooling, and both must be
clean (or have logged, accepted exceptions):

| Gate | What it checks | Tooling |
|---|---|---|
| **Code review** | Go error handling, concurrency, interface design, table-test quality, SQL/N+1, coverage gaps | the `technical-code-reviewing` skill (Go/React/SQL/Docker aware) |
| **Security review** | Egress paths, secret handling, path/PII leakage, dependency risk, injection | the **Security Guidance** plugin / `security-review` |

For AgentSpend specifically, the security review has a standing checklist beyond
the generic pass, because the product's entire promise is trust:

- The default build's import graph contains **no `net/*` transport** (asserted in
  CI, see §4).
- **No raw filesystem paths** are persisted, logged, or exported — only
  `*_path_hash`.
- Credentials/tokens never touch the events DB and never appear in logs.
- New dependencies are justified, pinned, and pure-Go where it preserves the
  single-static-binary promise.

## 4. CI gates (every push)

```text
go vet ./...                     # static checks
go build ./cmd/aispend           # default build compiles
go test ./... -race -cover       # tests + data races + coverage
golden fixtures                  # AgentEvent output is byte-stable (regen with -update)
# No-egress assertion — through Phase 0A the default build imports NO net package:
  go list -deps ./cmd/aispend | grep -Eq '^net(/|$)' && { echo "egress in default build"; exit 1; } || true
```

The assertion turns "your data never leaves your laptop" from a README sentence
into a property the build *proves*. It is allowed to fail the pipeline.

**From Phase 0B it narrows rather than disappears** — the pricing module adds an
opt-out, inbound-only price refresh ([05-llm-pricing.md](05-llm-pricing.md) §4),
isolated in `internal/pricing/refresh`, the only importer of `net/*`. Two rules
replace the blanket one: (1) `net` is reachable *only* through that package, and
(2) the `//go:build offline` artifact still imports no `net` at all
(`go list -deps -tags offline ./cmd/aispend` is net-free). What never changes: **no
code anywhere uploads user data**, the cloud sink stays behind `//go:build cloudyali`,
and the refresh only ever GETs a public price file.

The no-egress assertion targets the **default `FileStore` build**. The optional
`-tags sqlite` backend pulls `net` transitively through `modernc.org/libc` (socket
shims, not networking), so it is intentionally exempt from the net-free grep —
another reason FileStore is the default and SQLite an explicit opt-in.

## 5. Commit & change-set discipline

- **Design and code travel together.** A design change lands in the relevant
  `design-documents/` file in the *same* change-set as the code that realizes it.
- Commits are small and green; messages name the behavior, not the file.
- A phase flips to **Done** in [00-index.md](00-index.md) only when its
  acceptance checklist is fully ticked, both review gates are clean, and its
  "Demonstratable output" has been updated to the *actual* captured output.

## 6. Toolchain notes

- Go `1.25` (the toolchain vendored into the repo); `GOTOOLCHAIN=local` in CI so
  no toolchain is ever fetched at build time. The module floor can be lowered
  toward the PRD's `1.22+` once a CI version matrix exists.
- `modernc.org/sqlite` (pure Go, no cgo) is the only non-stdlib runtime
  dependency planned for 0A, and it lives behind the `Store` interface.
