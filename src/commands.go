package main

import (
	"fmt"
	"strings"
)

// CmdDef defines a Telegram command.
type CmdDef struct {
	Command     string // "/help"
	Description string // "Show all commands"
	Category    string // "Meta"
	WhiteListed bool   // allowed during active agent run
}

// cmdRegistry holds all registered commands in display order.
var cmdRegistry = []CmdDef{
	// Welcome
	{"/start", "Show welcome & help", "Welcome", false},
	{"/help", "Show all commands", "Welcome", true},

	// Sessions
	{"/sessions", "List all sessions", "Sessions", true},
	{"/new", "Create a new session", "Sessions", false},
	{"/switch", "Switch to another session", "Sessions", false},
	{"/rename", "Rename active session (or auto-name)", "Sessions", false},
	{"/delete", "Delete session (active if no name given)", "Sessions", false},
	{"/del", "Alias for /delete", "Sessions", false},
	{"/sw", "Alias for /switch", "Sessions", false},

	// Conversation
	{"/compress", "Compress conversation context", "Conversation", true},

	// Control
	{"/stop", "Stop the current task after this step", "Control", false},
	{"/abort", "Force-stop the current task (kills tools)", "Control", false},

	// Configuration
	{"/models", "Pick a model (inline buttons)", "Configuration", false},
	{"/model", "Show or set the current model", "Configuration", false},
	{"/provider", "Show or set the current provider", "Configuration", false},
	{"/system", "Show or set the system prompt", "Configuration", false},
	{"/maxsteps", "Show or set the tool-call budget", "Configuration", false},
	{"/shell", "Show or change the terminal shell", "Configuration", false},

	// Context
	{"/dcp", "Dynamic Context Pruning status/config", "Context", true},

	// Visibility
	{"/tools", "List available tools", "Visibility", true},
	{"/trace", "Show the last agent actions", "Visibility", true},
	{"/verbose", "Toggle inline traces + tool annotations", "Visibility", true},

	// Self-update
	{"/version", "Show build version", "Self-update", true},
	{"/rollback", "Roll back to a previous version", "Self-update", false},
	{"/gitsha", "Show the current commit", "Self-update", true},
	{"/gitlog", "Show recent commits", "Self-update", false},
	{"/gitdiff", "Show working-tree diff", "Self-update", false},

	// Meta
	{"/chatid", "Show this chat's id", "Meta", true},
	{"/health", "Liveness check", "Meta", true},
}

// cmdWhitelist is the set of commands allowed during an active agent run.
var cmdWhitelist map[string]bool

func init() {
	cmdWhitelist = make(map[string]bool)
	for _, c := range cmdRegistry {
		if c.WhiteListed {
			cmdWhitelist[c.Command] = true
		}
	}
}

// isWhitelistedCommand returns true if the command can be used during a running task.
func isWhitelistedCommand(text string) bool {
	if cmdWhitelist[text] {
		return true
	}
	// Check prefix: e.g. "/sw something" should match "/sw"
	space := strings.IndexByte(text, ' ')
	if space > 0 {
		return cmdWhitelist[text[:space]]
	}
	return false
}

// buildHelpText generates the /help message from the command registry.
func buildHelpText() string {
	var b strings.Builder
	b.WriteString("*SMAGo Commands*\n")

	lastCat := ""
	for _, c := range cmdRegistry {
		if c.Category != lastCat {
			b.WriteString("\n*" + c.Category + ":*\n")
			lastCat = c.Category
		}
		b.WriteString(fmt.Sprintf("%s \u2014 %s\n", c.Command, c.Description))
	}
	return b.String()
}

// buildBotCommands returns the BotCommand list for Telegram's setMyCommands API.
func buildBotCommands() []BotCommand {
	cmds := make([]BotCommand, len(cmdRegistry))
	for i, c := range cmdRegistry {
		cmds[i] = BotCommand{Command: c.Command[1:], Description: c.Description}
	}
	return cmds
}
