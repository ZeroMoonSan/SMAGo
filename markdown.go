package main

import (
	"strings"
	"unicode/utf8"
)

// mdToTelegramHTML converts a Markdown-ish string (what the LLM produces)
// into Telegram's HTML dialect. It handles bold, italic, code spans,
// code blocks, tables, headings, and links. It escapes any HTML-significant
// character that isn't part of a recognised tag so Telegram's HTML parser
// doesn't choke.
//
// This is intentionally simple — it does NOT try to be a full Markdown
// parser. It covers the constructs the LLM uses most often.
func mdToTelegramHTML(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	const codeBlockPH = "xXB7CODEBLOCKxXB7"
	const codeSpanPH = "xXB7CODESPANxXB7"
	const tablePH = "xXB7TABLExXB7"
	const headingPH = "xXB7HEADINGxXB7"

	// 1. Fenced code blocks
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
		body := s[i+3 : i+3+j]
		body = strings.TrimLeft(body, "\n")
		if nl := strings.Index(body, "\n"); nl >= 0 && nl <= 16 {
			body = body[nl+1:]
		}
		blocks = append(blocks, "<pre>"+htmlEscape(body)+"</pre>")
		s = s[:i] + codeBlockPH + s[i+3+j+3:]
	}

	// 2. Inline code spans
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
		s = s[:i] + codeSpanPH + s[i+1+j+1:]
	}

	// 3. Tables — collect consecutive |...| lines, then align columns.
	var tables []string
	{
		lines := strings.Split(s, "\n")
		var out []string
		var buf []string
		flush := func() {
			if len(buf) > 1 {
				tables = append(tables, "<pre>"+htmlEscape(renderAlignedTable(buf))+"</pre>")
				out = append(out, tablePH)
			} else {
				out = append(out, buf...)
			}
			buf = buf[:0]
		}
		for _, line := range lines {
			t := strings.TrimSpace(line)
			if len(t) > 1 && strings.HasPrefix(t, "|") && strings.HasSuffix(t, "|") {
				buf = append(buf, line)
			} else {
				flush()
				out = append(out, line)
			}
		}
		flush()
		s = strings.Join(out, "\n")
	}

	// 4. Headings
	var headings []string
	{
		lines := strings.Split(s, "\n")
		for i, line := range lines {
			trimmed := strings.TrimLeft(line, " \t")
			if !strings.HasPrefix(trimmed, "#") {
				continue
			}
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			if level >= 1 && level <= 6 && level < len(trimmed) && trimmed[level] == ' ' {
				text := strings.TrimSpace(trimmed[level+1:])
				headings = append(headings, "<b>"+htmlEscape(text)+"</b>")
				lines[i] = headingPH
			}
		}
		s = strings.Join(lines, "\n")
	}

	// 5. Escape remaining HTML
	s = htmlEscape(s)

	// 6. Bold and italic
	s = replaceAll(s, `\*\*(.+?)\*\*`, "<b>$1</b>")
	s = replaceAll(s, `__(.+?)__`, "<b>$1</b>")
	s = replaceAll(s, `\*([^\*\n]+?)\*`, "<i>$1</i>")
	s = replaceAll(s, `_([^_\n]+?)_`, "<i>$1</i>")

	// 7. Links
	s = replaceAll(s, `\[([^\]]+)\]\(([^\)]+)\)`, `<a href="$2">$1</a>`)

	// 8. Restore placeholders (tables first, then headings, then code)
	for _, t := range tables {
		s = strings.Replace(s, tablePH, t, 1)
	}
	for _, h := range headings {
		s = strings.Replace(s, headingPH, h, 1)
	}
	for _, sp := range spans {
		s = strings.Replace(s, codeSpanPH, sp, 1)
	}
	for _, b := range blocks {
		s = strings.Replace(s, codeBlockPH, b, 1)
	}

	return s
}

// renderAlignedTable takes markdown table lines (| col1 | col2 |)
// and returns a single string with columns padded to equal width.
func renderAlignedTable(lines []string) string {
	// Parse into rows of cells.
	var rows [][]string
	for _, line := range lines {
		cells := parseTableRow(line)
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		return strings.Join(lines, "\n")
	}

	// Determine number of columns.
	numCols := 0
	for _, row := range rows {
		if len(row) > numCols {
			numCols = len(row)
		}
	}

	// Compute max width per column (using rune-aware width).
	colWidths := make([]int, numCols)
	for _, row := range rows {
		for c, cell := range row {
			w := utf8.RuneCountInString(cell)
			if w > colWidths[c] {
				colWidths[c] = w
			}
		}
	}

	// Rebuild with padding.
	var out []string
	for ri, row := range rows {
		// Check if this row is a separator (e.g. |---|---|).
		isSep := true
		for _, cell := range row {
			if !isSeparator(cell) {
				isSep = false
				break
			}
		}

		var parts []string
		for c := 0; c < numCols; c++ {
			cell := ""
			if c < len(row) {
				cell = row[c]
			}
			if isSep {
				parts = append(parts, strings.Repeat("-", colWidths[c]))
			} else {
				parts = append(parts, padRight(cell, colWidths[c]))
			}
		}
		line := "| " + strings.Join(parts, " | ") + " |"
		out = append(out, line)
		// Keep original separator line formatting (dashes).
		_ = ri
	}

	return strings.Join(out, "\n")
}

// parseTableRow splits "| a | b | c |" into ["a", "b", "c"].
func parseTableRow(line string) []string {
	line = strings.TrimSpace(line)
	// Remove leading and trailing |
	if strings.HasPrefix(line, "|") {
		line = line[1:]
	}
	if strings.HasSuffix(line, "|") {
		line = line[:len(line)-1]
	}
	parts := strings.Split(line, "|")
	var cells []string
	for _, p := range parts {
		cells = append(cells, strings.TrimSpace(p))
	}
	return cells
}

// isSeparator returns true if the cell looks like a table separator (---, :---:, etc.)
func isSeparator(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return false
	}
	for _, r := range s {
		if r != '-' && r != ':' {
			return false
		}
	}
	return strings.ContainsRune(s, '-')
}

// padRight pads s with spaces on the right to reach width w (rune-aware).
func padRight(s string, w int) string {
	cur := utf8.RuneCountInString(s)
	if cur >= w {
		return s
	}
	return s + strings.Repeat(" ", w-cur)
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

func replaceAll(s, pattern, repl string) string {
	return regexCache.MustCompile(pattern).ReplaceAllString(s, repl)
}
