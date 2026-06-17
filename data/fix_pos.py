import pathlib
p = pathlib.Path('../src/dcp_strategies.go')
code = p.read_text(encoding='utf-8')
old = "\tpos := 0\n\tvar parts []string"
new = "\tvar parts []string"
if old in code:
    code = code.replace(old, new, 1)
    old2 = "\t\tpos++"
    new2 = ""
    if old2 in code:
        code = code.replace(old2, new2, 1)
    p.write_text(code, encoding='utf-8')
    print("OK: removed unused pos")
else:
    print("not found")
