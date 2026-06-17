import pathlib
p = pathlib.Path('../src/agent.go')
code = p.read_text(encoding='utf-8')
# The issue: handleRenameSession's closing } is missing before handleDeleteSession
# Looking at the pattern: "a.send(chatID, fmt.Sprintf(...))\n\t}\n\nfunc (a *Agent) handleDeleteSession"
old = 'a.send(chatID, fmt.Sprintf("\u2705 renamed: %s \u2192 %s", oldName, newName))\n\t}\n\nfunc (a *Agent) handleDeleteSession'
new = 'a.send(chatID, fmt.Sprintf("\u2705 renamed: %s \u2192 %s", oldName, newName))\n}\n\nfunc (a *Agent) handleDeleteSession'
if old in code:
    code = code.replace(old, new, 1)
    p.write_text(code, encoding='utf-8')
    print("OK: fixed function close")
else:
    print("ERROR: not found")
