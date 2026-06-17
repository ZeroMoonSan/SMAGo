package main

import "encoding/json"

// EstimateTokens gives a rough token count for text (~4 chars per token).
func EstimateTokens(text string) int {
	n := len(text)
	if n == 0 {
		return 0
	}
	return n/4 + 1
}

// CountMessageTokens estimates tokens for a single ChatMessage.
func CountMessageTokens(msg ChatMessage) int {
	tokens := 4 // role overhead
	tokens += EstimateTokens(msg.Content)
	for _, tc := range msg.ToolCalls {
		tokens += EstimateTokens(tc.Function.Name)
		tokens += EstimateTokens(tc.Function.Arguments)
	}
	if msg.ToolCallID != "" {
		tokens += 2
	}
	if msg.Name != "" {
		tokens += EstimateTokens(msg.Name)
	}
	return tokens
}

// CountAllMessagesTokens estimates total tokens for a message slice.
func CountAllMessagesTokens(msgs []ChatMessage) int {
	total := 0
	for _, m := range msgs {
		total += CountMessageTokens(m)
	}
	return total
}

// EstimateTokensFromUsage returns the usage token count, or falls back to estimation.
func EstimateTokensFromUsage(msgs []ChatMessage, usage *Usage) int {
	if usage != nil && usage.PromptTokens > 0 {
		return usage.PromptTokens
	}
	return CountAllMessagesTokens(msgs)
}

// ToolCallSignature creates a dedup signature for a tool call.
func ToolCallSignature(name string, args map[string]any) string {
	// Sort keys for deterministic signature
	sorted := sortedKeys(args)
	argBytes, _ := json.Marshal(struct {
		Name string         `json:"n"`
		Args map[string]any `json:"a"`
	}{Name: name, Args: args})
	// Use the raw bytes — already deterministic enough from sorted keys
	_ = sorted
	return string(argBytes)
}
