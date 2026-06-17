import pathlib
p = pathlib.Path('../src/agent.go')
lines = p.read_text(encoding='utf-8').split('\n')

# Find the two blocks: line 189 (0-indexed 188) = b.WriteString("**" + name)
# and line 195 (0-indexed 194) = b.WriteString("\n... " + a)
# We want: annotation first, then name

# Simple approach: replace the whole function header block
old_block = '\tb.WriteString("**" + name + "**")\n\tif annotation != "" {\n\t\ta := strings.TrimSpace(annotation)\n\t\tif len(a) > 200 {\n\t\t\ta = a[:200] + "\xe2\x80\xa6"\n\t\t}\n\t\tb.WriteString("\n\xF0\x9F\x93\x9D " + a)\n\t}'
new_block = '\tif annotation != "" {\n\t\ta := strings.TrimSpace(annotation)\n\t\tif len(a) > 200 {\n\t\t\ta = a[:200] + "\xe2\x80\xa6"\n\t\t}\n\t\tb.WriteString(a + "\n")\n\t}\n\tb.WriteString("**" + name + "**")'

code = p.read_text(encoding='utf-8')
if old_block not in code:
    # Try without emojis
    old2 = '\tb.WriteString("**" + name + "**")\n\tif annotation != "" {\n\t\ta := strings.TrimSpace(annotation)\n\t\tif len(a) > 200 {\n\t\t\ta = a[:200] + "\xe2\x80\xa6"\n\t\t}\n\t\tb.WriteString("\n" + a)\n\t}'
    new2 = '\tif annotation != "" {\n\t\ta := strings.TrimSpace(annotation)\n\t\tif len(a) > 200 {\n\t\t\ta = a[:200] + "\xe2\x80\xa6"\n\t\t}\n\t\tb.WriteString(a + "\n")\n\t}\n\tb.WriteString("**" + name + "**")'
    if old2 in code:
        code = code.replace(old2, new2, 1)
        p.write_text(code, encoding='utf-8')
        print("OK (no emoji variant)")
    else:
        print("ERROR: neither variant found")
        # Debug
        for i in range(187, 200):
            print(f"  [{i}] {repr(lines[i])}")
else:
    code = code.replace(old_block, new_block, 1)
    p.write_text(code, encoding='utf-8')
    print("OK")
