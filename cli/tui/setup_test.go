package tui

import (
	"context"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

func TestSetupKeepsKeyMaskedAndFitsSmallTerminal(t *testing.T) {
	const entered = "fixture-secret-token"
	input := textinput.New()
	input.EchoMode = textinput.EchoPassword
	input.Focus()
	m := setupModel{SetupDialog: SetupDialog{Title: "API 키 입력", Secret: true}, prefs: Preferences{Language: "ko"}, input: input}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 44, Height: 12})
	next, _ = next.Update(tea.PasteMsg{Content: entered})
	m = next.(setupModel)
	view := m.View().Content
	if m.input.Value() != entered || strings.Contains(ansi.Strip(view), entered) || lipgloss.Width(view) > 44 || lipgloss.Height(view) > 12 {
		t.Fatal("the masked key field lost input, exposed the key, or overflowed")
	}
	next, _ = m.Update(press(tea.KeyEnter))
	m = next.(setupModel)
	if m.result != entered || m.input.Value() != "" || strings.Contains(ansi.Strip(m.View().Content), entered) {
		t.Fatal("submission did not clear the visible field")
	}
}

func TestSetupStatusKeepsTokenNameVisibleOnSmallTerminal(t *testing.T) {
	m := setupModel{
		SetupDialog: SetupDialog{
			Title: "이미지 MCP", Detail: "fixture_image · https://mcp.fixture.test/mcp",
			Check:   func(context.Context) (string, string) { return "", "" },
			Options: []SetupOption{{ID: "change", Label: "서버 또는 도구 변경"}, {ID: "check", Label: "연결 상태 다시 확인"}},
		},
		prefs: Preferences{Language: "ko"}, width: 44, height: 12, index: 1,
	}
	next, _ := m.Update(setupCheckResult{index: -1, status: "토큰 필요", detail: "토큰 환경변수를 설정하세요 FREEWAY_MCP_IMAGE_TOKEN"})
	m = next.(setupModel)
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "토큰 필요") || !strings.Contains(view, "FREEWAY_MCP_IMAGE_TOKEN") || !strings.Contains(view, "Esc 뒤로") || m.index != 1 {
		t.Fatalf("status hid the token name, navigation help or selected action:\n%s", view)
	}
}

func TestThemePickerPreviewsConversationAndRestoresOnCancel(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 44, Height: 12}, {Width: 80, Height: 24}} {
		for _, name := range []string{"XDG_CONFIG_HOME", "HOME", "APPDATA", "USERPROFILE"} {
			t.Setenv(name, t.TempDir())
		}
		original := Preferences{Language: "ko", Theme: "dark"}
		if err := original.Save(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { applyTheme("system") })
		m := update(New().WithPreferences(original), size)
		m.prompt.SetValue("Unsent draft")
		m.messages = []client.Message{{Role: "assistant", Content: "Visible reply", Status: "done"}}
		m.renderMessages()
		dark := m.View().BackgroundColor
		before := m.renderCache[0].text
		next, _ := m.execute("/theme", "")
		m = update(next.(Model), press(tea.KeyDown))
		view := m.View()
		text := ansi.Strip(view.Content)
		if view.BackgroundColor == nil || view.BackgroundColor == dark || m.renderCache[0].text == before || m.prefs.Theme != "dark" || LoadPreferences().Theme != "dark" {
			t.Fatal("moving the selection must preview the entire theme without saving")
		}
		if !strings.Contains(text, "Visible reply") || !strings.Contains(text, "Enter 저장") || lipgloss.Height(view.Content) > size.Height || lipgloss.Width(view.Content) > size.Width {
			t.Fatalf("theme picker hid the reply or did not fit %dx%d:\n%s", size.Width, size.Height, text)
		}
		m = update(m, press(tea.KeyEsc))
		if m.View().BackgroundColor != dark || m.renderCache[0].text != before || m.prompt.Value() != "Unsent draft" || m.modelsFocused {
			t.Fatal("cancel must restore the original palette and draft")
		}
		next, _ = m.openMenu("settings")
		m = update(next.(Model), press(tea.KeyDown))
		m = update(m, press(tea.KeyEnter))
		m = update(m, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
		m = update(m, press(tea.KeyEnter))
		if m.View().BackgroundColor != view.BackgroundColor || LoadPreferences().Theme != "light" || m.prompt.Value() != "Unsent draft" || m.modelsFocused {
			t.Fatal("confirm must keep the previewed palette and save it for the next launch")
		}
	}
}

func TestPreferencesKeepDraftAndPersistAcrossLaunches(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"XDG_CONFIG_HOME", "HOME", "APPDATA", "USERPROFILE"} {
		t.Setenv(name, dir)
	}
	t.Cleanup(func() { applyTheme("system") })
	if LoadPreferences().appearanceConfigured {
		t.Fatal("default preferences should prompt for appearance")
	}
	m := update(New().WithPreferences(Preferences{Language: "en", Theme: "system"}), tea.WindowSizeMsg{Width: 80, Height: 24})
	m.prompt.SetValue("Test draft")
	next, _ := m.execute("/language", "")
	next, _ = next.(Model).chooseItem(modelItem{ID: "ko"})
	m = next.(Model)
	if !strings.Contains(ansi.Strip(m.View().Content), "무엇을 만들어 볼까요?") || m.prompt.Value() != "Test draft" {
		t.Fatal("Korean was not applied or the draft was modified")
	}
	next, _ = m.execute("/theme", "")
	next, _ = next.(Model).chooseItem(modelItem{ID: "light"})
	m = next.(Model)
	prefs := LoadPreferences()
	if m.View().BackgroundColor == nil || !prefs.appearanceConfigured || prefs.Language != "ko" || prefs.Theme != "light" {
		t.Fatalf("theme or language did not persist: %+v", prefs)
	}
}
