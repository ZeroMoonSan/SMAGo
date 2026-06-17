import pathlib
p = pathlib.Path('../src/agent.go')
code = p.read_text(encoding='utf-8')
# Find "Renamed: oldName -> newName" send line
idx = code.find('renamed: %s')
if idx < 0:
    print('not found renamed')
else:
    # Find the closing of the RenameSession if block after it
    after = code[idx:]
    # Find "\t}\n\n" after the send line
    end_idx = after.find('\t}\n\nfunc (a *Agent) handleDeleteSession')
    if end_idx < 0:
        print('pattern not found in after')
    else:
        # Insert the function-closing } before the next func
        old = '\t}\n\nfunc (a *Agent) handleDeleteSession'
        new = '\t}\n}\n\nfunc (a *Agent) handleDeleteSession'
        code = code.replace(old, new, 1)
        p.write_text(code, encoding='utf-8')
        print('OK: added function-closing brace')
