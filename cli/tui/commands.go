package tui

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/wooinwoo/freeway-cli/cli/client"
	"github.com/wooinwoo/freeway-cli/cli/imagegen"
)

type command struct{ Name, Description, Korean string }

var commands = []command{
	{"/model", "Choose a model", "모델 선택"}, {"/history", "Search and resume conversations", "대화 검색 및 이어서 시작"}, {"/new", "New conversation", "새 대화"},
	{"/providers", "Check provider connections and free models", "공급처 연결 및 무료 모델 확인"},
	{"/context", "Review or limit what the next request sends", "다음 요청에 보낼 문맥 확인 및 제한"},
	{"/opencode", "Filter models by OpenCode", "OpenCode 모델 검색"},
	{"/continue", "Continue the latest session in this project", "현재 프로젝트의 최근 대화 이어서"},
	{"/find", "Find text in this conversation", "현재 대화에서 검색"},
	{"/retry", "Regenerate the last response", "마지막 응답 다시 생성"}, {"/retry-shorter", "Retry with a shorter reply", "더 짧은 응답으로 다시 시도"}, {"/copy", "Copy the last response", "마지막 응답 복사"}, {"/code", "Copy the last code block", "마지막 코드 블록 복사"},
	{"/export", "Export conversation to Markdown", "대화를 Markdown으로 내보내기"}, {"/settings", "Preferences and response length", "화면 설정 및 응답 길이"},
	{"/language", "Change interface language", "표시 언어 변경"}, {"/theme", "Change terminal colors", "색상 테마 변경"},
	{"/auto", "Choose a free model automatically", "무료 모델 자동 선택"}, {"/reconnect", "Reconnect", "다시 연결"}, {"/save", "Save now", "즉시 저장"},
	{"/recover", "Save a plain-text recovery file and continue after a failed save", "저장 실패 뒤 일반 텍스트 복구 파일을 저장하고 계속"},
	{"/restore", "Recover a draft from the latest backup or a given JSON path", "최근 백업이나 지정한 JSON 경로에서 초안 복구"},
	{"/image", "Generate an image from text", "텍스트로 이미지 생성"},
	{"/image-setup", "Connect an MCP image tool", "MCP 이미지 도구 연결"},
	{"/mcp", "Manage MCP server connections", "MCP 서버 연결 관리"},
	{"/mcp-ask", "Send a question and tool summaries to the selected model", "질문과 도구 요약을 선택한 모델에 보내기"},
	{"/image-open", "Open the latest generated image", "최근 생성 이미지 열기"},
	{"/image-path", "Copy the latest image path", "최근 이미지 경로 복사"},
	{"/help", "Keyboard shortcuts and commands", "단축키와 명령"}, {"/exit", "Save and quit", "저장하고 종료"},
	{"/attach", "Attach a project text file", "프로젝트의 텍스트 파일 첨부"}, {"/detach", "Remove attached files", "첨부 파일 제거"}, {"/queue", "Review queued follow-ups", "예약한 후속 질문 확인"},
	{"/editor", "Edit the draft in VISUAL or EDITOR", "VISUAL 또는 EDITOR에서 초안 편집"}, {"/prompts", "Search prompt history", "질문 기록 검색"}, {"/fork", "Continue in a new conversation", "현재 대화를 분기"}, {"/rename", "Rename this conversation", "현재 대화 이름 변경"}, {"/resume", "Open a saved conversation", "저장된 대화 열기"}, {"/clear", "Start a new conversation", "새 대화 시작"},
}

type tickMsg time.Time
type reconnectMsg struct{}
type actionResult struct {
	text string
	err  error
	// clipboard is text that failed to reach the system clipboard; the terminal can still copy it.
	clipboard string
}

func tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// nextTick keeps the 100 ms cadence only while work is pending. Each tick redraws
// the whole screen, so an idle screen refreshes once per second instead.
func (m Model) nextTick() tea.Cmd {
	if m.busy || m.dirty || m.pendingSave != nil || m.saving || m.renderPending || m.recovering ||
		len(m.queue) > 0 || !m.modelsStarted || time.Now().Before(m.toastUntil) {
		return tick()
	}
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) commandMatches() []command {
	value := m.prompt.Value()
	if !strings.HasPrefix(value, "/") || strings.ContainsAny(value, " \n\t") {
		return nil
	}
	var matches []command
	for _, c := range commands {
		if strings.HasPrefix(c.Name, value) {
			matches = append(matches, c)
		}
	}
	return matches
}
func (m Model) commandPending() bool { return len(m.commandMatches()) > 0 }
func (m Model) commandSuggestion() command {
	matches := m.commandMatches()
	if len(matches) == 0 {
		return command{}
	}
	return matches[max(0, min(m.commandIndex, len(matches)-1))]
}

func (m Model) shortcut(key tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	keyName := key.String()
	if keyName == "f2" || keyName == "alt+p" {
		next, cmd := m.openModels()
		return next, cmd, true
	}
	if keyName == "f9" && !m.modelsFocused {
		next, cmd := m.setupImage()
		return next, cmd, true
	}
	if keyName == "f8" {
		if m.modelsFocused && m.menu == "context" {
			next, cmd := m.changeFocus(false)
			return next, cmd, true
		}
		next, cmd := m.openMenu("context")
		return next, cmd, true
	}
	if keyName == "f4" && (!m.modelsFocused || m.menu == "models") ||
		keyName == "ctrl+r" && m.modelsFocused && m.menu == "providers" {
		next, cmd := m.showProviders()
		return next, cmd, true
	}
	if keyName == "ctrl+shift+y" {
		next, cmd := m.copyAnswer(false)
		return next, cmd, true
	}
	if m.modelsFocused {
		if keyName == "ctrl+r" && m.menu == "models" && m.gateway != nil {
			next, cmd := m.refreshModelPicker(false)
			return next, cmd, true
		}
		if keyName == "ctrl+t" && m.menu == "models" {
			next, cmd := m.changeModelRanking()
			return next, cmd, true
		}
		if keyName == "ctrl+a" && m.menu == "history" {
			query := m.menuQuery
			m.allSessions = !m.allSessions
			if m.historyPage.paged {
				m.history = nil
				m.historyPage.cursor, m.historyPage.ready = "", false
				m.menuAll = nil
				m.models.list.SetItems(nil)
				next, cmd := m.fetchHistoryPage(false)
				return next, cmd, true
			}
			m.history = client.ScopeConversations(m.allHistory, m.workdir, m.allSessions)
			next, cmd := m.openMenu("history")
			model := next.(Model)
			if query != "" {
				model.menuQuery, model.menuSearching = query, true
				cmd = tea.Batch(cmd, model.applyMenuFilter())
			}
			return model, cmd, true
		}
		if keyName == "ctrl+r" && m.menu == "history" && m.historyPage.paged {
			next, cmd := m.fetchHistoryPage(false)
			return next, cmd, true
		}
		if keyName == "end" && m.menu == "history" {
			m.models.list.Select(max(0, len(m.models.list.Items())-1))
			return m, nil, true
		}
		if keyName == "ctrl+a" && m.menu == "prompts" {
			if _, ok := m.gateway.(scopedPromptGateway); ok {
				m.allPrompts = !m.allPrompts
				m.savedPrompts = nil
				m.promptsIncomplete = false
				items := m.refreshMenuItems(m.promptItems())
				load := m.loadPrompts()
				return m, tea.Batch(items, load), true
			}
		}
		if keyName == "ctrl+r" && m.menu == "prompts" && m.promptsIncomplete && !m.promptsLoading {
			load := m.loadPrompts()
			return m, load, true
		}
		if keyName == "ctrl+p" {
			m.models.list.CursorUp()
			return m, nil, true
		}
		if keyName == "ctrl+n" || keyName == "ctrl+r" && m.menu == "prompts" {
			m.models.list.CursorDown()
			return m, nil, true
		}
		if next, cmd, handled := m.menuKey(key); handled {
			return next, cmd, true
		}
		return m, nil, false
	}
	switch keyName {
	case "?":
		if m.prompt.Value() == "" && len(m.files) == 0 {
			next, cmd := m.openMenu("help")
			return next, cmd, true
		}
	case "f3":
		next, cmd := m.findTranscript("")
		return next, cmd, true
	case "ctrl+l":
		return m, tea.ClearScreen, true
	case "alt+h":
		next, cmd := m.showHistory(false)
		return next, cmd, true
	case "alt+n":
		next, cmd := m.navigate("new")
		return next, cmd, true
	case "ctrl+r":
		next, cmd := m.openPromptSearch()
		return next, cmd, true
	case "alt+r":
		next, cmd := m.retry()
		return next, cmd, true
	case "f1":
		next, cmd := m.openMenu("help")
		return next, cmd, true
	case "ctrl+g":
		next, cmd := m.openEditor()
		return next, cmd, true
	case "ctrl+s":
		next, cmd := m.stashDraft()
		return next, cmd, true
	case "ctrl+y":
		next, cmd := m.yank(false)
		return next, cmd, true
	case "alt+y":
		next, cmd := m.yank(true)
		return next, cmd, true
	case "ctrl+u", "ctrl+k", "ctrl+w", "alt+backspace", "ctrl+backspace", "alt+d":
		next, cmd := m.killInput(keyName)
		return next, cmd, true
	case "ctrl+z", "ctrl+_", "ctrl+shift+-":
		next, cmd := m.undoInput(false)
		return next, cmd, true
	case "ctrl+shift+z", "alt+shift+z":
		next, cmd := m.undoInput(true)
		return next, cmd, true
	case "ctrl+d":
		if m.prompt.Value() == "" {
			next, cmd := m.handleInterrupt()
			return next, cmd, true
		}
	case "ctrl+home":
		m.chat.GotoTop()
		return m, nil, true
	case "ctrl+end":
		m.chat.GotoBottom()
		return m, nil, true
	case "up", "down", "ctrl+p", "ctrl+n":
		up := keyName == "up" || keyName == "ctrl+p"
		if m.commandPending() {
			matches := m.commandMatches()
			if up {
				m.commandIndex = (m.commandIndex - 1 + len(matches)) % len(matches)
			} else {
				m.commandIndex = (m.commandIndex + 1) % len(matches)
			}
			return m, nil, true
		}
		if up && m.takeQueued() {
			return m, nil, true
		}
		if m.recall(up) {
			return m, nil, true
		}
	}
	return m, nil, false
}

func (m *Model) remember(text string) {
	if text == "" {
		return
	}
	if len(m.inputHistory) == 0 || m.inputHistory[len(m.inputHistory)-1] != text {
		m.inputHistory = append(m.inputHistory, text)
	}
	if len(m.inputHistory) > 100 {
		m.inputHistory = append([]string(nil), m.inputHistory[len(m.inputHistory)-100:]...)
	}
	m.inputIndex = -1
}

func (m *Model) rememberPrompt(text string, files []client.Attachment) {
	m.remember(text)
	if m.inputFiles == nil {
		m.inputFiles = map[string][]client.Attachment{}
	}
	m.inputFiles[text] = append([]client.Attachment(nil), files...)
}

func (m *Model) recall(up bool) bool {
	if len(m.inputHistory) == 0 {
		return false
	}
	value := m.prompt.Value()
	if m.inputIndex < 0 {
		if !up || m.prompt.Line() != 0 || m.prompt.LineInfo().RowOffset != 0 {
			return false
		}
		m.inputDraft = value
		m.inputDraftFiles = append([]client.Attachment(nil), m.files...)
		m.inputIndex = len(m.inputHistory)
	} else if value != m.lastRecalled {
		m.inputIndex = -1
		return false
	}
	if up && (m.prompt.Line() != 0 || m.prompt.LineInfo().RowOffset != 0) {
		return false
	}
	if !up && (m.prompt.Line() != m.prompt.LineCount()-1 || m.prompt.LineInfo().RowOffset != m.prompt.LineInfo().Height-1) {
		return false
	}
	if up {
		m.inputIndex = max(0, m.inputIndex-1)
	} else {
		m.inputIndex++
	}
	if m.inputIndex >= len(m.inputHistory) {
		m.prompt.SetValue(m.inputDraft)
		m.files = append([]client.Attachment(nil), m.inputDraftFiles...)
		m.inputIndex = -1
		m.lastRecalled = ""
	} else {
		m.lastRecalled = m.inputHistory[m.inputIndex]
		m.prompt.SetValue(m.lastRecalled)
		m.files = append([]client.Attachment(nil), m.inputFiles[m.lastRecalled]...)
	}
	m.prompt.MoveToEnd()
	m.dirty, m.lastEdit = true, time.Now()
	m.layout()
	return true
}

func newMessage(role, text string) client.Message {
	return client.Message{ID: rand.Text(), Role: role, Content: text, Status: "done"}
}

func (m Model) submit() (tea.Model, tea.Cmd) {
	if m.attachmentsPending > 0 {
		m.notify(m.tr("Attaching files. Your question has not been sent yet.", "파일을 첨부하고 있습니다. 질문은 전송 전입니다."))
		return m, nil
	}
	text := strings.TrimSpace(m.prompt.Value())
	if text == "" {
		return m, nil
	}
	imagePrompt, imageRequest := imagegen.Prompt(text, m.selected == "free/auto")
	mcpPrompt, mcpRequest := mcpQuestion(text)
	if imageRequest && m.busy {
		m.notify(m.tr("Wait for the current response. Your image prompt is kept.", "현재 응답이 끝날 때까지 기다리세요. 이미지 요청은 보존했습니다."))
		return m, nil
	}
	if mcpRequest && m.busy {
		m.notify(m.tr("Wait for the current response. Your MCP question is kept.", "현재 응답이 끝날 때까지 기다리세요. MCP 질문은 보존했습니다."))
		return m, nil
	}
	if strings.HasPrefix(text, "/") && !imageRequest && !mcpRequest {
		parts := strings.SplitN(text, " ", 2)
		name := parts[0]
		arg := ""
		if len(parts) > 1 {
			arg = strings.TrimSpace(parts[1])
		}
		if m.commandPending() {
			name = m.commandSuggestion().Name
		}
		known := name == "/models" || name == "/open" || name == "/exit!"
		for _, c := range commands {
			if c.Name == name {
				known = true
				break
			}
		}
		if !known {
			m.notify(m.tr("Unknown command. Open /help to see available commands.", "알 수 없는 명령입니다. /help에서 명령을 확인하세요."))
			return m, nil
		}
		m.prompt.Reset()
		m.remember(text)
		m.layout()
		return m.execute(name, arg)
	}
	if m.loadingSession || m.loadingHistory {
		m.notify(m.tr("Loading conversations. Your draft is safe.", "대화를 불러오는 중입니다. 초안은 보존됩니다."))
		return m, nil
	}
	if mcpRequest {
		if issue := m.validateMCPQuestion(mcpPrompt, m.files); issue != "" {
			m.notify(issue)
			return m, nil
		}
	}
	if imageRequest {
		if _, issue := m.validateImageRequest(imagePrompt, m.files); issue != "" {
			m.notify(issue)
			return m, nil
		}
	} else if m.gateway != nil && !m.ready {
		m.notify(m.tr("Checking your connection again. Your draft is safe.", "연결을 다시 확인하고 있습니다. 초안은 보존됩니다."))
		if m.loadingModels {
			return m, nil
		}
		m.loadingModels = true
		return m, m.loadModels()
	} else if issue := m.textRouteIssue(); issue != "" {
		m.notify(issue)
		return m, nil
	}
	if len(m.messages) > 194 {
		m.notify(m.tr("This conversation is getting long. Press Alt+N to start a new one.", "대화가 길어졌습니다. Alt+N으로 새 대화를 시작하세요."))
		return m, nil
	}
	if m.editingAt >= 0 {
		m.pendingEdited = &client.PendingPrompt{Text: text, Files: append([]client.Attachment(nil), m.files...)}
		return m.navigate("fork-edit")
	}
	m.rememberPrompt(text, m.files)
	m.messages = append(m.messages, newMessage("user", text))
	m.messages[len(m.messages)-1].Files = append([]client.Attachment(nil), m.files...)
	m.files = nil
	m.prompt.Reset()
	m.dirty = true
	m.layout()
	if m.gateway != nil {
		return m.startResponse()
	}
	m.renderMessages()
	m.chat.GotoBottom()
	return m, nil
}

func (m Model) execute(name, arg string) (tea.Model, tea.Cmd) {
	if (m.loadingSession || m.loadingHistory) && name != "/help" {
		m.notify(m.tr("Loading conversations. Your work is safe.", "대화를 불러오는 중입니다. 작업 내용은 보존됩니다."))
		return m, nil
	}
	switch name {
	case "/image":
		m.prompt.SetValue("/image ")
		m.prompt.CursorEnd()
		m.layout()
		return m, nil
	case "/image-setup":
		return m.setupImage()
	case "/mcp":
		return m.openMCPManager("mcp", "manage")
	case "/mcp-ask":
		m.prompt.SetValue("/mcp-ask ")
		m.prompt.CursorEnd()
		m.layout()
		m.notify(m.tr("Your question and saved tool summaries are shared with the selected model.", "질문과 저장된 MCP 도구 요약을 선택한 모델에 보냅니다."))
		return m, nil
	case "/image-open":
		return m.latestImage(false)
	case "/image-path":
		return m.latestImage(true)
	case "/model", "/models":
		if arg != "" {
			for _, model := range m.availableModels {
				if model.ID == arg || slices.Contains(model.RouteIDs, arg) {
					m.selected, m.listedSelection = model.ID, model.ID
					m.dirty = true
					m.showModelSelection(model.Name)
					return m, m.persist()
				}
			}
			m.notify(m.tr("Choose an available model from the list.", "목록에서 사용할 수 있는 모델을 선택하세요."))
		}
		return m.openModels()
	case "/auto":
		m.selected = "free/auto"
		m.dirty = true
		m.notice = ""
		if issue := m.textRouteIssue(); issue != "" {
			m.notify(issue)
		} else if m.busy {
			m.showModelSelection(m.modelLabel(m.selected))
		} else {
			m.notify(m.tr("Auto will find an available free connection.", "자동으로 사용 가능한 무료 연결을 찾습니다."))
		}
		m.renderMessages()
		return m, m.persist()
	case "/providers":
		return m.showProviders()
	case "/context":
		return m.contextCommand(arg)
	case "/opencode":
		next, cmd := m.openModels()
		m = next.(Model)
		m.menuQuery, m.menuSearching = "OpenCode", true
		return m, tea.Batch(cmd, m.applyMenuFilter())
	case "/history":
		return m.showHistory(false)
	case "/find":
		return m.findTranscript(arg)
	case "/open":
		return m.navigate("open:" + arg)
	case "/new":
		return m.navigate("new")
	case "/retry":
		return m.retry()
	case "/retry-shorter":
		return m.retryShorter()
	case "/copy":
		return m.copyAnswer(false)
	case "/code":
		return m.copyAnswer(true)
	case "/settings":
		if arg != "" {
			tokens, err := strconv.Atoi(arg)
			if err != nil || tokens < 1 || tokens > 32768 {
				m.notify(m.tr("Enter /settings followed by an output limit from 1 to 32768.", "/settings 뒤에 1부터 32768 사이의 출력 한도를 입력하세요."))
				return m, nil
			}
			m.settings.MaxTokens = tokens
			m.dirty = true
			m.notify(fmt.Sprintf(m.tr("Output limit set to %d tokens.", "출력 한도를 %d토큰으로 정했습니다."), tokens))
			return m, m.persist()
		}
		return m.openMenu("settings")
	case "/language", "/theme":
		return m.openMenu(strings.TrimPrefix(name, "/"))
	case "/help":
		return m.openMenu("help")
	case "/export":
		return m.export(arg)
	case "/save":
		if m.saveBlocked == "save_uncertain" || m.saveBlocked == "chat_conflict" || m.saveBlocked == "save_key_reused" {
			m.notify(m.saveError + m.tr(" Press F6 to preserve your work.", " F6으로 작업 내용을 보존하세요."))
			return m, nil
		}
		m.saveBlocked = ""
		m.dirty = true
		return m, m.persist()
	case "/recover":
		return m.recoverConversation()
	case "/restore":
		return m.restoreRecovery(arg)
	case "/reconnect":
		if m.gateway == nil {
			m.notify(m.tr("Start freeway without --preview to connect.", "연결하려면 --preview 옵션을 빼고 Freeway를 실행하세요."))
			return m, nil
		}
		return m.refreshModelPicker(true)
	case "/exit":
		return m.navigate("quit")
	case "/exit!":
		return m, tea.Quit
	case "/prompts":
		return m.openPromptSearch()
	case "/editor":
		return m.openEditor()
	case "/attach":
		if arg == "" {
			return m.openFiles(fileQuery{})
		}
		return m.attachPath(strings.Trim(arg, "\""), fileQuery{})
	case "/detach":
		m.recordUndo(m.captureDraft())
		if arg == "" {
			m.files = nil
		} else {
			kept := m.files[:0]
			for _, file := range m.files {
				if file.Path != arg {
					kept = append(kept, file)
				}
			}
			m.files = kept
		}
		m.markDraftChanged()
		m.layout()
		m.notify(m.tr("Attachments updated.", "첨부를 갱신했습니다."))
		return m, nil
	case "/queue":
		return m.reviewQueue(arg)
	case "/fork":
		return m.navigate("fork")
	case "/rename":
		if arg == "" {
			m.notify(m.tr("Usage: /rename Your conversation title", "사용법 /rename 새 대화 제목"))
			return m, nil
		}
		runes := []rune(arg)
		if len(runes) > 120 {
			m.notify(m.tr("Use a title of 120 characters or fewer.", "제목은 120자 이내로 입력하세요."))
			return m, nil
		}
		m.session.Title = arg
		m.dirty = true
		return m, m.persist()
	case "/resume":
		if arg == "--all" {
			m.allSessions = true
		} else {
			m.historyQuery = arg
		}
		return m.showHistory(false)
	case "/continue":
		m.historyQuery = "--last"
		return m.showHistory(false)
	case "/clear":
		return m.navigate("new")
	}
	return m, nil
}

func (m Model) retry() (tea.Model, tea.Cmd) {
	return m.retryWithSettings(m.settings)
}

func (m Model) retryShorter() (tea.Model, tea.Cmd) {
	if m.shorterMax == 0 || m.selected != m.responseTarget.model {
		m.notify(m.tr("F5 is available after a reply exceeds the local token budget.", "F5는 응답이 로컬 토큰 예산을 넘었을 때 사용할 수 있습니다."))
		return m, nil
	}
	settings := m.settings
	settings.MaxTokens = min(settings.MaxTokens, m.shorterMax)
	return m.retryWithSettings(settings)
}

func (m Model) retryWithSettings(settings client.Settings) (tea.Model, tea.Cmd) {
	if m.loadingSession || m.loadingHistory {
		m.notify(m.tr("Wait for the conversation to finish loading before retrying.", "대화를 다 불러온 뒤 다시 시도하세요."))
		return m, nil
	}
	if m.busy {
		m.notify(m.tr("Press Esc to stop the current response first.", "먼저 Esc로 현재 응답을 중지하세요."))
		return m, nil
	}
	last := len(m.messages) - 1
	for last >= 0 && m.messages[last].Role != "user" {
		last--
	}
	if last < 0 {
		m.notify(m.tr("There is no question to regenerate yet.", "다시 보낼 질문이 없습니다."))
		return m, nil
	}
	if m.gateway == nil {
		m.notify(m.tr("Preview mode does not send requests.", "미리보기에서는 요청을 전송하지 않습니다."))
		return m, nil
	}
	question := m.messages[last]
	if prompt, asksMCP := mcpQuestion(question.Content); asksMCP {
		if issue := m.validateMCPQuestion(prompt, question.Files); issue != "" {
			m.notify(issue)
			return m, nil
		}
	}
	if prompt, image := imagegen.Prompt(question.Content, m.selected == "free/auto"); image {
		if _, issue := m.validateImageRequest(prompt, question.Files); issue != "" {
			m.notify(issue)
			return m, nil
		}
	} else if issue := m.textRouteIssue(); issue != "" {
		m.notify(issue)
		return m, nil
	}
	m.messages = append([]client.Message(nil), m.messages[:last+1]...)
	m.dirty = true
	return m.startResponseWithSettings(settings)
}

func (m Model) copyAnswer(codeOnly bool) (tea.Model, tea.Cmd) {
	content := ""
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Role == "assistant" && m.messages[i].Content != "" {
			content = m.messages[i].Content
			break
		}
	}
	if codeOnly {
		content = lastCode(content)
	}
	if content == "" {
		if codeOnly {
			m.notify(m.tr("There is no code block to copy yet.", "복사할 코드 블록이 없습니다."))
		} else {
			m.notify(m.tr("There is no response to copy yet.", "복사할 응답이 없습니다."))
		}
		return m, nil
	}
	write := m.copyText
	if write == nil {
		write = clipboard.WriteAll
	}
	return m, func() tea.Msg {
		err := write(content)
		label := m.tr("Response copied.", "응답을 복사했습니다.")
		if codeOnly {
			label = m.tr("Code copied.", "코드를 복사했습니다.")
		}
		return actionResult{text: label, err: err, clipboard: content}
	}
}

func lastCode(text string) string {
	var current []string
	last := ""
	inside := false
	fence := ""
	for _, line := range strings.Split(text, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			if !inside {
				inside = true
				fence = trim[:3]
				current = nil
			} else if strings.HasPrefix(trim, fence) {
				last = strings.Join(current, "\n")
				inside = false
			}
			continue
		}
		if inside {
			current = append(current, line)
		}
	}
	if inside && len(current) > 0 {
		last = strings.Join(current, "\n")
	}
	return last
}

func (m Model) export(name string) (tea.Model, tea.Cmd) {
	if len(m.messages) == 0 && m.prompt.Value() == "" && len(m.files) == 0 && len(m.queue) == 0 && m.stash == nil {
		m.notify(m.tr("There is no conversation to export yet.", "내보낼 대화가 없습니다."))
		return m, nil
	}
	if name == "" {
		name = "freeway-" + time.Now().Format("20060102-150405.000") + ".md"
	}
	path, err := filepath.Abs(strings.Trim(name, "\""))
	if err != nil {
		m.notify(err.Error())
		return m, nil
	}
	chat := m.snapshot()
	return m, func() tea.Msg {
		var b strings.Builder
		writeFiles := func(files []client.Attachment) {
			for _, file := range files {
				fmt.Fprintf(&b, "\n### Attachment: %s\n\n", file.Path)
				for _, line := range strings.Split(file.Content, "\n") {
					fmt.Fprintf(&b, "    %s\n", line)
				}
			}
		}
		fmt.Fprintf(&b, "# %s\n\n", m.titleOrDefault(chat.Title))
		for _, message := range chat.Messages {
			label := "You"
			if message.Role == "assistant" {
				label = "Freeway"
			}
			fmt.Fprintf(&b, "## %s\n\n%s\n\n", label, message.Content)
			writeFiles(message.Files)
		}
		if chat.Draft != "" {
			fmt.Fprintf(&b, "## Draft\n\n%s\n", chat.Draft)
		}
		writeFiles(chat.DraftFiles)
		if chat.Stash != nil {
			fmt.Fprintf(&b, "\n## Stashed draft\n\n%s\n", chat.Stash.Text)
			writeFiles(chat.Stash.Files)
		}
		for i, prompt := range chat.Queued {
			fmt.Fprintf(&b, "\n## Queued follow-up %d\n\n%s\n", i+1, prompt.Text)
			writeFiles(prompt.Files)
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return actionResult{err: err}
		}
		_, err = f.WriteString(b.String())
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		return actionResult{text: fmt.Sprintf(m.tr("Exported to %s", "대화를 내보냈습니다. %s"), path), err: err}
	}
}

func (m Model) openMenu(kind string) (tea.Model, tea.Cmd) {
	if m.busy && kind == "history" {
		m.notify(m.tr("Press Esc to stop the response before opening this menu.", "Esc로 응답을 중지한 뒤 이 메뉴를 여세요."))
		return m, nil
	}
	m.menu, m.menuParent = kind, ""
	items := []list.Item{}
	switch kind {
	case "context":
		items = m.contextItems()
	case "settings":
		items = append(items,
			modelItem{ID: "language", Name: m.tr("Language", "표시 언어"), Detail: optionLabel(languageOptions(), m.prefs.Language)},
			modelItem{ID: "theme", Name: m.tr("Color theme", "색상 테마"), Detail: optionLabel(m.prefs.themeOptions(), m.prefs.Theme)})
		lengths := []string{m.tr("Standard", "표준"), m.tr("Long", "길게"), m.tr("Extended", "더 길게")}
		for i, n := range []int{4096, 8192, 16384} {
			detail := fmt.Sprintf(m.tr("Up to %d output tokens", "최대 출력 %d토큰"), n)
			if n == m.settings.MaxTokens {
				detail = m.tr("✓ Current · ", "✓ 현재 · ") + detail
			}
			items = append(items, modelItem{ID: fmt.Sprint(n), Name: fmt.Sprintf(m.tr("Response length · %s", "응답 길이 · %s"), lengths[i]), Detail: detail})
		}
	case "language":
		for _, option := range languageOptions() {
			items = append(items, modelItem{ID: option.ID, Name: option.Label})
		}
	case "theme":
		for _, option := range m.prefs.themeOptions() {
			items = append(items, modelItem{ID: option.ID, Name: option.Label})
		}
	case "queue-clear":
		items = append(items, modelItem{ID: "back", Name: m.tr("Keep queued prompts", "예약 유지")}, modelItem{
			ID: "clear", Name: m.tr("Clear queue", "예약 비우기"),
			Detail: m.tr("Remove queued prompts and their attachments", "예약한 질문과 첨부를 삭제"),
		})
	case "help":
		for _, entry := range []command{
			{"Enter / Ctrl+J", "Send / newline · Shift+Enter and Alt+Enter also work", "보내기 / 줄바꿈 · Shift+Enter와 Alt+Enter도 가능"},
			{"↑↓ / Ctrl+P·N", "Move within multiline input; recall history at edges", "줄 사이 이동 · 입력의 처음과 끝에서 기록 탐색"},
			{"Ctrl+R", "Search saved prompt history", "저장된 질문 기록 검색"},
			{"F3 / /find", "Find a prompt or reply in the current conversation", "현재 대화에서 질문과 응답 검색"},
			{"F4 / /providers", "Check provider keys, models and usage limits", "공급처 연결, 모델 및 할당량 확인"},
			{"F5", "Retry the last question with a shorter reply", "더 짧은 응답으로 마지막 질문 다시 시도"},
			{"F6 / F7", "Save a recovery file / restore a draft", "복구 파일 저장 / 초안 복원"},
			{"F8 / /context", "Review earlier turns and attachments sent to the model", "모델에 보낼 이전 대화와 첨부 확인"},
			{"F9 / /image-setup", "Connect an image tool and keep the draft", "초안을 보존하고 이미지 도구 연결"},
			{"Alt+H / Alt+N", "Resume a saved session / start a new one", "저장된 대화 이어서 / 새 대화 시작"},
			{"Alt+P / Alt+R", "Choose a model / regenerate response", "모델 선택 / 응답 다시 생성"},
			{"Ctrl+S / Ctrl+G", "Stash or restore draft / open external editor", "초안 보관 또는 복원 / 외부 편집기 열기"},
			{"Ctrl+U·K·W / Ctrl+Y", "Delete to line edge or previous word / yank deleted text", "줄 끝 또는 단어까지 삭제 / 삭제한 텍스트 붙여넣기"},
			{"Ctrl+Z / Ctrl+Shift+Z", "Undo / redo input changes", "입력 되돌리기 / 다시 적용"},
			{"Tab / ↑", "While a reply runs: queue a follow-up / take it back", "응답 중: 후속 질문 예약 / 예약 되돌리기"},
			{"@ / /attach", "Select a project file to include in the prompt", "질문에 첨부할 프로젝트 파일 선택"},
			{"Esc Esc", "Clear draft; on empty input, edit last prompt in a fork", "초안 비우기 · 입력이 비어 있으면 이전 질문 편집"},
			{"Ctrl+C", "Stop response; clear draft; press twice on empty input to quit", "응답 중지 · 초안 비우기 · 빈 입력에서 두 번 눌러 종료"},
			{"Ctrl+L / PgUp·PgDn", "Redraw / scroll conversation", "화면 다시 그리기 / 대화 스크롤"},
			{"/copy / /code", "Copy response / copy code block", "응답 복사 / 코드 블록 복사"},
			{"Shift+drag", "Select text in the terminal", "터미널에서 텍스트 선택"},
		} {
			items = append(items, modelItem{ID: "help", Name: entry.Name, Detail: m.tr(entry.Description, entry.Korean)})
		}
		for _, c := range commands {
			items = append(items, modelItem{ID: c.Name, Name: c.Name, Detail: m.tr(c.Description, c.Korean)})
		}
	case "history":
		items = m.historyItems()
	}
	if kind == "language" || kind == "theme" {
		selected := 0
		for i, item := range items {
			entry := item.(modelItem)
			if kind == "language" && entry.ID == m.prefs.Language || kind == "theme" && entry.ID == m.prefs.Theme {
				entry.Detail = m.tr("✓ Current", "✓ 현재")
				items[i], selected = entry, i
				break
			}
		}
		m.models.list.SetItems(items)
		next, cmd := m.changeFocus(true)
		m = next.(Model)
		m.models.list.Select(selected)
		return m, cmd
	}
	m.models.list.SetItems(items)
	return m.changeFocus(true)
}

func (m Model) chooseItem(item modelItem) (tea.Model, tea.Cmd) {
	kind := m.menu
	if kind == "context" {
		return m.chooseContext(item)
	}
	if kind == "providers" {
		return m.connectProvider(item.ID)
	}
	if kind == "files" {
		query := m.fileSelection
		next, _ := m.changeFocus(false)
		return next.(Model).attachPath(item.ID, query)
	}
	if kind == "prompts" {
		draft := m.captureDraft()
		m.recordUndo(draft)
		if m.stash == nil && (draft.Text != "" || len(draft.Files) > 0) {
			m.stash = &draft
			m.notify(m.tr("Prompt loaded. Ctrl+S restores your previous draft.", "질문을 불러왔습니다. Ctrl+S로 이전 초안을 복원할 수 있습니다."))
		}
		m.prompt.SetValue(item.Detail)
		m.prompt.MoveToEnd()
		m.files = append([]client.Attachment(nil), m.inputFiles[item.Detail]...)
		m.markDraftChanged()
		m.layout()
		return m.changeFocus(false)
	}
	if kind == "queue" {
		index := 0
		fmt.Sscan(item.ID, &index)
		if index < 0 || index >= len(m.queue) {
			return m, nil
		}
		m.recordUndo(m.captureDraft())
		if m.prompt.Value() != "" {
			d := m.captureDraft()
			m.stash = &d
		}
		item := m.queue[index]
		m.queue = append(m.queue[:index], m.queue[index+1:]...)
		m.prompt.SetValue(item.Text)
		m.prompt.MoveToEnd()
		m.files = append([]client.Attachment(nil), item.Files...)
		m.markDraftChanged()
		m.layout()
		return m.changeFocus(false)
	}
	if kind == "queue-clear" {
		if item.ID != "clear" {
			return m.changeFocus(false)
		}
		m.queue, m.queuePaused = nil, false
		m.dirty = true
		m.notify(m.tr("Queue cleared.", "예약을 비웠습니다."))
		next, cmd := m.changeFocus(false)
		m = next.(Model)
		return m, tea.Batch(cmd, m.persist())
	}
	if kind == "help" {
		if !strings.HasPrefix(item.ID, "/") {
			m.notify(item.Detail)
			return m, nil
		}
		next, _ := m.changeFocus(false)
		return next.(Model).execute(item.ID, "")
	}
	if kind == "history" {
		if item.ID == historyMoreID {
			return m.fetchHistoryPage(true)
		}
		if m.historyPage.loading {
			return m, nil
		}
		next, _ := m.changeFocus(false)
		return next.(Model).navigate("open:" + item.ID)
	}
	if kind == "settings" {
		if item.ID == "language" || item.ID == "theme" {
			next, cmd := m.openMenu(item.ID)
			model := next.(Model)
			model.menuParent = "settings"
			return model, cmd
		}
		fmt.Sscan(item.ID, &m.settings.MaxTokens)
		m.dirty = true
		m.notify(item.Name + m.tr(" selected.", " 선택됨"))
		next, cmd := m.changeFocus(false)
		model := next.(Model)
		if model.busy {
			return model, cmd
		}
		return model, tea.Batch(cmd, model.persist())
	}
	if kind == "language" || kind == "theme" {
		if kind == "language" {
			m.prefs.Language = item.ID
		} else {
			m.prefs.Theme = item.ID
		}
		next, cmd := m.changeFocus(false)
		m = next.(Model)
		m.applyPreferences()
		if err := m.prefs.Save(); err != nil {
			m.notify(m.tr("Preference applied for this session. Could not save it.", "이번 실행에 설정을 적용했습니다. 설정 파일은 저장하지 못했습니다."))
		} else {
			m.notify(m.tr("Preference saved.", "설정을 저장했습니다."))
		}
		return m, cmd
	}
	m.selected, m.listedSelection = item.ID, item.ID
	m.dirty = true
	m.notice = ""
	m.showModelSelection(item.Name)
	m.renderMessages()
	next, cmd := m.changeFocus(false)
	model := next.(Model)
	if model.busy {
		return model, cmd
	}
	return model, tea.Batch(cmd, model.persist())
}

func (m *Model) showModelSelection(name string) {
	if m.busy {
		m.notify(fmt.Sprintf(m.tr("Next question · %s", "다음 질문 모델 · %s"), name))
		return
	}
	m.notify(name + m.tr(" selected", " 선택됨"))
}

func (m Model) menuTitle() string {
	switch m.menu {
	case "providers":
		return m.tr("Providers", "공급처")
	case "files":
		return m.tr("Project files", "프로젝트 파일")
	case "prompts":
		return m.tr("Prompt history · Ctrl+R for next match", "질문 기록 · Ctrl+R 다음 결과")
	case "queue":
		return m.tr("Queued follow-ups · Enter to take back", "예약된 후속 질문 · Enter로 되돌리기")
	case "queue-clear":
		return m.tr("Clear queued prompts?", "예약한 질문을 비울까요?")
	case "history":
		return m.tr("Conversations", "대화 기록")
	case "help":
		return m.tr("Keyboard shortcuts", "단축키")
	case "settings":
		return m.tr("Settings", "설정")
	case "language":
		return m.tr("Language", "표시 언어")
	case "theme":
		return m.tr("Color theme", "색상 테마")
	case "context":
		return m.tr("Request context", "요청 문맥")
	}
	return m.rankingTitle()
}
func (m Model) menuDetail() string {
	if m.menu == "providers" {
		item, selected := m.models.list.SelectedItem().(modelItem)
		if m.providersLoading && (!selected || m.providerChecksPending == nil || m.providerChecksPending[item.ID]) {
			return m.tr("Checking provider connections…", "공급처 연결 확인 중…")
		}
		if m.providersError != "" {
			return m.providersError
		}
		if selected {
			provider := m.providers[item.ID]
			if provider.State == "status_check_failed" {
				return m.tr("Check failed · Ctrl+R to retry", "상태 확인 실패 · Ctrl+R 다시 시도")
			}
			if m.providerSetup != "" && provider.Management {
				return ProviderUsageText(m.prefs, provider)
			}
			return providerStatusHelp(provider)
		}
		return ""
	}
	if m.menu == "history" {
		if m.historyPage.loading {
			return m.tr("Loading conversations…", "대화 불러오는 중…")
		}
		if m.historyPage.err != "" {
			return m.historyPage.err
		}
		if item, ok := m.models.list.SelectedItem().(modelItem); ok {
			if item.ID == historyMoreID {
				return item.Detail
			}
			if preview := m.historyPreview(item.ID); preview != "" && m.menuPreviewHeight() == 0 {
				return preview
			}
			return item.Detail
		}
	}
	if m.menu == "models" && m.gateway != nil && len(m.availableModels) > 0 {
		switch {
		case m.loadingModels:
			return m.tr("Checking models…", "모델 확인 중…")
		case !m.ready:
			return m.tr("Model check failed · Ctrl+R", "모델 확인 실패 · Ctrl+R")
		}
	}
	if item, ok := m.models.list.SelectedItem().(modelItem); ok {
		if m.menu == "models" && len(item.ReadyProviders) > 0 {
			return strings.Join(item.ReadyProviders, " · ")
		}
		if item.Detail != "" {
			return terminalSafe(item.Detail)
		}
	}
	if m.menu != "models" {
		return ""
	}
	return m.modelLabel(m.selected) + m.tr(" · current", " · 현재 선택")
}

func optionLabel(options []SetupOption, id string) string {
	for _, option := range options {
		if option.ID == id {
			return option.Label
		}
	}
	return id
}
