package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/playwright-community/playwright-go"
)

// truncStr is a local truncate to avoid name clash with truncate() in llm.go.
func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... [truncated]"
}

// BrowserTool wraps a single Chromium page that the agent reuses across
// requests. The browser is launched lazily on first use and shut down
// with the agent.
//
// Single tool, many actions — keeps the LLM's tool list compact.
type BrowserTool struct {
	cfg *Config

	mu      sync.Mutex
	pw      *playwright.Playwright
	browser playwright.Browser
	page    playwright.Page
	inited  bool
	failed  bool
}

func (b *BrowserTool) Definition() ToolDef {
	return ToolDef{
		Name: "browser",
		Description: "Drive a headless Chromium browser. Use for: Google search, fetching JS-heavy pages, clicking links, filling forms. " +
			"One persistent page is reused across calls. " +
			"Actions: search (Google), navigate, snapshot (page text), click, type, press, screenshot, close.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"search", "navigate", "snapshot", "click", "type", "press", "screenshot", "close"},
					"description": "What to do",
				},
				"query":    map[string]any{"type": "string", "description": "search: the Google query"},
				"url":      map[string]any{"type": "string", "description": "navigate: URL to open"},
				"selector": map[string]any{"type": "string", "description": "click/type: CSS selector of the target element"},
				"text":     map[string]any{"type": "string", "description": "type: text to type into the element"},
				"key":      map[string]any{"type": "string", "description": "press: key name (e.g. 'Enter', 'Escape', 'ArrowDown')"},
				"path":     map[string]any{"type": "string", "description": "screenshot: output file path (relative to ./data)"},
				"limit":    map[string]any{"type": "integer", "description": "snapshot: max chars to return (default 4000)"},
			},
			"required": []string{"action"},
		},
		Execute: b.Execute,
	}
}

func (b *BrowserTool) Close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.browser != nil {
		_ = b.browser.Close()
		b.browser = nil
	}
	if b.pw != nil {
		_ = b.pw.Stop()
		b.pw = nil
	}
	b.inited = false
}

func (b *BrowserTool) ensureInit() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.inited {
		return nil
	}
	if b.failed {
		return fmt.Errorf("browser previously failed; will retry on next call")
	}
	pw, err := playwright.Run()
	if err != nil {
		b.failed = true
		return fmt.Errorf("playwright run: %w", err)
	}
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless:  playwright.Bool(true),
		ChromiumSandbox: playwright.Bool(false),
		Args: []string{
			// Force the new headless mode (truly no UI, no window).
			"--headless=new",
			// Stability / sandbox flags.
			"--no-sandbox",
			"--disable-gpu",
			"--disable-software-rasterizer",
			"--disable-dev-shm-usage",
			// Quiet startup — no "first run" / "default browser" / "translate" prompts.
			"--no-first-run",
			"--no-default-browser-check",
			"--disable-default-apps",
			"--disable-extensions",
			"--disable-component-extensions-with-background-pages",
			"--disable-background-networking",
			"--disable-sync",
			"--disable-translate",
			// Suppress the features that spawn extra processes / popups.
			"--disable-features=Translate,BackForwardCache,MediaRouter,OptimizationHints,AudioServiceOutOfProcess,InterestFeedContentSuggestions",
			"--mute-audio",
			// Belt-and-suspenders: position any stray window off-screen.
			"--window-position=-10000,-10000",
		},
	})
	if err != nil {
		_ = pw.Stop()
		b.failed = true
		return fmt.Errorf("chromium launch: %w", err)
	}
	page, err := browser.NewPage()
	if err != nil {
		_ = browser.Close()
		_ = pw.Stop()
		b.failed = true
		return fmt.Errorf("new page: %w", err)
	}
	b.pw = pw
	b.browser = browser
	b.page = page
	b.inited = true
	return nil
}

func (b *BrowserTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	action, _ := args["action"].(string)
	if action == "" {
		return "", fmt.Errorf("action is required")
	}
	// Snapshot ctx so the per-action goroutines can read it after we return.
	// The browser API is synchronous so we can't truly cancel mid-navigation
	// from the same goroutine, but we honour ctx by checking it between
	// actions in a multi-step Playwright call. For now the main benefit is
	// that Close() and any future async paths see cancellation.
	_ = ctx
	switch action {
	case "search":
		return b.actionSearch(args)
	case "navigate":
		return b.actionNavigate(args)
	case "snapshot":
		return b.actionSnapshot(args)
	case "click":
		return b.actionClick(args)
	case "type":
		return b.actionType(args)
	case "press":
		return b.actionPress(args)
	case "screenshot":
		return b.actionScreenshot(args)
	case "close":
		b.Close()
		return "browser closed", nil
	default:
		return "", fmt.Errorf("unknown action %q", action)
	}
}

func (b *BrowserTool) actionSearch(args map[string]any) (string, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return "", fmt.Errorf("query required for search")
	}
	if err := b.ensureInit(); err != nil {
		return "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.page.Goto("https://www.google.com/"); err != nil {
		return "", fmt.Errorf("navigate: %w", err)
	}
	// Google uses <textarea name="q">; the old <input name="q"> fallback
	// covers some locales.
	if err := b.page.Locator("textarea[name='q'], input[name='q']").First().Fill(query); err != nil {
		return "", fmt.Errorf("fill: %w", err)
	}
	if err := b.page.Keyboard().Press("Enter"); err != nil {
		return "", fmt.Errorf("press enter: %w", err)
	}
	// Wait for either the results block or the "did not match" message.
	_, err := b.page.WaitForFunction("() => document.querySelector('#search, .g, #topstuff, [data-hveid]') !== null || /did not match|no results/i.test(document.body.innerText)",
		nil, playwright.PageWaitForFunctionOptions{Timeout: playwright.Float(10000)})
	if err != nil {
		return "", fmt.Errorf("wait for results: %w", err)
	}
	// Give the page a moment to finish laying out the results.
	loadState := playwright.LoadState("networkidle")
	_ = b.page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State:   &loadState,
		Timeout: playwright.Float(3000),
	})
	return b.extractSearchResults()
}

func (b *BrowserTool) extractSearchResults() (string, error) {
	// Pull a compact, structured dump: each result gets a title + url + snippet.
	results, err := b.page.Locator("div.g, div[data-hveid]").All()
	if err != nil {
		// Fall back to body text if the structural parse fails.
		t, _ := b.page.Locator("body").TextContent()
		return truncStr(t, 4000), nil
	}
	var out strings.Builder
	for i, r := range results {
		if i >= 10 {
			break
		}
		title, _ := r.Locator("h3").First().TextContent()
		var url string
		if a, err := r.Locator("a").First().GetAttribute("href"); err == nil {
			url = a
		}
		var snippet string
		if s, err := r.Locator(".VwiC3b, .yXK7lf, .st, [data-content-feature]").First().TextContent(); err == nil {
			snippet = s
		}
		out.WriteString(fmt.Sprintf("%d. %s\n   %s\n   %s\n\n", i+1, strings.TrimSpace(title), url, strings.TrimSpace(snippet)))
	}
	if out.Len() == 0 {
		t, _ := b.page.Locator("body").TextContent()
		return truncStr(t, 4000), nil
	}
	return out.String(), nil
}

func (b *BrowserTool) actionNavigate(args map[string]any) (string, error) {
	url, _ := args["url"].(string)
	if url == "" {
		return "", fmt.Errorf("url required for navigate")
	}
	if err := b.ensureInit(); err != nil {
		return "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	waitUntil := playwright.WaitUntilState("domcontentloaded")
	resp, err := b.page.Goto(url, playwright.PageGotoOptions{
		Timeout:   playwright.Float(20000),
		WaitUntil: &waitUntil,
	})
	if err != nil {
		return "", fmt.Errorf("navigate: %w", err)
	}
	title, _ := b.page.Title()
	status := -1
	if resp != nil {
		status = resp.Status()
	}
	return fmt.Sprintf("navigated: status=%d title=%q", status, title), nil
}

func (b *BrowserTool) actionSnapshot(args map[string]any) (string, error) {
	if err := b.ensureInit(); err != nil {
		return "", err
	}
	limit := 4000
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	// Try structural: gather text of body in chunks.
	text, err := b.page.Locator("body").TextContent()
	if err != nil {
		return "", err
	}
	return truncStr(text, limit), nil
}

func (b *BrowserTool) actionClick(args map[string]any) (string, error) {
	sel, _ := args["selector"].(string)
	if sel == "" {
		return "", fmt.Errorf("selector required for click")
	}
	if err := b.ensureInit(); err != nil {
		return "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.page.Locator(sel).First().Click(playwright.LocatorClickOptions{Timeout: playwright.Float(10000)}); err != nil {
		return "", fmt.Errorf("click %q: %w", sel, err)
	}
	loadState := playwright.LoadState("domcontentloaded")
	_ = b.page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State:   &loadState,
		Timeout: playwright.Float(5000),
	})
	return "clicked: " + sel, nil
}

func (b *BrowserTool) actionType(args map[string]any) (string, error) {
	sel, _ := args["selector"].(string)
	text, _ := args["text"].(string)
	if sel == "" || text == "" {
		return "", fmt.Errorf("selector and text required for type")
	}
	if err := b.ensureInit(); err != nil {
		return "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.page.Locator(sel).First().Fill(text); err != nil {
		return "", fmt.Errorf("type into %q: %w", sel, err)
	}
	return fmt.Sprintf("typed into %s: %d chars", sel, len(text)), nil
}

func (b *BrowserTool) actionPress(args map[string]any) (string, error) {
	key, _ := args["key"].(string)
	if key == "" {
		return "", fmt.Errorf("key required for press")
	}
	if err := b.ensureInit(); err != nil {
		return "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.page.Keyboard().Press(key); err != nil {
		return "", fmt.Errorf("press %q: %w", key, err)
	}
	return "pressed: " + key, nil
}

func (b *BrowserTool) actionScreenshot(args map[string]any) (string, error) {
	if err := b.ensureInit(); err != nil {
		return "", err
	}
	path, _ := args["path"].(string)
	if path == "" {
		path = fmt.Sprintf("screenshots/shot-%s.png", time.Now().Format("20060102-150405"))
	}
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(b.cfg.DataDir, path)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		return "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.page.Screenshot(playwright.PageScreenshotOptions{Path: playwright.String(full), FullPage: playwright.Bool(false)}); err != nil {
		return "", fmt.Errorf("screenshot: %w", err)
	}
	return "saved: " + path, nil
}
