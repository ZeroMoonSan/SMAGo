package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// injectServer runs a small localhost-only HTTP server that lets tools and
// debug sessions push messages into the agent's update loop without going
// through Telegram. It also captures a mirror of every outgoing message
// so local debug clients can read bot responses without a Telegram account.
//
// Endpoints:
//   POST /inject   {"chat_id": <int>, "text": "<string>"}
//   GET  /health   returns "ok"
//   GET  /mirror   returns buffered outgoing messages (drains the ring)
//   GET  /mirror?wait=1   long-polls until at least one message arrives
type injectServer struct {
	push  func(chatID int64, text string) error
	addr  string
	srv   *http.Server

	mu       sync.Mutex
	outbound []OutboundMsg
	waiters  []chan struct{}
}

type OutboundMsg struct {
	ChatID int64     `json:"chat_id"`
	Text   string    `json:"text"`
	Time   time.Time `json:"time"`
}

func newInjectServer(addr string, push func(chatID int64, text string) error) *injectServer {
	return &injectServer{addr: addr, push: push}
}

func (s *injectServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	})
	mux.HandleFunc("/inject", s.handleInject)
	mux.HandleFunc("/mirror", s.handleMirror)
	s.srv = &http.Server{Addr: s.addr, Handler: mux}
	go func() {
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("inject server: %v", err)
		}
	}()
	return nil
}

func (s *injectServer) Stop() {
	if s.srv != nil {
		_ = s.srv.Close()
	}
}

// Record is called by the agent right after it sends a Telegram message
// so debug clients can read what was sent.
func (s *injectServer) Record(chatID int64, text string) {
	msg := OutboundMsg{ChatID: chatID, Text: text, Time: time.Now()}
	s.mu.Lock()
	s.outbound = append(s.outbound, msg)
	if len(s.outbound) > 200 {
		s.outbound = s.outbound[len(s.outbound)-200:]
	}
	waiters := s.waiters
	s.waiters = nil
	s.mu.Unlock()
	for _, ch := range waiters {
		close(ch)
	}
}

func (s *injectServer) handleInject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ChatID int64  `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Text == "" {
		http.Error(w, "text required", http.StatusBadRequest)
		return
	}
	if err := s.push(req.ChatID, req.Text); err != nil {
		http.Error(w, "push: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"ok":true}`)
}

func (s *injectServer) handleMirror(w http.ResponseWriter, r *http.Request) {
	wait := r.URL.Query().Get("wait") == "1"
	if !wait {
		s.mu.Lock()
		out := append([]OutboundMsg(nil), s.outbound...)
		s.outbound = nil
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	// Long-poll: wait until at least one message is recorded.
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.waiters = append(s.waiters, ch)
	s.mu.Unlock()
	select {
	case <-ch:
		s.mu.Lock()
		out := append([]OutboundMsg(nil), s.outbound...)
		s.outbound = nil
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	case <-time.After(30 * time.Second):
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[]`)
	case <-r.Context().Done():
		return
	}
}
