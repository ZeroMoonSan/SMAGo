package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

type Agent struct {
	cfg     *Config
	llm     *LLM
	store   *Store
	tg      *Telegram
	tools   *ToolRegistry
	inject  chan injectedMsg
	record  func(chatID int64, text string)
}

// injectedMsg is a message pushed into the agent loop from outside Telegram.
type injectedMsg struct {
	ChatID  int64
	Text    string
	trusted bool // injected messages skip the trusted-id check
}

func NewAgent(cfg *Config, llm *LLM, store *Store, tg *Telegram, tools *ToolRegistry) *Agent {
	return &Agent{
		cfg:    cfg,
		llm:    llm,
		store:  store,
		tg:     tg,
		tools:  tools,
		inject: make(chan injectedMsg, 16),
	}
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

	maxSteps := 5
	for i := 0; i < maxSteps; i++ {
		resp, err := a.llm.Chat(messages, tools)
		if err != nil {
			return "", err
		}

		if len(resp.ToolCalls) == 0 {
			_ = sess.Append(ChatMessage{Role: "assistant", Content: resp.Content})
			return resp.Content, nil
		}

		_ = sess.Append(ChatMessage{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})

		for _, tc := range resp.ToolCalls {
			tdef, ok := a.tools.Get(tc.Function.Name)
			if !ok {
				_ = sess.Append(ChatMessage{
					Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name,
					Content: "error: unknown tool",
				})
				continue
			}
			var args map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				_ = sess.Append(ChatMessage{
					Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name,
					Content: "error: bad arguments: " + err.Error(),
				})
				continue
			}
			out, err := tdef.Execute(args)
			if err != nil {
				out = "error: " + err.Error()
			}
			_ = sess.Append(ChatMessage{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: out})
			messages = append(messages,
				ChatMessage{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls},
				ChatMessage{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: out},
			)
		}
	}
	return "", fmt.Errorf("agent loop exceeded %d steps", maxSteps)
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
			if cq.Message != nil {
				chatID = cq.Message.Chat.ID
			}
			switch {
			case strings.HasPrefix(data, "model:"):
				name := strings.TrimPrefix(data, "model:")
				a.cfg.DefaultModel = name
				if chatID != 0 {
					a.send(chatID, "✅ model → "+name)
				}
				_ = a.tg.AnswerCallback(cq.ID, "model: "+name)
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
					"Commands:\n"+
					"/start — this message\n"+
					"/help — command reference\n"+
					"/tools — list available tools\n"+
					"/models — pick a model (inline buttons)\n"+
					"/model [name] — show or set model\n"+
					"/provider [name] — show or set provider\n"+
					"/system [text] — show or set system prompt\n"+
					"/chatid — show this chat's id\n"+
					"/health — liveness ping\n"+
					"/clear — wipe session history\n\n"+
					"Just type anything to chat. I can run shell commands, read/write files in ./data.")
			continue
		case text == "/help":
			a.send(upd.Message.Chat.ID,
				"/start /help /tools /models /model /provider /system /chatid /health /clear")
			continue
		case text == "/models":
			a.sendModelGrid(upd.Message.Chat.ID)
			continue
		case text == "/clear":
			sess, _ := a.store.LoadOrCreate(upd.Message.Chat.ID)
			_ = sess.Truncate(0)
			a.send(upd.Message.Chat.ID, "🗑 session cleared")
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
			a.send(upd.Message.Chat.ID, "smago "+version+", build: "+flagValue("--smago-version"))
			continue
		case strings.HasPrefix(text, "/"):
			a.send(upd.Message.Chat.ID, "unknown command: "+text+"\ntype /help")
			continue
		}

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
