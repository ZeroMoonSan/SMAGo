import pathlib

p = pathlib.Path('../src/agent.go')
code = p.read_text(encoding='utf-8')

# 1. Add "Connection prematurely closed" to isRetryableError
old1 = 'strings.Contains(msg, "EOF")'
new1 = 'strings.Contains(msg, "Connection prematurely closed") ||\n\t\tstrings.Contains(msg, "EOF")'
assert old1 in code, 'pattern1 not found'
code = code.replace(old1, new1, 1)

# 2. Replace hardcoded whitelist with isWhitelistedCommand
old2 = '''	if rs := a.getRun(chatID); rs != nil {
\t\t\tswitch {
\t\t\tcase text == "/help", text == "/health", text == "/chatid", text == "/version",
\t\t\t\ttext == "/trace", text == "/debug", text == "/tools", text == "/verbose",
\t\t\t\ttext == "/compress",
\t\t\t\ttext == "/sessions",
\t\t\t\ttext == "/dcp" || strings.HasPrefix(text, "/dcp "):
\t\t\t\t// whitelisted -- fall through
\t\t\tdefault:
\t\t\t\ta.send(chatID, "⏳ task in progress -- use /stop or /abort to interrupt")
\t\t\t\tcontinue
\t\t\t}
\t\t}'''
new2 = '''	if rs := a.getRun(chatID); rs != nil && !isWhitelistedCommand(text) {
\t\t\ta.send(chatID, "⏳ task in progress -- use /stop or /abort to interrupt")
\t\t\tcontinue
\t\t}'''
if old2 in code:
    code = code.replace(old2, new2, 1)
    print("OK: whitelist replaced")
else:
    print("WARN: whitelist pattern not found, skipping")

# 3. Replace hardcoded /help text with buildHelpText()
old3 = '''case text == "/help":
\t\t\ta.send(chatID, "/sessions /new /switch /rename /delete\\n/clear /stop /abort\\n/models /model /provider /system /maxsteps /shell\\n/dcp\\n/tools /trace /verbose\\n/version /rollback /gitsha /gitlog /gitdiff\\n/chatid /health")'''
new3 = '''case text == "/help":
\t\t\ta.send(chatID, buildHelpText())'''
if old3 not in code:
    # try multiline version
    old3b = 'case text == "/help":'
    # Find and replace the next few lines after /help
    lines = code.split('\n')
    new_lines = []
    i = 0
    while i < len(lines):
        new_lines.append(lines[i])
        if 'case text == "/help":' in lines[i]:
            # Skip the old help text lines (look for the send line)
            i += 1
            while i < len(lines):
                if 'a.send(chatID,' in lines[i] and '/sessions' in lines[i]:
                    new_lines.append('\t\t\ta.send(chatID, buildHelpText())')
                    i += 1
                    break
                elif 'continue' in lines[i].strip():
                    new_lines.append(lines[i])
                    i += 1
                    break
                elif 'case ' in lines[i] or '// ' in lines[i]:
                    # hit next case, insert the new help
                    new_lines.append('\t\t\ta.send(chatID, buildHelpText())')
                    new_lines.append('\t\t\tcontinue')
                    break
                i += 1
        i += 1
    code = '\n'.join(new_lines)
    print("OK: /help replaced (multiline)")
else:
    code = code.replace(old3, new3, 1)
    print("OK: /help replaced")

p.write_text(code, encoding='utf-8')
