#!/usr/bin/env bash
#
# prepare-release-commits.sh — stage & commit the remaining OSS-release-prep work
# in logical, Conventional-Commit groups.
#
# Safe by design:
#   * uses EXPLICIT paths (never `git add -A`), so it can't sweep in stray files
#     or commit itself;
#   * commits a group only when it actually has staged changes;
#   * does NOT push — review with `git log` first, then push yourself.
#
# Run it from anywhere (it cd's into its own directory = the repo root):
#   bash prepare-release-commits.sh
#
# Delete this script once you've used it.

cd "$(dirname "$0")" || exit 1

# --- safety: must be inside the ai-agent-spend git repo ---
if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "error: not a git repository ($(pwd))" >&2
  exit 1
fi
case "$(git config --get remote.origin.url)" in
  *ai-agent-spend*) : ;;
  *) echo "error: 'origin' is not ai-agent-spend — refusing to run here." >&2; exit 1 ;;
esac

echo "Working tree BEFORE:"
git status --short
echo

# --- helpers (portable: no arrays, no 'set -e' footguns) ---
add_if_exists() {
  for p in "$@"; do
    [ -e "$p" ] && git add -- "$p"
  done
}
commit_group() {
  # $1 = commit message (paths already staged by the caller)
  if git diff --cached --quiet; then
    echo "  (nothing staged — skipping: $1)"
  else
    git commit -m "$1"
  fi
}

# ===========================================================================
# Release-critical / OSS-prep changes
# ===========================================================================

echo "==> LICENSE (currently untracked — required for release)"
add_if_exists LICENSE
commit_group "docs: add MIT LICENSE"

echo "==> docs/ (screenshot assets referenced by the README)"
add_if_exists docs
commit_group "docs: add screenshot assets referenced by the README"

echo "==> RELEASE_CHECKLIST.md (Homebrew tap, code signing, PAT steps)"
add_if_exists RELEASE_CHECKLIST.md
commit_group "docs: expand release checklist (Homebrew tap, signing, PAT steps)"

# ===========================================================================
# Your in-flight docs — REVIEW THESE. Comment out a block if you're not ready
# to commit it; these are your own pre-existing edits, not the OSS-prep pass.
# ===========================================================================

echo "==> CLAUDE.md + design-documents/12 (scan-on-launch notes)"
add_if_exists CLAUDE.md "design-documents/12-surfaces-ingestion-roadmap.md"
commit_group "docs: note scan-on-launch and ingestion roadmap"

echo "==> .aispend.toml (repo trailer config — optional to track)"
add_if_exists .aispend.toml
commit_group "chore: add repo .aispend.toml trailer config"

# ===========================================================================

echo
echo "Working tree AFTER:"
git status --short
echo
echo "Review:  git log --oneline -8"
echo "Push:    git push origin main"
echo
echo "(You can now delete this script: rm prepare-release-commits.sh)"
