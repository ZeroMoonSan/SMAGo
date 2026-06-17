package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type ShellType int

const (
	ShellPowerPowerShell ShellType = iota
	ShellPowerAdmin
	ShellWSL
	ShellGitBash
	ShellCMD
)

func (s ShellType) String() string {
	switch s {
	case ShellPowerPowerShell:
		return "powershell"
	case ShellPowerAdmin:
		return "admin"
	case ShellWSL:
		return "wsl"
	case ShellGitBash:
		return "gitbash"
	case ShellCMD:
		return "cmd"
	default:
		return "unknown"
	}
}

func ParseShellType(s string) (ShellType, bool) {
	switch s {
	case "powershell":
		return ShellPowerPowerShell, true
	case "admin", "powershell-admin":
		return ShellPowerAdmin, true
	case "wsl", "wsl-bash":
		return ShellWSL, true
	case "gitbash", "git-bash", "git_bash":
		return ShellGitBash, true
	case "cmd", "cmd.exe":
		return ShellCMD, true
	default:
		return ShellPowerPowerShell, false
	}
}

var ProbedShells []ShellType

func BuildShellCommand(shell ShellType, command string) (name string, args []string) {
	switch shell {
	case ShellPowerPowerShell:
		return "powershell", []string{"-NoProfile", "-Command", command}
	case ShellPowerAdmin:
		// Start-Process -Verb RunAs triggers UAC if not already elevated.
		// We wrap the user command in a powershell call.
		escaped := strings.ReplaceAll(command, "'", "''")
		return "powershell", []string{"-NoProfile", "-Command", fmt.Sprintf("Start-Process powershell -Verb RunAs -ArgumentList '-NoProfile -Command \"%s\"' -Wait", escaped)}
	case ShellWSL:
		return "wsl", []string{"bash", "-c", command}
	case ShellGitBash:
		return gitBashExe(), []string{"-c", command}
	case ShellCMD:
		return "cmd", []string{"/c", command}
	default:
		return "powershell", []string{"-NoProfile", "-Command", command}
	}
}

func gitBashExe() string {
	if found, p := hasGitBash(); found {
		return p
	}
	return "bash"
}

func ProbeShells() []ShellType {
	var available []ShellType

	available = append(available, ShellPowerPowerShell)
	available = append(available, ShellPowerAdmin)

	if runtime.GOOS == "windows" {
		available = append(available, ShellCMD)
	}

	if runtime.GOOS == "windows" {
		if err := exec.Command("wsl", "--status").Run(); err == nil {
			available = append(available, ShellWSL)
		}
	}

	if found, _ := hasGitBash(); found {
		available = append(available, ShellGitBash)
	}

	return available
}

func hasGitBash() (bool, string) {
	candidates := []string{
		filepath.Join("C:\\Program Files\\Git\\bin\\bash.exe"),
		filepath.Join("C:\\Program Files\\Git\\usr\\bin\\bash.exe"),
		filepath.Join("C:\\Program Files (x86)\\Git\\bin\\bash.exe"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return true, p
		}
	}
	return false, ""
}

type shellCtxKey struct{}

func WithShell(ctx context.Context, shell ShellType) context.Context {
	return context.WithValue(ctx, shellCtxKey{}, shell)
}

func ShellFromContext(ctx context.Context) ShellType {
	s, _ := ctx.Value(shellCtxKey{}).(ShellType)
	return s
}

func ShellNames(shells []ShellType) []string {
	names := make([]string, len(shells))
	for i, s := range shells {
		names[i] = s.String()
	}
	return names
}

func FormatShellList(shells []ShellType, current ShellType) string {
	var b strings.Builder
	for _, s := range shells {
		marker := ""
		if s == current {
			marker = " ✅"
		}
		b.WriteString("• ")
		b.WriteString(s.String())
		b.WriteString(marker)
		b.WriteString("\n")
	}
	return b.String()
}
