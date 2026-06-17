package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Telegram struct {
	token  string
	client *http.Client
	offset int64
	proxy  string
}

type TGUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		MessageID int64 `json:"message_id"`
		From      *struct {
			ID int64 `json:"id"`
		} `json:"from"`
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
	CallbackQuery *struct {
		ID      string `json:"id"`
		From    *struct {
			ID int64 `json:"id"`
		} `json:"from"`
		Message *struct {
			MessageID int64 `json:"message_id"`
			Chat      struct {
				ID int64 `json:"id"`
			} `json:"chat"`
		} `json:"message"`
		Data string `json:"data"`
	} `json:"callback_query"`
}

type InlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

type TGResponse struct {
	OK     bool        `json:"ok"`
	Result []TGUpdate  `json:"result"`
	Error  interface{} `json:"error,omitempty"`
}

type TGMe struct {
	OK     bool `json:"ok"`
	Result *struct {
		ID        int64  `json:"id"`
		IsBot     bool   `json:"is_bot"`
		FirstName string `json:"first_name"`
		Username  string `json:"username"`
	} `json:"result"`
}

func NewTelegram(token string) *Telegram {
	return &Telegram{token: token, client: &http.Client{Timeout: 35 * time.Second}}
}

func (t *Telegram) SetProxyURL(rawURL string) error {
	if rawURL == "" {
		t.client.Transport = nil
		t.proxy = ""
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	t.client.Transport = &http.Transport{Proxy: http.ProxyURL(u)}
	t.proxy = rawURL
	return nil
}

func (t *Telegram) GetMe(ctx context.Context) (*TGMe, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.telegram.org/bot"+t.token+"/getMe", nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var m TGMe
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, err
	}
	if !m.OK || m.Result == nil {
		return nil, fmt.Errorf("telegram: getMe returned ok=false")
	}
	return &m, nil
}

func (t *Telegram) LongPoll(ctx context.Context) (*TGUpdate, error) {
	for ctx.Err() == nil {
		v := url.Values{}
		v.Set("timeout", "30")
		v.Set("offset", fmt.Sprintf("%d", t.offset))
		v.Set("allowed_updates", `["message","callback_query"]`)
		req, err := http.NewRequestWithContext(ctx, "GET", "https://api.telegram.org/bot"+t.token+"/getUpdates?"+v.Encode(), nil)
		if err != nil {
			return nil, err
		}
		resp, err := t.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			time.Sleep(2 * time.Second)
			continue
		}
		var tr TGResponse
		if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
			resp.Body.Close()
			time.Sleep(2 * time.Second)
			continue
		}
		resp.Body.Close()
		if !tr.OK {
			time.Sleep(2 * time.Second)
			continue
		}
		if len(tr.Result) > 0 {
			t.offset = tr.Result[len(tr.Result)-1].UpdateID + 1
			for _, u := range tr.Result {
				if u.Message != nil && u.Message.Text != "" {
					return &u, nil
				}
				if u.CallbackQuery != nil {
					return &u, nil
				}
			}
		}
	}
	return nil, ctx.Err()
}

func (t *Telegram) SendChatAction(chatID int64, action string) error {
	v := url.Values{}
	v.Set("chat_id", fmt.Sprintf("%d", chatID))
	v.Set("action", action)
	req, err := http.NewRequest("POST", "https://api.telegram.org/bot"+t.token+"/sendChatAction", strings.NewReader(v.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (t *Telegram) Typing(chatID int64) error {
	return t.SendChatAction(chatID, "typing")
}

func (t *Telegram) Send(chatID int64, text string) error {
	return t.SendButtons(chatID, text, nil)
}

func (t *Telegram) SendPlain(chatID int64, text string) error {
	return t.sendMessage(chatID, text, false)
}

func (t *Telegram) SendSilent(chatID int64, text string) error {
	return t.sendMessage(chatID, text, true)
}

func (t *Telegram) sendMessage(chatID int64, text string, silent bool) error {
	if len(text) > 4000 {
		text = text[:4000] + "\n\n[...truncated]"
	}
	v := url.Values{}
	v.Set("chat_id", fmt.Sprintf("%d", chatID))
	v.Set("text", mdToTelegramHTML(text))
	v.Set("parse_mode", "HTML")
	if silent {
		v.Set("disable_notification", "true")
	}
	req, err := http.NewRequest("POST", "https://api.telegram.org/bot"+t.token+"/sendMessage", strings.NewReader(v.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (t *Telegram) SendButtons(chatID int64, text string, rows [][]InlineButton) error {
	if len(text) > 4000 {
		text = text[:4000] + "\n\n[...truncated]"
	}
	v := url.Values{}
	v.Set("chat_id", fmt.Sprintf("%d", chatID))
	v.Set("text", mdToTelegramHTML(text))
	v.Set("parse_mode", "HTML")
	if len(rows) > 0 {
		kb, _ := json.Marshal(map[string]any{"inline_keyboard": rows})
		v.Set("reply_markup", string(kb))
	}
	req, err := http.NewRequest("POST", "https://api.telegram.org/bot"+t.token+"/sendMessage", strings.NewReader(v.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (t *Telegram) AnswerCallback(callbackID, text string) error {
	v := url.Values{}
	v.Set("callback_query_id", callbackID)
	if text != "" {
		v.Set("text", text)
		v.Set("show_alert", "false")
	}
	req, err := http.NewRequest("POST", "https://api.telegram.org/bot"+t.token+"/answerCallbackQuery", strings.NewReader(v.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

func (t *Telegram) SetMyCommands(commands []BotCommand) error {
	payload, err := json.Marshal(map[string]any{"commands": commands, "scope": map[string]any{"type": "default"}})
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", "https://api.telegram.org/bot"+t.token+"/setMyCommands", strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("setMyCommands HTTP %d", resp.StatusCode)
	}
	return nil
}

func (t *Telegram) EditMessageText(chatID int64, messageID int64, text string, rows [][]InlineButton) error {
	if len(text) > 4000 {
		text = text[:4000] + "\n\n[...truncated]"
	}
	v := url.Values{}
	v.Set("chat_id", fmt.Sprintf("%d", chatID))
	v.Set("message_id", fmt.Sprintf("%d", messageID))
	v.Set("text", mdToTelegramHTML(text))
	v.Set("parse_mode", "HTML")
	if len(rows) > 0 {
		kb, _ := json.Marshal(map[string]any{"inline_keyboard": rows})
		v.Set("reply_markup", string(kb))
	}
	req, err := http.NewRequest("POST", "https://api.telegram.org/bot"+t.token+"/editMessageText", strings.NewReader(v.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}
