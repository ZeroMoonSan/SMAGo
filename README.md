# SMAGo — Self-Modifying AI Agent

Lightweight Go agent that talks to you via Telegram, calls an OpenAI-compatible LLM (any provider), keeps conversation history in SQLite, and can run shell commands, read/write files, and modify its own binary.

## Features

- **Telegram bot** — long-polling, no webhooks needed
- **Multi-provider LLM** — DeepSeek, llama.cpp, OpenCode, any OpenAI-compatible API
- **Tool calling** — terminal, read/write/edit files, web search, vision, Playwright browser
- **MCP support** — connects to Model Context Protocol servers (Playwright, etc.)
- **Self-modification** — upgrade, rollback, restart via `self_modify` tool or Telegram commands
- **Supervisor** — system tray icon, auto-restart on crash, version swap
- **Markdown rendering** — headings as bold, tables with column alignment, code blocks
- **Typing indicator** — bot shows "typing..." while processing
- **Session history** — SQLite-backed, per-chat
- **Stop/abort** — interrupt long-running tasks gracefully or forcefully

## Quick start

### 1. Build

```cmd
build.bat
```

Produces:
- `agent.exe` — console build (for debugging)
- `smago-bg.exe` — background build (no console window)
- `supervisor-bg.exe` — supervisor with system tray icon

### 2. Configure

Copy `config.example.json` to `config.json` and fill in:
- `telegramToken` — get from @BotFather
- `telegramChatID` — your chat id (run `/chatid` to find it)

### 3. Run

**With supervisor (recommended):**
```cmd
start-supervised.bat
```

**Without supervisor:**
```cmd
start-bg.bat
```

### 4. Manage

| File | Description |
|------|-------------|
| `start-bg.bat` | Start in background |
| `stop.bat` | Stop |
| `data/smago.log` | Live logs |
| `data/sessions.db` | Conversation history |

## Telegram Commands

### Conversation
- `/start` — help message
- `/new` — start fresh session
- `/clear` — wipe session history
- `/stop` — stop after current step (graceful)
- `/abort` — kill current tool and stop (forceful)

### Configuration
- `/models` — pick a model (inline buttons)
- `/model [name]` — show or set model
- `/provider [name]` — show or set provider
- `/system [text]` — show or set system prompt
- `/maxsteps [N]` — tool-call budget (default 200)

### Visibility
- `/tools` — list available tools
- `/trace` — show last 20 agent actions
- `/verbose` — toggle inline tool-call traces

### Self-update
- `/version` — show build version, git SHA, uptime
- `/upgrade SHA` — build and swap to commit SHA
- `/rollback` — pick a previous version to roll back to
- `/restart` — restart the agent
- `/gitsha` — show current git HEAD
- `/gitlog [N]` — show last N commits
- `/gitdiff [path]` — show diff

## Tools (for LLM)

| Tool | Description |
|------|-------------|
| `terminal` | Run shell commands (30s timeout) |
| `read_file` | Read a file from disk |
| `write_file` | Write a file (requires read_file first) |
| `edit_file` | Line-level edits: replace, delete, insert |
| `list_dir` | List directory contents |
| `web_search` | Search DuckDuckGo |
| `vision` | Analyze images (mimo-v2.5) |
| `self_modify` | Restart, upgrade, rollback, or check version |
| `playwright__*` | Playwright browser tools (29 tools via MCP) |

### Tool call formatting

Tool calls are displayed in a tree-style format:

```
**terminal**
┣ command: `ls -la`
┗ annotation: List files in data directory
→ 237 chars
```

For multi-line arguments:

```
**terminal**
╺ command:
```
cd .. && go build -o agent.exe . 2>&1
```
╺ annotation: Build the agent
→ 0 chars
```

## Version management

SMAGo uses git commit SHAs as version identifiers:

```
data/
  versions/
    cff3262/agent.exe
    addc8d7/agent.exe
  current.json    {"version": "cff3262"}
  next.json       {"version": "addc8d7"} (pending swap)
```

The supervisor watches for `next.json` and swaps binaries gracefully. If a version crashes within 20 seconds, it's marked as bad and won't be used again.

## Architecture

```
main.go              entry point, signal handling, PID file, logging
config.go            JSON config with multi-provider support
llm.go               OpenAI-compatible chat completions client
telegram.go          long-polling Bot API client (stdlib only, no deps)
session.go           SQLite store (modernc.org/sqlite, no CGO)
agent.go             main loop: user msg → LLM → tool calls → response
tools.go             tool registry: terminal, read/write/edit, list_dir
self_modify_tool.go  self-modification: upgrade, rollback, restart
mcp.go               Model Context Protocol client (stdio JSON-RPC)
markdown.go          Markdown → Telegram HTML (headings, tables, code, bold/italic)
vision.go            image analysis via mimo-v2.5
web_search_tool.go   DuckDuckGo HTML search
cmd/supervisor/      system tray supervisor with version management
```

## Config search order

1. Path passed as first argument
2. `$SMAGO_CONFIG`
3. `<exe-dir>\config.json`
4. `<cwd>\config.json`
5. `~/.config/smago/config.json`

## Environment overrides

Keep secrets out of `config.json`:
```cmd
set SMAGO_TELEGRAM_TOKEN=your_token
set SMAGO_TELEGRAM_CHAT_ID=your_chat_id
set SMAGO_OPENCODE_KEY=your_api_key
```
