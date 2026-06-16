// supervisor watches the agent, swaps versions on demand, rolls back on crash.
//
// HTTP API on 127.0.0.1:7778:
//   POST /upgrade?v=vN    signal supervisor to swap on next cycle
//   POST /rebuild         rebuild agent from source and swap
//   GET  /health          return "ok"
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

func main() {
	runtime.LockOSThread()

	mustExist("data")
	mustExist("data/versions")

	_ = os.WriteFile("data/supervisor.pid", []byte(fmt.Sprintf("%d", os.Getpid())), 0644)
	defer os.Remove("data/supervisor.pid")

	systray.Run(onReady, onExit)
}

func onReady() {
	iconPath := "smago.ico"
	if _, err := os.Stat(iconPath); err == nil {
		icon, _ := os.ReadFile(iconPath)
		if len(icon) > 0 {
			systray.SetIcon(icon)
		}
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

		build := exec.Command("go", "build", "-o", outPath, ".")
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
			setStatus(target + " missing, keeping " + current)
			target = current
			exePath = "data/versions/" + target + "/" + exeName
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

func mustExist(path string) {
	if _, err := os.Stat(path); err != nil {
		log.Fatalf("supervisor: required path %s missing: %v", path, err)
	}
}
