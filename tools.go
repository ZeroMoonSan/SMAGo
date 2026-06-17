package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type ToolRegistry struct {
	cfg        *Config
	tools      map[string]ToolDef
	mcpClients []*MCPClient
	readFiles  map[string]bool
}

type ToolDef struct {
	Name        string
	Description string
	Parameters  map[string]any
	Execute     func(ctx context.Context, args map[string]any) (string, error)
}

func NewToolRegistry(cfg *Config) *ToolRegistry {
	return &ToolRegistry{
		cfg:       cfg,
		tools:     make(map[string]ToolDef),
		readFiles: make(map[string]bool),
	}
}

func (r *ToolRegistry) registerDefaults() {
	ws := &WebSearchTool{}
	r.tools["web_search"] = ws.Definition()

	r.tools["self_modify"] = (&SelfModifyTool{cfg: r.cfg}).Definition()

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
		vt := &VisionTool{
			APIKey:    apiKey,
			BaseURL:   base,
			Model:     model,
			MagickExe: r.cfg.MagickExe,
		}
		r.tools["vision"] = vt.Definition()
	}

	r.tools["terminal"] = ToolDef{
		Name:        "terminal",
		Description: "Run a shell command and return its output. Working directory: " + r.cfg.DataDir,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "Shell command to execute"},
			},
			"required": []string{"command"},
		},
		Execute: r.execTerminal,
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
		Description: "Write a file to disk. Path is relative to the working directory. Requires read_file to be called first on the same path.",
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
	r.tools["edit_file"] = ToolDef{
		Name:        "edit_file",
		Description: "Edit a file by line-level operations: replace, delete, or insert. Requires read_file first. Lines are 1-based. For insert, start=0 means insert before line 1.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "File path"},
				"action":  map[string]any{"type": "string", "enum": []string{"replace", "delete", "insert"}},
				"start":   map[string]any{"type": "integer", "description": "Line number (1-based). For insert: 0=before line1, N=after line N."},
				"end":     map[string]any{"type": "integer", "description": "End line (1-based, inclusive). For replace/delete only. Defaults to start."},
				"content": map[string]any{"type": "string", "description": "New content for replace/insert."},
			},
			"required": []string{"path", "action", "start"},
		},
		Execute: r.editFile,
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

	// DCP compress tool — actual execution intercepted in agent.go Handle().
	r.tools["compress"] = ToolDef{
		Name:        "compress",
		Description: "Replace closed/old conversation ranges with detailed summaries to manage context length.",
		Parameters:  compressSchema,
		Execute:     nil,
	}


	r.connectMCPServers()
}

func (r *ToolRegistry) connectMCPServers() {
	if len(r.cfg.MCP) == 0 {
		return
	}
	for name, cfg := range r.cfg.MCP {
		if !cfg.Enabled {
			continue
		}
		log.Printf("mcp: connecting to %s ...", name)
		client, err := NewMCPClient(name, cfg)
		if err != nil {
			log.Printf("mcp: ✗ %s failed: %v", name, err)
			continue
		}
		r.mcpClients = append(r.mcpClients, client)

		tools, err := client.ListTools()
		if err != nil {
			log.Printf("mcp: ✗ %s listTools: %v", name, err)
			continue
		}
		log.Printf("mcp: ✓ %s — %d tool(s)", name, len(tools))

		// Cap MCP tools at a sensible number — the LLM can't handle 29
		// separate tool JSON schemas in a single request (context explosion
		// and client timeouts). Take the first maxMcpTools (they arrive in
		// declaration order from the server, so the most core ones come
		// first).
		maxMcpTools := 10
		if maxMcpTools > len(tools) {
			maxMcpTools = len(tools)
		}
		for _, mt := range tools[:maxMcpTools] {
			toolName := name + "__" + mt.Name
			mtCopy := mt
			clientCopy := client
			desc := mtCopy.Description
			if len(desc) > 150 {
				desc = desc[:150] + "…"
			}
			r.tools[toolName] = ToolDef{
				Name:        toolName,
				Description: desc,
				Parameters:  mtCopy.InputSchema,
				Execute: func(ctx context.Context, args map[string]any) (string, error) {
					return clientCopy.CallTool(ctx, mtCopy.Name, args)
				},
			}
		}
	}
}

func (r *ToolRegistry) Close() {
	for _, c := range r.mcpClients {
		_ = c.Close()
	}
	r.mcpClients = nil
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

func (r *ToolRegistry) ResetReadFiles() {
	r.readFiles = make(map[string]bool)
}

func (r *ToolRegistry) MarkRead(path string) {
	r.readFiles[path] = true
}

func (r *ToolRegistry) WasRead(path string) bool {
	return r.readFiles[path]
}

func (r *ToolRegistry) execTerminal(ctx context.Context, args map[string]any) (string, error) {
	cmd, ok := args["command"].(string)
	if !ok {
		return "", fmt.Errorf("command must be a string")
	}
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = hiddenCmdContext(runCtx, "cmd", "/c", cmd)
	} else {
		c = hiddenCmdContext(runCtx, "bash", "-c", cmd)
	}
	c.Dir = r.cfg.DataDir
	out, err := c.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return fmt.Sprintf("ERROR: %v\n%s", err, string(out)), nil
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *ToolRegistry) readFile(ctx context.Context, args map[string]any) (string, error) {
	p, _ := args["path"].(string)
	if p == "" {
		return "", fmt.Errorf("path required")
	}
	full := r.resolvePath(p)
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	r.MarkRead(p)
	if len(data) > 50_000 {
		return string(data[:50_000]) + "\n...[truncated]...", nil
	}
	return string(data), nil
}

func (r *ToolRegistry) writeFile(ctx context.Context, args map[string]any) (string, error) {
	p, _ := args["path"].(string)
	if p == "" {
		return "", fmt.Errorf("path required")
	}
	if !r.WasRead(p) {
		return "", fmt.Errorf("read_file must be called before write_file on %s", p)
	}
	content, _ := args["content"].(string)
	full := r.resolvePath(p)
	dir := filepath.Dir(full)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return "", fmt.Errorf("directory does not exist: %s", dir)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("ok: wrote %d bytes to %s", len(content), p), nil
}

func (r *ToolRegistry) editFile(ctx context.Context, args map[string]any) (string, error) {
	p, _ := args["path"].(string)
	if p == "" {
		return "", fmt.Errorf("path required")
	}
	action, _ := args["action"].(string)
	if action == "" {
		return "", fmt.Errorf("action required (replace, delete, insert)")
	}
	if !r.WasRead(p) {
		return "", fmt.Errorf("read_file must be called before edit_file on %s", p)
	}

	full := r.resolvePath(p)
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(data), "\n")
	nLines := len(lines)
	start := toInt(args["start"])

	switch action {
	case "replace":
		if start < 1 {
			return "", fmt.Errorf("start must be >= 1 for replace")
		}
		end := toInt(args["end"])
		if end == 0 {
			end = start
		}
		if end < start {
			return "", fmt.Errorf("end must be >= start")
		}
		if start > nLines {
			return "", fmt.Errorf("start line %d exceeds file length %d", start, nLines)
		}
		content, _ := args["content"].(string)
		newLines := strings.Split(content, "\n")
		before := lines[:start-1]
		after := lines[end:]
		result := make([]string, 0, len(before)+len(newLines)+len(after))
		result = append(result, before...)
		result = append(result, newLines...)
		result = append(result, after...)
		lines = result

	case "delete":
		if start < 1 {
			return "", fmt.Errorf("start must be >= 1 for delete")
		}
		end := toInt(args["end"])
		if end == 0 {
			end = start
		}
		if end < start {
			return "", fmt.Errorf("end must be >= start")
		}
		if start > nLines {
			return "", fmt.Errorf("start line %d exceeds file length %d", start, nLines)
		}
		if end > nLines {
			end = nLines
		}
		before := lines[:start-1]
		after := lines[end:]
		lines = append(before, after...)

	case "insert":
		if start < 0 {
			return "", fmt.Errorf("start must be >= 0 for insert")
		}
		if start > nLines {
			return "", fmt.Errorf("line %d exceeds file length %d", start, nLines)
		}
		content, _ := args["content"].(string)
		newLines := strings.Split(content, "\n")
		result := make([]string, 0, nLines+len(newLines))
		result = append(result, lines[:start]...)
		result = append(result, newLines...)
		result = append(result, lines[start:]...)
		lines = result

	default:
		return "", fmt.Errorf("unknown action: %s (use replace, delete or insert)", action)
	}

	newContent := strings.Join(lines, "\n")
	if err := os.WriteFile(full, []byte(newContent), 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("ok: %s on %s (%d lines)", action, p, len(lines)), nil
}

func (r *ToolRegistry) listDir(ctx context.Context, args map[string]any) (string, error) {
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

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

func dumpArgs(args map[string]any) string {
	b, _ := json.Marshal(args)
	return string(b)
}
