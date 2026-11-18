package tui

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

type command struct{ Name, Description string }

var commands = []command{
	{"/model", "Choose a model"}, {"/history", "Search and resume conversations"}, {"/new", "New conversation"},
	{"/continue", "Continue the latest session in this project"},
	{"/retry", "Regenerate the last response"}, {"/copy", "Copy the last response"}, {"/code", "Copy the last code block"},
	{"/export", "Export conversation to Markdown"}, {"/settings", "Response length"},
	{"/auto", "Choose a free model automatically"}, {"/reconnect", "Reconnect"}, {"/save", "Save now"},
	{"/help", "Keyboard shortcuts and commands"}, {"/exit", "Save and quit"},
	{"/attach", "Attach a project text file"}, {"/detach", "Remove attached files"}, {"/queue", "Review queued follow-ups"},
	{"/editor", "Edit the draft in VISUAL or EDITOR"}, {"/prompts", "Search prompt history"}, {"/fork", "Continue in a new conversation"}, {"/rename", "Rename this conversation"}, {"/resume", "Open a saved conversation"}, {"/clear", "Start a new conversation"},
}

type tickMsg time.Time
type reconnectMsg struct{}
type actionResult struct {
	text string
	err  error
}

func tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
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
	if keyName == "ctrl+shift+y" {
		next, cmd := m.copyAnswer(false)
		return next, cmd, true
	}
	if m.modelsFocused {
		if keyName == "ctrl+a" && m.menu == "history" {
			m.allSessions = !m.allSessions
			m.history = client.ScopeConversations(m.allHistory, m.workdir, m.allSessions)
			next, cmd := m.openMenu("history")
			return next, cmd, true
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
	case "f2":
		next, cmd := m.changeFocus(true)
		return next, cmd, true
	case "alt+p":
		next, cmd := m.changeFocus(true)
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
		m.notify("Attaching files. Your question has not been sent yet.")
		return m, nil
	}
	text := strings.TrimSpace(m.prompt.Value())
	if text == "" {
		return m, nil
	}
	if strings.HasPrefix(text, "/") {
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
			m.notify("Unknown command. Open /help to see available commands.")
			return m, nil
		}
		m.prompt.Reset()
		m.remember(text)
		m.layout()
		return m.execute(name, arg)
	}
	if m.loadingSession || m.loadingHistory {
		m.notify("Loading conversations. Your draft is safe.")
		return m, nil
	}
	if m.gateway != nil && !m.ready {
		m.notify("Checking your connection again. Your draft is safe.")
		if m.loadingModels {
			return m, nil
		}
		m.loadingModels = true
		return m, m.loadModels()
	}
	if len(m.messages) > 194 {
		m.notify("This conversation is getting long. Press Alt+N to start a new one.")
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
		m.notify("Loading conversations. Your work is safe.")
		return m, nil
	}
	switch name {
	case "/model", "/models":
		if arg != "" {
			for _, model := range m.availableModels {
				if model.ID == arg {
					m.selected = arg
					m.dirty = true
					m.notify(model.Name + " selected")
					return m, m.persist()
				}
			}
			m.notify("Choose an available model from the list.")
		}
		return m.changeFocus(true)
	case "/auto":
		m.selected = "free/auto"
		m.dirty = true
		m.notice = ""
		m.notify("Auto will find an available free connection.")
		m.renderMessages()
		return m, m.persist()
	case "/history":
		return m.showHistory(false)
	case "/open":
		return m.navigate("open:" + arg)
	case "/new":
		return m.navigate("new")
	case "/retry":
		return m.retry()
	case "/copy":
		return m.copyAnswer(false)
	case "/code":
		return m.copyAnswer(true)
	case "/settings":
		return m.openMenu("settings")
	case "/help":
		return m.openMenu("help")
	case "/export":
		return m.export(arg)
	case "/save":
		m.dirty = true
		return m, m.persist()
	case "/reconnect":
		m.reconnectAttempts = 0
		m.loadingModels = true
		m.status = "Connecting"
		return m, m.loadModels()
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
		m.notify("Attachments updated.")
		return m, nil
	case "/queue":
		return m.reviewQueue(arg)
	case "/fork":
		return m.navigate("fork")
	case "/rename":
		if arg == "" {
			m.notify("Usage: /rename Your conversation title")
			return m, nil
		}
		runes := []rune(arg)
		if len(runes) > 120 {
			m.notify("Use a title of 120 characters or fewer.")
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
	if m.loadingSession || m.loadingHistory {
		m.notify("Wait for the conversation to finish loading before retrying.")
		return m, nil
	}
	if m.busy {
		m.notify("Press Esc to stop the current response first.")
		return m, nil
	}
	last := len(m.messages) - 1
	for last >= 0 && m.messages[last].Role != "user" {
		last--
	}
	if last < 0 {
		m.notify("There is no question to regenerate yet.")
		return m, nil
	}
	if m.gateway == nil {
		m.notify("Preview mode does not send requests.")
		return m, nil
	}
	m.messages = append([]client.Message(nil), m.messages[:last+1]...)
	m.dirty = true
	return m.startResponse()
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
			m.notify("There is no code block to copy yet.")
		} else {
			m.notify("There is no response to copy yet.")
		}
		return m, nil
	}
	write := m.copyText
	if write == nil {
		write = clipboard.WriteAll
	}
	return m, func() tea.Msg {
		err := write(content)
		label := "Response copied."
		if codeOnly {
			label = "Code copied."
		}
		return actionResult{text: label, err: err}
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
		m.notify("There is no conversation to export yet.")
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
		fmt.Fprintf(&b, "# %s\n\n", chat.Title)
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
		return actionResult{text: "Exported to " + path, err: err}
	}
}

func (m Model) openMenu(kind string) (tea.Model, tea.Cmd) {
	if m.busy && kind == "history" {
		m.notify("Press Esc to stop the response before opening this menu.")
		return m, nil
	}
	m.menu = kind
	items := []list.Item{}
	switch kind {
	case "settings":
		for _, n := range []int{4096, 8192, 16384} {
			items = append(items, modelItem{ID: fmt.Sprint(n), Name: fmt.Sprintf("Response length · %s", map[int]string{4096: "Standard", 8192: "Long", 16384: "Extended"}[n]), Detail: fmt.Sprintf("Up to %s output tokens", fmt.Sprint(n))})
		}
	case "help":
		for _, entry := range []command{
			{"Enter / Ctrl+J", "Send / newline · Shift+Enter and Alt+Enter also work"},
			{"↑↓ / Ctrl+P·N", "Move within multiline input; recall history at edges"},
			{"Ctrl+R", "Search saved prompt history"},
			{"Alt+H / Alt+N", "Resume a saved session / start a new one"},
			{"Alt+P / Alt+R", "Choose a model / regenerate response"},
			{"Ctrl+S / Ctrl+G", "Stash or restore draft / open external editor"},
			{"Ctrl+U·K·W / Ctrl+Y", "Delete to line edge or previous word / yank deleted text"},
			{"Ctrl+Z / Ctrl+Shift+Z", "Undo / redo input changes"},
			{"Tab / ↑ while running", "Queue follow-up / take queued message back"},
			{"@ / /attach", "Select a project file to include in the prompt"},
			{"Esc Esc", "Clear draft; on empty input, edit last prompt in a fork"},
			{"Ctrl+C", "Stop response; clear draft; press twice on empty input to quit"},
			{"Ctrl+L / PgUp·PgDn", "Redraw / scroll conversation"},
			{"/copy / /code", "Copy response / copy code block"},
			{"Shift+drag", "Select text in the terminal"},
		} {
			items = append(items, modelItem{ID: "help", Name: entry.Name, Detail: entry.Description})
		}
		for _, c := range commands {
			items = append(items, modelItem{ID: c.Name, Name: c.Name, Detail: c.Description})
		}
	case "history":
		for _, chat := range m.history {
			when := chat.UpdatedAt
			if t, err := time.Parse(time.RFC3339, when); err == nil {
				when = t.Local().Format("01/02 15:04")
			}
			detail := fmt.Sprintf("%s · %d messages", when, chat.MessageCount)
			if chat.Workspace != "" {
				detail += " · " + chat.Workspace
			}
			if chat.HasDraft {
				detail += " · draft"
			}
			if chat.QueuedCount > 0 {
				detail += fmt.Sprintf(" · %d queued", chat.QueuedCount)
			}
			items = append(items, modelItem{ID: chat.ID, Name: chat.Title, Detail: detail})
		}
	}
	m.models.list.SetItems(items)
	return m.changeFocus(true)
}

func (m Model) chooseItem(item modelItem) (tea.Model, tea.Cmd) {
	kind := m.menu
	if kind == "files" {
		query := m.fileSelection
		next, _ := m.changeFocus(false)
		return next.(Model).attachPath(item.ID, query)
	}
	if kind == "prompts" {
		m.recordUndo(m.captureDraft())
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
	if kind == "help" {
		if !strings.HasPrefix(item.ID, "/") {
			m.notify(item.Detail)
			return m, nil
		}
		next, _ := m.changeFocus(false)
		return next.(Model).execute(item.ID, "")
	}
	if kind == "history" {
		next, _ := m.changeFocus(false)
		return next.(Model).navigate("open:" + item.ID)
	}
	if kind == "settings" {
		fmt.Sscan(item.ID, &m.settings.MaxTokens)
		m.dirty = true
		m.notify(item.Name + " selected.")
		next, cmd := m.changeFocus(false)
		model := next.(Model)
		if model.busy {
			return model, cmd
		}
		return model, tea.Batch(cmd, model.persist())
	}
	m.selected = item.ID
	m.dirty = true
	m.notice = ""
	m.notify(item.Name + " selected")
	m.renderMessages()
	next, cmd := m.changeFocus(false)
	model := next.(Model)
	if model.busy {
		return model, cmd
	}
	return model, tea.Batch(cmd, model.persist())
}

func (m Model) menuTitle() string {
	switch m.menu {
	case "files":
		return "Project files"
	case "prompts":
		return "Prompt history · Ctrl+R for next match"
	case "queue":
		return "Queued follow-ups · Enter to take back"
	case "history":
		return "Conversations"
	case "help":
		return "Keyboard shortcuts"
	case "settings":
		return "Response settings"
	}
	return "Models"
}
func (m Model) menuDetail() string {
	if m.menu == "history" {
		if item, ok := m.models.list.SelectedItem().(modelItem); ok {
			return item.ID + " · " + item.Detail
		}
	}
	if item, ok := m.models.list.SelectedItem().(modelItem); ok && item.Detail != "" {
		return terminalSafe(item.Detail)
	}
	return m.modelLabel() + " · current"
}
