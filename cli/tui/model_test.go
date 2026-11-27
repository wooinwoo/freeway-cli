package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func update(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

func press(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }

func TestPreviewInputStaysLocal(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 100, Height: 28})
	m = update(m, tea.KeyPressMsg{Code: '안', Text: "안녕"})
	m = update(m, tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	m = update(m, tea.KeyPressMsg{Code: '세', Text: "세계 🌏"})
	if m.prompt.Value() != "안녕\n세계 🌏" {
		t.Fatalf("multiline input lost: %q", m.prompt.Value())
	}
	m = update(m, press(tea.KeyEnter))
	if len(m.messages) != 1 || m.messages[0].Content != "안녕\n세계 🌏" {
		t.Fatalf("local message was not preserved: %#v", m.messages)
	}
	if !strings.Contains(m.View().Content, "Not sent") || m.prompt.Value() != "" {
		t.Fatalf("preview pretended to send a request: %#v", m)
	}
	m = update(m, press(tea.KeyEnter))
	if len(m.messages) != 1 {
		t.Fatal("empty input added a message")
	}
}

func TestModelSelectionAndFilterWithNoResults(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 60, Height: 24})
	m = update(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModAlt})
	m = update(m, press(tea.KeyDown))
	m = update(m, press(tea.KeyEnter))
	if m.selected != "preview/deepseek" || m.modelsFocused || !m.prompt.Focused() {
		t.Fatalf("model selection did not return focus: %s, %v", m.selected, m.modelsFocused)
	}
	m = update(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModAlt})
	m.models.list.SetFilterText("no-such-model")
	m = update(m, press(tea.KeyEnter))
	if m.selected != "preview/deepseek" {
		t.Fatal("empty filter changed the selected model")
	}
	m = update(m, press(tea.KeyTab))
	if m.modelsFocused || !m.prompt.Focused() {
		t.Fatal("Tab did not escape an empty model list")
	}
}

func TestResizeFitsAndPreservesDraft(t *testing.T) {
	m := New()
	m.prompt.SetValue("작은 창에서도 유지할 초안 " + strings.Repeat("가", 100))
	draft := m.prompt.Value()
	for _, size := range [][2]int{{140, 40}, {90, 24}, {89, 24}, {44, 16}, {44, 12}, {20, 5}, {1, 1}, {100, 30}} {
		m = update(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, focus := range []bool{false, true} {
			next, _ := m.changeFocus(focus)
			m = next.(Model)
			view := m.View().Content
			if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
				t.Fatalf("%dx%d focus=%v rendered %dx%d", size[0], size[1], focus, lipgloss.Width(view), lipgloss.Height(view))
			}
			if m.prompt.Value() != draft {
				t.Fatal("resize discarded the draft")
			}
		}
	}
}

func TestComposerGrowsAndKeepsLongPaste(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 120, Height: 40})
	if m.prompt.Height() != 1 || lipgloss.Height(m.View().Content) != 40 {
		t.Fatal("composer did not start with one row at the bottom")
	}
	paste := strings.Repeat("긴 대화 🌏\n", 50)
	m = update(m, tea.PasteMsg{Content: paste})
	if m.prompt.Value() != paste || m.prompt.Height() != 8 {
		t.Fatal("long paste was truncated or composer was not capped at eight rows")
	}
	if lipgloss.Height(m.View().Content) != 40 {
		t.Fatal("growing input moved the footer out of the terminal")
	}
	m = update(m, press(tea.KeyEnter))
	if m.messages[0].Content != strings.TrimSpace(paste) || m.chat.ScrollPercent() != 1 {
		t.Fatal("long conversation did not preserve its content and scroll to the last line")
	}
	if m.prompt.Height() != 1 || lipgloss.Height(m.View().Content) != 40 {
		t.Fatal("composer did not shrink after submitting")
	}
}

func TestModelMenuPreservesDraft(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 100, Height: 28})
	m = update(m, tea.PasteMsg{Content: "작성 중인 초안\n둘째 줄 🚗"})
	draft := m.prompt.Value()
	if strings.Contains(m.View().Content, "preview/deepseek") || strings.Contains(m.View().Content, "┏") {
		t.Fatal("chat view still contains a persistent model panel or thick frame")
	}
	m = update(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModAlt})
	if !strings.Contains(m.View().Content, "preview/deepseek") {
		t.Fatal("model menu did not open")
	}
	m = update(m, press(tea.KeyDown))
	m = update(m, press(tea.KeyEnter))
	if m.selected != "preview/deepseek" || m.prompt.Value() != draft || !m.prompt.Focused() {
		t.Fatal("model selection lost the draft or input focus")
	}
	m = update(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModAlt})
	m = update(m, tea.KeyPressMsg{Code: '/', Text: "/"})
	m = update(m, press(tea.KeyEsc))
	if m.modelsFocused || m.prompt.Value() != draft || !m.prompt.Focused() {
		t.Fatal("Esc during filtering failed to close the menu and preserve the draft")
	}
	if strings.Contains(m.View().Content, "preview/local") {
		t.Fatal("model menu remained visible after closing")
	}
}

func TestModelCommandsOpenMenu(t *testing.T) {
	for _, command := range []string{"/", "/m", "/model", "/models", " /model "} {
		m := update(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
		m.prompt.SetValue(command)
		m = update(m, press(tea.KeyEnter))
		if !m.modelsFocused || len(m.messages) != 0 || m.prompt.Value() != "" {
			t.Fatalf("%s was appended as a chat message instead of opening the menu", command)
		}
	}
}

func TestABareSlashShowsSixCommandsAndItsPlaceInTheList(t *testing.T) {
	// Only rows under the composer count; the welcome screen also names /models.
	listed := regexp.MustCompile(`(?m)^ *(› )?/[a-z-]+ {2,}\S`)
	below := func(view string) string { return view[strings.LastIndex(view, "╯"):] }
	m := update(New(), tea.WindowSizeMsg{Width: 80, Height: 60})
	m = update(m, tea.KeyPressMsg{Code: '/', Text: "/"})
	view := ansi.Strip(m.View().Content)
	if shown := len(listed.FindAllString(below(view), -1)); shown != 6 || !strings.Contains(view, fmt.Sprintf("Command 1/%d", len(commands))) {
		t.Fatalf("a tall window showed %d commands or hid the position:\n%s", shown, view)
	}
	m = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = update(m, press(tea.KeyUp))
	view = ansi.Strip(m.View().Content)
	last := commands[len(commands)-1]
	if !regexp.MustCompile(`› `+regexp.QuoteMeta(last.Name)+` {2,}`+last.Description).MatchString(view) || !strings.Contains(view, fmt.Sprintf("Command %d/%d", len(commands), len(commands))) {
		t.Fatalf("moving up from the first command did not show the last one and its position:\n%s", view)
	}
	if shown := len(listed.FindAllString(below(view), -1)); shown != 6 || lipgloss.Height(m.View().Content) != 24 {
		t.Fatalf("a 24-row window showed %d commands or pushed the footer out", shown)
	}
	m = update(m, tea.WindowSizeMsg{Width: 44, Height: 24})
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "↑↓ choose · Enter run · Esc close") {
		t.Fatalf("the command hint was cut short in a 44-column window:\n%s", view)
	}
}

func TestDraggingExplainsHowToSelectText(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 100, Height: 24})
	m = update(m, tea.MouseClickMsg{Button: tea.MouseLeft, X: 10, Y: 5})
	m = update(m, tea.MouseMotionMsg{Button: tea.MouseLeft, X: 20, Y: 5})
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "hold Shift while dragging") {
		t.Fatalf("a drag gave no hint about selecting text:\n%s", view)
	}
}

func TestSlashCompletion(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	m = update(m, tea.KeyPressMsg{Code: '/', Text: "/"})
	if !regexp.MustCompile(`/model +Choose a model`).MatchString(m.View().Content) {
		t.Fatal("slash command suggestion is missing")
	}
	if closed := update(m, press(tea.KeyEscape)); closed.prompt.Value() != "" || len(closed.commandMatches()) != 0 {
		t.Fatal("Esc did not close the command list")
	}
	m = update(m, press(tea.KeyTab))
	if m.prompt.Value() != "/model" || m.modelsFocused {
		t.Fatal("Tab did not complete the slash command in the composer")
	}
	if lipgloss.Height(m.View().Content) != 24 {
		t.Fatal("command suggestion pushed the footer outside the terminal")
	}
	m = update(m, press(tea.KeyEnter))
	if !m.modelsFocused || len(m.messages) != 0 || m.prompt.Value() != "" {
		t.Fatal("completed command did not open the model menu")
	}
	m = update(m, press(tea.KeyEsc))
	m = update(m, tea.PasteMsg{Content: "please explain /model"})
	m = update(m, press(tea.KeyEnter))
	if len(m.messages) != 1 || m.modelsFocused {
		t.Fatal("slash inside a normal message was treated as a command")
	}
}
