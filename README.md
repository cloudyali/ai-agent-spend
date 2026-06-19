# aispend

Local, explainable AI-coding spend. `aispend` scans your Claude Code / Codex
session logs, prices each turn against a pinned rate table, and keeps an evidence
ledger you can open — every number drills down to where it came from. It's a single
static Go binary: no Node, no Python, no runtime. By default it never touches the
network, and it can prove it (`aispend doctor --network`).

## Install

Pick whichever fits. All paths land you the same binary; all of them let you verify
what you ran.

### Homebrew (macOS)

```sh
brew install agentspend/tap/aispend
```

### Install script (macOS / Linux)

Downloads the right prebuilt binary from GitHub Releases and verifies its SHA-256
against the published `checksums.txt` before installing:

```sh
curl -fsSL https://raw.githubusercontent.com/agentspend/ai-agent-spend/main/install.sh | sh
```

Pipe-to-shell not your thing? Read the script first, or skip it and follow
[From a prebuilt binary](#from-a-prebuilt-binary) by hand. Knobs:

```sh
# pin a version, or choose where it lands
AISPEND_VERSION=v0.2.0 AISPEND_BIN_DIR="$HOME/.local/bin" \
  sh -c "$(curl -fsSL https://raw.githubusercontent.com/agentspend/ai-agent-spend/main/install.sh)"
```

### With Go

```sh
go install github.com/agentspend/ai-agent-spend/cmd/aispend@latest
```

### From a prebuilt binary

Every release ships binaries for macOS and Linux (amd64 + arm64) and Windows, plus a
`checksums.txt`. Grab them from the [Releases page](https://github.com/agentspend/ai-agent-spend/releases),
verify, and drop on your PATH:

```sh
# example: macOS arm64
tar -xzf aispend_0.2.0_darwin_arm64.tar.gz
shasum -a 256 -c checksums.txt --ignore-missing   # or: sha256sum -c
sudo mv aispend /usr/local/bin/
```

### From source

Pure Go, vendored, no codegen — `git clone` and build:

```sh
git clone https://github.com/agentspend/ai-agent-spend
cd ai-agent-spend
go build ./cmd/aispend
```

Want the binary that *physically can't* reach the network? Build the offline SKU —
it compiles out all `net/*` imports via a build tag (the same artifact is published
on each release as `aispend-offline_*`):

```sh
go build -tags offline ./cmd/aispend
```

## Verify it's offline

```sh
aispend doctor --network   # discloses every outbound call the build can make
aispend version
```

## License

See [LICENSE](LICENSE).
