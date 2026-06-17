import pathlib
p = pathlib.Path('../src/main.go')
code = p.read_text(encoding='utf-8')

old = '\tcmds := []BotCommand{'
if old not in code:
    print("ERROR: not found")
else:
    # Find the whole cmds block + SetMyCommands call
    lines = code.split('\n')
    start = -1
    end = -1
    for i, line in enumerate(lines):
        if 'cmds := []BotCommand{' in line:
            start = i
        if start >= 0 and 'len(cmds)' in line:
            end = i
            break
    if start < 0 or end < 0:
        print("ERROR: could not find cmds block")
    else:
        replacement = [
            '\tbotCmds := buildBotCommands()',
            '\tif err := tg.SetMyCommands(botCmds); err != nil {',
            '\t\tlog.Printf("warn: setMyCommands failed: %v", err)',
            '\t} else {',
            '\t\tlog.Printf("telegram: ✓ registered %d bot commands", len(botCmds))',
            '\t}',
        ]
        lines = lines[:start] + replacement + lines[end+1:]
        p.write_text('\n'.join(lines), encoding='utf-8')
        print("OK: replaced hardcoded cmds")
