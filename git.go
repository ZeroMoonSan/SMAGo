package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// gitCmd runs a git command in the project root and returns combined
// stdout/stderr. Used by the agent for self-versioning.
func gitCmd(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
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
