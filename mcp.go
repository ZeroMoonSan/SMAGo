package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"time"
)

// MCPClient speaks the Model Context Protocol over stdio (JSON-RPC 2.0
// newline-delimited).
type MCPClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr io.ReadCloser

	name    string
	version string

	// The read-loop runs in a dedicated goroutine. Each send() puts a
	// channel into pending; the read-loop dispatches responses by ID.
	mu      sync.Mutex
	nextID  int
	pending map[int]chan json.RawMessage
}

// MCPServerConfig is one entry from the agent config's "mcp" map.
type MCPServerConfig struct {
	Command []string          `json:"command"`
	Enabled bool              `json:"enabled"`
	Env     map[string]string `json:"env"`
}

// MCPTool is what we hand back to the ToolRegistry.
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

// NewMCPClient spawns the server process and runs the initialize handshake.
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
		cmd:     cmd,
		stdin:   stdin,
		stderr:  stderr,
		name:    name,
		pending: make(map[int]chan json.RawMessage),
	}

	// Start the persistent read-loop.
	go c.readLoop(bufio.NewReader(stdout))
	go c.drainStderr()

	// Initialize handshake.
	res, err := c.send(context.Background(), "initialize", map[string]any{
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

	c.notify("notifications/initialized", map[string]any{})
	return c, nil
}

// readLoop reads lines from stdout and dispatches responses by ID.
// It runs for the lifetime of the process.
func (c *MCPClient) readLoop(reader *bufio.Reader) {
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		var msg struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			log.Printf("mcp[%s] non-JSON: %s", c.name, line)
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[msg.ID]
		c.mu.Unlock()
		if ok {
			// If the error is non-nil, wrap it into the result so the
			// caller can decode it.
			if msg.Error != nil {
				errJSON, _ := json.Marshal(msg.Error)
				ch <- errJSON
			} else {
				ch <- msg.Result
			}
		}
		// If nobody is waiting (stale ID), just drop it.
	}
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
		"method":  method,
		"params":  params,
	})
	data = append(data, '\n')
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = c.stdin.Write(data)
}

func (c *MCPClient) send(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan json.RawMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	data, _ := json.Marshal(req)
	data = append(data, '\n')

	c.mu.Lock()
	_, err := c.stdin.Write(data)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case raw := <-ch:
		// Check if this is an error response (from readLoop's error wrapping).
		// MCP error objects have "code" and "message" fields.
		var errCheck struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(raw, &errCheck); err == nil && errCheck.Code != 0 {
			return nil, fmt.Errorf("mcp error %d: %s", errCheck.Code, errCheck.Message)
		}
		return raw, nil
	case <-time.After(120 * time.Second):
		return nil, fmt.Errorf("mcp %s: timeout waiting for response to %s", c.name, method)
	}
}

func (c *MCPClient) ListTools() ([]MCPTool, error) {
	res, err := c.send(context.Background(), "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var lt mcpListToolsResult
	if err := json.Unmarshal(res, &lt); err != nil {
		return nil, err
	}
	return lt.Tools, nil
}

func (c *MCPClient) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	res, err := c.send(ctx, "tools/call", map[string]any{
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
	for i, ct := range cr.Content {
		if ct.Type == "text" {
			if i > 0 {
				out += "\n"
			}
			out += ct.Text
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
