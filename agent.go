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

// defaultMaxSteps caps the tool-call iterations per user message.
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

	runMu sync.Mutex
	runs  map[int64]*runState

	pendingForceVersion string
}

type injectedMsg struct {
	ChatID  int64
	Text    string
	trusted bool
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
		verbose:  true,
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

func (a *Agent) Push(chatID int64, text string) error {
	select {
	case a.inject <- injectedMsg{ChatID: chatID, Text: text, trusted: true}:
		return nil
	default:
		return fmt.Errorf("inject channel full")
	}
}

func (a *Agent) SetRecorder(fn func(chatID int64, text string)) {
	a.record = fn
}

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

	runCtx, runCancel := context.WithCancel(context.Background())
	rs := &runState{ctx: runCtx, cancel: runCancel, stop: make(chan struct{})}
	cleanup := a.registerRun(chatID, rs)
	defer cleanup()

	a.recordTrace(chatID, fmt.Sprintf("→ %s\nmodel=%s\nmax=%d\ntools=%d",
		truncateLog(userText, 100), a.cfg.DefaultModel, maxSteps, len(tools)))

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
		resp, usage, err := a.llm.Chat(messages, tools)
		stepDur := time.Since(stepStart)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				a.recordTrace(chatID, "🛑 aborted")
				return "🛑 aborted.", nil
			}
			a.recordTrace(chatID, fmt.Sprintf("  ✗ LLM error: %v", err))
			return "", err
		}

		var toolLines []string

		if len(resp.ToolCalls) == 0 {
			a.recordStep(chatID, i+1, maxSteps, usage, stepDur, nil, len(resp.Content))
			_ = sess.Append(ChatMessage{Role: "assistant", Content: resp.Content})
			return resp.Content, nil
		}

		_ = sess.Append(ChatMessage{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})

		for _, tc := range resp.ToolCalls {
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
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					toolLines = append(toolLines, fmt.Sprintf("  🛑 %s(%s) cancelled", tc.Function.Name, argStr))
					a.recordStep(chatID, i+1, maxSteps, usage, stepDur, toolLines, -1)
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
		a.recordStep(chatID, i+1, maxSteps, usage, stepDur, toolLines, -1)
	}

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

func (a *Agent) recordStep(chatID int64, step, max int, usage *Usage, dur time.Duration, toolLines []string, textReplyLen int) {
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

		// Handle /stop and /abort even while a task is running
		switch {
		case text == "/stop":
			if rs := a.getRun(chatID); rs != nil {
				rs.Stop()
				a.send(chatID, "⏹ <i>stopping after current step…</i>")
			} else {
				a.send(chatID, "no task in progress")
			}
			continue
		case text == "/abort":
			if rs := a.getRun(chatID); rs != nil {
				rs.Abort()
				a.send(chatID, "🛑 <i>aborted</i>")
			} else {
				a.send(chatID, "no task in progress")
			}
			continue
		}

		// If a task is already running for this chat, send "busy" hint
		if rs := a.getRun(chatID); rs != nil {
			a.send(chatID, "⏳ task in progress — use /stop or /abort to interrupt")
			continue
		}

		switch {
		case text == "/start":
			a.send(chatID,
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
			a.send(chatID,
				"/start /help /new /clear /stop /abort\n"+
					"/models /model /provider /system /maxsteps\n"+
					"/tools /trace /verbose\n"+
					"/version /upgrade /rollback /gitsha /gitlog /gitdiff\n"+
					"/chatid /health")
			continue
		case text == "/models":
			a.sendModelGrid(chatID)
			continue
		case text == "/clear":
			sess, _ := a.store.LoadOrCreate(chatID)
			_ = sess.Truncate(0)
			a.send(chatID, "🗑 session cleared")
			continue
		case text == "/new":
			sess, _ := a.store.LoadOrCreate(chatID)
			_ = sess.Truncate(0)
			a.send(chatID, "🆕 new session — send your first message")
			continue
		case text == "/rollback":
			a.showRollbackMenu(chatID)
			continue
		case text == "/list-versions" || text == "/versions":
			versions, err := listVersions()
			if err != nil {
				a.send(chatID, "❌ list: "+err.Error())
				continue
			}
			if len(versions) == 0 {
				a.send(chatID, "no versions on disk (data/versions/ is empty)")
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
		case text == "/tools":
			var b strings.Builder
			b.WriteString("🛠 Available tools:\n")
			for _, t := range a.tools.All() {
				b.WriteString("• " + t.Name + " — " + t.Description + "\n")
			}
			a.send(chatID, b.String())
			continue
		case text == "/health":
			a.send(chatID, "✅ ok")
			continue
		case text == "/chatid":
			a.send(chatID, fmt.Sprintf("chat.id = %d", chatID))
			continue
		case text == "/model" || strings.HasPrefix(text, "/model "):
			args := strings.TrimPrefix(text, "/model")
			args = strings.TrimSpace(args)
			if args == "" {
				a.send(chatID, "current model: "+a.cfg.DefaultModel)
			} else {
				if _, ok := a.cfg.Providers[a.cfg.Provider]; ok {
					a.cfg.DefaultModel = args
					a.send(chatID, "✅ model set to "+args)
				} else {
					a.send(chatID, "❌ no provider selected")
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
				a.send(chatID, b.String())
			} else {
				if _, ok := a.cfg.Providers[args]; ok {
					a.cfg.Provider = args
					a.send(chatID, "✅ provider set to "+args)
				} else {
					a.send(chatID, "❌ unknown provider: "+args)
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
				a.send(chatID, "current system prompt:\n\n"+preview)
			} else {
				a.cfg.SystemPrompt = args
				a.send(chatID, "✅ system prompt updated ("+fmt.Sprintf("%d", len(args))+" chars)")
			}
			continue
		case text == "/upgrade" || strings.HasPrefix(text, "/upgrade "):
			args := strings.TrimPrefix(text, "/upgrade")
			args = strings.TrimSpace(args)
			if args == "" {
				a.send(chatID, "usage: /upgrade vN\ncurrent: "+flagValue("--smago-version"))
				continue
			}
			a.send(chatID, "🔨 building "+args+"...")
			go func(v string) {
				out, err := runSelfUpgrade(v)
				if err != nil {
					a.send(chatID, "❌ upgrade failed: "+err.Error()+"\n\n"+truncateLog(out, 1500))
					return
				}
				a.send(chatID, "✅ upgrade "+v+" sent to supervisor\n\n"+truncateLog(out, 1000))
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
			a.send(chatID, fmt.Sprintf(
				"smago %s\nbuild: %s\ngit: %s\nbinary: %s (%s)\npid: %d\nuptime: %s",
				version, buildVer, sha, exe, sizeStr, pid, uptime.Truncate(time.Second)))
			continue
		case text == "/restart":
			a.send(chatID, "🔄 restarting — supervisor will bring me back in a moment")
			go func() {
				time.Sleep(500 * time.Millisecond)
				log.Println("restart: clean exit requested by user")
				os.Exit(0)
			}()
			continue
		case text == "/trace" || text == "/debug":
			buf := a.traceBuf[chatID]
			if len(buf) == 0 {
				a.send(chatID, "no agent activity yet — send any message first")
				continue
			}
			var b strings.Builder
			b.WriteString(fmt.Sprintf("🪛 last %d agent actions:\n\n", len(buf)))
			for _, line := range buf {
				b.WriteString(line + "\n")
			}
			a.sendPlain(chatID, b.String())
			continue
		case text == "/verbose" || text == "/quiet":
			a.verbose = !a.verbose
			if a.verbose {
				a.send(chatID, "✅ verbose ON — tool calls will be shown inline")
			} else {
				a.send(chatID, "✅ verbose OFF — only final answers")
			}
			continue
		case text == "/maxsteps" || strings.HasPrefix(text, "/maxsteps "):
			args := strings.TrimPrefix(text, "/maxsteps")
			args = strings.TrimSpace(args)
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
			a.send(chatID, fmt.Sprintf("✅ max steps set to %d for this chat", n))
			continue
		case text == "/gitsha" || text == "/githead":
			sha, err := gitHead()
			if err != nil {
				a.send(chatID, "❌ git: "+err.Error())
			} else {
				a.send(chatID, "🔖 HEAD: "+sha)
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
				a.send(chatID, "❌ git log: "+err.Error())
			} else if out == "" {
				a.send(chatID, "no commits yet")
			} else {
				a.sendPlain(chatID, "📜 last "+fmt.Sprintf("%d", n)+" commits:\n\n"+out)
			}
			continue
		case text == "/gitdiff" || strings.HasPrefix(text, "/gitdiff "):
			args := strings.TrimPrefix(text, "/gitdiff")
			args = strings.TrimSpace(args)
			out, err := gitDiff(args)
			if err != nil {
				a.send(chatID, "❌ git diff: "+err.Error())
			} else if out == "" {
				a.send(chatID, "no diff")
			} else {
				a.sendPlain(chatID, "📊 diff "+args+":\n\n"+truncateLog(out, 3500))
			}
			continue
		case strings.HasPrefix(text, "/"):
			a.send(chatID, "unknown command: "+text+"\ntype /help")
			continue
		}

		a.typing(chatID)

		// Run Handle in a goroutine so RunLoop stays responsive to /abort and /stop
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
		a.send(chatID, "❌ lost track of the requested version — try /rollback again")
		return
	}
	a.executeRollback(chatID, msgID, v, true)
}

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
