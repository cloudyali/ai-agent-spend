# Contributing to aispend

Thanks for considering a contribution. `aispend` is a tool people point at their own
spend data, so the bar for trust is high — and the conventions below are what keep the
numbers trustworthy. They're not bureaucracy; they're the product.

Bug reports, doc fixes, new provider parsers, pricing corrections, and feature ideas
are all welcome. If you're planning something large, open an issue first so we can
agree on the shape before you write code.

## The non-negotiables

These three are the heart of how the project stays honest. A PR that skips them won't
be merged, however good the idea:

- **t-wada-style TDD.** Write the failing test first and confirm it's **RED**, write
  the minimal code to get to **GREEN**, then refactor. Every behavioral change lands
  with the test that drove it. If a number can change, a test should pin it.
- **85–90% coverage minimum, per package.** Not as a vanity metric — as evidence the
  pricing and parsing paths are actually exercised. `go test ./internal/... -cover`
  is the check.
- **Reviews before "done."** Run a code review and a security review over your changes
  before you mark a PR ready. The point of the tool is that any number opens to its
  evidence; the same scrutiny applies to the code that produces it.

## Getting set up

Pure Go, vendored, no codegen, no network needed to build or test.

```sh
git clone https://github.com/cloudyali/ai-agent-spend
cd ai-agent-spend
go build ./cmd/aispend
go test ./...
```

You'll want a recent Go toolchain (see the `go` directive in [`go.mod`](go.mod) for the
minimum). Dependencies are vendored, so builds run offline — set `GOFLAGS=-mod=vendor`
if your environment doesn't pick that up automatically.

There are two build SKUs:

```sh
go build ./cmd/aispend                 # default build
go build -tags offline ./cmd/aispend   # the provably-offline SKU (net/* + TUI compiled out)
```

If you touch anything network-adjacent, build **both** — the `offline` tag must keep
compiling, and `aispend doctor --network` must keep passing.

## The development loop

1. Open an issue (for anything non-trivial) and describe the behavior you want to
   change.
2. Write the failing test. Run it. See it fail for the _right_ reason.
3. Write the least code that makes it pass.
4. Refactor with the test green.
5. Repeat until the change is complete and the package is still at 85–90%+ coverage.

## Before you open a PR

Run the same gate CI runs:

```sh
gofmt -l internal/ cmd/        # must print nothing
go vet ./...                   # must be clean
go test ./...                  # must be green
go test ./internal/... -cover  # 85–90% min per package
```

CI also re-runs the suite under non-UTC timezones (`Asia/Kolkata`,
`America/Los_Angeles`) — `aispend` is UTC end-to-end internally and only renders local
time at the surface, so any time-dependent test must pass in any zone. Worth running
locally if you've touched anything date- or period-related:

```sh
TZ=Asia/Kolkata go test ./...
```

## Commit messages

The release changelog is generated from commit messages, so please use
[Conventional Commits](https://www.conventionalcommits.org/):

- `feat: …` and `fix: …` show up in the release notes (under Features / Fixes).
- `docs: …`, `test: …`, `chore: …` are kept out of the changelog by design.

Example: `feat(pricing): price the Anthropic 1-hour cache tier`.

## Pull requests

- Keep PRs focused — one logical change per PR is much easier to review.
- Describe _what_ changed and _why_, and call out any number that moves.
- Link the issue it closes.
- Make sure the gate above is green and both build SKUs compile.

## Understand the "why" before the "how"

The design record is unusually complete and is the fastest way to understand why a
thing is the way it is. Start here:

- [`CLAUDE.md`](CLAUDE.md) — working notes and the conventions in one page.
- [`design-documents/00-index.md`](design-documents/00-index.md) — the full design
  record. Pricing lives in `05-llm-pricing.md`; the data model in `02-data-model.md`.

## Reporting bugs & proposing features

Use the issue templates — they nudge you toward the details that make a report
actionable (your OS, the command, what you expected vs. saw). For anything that looks
like a security or privacy issue, **don't** open a public issue; see
[`SECURITY.md`](SECURITY.md).

## A note on conduct

Be respectful and assume good faith — we're all here to make a useful, trustworthy
tool. Harassment or hostility isn't welcome.

## License

By contributing, you agree that your contributions are licensed under the project's
[MIT License](LICENSE).
