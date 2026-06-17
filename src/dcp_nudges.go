package main

import "fmt"

// injectNudges adds system messages prompting the LLM to use compress.
func injectNudges(msgs []ChatMessage, dcp *DCPState, step int, cfg DCPConfig) []ChatMessage {
	if !cfg.Enabled || cfg.ManualMode {
		return msgs
	}

	nudge := buildNudge(dcp, step, cfg)
	if nudge == "" {
		return msgs
	}

	result := make([]ChatMessage, len(msgs)+1)
	copy(result, msgs)
	// Insert nudge just before the last user message (or at the end)
	insertAt := len(msgs)
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			insertAt = i
			break
		}
	}
	result[insertAt] = ChatMessage{Role: "system", Content: nudge}
	copy(result[insertAt+1:], msgs[insertAt:])

	return result
}

// buildNudge decides whether a nudge is needed and returns its text.
func buildNudge(dcp *DCPState, step int, cfg DCPConfig) string {
	// Hard limit nudge: context is too big
	if cfg.MaxContextTokens > 0 && dcp.CurrentTokens > cfg.MaxContextTokens {
		dcp.LastNudgeStep = step
		return "[SYSTEM] Context approaching limit. Use 'compress' tool to summarize old conversation ranges."
	}

	// Soft nudge: periodic reminder
	if cfg.MinContextTokens > 0 && dcp.CurrentTokens > cfg.MinContextTokens {
		freq := cfg.NudgeFrequency
		if freq <= 0 {
			freq = 5
		}
		if step-dcp.LastNudgeStep >= freq {
			dcp.LastNudgeStep = step
			return fmt.Sprintf(
				"[SYSTEM] Context at ~%d tokens. Consider compressing completed sections to free context.",
				dcp.CurrentTokens,
			)
		}
	}

	return ""
}
