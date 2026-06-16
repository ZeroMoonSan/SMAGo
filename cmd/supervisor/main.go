// supervisor watches the agent, swaps versions on demand, rolls back on crash.
//
// This program is THE thing that never changes — it's the safety net.
// If you find yourself "improving" it, stop and write a new version of
// the agent instead.
//
// Protocol (files under ./data):
//   current.json  {"version": "v1"}        — what we're running right now
//   next.json     {"version": "v2"}        — what to swap to on next cycle
//   bad.json      {"version": "v3"}        — versions that crashed on boot
//
// HTTP API on 127.0.0.1:7778:
//   POST /upgrade?v=vN    signal supervisor to swap on next cycle
//   GET  /health          return "ok"
//
// UI: system tray icon (smago.ico) with a menu (status / open log / quit).
// No console window — the binary is built with -H windowsgui.
package main

import (
	"context"
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
	// Make sure we don't pop a console when the binary is built with
	// -H windowsgui (it shouldn't, but be defensive).
	runtime.LockOSThread()

	mustExist("data")
	mustExist("data/versions")

	_ = os.WriteFile("data/supervisor.pid", []byte(fmt.Sprintf("%d", os.Getpid())), 0644)
	defer os.Remove("data/supervisor.pid")

	// Tray icon + menu in its own goroutine. systray.Run blocks.
	// We initialise from the goroutine and exit cleanly via stopCh.
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

	mLogs := systray.AddMenuItem("Open log", "open data\\smago.log in the default app")
	mData := systray.AddMenuItem("Open data dir", "open the data directory in Explorer")
	systray.AddSeparator()
	mHealth := systray.AddMenuItem("Health check", "ping 127.0.0.1:7778/health")
	mRestart := systray.AddMenuItem("Restart agent", "kill current agent, supervisor relaunches it")
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

func onExit() {
	close(stopCh)
}

func handleTrayClicks() {} // unused — kept for future

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
			if target != current {
				markBad(target)
			} else {
				markBad(target)
			}
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
	current := os.Getenv("PATH")
	augmented := current
	for _, p := range paths {
		if !strings.Contains(strings.ToLower(augmented), strings.ToLower(p)) {
			augmented = p + ";" + augmented
		}
	}
	out := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(strings.ToUpper(e), "PATH=") {
			continue
		}
		out = append(out, e)
	}
	return append(out, "PATH="+augmented)
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

// silence unused-import warning if context goes out of use later.
var _ = context.Background
