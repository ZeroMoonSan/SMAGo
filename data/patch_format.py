import pathlib
p = pathlib.Path('../src/agent.go')
code = p.read_text(encoding='utf-8')

# Add pretty formatter for compress ranges before formatToolCall
old = 'func formatToolCall(name string, args map[string]any, annotation string, resultLen int, toolErr error) string {'
new = '''// formatCompressArgs pretty-prints compress tool arguments.
func formatCompressArgs(args map[string]any) string {
	var b strings.Builder
	if topic, ok := args["topic"].(string); ok {
		b.WriteString("\\n┗ topic: " + topic)
	}
	ranges, ok := args["ranges"].([]any)
	if !ok || len(ranges) == 0 {
		return b.String()
	}
	b.WriteString(fmt.Sprintf("\\n├ ranges (%d):", len(ranges)))
	for i, r := range ranges {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		start := -1
		end := -1
		if v, ok := m["start_idx"].(float64); ok {
			start = int(v)
		}
		if v, ok := m["end_idx"].(float64); ok {
			end = int(v)
		}
		summary := ""
		if v, ok := m["summary"].(string); ok {
			summary = truncateLog(v, 60)
		}
		icon := "├"
		if i == len(ranges)-1 {
			icon = "┗"
		}
		b.WriteString(fmt.Sprintf("\\n  %s [%d..%d] %s", icon, start, end, summary))
	}
	return b.String()
}

func formatToolCall(name string, args map[string]any, annotation string, resultLen int, toolErr error) string {'''

if old not in code:
    print("ERROR: formatToolCall not found")
else:
    code = code.replace(old, new, 1)
    
    # Now update the compress intercept to use the pretty formatter
    old2 = 'toolLines = append(toolLines, formatToolCall("compress", args, resp.Content, len(result), nil))'
    new2 = 'toolLines = append(toolLines, "**compress**"+formatCompressArgs(args)+"\\n→ "+fmt.Sprintf("%d", len(result))+" chars")'
    if old2 in code:
        code = code.replace(old2, new2, 1)
    else:
        print("WARNING: compress success formatToolCall not found")
    
    # Also the error case
    old3 = 'toolLines = append(toolLines, formatToolCall("compress", args, resp.Content, 0, execErr))'
    new3 = 'toolLines = append(toolLines, "**compress**"+formatCompressArgs(args)+"\\n→ error: "+execErr.Error())'
    if old3 in code:
        code = code.replace(old3, new3, 1)
    else:
        print("WARNING: compress error formatToolCall not found")
    
    p.write_text(code, encoding='utf-8')
    print("OK: added formatCompressArgs + updated compress trace output")
