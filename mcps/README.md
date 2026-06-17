# MCP Servers

This directory stores MCP (Model Context Protocol) server binaries/scripts.
It is gitignored - do not commit server files here.

## What is MCP?

MCP lets SMAGo connect to external tool servers via a standard protocol.
Each server exposes tools that the LLM can call, just like built-in tools.

## Adding a server

1. Put your MCP server files in a subfolder: mcps/my-server/
2. Add it to config.json under the "mcp" key
3. Restart SMAGo - tools from the server appear as my-server__toolname

## Example: filesystem server

"mcp": {
  "fs": {
    "command": ["npx", "-y", "@modelcontextprotocol/server-filesystem", "C:\\Users\\me\\docs"],
    "enabled": true
  }
}

## Notes

- Any stdio-based MCP server works (Node.js, Python, Go, Rust, etc.)
- SMAGo connects on startup, max 10 tools per server
- Disabled servers (enabled: false) are skipped
- Server logs appear in data/smago.log

## Links

- https://modelcontextprotocol.io/
- https://github.com/modelcontextprotocol/servers
