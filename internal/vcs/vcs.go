// Package vcs reconstructs git provenance for a turn from the repository on disk,
// best-effort and dependency-free. The session logs carry a branch but never a
// commit SHA, so HeadAt recovers the SHA that was HEAD at a turn's timestamp by
// reading the repo's reflog (.git/logs/HEAD) — pure file I/O, no git binary, no
// network (so the `offline` build and `doctor --network` promise hold). It returns
// ("", false) whenever the answer can't be known (no repo, no reflog, a timestamp
// predating the log, or reflog expiry) rather than guessing — a wrong SHA is worse
// than an absent one.
//
// Churn (line-level diff) is the one signal that needs the git binary; it lives
// behind the Differ seam (see numstat.go) so the SHA path stays pure.
package vcs

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// HeadAt returns the commit SHA that was HEAD of repoRoot at instant t, and true,
// reading the reflog at .git/logs/HEAD. It returns ("", false) best-effort when:
// repoRoot is empty, no git dir/reflog exists, every reflog entry post-dates t (we
// can't know HEAD before the log begins), or nothing parses. The SHA is returned
// in full; display layers shorten it.
func HeadAt(repoRoot string, t time.Time) (string, bool) {
	if repoRoot == "" {
		return "", false
	}
	gitDir, ok := resolveGitDir(repoRoot)
	if !ok {
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(gitDir, "logs", "HEAD"))
	if err != nil {
		return "", false
	}

	best, found := "", false
	var bestTS time.Time
	for _, raw := range strings.Split(string(data), "\n") {
		newSHA, ts, ok := parseReflogLine(raw)
		if !ok || ts.After(t) {
			continue
		}
		if !found || ts.After(bestTS) || ts.Equal(bestTS) {
			best, bestTS, found = newSHA, ts, true
		}
	}
	return best, found
}

// resolveGitDir returns the git directory for repoRoot. Normally that is
// repoRoot/.git (a directory); for a linked worktree or submodule it is a FILE
// containing "gitdir: <path>", which we follow (the path may be relative to
// repoRoot). Returns ("", false) when neither exists.
func resolveGitDir(repoRoot string) (string, bool) {
	p := filepath.Join(repoRoot, ".git")
	fi, err := os.Stat(p)
	if err != nil {
		return "", false
	}
	if fi.IsDir() {
		return p, true
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:"); ok {
			dir := strings.TrimSpace(rest)
			if dir == "" {
				return "", false
			}
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(repoRoot, dir)
			}
			return dir, true
		}
	}
	return "", false
}

// parseReflogLine extracts the new SHA and timestamp from one reflog entry, whose
// shape is "<old> <new> <name> <email> <unixts> <tz>\t<message>". The committer
// identity can contain spaces, so we anchor on position: the new SHA is the second
// field, and the unix timestamp is the second-to-last field before the tab. A line
// that doesn't parse (blank, truncated, non-numeric timestamp) yields ok=false and
// is skipped, never fatal.
func parseReflogLine(line string) (sha string, ts time.Time, ok bool) {
	if i := strings.IndexByte(line, '\t'); i >= 0 {
		line = line[:i]
	}
	fields := strings.Fields(line)
	if len(fields) < 4 { // old, new, …, unixts, tz
		return "", time.Time{}, false
	}
	secs, err := strconv.ParseInt(fields[len(fields)-2], 10, 64)
	if err != nil {
		return "", time.Time{}, false
	}
	if !isHexSHA(fields[1]) { // reject garbage / a crafted "sha" that's really a git flag
		return "", time.Time{}, false
	}
	return fields[1], time.Unix(secs, 0), true
}

// isHexSHA reports whether s looks like a git object id: all hex, 7–64 chars (sha1
// is 40, sha256 64; a short id is at least 7). The check keeps a malformed or
// crafted reflog entry from ever being passed to `git diff` as an option.
func isHexSHA(s string) bool {
	if len(s) < 7 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}
