package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

func TestPromptHistoryRestoresDraftAndSupportsUnicodeMultiline(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 100, Height: 28})
	for _, text := range []string{"첫 질문", "여러 줄\n코드 🌏", "최근 질문"} {
		m.remember(text)
	}
	m.prompt.SetValue("아직 보내지 않은 초안")
	for _, expected := range []string{"최근 질문", "여러 줄\n코드 🌏", "첫 질문", "첫 질문"} {
		m.prompt.MoveToBegin()
		m = update(m, press(tea.KeyUp))
		if m.prompt.Value() != expected {
			t.Fatalf("history up: got %q want %q", m.prompt.Value(), expected)
		}
	}
	for _, expected := range []string{"여러 줄\n코드 🌏", "최근 질문", "아직 보내지 않은 초안"} {
		m.prompt.MoveToEnd()
		m = update(m, press(tea.KeyDown))
		if m.prompt.Value() != expected {
			t.Fatalf("history down: got %q want %q", m.prompt.Value(), expected)
		}
	}
	m.prompt.SetValue("새로운\n여러 줄 초안")
	m = update(m, press(tea.KeyUp))
	if m.prompt.Value() != "새로운\n여러 줄 초안" {
		t.Fatal("Up replaced a multiline draft instead of moving the caret")
	}
}

type burstGateway struct {
	calls              []string
	requests           [][]client.Message
	failFirst, partial bool
	count              int
}

func (g *burstGateway) Models(context.Context) ([]client.Model, error) {
	return []client.Model{{ID: "free/auto", Name: "자동 선택"}, {ID: "free/chosen", Name: "Chosen"}}, nil
}
func (g *burstGateway) Stream(_ context.Context, model string, messages []client.Message, emit func(client.Event)) error {
	g.calls = append(g.calls, model)
	g.requests = append(g.requests, withoutToolRules(messages))
	if g.failFirst && len(g.calls) == 1 {
		if g.partial {
			emit(client.Event{Type: "text", Text: "부분 답변"})
		}
		err := &client.GatewayError{Message: "provider is unavailable", Status: 503, Code: "upstream_error"}
		emit(client.Event{Type: "error", Error: err.Error()})
		return err
	}
	n := g.count
	if n == 0 {
		n = 1
	}
	for range n {
		emit(client.Event{Type: "text", Text: "안녕🌏 "})
	}
	emit(client.Event{Type: "done", Text: strings.Repeat("안녕🌏 ", n), Model: model, Provider: "fixture"})
	return nil
}
func connectedFixture(g gateway) Model {
	m := NewConnected(context.Background(), g)
	m.startupDone = true
	m.loadingHistory = false
	m = update(m, m.loadModels()())
	return update(m, tea.WindowSizeMsg{Width: 100, Height: 28})
}
func drainResponse(t *testing.T, m Model, cmd tea.Cmd) (Model, int) {
	t.Helper()
	events := 0
	for m.busy {
		if cmd == nil {
			t.Fatal("busy without a response command")
		}
		next, following := m.Update(cmd())
		m = next.(Model)
		cmd = following
		events++
		if events > 1000 {
			t.Fatal("stream did not finish")
		}
	}
	return m, events
}

func TestTokenBurstIsCoalescedAndDraftRemainsEditable(t *testing.T) {
	g := &burstGateway{count: 5000}
	m := connectedFixture(g)
	m.prompt.SetValue("最初 질문")
	next, cmd := m.Update(press(tea.KeyEnter))
	m = next.(Model)
	m = update(m, tea.PasteMsg{Content: "다음 질문\n붙여 넣은 코드"})
	var events int
	m, events = drainResponse(t, m, cmd)
	if events > 30 {
		t.Fatalf("5000 tokens generated %d UI events", events)
	}
	if m.messages[1].Content != strings.Repeat("안녕🌏 ", 5000) || m.prompt.Value() != "다음 질문\n붙여 넣은 코드" {
		t.Fatal("stream coalescing lost output or the next draft")
	}
}

func TestSelectedModelFailureKeepsSelectionAndPrompt(t *testing.T) {
	g := &burstGateway{failFirst: true}
	m := connectedFixture(g)
	m.selected = "model/deepseek/deepseek-v4.1-flash"
	m = update(m, modelsLoaded{models: []client.Model{{ID: m.selected, Name: "DeepSeek", Free: true, Ready: true}}})
	m.prompt.SetValue("중복되면 안 되는 질문")
	next, cmd := m.Update(press(tea.KeyEnter))
	m, _ = drainResponse(t, next.(Model), cmd)
	if len(g.calls) != 1 || g.calls[0] != m.selected || m.selected != "model/deepseek/deepseek-v4.1-flash" || len(g.requests[0]) != 1 {
		t.Fatalf("failed request changed the selection or duplicated the prompt: %#v %#v", g.calls, m.messages)
	}
	if m.notice == "" {
		t.Fatal("failed request did not surface a notice")
	}
}

func TestFailureAfterOutputDoesNotSwitchModelsAndRetryKeepsDraft(t *testing.T) {
	g := &burstGateway{failFirst: true, partial: true}
	m := connectedFixture(g)
	m.selected = "free/chosen"
	m.prompt.SetValue("질문")
	next, cmd := m.Update(press(tea.KeyEnter))
	m, _ = drainResponse(t, next.(Model), cmd)
	if len(g.calls) != 1 || m.messages[1].Status != "error" {
		t.Fatal("switched routes after visible output")
	}
	m.prompt.SetValue("다음 질문 초안")
	next, cmd = m.retry()
	m, _ = drainResponse(t, next.(Model), cmd)
	if m.prompt.Value() != "다음 질문 초안" || len(m.messages) != 2 || len(g.requests[1]) != 1 {
		t.Fatal("retry duplicated the question, sent failed output, or discarded the next draft")
	}
}

type memoryGateway struct {
	burstGateway
	saved        []client.Conversation
	chat         client.Conversation
	usedSettings client.Settings
}

func (g *memoryGateway) Conversations(context.Context) ([]client.ConversationSummary, error) {
	return []client.ConversationSummary{{ID: g.chat.ID, Title: g.chat.Title}}, nil
}
func (g *memoryGateway) Conversation(context.Context, string) (client.Conversation, error) {
	return g.chat, nil
}
func (g *memoryGateway) SaveConversation(_ context.Context, chat client.Conversation) (client.Conversation, error) {
	if chat.ID == "" {
		chat.ID = "fixture-conversation"
	}
	chat.Revision++
	g.saved = append(g.saved, chat)
	g.chat = chat
	return chat, nil
}

func (g *memoryGateway) StreamWithSettings(ctx context.Context, model string, messages []client.Message, settings client.Settings, emit func(client.Event)) error {
	g.usedSettings = settings
	return g.Stream(ctx, model, messages, emit)
}

func TestCancelWhileInitialSavePendingDoesNotStartGeneration(t *testing.T) {
	g := &memoryGateway{}
	m := connectedFixture(g)
	m.prompt.SetValue("취소할 질문")
	next, save := m.Update(press(tea.KeyEnter))
	m = next.(Model)
	if !m.saving || m.afterSave != "generate" {
		t.Fatal("question was not saved before generation")
	}
	m = update(m, press(tea.KeyEsc))
	m = update(m, save())
	if m.busy || m.afterSave != "" || len(g.calls) > 0 {
		t.Fatal("a cancelled save completion restarted inference")
	}
	if m.session.ID == "" || m.session.Revision != 1 {
		t.Fatal("saved session identity was lost")
	}
}

func TestModelChangeDuringInitialSaveAppliesToNextQuestion(t *testing.T) {
	g := &memoryGateway{}
	m := connectedFixture(g)
	m.prompt.SetValue("Use the model selected at send time")
	next, save := m.Update(press(tea.KeyEnter))
	m = next.(Model)
	if !m.saving || m.afterSave != "generate" {
		t.Fatal("question was not saved before generation")
	}
	m = update(m, press(tea.KeyF2))
	m = update(m, press(tea.KeyDown))
	m = update(m, press(tea.KeyEnter))
	if m.selected != "free/chosen" || m.toast != "Next question · Chosen" {
		t.Fatalf("model picker did not explain the next selection: %q, %q", m.selected, m.toast)
	}
	m.notify("Queued another question.")
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Auto · Next Chosen") {
		t.Fatalf("the conversation lost the active or next model after another notification:\n%s", view)
	}
	originalSettings := m.settings
	m.menu = "settings"
	next, _ = m.chooseItem(modelItem{ID: "8192", Name: "Long"})
	m = next.(Model)
	m, _ = drainResponse(t, m, save)
	nextConfig := m.snapshot()
	if len(g.calls) != 1 || g.calls[0] != "free/auto" || nextConfig.Model != "free/chosen" || g.usedSettings != originalSettings || nextConfig.Settings.MaxTokens != 8192 {
		t.Fatalf("current response: %v %+v; next selection: %q %+v", g.calls, g.usedSettings, nextConfig.Model, nextConfig.Settings)
	}
}

func TestNavigationSavesEditsMadeWhileSaveWasPending(t *testing.T) {
	g := &memoryGateway{}
	m := connectedFixture(g)
	m.messages = []client.Message{newMessage("user", "기존 질문")}
	m.prompt.SetValue("초안")
	m.dirty = true
	save := m.persist()
	next, _ := m.navigate("new")
	m = next.(Model)
	m = update(m, tea.PasteMsg{Content: " 추가"})
	next, resave := m.Update(save())
	m = next.(Model)
	if resave == nil {
		t.Fatal("new conversation discarded edits made during a pending save")
	}
	m = update(m, resave())
	if len(g.saved) != 2 || g.saved[1].Draft != "초안 추가" || len(m.messages) != 0 || m.prompt.Value() != "" {
		t.Fatalf("navigation lost draft or did not reset: %#v %q", g.saved, m.prompt.Value())
	}
}

func TestCopyCodeAndExportPreserveRawText(t *testing.T) {
	m := New()
	m.prefs.Language = "ko"
	raw := "# 코드\n```go\n  fmt.Println(\"안녕🌏\")\n```"
	m.messages = []client.Message{newMessage("assistant", raw)}
	copied := ""
	m.copyText = func(s string) error { copied = s; return nil }
	_, cmd := m.copyAnswer(false)
	m = update(m, cmd())
	if copied != raw || m.toast != "응답을 복사했습니다." {
		t.Fatalf("unexpected response copy: %q, %q", copied, m.toast)
	}
	_, cmd = m.copyAnswer(true)
	m = update(m, cmd())
	if copied != "  fmt.Println(\"안녕🌏\")" || m.toast != "코드를 복사했습니다." {
		t.Fatalf("unexpected code copy: %q, %q", copied, m.toast)
	}
	m.copyText = func(string) error { return os.ErrNotExist }
	_, cmd = m.copyAnswer(true)
	next, osc := m.Update(cmd())
	m = next.(Model)
	if osc == nil || m.toast != "코드를 복사했습니다 (터미널 클립보드)." {
		t.Fatalf("missing clipboard tool did not fall back to the terminal: %q", m.toast)
	}
	path := filepath.Join(t.TempDir(), "대화.md")
	_, cmd = m.export(path)
	m = update(m, cmd())
	if m.toast != "대화를 내보냈습니다. "+path {
		t.Fatalf("unexpected export result: %q", m.toast)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), raw) {
		t.Fatalf("export changed code: %s %v", data, err)
	}
	if !strings.Contains(string(data), "## 답변\n") || strings.Contains(string(data), "## Freeway") {
		t.Fatalf("export headings ignore the language in use:\n%s", data)
	}
	_, cmd = m.export(path)
	if result := cmd().(actionResult); result.err == nil {
		t.Fatal("export overwrote an existing file")
	}
}

func TestAllMenusFitSmallWindowsAndKeepDraft(t *testing.T) {
	for _, size := range [][2]int{{100, 30}, {60, 18}, {44, 12}} {
		for _, menu := range []string{"models", "help", "settings", "history"} {
			m := update(New(), tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.prompt.SetValue("작성 중인 초안")
			m.history = []client.ConversationSummary{{ID: "sample", Title: strings.Repeat("긴 대화 제목", 20), Preview: strings.Repeat("내용", 50)}}
			var next tea.Model
			if menu == "models" {
				next, _ = m.changeFocus(true)
			} else {
				next, _ = m.openMenu(menu)
			}
			m = next.(Model)
			view := m.View().Content
			if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
				t.Fatalf("%s at %v overflowed: %dx%d", menu, size, lipgloss.Width(view), lipgloss.Height(view))
			}
			m = update(m, press(tea.KeyEsc))
			if m.prompt.Value() != "작성 중인 초안" {
				t.Fatal("menu discarded input")
			}
		}
	}
}

func TestStreamRenderingCachesHistoryAndDoesNotDragScrollToBottom(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	for range 20 {
		m.messages = append(m.messages, newMessage("user", strings.Repeat("예전 대화\n", 6)), newMessage("assistant", strings.Repeat("답변\n", 6)))
	}
	m.renderMessages()
	m.chat.GotoTop()
	before := m.renderCache[0].text
	m.messages[len(m.messages)-1].Content += "새 토큰"
	m.renderMessages()
	if m.renderCache[0].text != before || m.chat.YOffset() != 0 {
		t.Fatal("stream discarded cached history or moved the user away from earlier messages")
	}
	m.lastRender = time.Now()
	m.renderPending = true
	m = update(m, tickMsg(time.Now()))
	if m.renderPending {
		t.Fatal("pending final text was not drawn on the next frame")
	}
}

func TestMenuTypeAheadAndQuickSelectPreserveComposer(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 100, Height: 28})
	m.prompt.SetValue("Keep this draft")
	next, _ := m.changeFocus(true)
	m = next.(Model)
	for _, r := range "deepseek" {
		m = update(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if len(m.models.list.Items()) != 1 || m.menuQuery != "deepseek" {
		t.Fatal("typing did not filter the menu immediately")
	}
	m = update(m, press(tea.KeyEnter))
	if m.modelsFocused || m.selected != "preview/deepseek" || m.prompt.Value() != "Keep this draft" {
		t.Fatal("search required a second Enter or discarded the draft")
	}
	next, _ = m.changeFocus(true)
	m = next.(Model)
	m = update(m, tea.KeyPressMsg{Code: '3', Text: "3"})
	if m.modelsFocused || m.selected != "preview/local" {
		t.Fatal("number shortcut did not choose the visible item")
	}
}

func TestEnglishWorkspaceHasRoleLabelsAndNativeCursor(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 100, Height: 28})
	empty := m.View().Content
	if !strings.Contains(empty, "What would you like to make?") || !strings.Contains(empty, "AI workspace") {
		t.Fatal("POC welcome and header are missing")
	}
	m.messages = []client.Message{newMessage("user", "Review this function"), newMessage("assistant", "```go\nfunc answer() int {\n    return 42\n}\n```")}
	m.renderMessages()
	m.prompt.SetValue("Next question")
	view := m.View()
	plain := ansi.Strip(view.Content)
	if !strings.Contains(plain, "Review this function") || !strings.Contains(plain, "✦") || !strings.Contains(plain, "╭─ go") {
		t.Fatal("conversation roles or code frame are missing")
	}
	if view.Cursor == nil || view.Cursor.Y < 0 || view.Cursor.Y >= m.height || m.prompt.VirtualCursor() {
		t.Fatal("native terminal cursor was not positioned inside the composer")
	}
	for _, r := range view.Content {
		if r >= 0xac00 && r <= 0xd7a3 {
			t.Fatal("Korean interface text leaked into the English workspace")
		}
	}
}

func TestTypingACommandInANewChatDoesNotSaveAnEmptyConversation(t *testing.T) {
	g := &memoryGateway{}
	m := connectedFixture(g)
	m.prompt.SetValue("/sett")
	m.dirty = true
	if cmd := m.persist(); cmd != nil || len(g.saved) != 0 {
		t.Fatal("a pending slash command created an empty saved conversation")
	}
}
