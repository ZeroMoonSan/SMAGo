# SMAGo — Self-Modifying AI Agent

A lightweight Go agent that communicates via Telegram, calls any OpenAI-compatible LLM, stores conversation history in SQLite, and can modify its own source code and binary at runtime.

SMAGo runs as a Windows system tray application with a supervisor that auto-restarts on crash and manages hot-swap upgrades.

## Features

- **Telegram bot** — long-polling, no webhooks needed
- **Multi-provider LLM** — OpenCode, DeepSeek, llama.cpp, or any OpenAI-compatible API
- **Tool calling** — terminal, read/write/edit files, web search, vision, Playwright browser
- **MCP support** — connects to Model Context Protocol servers (Playwright)
- **Self-modification** — upgrade, rollback, restart via `self_modify` tool or Telegram commands
- **Supervisor** — system tray icon, auto-restart on crash, version swap with bad-version detection
- **Markdown rendering** — headings as bold, tables with alignment, code blocks
- **Typing indicator** — bot shows "typing..." while processing
- **Session management** — SQLite-backed, multi-session, per-chat
- **DCP** — Dynamic Context Pruning to stay within model context windows
- **Stop/abort** — interrupt long-running tasks gracefully or forcefully

---

## Setup & Installation

### Prerequisites

- **Windows 10/11**
- **Go 1.26+** ([download](https://go.dev/dl/))
- **Git**
- **Node.js + npm** (for Playwright MCP)
- **Telegram bot token** — get one from [@BotFather](https://t.me/BotFather)

### 1. Clone the repository

```cmd
git clone git@github.com:AsmanovLev/SMAGo.git
cd SMAGo
```

### 2. Build

```cmd
build.bat
```

This produces three binaries in `bin/`:

| Binary | Description |
|--------|-------------|
| `bin/agent.exe` | Console build (for debugging) |
| `bin/smago-bg.exe` | Background build (no console window) |
| `bin/supervisor-bg.exe` | Supervisor with system tray icon |

### 3. Configure

```cmd
copy config.example.json config.json
```

Edit `config.json` and fill in:

| Field | Description |
|-------|-------------|
| `telegramToken` | Bot token from @BotFather |
| `telegramChatID` | Your Telegram chat ID (see below) |
| `provider` | LLM provider name (default: `opencode-go`) |
| `defaultModel` | Model name (default: `mimo-v2.5`) |
| `providers.*.apiKey` | API key for the chosen provider |

#### Finding your Telegram Chat ID

1. Start a chat with your bot in Telegram
2. Send any message
3. Open `data/smago.log` and look for the chat ID, or use a [@userinfobot](https://t.me/userinfobot)

#### Environment variables (optional, for secrets)

Keep sensitive values out of `config.json`:

```cmd
set SMAGO_TELEGRAM_TOKEN=your_token
set SMAGO_TELEGRAM_CHAT_ID=your_chat_id
set SMAGO_OPENCODE_KEY=your_api_key
```

### 4. Run

**With supervisor (recommended):**

```cmd
start-supervised.bat
```

The supervisor runs silently in the system tray. Right-click the tray icon for options.

**Without supervisor:**

```cmd
bin\smago-bg.exe
```

**Debug mode (console output):**

```cmd
bin\agent.exe
```

### 5. Verify

Send `/start` to your bot in Telegram. You should see a help message.

---

## Telegram Commands

### Conversation

| Command | Description |
|---------|-------------|
| `/start` | Show help message |
| `/new` | Start a fresh session |
| `/clear` | Wipe current session history |
| `/stop` | Stop after current step (graceful) |
| `/abort` | Kill current tool and stop (forceful) |
| `/compress` | Manually trigger context compression |

### Configuration

| Command | Description |
|---------|-------------|
| `/models` | Pick a model (inline buttons) |
| `/model [name]` | Show or set model |
| `/provider [name]` | Show or set provider |
| `/system [text]` | Show or set system prompt |
| `/maxsteps [N]` | Tool-call budget (default: 200) |
| `/rename [name]` | Rename current session (auto-generates name if omitted) |

### Visibility

| Command | Description |
|---------|-------------|
| `/tools` | List available tools |
| `/trace` | Show last 20 agent actions |
| `/verbose` | Toggle inline tool-call traces |
| `/dcp [on\|off\|reset]` | Dynamic Context Pruning controls |

### Session Management

| Command | Description |
|---------|-------------|
| `/sessions` | List all sessions |
| `/switch <name>` | Switch to a named session |
| `/delete <name>` | Delete a session |
| `/del <name>` | Alias for `/delete` |

### Self-Update

| Command | Description |
|---------|-------------|
| `/version` | Show build version, git SHA, uptime |
| `/upgrade [SHA]` | Build and swap to a commit |
| `/rollback` | Pick a previous version to roll back to |
| `/restart` | Restart the agent |
| `/gitsha` | Show current git HEAD |
| `/gitlog [N]` | Show last N commits |
| `/gitdiff [path]` | Show diff |

---

## Tools (LLM-callable)

| Tool | Description |
|------|-------------|
| `terminal` | Run shell commands (30s timeout) |
| `read_file` | Read a file from disk |
| `write_file` | Write a file (requires `read_file` first on same path) |
| `edit_file` | Line-level edits: replace, delete, insert |
| `list_dir` | List directory contents |
| `web_search` | Search DuckDuckGo (top 10 results) |
| `vision` | Analyze images via multimodal model |
| `compress` | Compress old conversation ranges with summaries |
| `self_modify` | Restart, upgrade, rollback, or check version |
| `playwright__*` | Playwright browser tools (29 tools via MCP) |

---

## Architecture

```
SMAGo/
├── src/
│   ├── main.go                 # Entry point, signal handling, PID, logging
│   ├── config.go               # JSON config with multi-provider support
│   ├── llm.go                  # OpenAI-compatible chat completions client
│   ├── telegram.go             # Long-polling Bot API (stdlib only, zero deps)
│   ├── session.go              # SQLite store (modernc.org/sqlite, no CGO)
│   ├── agent.go                # Main loop: msg → LLM → tools → response
│   ├── tools.go                # Tool registry
│   ├── self_modify_tool.go     # Self-modification: upgrade, rollback, restart
│   ├── mcp.go                  # Model Context Protocol client (stdio JSON-RPC)
│   ├── markdown.go             # Markdown → Telegram HTML
│   ├── dcp.go                  # Dynamic Context Pruning
│   ├── dcp_compress.go         # Context compression logic
│   ├── dcp_strategies.go       # Pruning strategies
│   ├── vision.go               # Image analysis via multimodal model
│   ├── web_search_tool.go      # DuckDuckGo HTML search
│   ├── browser_tool.go         # Playwright browser integration
│   ├── shell.go                # Shell command execution
│   ├── http.go                 # HTTP client
│   ├── inject.go               # Prompt injection helpers
│   ├── git.go                  # Git operations for self-upgrade
│   ├── cmd_upgrade.go          # Upgrade build logic
│   ├── cmd/
│   │   ├── supervisor/         # System tray supervisor
│   │   └── genicon/            # Icon generation tool
│   └── go.mod
├── bin/                        # Built binaries
├── data/                       # Runtime data (sessions, logs, versions)
├── config.json                 # Your configuration (not in git)
├── config.example.json         # Example configuration
└── build.bat                   # Build script
```

## Version Management

SMAGo uses git commit SHAs as version identifiers:

```
data/
  versions/
    cff3262/agent.exe
    addc8d7/agent.exe
  current.json    → {"version": "cff3262"}
  next.json       → {"version": "addc8d7"} (pending swap)
```

The supervisor watches for `next.json` and swaps binaries gracefully. If a version crashes within 20 seconds, it's marked as bad and won't be used again.

---

## Config Search Order

1. Path passed as first CLI argument
2. `$SMAGO_CONFIG` environment variable
3. `<exe-dir>\config.json`
4. `<cwd>\config.json`
5. `~/.config/smago/config.json`

---

## License

Not yet specified. Contact the author for usage terms.
