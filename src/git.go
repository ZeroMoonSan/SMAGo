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

func gitCmd(args ...string) (string, error) {
	cmd := hiddenCmd("git", args...)
	cmd.Dir = projectRoot()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func gitHead() (string, error) {
	out, err := gitCmd("rev-parse", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func gitCommitAll(message string) (string, error) {
	if _, err := gitCmd("add", "-A"); err != nil {
		return "", fmt.Errorf("add: %w", err)
	}
	if _, err := gitCmd("commit", "-m", message); err != nil {
		if strings.Contains(err.Error(), "exit status") {
			return gitHead()
		}
		return "", err
	}
	return gitHead()
}

func gitLog(n int) (string, error) {
	out, err := gitCmd("log", "--oneline", fmt.Sprintf("-%d", n))
	if err != nil {
		return "", err
	}
	return strings.TrimRight(out, "\n"), nil
}

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

func gitCheckout(ref string) error {
	_, err := gitCmd("checkout", ref)
	return err
}

func gitPorcelainStatus() (string, error) {
	out, err := gitCmd("status", "--porcelain")
	if err != nil {
		return "", err
	}
	return out, nil
}

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
		xy := line[:2]
		path := strings.TrimSpace(line[3:])
		if xy == "??" || xy == "!!" {
			continue
		}
		dirty = append(dirty, fmt.Sprintf("%s  %s", xy, path))
	}
	return dirty, nil
}

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

type VersionInfo struct {
	Version   string // git SHA (short)
	ShortSHA  string // first 7 chars
	CommitSHA string // full commit SHA from commit.txt
	BuiltAt   time.Time
	IsCurrent bool
}

func listVersions() ([]VersionInfo, error) {
	root := filepath.Join(projectRoot(), "data", "versions")
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
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].BuiltAt.After(versions[j].BuiltAt)
	})
	return versions, nil
}

func readCurrentVersion() string {
	data, err := os.ReadFile(filepath.Join(projectRoot(), "data", "current.json"))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(data))
	if i := strings.Index(s, ":"); i >= 0 {
		rest := strings.TrimSpace(s[i+1:])
		rest = strings.Trim(rest, ",}\" ")
		return rest
	}
	return ""
}
