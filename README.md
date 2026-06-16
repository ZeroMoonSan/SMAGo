# SMAGo — Self-Modifying AI Agent

Lightweight Go agent that talks to you via Telegram, calls an OpenAI-compatible LLM (any of your local/free providers), keeps conversation history in SQLite, and can run shell commands and read/write files.

## Quick start

1. **Build** (Windows):
   ```cmd
   build.bat
   ```
   Produces `smago.exe` (console, for debugging) and `smago-bg.exe` (silent, for double-click).

2. **Fill in `config.json`** — `telegramToken` is already there, set `telegramChatID` to your chat id.

3. **Get your chat id**: run `smago.exe`, write `/chatid` to the bot in Telegram, paste the number it returns into `config.json`. (Or skip this — the bot will only respond to anyone who messages it, but won't proactively send.)

4. **Double-click `smago-bg.exe`** — it runs in the background, no console window. Logs go to `data\smago.log`.

5. **Manage**:
   - `start-bg.bat` — start in background
   - `stop.bat` — stop
   - `data\smago.log` — live logs
   - `data\sessions.db` — conversation history

## Environment overrides (recommended for secrets)

Keep your token out of `config.json`:
```cmd
set SMAGO_TELEGRAM_TOKEN=6627691089:AAHc37tl0TTVLxTQmz48xLp8KHRCy6aSnOQ
set SMAGO_TELEGRAM_CHAT_ID=<your chat id>
smago-bg.exe
```

## Commands in Telegram

- `/chatid` — show your chat id
- `/tools` — list available tools
- `/health` — liveness ping
- `/clear` — wipe the current session history

## Config search order

1. Path passed as first argument
2. `$SMAGO_CONFIG`
3. `<exe-dir>\config.json` or `<exe-dir>\smago.json`
4. `<cwd>\config.json` or `<cwd>\smago.json`
5. `~/.config/smago/config.json`

## Architecture (MVP)

```
main.go     entry point, signal handling, PID file, logging
config.go   JSON config (mirrors your opencode providers)
llm.go      OpenAI-compatible chat completions client
telegram.go long-polling Bot API client (stdlib only)
session.go  SQLite store (modernc.org/sqlite, no CGO)
tools.go    in-process tools: bash, read_file, write_file, list_dir
agent.go    main loop: user msg → LLM → tool calls → response
```

All in a single static binary. No Docker, no WSL, no external services.

## Migrated from opencode

Pulled your existing providers verbatim from `~/.config/opencode/opencode.json`:
- `llama-local` → `http://127.0.0.1:8080/v1`
- `arrayredes` → `http://arrayredes.ddns.net:54321/v1`
- `free-deepseek` → `http://127.0.0.1:8000/v1`

## Roadmap

- v2: MCP client for hot-loadable tool servers (Playwright, TSU, email, etc.)
- v2: Self-modification loop — agent writes Go code, compiles, hot-loads MCP server, rolls back on failure
- v2: Watchdog process — supervisor never modified by agent, restarts on crash
- v3: TUI / Web UI on top of the same agent core
