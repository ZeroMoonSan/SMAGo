import pathlib

p = pathlib.Path('../src/dcp_strategies.go')
code = p.read_text(encoding='utf-8')

helpers = '''// activeRanges returns count of active compressed ranges.
func activeRanges(dcp *DCPState) int {
	n := 0
	for _, r := range dcp.CompressedRanges {
		if r.Active {
			n++
		}
	}
	return n
}

// mergeOverlappingRanges merges CompressedRanges that overlap or are adjacent.
// This prevents context bloat from multiple compress passes.
func mergeOverlappingRanges(dcp *DCPState) {
	if len(dcp.CompressedRanges) <= 1 {
		return
	}
	// Sort by StartIdx (simple insertion sort is fine for small N)
	sorted := make([]CompressedRange, len(dcp.CompressedRanges))
	copy(sorted, dcp.CompressedRanges)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j].StartIdx < sorted[j-1].StartIdx; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}

	merged := make([]CompressedRange, 0, len(sorted))
	for _, r := range sorted {
		if !r.Active {
			continue
		}
		if len(merged) == 0 {
			merged = append(merged, r)
			continue
		}
		last := &merged[len(merged)-1]
		// Merge if overlapping or adjacent
		if r.StartIdx <= last.EndIdx+1 {
			if r.EndIdx > last.EndIdx {
				last.EndIdx = r.EndIdx
				last.Summary = last.Summary + "\\n\\n" + r.Summary
				last.SummaryTokens = EstimateTokens(last.Summary)
			}
		} else {
			merged = append(merged, r)
		}
	}
	dcp.CompressedRanges = merged
}

// metaSummarize merges ALL active compressed ranges into a single summary.
func metaSummarize(dcp *DCPState) {
	var parts []string
	minIdx := -1
	maxIdx := -1
	for _, r := range dcp.CompressedRanges {
		if !r.Active {
			continue
		}
		parts = append(parts, r.Summary)
		if minIdx < 0 || r.StartIdx < minIdx {
			minIdx = r.StartIdx
		}
		if r.EndIdx > maxIdx {
			maxIdx = r.EndIdx
		}
	}
	if len(parts) == 0 {
		return
	}
	combined := strings.Join(parts, "\\n\\n")
	dcp.CompressedRanges = []CompressedRange{{
		ID:            dcp.NextRangeID,
		StartIdx:      minIdx,
		EndIdx:        maxIdx,
		Summary:       combined,
		SummaryTokens: EstimateTokens(combined),
		Topic:         "meta-summary",
		Active:        true,
	}}
	dcp.NextRangeID++
}

'''

marker = '// applyCompression replaces compressed message ranges'
if marker not in code:
    print("ERROR: marker not found")
else:
    code = code.replace(marker, helpers + marker, 1)
    p.write_text(code, encoding='utf-8')
    print("OK: inserted 3 helper functions before applyCompression")
