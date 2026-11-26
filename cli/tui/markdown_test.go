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

func TestTablesFollowTheDividerAlignmentAndFitShortColumns(t *testing.T) {
	rendered, _ := markdown("| 회차 | 매출 | 메모 |\n|---|--:|:-:|\n| 1회 | 1,120 | a |\n| 10회 | 21,268.8 | bbb |", 60)
	plain := ansi.Strip(rendered)
	for _, want := range []string{"│ 1회  │    1,120 │  a   │", "│ 10회 │ 21,268.8 │ bbb  │"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("columns ignore the divider's alignment, missing %q in\n%s", want, plain)
		}
	}
	if narrow, _ := markdown("| a | b | c | d | e | f | g | h |\n|---|---|---|---|---|---|---|---|\n| 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 |", 40); strings.Contains(narrow, "|---") {
		t.Fatalf("a table of short cells that fits was left as raw text:\n%s", narrow)
	}
}

func TestUserMessageIsABandInTheAnswerColumn(t *testing.T) {
	out, _ := New().renderUserMessage(newMessage("user", "hi there"), 40, grooveColors())
	lines := strings.Split(ansi.Strip(out), "\n")
	if len(lines) != 3 || lines[0] != strings.Repeat("▄", 40) || lines[1] != "  hi there"+strings.Repeat(" ", 30) {
		t.Fatalf("user message is not a band with text in the answer column: %q", lines)
	}
}

func TestConversationUsesTheFullWidthOfAWideTerminal(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 200, Height: 40})
	m.messages = []client.Message{newMessage("user", "질문"), newMessage("assistant", strings.Repeat("긴 답변 ", 80))}
	m.renderMessages()
	view := ansi.Strip(m.View().Content)
	if !strings.HasPrefix(view, "  ↗ freeway") || !strings.Contains(view, "  "+strings.Repeat("▄", 196)) {
		t.Fatal("a wide terminal left empty margins instead of using its full width")
	}
	lines := strings.Split(ansi.Strip(m.chat.View()), "\n")
	labelled := false
	for i, line := range lines {
		labelled = labelled || strings.HasPrefix(line, "✦") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "  긴 답변")
	}
	if !labelled {
		t.Fatal("the answer does not start right under its label in the answer column")
	}
}

func TestMarkdownDrawsInlineLinksItalicsTasksAndSections(t *testing.T) {
	rendered, _ := markdown("intro\n## Part\nsee [docs](https://x.dev) and *this*, but 2*3*4 stays\n- [ ] todo\n- [x] done\n```\nplain\n```", 60)
	plain := ansi.Strip(rendered)
	for _, want := range []string{"intro\n\nPart", "see docs (https://x.dev) and this, but 2*3*4 stays", "[ ] todo", "[✓] done", "╭──"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %q in\n%s", want, plain)
		}
	}
	for _, raw := range []string{"[docs]", "*this*", "• [", "─ code ─"} {
		if strings.Contains(plain, raw) {
			t.Fatalf("raw markup %q left in\n%s", raw, plain)
		}
	}
}

func TestConversationRowsForWaitingUnansweredAndNoColor(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	m.messages = []client.Message{newMessage("user", "질문"), newMessage("assistant", "")}
	m.busy = true
	m.renderMessages()
	if !strings.Contains(ansi.Strip(m.chat.View()), "✦  Thinking…") {
		t.Fatal("the waiting answer does not fit on its label row")
	}
	m.busy = false
	m.messages = m.messages[:1]
	m.renderMessages()
	if !strings.Contains(ansi.Strip(m.chat.View()), "No reply · Alt+R to regenerate") {
		t.Fatal("a question without an answer gives no way to retry")
	}
	m.prefs.NoColor = true
	out, _ := m.renderUserMessage(newMessage("user", "질문"), 40, grooveColors())
	if ansi.Strip(out) != "│ 질문"+strings.Repeat(" ", 34) {
		t.Fatalf("NO_COLOR question is not a single ruled row: %q", ansi.Strip(out))
	}
}

func TestComposerShowsTheLineCountOfALongPaste(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	m = update(m, tea.PasteMsg{Content: strings.Repeat("줄\n", 19) + "끝"})
	if !strings.Contains(m.View().Content, "Message · 20 lines") {
		t.Fatal("the composer does not say how long a pasted draft is")
	}
}

func TestOfflineWelcomeBlamesTheNetwork(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	m.providers = map[string]client.ProviderStatus{"kilo": {ID: "kilo", Name: "Kilo", State: "unreachable"}}
	m.status, m.notice = "Connection needs attention", "No free connection is ready for this model."
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Check your internet connection") || strings.Contains(view, "No free connection") {
		t.Fatal("the offline welcome does not match the header's internet hint")
	}
}

func TestFilePickerInAnEmptyFolderSaysSoInsteadOfNoMatches(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	m.menu, m.fileItems, m.filesLoading = "files", nil, false
	m.models.list.SetItems(nil)
	if got := ansi.Strip(m.menuItemsView()); !strings.Contains(got, "No text files to attach in this folder.") {
		t.Fatalf("empty folder message: %q", got)
	}
}

func TestHeadingLevelsLookDifferent(t *testing.T) {
	rendered, _ := markdown("# One\n## Two\n### Three", 40)
	rows := strings.Split(rendered, "\n")
	one, two, three := rows[0], rows[2], rows[4]
	if ansi.Strip(one) != "One" || ansi.Strip(two) != "Two" || ansi.Strip(three) != "Three" || one == two || two == three || one == three {
		t.Fatalf("heading levels are drawn alike: %q", rows)
	}
}

func TestSmallestWindowKeepsTheWholeHelpHint(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 44, Height: 12})
	if !strings.Contains(ansi.Strip(m.View().Content), "? help") {
		t.Fatal("the help hint is cut off in a 44-column window")
	}
}
