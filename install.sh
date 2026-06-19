#!/bin/sh
# shellcheck shell=sh
#
# aispend installer — downloads the right prebuilt binary from GitHub Releases,
# verifies its SHA-256 against the signed checksums.txt, and drops it on your PATH.
#
#   curl -fsSL https://raw.githubusercontent.com/agentspend/ai-agent-spend/main/install.sh | sh
#
# It makes exactly one kind of network call: GETs to github.com / api.github.com to
# fetch the release. No telemetry, nothing else. Prefer to trust nothing? Read this
# script, or skip it entirely and grab the binary + checksums.txt yourself from the
# Releases page — every step below is one you can do by hand.
#
# Env overrides:
#   AISPEND_VERSION   pin a version (e.g. v0.2.0); default: latest release
#   AISPEND_BIN_DIR   install target; default: first writable of /usr/local/bin, ~/.local/bin
#   AISPEND_REPO      owner/repo; default: agentspend/ai-agent-spend

set -u

# --- pure helpers (unit-tested by install_test.sh; keep them side-effect free) ---

# detect_os maps `uname -s` to the GoReleaser .Os token used in archive names.
# shellcheck disable=SC2120  # $1 is an optional test seam; prod calls with no args (uname -s)
detect_os() {
	os="${1:-$(uname -s)}"
	case "$os" in
	Linux) echo linux ;;
	Darwin) echo darwin ;;
	*)
		echo "aispend: unsupported OS: $os (prebuilt binaries: linux, darwin)" >&2
		return 1
		;;
	esac
}

# detect_arch maps `uname -m` to the GoReleaser .Arch token.
# shellcheck disable=SC2120  # $1 is an optional test seam; prod calls with no args (uname -m)
detect_arch() {
	arch="${1:-$(uname -m)}"
	case "$arch" in
	x86_64 | amd64) echo amd64 ;;
	arm64 | aarch64) echo arm64 ;;
	*)
		echo "aispend: unsupported architecture: $arch (prebuilt binaries: amd64, arm64)" >&2
		return 1
		;;
	esac
}

# archive_name must match the name_template in .goreleaser.yaml exactly.
archive_name() {
	# $1=version (no leading v)  $2=os  $3=arch
	printf 'aispend_%s_%s_%s.tar.gz' "$1" "$2" "$3"
}

# sha256_of prints the SHA-256 of a file using whichever tool is present.
sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	else
		echo "aispend: need sha256sum or shasum to verify the download" >&2
		return 1
	fi
}

# verify_checksum confirms $1's hash matches its entry in checksums.txt ($2).
# Non-zero on a mismatch, a tampered file, or a missing entry — the caller must
# refuse to install if this fails.
verify_checksum() {
	file="$1"
	sums="$2"
	base="$(basename "$file")"
	want="$(awk -v f="$base" '$NF == f {print $1; exit}' "$sums")"
	if [ -z "$want" ]; then
		echo "aispend: no checksum entry for $base" >&2
		return 1
	fi
	got="$(sha256_of "$file")" || return 1
	if [ "$want" != "$got" ]; then
		echo "aispend: CHECKSUM MISMATCH for $base — refusing to install" >&2
		echo "  expected: $want" >&2
		echo "  got:      $got" >&2
		return 1
	fi
}

# --- network + filesystem (not exercised by the unit tests) ---

fetch_stdout() {
	url="$1"
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --proto '=https' --tlsv1.2 "$url"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO- "$url"
	else
		echo "aispend: need curl or wget" >&2
		return 1
	fi
}

fetch_file() {
	url="$1"
	dest="$2"
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --proto '=https' --tlsv1.2 -o "$dest" "$url"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO "$dest" "$url"
	else
		echo "aispend: need curl or wget" >&2
		return 1
	fi
}

latest_version() {
	repo="$1"
	fetch_stdout "https://api.github.com/repos/${repo}/releases/latest" |
		awk -F'"' '/"tag_name"/ {print $4; exit}'
}

install_dir() {
	if [ -n "${AISPEND_BIN_DIR:-}" ]; then
		echo "$AISPEND_BIN_DIR"
		return
	fi
	for d in /usr/local/bin "$HOME/.local/bin"; do
		if [ -d "$d" ] && [ -w "$d" ]; then
			echo "$d"
			return
		fi
	done
	echo "$HOME/.local/bin"
}

main() {
	set -e
	repo="${AISPEND_REPO:-agentspend/ai-agent-spend}"
	bin="aispend"

	os="$(detect_os)"
	arch="$(detect_arch)"

	case "${AISPEND_VERSION:-latest}" in
	"" | latest)
		tag="$(latest_version "$repo")"
		[ -n "$tag" ] || {
			echo "aispend: could not resolve the latest release for $repo" >&2
			exit 1
		}
		;;
	v*) tag="${AISPEND_VERSION}" ;;
	*) tag="v${AISPEND_VERSION}" ;;
	esac
	version="${tag#v}"

	archive="$(archive_name "$version" "$os" "$arch")"
	base_url="https://github.com/${repo}/releases/download/${tag}"

	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' EXIT

	echo "aispend: downloading ${archive} (${tag})…" >&2
	fetch_file "${base_url}/${archive}" "${tmp}/${archive}"
	fetch_file "${base_url}/checksums.txt" "${tmp}/checksums.txt"

	verify_checksum "${tmp}/${archive}" "${tmp}/checksums.txt"
	echo "aispend: checksum verified." >&2

	tar -xzf "${tmp}/${archive}" -C "$tmp"

	dir="$(install_dir)"
	mkdir -p "$dir"
	if ! install -m 0755 "${tmp}/${bin}" "${dir}/${bin}" 2>/dev/null; then
		cp "${tmp}/${bin}" "${dir}/${bin}"
		chmod 0755 "${dir}/${bin}"
	fi

	echo "aispend: installed ${tag} to ${dir}/${bin}" >&2
	case ":${PATH}:" in
	*":${dir}:"*) ;;
	*) echo "aispend: ${dir} is not on your PATH — add it to use \`aispend\` directly." >&2 ;;
	esac
}

# Sourced as a library by the test harness (AISPEND_INSTALL_LIB=1) → don't run main.
if [ "${AISPEND_INSTALL_LIB:-0}" != "1" ]; then
	main "$@"
fi
