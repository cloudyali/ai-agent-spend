#!/bin/sh
# install_test.sh — t-wada-style guard for install.sh.
#
# Written BEFORE install.sh existed (RED), then install.sh was implemented to make
# it GREEN. Pure POSIX sh, zero network: it sources install.sh as a library
# (AISPEND_INSTALL_LIB=1 suppresses main) and exercises the pure functions —
# platform detection and checksum verification — which is where install bugs hide.
#
# Run:  sh install_test.sh
set -u

AISPEND_INSTALL_LIB=1
export AISPEND_INSTALL_LIB
# shellcheck source=install.sh
. "$(dirname "$0")/install.sh"

fail=0
check() {
	desc="$1"
	shift
	if "$@"; then
		printf 'ok   - %s\n' "$desc"
	else
		printf 'FAIL - %s\n' "$desc"
		fail=1
	fi
}
# expect_fail passes when the wrapped command returns non-zero.
# shellcheck disable=SC2329  # invoked indirectly via check() as "$@"
expect_fail() {
	if "$@" >/dev/null 2>&1; then
		return 1
	else
		return 0
	fi
}

# --- OS detection: uname -s tokens -> GoReleaser .Os tokens ---
check "Linux maps to linux"       test "$(detect_os Linux)" = linux
check "Darwin maps to darwin"     test "$(detect_os Darwin)" = darwin
check "unknown OS is rejected"    expect_fail detect_os Plan9

# --- Arch detection: uname -m tokens -> GoReleaser .Arch tokens ---
check "x86_64 maps to amd64"      test "$(detect_arch x86_64)" = amd64
check "amd64 maps to amd64"       test "$(detect_arch amd64)" = amd64
check "arm64 maps to arm64"       test "$(detect_arch arm64)" = arm64
check "aarch64 maps to arm64"     test "$(detect_arch aarch64)" = arm64
check "unknown arch is rejected"  expect_fail detect_arch sparc64

# --- Archive name matches the GoReleaser name_template ---
check "archive name template" \
	test "$(archive_name 0.2.0 linux amd64)" = "aispend_0.2.0_linux_amd64.tar.gz"

# --- Checksum verification (sha256, no network) ---
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
art="$tmp/aispend_1.0.0_linux_amd64.tar.gz"
printf 'pretend-binary-archive' >"$art"
good="$(sha256_of "$art")"
printf '%s  %s\n' "$good" "aispend_1.0.0_linux_amd64.tar.gz" >"$tmp/checksums.txt"

check "valid checksum passes"        verify_checksum "$art" "$tmp/checksums.txt"
printf 'tampered' >"$art"
check "tampered archive is rejected" expect_fail verify_checksum "$art" "$tmp/checksums.txt"
check "missing entry is rejected"    expect_fail verify_checksum "$tmp/absent.tar.gz" "$tmp/checksums.txt"

if [ "$fail" -eq 0 ]; then
	echo "PASS"
	exit 0
fi
echo "FAILURES PRESENT"
exit 1
