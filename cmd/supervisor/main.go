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
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
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

var upgradeCh = make(chan string, 4)

func main() {
	mustExist("data")
	mustExist("data/versions")

	_ = os.WriteFile("data/supervisor.pid", []byte(fmt.Sprintf("%d", os.Getpid())), 0644)
	defer os.Remove("data/supervisor.pid")

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
		log.Printf("supervisor: queued swap to %s", v)
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

	for {
		target := current
		// Drain pending upgrade requests.
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
			log.Printf("supervisor: %s marked bad, refusing", target)
			target = current
		}

		exePath := "data/versions/" + target + "/" + exeName
		if _, err := os.Stat(exePath); err != nil {
			log.Printf("supervisor: %s missing (%v), keeping %s", exePath, err, current)
			target = current
			exePath = "data/versions/" + target + "/" + exeName
		}

		// Run it; update current on success if it's a new version.
		newCurrent, ok := runOne(target, exePath, current)
		_ = newCurrent
		if ok && newCurrent != current {
			current = newCurrent
			_ = os.WriteFile(currentFile, []byte(fmt.Sprintf(`{"version":%q}`, current)), 0644)
		} else if !ok {
			// Stay on `current`.
		}
		time.Sleep(1 * time.Second)
	}
}

// runOne launches the given version and waits for it to exit OR for an
// upgrade request. Returns the new current version (may equal the input)
// and whether the run was "successful" (ran at least crashWindow).
func runOne(target, exePath, current string) (string, bool) {
	log.Printf("supervisor: launching %s", exePath)
	started := time.Now()
	cmd := exec.Command(exePath, "--smago-version="+target, "--smago-supervisor=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Inherit but ensure Go is on PATH so the agent can run `go build`
	// for self-upgrades. We add common Go install locations if missing.
	cmd.Env = augmentPath(os.Environ())
	if err := cmd.Start(); err != nil {
		log.Printf("supervisor: failed to start %s: %v", exePath, err)
		return current, false
	}

	exitCh := make(chan error, 1)
	go func() { exitCh <- cmd.Wait() }()

	select {
	case err := <-exitCh:
		elapsed := time.Since(started)
		if err == nil {
			log.Printf("supervisor: %s exited cleanly after %s", target, elapsed)
			return current, true
		}
		log.Printf("supervisor: %s exited with %v after %s", target, err, elapsed)
		if elapsed < crashWindow {
			if target != current {
				log.Printf("supervisor: NEW %s crashed fast — marking bad, keeping %s", target, current)
				markBad(target)
			} else {
				log.Printf("supervisor: %s crashed fast — marking bad, no fallback", target)
				markBad(target)
			}
		}
		return current, false

	case v := <-upgradeCh:
		log.Printf("supervisor: upgrade to %s requested, stopping %s gracefully", v, target)
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exitCh:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-exitCh
		}
		log.Printf("supervisor: %s stopped, switching to %s", target, v)
		// Don't update `current` here — the caller's loop will pick up v as
		// the new target on the next iteration.
		_ = current
		return v, true
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
