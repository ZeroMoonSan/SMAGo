import pathlib
p = pathlib.Path('../src/agent.go')
code = p.read_text(encoding='utf-8')

# Make /compress pass through to LLM by excluding it from the unknown-command fallback
old = 'case strings.HasPrefix(text, "/"):\n\t\t\ta.send(chatID, "unknown command: "+text+"\\ntype /help")\n\t\t\tcontinue'
new = 'case strings.HasPrefix(text, "/") && text != "/compress":\n\t\t\ta.send(chatID, "unknown command: "+text+"\\ntype /help")\n\t\t\tcontinue'
if old not in code:
    print("ERROR: fallback case not found")
else:
    code = code.replace(old, new, 1)
    p.write_text(code, encoding='utf-8')
    print("OK: /compress now passes through to LLM")
