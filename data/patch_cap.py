import pathlib

# Cap max compressed ranges
p = pathlib.Path('../src/dcp_compress.go')
code = p.read_text(encoding='utf-8')

old = "\tmergeOverlappingRanges(dcp)\n\n\t// Build user-friendly progress output"
new = "\tmergeOverlappingRanges(dcp)\n\n\t// Cap: if too many ranges, merge into a single meta-summary\n\tif activeRanges(dcp) > 5 {\n\t\tmetaSummarize(dcp)\n\t}\n\n\t// Build user-friendly progress output"
if old not in code:
    print("ERROR: dcp_compress.go pattern not found")
else:
    code = code.replace(old, new, 1)
    p.write_text(code, encoding='utf-8')
    print("OK: added cap + meta-summarize call")

# Add helper functions to dcp_strategies.go
p2 = pathlib.Path('../src/dcp_strategies.go')
code2 = p2.read_text(encoding='utf-8')

helpers = '''// activeRanges returns count of active compressed ranges.
func activeRanges(dcp *DCPState) int {
\tn := 0
\tfor _, r := range dcp.CompressedRanges {
\t\tif r.Active {
\t\t\tn++
\t\t}
\t}
\treturn n
}

// metaSummarize merges ALL active compressed ranges into a single summary.
func metaSummarize(dcp *DCPState) {
\tpos := 0
\tvar parts []string
\tminIdx := -1
\tmaxIdx := -1
\tfor _, r := range dcp.CompressedRanges {
\t\tif !r.Active {
\t\t\tcontinue
\t\t}
\t\tparts = append(parts, r.Summary)
\t\tif minIdx < 0 || r.StartIdx < minIdx {
\t\t\tminIdx = r.StartIdx
\t\t}
\t\tif r.EndIdx > maxIdx {
\t\t\tmaxIdx = r.EndIdx
\t\t}
\t\tpos++
\t}
\tif len(parts) == 0 {
\t\treturn
\t}
\tcombined := strings.Join(parts, "\\n\\n")
\tdcp.CompressedRanges = []CompressedRange{{
\t\t\tID:            dcp.NextRangeID,
\t\t\tStartIdx:      minIdx,
\t\t\tEndIdx:        maxIdx,
\t\t\tSummary:       combined,
\t\t\tSummaryTokens: EstimateTokens(combined),
\t\t\tTopic:         "meta-summary",
\t\t\tActive:        true,
\t\t}}
\tdcp.NextRangeID++
}

'''

marker = '// applyCompression replaces compressed message ranges'
if marker not in code2:
    print("ERROR: dcp_strategies.go marker not found")
else:
    code2 = code2.replace(marker, helpers + marker, 1)
    p2.write_text(code2, encoding='utf-8')
    print("OK: added activeRanges + metaSummarize")
