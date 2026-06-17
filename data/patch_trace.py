import pathlib
p = pathlib.Path('../src/agent.go')
code = p.read_text(encoding='utf-8')

# Show DCP result in toolLines instead of compress args
old = 'toolLines = append(toolLines, "**compress**"+formatCompressArgs(args)+"\\n→ "+fmt.Sprintf("%d", len(result))+" chars")'
new = 'toolLines = append(toolLines, result)'
if old in code:
    code = code.replace(old, new, 1)
    print("OK: toolLines now shows DCP result")
else:
    print("ERROR: not found")
p.write_text(code, encoding='utf-8')
