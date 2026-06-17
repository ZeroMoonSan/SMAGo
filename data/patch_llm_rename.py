import pathlib
p = pathlib.Path('../src/agent.go')
code = p.read_text(encoding='utf-8')

old_block = '''\tif newName == "" {
\t\t// Auto-generate name for the active session
\t\tsess, err := a.store.GetActive(chatID)
\t\tif err != nil {
\t\t\ta.send(chatID, \"\u274c no active session: \"+err.Error())
\t\t\treturn
\t\t}
\t\tsessions, _ := a.store.ListSessions(chatID)
\t\tn := len(sessions) + 1
\t\tfor {
\t\t\tcandidate := fmt.Sprintf(\"renamed-%d\", n)
\t\t\tfound := false
\t\t\tfor _, s := range sessions {
\t\t\t\tif s.Name == candidate {
\t\t\t\t\tfound = true
\t\t\t\t\tbreak
\t\t\t\t}
\t\t\t}
\t\t\tif !found {
\t\t\t\tnewName = candidate
\t\t\t\tbreak
\t\t\t}
\t\t\tn++
\t\t}
\t\toldName := sess.Name()
\t\tif err := a.store.RenameSession(chatID, oldName, newName); err != nil {
\t\t\ta.send(chatID, \"\u274c \"+err.Error())
\t\t\treturn
\t\t}
\t\ta.send(chatID, fmt.Sprintf(\"\u2705 renamed: %s \u2192 %s\", oldName, newName))
\t\treturn
\t}'''

if old_block not in code:
    print('ERROR: block not found')
else:
    new_block = '''\tif newName == "" {
\t\t// Ask LLM to generate a session name from conversation context
\t\tsess, err := a.store.GetActive(chatID)
\t\tif err != nil {
\t\t\ta.send(chatID, \"\u274c no active session: \"+err.Error())
\t\t\treturn
\t\t}
\t\tmsgs := sess.Messages()
\t\tif len(msgs) == 0 {
\t\t\ta.send(chatID, \"\u274c session is empty \u2014 nothing to base a name on\")
\t\t\treturn
\t\t}
\t\ta.typing(chatID)
\t\tprompt := []ChatMessage{
\t\t\t{Role: "system", Content: "You are a naming assistant. Given the first few messages of a conversation, suggest a short, lowercase, hyphenated session name (2-4 words, e.g. 'bug-fix-503-retry' or 'tomsk-bus-routes'). Reply with ONLY the name, nothing else."},
\t\t}
\t\tn := len(msgs)
\t\tif n > 5 { n = 5 }
\t\tprompt = append(prompt, msgs[:n]...)
\t\tresp, _, llmErr := a.llm.Chat(prompt, nil)
\t\tif llmErr != nil {
\t\t\ta.send(chatID, \"\u274c failed to generate name: \"+llmErr.Error())
\t\t\treturn
\t\t}
\t\tnewName = strings.TrimSpace(resp.Content)
\t\tnewName = strings.ToLower(newName)
\t\tnewName = strings.ReplaceAll(newName, " ", "-")
\t\tnewName = strings.Trim(newName, "\x22\x27")
\t\tif len(newName) > 40 { newName = newName[:40] }
\t\tif newName == "" {
\t\t\ta.send(chatID, \"\u274c LLM returned empty name\")
\t\t\treturn
\t\t}
\t}'''
    code = code.replace(old_block, new_block, 1)
    p.write_text(code, encoding='utf-8')
    print('OK: replaced with LLM-powered rename')
