//go:build ignore

package main

import (
	"fmt"
	"strings"
)

func mdToTelegramHTML(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

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
		body := s[i+3 : i+3+j]
		body = strings.TrimLeft(body, "\n")
		if nl := strings.Index(body, "\n"); nl >= 0 && strings.IndexAny(body[:nl], " \t") > 0 {
			body = body[nl+1:]
		} else if nl >= 0 && nl <= 16 {
			body = body[nl+1:]
		}
		blocks = append(blocks, "<pre>"+htmlEscape(body)+"</pre>")
		s = s[:i] + codeBlockPlaceholder + s[i+3+j+3:]
	}

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

	const tablePlaceholder = "\x00TABLE\x00"
	var tables []string
	{
		lines := strings.Split(s, "\n")
		var out []string
		var buf []string
		flush := func() {
			if len(buf) > 1 {
				tables = append(tables, "<pre>"+htmlEscape(strings.Join(buf, "\n"))+"</pre>")
				out = append(out, tablePlaceholder)
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

	const headingPlaceholder = "\x00HEADING\x00"
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
				lines[i] = headingPlaceholder
			}
		}
		s = strings.Join(lines, "\n")
	}

	s = htmlEscape(s)

	s = replaceAll(s, `\*\*(.+?)\*\*`, "<b>$1</b>")
	s = replaceAll(s, `__(.+?)__`, "<b>$1</b>")
	s = replaceAll(s, `\*([^\*\n]+?)\*`, "<i>$1</i>")
	s = replaceAll(s, `_([^_\n]+?)_`, "<i>$1</i>")

	s = replaceAll(s, `\[([^\]]+)\]\(([^\)]+)\)`, `<a href="$2">$1</a>`)

	for _, b := range blocks {
		s = strings.Replace(s, codeBlockPlaceholder, b, 1)
	}
	for _, sp := range spans {
		s = strings.Replace(s, codeSpanPlaceholder, sp, 1)
	}
	for _, t := range tables {
		s = strings.Replace(s, tablePlaceholder, t, 1)
	}
	for _, h := range headings {
		s = strings.Replace(s, headingPlaceholder, h, 1)
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

func replaceAll(s, pattern, repl string) string {
	re := regexp.MustCompile(pattern)
	return re.ReplaceAllString(s, repl)
}

func main() {
	input := "### Заголовок уровня 3\n\n| Имя       | Описание         |\n|-----------|------------------|\n| bash      | shell-команды    |\n| read_file | чтение файлов    |\n| write_file| запись файлов    |\n| vision    | анализ картинок  |\n\nОбычный текст после таблицы."

	output := mdToTelegramHTML(input)
	fmt.Println("=== OUTPUT ===")
	fmt.Println(output)
}
