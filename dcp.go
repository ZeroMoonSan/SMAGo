package main

// buildDCPMessages assembles a virtual message array from the original session,
// applying compression, dedup, error purging, and nudge injection.
// The original session messages are NEVER modified.
func (a *Agent) buildDCPMessages(sess *Session, dcp *DCPState, step int) []ChatMessage {
	msgs := sess.Messages()

	// 1. Apply compression: replace compressed ranges with synthetic summaries
	msgs = applyCompression(msgs, dcp)

	// 2. Deduplication: replace duplicate tool calls
	msgs = deduplicateToolCalls(msgs, dcp)

	// 3. Purge old error inputs
	purgeThreshold := 4
	if a.cfg.DCP.PurgeErrorsTurns > 0 {
		purgeThreshold = a.cfg.DCP.PurgeErrorsTurns
	}
	msgs = purgeErrorInputs(msgs, dcp, purgeThreshold)

	// 4. Inject nudge messages if needed
	msgs = injectNudges(msgs, dcp, step, a.cfg.DCP)

	// 5. Prepend system prompt
	result := make([]ChatMessage, 0, len(msgs)+1)
	result = append(result, ChatMessage{Role: "system", Content: a.cfg.SystemPrompt})
	result = append(result, msgs...)

	return result
}
