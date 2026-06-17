package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type ChatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type Tool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Parameters  any    `json:"parameters"`
	} `json:"function"`
}

type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Tools    []Tool        `json:"tools,omitempty"`
	Stream   bool          `json:"stream"`
}

type ChatResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
	Usage *Usage `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"error"`
	} `json:"error,omitempty"`
}

// Usage mirrors the OpenAI `usage` block. Optional — providers that don't
// return it will leave PromptTokens / CompletionTokens at 0.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (u *Usage) Add(other *Usage) {
	if u == nil || other == nil {
		return
	}
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
	u.TotalTokens += other.TotalTokens
}

type LLM struct {
	cfg      *Config
	provider *Provider
	client   *http.Client
}

func NewLLM(cfg *Config) (*LLM, error) {
	prov, ok := cfg.Providers[cfg.Provider]
	if !ok {
		return nil, fmt.Errorf("provider %q not found in config", cfg.Provider)
	}
	return &LLM{
		cfg:      cfg,
		provider: &prov,
		client:   defaultHTTP, // share the global client (proxy-aware)
	}, nil
}

func (l *LLM) Chat(messages []ChatMessage, tools []Tool) (*ChatMessage, *Usage, error) {
	req := ChatRequest{
		Model:    l.cfg.DefaultModel,
		Messages: messages,
		Tools:    tools,
		Stream:   false,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, nil, err
	}

	// Try the user-configured path first, then fall back to common variants.
	// Many OpenAI-compatible servers expose /chat/completions directly
	// (no /v1 prefix) even when the user types /v1 in the baseURL.
	paths := candidatePaths(l.provider.BaseURL)
	var lastErr error
	var lastBody string
	var lastStatus int
	for _, p := range paths {
		msg, usage, status, body, err := l.post(p, body)
		lastErr, lastBody, lastStatus = err, body, status
		if err == nil {
			return msg, usage, nil
		}
		if status != 404 {
			return nil, nil, err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no paths tried")
	}
	return nil, nil, fmt.Errorf("status %d: %s (tried: %v, last body: %s)",
		lastStatus, lastErr, paths, truncate(lastBody, 300))
}

func (l *LLM) post(path string, body []byte) (*ChatMessage, *Usage, int, string, error) {
	httpReq, err := http.NewRequest("POST", path, bytes.NewReader(body))
	if err != nil {
		return nil, nil, 0, "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if l.provider.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+l.provider.APIKey)
	}
	for k, v := range l.provider.Headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := l.client.Do(httpReq)
	if err != nil {
		return nil, nil, 0, "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, resp.StatusCode, string(respBody), err
	}
	if resp.StatusCode >= 400 {
		return nil, nil, resp.StatusCode, string(respBody),
			fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, nil, resp.StatusCode, string(respBody),
			fmt.Errorf("decode: %w", err)
	}
	if chatResp.Error != nil {
		return nil, nil, resp.StatusCode, string(respBody),
			fmt.Errorf("API error: %s", chatResp.Error.Message)
	}
	if len(chatResp.Choices) == 0 {
		return nil, nil, resp.StatusCode, string(respBody),
			fmt.Errorf("no choices (body: %s)", truncate(string(respBody), 200))
	}
	return &chatResp.Choices[0].Message, chatResp.Usage, resp.StatusCode, string(respBody), nil
}

// candidatePaths returns a list of URLs to try for chat completions.
// Starts with the baseURL as-is (for explicit override), then strips /v1
// or adds it depending on what's already there.
func candidatePaths(baseURL string) []string {
	base := strings.TrimRight(baseURL, "/")
	seen := map[string]bool{}
	out := []string{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	add(base + "/chat/completions")
	if strings.HasSuffix(base, "/v1") {
		add(strings.TrimSuffix(base, "/v1") + "/chat/completions")
	} else {
		add(base + "/v1/chat/completions")
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
