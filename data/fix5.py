import pathlib
p = pathlib.Path('../src/agent.go')
code = p.read_text(encoding='utf-8')
idx = code.find('handleDeleteSession')
print(repr(code[idx-80:idx+20]))
