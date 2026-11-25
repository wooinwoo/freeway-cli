package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestMarkdownDrawsTablesRulesAndNestedCode(t *testing.T) {
	text := strings.Join([]string{
		"| 항목 | list | tuple |",
		"|---|:---:|---|",
		"| 문법 | `[1, 2]` | (1, 2) |",
		"| 가변성 | 가변 — 요소 추가/삭제/변경 가능 | 불변 |",
		"",
		"---",
		"## 2) `email-validator` **방식**",
		"1. 쉼표",
		"   ```python",
		"   t = (5,)",
		"   ```",
	}, "\n")
	rendered, _ := markdown(text, 40)
	lines := strings.Split(ansi.Strip(rendered), "\n")
	for i, line := range lines {
		if w := ansi.StringWidth(line); w > 40 {
			t.Fatalf("line %d is %d cells wide: %q", i, w, line)
		}
	}
	plain := strings.Join(lines, "\n")
	for _, want := range []string{"╭──", "│ 항목", "├──", "[1, 2]", strings.Repeat("─", 40), "│ t = (5,)", "2) email-validator 방식"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %q in\n%s", want, plain)
		}
	}
	for _, raw := range []string{"|---|", "\n---\n", "`[1, 2]`", "│    t ="} {
		if strings.Contains(plain, raw) {
			t.Fatalf("raw markdown %q left in\n%s", raw, plain)
		}
	}
	// A table row is drawn once per wrapped line, with the same width for every row.
	width := ansi.StringWidth(lines[0])
	for _, line := range lines {
		if line == "" {
			break
		}
		if ansi.StringWidth(line) != width {
			t.Fatalf("table rows are not aligned:\n%s", plain)
		}
	}
}
