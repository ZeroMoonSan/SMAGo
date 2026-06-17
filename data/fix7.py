import pathlib
p = pathlib.Path('../src/agent.go')
code = p.read_text(encoding='utf-8')
# The func def version
old = 'return\n\t}\n\nfunc (a *Agent) handleDeleteSession(chatID int64, text string) {'
new = 'return\n\t}\n}\n\nfunc (a *Agent) handleDeleteSession(chatID int64, text string) {'
if old in code:
    code = code.replace(old, new, 1)
    p.write_text(code, encoding='utf-8')
    print('OK')
else:
    print('ERROR')
