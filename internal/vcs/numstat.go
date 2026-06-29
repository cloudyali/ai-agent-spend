package vcs

import (
	"os/exec"
	"strconv"
	"strings"

	"github.com/cloudyali/ai-agent-spend/internal/event"
)

// Numstat returns per-file line churn between commits fromSHA..toSHA in repoRoot,
// restricted to the given repo-relative files, by shelling out to `git diff
// --numstat`. This is the one git-binary dependency in aispend — the SHA path
// (HeadAt) stays pure-Go — and it is a local read, so the offline build and
// `doctor --network` are unaffected (no network). Best-effort: it returns nil when
// git is missing, the repo or commits are gone, the range is empty, or nothing
// parses, so a churn number is shown only when git can prove it. Binary files
// (numstat "-") are skipped.
func Numstat(repoRoot, fromSHA, toSHA string, files []string) []event.FileChurn {
	if repoRoot == "" || fromSHA == "" || toSHA == "" || fromSHA == toSHA {
		return nil
	}
	args := []string{"-C", repoRoot, "diff", "--numstat", fromSHA, toSHA}
	if len(files) > 0 {
		args = append(args, "--")
		args = append(args, files...)
	}
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil
	}
	return parseNumstat(string(out))
}

// parseNumstat parses `git diff --numstat` output ("<added>\t<removed>\t<path>"
// per line). Binary files report "-" for the counts and are skipped; malformed or
// blank lines are ignored. Pure and side-effect-free, so it is unit-tested without
// invoking git.
func parseNumstat(s string) []event.FileChurn {
	var churn []event.FileChurn
	for _, line := range strings.Split(s, "\n") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		added, err1 := strconv.Atoi(strings.TrimSpace(fields[0]))
		removed, err2 := strconv.Atoi(strings.TrimSpace(fields[1]))
		if err1 != nil || err2 != nil { // binary files report "-"; skip
			continue
		}
		path := strings.TrimSpace(fields[2])
		if path == "" {
			continue
		}
		churn = append(churn, event.FileChurn{Path: path, Added: added, Removed: removed})
	}
	return churn
}
