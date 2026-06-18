package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultMaxSteps = 200

var startedAt = time.Now()

type runState struct {
	ctx    context.Context
	cancel context.CancelFunc
	stop   chan struct{}
	once   sync.Once
}

func (rs *runState) Stop()  { rs.once.Do(func() { close(rs.stop) }) }
func (rs *runState) Abort() { rs.cancel(); rs.Stop() }

type Agent struct {
	cfg      *Config
	llm      *LLM
	store    *Store
	tg       *Telegram
	tools    *ToolRegistry
	inject   chan injectedMsg
	record   func(chatID int64, text string)
	maxSteps map[int64]int
	verbose  bool
	traceBuf map[int64][]string

	runMu               sync.Mutex
	runs                map[int64]*runState
	pendingForceVersion string
	shellOverride       map[int64]ShellType
	dcpStates           map[int64]*DCPState
}

type injectedMsg struct {
	ChatID  int64
	Text    string
	trusted bool
}

func NewAgent(cfg *Config, llm *LLM, store *Store, tg *Telegram, tools *ToolRegistry) *Agent {
	return &Agent{
		cfg: cfg, llm: llm, store: store, tg: tg, tools: tools,
		inject: make(chan injectedMsg, 16), maxSteps: make(map[int64]int),
		traceBuf: make(map[int64][]string), runs: make(map[int64]*runState),
		shellOverride: make(map[int64]ShellType),
		dcpStates:     make(map[int64]*DCPState),
		verbose: true,
	}
}

func (a *Agent) getRun(chatID int64) *runState {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	return a.runs[chatID]
}

func (a *Agent) registerRun(chatID int64, rs *runState) func() {
	a.runMu.Lock()
	a.runs[chatID] = rs
	a.runMu.Unlock()
	return func() {
		a.runMu.Lock()
		if a.runs[chatID] == rs {
			delete(a.runs, chatID)
		}
		a.runMu.Unlock()
	}
}

// ── DCP state helpers ──────────────────────────────────

func (a *Agent) getDCPState(chatID int64) *DCPState {
	if dcp, ok := a.dcpStates[chatID]; ok {
		return dcp
	}
	dcp, err := a.store.LoadDCPState(chatID)
	if err != nil {
		log.Printf("dcp: load state failed for %d: %v", chatID, err)
		dcp = NewDCPState()
	}
	a.dcpStates[chatID] = dcp
	return dcp
}

func (a *Agent) saveDCPState(chatID int64, dcp *DCPState) {
	a.dcpStates[chatID] = dcp
	if err := a.store.SaveDCPState(chatID, dcp); err != nil {
		log.Printf("dcp: save state failed for %d: %v", chatID, err)
	}
}

func (a *Agent) getModelContextWindow() int {
	prov, ok := a.cfg.Providers[a.cfg.Provider]
	if !ok {
		return 0
	}
	m, ok := prov.Models[a.cfg.DefaultModel]
	if !ok {
		return 0
	}
	return m.ContextWindow
}

func (a *Agent) updateDCPLimitsFromModel(dcp *DCPState) {
	cw := a.getModelContextWindow()
	if cw <= 0 {
		return
	}
	dcp.CurrentTokens = 0
	a.cfg.DCP.MinContextTokens = cw * 20 / 100
	a.cfg.DCP.MaxContextTokens = cw * 80 / 100
}

// ── Trace / send helpers ───────────────────────────────

func (a *Agent) recordTrace(chatID int64, line string) string {
	a.traceBuf[chatID] = append(a.traceBuf[chatID], line)
	if len(a.traceBuf[chatID]) > 20 {
		a.traceBuf[chatID] = a.traceBuf[chatID][len(a.traceBuf[chatID])-20:]
	}
	if a.verbose {
		_ = a.tg.SendSilent(chatID, line)
		if a.record != nil {
			a.record(chatID, line)
		}
	}
	return line
}

func (a *Agent) Push(chatID int64, text string) error {
	select {
	case a.inject <- injectedMsg{ChatID: chatID, Text: text, trusted: true}:
		return nil
	default:
		return fmt.Errorf("inject channel full")
	}
}

func (a *Agent) SetRecorder(fn func(chatID int64, text string)) { a.record = fn }

func (a *Agent) send(chatID int64, text string) {
	_ = a.tg.Send(chatID, text)
	if a.record != nil {
		a.record(chatID, text)
	}
}

func (a *Agent) sendButtons(chatID int64, text string, rows [][]InlineButton) {
	_ = a.tg.SendButtons(chatID, text, rows)
	if a.record != nil {
		a.record(chatID, text)
	}
}

func (a *Agent) sendPlain(chatID int64, text string) {
	_ = a.tg.SendPlain(chatID, text)
	if a.record != nil {
		a.record(chatID, text)
	}
}

func (a *Agent) typing(chatID int64) { _ = a.tg.Typing(chatID) }

func truncateLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// formatToolCall renders a single tool call for verbose trace output.
func formatToolCall(name string, args map[string]any, resultLen int, toolErr error) string {
	var b strings.Builder
	b.WriteString("**" + name + "**")
	keys := sortedKeys(args)
	for i, k := range keys {
		valStr := fmt.Sprintf("%v", args[k])
		prefix := "┣"
		if i == len(keys)-1 {
			prefix = "┗"
		}
		if strings.Contains(valStr, "\n") || len(valStr) > 80 {
			b.WriteString("\n" + prefix + " " + k + ":\n```\n" + valStr + "\n```")
		} else {
			b.WriteString("\n" + prefix + " " + k + ": `" + truncateLog(valStr, 60) + "`")
		}
	}
	if toolErr != nil {
		b.WriteString("\n→ error: " + toolErr.Error())
	} else {
		b.WriteString(fmt.Sprintf("\n→ %d chars", resultLen))
	}
	return b.String()
}

func (a *Agent) getEffectiveShell(chatID int64) ShellType {
	if s, ok := a.shellOverride[chatID]; ok {
		return s
	}
	if a.cfg.DefaultShell != "" {
		if s, ok := ParseShellType(a.cfg.DefaultShell); ok {
			return s
		}
	}
	return ShellPowerPowerShell
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// isRetryableError returns true for transient HTTP errors worth retrying.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "503") ||
		strings.Contains(msg, "502") ||
		strings.Contains(msg, "429") ||
		strings.Contains(msg, "500") ||
		strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "Client.Timeout") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "Connection prematurely closed") ||
		strings.Contains(msg, "EOF")
}

// ──────────────────────────────────────────────────────
// Handle — main agent loop for one user message.
// ──────────────────────────────────────────────────────

func (a *Agent) Handle(chatID int64, userText string) (string, error) {
	sess, err := a.store.GetActive(chatID)
	if err != nil {
		return "", err
	}
	if err := sess.Append(ChatMessage{Role: "user", Content: userText}); err != nil {
		return "", err
	}

	dcp := a.getDCPState(chatID)
	dcp.CurrentTurn++
	a.updateDCPLimitsFromModel(dcp)

	var messages []ChatMessage
	if a.cfg.DCP.Enabled {
		messages = a.buildDCPMessages(sess, dcp, 0)
	} else {
		messages = []ChatMessage{{Role: "system", Content: a.cfg.SystemPrompt}}
		messages = append(messages, sess.Messages()...)
	}

	tools := a.tools.AsLLMTools()

	maxSteps := defaultMaxSteps
	if v, ok := a.maxSteps[chatID]; ok {
		maxSteps = v
	}

	shell := a.getEffectiveShell(chatID)
	runCtx, runCancel := context.WithCancel(WithShell(context.Background(), shell))
	rs := &runState{ctx: runCtx, cancel: runCancel, stop: make(chan struct{})}
	cleanup := a.registerRun(chatID, rs)
	defer cleanup()

	a.recordTrace(chatID, fmt.Sprintf("→ %s\nmax=%d tools=%d", truncateLog(userText, 100), maxSteps, len(tools)))

	for i := 0; i < maxSteps; i++ {
		select {
		case <-rs.stop:
			a.recordTrace(chatID, "⏹ stopped by user")
			return "⏹ stopped.", nil
		default:
		}
		if err := runCtx.Err(); err != nil {
			a.recordTrace(chatID, "🛑 aborted")
			return "🛑 aborted.", nil
		}

		a.typing(chatID)
		stepStart := time.Now()

		// LLM call with retry on transient errors (up to 4 retries, 15s intervals)
		var resp *ChatMessage
		var usage *Usage
		var llmErr error
		const maxRetries = 4
		const retryDelay = 15 * time.Second
		for attempt := 0; attempt <= maxRetries; attempt++ {
			resp, usage, llmErr = a.llm.Chat(messages, tools)
			if llmErr == nil {
				break
			}
			if errors.Is(llmErr, context.Canceled) {
				a.recordTrace(chatID, "🛑 aborted")
				return "🛑 aborted.", nil
			}
			if isRetryableError(llmErr) && attempt < maxRetries {
				a.recordTrace(chatID, fmt.Sprintf("⚠ LLM error (attempt %d/%d): %v — retrying in %ds",
					attempt+1, maxRetries+1, llmErr, int(retryDelay.Seconds())))
				select {
				case <-rs.stop:
					a.recordTrace(chatID, "⏹ stopped by user")
					return "⏹ stopped.", nil
				case <-runCtx.Done():
					a.recordTrace(chatID, "🛑 aborted")
					return "🛑 aborted.", nil
				case <-time.After(retryDelay):
				}
				continue
			}
			break
		}
		stepDur := time.Since(stepStart)
		if llmErr != nil {
			a.recordTrace(chatID, fmt.Sprintf("  ✗ LLM error: %v", llmErr))
			return "", llmErr
		}

		if usage != nil && usage.PromptTokens > 0 {
			dcp.CurrentTokens = usage.PromptTokens
		}

		var toolLines []string

		if len(resp.ToolCalls) == 0 {
			a.recordStep(chatID, i+1, maxSteps, usage, stepDur, nil, len(resp.Content), "")
			_ = sess.Append(ChatMessage{Role: "assistant", Content: resp.Content})
			a.saveDCPState(chatID, dcp)
			return resp.Content, nil
		}

		_ = sess.Append(ChatMessage{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})

		compressedThisStep := false
		compressCount := 0
		for _, tc := range resp.ToolCalls {
			a.typing(chatID)

			// Intercept compress tool — only allow once per step to prevent loops
			if tc.Function.Name == "compress" && a.cfg.DCP.Enabled {
				if compressedThisStep || compressCount >= 3 {
					msg := "Compression already done this step. Continue with your main task."
					if compressCount >= 3 {
						msg = "Maximum compressions reached for this turn (3). Continue with your main task."
					}
					_ = sess.Append(ChatMessage{Role: "tool", ToolCallID: tc.ID, Name: "compress",
						Content: msg})
					continue
				}
				var args map[string]any
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					toolLines = append(toolLines, fmt.Sprintf("  ✗ compress: bad args: %v", err))
					_ = sess.Append(ChatMessage{Role: "tool", ToolCallID: tc.ID, Name: "compress", Content: "error: bad arguments: " + err.Error()})
					continue
				}
				result, execErr := execCompress(runCtx, args, dcp, sess.Len())
				if execErr != nil {
					_ = sess.Append(ChatMessage{Role: "tool", ToolCallID: tc.ID, Name: "compress", Content: "error: " + execErr.Error()})
					toolLines = append(toolLines, "  ✗ compress: "+execErr.Error())
					continue
				}
				dcp.LastCompressStep = i + 1
				compressedThisStep = true
				compressCount++
				_ = sess.Append(ChatMessage{Role: "tool", ToolCallID: tc.ID, Name: "compress", Content: result})
				toolLines = append(toolLines, result)

				// Rebuild messages so LLM sees compressed context and stops compressing
				messages = a.buildDCPMessages(sess, dcp, i+1)
				continue
			}

			tdef, ok := a.tools.Get(tc.Function.Name)
			if !ok {
				toolLines = append(toolLines, fmt.Sprintf("  ✗ %s: unknown tool", tc.Function.Name))
				_ = sess.Append(ChatMessage{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: "error: unknown tool \"" + tc.Function.Name + "\""})
				continue
			}
			var args map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				toolLines = append(toolLines, fmt.Sprintf("  ✗ %s: bad args: %v", tc.Function.Name, err))
				_ = sess.Append(ChatMessage{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: "error: bad arguments: " + err.Error()})
				continue
			}

			if a.cfg.DCP.Enabled {
				dcp.RecordToolCall(tc.Function.Name, args, sess.Len()-1)
			}

			var toolErr error
			out, err := tdef.Execute(runCtx, args)
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					toolLines = append(toolLines, fmt.Sprintf("  🛑 %s cancelled", tc.Function.Name))
					a.recordStep(chatID, i+1, maxSteps, usage, stepDur, toolLines, -1, "")
					a.saveDCPState(chatID, dcp)
					return "🛑 aborted.", nil
				}
				toolErr = err
				out = "error: " + err.Error()
				if a.cfg.DCP.Enabled {
					dcp.RecordErrorToolCall(tc.ID, tc.Function.Name, tc.Function.Arguments, sess.Len()-1)
				}
			}
			toolLines = append(toolLines, formatToolCall(tc.Function.Name, args, len(out), toolErr))
			_ = sess.Append(ChatMessage{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: out})

			if a.cfg.DCP.Enabled {
				messages = a.buildDCPMessages(sess, dcp, i+1)
			} else {
				messages = append(messages,
					ChatMessage{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls},
					ChatMessage{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: out},
				)
			}
		}
		a.recordStep(chatID, i+1, maxSteps, usage, stepDur, toolLines, -1, resp.Content)
	}

	a.recordTrace(chatID, fmt.Sprintf("✗ hit %d-step cap", maxSteps))
	messages = append(messages, ChatMessage{Role: "system", Content: "Tool-call budget exhausted. Summarise in plain text."})
	resp, _, err := a.llm.Chat(messages, nil)
	if err != nil {
		return "", fmt.Errorf("summarise failed: %w", err)
	}
	_ = sess.Append(ChatMessage{Role: "assistant", Content: resp.Content})
	a.saveDCPState(chatID, dcp)
	return resp.Content, nil
}

func (a *Agent) recordStep(chatID int64, step, max int, usage *Usage, dur time.Duration, toolLines []string, textReplyLen int, annotation string) {
	if annotation != "" {
		a.recordTrace(chatID, strings.TrimSpace(annotation))
	}

	in, out, total := 0, 0, 0
	if usage != nil {
		in, out, total = usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens
	}
	secs := dur.Seconds()
	otps := 0.0
	if secs > 0 && out > 0 {
		otps = float64(out) / secs
	}
	var b strings.Builder
	fmt.Fprintf(&b, "✓ step %d/%d\nin=%d out=%d total=%d otps=%.1f\ndur=%.1fs", step, max, in, out, total, otps, secs)
	for _, line := range toolLines {
		b.WriteString("\n")
		b.WriteString(line)
	}
	if len(toolLines) == 0 && textReplyLen >= 0 {
		fmt.Fprintf(&b, "\n(text reply, %d chars)", textReplyLen)
	}
	a.recordTrace(chatID, b.String())
}

func (a *Agent) sendModelGrid(chatID int64) {
	prov, ok := a.cfg.Providers[a.cfg.Provider]
	if !ok {
		a.send(chatID, "❌ no provider selected")
		return
	}
	if len(prov.Models) == 0 {
		a.send(chatID, "no models in current provider "+a.cfg.Provider)
		return
	}
	var rows [][]InlineButton
	for name, m := range prov.Models {
		label := "• " + name
		if name == a.cfg.DefaultModel {
			label += " ✅"
		}
		if m.ContextWindow > 0 {
			label += fmt.Sprintf("  (%dk ctx)", m.ContextWindow/1000)
		}
		if len(label) > 60 {
			label = label[:60] + "…"
		}
		rows = append(rows, []InlineButton{{Text: label, CallbackData: "model:" + name}})
	}
	cw := a.getModelContextWindow()
	dcpStatus := "OFF"
	if a.cfg.DCP.Enabled {
		dcpStatus = "ON"
	}
	info := fmt.Sprintf("🤖 provider: %s\npick a model:\n\n📦 DCP: %s", a.cfg.Provider, dcpStatus)
	if cw > 0 {
		info += fmt.Sprintf("\ncontext: %dk | min 20%%: %dk | max 80%%: %dk", cw/1000, a.cfg.DCP.MinContextTokens/1000, a.cfg.DCP.MaxContextTokens/1000)
	}
	a.sendButtons(chatID, info, rows)
}

// ──────────────────────────────────────────────────────
// DCP commands
// ──────────────────────────────────────────────────────

func (a *Agent) handleDCPCommand(chatID int64, text string) {
	args := strings.TrimSpace(strings.TrimPrefix(text, "/dcp"))
	switch args {
	case "", "status":
		dcp := a.getDCPState(chatID)
		status := "OFF"
		if a.cfg.DCP.Enabled {
			status = "ON"
		}
		cw := a.getModelContextWindow()
		var b strings.Builder
		fmt.Fprintf(&b, "📦 DCP: %s\n\n", status)
		b.WriteString(dcp.Summary())
		b.WriteString(fmt.Sprintf("\ntokens now: ~%d", dcp.CurrentTokens))
		if cw > 0 {
			b.WriteString(fmt.Sprintf("\nmodel context: %dk", cw/1000))
			b.WriteString(fmt.Sprintf("\nmin (20%%): %dk", a.cfg.DCP.MinContextTokens/1000))
			b.WriteString(fmt.Sprintf("\nmax (80%%): %dk", a.cfg.DCP.MaxContextTokens/1000))
		} else {
			b.WriteString(fmt.Sprintf("\nmin: %d", a.cfg.DCP.MinContextTokens))
			b.WriteString(fmt.Sprintf("\nmax: %d", a.cfg.DCP.MaxContextTokens))
		}
		b.WriteString(fmt.Sprintf("\nturn: %d", dcp.CurrentTurn))
		if dcp.LastCompressStep > 0 {
			b.WriteString(fmt.Sprintf("\nlast compress: step %d", dcp.LastCompressStep))
		}
		a.send(chatID, b.String())
	case "on":
		a.cfg.DCP.Enabled = true
		a.updateDCPLimitsFromModel(a.getDCPState(chatID))
		a.send(chatID, "✅ DCP enabled")
	case "off":
		a.cfg.DCP.Enabled = false
		a.send(chatID, "✅ DCP disabled")
	case "reset":
		a.dcpStates[chatID] = NewDCPState()
		a.saveDCPState(chatID, a.dcpStates[chatID])
		a.send(chatID, "🔄 DCP state reset — compressed ranges cleared")
	default:
		a.send(chatID, "usage:\n/dcp — status\n/dcp on — enable\n/dcp off — disable\n/dcp reset — clear compressed ranges")
	}
}

// ──────────────────────────────────────────────────────
// RunLoop — Telegram polling + command dispatch.
// ──────────────────────────────────────────────────────

func (a *Agent) handleUpgradeResume(chatID int64) {
	version, _ := gitHead()
	err := saveResumeMarker(chatID, version)
	if err != nil {
		a.send(chatID, "failed to save resume marker: "+err.Error())
		return
	}
	a.send(chatID, "upgrading and resuming at "+version+"...")
	go func() {
		time.Sleep(500 * time.Millisecond)
		log.Println("upgrade-resume: clean exit requested")
		os.Exit(0)
	}()
}

func (a *Agent) RunLoop(ctx context.Context) error {
	if a.cfg.TelegramChatID != 0 {
		sha, _ := gitHead()
		a.send(a.cfg.TelegramChatID, "🤖 SMAGo started. /models to pick, /help for commands.\nVersion: "+sha)
	}

	pollCh := make(chan *TGUpdate, 4)
	go func() {
		defer close(pollCh)
		for {
			upd, err := a.tg.LongPoll(ctx)
			if err != nil {
				return
			}
			if upd == nil {
				continue
			}
			select {
			case pollCh <- upd:
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		var upd *TGUpdate
		var inj *injectedMsg
		select {
		case <-ctx.Done():
			return ctx.Err()
		case u, ok := <-pollCh:
			if !ok {
				return nil
			}
			upd = u
		case m := <-a.inject:
			inj = &m
		}

		if inj != nil {
			upd = &TGUpdate{
				Message: &struct {
					MessageID int64 `json:"message_id"`
					From      *struct {
						ID int64 `json:"id"`
					} `json:"from"`
					Chat struct {
						ID int64 `json:"id"`
					} `json:"chat"`
					Text string `json:"text"`
				}{Text: inj.Text, Chat: struct {
					ID int64 `json:"id"`
				}{ID: inj.ChatID}},
			}
		}

		if upd.Message == nil {
			continue
		}

		isTrusted := inj != nil && inj.trusted
		if !isTrusted && len(a.cfg.TrustedChatIDs) > 0 {
			allowed := false
			fromID := upd.Message.Chat.ID
			if upd.Message.From != nil {
				fromID = upd.Message.From.ID
			}
			for _, id := range a.cfg.TrustedChatIDs {
				if id == fromID {
					allowed = true
					break
				}
			}
			if !allowed {
				log.Printf("blocked message from chatID=%d (not in trusted list)", fromID)
				a.send(upd.Message.Chat.ID, "⛔ not authorized. your chat.id is "+fmt.Sprintf("%d", upd.Message.Chat.ID))
				continue
			}
		}

		if upd.CallbackQuery != nil {
			cq := upd.CallbackQuery
			data := cq.Data
			chatID := int64(0)
			var msgID int64
			if cq.Message != nil {
				chatID = cq.Message.Chat.ID
				msgID = cq.Message.MessageID
			}
			switch {
			case strings.HasPrefix(data, "model:"):
				name := strings.TrimPrefix(data, "model:")
				a.cfg.DefaultModel = name
				if chatID != 0 {
					a.send(chatID, "✅ model → "+name)
				}
				_ = a.tg.AnswerCallback(cq.ID, "model: "+name)
			case strings.HasPrefix(data, "rollback:"):
				version := strings.TrimPrefix(data, "rollback:")
				_ = a.tg.AnswerCallback(cq.ID, "rolling back to "+version)
				a.runRollback(chatID, msgID, version, false)
			case data == "rollback:force":
				_ = a.tg.AnswerCallback(cq.ID, "force rollback")
				a.runRollbackFromDirty(chatID, msgID)
			case strings.HasPrefix(data, "switch:"):
				name := strings.TrimPrefix(data, "switch:")
				if err := a.store.SwitchActive(chatID, name); err != nil {
					a.send(chatID, "❌ "+err.Error())
				} else {
					a.dcpStates[chatID] = NewDCPState()
					a.saveDCPState(chatID, a.dcpStates[chatID])
					a.send(chatID, "✅ session → "+name)
				}
				_ = a.tg.AnswerCallback(cq.ID, "switched")
			default:
				_ = a.tg.AnswerCallback(cq.ID, "")
			}
			continue
		}

		text := strings.TrimSpace(upd.Message.Text)
		if text == "" {
			continue
		}

		log.Printf("msg: chatID=%d text=%q", upd.Message.Chat.ID, truncateLog(text, 200))
		chatID := upd.Message.Chat.ID

		switch {
		case text == "/stop":
			if rs := a.getRun(chatID); rs != nil {
				rs.Stop()
				a.send(chatID, "⏹ *stopping after current step…*")
			} else {
				a.send(chatID, "no task in progress")
			}
			continue
		case text == "/abort":
			if rs := a.getRun(chatID); rs != nil {
				rs.Abort()
				a.send(chatID, "🛑 *aborted*")
			} else {
				a.send(chatID, "no task in progress")
			}
			continue
		}

		if rs := a.getRun(chatID); rs != nil && !isWhitelistedCommand(text) {
			a.send(chatID, "⏳ task in progress — use /stop or /abort to interrupt")
			continue
		}
		switch {

		// ── Welcome / help ────────────────────────────
		case text == "/start":
			a.send(chatID, "👋 I'm SMAGo.\n\n"+
				"Sessions:\n/sessions /new /switch /del /rename /delete\n\n"+
			"Conversation:\n/stop /abort /compress\n\n"+
				"Configuration:\n/models /model /provider /system /maxsteps /shell\n\n"+
				"Context:\n/dcp\n\n"+
				"Visibility:\n/tools /trace /verbose\n\n"+
				"Self-update:\n/version /rollback /gitsha /gitlog /gitdiff\n\n"+
				"Meta:\n/chatid /health /help")
			continue
		case text == "/help":
			a.send(chatID, buildHelpText())
			continue

		// ── DCP ──────────────────────────────────────
		case text == "/dcp" || strings.HasPrefix(text, "/dcp "):
			a.handleDCPCommand(chatID, text)
			continue

		// ── Session management ────────────────────────
		case text == "/sessions":
			a.showSessionList(chatID)
			continue
		case strings.HasPrefix(text, "/new"):
			a.handleNewSession(chatID, text)
			continue
		case strings.HasPrefix(text, "/switch"), strings.HasPrefix(text, "/sw"):
			a.handleSwitchSession(chatID, text)
			continue
		case strings.HasPrefix(text, "/rename"):
			a.handleRenameSession(chatID, text)
			continue
		case strings.HasPrefix(text, "/delete"), strings.HasPrefix(text, "/del"):
			a.handleDeleteSession(chatID, text)
			continue


		// ── Model / provider ──────────────────────────
		case text == "/models":
			a.sendModelGrid(chatID)
			continue
		case text == "/model" || strings.HasPrefix(text, "/model "):
			args := strings.TrimSpace(strings.TrimPrefix(text, "/model"))
			if args == "" {
				a.send(chatID, "current model: "+a.cfg.DefaultModel)
			} else {
				a.cfg.DefaultModel = args
				a.send(chatID, "✅ model → "+args)
			}
			continue
		case text == "/provider" || strings.HasPrefix(text, "/provider "):
			args := strings.TrimSpace(strings.TrimPrefix(text, "/provider"))
			if args == "" {
				var b strings.Builder
				b.WriteString("provider: " + a.cfg.Provider + "\navailable:\n")
				for name := range a.cfg.Providers {
					b.WriteString("  • " + name + "\n")
				}
				a.send(chatID, b.String())
			} else if _, ok := a.cfg.Providers[args]; ok {
				a.cfg.Provider = args
				a.send(chatID, "✅ provider → "+args)
			} else {
				a.send(chatID, "❌ unknown provider: "+args)
			}
			continue
		case text == "/system" || strings.HasPrefix(text, "/system "):
			args := strings.TrimSpace(strings.TrimPrefix(text, "/system"))
			if args == "" {
				preview := a.cfg.SystemPrompt
				if len(preview) > 1500 {
					preview = preview[:1500] + "…"
				}
				a.send(chatID, "system prompt:\n\n"+preview)
			} else {
				a.cfg.SystemPrompt = args
				a.send(chatID, fmt.Sprintf("✅ system prompt updated (%d chars)", len(args)))
			}
			continue
		case text == "/maxsteps" || strings.HasPrefix(text, "/maxsteps "):
			args := strings.TrimSpace(strings.TrimPrefix(text, "/maxsteps"))
			if args == "" {
				cur := defaultMaxSteps
				if v, ok := a.maxSteps[chatID]; ok {
					cur = v
				}
				a.send(chatID, fmt.Sprintf("max steps: %d (default %d)", cur, defaultMaxSteps))
				continue
			}
			n, err := strconv.Atoi(args)
			if err != nil || n < 1 {
				a.send(chatID, "usage: /maxsteps [1-1000]")
				continue
			}
			a.maxSteps[chatID] = n
			a.send(chatID, fmt.Sprintf("✅ max steps → %d", n))
			continue

		// ── Shell ──────────────────────────────────────
		case text == "/shell" || strings.HasPrefix(text, "/shell "):
			args := strings.TrimSpace(strings.TrimPrefix(text, "/shell"))
			switch {
			case args == "" || args == "current":
				cur := a.getEffectiveShell(chatID)
				a.send(chatID, fmt.Sprintf("🖥 shell: %s\n\navailable:\n%s", cur, FormatShellList(ProbedShells, cur)))
			case args == "list":
				a.send(chatID, "available shells:\n"+FormatShellList(ProbedShells, -1))
			case strings.HasPrefix(args, "set "):
				name := strings.TrimSpace(strings.TrimPrefix(args, "set "))
				s, ok := ParseShellType(name)
				if !ok {
					a.send(chatID, "❌ unknown shell: "+name+"\navailable: "+strings.Join(ShellNames(ProbedShells), ", "))
					continue
				}
				found := false
				for _, ps := range ProbedShells {
					if ps == s {
						found = true
						break
					}
				}
				if !found {
					a.send(chatID, "❌ shell not detected on this system: "+name)
					continue
				}
				a.shellOverride[chatID] = s
				a.send(chatID, "✅ shell → "+name)
			case args == "reset":
				delete(a.shellOverride, chatID)
				a.send(chatID, "✅ shell reset to default ("+a.cfg.DefaultShell+")")
			default:
				a.send(chatID, "usage: /shell [current|list|set <name>|reset]")
			}
			continue

		// ── Visibility ────────────────────────────────
		case text == "/tools":
			var b strings.Builder
			b.WriteString("🛠 Available tools:\n")
			for _, t := range a.tools.All() {
				b.WriteString("• " + t.Name + " — " + t.Description + "\n")
			}
			a.send(chatID, b.String())
			continue
		case text == "/trace" || text == "/debug":
			buf := a.traceBuf[chatID]
			if len(buf) == 0 {
				a.send(chatID, "no agent activity yet")
				continue
			}
			var b strings.Builder
			fmt.Fprintf(&b, "🪛 last %d agent actions:\n\n", len(buf))
			for _, line := range buf {
				b.WriteString(line + "\n")
			}
			a.sendPlain(chatID, b.String())
			continue
		case text == "/verbose":
			a.verbose = !a.verbose
			if a.verbose {
				a.send(chatID, "✅ verbose ON — tool annotations shown inline")
			} else {
				a.send(chatID, "✅ verbose OFF — traces hidden, use /trace")
			}
			continue

		// ── Meta ──────────────────────────────────────
		case text == "/version":
			sha, _ := gitHead()
			exe, _ := os.Executable()
			info, _ := os.Stat(exe)
			var sizeStr string
			if info != nil {
				sizeStr = fmt.Sprintf("%.1f MB", float64(info.Size())/1024/1024)
			}
			uptime := time.Since(startedAt)
			a.send(chatID, fmt.Sprintf("git: %s\nbinary: %s (%s)\npid: %d\nuptime: %s",
				sha, exe, sizeStr, os.Getpid(), uptime.Truncate(time.Second)))
			continue
		case text == "/restart":
			a.send(chatID, "🔄 restarting — supervisor will bring me back in a moment")
			go func() {
				time.Sleep(500 * time.Millisecond)
				log.Println("restart: clean exit requested by user")
				os.Exit(0)
			}()
			continue

		case text == "/upgrade-resume" || text == "/resume":
		a.handleUpgradeResume(chatID)
		continue
		case text == "/health":
			a.send(chatID, "✅ ok")
			continue
		case text == "/chatid":
			a.send(chatID, fmt.Sprintf("chat.id = %d", chatID))
			continue

		// ── Git ───────────────────────────────────────
		case text == "/gitsha" || text == "/githead":
			sha, err := gitHead()
			if err != nil {
				a.send(chatID, "❌ git: "+err.Error())
			} else {
				a.send(chatID, "🔖 HEAD: "+sha)
			}
			continue
		case text == "/gitlog" || strings.HasPrefix(text, "/gitlog "):
			args := strings.TrimSpace(strings.TrimPrefix(text, "/gitlog"))
			n := 10
			if args != "" {
				fmt.Sscanf(args, "%d", &n)
			}
			if n < 1 || n > 50 {
				n = 10
			}
			out, err := gitLog(n)
			if err != nil {
				a.send(chatID, "❌ git log: "+err.Error())
			} else if out == "" {
				a.send(chatID, "no commits yet")
			} else {
				a.sendPlain(chatID, "📜 last "+fmt.Sprintf("%d", n)+" commits:\n\n"+out)
			}
			continue
		case text == "/gitdiff" || strings.HasPrefix(text, "/gitdiff "):
			args := strings.TrimSpace(strings.TrimPrefix(text, "/gitdiff"))
			out, err := gitDiff(args)
			if err != nil {
				a.send(chatID, "❌ git diff: "+err.Error())
			} else if out == "" {
				a.send(chatID, "no diff")
			} else {
				a.sendPlain(chatID, "📊 diff "+args+":\n\n"+truncateLog(out, 3500))
			}
			continue

		// ── Rollback ──────────────────────────────────
		case text == "/rollback":
			a.showRollbackMenu(chatID)
			continue
		case text == "/list-versions" || text == "/versions":
			versions, err := listVersions()
			if err != nil {
				a.send(chatID, "❌ "+err.Error())
				continue
			}
			if len(versions) == 0 {
				a.send(chatID, "no versions on disk")
				continue
			}
			var b strings.Builder
			fmt.Fprintf(&b, "📦 %d version(s):\n", len(versions))
			for _, v := range versions {
				marker := ""
				if v.IsCurrent {
					marker = "  ← current"
				}
				fmt.Fprintf(&b, "  %s  %s  %s%s\n", v.Version, v.ShortSHA, v.BuiltAt.Format("2006-01-02 15:04"), marker)
			}
			a.send(chatID, strings.TrimRight(b.String(), "\n"))
			continue

		// ── Fallback ──────────────────────────────────
		case strings.HasPrefix(text, "/") && text != "/compress":
			a.send(chatID, "unknown command: "+text+"\ntype /help")
			continue
		}

		a.typing(chatID)
		go func(cid int64, msg string) {
			reply, err := a.Handle(cid, msg)
			if err != nil {
				log.Printf("err: chatID=%d %v", cid, err)
				a.send(cid, "❌ "+err.Error())
				return
			}
			log.Printf("reply: chatID=%d %q", cid, truncateLog(reply, 200))
			a.send(cid, reply)
		}(chatID, text)
	}
}

// ──────────────────────────────────────────────────────
// Session commands
// ──────────────────────────────────────────────────────

func (a *Agent) showSessionList(chatID int64) {
	sessions, err := a.store.ListSessions(chatID)
	if err != nil {
		a.send(chatID, "❌ "+err.Error())
		return
	}
	if len(sessions) == 0 {
		a.send(chatID, "no sessions yet — /new to create one")
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "📂 %d session(s):\n\n", len(sessions))
	for _, s := range sessions {
		marker := "  "
		if s.Active {
			marker = "✅"
		}
		age := humanAge(s.UpdatedAt)
		fmt.Fprintf(&b, "%s %s — %d msgs, %s\n", marker, s.Name, s.Messages, age)
	}
	b.WriteString("\ntap a session to switch:")

	var rows [][]InlineButton
	for _, s := range sessions {
		label := s.Name
		if s.Active {
			label += " ✅"
		}
		if s.Messages > 0 {
			label += fmt.Sprintf(" (%d)", s.Messages)
		}
		if len(label) > 40 {
			label = label[:40] + "…"
		}
		cb := "switch:" + s.Name
		if s.Active {
			cb = "noop"
		}
		rows = append(rows, []InlineButton{{Text: label, CallbackData: cb}})
	}
	a.sendButtons(chatID, b.String(), rows)
}

func (a *Agent) handleNewSession(chatID int64, text string) {
	name := strings.TrimSpace(strings.TrimPrefix(text, "/new"))
	if name == "" {
		sessions, _ := a.store.ListSessions(chatID)
		n := len(sessions) + 1
		for {
			candidate := fmt.Sprintf("new-%d", n)
			found := false
			for _, s := range sessions {
				if s.Name == candidate {
					found = true
					break
				}
			}
			if !found {
				name = candidate
				break
			}
			n++
		}
	}

	sess, err := a.store.LoadOrCreate(chatID, name)
	if err != nil {
		a.send(chatID, "❌ "+err.Error())
		return
	}
	_ = a.store.SwitchActive(chatID, name)
	_ = sess
	a.dcpStates[chatID] = NewDCPState()
	a.saveDCPState(chatID, a.dcpStates[chatID])
	a.send(chatID, fmt.Sprintf("🆕 new session: %s\n(active)", name))
}

func (a *Agent) handleSwitchSession(chatID int64, text string) {
	name := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(text, "/sw"), "/switch"))
	if name == "" {
		a.showSessionList(chatID)
		return
	}
	if err := a.store.SwitchActive(chatID, name); err != nil {
		a.send(chatID, "❌ "+err.Error())
		return
	}
	a.dcpStates[chatID] = NewDCPState()
	a.saveDCPState(chatID, a.dcpStates[chatID])
	sess, _ := a.store.GetActive(chatID)
	a.send(chatID, fmt.Sprintf("✅ switched to: %s\n(%d messages)", name, sess.Len()))
}

func (a *Agent) handleRenameSession(chatID int64, text string) {
	newName := strings.TrimSpace(strings.TrimPrefix(text, "/rename"))
	if newName == "" {
		// Ask LLM to generate a session name from conversation context
		sess, err := a.store.GetActive(chatID)
		if err != nil {
			a.send(chatID, "❌ no active session: "+err.Error())
			return
		}
		msgs := sess.Messages()
		if len(msgs) == 0 {
			a.send(chatID, "❌ session is empty — nothing to base a name on")
			return
		}
		a.typing(chatID)
		prompt := []ChatMessage{
			{Role: "system", Content: "You are a naming assistant. Given the first few messages of a conversation, suggest a short, lowercase, hyphenated session name (2-4 words, e.g. 'bug-fix-503-retry' or 'tomsk-bus-routes'). Reply with ONLY the name, nothing else."},
		}
		n := len(msgs)
		if n > 5 { n = 5 }
		prompt = append(prompt, msgs[:n]...)
		resp, _, llmErr := a.llm.Chat(prompt, nil)
		if llmErr != nil {
			a.send(chatID, "❌ failed to generate name: "+llmErr.Error())
			return
		}
		newName = strings.TrimSpace(resp.Content)
		newName = strings.ToLower(newName)
		newName = strings.ReplaceAll(newName, " ", "-")
		newName = strings.Trim(newName, "\x22\x27")
		if len(newName) > 40 { newName = newName[:40] }
		if newName == "" {
			a.send(chatID, "❌ LLM returned empty name")
			return
		}
	}
	// Rename active session to the given name
	sess, err := a.store.GetActive(chatID)
	if err != nil {
		a.send(chatID, "❌ no active session: "+err.Error())
		return
	}
	oldName := sess.Name()
	if err := a.store.RenameSession(chatID, oldName, newName); err != nil {
		a.send(chatID, "❌ "+err.Error())
		return
	}
	a.send(chatID, fmt.Sprintf("✅ renamed: %s → %s", oldName, newName))
}

func (a *Agent) handleDeleteSession(chatID int64, text string) {
	// Strip both /delete and /del prefix
	name := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(text, "/del"), "/delete"))
	if name == "" {
		sess, err := a.store.GetActive(chatID)
		if err != nil {
			a.send(chatID, "❌ no active session: "+err.Error())
			return
		}
		name = sess.Name()
	}
	if err := a.store.DeleteSession(chatID, name); err != nil {
		a.send(chatID, "❌ "+err.Error())
		return
	}
	a.dcpStates[chatID] = NewDCPState()
	a.saveDCPState(chatID, a.dcpStates[chatID])
	a.send(chatID, fmt.Sprintf("🗑 deleted session: %s", name))
}

// ──────────────────────────────────────────────────────
// Rollback UI
// ──────────────────────────────────────────────────────

func (a *Agent) showRollbackMenu(chatID int64) {
	versions, err := listVersions()
	if err != nil {
		a.send(chatID, "❌ "+err.Error())
		return
	}
	if len(versions) == 0 {
		a.send(chatID, "no versions on disk")
		return
	}
	var rows [][]InlineButton
	var b strings.Builder
	b.WriteString("⏪ pick a version:\n")
	for _, v := range versions {
		marker := ""
		if v.IsCurrent {
			marker = " ✅"
		}
		label := fmt.Sprintf("%s %s (%s)%s", v.Version, v.ShortSHA, humanAge(v.BuiltAt), marker)
		if len(label) > 60 {
			label = label[:60] + "…"
		}
		rows = append(rows, []InlineButton{{Text: label, CallbackData: "rollback:" + v.Version}})
	}
	a.sendButtons(chatID, b.String(), rows)
}

func (a *Agent) runRollback(chatID, msgID int64, version string, force bool) {
	if !force {
		dirty, err := gitTrackedDirty()
		if err != nil {
			a.send(chatID, "❌ "+err.Error())
			return
		}
		if len(dirty) > 0 {
			preview := strings.Join(dirty, "\n")
			if len(preview) > 500 {
				preview = preview[:500] + "…"
			}
			rows := [][]InlineButton{{{Text: "⚠️ Force", CallbackData: "rollback:force"}}}
			_ = a.tg.EditMessageText(chatID, msgID, "⏪ uncommitted changes:\n\n"+preview+"\n\nCommit/stash or tap Force.", rows)
			a.pendingForceVersion = version
			return
		}
	}
	a.executeRollback(chatID, msgID, version, force)
}

func (a *Agent) runRollbackFromDirty(chatID, msgID int64) {
	v := a.pendingForceVersion
	a.pendingForceVersion = ""
	if v == "" {
		a.send(chatID, "❌ lost version — try /rollback again")
		return
	}
	a.executeRollback(chatID, msgID, v, true)
}

func (a *Agent) executeRollback(chatID, msgID int64, version string, force bool) {
	a.send(chatID, "⏪ rolling back to "+version+"…")
	go func() {
		out, err := runSelfRollback(version, force)
		if err != nil {
			a.tg.EditMessageText(chatID, msgID, "❌ rollback failed: "+err.Error()+"\n\n"+truncateLog(out, 1500), nil)
			return
		}
		a.tg.EditMessageText(chatID, msgID, "✅ rollback "+version+" sent to supervisor\n\n"+truncateLog(out, 1000), nil)
	}()
}

func humanAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}
