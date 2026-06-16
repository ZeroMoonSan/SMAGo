package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// gitCmd runs a git command in the project root and returns combined
// stdout/stderr. Used by the agent for self-versioning.
func gitCmd(args ...string) (string, error) {
	cmd := hiddenCmd("git", args...)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// gitHead returns the current HEAD commit hash (short form).
func gitHead() (string, error) {
	out, err := gitCmd("rev-parse", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// gitCommitAll stages everything and commits with the given message.
// Returns the resulting commit hash.
func gitCommitAll(message string) (string, error) {
	if _, err := gitCmd("add", "-A"); err != nil {
		return "", fmt.Errorf("add: %w", err)
	}
	if _, err := gitCmd("commit", "-m", message); err != nil {
		// "nothing to commit" returns non-zero. Detect that case.
		if strings.Contains(err.Error(), "exit status") {
			// Try to return the existing HEAD instead.
			return gitHead()
		}
		return "", err
	}
	return gitHead()
}

// gitLog returns the last N commits in oneline format.
func gitLog(n int) (string, error) {
	out, err := gitCmd("log", "--oneline", fmt.Sprintf("-%d", n))
	if err != nil {
		return "", err
	}
	return strings.TrimRight(out, "\n"), nil
}

// gitDiff returns the diff of working tree vs HEAD (or vs a specific ref).
func gitDiff(ref string) (string, error) {
	args := []string{"diff"}
	if ref != "" {
		args = append(args, ref)
	}
	args = append(args, "--stat")
	out, err := gitCmd(args...)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(out, "\n"), nil
}

// gitCheckout reverts the working tree to the given ref. Used for rollback.
func gitCheckout(ref string) error {
	_, err := gitCmd("checkout", ref)
	return err
}

// gitPorcelainStatus returns true if there is any uncommitted change in the
// working tree (tracked file edits, deletions, or new untracked files).
// We only count "tracked" changes for the rollback guard — untracked new
// files are preserved through `git checkout` so they're safe.
func gitPorcelainStatus() (string, error) {
	out, err := gitCmd("status", "--porcelain")
	if err != nil {
		return "", err
	}
	return out, nil
}

// gitIsDirty returns the list of changed TRACKED files (lines starting with
// " M", "M ", "MM", "D ", "A " etc. — anything that's not "??" or "!!").
// Untracked files don't block rollback.
func gitTrackedDirty() ([]string, error) {
	out, err := gitPorcelainStatus()
	if err != nil {
		return nil, err
	}
	var dirty []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 3 {
			continue
		}
		// Porcelain v1: two status chars, space, then path.
		xy := line[:2]
		path := strings.TrimSpace(line[3:])
		if xy == "??" || xy == "!!" {
			continue
		}
		dirty = append(dirty, fmt.Sprintf("%s  %s", xy, path))
	}
	return dirty, nil
}

// gitCommitTime returns the committer date of the given ref as Unix seconds.
func gitCommitTime(ref string) (int64, error) {
	out, err := gitCmd("log", "-1", "--format=%ct", ref)
	if err != nil {
		return 0, err
	}
	t, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return 0, err
	}
	return t, nil
}

// VersionInfo describes a previously-built version, for the /rollback list.
type VersionInfo struct {
	Version   string // e.g. "v3"
	ShortSHA  string // first 7 chars of commit SHA
	CommitSHA string // full commit SHA
	BuiltAt   time.Time
	IsCurrent bool
}

// listVersions scans data/versions/ and returns a sorted (newest first) list
// of available versions, joined with their commit SHAs from commit.txt.
func listVersions() ([]VersionInfo, error) {
	root := filepath.Join("data", "versions")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	current := readCurrentVersion()

	var versions []VersionInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		v := e.Name()
		commitPath := filepath.Join(root, v, "commit.txt")
		commitData, err := os.ReadFile(commitPath)
		if err != nil {
			continue
		}
		sha := strings.TrimSpace(string(commitData))
		short := sha
		if len(short) > 7 {
			short = short[:7]
		}
		// Committer time of the build's commit, with directory mtime as a
		// fallback if git is unavailable.
		t := time.Time{}
		if info, err := e.Info(); err == nil {
			t = info.ModTime()
		}
		if ct, err := gitCommitTime(sha); err == nil {
			t = time.Unix(ct, 0)
		}
		versions = append(versions, VersionInfo{
			Version:   v,
			ShortSHA:  short,
			CommitSHA: sha,
			BuiltAt:   t,
			IsCurrent: v == current,
		})
	}
	// Newest first.
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].BuiltAt.After(versions[j].BuiltAt)
	})
	return versions, nil
}

// readCurrentVersion returns the version currently marked in
// data/current.json, or "" if the file is missing/malformed.
func readCurrentVersion() string {
	data, err := os.ReadFile(filepath.Join("data", "current.json"))
	if err != nil {
		return ""
	}
	// {"version": "v3"} — strip the JSON envelope with a tiny parse.
	s := strings.TrimSpace(string(data))
	if i := strings.Index(s, ":"); i >= 0 {
		rest := strings.TrimSpace(s[i+1:])
		rest = strings.Trim(rest, ",}\" ")
		return rest
	}
	return ""
}
