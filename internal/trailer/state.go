package trailer

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PendingFunc reports the priced, deduped usage on `branch` strictly after `since`
// (the branch watermark). The CLI wires this to the stored ledger; tests stub it.
type PendingFunc func(repoDir, branch string, since time.Time) (Usage, error)

// Trailer is the prepare-commit-msg entry point: it routes on the source hint,
// computes pending usage since the branch watermark, stamps the message, and stages
// (not promotes) the new watermark. Staging-not-promoting is the deferred-truncation
// guarantee: a cancelled commit never reaches consume, so its usage carries forward.
func Trailer(repoDir, source, msgFile string, cfg Config, pending PendingFunc, now func() time.Time) error {
	mode, skip := modeForSource(source)
	if skip {
		return nil
	}
	gitDir, branch, ok := resolveRepo(repoDir)
	if !ok {
		return nil // not a repo, or detached HEAD (no branch to attribute to)
	}
	raw, err := os.ReadFile(msgFile)
	if err != nil {
		return err
	}

	if mode == ModeSquash {
		if out := Apply(string(raw), Usage{}, cfg, ModeSquash); out != string(raw) {
			return os.WriteFile(msgFile, []byte(out), 0o644)
		}
		return nil // squash leaves the watermark untouched
	}

	wm := watermark{gitDir: gitDir}
	since, _ := wm.get(branch)
	u, err := pending(repoDir, branch, since)
	if err != nil {
		return err
	}
	if len(FormatTrailers(u, cfg)) == 0 {
		return nil // nothing attributable — no trailer, no watermark move
	}
	if out := Apply(string(raw), u, cfg, ModeNormal); out != string(raw) {
		if err := os.WriteFile(msgFile, []byte(out), 0o644); err != nil {
			return err
		}
	}
	adv := u.MaxTS
	if adv.IsZero() {
		adv = now()
	}
	return wm.stage(branch, adv)
}

// Preview returns the usage the next commit on the current branch would be stamped
// with — the same resolution + watermark path as Trailer, minus the write. ok is
// false when repoDir isn't a repo, HEAD is detached, or there's nothing
// attributable to show, so a caller (e.g. `today`) prints a line only when there is
// genuinely something pending.
func Preview(repoDir string, pending PendingFunc) (Usage, string, bool) {
	gitDir, branch, ok := resolveRepo(repoDir)
	if !ok {
		return Usage{}, "", false
	}
	since, _ := watermark{gitDir: gitDir}.get(branch)
	u, err := pending(repoDir, branch, since)
	if err != nil || u.Requests == 0 || u.Cost.Micros == 0 {
		return Usage{}, branch, false
	}
	return u, branch, true
}

// ReadCommitCost reads the cost trailer (costName, e.g. "AI-Cost") from the commit
// message of sha in repoDir, returning its value in micros. ok is false when the
// commit isn't in this repo, carries no such trailer, or the value doesn't parse —
// so a caller (the TUI badge) shows it only when there's a real, local, parseable
// trailer. Local git read, no network.
func ReadCommitCost(repoDir, sha, costName string) (int64, bool) {
	if sha == "" || costName == "" {
		return 0, false
	}
	out, err := runGit(repoDir, "show", "-s", "--format=%B", sha)
	if err != nil {
		return 0, false
	}
	return parseCostTrailer(out, costName)
}

func parseCostTrailer(msg, costName string) (int64, bool) {
	prefix := costName + ": "
	for _, ln := range strings.Split(msg, "\n") {
		if strings.HasPrefix(ln, prefix) {
			if v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(ln, prefix)), 64); err == nil {
				return int64(v*1_000_000 + 0.5), true
			}
		}
	}
	return 0, false
}

// Consume is the post-commit entry point: it promotes the staged watermark so the
// next commit only counts newer activity — unless a rebase is in progress, in which
// case it is a no-op so usage destined for the next real commit survives the replay.
func Consume(repoDir string) error {
	gitDir, ok := gitDirOf(repoDir)
	if !ok {
		return nil
	}
	if rebaseInProgress(gitDir) {
		return nil
	}
	return watermark{gitDir: gitDir}.promote()
}

// --- per-branch watermark store (lives in .git, never committed) ---

type watermark struct{ gitDir string }

func (w watermark) committedPath() string { return filepath.Join(w.gitDir, "aispend-trailer") }
func (w watermark) pendingPath() string   { return filepath.Join(w.gitDir, "aispend-trailer.pending") }

// get returns the committed high-water mark for branch, if any.
func (w watermark) get(branch string) (time.Time, bool) {
	ts, ok := readMarks(w.committedPath())[branch]
	return ts, ok
}

// stage records the candidate new mark for branch in the pending file (one entry).
func (w watermark) stage(branch string, ts time.Time) error {
	line := branch + "\t" + strconv.FormatInt(ts.UnixNano(), 10) + "\n"
	return os.WriteFile(w.pendingPath(), []byte(line), 0o644)
}

// promote folds the staged pending mark into the committed set and clears pending.
// No pending file → no-op (a commit with no attributable usage stages nothing).
func (w watermark) promote() error {
	data, err := os.ReadFile(w.pendingPath())
	if err != nil {
		return nil
	}
	branch, ts, ok := parseMarkLine(strings.TrimSpace(string(data)))
	if !ok {
		return os.Remove(w.pendingPath())
	}
	marks := readMarks(w.committedPath())
	marks[branch] = ts
	if err := writeMarks(w.committedPath(), marks); err != nil {
		return err
	}
	return os.Remove(w.pendingPath())
}

func readMarks(path string) map[string]time.Time {
	marks := map[string]time.Time{}
	data, err := os.ReadFile(path)
	if err != nil {
		return marks
	}
	for _, ln := range strings.Split(string(data), "\n") {
		if b, ts, ok := parseMarkLine(ln); ok {
			marks[b] = ts
		}
	}
	return marks
}

// parseMarkLine parses "<branch>\t<unixnano>". Branch names may contain '/', so we
// split on the LAST tab; the timestamp can't contain one.
func parseMarkLine(ln string) (string, time.Time, bool) {
	ln = strings.TrimRight(ln, "\r")
	i := strings.LastIndexByte(ln, '\t')
	if i <= 0 {
		return "", time.Time{}, false
	}
	nanos, err := strconv.ParseInt(ln[i+1:], 10, 64)
	if err != nil {
		return "", time.Time{}, false
	}
	return ln[:i], time.Unix(0, nanos).UTC(), true
}

func writeMarks(path string, marks map[string]time.Time) error {
	branches := make([]string, 0, len(marks))
	for b := range marks {
		branches = append(branches, b)
	}
	sort.Strings(branches)
	var b strings.Builder
	for _, br := range branches {
		b.WriteString(br)
		b.WriteByte('\t')
		b.WriteString(strconv.FormatInt(marks[br].UnixNano(), 10))
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// rebaseInProgress reports whether git is mid-rebase (replaying commits), so consume
// can stand down and not advance the watermark during the replay.
func rebaseInProgress(gitDir string) bool {
	for _, d := range []string{"rebase-merge", "rebase-apply"} {
		if fi, err := os.Stat(filepath.Join(gitDir, d)); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

// --- git resolution (local-only, no network) ---

// resolveRepo returns the git dir and current branch. ok is false when repoDir
// isn't a repo or HEAD is detached (a detached checkout matches no branch, so
// usage — which carries a real branch — attributes to nothing).
func resolveRepo(repoDir string) (gitDir, branch string, ok bool) {
	gd, ok := gitDirOf(repoDir)
	if !ok {
		return "", "", false
	}
	br, err := runGit(repoDir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || br == "" {
		return gd, "", false
	}
	return gd, br, true
}

func gitDirOf(repoDir string) (string, bool) {
	gd, err := runGit(repoDir, "rev-parse", "--absolute-git-dir")
	if err != nil || gd == "" {
		return "", false
	}
	return gd, true
}

// runGit runs `git -C dir <args...>` and returns trimmed stdout. Local reads only
// (rev-parse / symbolic-ref) — same git-binary seam as internal/vcs.
func runGit(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}
