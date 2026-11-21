package tui

import (
	"context"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

type providerGatewayFixture struct{ fixtureGateway }

func (providerGatewayFixture) ProviderStatuses(context.Context) ([]client.ProviderStatus, error) {
	return []client.ProviderStatus{
		{ID: "kilo", Name: "Kilo", State: "ready", FreeModels: 2},
		{ID: "groq", Name: "Groq", State: "needs_key"},
	}, nil
}

type openCodeGatewayFixture struct{ fixtureGateway }

func (openCodeGatewayFixture) CanOpenCode() bool { return true }

func (openCodeGatewayFixture) OpenCodeModels(context.Context) ([]client.Model, error) {
	return []client.Model{{ID: "opencode/fixture", Name: "Fixture OpenCode", Free: true, Ready: true}}, nil
}

func TestOpenCodeIsSelectableInTheChat(t *testing.T) {
	m := update(NewConnected(t.Context(), openCodeGatewayFixture{}), tea.WindowSizeMsg{Width: 80, Height: 24})
	m.prompt.SetValue("Keep this draft")
	m = update(m, press(tea.KeyF2))
	next, cmd := m.Update(press(tea.KeyEnter))
	m = update(next.(Model), cmd())
	if !strings.Contains(ansi.Strip(m.View().Content), "Fixture OpenCode") {
		t.Fatal("the OpenCode picker did not load")
	}
	m = update(m, press(tea.KeyEnter))
	m.ready = true
	m.ensureSelected()
	if m.selected != "opencode/fixture" || m.modelsFocused || m.prompt.Value() != "Keep this draft" {
		t.Fatal("OpenCode selection was lost or changed the draft")
	}
}

func TestModelMenuRefreshPreservesHighlightedChoice(t *testing.T) {
	m := update(NewConnected(t.Context(), openCodeGatewayFixture{}), tea.WindowSizeMsg{Width: 80, Height: 24})
	models := []client.Model{{ID: "free/auto", Name: "Auto"}, {ID: "kilo/one", Name: "One"}, {ID: "kilo/two", Name: "Two"}}
	m = update(m, modelsLoaded{models: models})
	m = update(m, press(tea.KeyF2))
	m = update(m, press(tea.KeyDown))
	m = update(m, press(tea.KeyDown))
	m = update(m, modelsLoaded{models: []client.Model{models[0], models[2], models[1]}})
	if item := m.models.list.SelectedItem().(modelItem); item.ID != "kilo/one" || m.selected != "free/auto" {
		t.Fatal("a catalog refresh changed the highlighted choice or current model")
	}
	if !strings.Contains(m.View().Content, "✓ Current") {
		t.Fatal("the current model was not marked")
	}
}

func TestMarkdownWrapsListsAndQuotesWithoutBreakingWords(t *testing.T) {
	result := ansi.Strip(markdown("- Keep the words together\n> Longer quoted text\n`**literal**`\n```go\n    call()\n```", 15))
	for _, want := range []string{"• Keep the\n  words", "│ Longer quoted\n│ text", "**literal**", "    call()"} {
		if !strings.Contains(result, want) {
			t.Fatalf("missing %q in %q", want, result)
		}
	}
}

func TestProviderStatusMenuPreservesDraft(t *testing.T) {
	m := update(NewConnected(t.Context(), providerGatewayFixture{}), tea.WindowSizeMsg{Width: 80, Height: 24})
	m.prompt.SetValue("Keep this draft")
	next, cmd := m.Update(press(tea.KeyF4))
	m = update(next.(Model), cmd())
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Guest · 2 free models") || !strings.Contains(view, "Key required") {
		t.Fatalf("provider status missing from view: %s", view)
	}
	m = update(m, press(tea.KeyDown))
	if !strings.Contains(ansi.Strip(m.View().Content), "freeway providers auth groq") {
		t.Fatal("missing key did not show the provider auth command")
	}
	m = update(m, press(tea.KeyEsc))
	if m.modelsFocused || m.prompt.Value() != "Keep this draft" || !m.prompt.Focused() {
		t.Fatal("provider status changed the current draft")
	}
}
func TestJumpToLatestButton(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {44, 12}} {
		m := update(New(), tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.messages = append(m.messages, newMessage("assistant", strings.Repeat("Earlier reply\n\n", 35)+"Latest reply"))
		m.prompt.SetValue("Draft")
		m.busy = true
		m.renderMessages()
		m.chat.GotoBottom()
		if strings.Contains(m.View().Content, "Jump to latest") {
			t.Fatal("jump button appeared at the bottom")
		}
		m = update(m, press(tea.KeyPgUp))
		view := m.View().Content
		if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
			t.Fatalf("jump button overflowed at %v", size)
		}
		x, y := m.latestButtonPosition()
		line := ansi.Strip(strings.Split(view, "\n")[y])
		start := strings.Index(line, latestButtonLabel)
		if start < 0 || ansi.StringWidth(line[:start]) != x {
			t.Fatalf("jump button does not align with its hit area: %q", line)
		}
		m = update(m, tea.MouseClickMsg{X: x - 1, Y: y, Button: tea.MouseLeft})
		if m.chat.AtBottom() {
			t.Fatal("clicking outside the button jumped to the bottom")
		}
		m = update(m, tea.MouseClickMsg{X: x + 2, Y: y, Button: tea.MouseLeft})
		if !m.chat.AtBottom() || m.prompt.Value() != "Draft" || strings.Contains(m.View().Content, "Jump to latest") {
			t.Fatal("click did not jump to the bottom and preserve the draft")
		}
	}
}

func TestTranscriptSearchKeepsDraftAndRestoresOrJumps(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	m.messages = append(m.messages, newMessage("assistant", "Old answer: Result [one]"))
	for range 12 {
		m.messages = append(m.messages, newMessage("user", "More context"))
	}
	m.messages = append(m.messages, newMessage("assistant", "Recent answer: RESULT [ONE]"))
	m.prompt.SetValue("Unsent 🌏")
	m.renderMessages()
	m.chat.GotoBottom()
	bottom := m.chat.YOffset()

	openSearch := func(m Model) Model {
		m = update(m, press(tea.KeyF3))
		m = update(m, tea.PasteMsg{Content: "result [one]"})
		if m.find == nil || len(m.find.matches) != 2 || m.View().Cursor == nil {
			t.Fatalf("F3 did not find both literal matches or show the cursor: %s", m.View().Content)
		}
		return update(m, press(tea.KeyDown))
	}
	m = openSearch(m)
	if m.chat.YOffset() >= bottom || !strings.Contains(m.View().Content, "Old answer:") {
		t.Fatal("next result did not wrap back to the old answer")
	}
	m = update(m, press(tea.KeyEsc))
	if m.find != nil || m.chat.YOffset() != bottom || m.prompt.Value() != "Unsent 🌏" || !m.prompt.Focused() {
		t.Fatal("Escape did not restore the original position and composer")
	}
	m = openSearch(m)
	position := m.chat.YOffset()
	m = update(m, press(tea.KeyEnter))
	if m.find != nil || m.chat.YOffset() != position || m.prompt.Value() != "Unsent 🌏" || !m.prompt.Focused() {
		t.Fatal("Enter did not keep the result position and composer")
	}
}

func TestFilePickerKeepsTypedPathAndFitsWindow(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {44, 12}} {
		m := update(New(), tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.messages = append(m.messages, newMessage("assistant", "Previous answer"))
		m.renderMessages()
		m.fileItems = []list.Item{modelItem{ID: "docs/index.md", Name: "docs/index.md"}, modelItem{ID: "lib/3main.go", Name: "lib/3main.go"}}
		m.prompt.SetValue("Read ")
		m = update(m, tea.KeyPressMsg{Code: '@', Text: "@"})
		m = update(m, tea.KeyPressMsg{Code: '3', Text: "3"})
		if len(m.models.list.Items()) != 1 || m.menuQuery != "3" || !m.modelsFocused {
			t.Fatal("a leading number should filter the file list")
		}
		m = update(m, ctrl('u'))
		m = update(m, tea.PasteMsg{Content: `lib\3ma`})
		if item, ok := m.models.list.SelectedItem().(modelItem); !ok || item.ID != "lib/3main.go" {
			t.Fatal("file search did not accept a Windows path")
		}
		view := m.View().Content
		if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
			t.Fatalf("file picker overflowed at %v: %dx%d", size, lipgloss.Width(view), lipgloss.Height(view))
		}
		if size[1] >= 18 && !strings.Contains(view, "Previous answer") {
			t.Fatal("inline picker hid the previous answer")
		}
		m = update(m, press(tea.KeyEsc))
		if m.modelsFocused || m.prompt.Value() != `Read @lib\3ma` || len(m.files) != 0 {
			t.Fatalf("Escape changed the draft or attached a file: %q", m.prompt.Value())
		}
	}
}

func TestPromptHistoryShowsPreviewAndStashesDraft(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	m.remember("First prompt")
	m.remember("2026 status\nSecond line")
	m.prompt.SetValue("Current draft")
	m = update(m, ctrl('r'))
	m = update(m, tea.PasteMsg{Content: "2026"})
	view := m.View().Content
	if !strings.Contains(view, "Prompt preview") || !strings.Contains(view, "Second line") || lipgloss.Height(view) > 24 {
		t.Fatal("prompt preview is missing or overflows")
	}
	m = update(m, press(tea.KeyEnter))
	if m.prompt.Value() != "2026 status\nSecond line" || m.stash == nil || m.stash.Text != "Current draft" {
		t.Fatal("loading a prompt did not stash the existing draft")
	}
	m = update(m, ctrl('s'))
	if m.prompt.Value() != "Current draft" {
		t.Fatal("the draft could not be recovered")
	}
}
