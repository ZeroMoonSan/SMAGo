package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"time"
)

// MCPClient speaks the Model Context Protocol over stdio (JSON-RPC 2.0
// newline-delimited). It is intentionally simple — synchronous
// request/response, no streaming, no notifications beyond `initialized`.
type MCPClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	stderr io.ReadCloser

	mu     sync.Mutex
	nextID int

	name    string
	version string
}

// MCPServerConfig is one entry from the agent config's "mcp" map.
type MCPServerConfig struct {
	Command []string          `json:"command"`
	Enabled bool              `json:"enabled"`
	Env     map[string]string `json:"env"`
}

// MCPTool is what we hand back to the ToolRegistry. The agent's LLM
// dispatcher doesn't care whether a tool is in-process or remote — the
// ToolDef.Execute closure hides the MCP call.
type MCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type mcpInitializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

type mcpListToolsResult struct {
	Tools []MCPTool `json:"tools"`
}

type mcpCallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// NewMCPClient spawns the server process and runs the initialize
// handshake. The caller must Close() the client when done.
func NewMCPClient(name string, cfg MCPServerConfig) (*MCPClient, error) {
	if len(cfg.Command) == 0 {
		return nil, fmt.Errorf("mcp %q: empty command", name)
	}
	cmd := exec.Command(cfg.Command[0], cfg.Command[1:]...)
	env := cmd.Environ()
	for k, v := range cfg.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn %s: %w", name, err)
	}

	c := &MCPClient{
		cmd:    cmd,
		stdin:  stdin,
		reader: bufio.NewReader(stdout),
		stderr: stderr,
		name:   name,
	}

	// Drain stderr to the agent log.
	go c.drainStderr()

	// Initialize handshake.
	res, err := c.send("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "smago", "version": "0.1.0"},
	})
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("initialize %s: %w", name, err)
	}
	var init mcpInitializeResult
	if err := json.Unmarshal(res, &init); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("parse init: %w", err)
	}
	c.version = init.ServerInfo.Version
	log.Printf("mcp %s: connected (%s)", name, init.ServerInfo.Name)

	// Send `notifications/initialized` (no id, no response).
	c.notify("notifications/initialized", map[string]any{})
	return c, nil
}

func (c *MCPClient) drainStderr() {
	r := bufio.NewReader(c.stderr)
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			log.Printf("mcp[%s] %s", c.name, line)
		}
		if err != nil {
			return
		}
	}
}

func (c *MCPClient) notify(method string, params any) {
	data, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method": method,
		"params": params,
	})
	data = append(data, '\n')
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = c.stdin.Write(data)
}

func (c *MCPClient) send(method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.nextID++
	id := c.nextID
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	if _, err := c.stdin.Write(data); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		c.reader = bufio.NewReader(nil) // no-op; keep field for clarity
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		var resp struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			// Non-JSON line on stdout — should not happen, log it.
			log.Printf("mcp[%s] non-JSON: %s", c.name, line)
			continue
		}
		if resp.ID != id {
			// Stale response or notification — keep reading.
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
	return nil, fmt.Errorf("mcp %s: timeout waiting for response to %s", c.name, method)
}

// ListTools queries the server for the tools it exposes.
func (c *MCPClient) ListTools() ([]MCPTool, error) {
	res, err := c.send("tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var lt mcpListToolsResult
	if err := json.Unmarshal(res, &lt); err != nil {
		return nil, err
	}
	return lt.Tools, nil
}

// CallTool invokes a tool and returns its text content. If the server
// returns multiple content blocks, they are joined with newlines.
func (c *MCPClient) CallTool(name string, args map[string]any) (string, error) {
	res, err := c.send("tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return "", err
	}
	var cr mcpCallResult
	if err := json.Unmarshal(res, &cr); err != nil {
		return "", err
	}
	var out string
	for i, c := range cr.Content {
		if c.Type == "text" {
			if i > 0 {
				out += "\n"
			}
			out += c.Text
		}
	}
	if cr.IsError && out == "" {
		out = "(tool reported error, no message)"
	}
	return out, nil
}

func (c *MCPClient) Close() error {
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	if c.cmd != nil {
		_ = c.cmd.Wait()
	}
	return nil
}
