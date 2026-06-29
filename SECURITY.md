# Security Policy

`aispend` is built around a trust promise — that it reads only local files and, in its
default build, has **no network-capable code path at all**. Security reports that test
or strengthen that promise are exactly the kind we want.

## Reporting a vulnerability

Please report security or privacy issues **privately** — don't open a public issue,
PR, or discussion for them.

- **Preferred:** GitHub's private vulnerability reporting — use
  [**Report a vulnerability**](https://github.com/cloudyali/ai-agent-spend/security/advisories/new)
  on the repo's **Security** tab.
- **Email:** `nishant.thorat@cloudyali.io` — put `aispend security` in the subject.

Please include enough to reproduce: the version (`aispend version`), your OS, the
command you ran, and what you observed. If a proof-of-concept matters, attach it.

We aim to acknowledge a report within a few business days, agree on a fix and a
disclosure timeline with you, and credit you when the fix ships (unless you'd rather
stay anonymous). This is a preview-stage project maintained on a best-effort basis, so
please bear with us on exact timing.

## What counts as a security issue here

Because of what the tool is, these carry extra weight:

- **Any outbound network behavior in the default build.** The default build is
  asserted to have no network-capable sink (`aispend doctor --network`). A way to make
  it send data anywhere is a serious finding.
- **The `offline` build reaching the network.** The `offline` build compiles `net/*`
  out entirely; any path that defeats that is in scope.
- **Leaking source paths, prompt text, or code.** `aispend` hashes source paths
  (`CWDHash`, `SourcePathHash`) and stores no prompt or code text. A way to recover any
  of that from the ledger is in scope.
- **Writing outside `~/.aispend`** (or the configured app home), or path-traversal via
  a crafted session log.
- **Mis-verification in the installer.** `install.sh` verifies a SHA-256 against
  `checksums.txt` before installing; a way to bypass that check is in scope.

## What's intentional (not a vulnerability)

- The single inbound `GET` performed **only** by `aispend pricing refresh`, of a
  public price file. `doctor --network` discloses it; the default build makes no other
  call, and the `offline` build can't make this one. This is by design.
- `install.sh` making network calls to `github.com` to download a release — that's its
  job, and it verifies the checksum before installing.

## Supported versions

`aispend` is pre-1.0 and in preview. Security fixes are applied to the latest release
and `main`; older preview tags are not maintained. Once there's a stable line, this
table will say which versions get fixes.

| Version | Supported |
|---|---|
| latest release / `main` | ✅ |
| older preview tags | ❌ |

## Verifying what you ran

Every release ships a `checksums.txt`; `install.sh` checks it automatically, and you
can verify a manual download yourself (`shasum -a 256 -c checksums.txt --ignore-missing`).
The default build's trust promise is checkable on your own copy at any time:

```sh
aispend doctor --network
```
