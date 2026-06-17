import pathlib
p = pathlib.Path('../src/dcp_compress.go')
code = p.read_text(encoding='utf-8')
old = "\tdcp.TotalPrunedTokens += totalSaved\n\n\tvar out strings.Builder"
new = "\tdcp.TotalPrunedTokens += totalSaved\n\n\t// Merge overlapping compressed ranges to prevent context bloat\n\tmergeOverlappingRanges(dcp)\n\n\tvar out strings.Builder"
if old not in code:
    print("ERROR: not found")
else:
    code = code.replace(old, new, 1)
    p.write_text(code, encoding='utf-8')
    print("OK")
