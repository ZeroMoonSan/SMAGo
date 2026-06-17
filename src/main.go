package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "upgrade":
			if err := cmdUpgrade(os.Args[2:]); err != nil {
				log.Fatal(err)
			}
			return
		case "rollback":
			if err := cmdRollback(os.Args[2:]); err != nil {
				log.Fatal(err)
			}
			return
		case "smoke-test":
			if err := cmdSmokeTest(); err != nil {
				log.Fatalf("smoke-test: %v", err)
			}
			return
		}
	}

	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	logLaunchFlags()
	enableWindowsVT()
	printBanner(shaFromGit(), os.Stderr)

	cfgPath, err := findConfig()
	if err != nil {
		return err
	}
	log.Printf("config: %s", cfgPath)

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	if v := os.Getenv("SMAGO_TELEGRAM_TOKEN"); v != "" {
		cfg.TelegramToken = v
	}
	if v := os.Getenv("SMAGO_TELEGRAM_CHAT_ID"); v != "" {
		var id int64
		fmt.Sscanf(v, "%d", &id)
		if id != 0 {
			cfg.TelegramChatID = id
		}
	}

	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return fmt.Errorf("data dir: %w", err)
	}
	if err := setupLogging(cfg.DataDir); err != nil {
		return fmt.Errorf("log setup: %w", err)
	}
	defer writePID(cfg.DataDir, 0)()

	ProbedShells = ProbeShells()
	log.Printf("shells: %v", ShellNames(ProbedShells))

	proxyURL := detectAndApplyProxy()
	if proxyURL != "" {
		log.Printf("proxy: %s (from system settings)", proxyURL)
	} else if v := os.Getenv("HTTPS_PROXY"); v != "" {
		log.Printf("proxy: %s (from env HTTPS_PROXY)", v)
	} else if v := os.Getenv("HTTP_PROXY"); v != "" {
		log.Printf("proxy: %s (from env HTTP_PROXY)", v)
	} else {
		log.Println("proxy: none")
	}
	setGlobalProxy(proxyURL)

	llm, err := NewLLM(cfg)
	if err != nil {
		return fmt.Errorf("llm: %w", err)
	}

	store, err := NewStore(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer store.Close()

	_ = writePID(cfg.DataDir, os.Getpid())

	tg := NewTelegram(cfg.TelegramToken)
	if proxyURL != "" {
		if err := tg.SetProxyURL(proxyURL); err != nil {
			log.Printf("warn: telegram proxy setup failed: %v", err)
		}
	}

	log.Println("telegram: dialing api.telegram.org...")
	dialCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	me, terr := tg.GetMe(dialCtx)
	cancel()
	if terr != nil {
		log.Printf("telegram: ✗ getMe failed: %v", terr)
	} else if me != nil && me.Result != nil {
		log.Printf("telegram: ✓ connected as @%s (id=%d, name=%q)",
			me.Result.Username, me.Result.ID, me.Result.FirstName)
	}

	cmds := []BotCommand{
		{Command: "start", Description: "Show welcome & help"},
		{Command: "help", Description: "Show all commands"},
		{Command: "sessions", Description: "List all sessions"},
		{Command: "new", Description: "Create a new session"},
		{Command: "switch", Description: "Switch to another session"},
		{Command: "rename", Description: "Rename a session"},
		{Command: "delete", Description: "Delete a session"},
		{Command: "clear", Description: "Clear current session"},
		{Command: "models", Description: "Pick a model (inline buttons)"},
		{Command: "model", Description: "Show or set the current model"},
		{Command: "provider", Description: "Show or set the current provider"},
		{Command: "system", Description: "Show or set the system prompt"},
		{Command: "maxsteps", Description: "Show or set the tool-call budget"},
		{Command: "shell", Description: "Show or change the terminal shell"},
		{Command: "tools", Description: "List available tools"},
		{Command: "trace", Description: "Show the last agent actions"},
		{Command: "verbose", Description: "Toggle inline traces + tool annotations"},
		{Command: "stop", Description: "Stop the current task after this step"},
		{Command: "abort", Description: "Force-stop the current task (kills tools)"},
		{Command: "rollback", Description: "Roll back to a previous version"},
		{Command: "versions", Description: "List all built versions"},
		{Command: "upgrade", Description: "Build a new version and ask supervisor to swap"},
		{Command: "restart", Description: "Restart the agent (supervisor respawns)"},
		{Command: "version", Description: "Show build version"},
		{Command: "gitlog", Description: "Show recent commits"},
		{Command: "gitsha", Description: "Show the current commit"},
		{Command: "gitdiff", Description: "Show working-tree diff"},
		{Command: "health", Description: "Liveness check"},
		{Command: "chatid", Description: "Show this chat's id"},
		{Command: "compress", Description: "Compress conversation context"},
		{Command: "dcp", Description: "Dynamic Context Pruning status/config"},
	}
	if err := tg.SetMyCommands(cmds); err != nil {
		log.Printf("warn: setMyCommands failed: %v", err)
	} else {
		log.Printf("telegram: ✓ registered %d bot commands", len(cmds))
	}

	tools := NewToolRegistry(cfg)
	tools.registerDefaults()
	defer tools.Close()
	agent := NewAgent(cfg, llm, store, tg, tools)

	injectAddr := os.Getenv("SMAGO_INJECT_ADDR")
	if injectAddr == "" {
		injectAddr = "127.0.0.1:7777"
	}
	inject := newInjectServer(injectAddr, agent.Push)
	if err := inject.Start(); err != nil {
		log.Printf("warn: inject server failed to start: %v", err)
	} else {
		log.Printf("inject: http://%s/inject (POST {chat_id,text})", injectAddr)
		log.Printf("mirror: http://%s/mirror (GET, polls outgoing messages)", injectAddr)
	}
	defer inject.Stop()
	agent.SetRecorder(inject.Record)

	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutdown signal")
		cancel()
	}()

	log.Printf("llm: provider=%s model=%s", cfg.Provider, cfg.DefaultModel)
	log.Printf("chat:  target chatID=%d (set via /chatid command or SMAGO_TELEGRAM_CHAT_ID)", cfg.TelegramChatID)
	log.Println("ready: long-polling for updates...")

	if err := agent.RunLoop(ctx); err != nil && err != context.Canceled {
		return fmt.Errorf("agent: %w", err)
	}
	return nil
}

func shaFromGit() string {
	if sha, err := gitHead(); err == nil && len(sha) >= 7 {
		return sha[:7]
	}
	return "unknown"
}

func detectAndApplyProxy() string {
	if v := os.Getenv("SMAGO_PROXY"); v != "" {
		_ = os.Setenv("HTTP_PROXY", v)
		_ = os.Setenv("HTTPS_PROXY", v)
		return v
	}
	if url, ok := detectWindowsProxy(); ok {
		return url
	}
	return ""
}

func findConfig() (string, error) {
	if len(os.Args) >= 2 {
		cand := os.Args[1]
		if !strings.HasPrefix(cand, "--") && !isSubcommand(cand) {
			if _, err := os.Stat(cand); err == nil {
				return cand, nil
			}
		}
	}
	if v := os.Getenv("SMAGO_CONFIG"); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v, nil
		}
	}
	exeDir, _ := os.Executable()
	if exeDir != "" {
		exeDir = filepath.Dir(exeDir)
		for _, name := range []string{"config.json", "smago.json"} {
			p := filepath.Join(exeDir, name)
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		for _, name := range []string{"config.json", "smago.json"} {
			p := filepath.Join(cwd, name)
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".config", "smago", "config.json")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no config found")
}

func setupLogging(dataDir string) error {
	logPath := filepath.Join(dataDir, "smago.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	log.SetOutput(f)
	return nil
}

func writePID(dataDir string, pid int) func() {
	path := filepath.Join(dataDir, "smago.pid")
	if pid > 0 {
		_ = os.WriteFile(path, []byte(fmt.Sprintf("%d", pid)), 0644)
		return func() { _ = os.Remove(path) }
	}
	return func() {}
}

func logLaunchFlags() {
	v := flagValue("--smago-version")
	if v != "" {
		log.Printf("launch: smago-version=%s", v)
	}
	if flagValue("--smago-supervisor") == "1" {
		log.Printf("launch: under supervisor")
	}
}

func isSubcommand(name string) bool {
	switch name {
	case "upgrade", "rollback", "smoke-test":
		return true
	}
	return false
}

func flagValue(name string) string {
	for i, a := range os.Args {
		if a == name && i+1 < len(os.Args) {
			return os.Args[i+1]
		}
		if strings.HasPrefix(a, name+"=") {
			return strings.TrimPrefix(a, name+"=")
		}
	}
	return ""
}
