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

// defaultMaxSteps caps the tool-call iterations per user message. If
// the model can't finish within this many steps it gets a "summarise
// what you have" prompt and the existing context.
const defaultMaxSteps = 200

// startedAt is the time the agent process started — used by /version to
// report uptime. Set in main before RunLoop.
var startedAt = time.Now()

// runState is the per-chat handle on the currently-running Handle() call.
// It carries:
//   - a cancellable context (so /abort can kill in-flight tools)
//   - a stop channel (so /stop can request a graceful exit between steps)
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
	maxSteps map[int64]int      // per-chat override
	verbose  bool               // send tool-call traces inline in Telegram
	traceBuf map[int64][]string // last 20 tool-call lines per chat (for /trace)

	runMu sync.Mutex
	runs  map[int64]*runState

	// Stash for the /rollback "force" flow — when the user taps a version
	// while the working tree is dirty, we remember which version they meant
	// so the subsequent "Force" tap knows what to roll back to.
	pendingForceVersion string
}

// injectedMsg is a message pushed into the agent loop from outside Telegram.
type injectedMsg struct {
	ChatID  int64
	Text    string
	trusted bool // injected messages skip the trusted-id check
}

func NewAgent(cfg *Config, llm *LLM, store *Store, tg *Telegram, tools *ToolRegistry) *Agent {
	return &Agent{
		cfg:      cfg,
		llm:      llm,
		store:    store,
		tg:       tg,
		tools:    tools,
		inject:   make(chan injectedMsg, 16),
		maxSteps: make(map[int64]int),
		traceBuf: make(map[int64][]string),
		runs:     make(map[int64]*runState),
		verbose:  true, // send tool-call traces inline by default
	}
}

// getRun returns the runState for chatID, or nil if no run is active.
func (a *Agent) getRun(chatID int64) *runState {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	return a.runs[chatID]
}

// registerRun stores rs in the per-chat slot and returns a func that
// removes it. Use as `defer a.registerRun(chatID, rs)()` in Handle.
func (a *Agent) registerRun(chatID int64, rs *runState) func() {
	a.runMu.Lock()
	a.runs[chatID] = rs
	a.runMu.Unlock()
	return func() {
		a.runMu.Lock()
		// Only delete the slot if it's still ours — a concurrent
		// /stop+restart could have replaced it.
		if a.runs[chatID] == rs {
			delete(a.runs, chatID)
		}
		a.runMu.Unlock()
	}
}

// recordTrace appends one line to the per-chat trace buffer and
// returns it. If verbose is on, the same line is also sent to the chat.
func (a *Agent) recordTrace(chatID int64, line string) string {
	a.traceBuf[chatID] = append(a.traceBuf[chatID], line)
	if len(a.traceBuf[chatID]) > 20 {
		a.traceBuf[chatID] = a.traceBuf[chatID][len(a.traceBuf[chatID])-20:]
	}
	if a.verbose {
		a.sendPlain(chatID, line)
	}
	return line
}

// Push submits a synthetic message as if it came from Telegram.
// Injected messages bypass the trusted-ChatID gate so local debug
// tools and HTTP /inject can drive the agent without one.
func (a *Agent) Push(chatID int64, text string) error {
	select {
	case a.inject <- injectedMsg{ChatID: chatID, Text: text, trusted: true}:
		return nil
	default:
		return fmt.Errorf("inject channel full")
	}
}

// SetRecorder wires a callback that gets every outgoing Telegram message.
// Used by the debug HTTP /mirror endpoint.
func (a *Agent) SetRecorder(fn func(chatID int64, text string)) {
	a.record = fn
}

// send mirrors a Telegram send to the local /mirror endpoint.
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

// sendPlain sends raw text without HTML conversion (for code, logs, diffs).
func (a *Agent) sendPlain(chatID int64, text string) {
	_ = a.tg.SendPlain(chatID, text)
	if a.record != nil {
		a.record(chatID, text)
	}
}

// trace sends a short "agent is doing X" message to the chat. It is
// best-effort and never returns an error. The flag `a.verbose` controls
// whether the user sees tool calls inline or only the final answer.
func (a *Agent) trace(chatID int64, text string) {
	if !a.verbose {
		return
	}
	a.sendPlain(chatID, text)
}

// typing sends the "typing..." indicator to the chat. Fire-and-forget.
func (a *Agent) typing(chatID int64) {
	_ = a.tg.Typing(chatID)
}

func truncateLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func (a *Agent) Handle(chatID int64, userText string) (string, error) {
	sess, err := a.store.LoadOrCreate(chatID)
	if err != nil {
		return "", err
	}

	if err := sess.Append(ChatMessage{Role: "user", Content: userText}); err != nil {
		return "", err
	}

	messages := []ChatMessage{
		{Role: "system", Content: a.cfg.SystemPrompt},
	}
	messages = append(messages, sess.Messages()...)

	tools := a.tools.AsLLMTools()

	maxSteps := defaultMaxSteps
	if v, ok := a.maxSteps[chatID]; ok {
		maxSteps = v
	}

	// Register a per-chat run state so /stop and /abort can reach us.
	runCtx, runCancel := context.WithCancel(context.Background())
	rs := &runState{ctx: runCtx, cancel: runCancel, stop: make(chan struct{})}
	cleanup := a.registerRun(chatID, rs)
	defer cleanup()

	a.recordTrace(chatID, fmt.Sprintf("→ %s\nmodel=%s\nmax=%d\ntools=%d",
		truncateLog(userText, 100), a.cfg.DefaultModel, maxSteps, len(tools)))

	for i := 0; i < maxSteps; i++ {
		// /stop check at the top of every step.
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

		// Show typing indicator before each LLM call (expires after ~5s, so refresh each step)
		a.typing(chatID)

		stepStart := time.Now()
		resp, usage, err := a.llm.Chat(messages, tools)
		stepDur := time.Since(stepStart)
		if err != nil {
			a.recordTrace(chatID, fmt.Sprintf("  ✗ LLM error: %v", err))
			return "", err
		}

		// Collect tool result lines so we can emit one multi-line trace
		// message at the end of the step instead of one message per line.
		var toolLines []string

		if len(resp.ToolCalls) == 0 {
			a.recordStep(chatID, i+1, maxSteps, a.cfg.DefaultModel, usage, stepDur,
				nil, len(resp.Content))
			_ = sess.Append(ChatMessage{Role: "assistant", Content: resp.Content})
			return resp.Content, nil
		}

		_ = sess.Append(ChatMessage{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})

		for _, tc := range resp.ToolCalls {
			// Show typing again while executing each tool
			a.typing(chatID)

			tdef, ok := a.tools.Get(tc.Function.Name)
			if !ok {
				toolLines = append(toolLines, fmt.Sprintf("  ✗ %s: unknown tool", tc.Function.Name))
				_ = sess.Append(ChatMessage{
					Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name,
					Content: "error: unknown tool \"" + tc.Function.Name + "\"",
				})
				continue
			}
			var args map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				toolLines = append(toolLines, fmt.Sprintf("  ✗ %s: bad args: %v", tc.Function.Name, err))
				_ = sess.Append(ChatMessage{
					Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name,
					Content: "error: bad arguments: " + err.Error(),
				})
				continue
			}
			argStr := truncateLog(fmt.Sprintf("%v", args), 150)
			out, err := tdef.Execute(runCtx, args)
			if err != nil {
				// /abort returns a context.Canceled error from the tool.
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					// Emit the step trace first so the user sees the tools
					// that ran, then bail.
					toolLines = append(toolLines, fmt.Sprintf("  🛑 %s(%s) cancelled", tc.Function.Name, argStr))
					a.recordStep(chatID, i+1, maxSteps, a.cfg.DefaultModel, usage, stepDur, toolLines, -1)
					return "🛑 aborted.", nil
				}
				toolLines = append(toolLines, fmt.Sprintf("  ✗ %s(%s) → error: %v", tc.Function.Name, argStr, err))
				out = "error: " + err.Error()
			} else {
				toolLines = append(toolLines, fmt.Sprintf("  ✓ %s(%s) → %d chars", tc.Function.Name, argStr, len(out)))
			}
			_ = sess.Append(ChatMessage{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: out})
			messages = append(messages,
				ChatMessage{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls},
				ChatMessage{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: out},
			)
		}
		// One trace message per step, with all tool lines baked in.
		a.recordStep(chatID, i+1, maxSteps, a.cfg.DefaultModel, usage, stepDur, toolLines, -1)
	}
	// Hit the step cap. Ask the model for a best-effort text answer.
	a.recordTrace(chatID, fmt.Sprintf("✗ hit %d-step cap, asking for best-effort text", maxSteps))
	messages = append(messages, ChatMessage{
		Role:    "system",
		Content: "Tool-call budget exhausted. Summarise what you have so far in plain text and answer the user.",
	})
	resp, _, err := a.llm.Chat(messages, nil)
	if err != nil {
		return "", fmt.Errorf("agent loop exceeded %d steps and final summarise failed: %w", maxSteps, err)
	}
	_ = sess.Append(ChatMessage{Role: "assistant", Content: resp.Content})
	return resp.Content, nil
}

// recordStep emits the per-step trace as a single multi-line Telegram
// message. The header (step, model, tokens, rates, duration) is followed
// by one line per tool call (or a single "(text reply, N chars)" line if
// the model returned no tool calls).
//
// All numeric fields are best-effort — providers that don't return usage
// just show 0.
//
// textReplyLen is used only when toolLines is empty; pass -1 when there
// were tool calls (otherwise the "(text reply, N chars)" line is appended
// after the tool lines, which is not what you want).
func (a *Agent) recordStep(chatID int64, step, max int, model string, usage *Usage, dur time.Duration, toolLines []string, textReplyLen int) {
	in, out, total := 0, 0, 0
	if usage != nil {
		in, out, total = usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens
	}
	secs := dur.Seconds()
	tps := 0.0
	if secs > 0 && total > 0 {
		tps = float64(total) / secs
	}
	itps := 0.0
	if secs > 0 && in > 0 {
		itps = float64(in) / secs
	}
	otps := 0.0
	if secs > 0 && out > 0 {
		otps = float64(out) / secs
	}
	var b strings.Builder
	fmt.Fprintf(&b, "✓ step %d/%d\n", step, max)
	fmt.Fprintf(&b, "model=%s\n", model)
	fmt.Fprintf(&b, "in=%d out=%d total=%d\n", in, out, total)
	fmt.Fprintf(&b, "tps=%.1f itps=%.1f otps=%.1f\n", tps, itps, otps)
	fmt.Fprintf(&b, "dur=%.1fs", secs)
	if len(toolLines) > 0 {
		for _, line := range toolLines {
			b.WriteString("\n")
			b.WriteString(line)
		}
	} else if textReplyLen >= 0 {
		fmt.Fprintf(&b, "\n(text reply, %d chars)", textReplyLen)
	}
	a.recordTrace(chatID, b.String())
}

// sendModelGrid sends a message with one inline button per available model.
// Each button carries a callback_data like "model:deepseek-reasoner".
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
	currentLabel := " ✅"
	for name, m := range prov.Models {
		label := "• " + name
		if name == a.cfg.DefaultModel {
			label += currentLabel
		}
		if m.Name != "" {
			label += "  " + m.Name
		}
		if len(label) > 60 {
			label = label[:60] + "…"
		}
		rows = append(rows, []InlineButton{{
			Text:         label,
			CallbackData: "model:" + name,
		}})
	}
	header := fmt.Sprintf("🤖 provider: %s\npick a model:", a.cfg.Provider)
	a.sendButtons(chatID, header, rows)
}

func (a *Agent) RunLoop(ctx context.Context) error {
	if a.cfg.TelegramChatID != 0 {
		a.send(a.cfg.TelegramChatID, "🤖 SMAGo started. /models to pick, /help for commands.")
	}

	// Drain Telegram long-poll in a background goroutine; results land in pollCh.
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

		// Convert injected message into a TGUpdate-shaped value to share handling.
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

		// Trusted-ChatID gate. Empty list = open (backwards compat).
		// Injected messages always pass.
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

		// Inline button tap (callback_query).
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

		switch {
		case text == "/start":
			a.send(upd.Message.Chat.ID,
				"👋 I'm SMAGo.\n\n"+
					"Conversation:\n"+
					"/new — start a fresh session\n"+
					"/clear — wipe session history\n"+
					"/stop — stop after the current step (graceful)\n"+
					"/abort — kill the current tool and stop (forceful)\n\n"+
					"Configuration:\n"+
					"/models — pick a model (inline buttons)\n"+
					"/model [name] — show or set the model\n"+
					"/provider [name] — show or set the provider\n"+
					"/system [text] — show or set the system prompt\n"+
					"/maxsteps [N] — tool-call budget (default 200)\n\n"+
					"Visibility:\n"+
					"/tools — list available tools\n"+
					"/trace — show last 20 agent actions\n"+
					"/verbose — toggle inline tool-call traces\n\n"+
					"Self-update:\n"+
					"/version — show build version\n"+
					"/upgrade vN — build and swap to vN\n"+
					"/rollback — pick a previous version to roll back to\n"+
					"/gitsha /gitlog /gitdiff — git plumbing\n\n"+
					"Meta:\n"+
					"/chatid — show this chat's id\n"+
					"/health — liveness ping\n"+
					"/help — short command list")
			continue
		case text == "/help":
			a.send(upd.Message.Chat.ID,
				"/start /help /new /clear /stop /abort\n"+
					"/models /model /provider /system /maxsteps\n"+
					"/tools /trace /verbose\n"+
					"/version /upgrade /rollback /gitsha /gitlog /gitdiff\n"+
					"/chatid /health")
			continue
		case text == "/models":
			a.sendModelGrid(upd.Message.Chat.ID)
			continue
		case text == "/clear":
			sess, _ := a.store.LoadOrCreate(upd.Message.Chat.ID)
			_ = sess.Truncate(0)
			a.send(upd.Message.Chat.ID, "🗑 session cleared")
			continue
		case text == "/new":
			sess, _ := a.store.LoadOrCreate(upd.Message.Chat.ID)
			_ = sess.Truncate(0)
			a.send(upd.Message.Chat.ID, "🆕 new session — send your first message")
			continue
		case text == "/stop":
			if rs := a.getRun(upd.Message.Chat.ID); rs != nil {
				rs.Stop()
				a.send(upd.Message.Chat.ID, "⏹ <i>stopping after current step…</i>")
			} else {
				a.send(upd.Message.Chat.ID, "no task in progress")
			}
			continue
		case text == "/abort":
			if rs := a.getRun(upd.Message.Chat.ID); rs != nil {
				rs.Abort()
				a.send(upd.Message.Chat.ID, "🛑 <i>aborted</i>")
			} else {
				a.send(upd.Message.Chat.ID, "no task in progress")
			}
			continue
		case text == "/rollback":
			a.showRollbackMenu(upd.Message.Chat.ID)
			continue
		case text == "/list-versions" || text == "/versions":
			versions, err := listVersions()
			if err != nil {
				a.send(upd.Message.Chat.ID, "❌ list: "+err.Error())
				continue
			}
			if len(versions) == 0 {
				a.send(upd.Message.Chat.ID, "no versions on disk (data/versions/ is empty)")
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
			a.send(upd.Message.Chat.ID, strings.TrimRight(b.String(), "\n"))
			continue
		case text == "/tools":
			var b strings.Builder
			b.WriteString("🛠 Available tools:\n")
			for _, t := range a.tools.All() {
				b.WriteString("• " + t.Name + " — " + t.Description + "\n")
			}
			a.send(upd.Message.Chat.ID, b.String())
			continue
		case text == "/health":
			a.send(upd.Message.Chat.ID, "✅ ok")
			continue
		case text == "/chatid":
			a.send(upd.Message.Chat.ID, fmt.Sprintf("chat.id = %d", upd.Message.Chat.ID))
			continue
		case text == "/model" || strings.HasPrefix(text, "/model "):
			args := strings.TrimPrefix(text, "/model")
			args = strings.TrimSpace(args)
			if args == "" {
				a.send(upd.Message.Chat.ID, "current model: "+a.cfg.DefaultModel)
			} else {
				if _, ok := a.cfg.Providers[a.cfg.Provider]; ok {
					if _, hasModel := a.cfg.Providers[a.cfg.Provider].Models[args]; hasModel || args != "" {
						a.cfg.DefaultModel = args
						a.send(upd.Message.Chat.ID, "✅ model set to "+args)
					} else {
						a.send(upd.Message.Chat.ID, "⚠️ model not in provider catalog, set anyway: "+args)
						a.cfg.DefaultModel = args
					}
				} else {
					a.send(upd.Message.Chat.ID, "❌ no provider selected")
				}
			}
			continue
		case text == "/provider" || strings.HasPrefix(text, "/provider "):
			args := strings.TrimPrefix(text, "/provider")
			args = strings.TrimSpace(args)
			if args == "" {
				var b strings.Builder
				b.WriteString("current provider: " + a.cfg.Provider + "\navailable:\n")
				for name := range a.cfg.Providers {
					b.WriteString("  • " + name + "\n")
				}
				a.send(upd.Message.Chat.ID, b.String())
			} else {
				if _, ok := a.cfg.Providers[args]; ok {
					a.cfg.Provider = args
					a.send(upd.Message.Chat.ID, "✅ provider set to "+args)
				} else {
					a.send(upd.Message.Chat.ID, "❌ unknown provider: "+args)
				}
			}
			continue
		case text == "/system" || strings.HasPrefix(text, "/system "):
			args := strings.TrimPrefix(text, "/system")
			args = strings.TrimSpace(args)
			if args == "" {
				preview := a.cfg.SystemPrompt
				if len(preview) > 1500 {
					preview = preview[:1500] + "…"
				}
				a.send(upd.Message.Chat.ID, "current system prompt:\n\n"+preview)
			} else {
				a.cfg.SystemPrompt = args
				a.send(upd.Message.Chat.ID, "✅ system prompt updated ("+fmt.Sprintf("%d", len(args))+" chars)")
			}
			continue
		case text == "/upgrade" || strings.HasPrefix(text, "/upgrade "):
			args := strings.TrimPrefix(text, "/upgrade")
			args = strings.TrimSpace(args)
			if args == "" {
				a.send(upd.Message.Chat.ID, "usage: /upgrade vN\ncurrent: "+flagValue("--smago-version"))
				continue
			}
			a.send(upd.Message.Chat.ID, "🔨 building "+args+"...")
			go func(v string) {
				out, err := runSelfUpgrade(v)
				if err != nil {
					a.send(upd.Message.Chat.ID, "❌ upgrade failed: "+err.Error()+"\n\n"+truncateLog(out, 1500))
					return
				}
				a.send(upd.Message.Chat.ID, "✅ upgrade "+v+" sent to supervisor\n\n"+truncateLog(out, 1000))
			}(args)
			continue
		case text == "/version":
			buildVer := flagValue("--smago-version")
			if buildVer == "" {
				buildVer = readCurrentVersion()
			}
			sha, _ := gitHead()
			exe, _ := os.Executable()
			info, _ := os.Stat(exe)
			var sizeStr string
			if info != nil {
				sizeStr = fmt.Sprintf("%.1f MB", float64(info.Size())/1024/1024)
			}
			pid := os.Getpid()
			uptime := time.Since(startedAt)
			a.send(upd.Message.Chat.ID, fmt.Sprintf(
				"smago %s\nbuild: %s\ngit: %s\nbinary: %s (%s)\npid: %d\nuptime: %s",
				version, buildVer, sha, exe, sizeStr, pid, uptime.Truncate(time.Second)))
			continue
		case text == "/restart":
			a.send(upd.Message.Chat.ID, "🔄 restarting — supervisor will bring me back in a moment")
			go func() {
				time.Sleep(500 * time.Millisecond) // let the message flush
				log.Println("restart: clean exit requested by user")
				os.Exit(0)
			}()
			continue
		case text == "/trace" || text == "/debug":
			buf := a.traceBuf[upd.Message.Chat.ID]
			if len(buf) == 0 {
				a.send(upd.Message.Chat.ID, "no agent activity yet — send any message first")
				continue
			}
			var b strings.Builder
			b.WriteString(fmt.Sprintf("🪛 last %d agent actions:\n\n", len(buf)))
			for _, line := range buf {
				b.WriteString(line + "\n")
			}
			a.sendPlain(upd.Message.Chat.ID, b.String())
			continue
		case text == "/verbose" || text == "/quiet":
			a.verbose = !a.verbose
			if a.verbose {
				a.send(upd.Message.Chat.ID, "✅ verbose ON — tool calls will be shown inline")
			} else {
				a.send(upd.Message.Chat.ID, "✅ verbose OFF — only final answers")
			}
			continue
		case text == "/maxsteps" || strings.HasPrefix(text, "/maxsteps "):
			args := strings.TrimPrefix(text, "/maxsteps")
			args = strings.TrimSpace(args)
			if args == "" {
				cur := defaultMaxSteps
				if v, ok := a.maxSteps[upd.Message.Chat.ID]; ok {
					cur = v
				}
				a.send(upd.Message.Chat.ID, fmt.Sprintf("max steps: %d (default %d)", cur, defaultMaxSteps))
				continue
			}
			n, err := strconv.Atoi(args)
			if err != nil || n < 1 {
				a.send(upd.Message.Chat.ID, "usage: /maxsteps [1-1000]")
				continue
			}
			a.maxSteps[upd.Message.Chat.ID] = n
			a.send(upd.Message.Chat.ID, fmt.Sprintf("✅ max steps set to %d for this chat", n))
			continue
		case text == "/gitsha" || text == "/githead":
			sha, err := gitHead()
			if err != nil {
				a.send(upd.Message.Chat.ID, "❌ git: "+err.Error())
			} else {
				a.send(upd.Message.Chat.ID, "🔖 HEAD: "+sha)
			}
			continue
		case text == "/gitlog" || strings.HasPrefix(text, "/gitlog "):
			args := strings.TrimPrefix(text, "/gitlog")
			args = strings.TrimSpace(args)
			n := 10
			if args != "" {
				fmt.Sscanf(args, "%d", &n)
			}
			if n < 1 || n > 50 {
				n = 10
			}
			out, err := gitLog(n)
			if err != nil {
				a.send(upd.Message.Chat.ID, "❌ git log: "+err.Error())
			} else if out == "" {
				a.send(upd.Message.Chat.ID, "no commits yet")
			} else {
				a.sendPlain(upd.Message.Chat.ID, "📜 last "+fmt.Sprintf("%d", n)+" commits:\n\n"+out)
			}
			continue
		case text == "/gitdiff" || strings.HasPrefix(text, "/gitdiff "):
			args := strings.TrimPrefix(text, "/gitdiff")
			args = strings.TrimSpace(args)
			out, err := gitDiff(args)
			if err != nil {
				a.send(upd.Message.Chat.ID, "❌ git diff: "+err.Error())
			} else if out == "" {
				a.send(upd.Message.Chat.ID, "no diff")
			} else {
				a.sendPlain(upd.Message.Chat.ID, "📊 diff "+args+":\n\n"+truncateLog(out, 3500))
			}
			continue
		case strings.HasPrefix(text, "/"):
			a.send(upd.Message.Chat.ID, "unknown command: "+text+"\ntype /help")
			continue
		}

		// Show typing indicator while the agent processes the message
		a.typing(upd.Message.Chat.ID)

		reply, err := a.Handle(upd.Message.Chat.ID, text)
		if err != nil {
			log.Printf("err: chatID=%d %v", upd.Message.Chat.ID, err)
			a.send(upd.Message.Chat.ID, "❌ "+err.Error())
			continue
		}
		log.Printf("reply: chatID=%d %q", upd.Message.Chat.ID, truncateLog(reply, 200))
		a.send(upd.Message.Chat.ID, reply)
	}
}

// showRollbackMenu lists the available versions as inline buttons.
// Each button's callback_data is "rollback:vN" — tapping it kicks off the
// rollback flow for that version.
func (a *Agent) showRollbackMenu(chatID int64) {
	versions, err := listVersions()
	if err != nil {
		a.send(chatID, "❌ list versions: "+err.Error())
		return
	}
	if len(versions) == 0 {
		a.send(chatID, "no versions on disk (data/versions/ is empty)")
		return
	}
	var rows [][]InlineButton
	var b strings.Builder
	b.WriteString("⏪ pick a version to roll back to:\n")
	for _, v := range versions {
		marker := ""
		if v.IsCurrent {
			marker = " ✅ current"
		}
		// e.g. "v3 abc1234 (2h ago)"
		label := fmt.Sprintf("%s %s (%s)%s", v.Version, v.ShortSHA, humanAge(v.BuiltAt), marker)
		if len(label) > 60 {
			label = label[:60] + "…"
		}
		rows = append(rows, []InlineButton{{
			Text:         label,
			CallbackData: "rollback:" + v.Version,
		}})
	}
	a.sendButtons(chatID, b.String(), rows)
}

// runRollback is the callback-driven rollback path. It checks for a dirty
// working tree first; if dirty, it edits the originating message to show a
// single "force" button instead of attempting the rollback.
func (a *Agent) runRollback(chatID, msgID int64, version string, force bool) {
	if !force {
		dirty, err := gitTrackedDirty()
		if err != nil {
			a.send(chatID, "❌ git status: "+err.Error())
			return
		}
		if len(dirty) > 0 {
			preview := strings.Join(dirty, "\n")
			if len(preview) > 500 {
				preview = preview[:500] + "…"
			}
			text := fmt.Sprintf("⏪ %s — working tree has uncommitted changes:\n\n%s\n\n"+
				"Commit/stash them first, or tap Force to overwrite.", version, preview)
			rows := [][]InlineButton{{
				{Text: "⚠️ Force rollback", CallbackData: "rollback:force"},
			}}
			_ = a.tg.EditMessageText(chatID, msgID, text, rows)
			// Stash the intended version on the agent for the force handler.
			a.pendingForceVersion = version
			return
		}
	}
	a.executeRollback(chatID, msgID, version, force)
}

// runRollbackFromDirty is the "force" callback after a dirty tree was
// detected. Pulls the version off the agent's pending slot.
func (a *Agent) runRollbackFromDirty(chatID, msgID int64) {
	v := a.pendingForceVersion
	a.pendingForceVersion = ""
	if v == "" {
		a.send(chatID, "❌ lost track of the requested version — try /rollback again")
		return
	}
	a.executeRollback(chatID, msgID, v, true)
}

// executeRollback shells out to a fresh `agent rollback` and reports the
// result via the message that the user clicked.
func (a *Agent) executeRollback(chatID, msgID int64, version string, force bool) {
	a.send(chatID, "⏪ rolling back to "+version+"…")
	go func() {
		out, err := runSelfRollback(version, force)
		if err != nil {
			a.tg.EditMessageText(chatID, msgID,
				"❌ rollback failed: "+err.Error()+"\n\n"+truncateLog(out, 1500), nil)
			return
		}
		a.tg.EditMessageText(chatID, msgID,
			"✅ rollback "+version+" sent to supervisor\n\n"+truncateLog(out, 1000), nil)
	}()
}

// humanAge returns a short, human-friendly age string ("just now", "5m ago",
// "2h ago", "3d ago") suitable for inline-button labels.
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
