import pathlib
p = pathlib.Path('../src/agent.go')
code = p.read_text(encoding='utf-8')
old = "return\n\nfunc (a *Agent) handleDeleteSession"
new = "return\n\t}\n\nfunc (a *Agent) handleDeleteSession"
if old in code:
    code = code.replace(old, new, 1)
    p.write_text(code, encoding='utf-8')
    print("OK")
else:
    print("ERROR: not found")
