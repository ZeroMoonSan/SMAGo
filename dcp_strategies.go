package main

import (
	"encoding/json"
	"strings"
)

// Protected tools that must NEVER be dedup-removed.
var protectedTools = map[string]bool{
	"terminal":    true,
	"write_file":  true,
	"edit_file":   true,
	"self_modify": true,
	"compress":    true,
}

// ── Deduplication ──────────────────────────────────────

// RecordToolCall records a tool call for dedup tracking.
func (d *DCPState) RecordToolCall(name string, args map[string]any, msgIdx int) {
	if protectedTools[name] {
		return
	}
	sig := ToolCallSignature(name, args)
	if _, exists := d.SeenToolCalls[sig]; !exists {
		d.SeenToolCalls[sig] = msgIdx
	}
}

// IsDuplicate returns true if this tool call was seen before at a lower index.
func (d *DCPState) IsDuplicate(name string, args map[string]any, msgIdx int) bool {
	if protectedTools[name] {
		return false
	}
	sig := ToolCallSignature(name, args)
	firstIdx, ok := d.SeenToolCalls[sig]
	if !ok {
		return false
	}
	return firstIdx < msgIdx
}

// deduplicateToolCalls replaces duplicate tool-call results with a placeholder.
func deduplicateToolCalls(msgs []ChatMessage, dcp *DCPState) []ChatMessage {
	if len(dcp.SeenToolCalls) == 0 {
		return msgs
	}
	result := make([]ChatMessage, len(msgs))
	copy(result, msgs)

	for i := range result {
		msg := &result[i]
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		// Check if any tool call in this assistant message is a duplicate
		allDup := true
		for _, tc := range msg.ToolCalls {
			if !dcp.IsDuplicate(tc.Function.Name, parseArgs(tc.Function.Arguments), i) {
				allDup = false
				break
			}
		}
		if allDup && len(msg.ToolCalls) > 0 {
			msg.Content = "[Duplicate removed]"
		}
	}

	// Also replace the corresponding tool result messages
	for i := range result {
		msg := &result[i]
		if msg.Role != "tool" {
			continue
		}
		// Find the corresponding assistant message with the tool call
		for j := i - 1; j >= 0; j-- {
			if result[j].Role == "assistant" && result[j].Content == "[Duplicate removed]" {
				msg.Content = "[Duplicate removed]"
				break
			}
			if result[j].Role == "assistant" {
				break
			}
		}
	}

	return result
}

// ── Purge Errors ───────────────────────────────────────

// RecordErrorToolCall records a failed tool call for later purging.
func (d *DCPState) RecordErrorToolCall(toolCallID, name, args string, msgIdx int) {
	d.ErrorToolCalls = append(d.ErrorToolCalls, ErrorToolEntry{
		ToolCallID: toolCallID,
		Name:       name,
		Args:       args,
		MessageIdx: msgIdx,
		Turn:       d.CurrentTurn,
	})
}

// purgeErrorInputs replaces old error tool inputs with a placeholder.
func purgeErrorInputs(msgs []ChatMessage, dcp *DCPState, purgeThreshold int) []ChatMessage {
	if len(dcp.ErrorToolCalls) == 0 {
		return msgs
	}
	result := make([]ChatMessage, len(msgs))
	copy(result, msgs)

	for _, entry := range dcp.ErrorToolCalls {
		age := dcp.CurrentTurn - entry.Turn
		if age < purgeThreshold {
			continue
		}
		// Replace the tool result content (the error output)
		if entry.MessageIdx >= 0 && entry.MessageIdx < len(result) {
			msg := &result[entry.MessageIdx]
			if msg.Role == "tool" && msg.ToolCallID == entry.ToolCallID {
				if strings.HasPrefix(msg.Content, "error:") {
					msg.Content = "[Error input removed to save context]"
				}
			}
		}
	}

	return result
}

// ── Compression ────────────────────────────────────────

// applyCompression replaces compressed message ranges with synthetic summary messages.
func applyCompression(msgs []ChatMessage, dcp *DCPState) []ChatMessage {
	if len(dcp.CompressedRanges) == 0 {
		return msgs
	}

	// Build a set of indices to remove
	removeSet := make(map[int]bool)
	for _, cr := range dcp.CompressedRanges {
		if !cr.Active {
			continue
		}
		for i := cr.StartIdx; i <= cr.EndIdx && i < len(msgs); i++ {
			removeSet[i] = true
		}
	}

	// Build result: summary messages + unremoved originals
	var result []ChatMessage

	// Track which ranges we've already emitted summaries for
	emitted := make(map[int]bool)

	for i, msg := range msgs {
		// Check if this index starts a compressed range
		for ci := range dcp.CompressedRanges {
			cr := &dcp.CompressedRanges[ci]
			if !cr.Active || emitted[cr.ID] {
				continue
			}
			if cr.StartIdx == i {
				// Emit a synthetic summary message
				result = append(result, ChatMessage{
					Role:    "system",
					Content: "[Compressed range " + cr.Topic + "]\n" + cr.Summary,
				})
				emitted[cr.ID] = true
			}
		}

		if !removeSet[i] {
			result = append(result, msg)
		}
	}

	return result
}

// ── Helper ─────────────────────────────────────────────

func parseArgs(argsStr string) map[string]any {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsStr), &args); err != nil {
		return nil
	}
	return args
}
