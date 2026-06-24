#!/usr/bin/env bash
# coverage-gate_test.sh — pins the behavior of coverage-gate.sh against fixtures.
# Run: bash scripts/coverage-gate_test.sh
set -u
here="$(cd "$(dirname "$0")" && pwd)"
gate="$here/coverage-gate.sh"
fails=0
ok()  { printf '  ok   %s\n' "$1"; }
bad() { printf '  FAIL %s\n' "$1"; fails=$((fails+1)); }

# Fixture A: a tested logic pkg below the floor must BLOCK; scaffolding/generated must only WARN.
fa="$(mktemp)"
{
  printf 'ok  \tgithub.com/x/internal/pricing\t0.123s\tcoverage: 90.0%% of statements\n'
  printf 'ok  \tgithub.com/x/internal/cli\t0.200s\tcoverage: 81.4%% of statements\n'      # FAIL target
  printf '?   \tgithub.com/x/cmd/aispend\t[no test files]\n'                              # warn
  printf '\tgithub.com/x/internal/store/sqlcgen\t\tcoverage: 0.0%% of statements\n'       # warn (bare form)
  printf 'ok  \tgithub.com/x/internal/empty\t0.010s\tcoverage: [no statements]\n'         # warn
  printf 'ok  \tgithub.com/x/internal/exact\t0.010s\tcoverage: 85.0%% of statements\n'    # pass (inclusive)
} > "$fa"
outA="$(COVERAGE_FLOOR=85 bash "$gate" --from "$fa" 2>&1)"; codeA=$?
if [ "$codeA" -eq 1 ]; then ok "below-floor logic pkg blocks (exit 1)"; else bad "expected exit 1, got $codeA"; fi
if [ "$(printf '%s\n' "$outA" | grep -c '^  FAIL')" -eq 1 ]; then ok "exactly one FAIL row"; else bad "expected exactly 1 FAIL row"; fi
if printf '%s\n' "$outA" | grep -q '^  FAIL.*internal/cli'; then ok "the FAIL is internal/cli"; else bad "internal/cli not the failure"; fi
if printf '%s\n' "$outA" | grep -q 'warn .*sqlcgen'; then ok "generated sqlcgen (0.0%%) warns, not fails"; else bad "sqlcgen should warn"; fi
if printf '%s\n' "$outA" | grep -q 'warn .*cmd/aispend'; then ok "thin main (no test files) warns"; else bad "cmd/aispend should warn"; fi
if printf '%s\n' "$outA" | grep -q '^  ok .*internal/exact'; then ok "85.0%% exactly passes (floor inclusive)"; else bad "85.0%% should pass"; fi

# Fixture B: everything tested is at/above floor -> PASS (scaffolding present but ignored).
fb="$(mktemp)"
{
  printf 'ok  \tgithub.com/x/internal/pricing\t0.1s\tcoverage: 90.0%% of statements\n'
  printf 'ok  \tgithub.com/x/internal/store\t0.2s\tcoverage: 88.6%% of statements\n'
  printf '?   \tgithub.com/x/cmd/aispend\t[no test files]\n'
} > "$fb"
COVERAGE_FLOOR=85 bash "$gate" --from "$fb" >/dev/null 2>&1; codeB=$?
if [ "$codeB" -eq 0 ]; then ok "all tested pkgs pass -> exit 0"; else bad "expected exit 0, got $codeB"; fi

# Fixture C: raising the floor reclassifies the 88.6% pkg as FAIL.
COVERAGE_FLOOR=89 bash "$gate" --from "$fb" >/dev/null 2>&1; codeC=$?
if [ "$codeC" -eq 1 ]; then ok "COVERAGE_FLOOR=89 blocks the 88.6%% pkg"; else bad "raised floor should block (got $codeC)"; fi

rm -f "$fa" "$fb"
echo
if [ "$fails" -eq 0 ]; then echo "coverage-gate_test: ALL PASS"; else echo "coverage-gate_test: $fails FAILED"; exit 1; fi
