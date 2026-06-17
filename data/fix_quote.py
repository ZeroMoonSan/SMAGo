import pathlib
p = pathlib.Path('../src/agent.go')
code = p.read_text(encoding='utf-8')
old = 'newName = strings.Trim(newName, '
# Find the exact line
lines = code.split('\n')
for i, line in enumerate(lines):
    if 'strings.Trim(newName' in line:
        lines[i] = '\t\tnewName = strings.Trim(newName, "\\x22\\x27")'
        break
p.write_text('\n'.join(lines), encoding='utf-8')
print('OK')
