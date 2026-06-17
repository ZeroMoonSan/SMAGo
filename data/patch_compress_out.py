import pathlib
p = pathlib.Path('../src/dcp_compress.go')
lines = p.read_text().splitlines(keepends=True)
# Replace lines 107-108 (0-indexed 106-107)
new_lines = [
    '\tout.WriteString(fmt.Sprintf("\xf0\x9f\x93\xa6 *Context compressed*\n"))\n',
    '\tout.WriteString(fmt.Sprintf("\xe2\x94\x9c Ranges: %d\n", len(ca.Ranges)))\n',
    '\tout.WriteString(fmt.Sprintf("\xe2\x94\x9c Saved: ~%d tokens\n", totalSaved))\n',
    '\tif len(dcp.CompressedRanges) > 0 {\n',
    '\t\tout.WriteString(fmt.Sprintf("\xe2\x94\x94 Total compressed: %d ranges\n\n", len(dcp.CompressedRanges)))\n',
    '\t}\n',
    '\tfor i, s := range summaries {\n',
    '\t\ticon := "\xe2\x94\x9c"\n',
    '\t\tif i == len(summaries)-1 {\n',
    '\t\t\ticon = "\xe2\x94\x94"\n',
    '\t\t}\n',
    '\t\tout.WriteString(fmt.Sprintf("%s %s\n", icon, s))\n',
    '\t}\n',
    '\n',
    '\treturn out.String(), nil\n',
]
result = lines[:106] + new_lines + lines[109:]
p.write_text(''.join(result))
print('OK')
