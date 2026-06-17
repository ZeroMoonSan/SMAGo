package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type SelfModifyTool struct {
	cfg *Config
}

func (s *SelfModifyTool) Definition() ToolDef {
	return ToolDef{
		Name:        "self_modify",
		Description: selfModifyDescription,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"list", "current", "upgrade", "rollback", "restart"},
					"description": "What to do",
				},
				"version": map[string]any{
					"type":        "string",
					"description": "For upgrade/rollback: target version as git SHA prefix.",
				},
				"force": map[string]any{
					"type":        "boolean",
					"description": "For rollback: bypass the working-tree-dirty check.",
				},
			},
			"required": []string{"action"},
		},
		Execute: s.Execute,
	}
}

const selfModifyDescription = "Manage this agent's own version. Use sparingly - " +
	"these actions change the running binary and may interrupt the current task. " +
	"Versions are identified by git commit SHA (short form). " +
	"Actions: list (show all built versions), current (show running version + git SHA), " +
	"upgrade (build a new version from current HEAD and ask the supervisor to swap to it), " +
	"rollback (revert the working tree to a previous version's commit and rebuild; version required, pass force=true to " +
	"skip the dirty-tree check), restart (exit cleanly so the supervisor brings a fresh " +
	"process up; same binary, no rebuild)."

func (s *SelfModifyTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	action, _ := args["action"].(string)
	if action == "" {
		return "", fmt.Errorf("action is required (list, current, upgrade, rollback, restart)")
	}
	switch action {
	case "list":
		return s.actionList()
	case "current":
		return s.actionCurrent()
	case "upgrade":
		return s.actionUpgrade(ctx, args)
	case "rollback":
		return s.actionRollback(ctx, args)
	case "restart":
		return s.actionRestart()
	default:
		return "", fmt.Errorf("unknown action %q", action)
	}
}

func (s *SelfModifyTool) actionList() (string, error) {
	versions, err := listVersions()
	if err != nil {
		return "", fmt.Errorf("list: %w", err)
	}
	if len(versions) == 0 {
		return "no versions on disk", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "available versions (%d):\n", len(versions))
	for _, v := range versions {
		marker := ""
		if v.IsCurrent {
			marker = "  <- current"
		}
		fmt.Fprintf(&b, "  %s  %s  %s%s\n", v.Version, v.ShortSHA, v.BuiltAt.Format("2006-01-02 15:04"), marker)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func (s *SelfModifyTool) actionCurrent() (string, error) {
	sha, _ := gitHead()
	exe, _ := os.Executable()
	exe = filepath.Base(exe)
	uptime := time.Since(startedAt).Truncate(time.Second)
	var b strings.Builder
	fmt.Fprintf(&b, "git:  %s\nexe:  %s\npid:  %d\nup:   %s", sha, exe, os.Getpid(), uptime)
	return b.String(), nil
}

func (s *SelfModifyTool) actionUpgrade(ctx context.Context, args map[string]any) (string, error) {
	version, _ := args["version"].(string)
	if version == "" {
		sha, err := gitHead()
		if err != nil {
			return "", fmt.Errorf("git HEAD: %w", err)
		}
		version = sha
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := cmdUpgrade([]string{"--version=" + version}); err != nil {
		return "", fmt.Errorf("upgrade %s: %w", version, err)
	}
	return fmt.Sprintf("upgrade %s sent to supervisor", version), nil
}

func (s *SelfModifyTool) actionRollback(ctx context.Context, args map[string]any) (string, error) {
	version, _ := args["version"].(string)
	if version == "" {
		return "", fmt.Errorf("version required (use action=list)")
	}
	force, _ := args["force"].(bool)
	if !force {
		dirty, err := gitTrackedDirty()
		if err != nil {
			return "", fmt.Errorf("git status: %w", err)
		}
		if len(dirty) > 0 {
			return "", fmt.Errorf("working tree dirty (%d changes); pass force=true", len(dirty))
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	argv := []string{"--version=" + version}
	if force {
		argv = append(argv, "--force")
	}
	if err := cmdRollback(argv); err != nil {
		return "", fmt.Errorf("rollback %s: %w", version, err)
	}
	return fmt.Sprintf("rollback %s sent to supervisor", version), nil
}

func (s *SelfModifyTool) actionRestart() (string, error) {
	go func() {
		time.Sleep(500 * time.Millisecond)
		log.Println("self_modify: clean exit requested")
		os.Exit(0)
	}()
	return "restart scheduled", nil
}
