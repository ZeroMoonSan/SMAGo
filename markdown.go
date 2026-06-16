package main

import (
	"strings"
)

// mdToTelegramHTML converts a Markdown-ish string (what the LLM produces)
// into Telegram's HTML dialect. It handles bold, italic, code spans,
// code blocks, and links. It escapes any HTML-significant character that
// isn't part of a recognised tag so Telegram's HTML parser doesn't choke.
//
// This is intentionally simple — it does NOT try to be a full Markdown
// parser. It covers the constructs the LLM uses most often.
func mdToTelegramHTML(s string) string {
	// 1. Pull out fenced code blocks and replace with placeholders so we
	//    don't try to interpret their contents as Markdown.
	const codeBlockPlaceholder = "\x00CODEBLOCK\x00"
	var blocks []string
	for {
		i := strings.Index(s, "```")
		if i < 0 {
			break
		}
		j := strings.Index(s[i+3:], "```")
		if j < 0 {
			break
		}
		// Find language tag (text right after opening ```) — drop it.
		body := s[i+3 : i+3+j]
		body = strings.TrimLeft(body, "\n")
		if nl := strings.Index(body, "\n"); nl >= 0 && strings.IndexAny(body[:nl], " \t") > 0 {
			// has language tag
			body = body[nl+1:]
		} else if nl >= 0 && nl <= 16 {
			// first line is just a language tag
			body = body[nl+1:]
		}
		blocks = append(blocks, "<pre>"+htmlEscape(body)+"</pre>")
		s = s[:i] + codeBlockPlaceholder + s[i+3+j+3:]
	}

	// 2. Inline code spans — same trick.
	const codeSpanPlaceholder = "\x00CODESPAN\x00"
	var spans []string
	for {
		i := strings.Index(s, "`")
		if i < 0 {
			break
		}
		j := strings.Index(s[i+1:], "`")
		if j < 0 {
			break
		}
		body := s[i+1 : i+1+j]
		spans = append(spans, "<code>"+htmlEscape(body)+"</code>")
		s = s[:i] + codeSpanPlaceholder + s[i+1+j+1:]
	}

	// 3. Escape remaining HTML.
	s = htmlEscape(s)

	// 4. Bold (**text**) and italic (*text* / _text_).
	// Bold first so we don't accidentally turn * inside ** into italic.
	s = replaceAll(s, `\*\*(.+?)\*\*`, "<b>$1</b>")
	s = replaceAll(s, `__(.+?)__`, "<b>$1</b>")
	s = replaceAll(s, `\*([^\*\n]+?)\*`, "<i>$1</i>")
	s = replaceAll(s, `_([^_\n]+?)_`, "<i>$1</i>")

	// 5. Links [text](url).
	s = replaceAll(s, `\[([^\]]+)\]\(([^\)]+)\)`, `<a href="$2">$1</a>`)

	// 6. Restore placeholders.
	for _, b := range blocks {
		s = strings.Replace(s, codeBlockPlaceholder, b, 1)
	}
	for _, sp := range spans {
		s = strings.Replace(s, codeSpanPlaceholder, sp, 1)
	}

	return s
}

func htmlEscape(s string) string {
	r := strings.NewReplacer(
		`&`, "&amp;",
		`<`, "&lt;",
		`>`, "&gt;",
		`"`, "&quot;",
	)
	return r.Replace(s)
}

// replaceAll does a single non-overlapping pass of regex replacement.
// Uses Go's regexp under the hood (we keep a tiny cache per call site
// would be nice but for this size it doesn't matter).
func replaceAll(s, pattern, repl string) string {
	return regexCache.MustCompile(pattern).ReplaceAllString(s, repl)
}
