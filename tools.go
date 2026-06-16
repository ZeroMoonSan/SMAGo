package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type ToolRegistry struct {
	cfg   *Config
	tools map[string]ToolDef
}

type ToolDef struct {
	Name        string
	Description string
	Parameters  map[string]any
	Execute     func(args map[string]any) (string, error)
}

func NewToolRegistry(cfg *Config) *ToolRegistry {
	r := &ToolRegistry{
		cfg:   cfg,
		tools: make(map[string]ToolDef),
	}
	r.registerDefaults()
	return r
}

func (r *ToolRegistry) registerDefaults() {
	// Vision tool (mimo-v2.5 via OpenCode Go API).
	if v := r.cfg.Providers["opencode-go"]; v.BaseURL != "" || os.Getenv("SMAGO_OPENCODE_KEY") != "" {
		apiKey := v.APIKey
		if apiKey == "" {
			apiKey = os.Getenv("SMAGO_OPENCODE_KEY")
		}
		base := v.BaseURL
		if base == "" {
			base = "https://opencode.ai/zen/go/v1"
		}
		model := "mimo-v2.5"
		if m, ok := v.Models["mimo-v2.5"]; ok && m.Name != "" {
			// keep default; we don't have a flag, future
			_ = m
		}
		vt := &VisionTool{
			APIKey:    apiKey,
			BaseURL:   base,
			Model:     model,
			MagickExe: r.cfg.MagickExe,
		}
		r.tools["vision"] = vt.Definition()
	}

	r.tools["bash"] = ToolDef{
		Name:        "bash",
		Description: "Run a shell command and return its output. Working directory: " + r.cfg.DataDir,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "Shell command to execute"},
			},
			"required": []string{"command"},
		},
		Execute: r.execBash,
	}
	r.tools["read_file"] = ToolDef{
		Name:        "read_file",
		Description: "Read a file from disk. Path is relative to the working directory.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
			"required": []string{"path"},
		},
		Execute: r.readFile,
	}
	r.tools["write_file"] = ToolDef{
		Name:        "write_file",
		Description: "Write a file to disk. Path is relative to the working directory.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"},
			},
			"required": []string{"path", "content"},
		},
		Execute: r.writeFile,
	}
	r.tools["list_dir"] = ToolDef{
		Name:        "list_dir",
		Description: "List files in a directory. Path is relative to the working directory.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Directory path, default '.'"},
			},
		},
		Execute: r.listDir,
	}
}

func (r *ToolRegistry) All() []ToolDef {
	out := make([]ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

func (r *ToolRegistry) Get(name string) (ToolDef, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func (r *ToolRegistry) AsLLMTools() []Tool {
	var out []Tool
	for _, t := range r.All() {
		var ti Tool
		ti.Type = "function"
		ti.Function.Name = t.Name
		ti.Function.Description = t.Description
		ti.Function.Parameters = t.Parameters
		out = append(out, ti)
	}
	return out
}

func (r *ToolRegistry) execBash(args map[string]any) (string, error) {
	cmd, ok := args["command"].(string)
	if !ok {
		return "", fmt.Errorf("command must be a string")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.CommandContext(ctx, "cmd", "/c", cmd)
	} else {
		c = exec.CommandContext(ctx, "bash", "-c", cmd)
	}
	c.Dir = r.cfg.DataDir
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("ERROR: %v\n%s", err, string(out)), nil
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *ToolRegistry) readFile(args map[string]any) (string, error) {
	p, _ := args["path"].(string)
	if p == "" {
		return "", fmt.Errorf("path required")
	}
	full := r.resolvePath(p)
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	if len(data) > 50_000 {
		return string(data[:50_000]) + "\n...[truncated]...", nil
	}
	return string(data), nil
}

func (r *ToolRegistry) writeFile(args map[string]any) (string, error) {
	p, _ := args["path"].(string)
	if p == "" {
		return "", fmt.Errorf("path required")
	}
	content, _ := args["content"].(string)
	full := r.resolvePath(p)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("ok: wrote %d bytes to %s", len(content), p), nil
}

func (r *ToolRegistry) listDir(args map[string]any) (string, error) {
	p, _ := args["path"].(string)
	if p == "" {
		p = "."
	}
	full := r.resolvePath(p)
	entries, err := os.ReadDir(full)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			b.WriteString("d  ")
		} else {
			b.WriteString("f  ")
		}
		b.WriteString(e.Name())
		b.WriteString("\n")
	}
	return b.String(), nil
}

func (r *ToolRegistry) resolvePath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(r.cfg.DataDir, p)
}

func dumpArgs(args map[string]any) string {
	b, _ := json.Marshal(args)
	return string(b)
}
