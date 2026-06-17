import pathlib
p = pathlib.Path('../src/agent.go')
code = p.read_text(encoding='utf-8')

old = '''func formatToolCall(name string, args map[string]any, annotation string, resultLen int, toolErr error) string {
	var b strings.Builder
	b.WriteString("**" + name + "**")
	if annotation != "" {
		a := strings.TrimSpace(annotation)
		if len(a) > 200 {
			a = a[:200] + "\xe2\x80\xa6"
		}
		b.WriteString("\n" + a)
	}'''

new = '''func formatToolCall(name string, args map[string]any, annotation string, resultLen int, toolErr error) string {
	var b strings.Builder
	if annotation != "" {
		a := strings.TrimSpace(annotation)
		if len(a) > 200 {
			a = a[:200] + "\xe2\x80\xa6"
		}
		b.WriteString(a + "\n")
	}
	b.WriteString("**" + name + "**")'''

if old not in code:
    print("ERROR: pattern not found")
else:
    code = code.replace(old, new, 1)
    p.write_text(code, encoding='utf-8')
    print("OK: annotation moved to top")
