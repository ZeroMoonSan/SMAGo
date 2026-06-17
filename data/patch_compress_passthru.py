import pathlib

# Fix 1: Make /compress pass through to LLM instead of being caught by fallback
p = pathlib.Path('../src/agent.go')
code = p.read_text(encoding='utf-8')

# Replace the DCP command handler to also pass /compress to LLM
old = """\t\t// \xe2\x94\x80\xef\xb8\x8f DCP \xe2\x94\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80
\t\tcase text == "/dcp" || strings.HasPrefix(text, "/dcp "):
\t\t\ta.handleDCPCommand(chatID, text)
\t\t\tcontinue"""

new = """\t\t// \xe2\x94\x80\xef\xb8\x8f DCP \xe2\x94\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80\xef\xb8\x80
\t\tcase text == "/dcp" || strings.HasPrefix(text, "/dcp "):
\t\t\ta.handleDCPCommand(chatID, text)
\t\t\tcontinue
\t\tcase text == "/compress":
\t\t\t// Pass /compress to LLM so it can decide when to compress
\t\t\tbreak"""

if old not in code:
    print("ERROR: DCP case not found")
else:
    code = code.replace(old, new, 1)
    p.write_text(code, encoding='utf-8')
    print("OK: /compress now passes through to LLM")
