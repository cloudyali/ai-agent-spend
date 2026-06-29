#!/usr/bin/env bash
# coverage-gate.sh — enforce a per-package statement-coverage floor.
#
# Policy (CONTRIBUTING.md): every package with logic
# carries >= 85% statement coverage. This gate is BLOCK, NO per-package exemptions
# for logic packages. Scaffolding Go itself reports without an `ok` test status — the
# thin mains and generated code (`[no test files]`, bare `coverage: 0.0%`) — is surfaced
# as a warning ("exempt until it gains logic"), never silently passed and never failed.
#
# Usage:
#   scripts/coverage-gate.sh            # runs `go test ./... -cover` and gates it
#   scripts/coverage-gate.sh --from F   # gates already-captured `go test -cover` output in F
#   COVERAGE_FLOOR=90 scripts/coverage-gate.sh   # raise the floor
set -euo pipefail

FLOOR="${COVERAGE_FLOOR:-85}"
SRC=""
case "${1:-}" in
  --from) SRC="${2:?--from needs a file}" ;;
  -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
  "") ;;
  *) echo "coverage-gate: unknown arg: $1" >&2; exit 2 ;;
esac

tmp=""
if [ -z "$SRC" ]; then
  tmp="$(mktemp)"; trap 'rm -f "$tmp"' EXIT
  go test ./... -coverprofile=coverage.out -covermode=atomic | tee "$tmp"
  SRC="$tmp"
fi

# A "tested" package is one Go prefixes with `ok` — only those carry a real coverage %
# and are held to the floor. `?` (no test files) and the bare `<pkg> coverage: 0.0%`
# form (untested scaffolding / generated code) are warned, not failed.
awk -v floor="$FLOOR" -v ci="${GITHUB_ACTIONS:-}" '
  function pkgfield(   i){ for(i=1;i<=NF;i++) if($i ~ /\//) return $i; return "" }
  $1=="ok" {
    pkg=pkgfield(); pct=""
    for(i=1;i<=NF;i++) if($i ~ /^[0-9]+(\.[0-9]+)?%$/){ pct=$i; sub(/%/,"",pct) }
    if(pct==""){ nostmt[++ns]=pkg; next }          # "ok pkg coverage: [no statements]"
    order[++oc]=pkg; cov[pkg]=pct
    if(pct+0 < floor+0){ nfail++; if(ci=="true") printf("::error::coverage %s%% < %s%% in %s\n",pct,floor,pkg) }
    next
  }
  $1=="?" { notest[++nt]=pkgfield(); next }         # "? pkg [no test files]"
  /coverage:/ && $1 ~ /\// { untested[++nu]=$1; if(ci=="true") printf("::warning::untested (scaffolding/generated?): %s\n",$1); next }
  END {
    printf("\ncoverage floor: %s%% per package (no exemptions for logic packages)\n", floor)
    print  "------------------------------------------------------------"
    for(i=1;i<=oc;i++){ p=order[i]; printf("  %-5s %6.1f%%  %s\n", (cov[p]+0<floor+0?"FAIL":"ok"), cov[p], p) }
    for(i=1;i<=nt;i++) printf("  %-5s %7s  %s\n", "warn", "notest", notest[i])
    for(i=1;i<=nu;i++) printf("  %-5s %7s  %s\n", "warn", "0stmt", untested[i])
    for(i=1;i<=ns;i++) printf("  %-5s %7s  %s\n", "warn", "0stmt", nostmt[i])
    print  "------------------------------------------------------------"
    if(nfail>0){ printf("FAIL: %d logic package(s) below %s%%\n", nfail, floor); exit 1 }
    printf("OK: %d package(s) meet the %s%% floor (%d scaffolding/untested warned)\n", oc, floor, nt+nu+ns)
  }
' "$SRC"
