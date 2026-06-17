import pathlib
p = pathlib.Path('../src/agent.go')
code = p.read_text(encoding='utf-8')

old_func = '''func (a *Agent) handleRenameSession(chatID int64, text string) {
\targs := strings.TrimSpace(strings.TrimPrefix(text, "/rename"))
\tparts := strings.Fields(args)
\tif len(parts) < 2 {
\t\ta.send(chatID, "usage: /rename <old> <new>")
\t\treturn
\t}
\toldName, newName := parts[0], parts[1]
\tif err := a.store.RenameSession(chatID, oldName, newName); err != nil {
\t\ta.send(chatID, "❌ "+err.Error())
\t\treturn
\t}
\ta.send(chatID, fmt.Sprintf("✅ renamed: %s → %s", oldName, newName))
}'''

new_func = '''func (a *Agent) handleRenameSession(chatID int64, text string) {
\tnewName := strings.TrimSpace(strings.TrimPrefix(text, "/rename"))
\tif newName == "" {
\t\t// Ask LLM to generate a session name from conversation context
\t\tsess, err := a.store.GetActive(chatID)
\t\tif err != nil {
\t\t\ta.send(chatID, "❌ no active session: "+err.Error())
\t\t\treturn
\t\t}
\t\tmsgs := sess.Messages()
\t\tif len(msgs) == 0 {
\t\t\ta.send(chatID, "❌ session is empty — nothing to base a name on")
\t\t\treturn
\t\t}
\t\ta.typing(chatID)
\t\tprompt := []ChatMessage{
\t\t\t{Role: "system", Content: "You are a naming assistant. Given the first few messages of a conversation, suggest a short, lowercase, hyphenated session name (2-4 words, e.g. 'bug-fix-503-retry' or 'tomsk-bus-routes'). Reply with ONLY the name, nothing else."},
\t\t}
\t\tn := len(msgs)
\t\tif n > 5 {
\t\t\tn = 5
\t\t}
\t\tprompt = append(prompt, msgs[:n]...)
\t\tresp, _, llmErr := a.llm.Chat(prompt, nil)
\t\tif llmErr != nil {
\t\t\ta.send(chatID, "❌ failed to generate name: "+llmErr.Error())
\t\t\treturn
\t\t}
\t\tnewName = strings.TrimSpace(resp.Content)
\t\tnewName = strings.ToLower(newName)
\t\tnewName = strings.ReplaceAll(newName, " ", "-")
\t\tnewName = strings.Trim(newName, "\\"'")
\t\tif len(newName) > 40 {
\t\t\tnewName = newName[:40]
\t\t}
\t\tif newName == "" {
\t\t\ta.send(chatID, "❌ LLM returned empty name")
\t\t\treturn
\t\t}
\t}
\tsess, err := a.store.GetActive(chatID)
\tif err != nil {
\t\ta.send(chatID, "❌ no active session: "+err.Error())
\t\treturn
\t}
\toldName := sess.Name()
\tif oldName == newName {
\t\ta.send(chatID, "session is already named "+newName)
\t\treturn
\t}
\tif err := a.store.RenameSession(chatID, oldName, newName); err != nil {
\t\ta.send(chatID, "❌ "+err.Error())
\t\treturn
\t}
\ta.send(chatID, fmt.Sprintf("✅ renamed: %s → %s", oldName, newName))
}'''

if old_func not in code:
    print("ERROR: old handleRenameSession not found")
else:
    code = code.replace(old_func, new_func, 1)
    p.write_text(code, encoding='utf-8')
    print("OK: replaced handleRenameSession with LLM-powered version")
