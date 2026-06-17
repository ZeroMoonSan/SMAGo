import pathlib

# Fix 1: Merge overlapping compressed ranges in dcp_compress.go
p = pathlib.Path('../src/dcp_compress.go')
code = p.read_text(encoding='utf-8')

old = '''	dcp.TotalPrunedTokens += totalSaved

	// Build user-friendly progress output'''
new = '''	dcp.TotalPrunedTokens += totalSaved

	// Merge overlapping compressed ranges to prevent context bloat
	mergeOverlappingRanges(dcp)

	// Build user-friendly progress output'''
if old not in code:
    print("ERROR: dcp_compress.go pattern not found")
else:
    code = code.replace(old, new, 1)
    p.write_text(code, encoding='utf-8')
    print("OK: added mergeOverlappingRanges call")

# Fix 2: Add merge function and fix applyCompression in dcp_strategies.go
p2 = pathlib.Path('../src/dcp_strategies.go')
code2 = p2.read_text(encoding='utf-8')

# Add merge function before applyCompression
merge_func = '''// mergeOverlappingRanges merges CompressedRanges that overlap or are adjacent.
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

'''

old2 = '// applyCompression replaces compressed message ranges with synthetic summary messages.'
new2 = merge_func + old2
if old2 not in code2:
    print("ERROR: dcp_strategies.go pattern not found")
else:
    code2 = code2.replace(old2, new2, 1)
    p2.write_text(code2, encoding='utf-8')
    print("OK: added mergeOverlappingRanges function")
