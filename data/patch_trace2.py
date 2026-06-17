import pathlib
p = pathlib.Path('../src/agent.go')
code = p.read_text(encoding='utf-8')

# Remove separate recordTrace for DCP since it now shows in toolLines
old = 'a.recordTrace(chatID, "\\U0001f4e6 DCP: "+result)'
new = ''
if old in code:
    code = code.replace(old, new, 1)
    p.write_text(code, encoding='utf-8')
    print("OK: removed separate DCP trace message")
else:
    print("ERROR: not found")
