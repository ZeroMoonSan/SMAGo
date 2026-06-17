import pathlib
p = pathlib.Path('../src/dcp_strategies.go')
lines = p.read_text(encoding='utf-8').split('\n')
# Remove blank line before closing brace in metaSummarize
cleaned = []
skip_next_blank = False
for i, line in enumerate(lines):
    if line.strip() == '' and i + 1 < len(lines) and lines[i+1].strip() == '}':
        continue
    cleaned.append(line)
p.write_text('\n'.join(cleaned), encoding='utf-8')
print("OK")
