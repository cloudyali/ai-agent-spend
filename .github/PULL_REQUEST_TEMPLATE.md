<!--
Thanks for the PR! Keep it focused on one logical change.
See CONTRIBUTING.md for the conventions below.
-->

## What & why

<!-- What does this change, and why? Call out any number that moves. -->

Closes #

## How it was tested

<!-- t-wada TDD: which failing test drove this, and what does it now assert? -->

## Checklist

- [ ] **TDD**: a failing test was written first (RED), then minimal code to GREEN, then refactor.
- [ ] `gofmt -l internal/ cmd/` prints nothing.
- [ ] `go vet ./...` is clean.
- [ ] `go test ./...` is green (and `TZ=Asia/Kolkata go test ./...` if this touches dates/periods).
- [ ] Coverage stays at **85–90%+** for any package I touched (`go test ./internal/... -cover`).
- [ ] Both builds still compile: `go build ./cmd/aispend` **and** `go build -tags offline ./cmd/aispend`.
- [ ] If anything network-adjacent changed: `aispend doctor --network` still passes.
- [ ] Commit messages follow Conventional Commits (`feat:` / `fix:` / `docs:` …).
- [ ] I ran a code review and a security review over the change.
