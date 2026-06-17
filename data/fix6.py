import pathlib
p = pathlib.Path('../src/agent.go')
code = p.read_text(encoding='utf-8')
# Find function definition of handleDeleteSession
idx = code.find('func (a *Agent) handleDeleteSession')
print(repr(code[idx-60:idx+20]))
