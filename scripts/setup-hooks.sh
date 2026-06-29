#!/usr/bin/env bash
# One-time: point git at the version-controlled hooks in .githooks/.
set -eu
cd "$(git rev-parse --show-toplevel)"
git config core.hooksPath .githooks
chmod +x .githooks/* scripts/*.sh 2>/dev/null || true
echo "hooks installed: core.hooksPath -> .githooks"
echo "  pre-commit : gofmt on staged Go files"
echo "  pre-push   : full gate + /checkin-review (code+security+YAGNI)"
