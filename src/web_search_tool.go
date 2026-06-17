package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// WebSearchTool does a Google search via the DuckDuckGo HTML endpoint.
// No browser, no spawned processes, no focus stealing — just an HTTP GET
// and an HTML parse. Returns the top N results (title, url, snippet).
type WebSearchTool struct{}

func (w *WebSearchTool) Definition() ToolDef {
	return ToolDef{
		Name:        "web_search",
		Description: "Search the web (DuckDuckGo HTML) and return the top 10 results with title, URL, and snippet. Pure HTTP, no browser.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Search query"},
			},
			"required": []string{"query"},
		},
		Execute: w.Execute,
	}
}

func (w *WebSearchTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return "", fmt.Errorf("query required")
	}
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://duckduckgo.com/html/?"+url.Values{"q": {query}}.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) SMAGo/0.1")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := defaultHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("search HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return parseDuckDuckGoHTML(string(body)), nil
}

func parseDuckDuckGoHTML(s string) string {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return "search: parse error"
	}
	type result struct {
		title, url, snippet string
	}
	var results []result
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			class := getAttr(n, "class")
			href := getAttr(n, "href")
			if strings.Contains(class, "result__a") && href != "" {
				title := textContent(n)
				var snippet string
				if p := n.Parent; p != nil {
					if s := findFirst(p, "a", "result__snippet"); s != nil {
						snippet = textContent(s)
					}
				}
				results = append(results, result{title: title, url: href, snippet: snippet})
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	if len(results) == 0 {
		return "no results (DuckDuckGo may have blocked the request, or your query matched nothing)"
	}
	var b strings.Builder
	for i, r := range results {
		if i >= 10 {
			break
		}
		b.WriteString(fmt.Sprintf("%d. %s\n   %s\n", i+1, strings.TrimSpace(r.title), strings.TrimSpace(r.url)))
		if r.snippet != "" {
			snippet := strings.TrimSpace(r.snippet)
			if len(snippet) > 240 {
				snippet = snippet[:240] + "…"
			}
			b.WriteString("   " + snippet + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func findFirst(n *html.Node, tag, class string) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == tag && strings.Contains(getAttr(c, "class"), class) {
			return c
		}
		if f := findFirst(c, tag, class); f != nil {
			return f
		}
	}
	return nil
}

func textContent(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(b.String())
}
