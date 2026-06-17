import pathlib
p = pathlib.Path('../src/dcp_compress.go')
code = p.read_text(encoding='utf-8')
old = "\tmergeOverlappingRanges(dcp)\n\n\tvar out strings.Builder"
new = "\tmergeOverlappingRanges(dcp)\n\n\t// Cap: if too many ranges, merge into a single meta-summary\n\tif activeRanges(dcp) > 5 {\n\t\tmetaSummarize(dcp)\n\t}\n\n\tvar out strings.Builder"
if old not in code:
    print("ERROR: pattern not found")
else:
    code = code.replace(old, new, 1)
    p.write_text(code, encoding='utf-8')
    print("OK")
