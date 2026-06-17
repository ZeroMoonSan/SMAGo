package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/energye/systray"
)

const (
	supervisorAddr = "127.0.0.1:7778"
	currentFile    = "data/current.json"
	nextFile       = "data/next.json"
	badFile        = "data/bad.json"
	crashWindow    = 20 * time.Second
	exeName        = "agent.exe"
)

type State struct {
	Version string `json:"version"`
}

var (
	upgradeCh   = make(chan string, 4)
	stopCh      = make(chan struct{})
	currentTag  = "—"
	statusItem  *systray.MenuItem
	versionItem *systray.MenuItem
)

// chdirToProjectRoot changes the working directory to the project root
// (the parent of the bin/ directory where supervisor runs from, or the
// first ancestor containing src/go.mod). This lets supervisor-bg.exe be
// launched from anywhere — CWD is always the SMAGo project root.
func chdirToProjectRoot() {
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe) // e.g. D:\...\SMAGo\bin
		parent := filepath.Dir(dir)
		if _, err := os.Stat(filepath.Join(parent, "src", "go.mod")); err == nil {
			if err := os.Chdir(parent); err == nil {
				return
			}
		}
		// try the exe dir itself
		if _, err := os.Stat(filepath.Join(dir, "src", "go.mod")); err == nil {
			if err := os.Chdir(dir); err == nil {
				return
			}
		}
	}
	// fallback: walk up from CWD
	for _, candidate := range []string{".", "..", "../..", "../../.."} {
		if _, err := os.Stat(filepath.Join(candidate, "src", "go.mod")); err == nil {
			abs, _ := filepath.Abs(candidate)
			_ = os.Chdir(abs)
			return
		}
	}
}

// sourceRoot finds the directory containing go.mod (where .go files live).
func sourceRoot() string {
	for _, candidate := range []string{"src", ".", ".."} {
		if _, err := os.Stat(filepath.Join(candidate, "go.mod")); err == nil {
			return candidate
		}
	}
	return "."
}

func main() {
	runtime.LockOSThread()

	chdirToProjectRoot()

	mustExist("data")
	mustExist("data/versions")

	_ = os.WriteFile("data/supervisor.pid", []byte(fmt.Sprintf("%d", os.Getpid())), 0644)
	defer os.Remove("data/supervisor.pid")

	systray.Run(onReady, onExit)
}

func onReady() {
	iconPath := filepath.Join("bin", "smago.ico")
	if _, err := os.Stat(iconPath); err != nil {
		iconPath = "smago.ico"
	}
	if icon, err := os.ReadFile(iconPath); err == nil && len(icon) > 0 {
		systray.SetIcon(icon)
	}
	systray.SetTitle("SMAGo")
	systray.SetTooltip("SMAGo supervisor")

	statusItem = systray.AddMenuItem("starting…", "current supervisor status")
	statusItem.Disable()
	systray.AddSeparator()
	versionItem = systray.AddMenuItem("version: —", "current agent version")
	versionItem.Disable()
	systray.AddSeparator()

	mLogs := systray.AddMenuItem("Open log", "open data\\smago.log")
	mData := systray.AddMenuItem("Open data dir", "open data dir")
	systray.AddSeparator()
	mHealth := systray.AddMenuItem("Health check", "ping health")
	mRestart := systray.AddMenuItem("Restart agent", "restart agent")
	systray.AddSeparator()

	mRollback := systray.AddMenuItem("Rollback...", "pick a previous commit to roll back to")
	rebuildRollbackMenu(mRollback)
	systray.AddSeparator()

	mQuit := systray.AddMenuItem("Quit supervisor", "stop everything")

	go runSupervisor()

	mLogs.Click(func() { openWithDefaultApp(filepath.Join("data", "smago.log")) })
	mData.Click(func() { openWithDefaultApp("data") })
	mHealth.Click(func() {
		resp, err := http.Get("http://" + supervisorAddr + "/health")
		if err != nil {
			setStatus("health: " + err.Error())
			return
		}
		resp.Body.Close()
		setStatus("health: ok")
	})
	mRestart.Click(func() {
		select {
		case upgradeCh <- currentTag:
		default:
		}
		setStatus("restart requested")
	})
	mQuit.Click(func() { systray.Quit() })
}

func onExit() { close(stopCh) }

func setStatus(s string) {
	if statusItem != nil {
		statusItem.SetTitle(s)
	}
}

func setVersion(v string) {
	currentTag = v
	if versionItem != nil {
		versionItem.SetTitle("version: " + v)
	}
	systray.SetTooltip("SMAGo supervisor — agent " + v)
}

func openWithDefaultApp(path string) {
	switch runtime.GOOS {
	case "windows":
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", path).Start()
		_ = exec.Command("cmd", "/c", "start", "", path).Start()
	case "darwin":
		_ = exec.Command("open", path).Start()
	default:
		_ = exec.Command("xdg-open", path).Start()
	}
}

func runSupervisor() {
	http.HandleFunc("/upgrade", func(w http.ResponseWriter, r *http.Request) {
		v := r.URL.Query().Get("v")
		if v == "" {
			http.Error(w, "missing v", 400)
			return
		}
		select {
		case upgradeCh <- v:
		default:
			_ = os.WriteFile(nextFile, []byte(fmt.Sprintf(`{"version":%q}`, v)), 0644)
		}
		_, _ = w.Write([]byte("ok"))
		setStatus("upgrade → " + v)
	})

	http.HandleFunc("/rebuild", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST only", 405)
			return
		}
		setStatus("rebuilding from source...")
		sha := gitShortHead()
		if sha == "" {
			http.Error(w, "git HEAD failed", 500)
			return
		}
		outDir := "data/versions/" + sha
		os.MkdirAll(outDir, 0755)
		outPath := outDir + "/" + exeName

		src := sourceRoot()
		build := exec.Command("go", "build", "-o", outPath, "./"+src)
		build.Stdout = os.Stdout
		build.Stderr = os.Stderr
		build.Env = augmentPath(os.Environ())
		if err := build.Run(); err != nil {
			setStatus("build failed: " + err.Error())
			http.Error(w, "build failed: "+err.Error(), 500)
			return
		}
		_ = os.WriteFile(outDir+"/commit.txt", []byte(sha+"\n"), 0644)

		select {
		case upgradeCh <- sha:
		default:
			_ = os.WriteFile(nextFile, []byte(fmt.Sprintf(`{"version":%q}`, sha)), 0644)
		}
		_, _ = w.Write([]byte("rebuilt and swapping to " + sha))
		setStatus("rebuilt → " + sha)
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	go func() {
		if err := http.ListenAndServe(supervisorAddr, nil); err != nil {
			log.Printf("supervisor: http: %v", err)
		}
	}()

	current := readState(currentFile)
	if current == "" {
		current = "v0"
		_ = os.WriteFile(currentFile, []byte(fmt.Sprintf(`{"version":%q}`, current)), 0644)
	}
	setVersion(current)

	for {
		select {
		case <-stopCh:
			setStatus("shutting down")
			return
		default:
		}

		target := current
		select {
		case v := <-upgradeCh:
			target = v
			_ = os.Remove(nextFile)
		default:
			if next := readState(nextFile); next != "" && next != current {
				target = next
				_ = os.Remove(nextFile)
			}
		}

		if isBad(target) {
			setStatus("refusing bad " + target)
			target = current
		}

		exePath := "data/versions/" + target + "/" + exeName
		if _, err := os.Stat(exePath); err != nil {
			fallback := latestVersionDir()
			if fallback != "" && fallback != target {
				setStatus(target + " missing, falling back to " + fallback)
				target = fallback
				current = fallback
				_ = os.WriteFile(currentFile, []byte(fmt.Sprintf(`{"version":%q}`, current)), 0644)
				exePath = "data/versions/" + target + "/" + exeName
			} else if fallback == target || fallback == "" {
				setStatus(target + " missing, no versions available")
				time.Sleep(5 * time.Second)
				continue
			}
		}

		if _, err := os.Stat(exePath); err != nil {
			setStatus(target + " binary missing")
			time.Sleep(5 * time.Second)
			continue
		}

		setStatus("running " + target)
		newCurrent, ok := runOne(target, exePath, current)
		if ok && newCurrent != current {
			current = newCurrent
			_ = os.WriteFile(currentFile, []byte(fmt.Sprintf(`{"version":%q}`, current)), 0644)
		}
		setVersion(current)
		time.Sleep(1 * time.Second)
	}
}

func runOne(target, exePath, current string) (string, bool) {
	started := time.Now()
	cmd := exec.Command(exePath, "--smago-version="+target, "--smago-supervisor=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = augmentPath(os.Environ())
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		setStatus("start failed: " + err.Error())
		return current, false
	}

	exitCh := make(chan error, 1)
	go func() { exitCh <- cmd.Wait() }()

	select {
	case err := <-exitCh:
		elapsed := time.Since(started)
		if err == nil {
			setStatus(target + " exited cleanly")
			return current, true
		}
		setStatus(fmt.Sprintf("%s crashed: %v", target, err))
		if elapsed < crashWindow {
			markBad(target)
		}
		return current, false

	case v := <-upgradeCh:
		setStatus("swapping " + target + " → " + v)
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exitCh:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-exitCh
		}
		return v, true

	case <-stopCh:
		setStatus("stopping " + target)
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exitCh:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-exitCh
		}
		return current, false
	}
}

func augmentPath(env []string) []string {
	paths := []string{
		`C:\Program Files\Go\bin`,
		`C:\Go\bin`,
		`C:\Program Files (x86)\Go\bin`,
	}
	augmented := os.Getenv("PATH")
	for _, p := range paths {
		if !strings.Contains(strings.ToLower(augmented), strings.ToLower(p)) {
			augmented = p + ";" + augmented
		}
	}
	out := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(strings.ToUpper(e), "PATH=") {
			out = append(out, e)
		}
	}
	return append(out, "PATH="+augmented)
}

func gitShortHead() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func readState(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return ""
	}
	return s.Version
}

func isBad(v string) bool {
	data, err := os.ReadFile(badFile)
	if err != nil {
		return false
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return false
	}
	return s.Version == v
}

func markBad(v string) {
	_ = os.WriteFile(badFile, []byte(fmt.Sprintf(`{"version":%q}`, v)), 0644)
}

func latestVersionDir() string {
	root := "data/versions"
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	var bestName string
	var bestTime time.Time
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		exePath := filepath.Join(root, e.Name(), exeName)
		if _, err := os.Stat(exePath); err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(bestTime) {
			bestTime = info.ModTime()
			bestName = e.Name()
		}
	}
	return bestName
}

func rebuildRollbackMenu(parent *systray.MenuItem) {
	commits := gitLog10()
	if len(commits) == 0 {
		empty := parent.AddSubMenuItem("no git commits found", "")
		empty.Disable()
		return
	}
	for i, c := range commits {
		label := c.short + " " + c.summary
		if len(label) > 55 {
			label = label[:55] + "..."
		}
		tip := ""
		if i == 0 {
			tip = " (current)"
		}
		item := parent.AddSubMenuItem(label+tip, "rollback to commit "+c.short)
		sha := c.short
		summary := c.summary
		item.Click(func() {
			go func() {
				setStatus("rollback → " + sha)
				if err := doRollback(sha, summary); err != nil {
					setStatus("rollback " + sha + ": " + err.Error())
				}
			}()
		})
	}
}

type gitCommit struct {
	short   string
	summary string
}

func gitLog10() []gitCommit {
	out, err := exec.Command("git", "log", "--oneline", "-10").Output()
	if err != nil {
		return nil
	}
	var commits []gitCommit
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if len(line) < 8 {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			continue
		}
		commits = append(commits, gitCommit{short: parts[0], summary: parts[1]})
	}
	return commits
}

func doRollback(sha, summary string) error {
	cmd := exec.Command("git", "checkout", sha)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git checkout %s: %w", sha, err)
	}

	outDir := filepath.Join("data", "versions", sha)
	_ = os.MkdirAll(outDir, 0755)
	outPath := filepath.Join(outDir, exeName)

	src := sourceRoot()
	build := exec.Command("go", "build", "-o", outPath, "./"+src)
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	build.Env = augmentPath(os.Environ())
	if err := build.Run(); err != nil {
		return fmt.Errorf("build: %w", err)
	}
	_ = os.WriteFile(filepath.Join(outDir, "commit.txt"), []byte(sha+"\n"), 0644)

	select {
	case upgradeCh <- sha:
	default:
		_ = os.WriteFile(nextFile, []byte(fmt.Sprintf(`{"version":%q}`, sha)), 0644)
	}
	setStatus("rollback → " + sha[:7] + " " + summary)
	return nil
}

func mustExist(path string) {
	if _, err := os.Stat(path); err != nil {
		log.Fatalf("supervisor: required path %s missing: %v", path, err)
	}
}
