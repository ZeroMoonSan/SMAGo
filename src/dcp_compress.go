package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

var compressSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"topic": map[string]any{
			"type":        "string",
			"description": "Brief topic label for the compressed range",
		},
		"ranges": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"start_idx": map[string]any{
						"type":        "integer",
						"description": "Start message index (0-based) in the original session",
					},
					"end_idx": map[string]any{
						"type":        "integer",
						"description": "End message index (0-based, inclusive) in the original session",
					},
					"summary": map[string]any{
						"type":        "string",
						"description": "Detailed summary of this conversation range",
					},
				},
				"required": []string{"start_idx", "end_idx", "summary"},
			},
			"description": "Ranges to compress",
		},
	},
	"required": []string{"topic", "ranges"},
}

type compressArgs struct {
	Topic  string `json:"topic"`
	Ranges []struct {
		StartIdx int    `json:"start_idx"`
		EndIdx   int    `json:"end_idx"`
		Summary  string `json:"summary"`
	} `json:"ranges"`
}

func execCompress(_ context.Context, args map[string]any, dcp *DCPState, sessLen int) (string, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("marshal args: %w", err)
	}
	var ca compressArgs
	if err := json.Unmarshal(raw, &ca); err != nil {
		return "", fmt.Errorf("bad args: %w", err)
	}

	if ca.Topic == "" {
		return "", fmt.Errorf("topic required")
	}
	if len(ca.Ranges) == 0 {
		return "", fmt.Errorf("at least one range required")
	}

	var summaries []string
	totalSaved := 0

	for _, r := range ca.Ranges {
		if r.StartIdx < 0 || r.EndIdx < r.StartIdx {
			return "", fmt.Errorf("invalid range [%d, %d]", r.StartIdx, r.EndIdx)
		}
		if r.EndIdx >= sessLen {
			return "", fmt.Errorf("end_idx %d >= session length %d", r.EndIdx, sessLen)
		}
		if r.Summary == "" {
			return "", fmt.Errorf("summary required for range [%d, %d]", r.StartIdx, r.EndIdx)
		}

		msgCount := r.EndIdx - r.StartIdx + 1
		originalTokens := msgCount * 80
		summaryTokens := EstimateTokens(r.Summary)
		saved := originalTokens - summaryTokens
		if saved < 0 {
			saved = 0
		}
		totalSaved += saved

		dcp.CompressedRanges = append(dcp.CompressedRanges, CompressedRange{
			ID:            dcp.NextRangeID,
			StartIdx:      r.StartIdx,
			EndIdx:        r.EndIdx,
			Summary:       r.Summary,
			SummaryTokens: summaryTokens,
			Topic:         ca.Topic,
			Active:        true,
		})
		dcp.NextRangeID++
		summaries = append(summaries, fmt.Sprintf("[%d..%d] → %s", r.StartIdx, r.EndIdx, truncateStr(r.Summary, 60)))
	}

	dcp.TotalPrunedTokens += totalSaved

	// Merge overlapping compressed ranges to prevent context bloat
	mergeOverlappingRanges(dcp)

	// Cap: if too many ranges, merge into a single meta-summary
	if activeRanges(dcp) > 5 {
		metaSummarize(dcp)
	}

	var out strings.Builder
	out.WriteString("\U0001f4e6 *Context compressed*\n")
	out.WriteString(fmt.Sprintf("\u251c Ranges: %d\n", len(ca.Ranges)))
	out.WriteString(fmt.Sprintf("\u251c Saved: ~%d tokens\n", totalSaved))
	if len(dcp.CompressedRanges) > 0 {
		out.WriteString(fmt.Sprintf("\u2514 Total compressed: %d ranges\n\n", len(dcp.CompressedRanges)))
	}
	for i, s := range summaries {
		icon := "\u251c"
		if i == len(summaries)-1 {
			icon = "\u2514"
		}
		out.WriteString(fmt.Sprintf("%s %s\n", icon, s))
	}
	return out.String(), nil
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
