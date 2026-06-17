# SMAGo — Self-Modifying AI Agent

A lightweight Go agent that communicates via Telegram, calls any OpenAI-compatible LLM, stores conversation history in SQLite, and can modify its own source code and binary at runtime.

SMAGo runs as a Windows system tray application with a supervisor that auto-restarts on crash and manages hot-swap upgrades.

> **Written entirely by AI.** Minimax M3 and DeepSeek via OpenCode created the architecture and initial boilerplate. Then SMAGo via MiMo V2.5 (vision) developed itself. Human role: idea, direction, code review.

> 🇷🇺 *Читать на русском: [README.ru.md](README.ru.md)*


## Features

- **Telegram bot** — long-polling, no webhooks needed
- **Multi-provider LLM** — OpenCode, DeepSeek, llama.cpp, or any OpenAI-compatible API
- **Tool calling** — terminal, read/write/edit files, web search, vision
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
- **Telegram bot token** — get one from [@BotFather](https://t.me/BotFather)

### Quick install (winget)

```cmd
winget install --id GoLang.Go -e --source winget
winget install --id Git.Git -e --source winget
```

Or download manually from [go.dev](https://go.dev/dl/) and [git-scm.com](https://git-scm.com/).


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
3. Open `data/smago.log` and look for the chat ID, or use [@userinfobot](https://t.me/userinfobot)

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

| `self_modify` | Restart, upgrade, rollback, or check version |
| `my-server__*` | Dynamic tools from MCP servers (see below) |

---

## MCP (Model Context Protocol)

SMAGo supports external tool servers via [MCP](https://modelcontextprotocol.io/).
Any stdio-based MCP server works — Node.js, Python, Go, Rust.

### How it works

1. You add an MCP server to `config.json`
2. SMAGo spawns the server process on startup
3. Tools from the server appear as `<server>__<tool>` (e.g. `fs__read_file`)
4. The LLM can call them just like built-in tools

### Setup

1. Put your MCP server files in `mcps/` (gitignored):

```cmd
mkdir mcps\my-server
```

2. Add to `config.json`:

```json
{
  "mcp": {
    "fs": {
      "command": ["npx", "-y", "@modelcontextprotocol/server-filesystem", "C:\\path\\to\\folder"],
      "enabled": true
    }
  }
}
```

3. Restart SMAGo. New tools appear in `/tools`.

### Config fields

| Field | Description |
|-------|-------------|
| `command` | Array: executable + args (spawned via stdio) |
| `enabled` | `true` to connect, `false` to skip |
| `env` | Optional env vars passed to the process |

### Notes

- Max 10 tools per server (most important ones, in declaration order)
- Server logs go to `data/smago.log`
- Disabled servers are skipped entirely
- See `mcps/README.md` for more examples

---


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
│   ├── markdown.go             # Markdown → Telegram HTML
│   ├── dcp.go                  # Dynamic Context Pruning
│   ├── dcp_compress.go         # Context compression logic
│   ├── dcp_strategies.go       # Pruning strategies
│   ├── vision.go               # Image analysis via multimodal model
│   ├── web_search_tool.go      # DuckDuckGo HTML search
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

The supervisor watches for `next.json` and swaps binaries gracefully. If a version crashes within 20 seconds, it is marked as bad and will not be used again.

---

## Config Search Order

1. Path passed as first CLI argument
2. `$SMAGO_CONFIG` environment variable
3. `<exe-dir>\config.json`
4. `<cwd>\config.json`
5. `~/.config/smago/config.json`

---

## Changelog / History

The project started on **June 16, 2026** and evolved rapidly over a few days.


### Boilerplate (`2850fcb`)

The initial working prototype, written in a single session by Minimax M3 and DeepSeek:

- **Telegram bot** via long-polling (stdlib `net/http`, zero external deps)
- **LLM** — OpenAI-compatible chat completions (talked to local llama.cpp, self-hosted endpoints, OpenCode)
- **4 tools** — `bash` (shell), `read_file`, `write_file`, `list_dir`
- **Vision** — image analysis via multimodal model (mimo-v2.5)
- **SQLite sessions** — per-chat conversation history (modernc.org/sqlite, no CGO)
- **Markdown → HTML** — headings, bold, italic, code blocks for Telegram
- **Self-modification** — agent could edit its own Go source, recompile, and swap the binary
- **Supervisor** — system tray app with auto-restart on crash
- **Single binary** — no Docker, no WSL, no external services

Providers were migrated from the author's `opencode.json`: local llama.cpp, a home server, and a self-hosted DeepSeek proxy.

### Self-driven development (`34ede0a` — `0001437`)

From this point on, **SMAGo developed itself**. Each feature, fix, and refactor was written by the agent editing its own Go source code and recompiling via `self_modify`. The OpenCode agent supervised the process. Three times SMAGo broke itself badly enough that OpenCode had to step in and restore the codebase.

**What SMAGo built on its own:**

- **Git integration & self-upgrade** (`34ede0a`) — `git.go`: read git history, show diffs, use commit SHAs as version identifiers
- **Abort & tool-call traces** (`76cb51d` — `b8d628c`) — `/stop` and `/abort` commands, compact single-line trace format
- **Major refactor** (`83e9e0a`) — switch from sequential version numbers to **git commit SHAs**, tree-style tool trace with annotations, silent notifications, supervisor `/rebuild`
- **Multi-session management** (`9d94ad8`) — multiple named sessions per chat, tool-call annotations, self-upgrade confirmation prompt
- **DCP — Dynamic Context Pruning** (`3d081b5`) — the biggest feature: `/compress`, pruning strategies (dedup, error purge, system nudge), auto-calculated limits based on model context window, retry on HTTP 503/502/429/500 with exponential backoff
- **Session management polish** (`b8bc85a` — `c6eff72`) — `/rename` with LLM auto-naming, `/sessions`, `/switch`, `/delete`, command whitelist during active tasks, rich `/help`
### Cleanup & documentation (`ebca1ae` — current)

- Removed ~300 MB of binaries from git history (filter-repo)
- Removed `opencode-ref` submodule
- Added `.gitignore` for build artifacts, logs, databases
- Added `README.md` (EN) and `README.ru.md` (RU)
- Removed Playwright browser, MCP client, and Node.js dependency
- Final tool set: `terminal`, `read_file`, `write_file`, `edit_file`, `list_dir`, `web_search`, `vision`, `compress`, `self_modify`

---

## License

[MIT](LICENSE)
