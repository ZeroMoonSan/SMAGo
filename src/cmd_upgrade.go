package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// projectRoot returns the absolute path to the project root directory
// (the one containing src/go.mod). It walks up from the executable's
// directory, then from CWD, to find it.
func projectRoot() string {
	// Try relative to the executable first.
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for i := 0; i < 10; i++ {
			if _, err := os.Stat(filepath.Join(dir, "src", "go.mod")); err == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	// Fallback: walk up from CWD.
	if cwd, err := os.Getwd(); err == nil {
		dir := cwd
		for i := 0; i < 10; i++ {
			if _, err := os.Stat(filepath.Join(dir, "src", "go.mod")); err == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
		_ = cwd
	}
	// Last resort: check common relative paths.
	for _, candidate := range []string{".", "..", "../.."} {
		if _, err := os.Stat(filepath.Join(candidate, "src", "go.mod")); err == nil {
			abs, _ := filepath.Abs(candidate)
			return abs
		}
	}
	// Give up — return CWD and hope for the best.
	cwd, _ := os.Getwd()
	return cwd
}

func cmdSmokeTest() error {
	log.SetOutput(os.Stderr)
	log.SetPrefix("smoke: ")

	enableWindowsVT()
	log.Printf("running smoke test (no Telegram)")

	cfgPath, err := findConfig()
	if err != nil {
		return err
	}
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if v := os.Getenv("SMAGO_TELEGRAM_TOKEN"); v != "" {
		cfg.TelegramToken = v
	}
	proxyURL := detectAndApplyProxy()
	setGlobalProxy(proxyURL)

	llm, err := NewLLM(cfg)
	if err != nil {
		return fmt.Errorf("llm: %w", err)
	}
	_ = llm

	store, err := NewStore(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	_ = store.Close()

	tg := NewTelegram(cfg.TelegramToken)
	if proxyURL != "" {
		_ = tg.SetProxyURL(proxyURL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	me, err := tg.GetMe(ctx)
	cancel()
	if err != nil {
		return fmt.Errorf("telegram getMe: %w", err)
	}
	log.Printf("telegram ok: @%s", me.Result.Username)

	tools := NewToolRegistry(cfg)
	tools.registerDefaults()
	if len(tools.All()) == 0 {
		return fmt.Errorf("no tools registered")
	}
	log.Printf("tools ok: %d registered", len(tools.All()))

	log.Printf("smoke test PASS")
	return nil
}

// findSourceRoot finds the directory containing go.mod.
func findSourceRoot() string {
	root := projectRoot()
	src := filepath.Join(root, "src")
	if _, err := os.Stat(filepath.Join(src, "go.mod")); err == nil {
		return src
	}
	return root
}

func cmdUpgrade(args []string) error {
	var version, source string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--version" && i+1 < len(args):
			version = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--version="):
			version = strings.TrimPrefix(args[i], "--version=")
		case args[i] == "--source" && i+1 < len(args):
			source = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--source="):
			source = strings.TrimPrefix(args[i], "--source=")
		}
	}
	if version == "" {
		return fmt.Errorf("--version=SHA required")
	}
	if source == "" {
		source = findSourceRoot()
	}

	root := projectRoot()
	outDir := filepath.Join(root, "data", "versions", version)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return err
	}
	outPath := filepath.Join(outDir, "agent.exe")

	sha, commitErr := gitCommitAll("upgrade: build "+version)
	if commitErr != nil {
		log.Printf("upgrade: git commit failed (continuing): %v", commitErr)
	} else {
		log.Printf("upgrade: git HEAD = %s", sha)
		_ = os.WriteFile(filepath.Join(outDir, "commit.txt"), []byte(sha+"\n"), 0644)
	}

	log.Printf("upgrade: building %s from %s", outPath, source)

	build := hiddenCmd("go", "build", "-o", outPath, ".")
	build.Dir = source
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}

	log.Printf("upgrade: smoke-testing new binary")
	test := hiddenCmd(outPath, "smoke-test")
	test.Dir = root
	test.Stdout = os.Stdout
	test.Stderr = os.Stderr
	if err := test.Run(); err != nil {
		return fmt.Errorf("smoke-test failed: %w", err)
	}

	log.Printf("upgrade: asking supervisor to swap to %s", version)
	resp, err := http.Post("http://127.0.0.1:7778/upgrade?v="+version, "", nil)
	if err != nil {
		return fmt.Errorf("supervisor not reachable: %w", err)
	}
	resp.Body.Close()
	log.Printf("upgrade: signal sent, supervisor will swap")
	return nil
}

func runSelfUpgrade(version string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	cmd := hiddenCmd(exe, "upgrade", "--version="+version)
	cmd.Dir = projectRoot()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func cmdRollback(args []string) error {
	var version, source string
	var force bool
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--version" && i+1 < len(args):
			version = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--version="):
			version = strings.TrimPrefix(args[i], "--version=")
		case args[i] == "--source" && i+1 < len(args):
			source = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--source="):
			source = strings.TrimPrefix(args[i], "--source=")
		case args[i] == "--force":
			force = true
		}
	}
	if version == "" {
		return fmt.Errorf("--version=SHA required")
	}
	if source == "" {
		source = findSourceRoot()
	}

	root := projectRoot()
	commitPath := filepath.Join(root, "data", "versions", version, "commit.txt")
	commitData, err := os.ReadFile(commitPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", commitPath, err)
	}
	sha := strings.TrimSpace(string(commitData))
	if sha == "" {
		return fmt.Errorf("empty commit SHA in %s", commitPath)
	}
	log.Printf("rollback: target=%s commit=%s", version, sha)

	if !force {
		dirty, err := gitTrackedDirty()
		if err != nil {
			return fmt.Errorf("git status: %w", err)
		}
		if len(dirty) > 0 {
			return fmt.Errorf("working tree has %d uncommitted tracked change(s); commit/stash first or pass --force:\n  %s",
				len(dirty), strings.Join(dirty, "\n  "))
		}
	}

	if err := gitCheckout(sha); err != nil {
		return fmt.Errorf("git checkout %s: %w", sha, err)
	}
	log.Printf("rollback: working tree reverted to %s", sha[:7])

	outDir := filepath.Join(root, "data", "versions", version)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return err
	}
	outPath := filepath.Join(outDir, "agent.exe")

	log.Printf("rollback: rebuilding %s", outPath)
	build := hiddenCmd("go", "build", "-o", outPath, ".")
	build.Dir = source
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}

	log.Printf("rollback: smoke-testing new binary")
	test := hiddenCmd(outPath, "smoke-test")
	test.Dir = root
	test.Stdout = os.Stdout
	test.Stderr = os.Stderr
	if err := test.Run(); err != nil {
		return fmt.Errorf("smoke-test failed: %w", err)
	}

	log.Printf("rollback: asking supervisor to swap to %s", version)
	resp, err := http.Post("http://127.0.0.1:7778/upgrade?v="+version, "", nil)
	if err != nil {
		return fmt.Errorf("supervisor not reachable: %w", err)
	}
	resp.Body.Close()
	log.Printf("rollback: signal sent, supervisor will swap")
	return nil
}

func runSelfRollback(version string, force bool) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	args := []string{"rollback", "--version=" + version}
	if force {
		args = append(args, "--force")
	}
	cmd := hiddenCmd(exe, args...)
	cmd.Dir = projectRoot()
	out, err := cmd.CombinedOutput()
	return string(out), err
}
