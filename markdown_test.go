package main

import (
	"fmt"
	"testing"
)

func TestMDConvert(t *testing.T) {
	inputs := []struct {
		name  string
		input string
	}{
		{"simple", "**bold** and *italic* and `code`"},
		{"table", "### Заголовок\n\n| Файл | Тип |\n|-----|-----|\n| `current.json` | 📄 |\n| `sessions.db` | 📄 |\n"},
		{"nested", "`outer `inner` more`"},
		{"link", "[click](https://example.com)"},
		{"codeblock", "before\n```go\nfunc x() {}\n```\nafter"},
	}
	for _, tc := range inputs {
		t.Run(tc.name, func(t *testing.T) {
			fmt.Println("=== INPUT ===")
			fmt.Println(tc.input)
			fmt.Println("=== OUTPUT ===")
			fmt.Println(mdToTelegramHTML(tc.input))
			fmt.Println()
		})
	}
}
