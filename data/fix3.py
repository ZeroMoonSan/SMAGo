import pathlib
p = pathlib.Path('../src/agent.go')
code = p.read_text(encoding='utf-8')
# Find what's between handleRenameSession last line and handleDeleteSession
idx = code.find('func (a *Agent) handleDeleteSession')
if idx < 0:
    print("ERROR: handleDeleteSession not found")
else:
    chunk = code[idx-200:idx]
    print(repr(chunk[-100:]))
