package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/wooinwoo/freeway-cli/cli/client"
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

func TestUserMessageIsABandInTheAnswerColumn(t *testing.T) {
	out, _ := New().renderUserMessage(newMessage("user", "hi there"), 40, grooveColors())
	lines := strings.Split(ansi.Strip(out), "\n")
	if len(lines) != 3 || lines[0] != strings.Repeat("▄", 40) || lines[1] != "  hi there"+strings.Repeat(" ", 30) {
		t.Fatalf("user message is not a band with text in the answer column: %q", lines)
	}
}

func TestConversationColumnStaysReadableOnWideTerminals(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 200, Height: 40})
	m.messages = []client.Message{newMessage("user", "질문"), newMessage("assistant", strings.Repeat("긴 답변 ", 80)+"\n\n---\n```go\nx := 1\n```")}
	m.renderMessages()
	lines := strings.Split(ansi.Strip(m.chat.View()), "\n")
	for _, line := range lines {
		if w := ansi.StringWidth(strings.TrimRight(line, " ")); w > 100 {
			t.Fatalf("a conversation row is %d cells wide, over 100: %q", w, line)
		}
	}
	labelled := false
	for i, line := range lines {
		labelled = labelled || strings.HasPrefix(line, "✦ FREEWAY") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "  긴 답변")
	}
	if !labelled {
		t.Fatal("the answer does not start right under its label in the answer column")
	}
	view := m.View()
	if !strings.HasPrefix(ansi.Strip(view.Content), strings.Repeat(" ", 50)+"↗ freeway") || view.Cursor == nil || view.Cursor.X < 50 {
		t.Fatal("the screen is not centered on a wide terminal")
	}
}
