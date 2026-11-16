package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

type conversationGateway interface {
	Conversations(context.Context) ([]client.ConversationSummary, error)
	Conversation(context.Context, string) (client.Conversation, error)
	SaveConversation(context.Context, client.Conversation) (client.Conversation, error)
}

type historyLoaded struct {
	records []client.ConversationSummary
	initial bool
	epoch   uint64
	err     error
}
type conversationLoaded struct {
	chat        client.Conversation
	epoch       uint64
	err         error
	draftAtLoad string
}
type conversationSaved struct {
	chat  client.Conversation
	epoch uint64
	err   error
}

func (m Model) WithSession(id string, fresh, noSave bool) Model {
	m.resumeID, m.startNew, m.saveEnabled = id, fresh, !noSave
	return m
}

func (m Model) WithStartMode(mode, query string, all, noSave bool) Model {
	m.startMode, m.resumeID, m.allSessions, m.saveEnabled = mode, query, all, !noSave
	m.startNew = mode == "new"
	return m
}

func (m Model) updateConvenience(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case projectLoaded:
		if client.SameWorkspace(msg.directory, m.workdir) {
			m.projectLabel = msg.label
		}
		return m, nil, true
	case promptsLoaded:
		m.promptsLoading = false
		current := append([]string(nil), m.inputHistory...)
		m.inputHistory = nil
		for _, text := range append(msg.prompts, current...) {
			m.remember(text)
		}
		if m.promptSearchWanted {
			m.promptSearchWanted = false
			if len(m.inputHistory) > 0 {
				next, cmd := m.openPromptSearch()
				return next, cmd, true
			}
			m.notify("No prompt history yet.")
		}
		return m, nil, true
	case editorFinished:
		m.editorOpen = false
		if msg.epoch != m.sessionEpoch {
			if msg.err == nil {
				m.stash = &draftState{Text: msg.text}
				m.notify("Editor draft kept in stash. Ctrl+S restores it.")
			}
			return m, nil, true
		}
		if msg.err != nil {
			m.notify("Editor did not finish: " + msg.err.Error())
			return m, nil, true
		}
		m.recordUndo(m.captureDraft())
		m.prompt.SetValue(msg.text)
		m.prompt.MoveToEnd()
		m.markDraftChanged()
		m.layout()
		m.notify("Draft updated from editor. Enter to send.")
		return m, nil, true
	case filesIndexed:
		if msg.epoch != m.fileEpoch {
			return m, nil, true
		}
		m.filesLoading = false
		m.fileItems = msg.items
		if m.modelsFocused && m.menu == "files" {
			m.menuAll = msg.items
			cmd := m.applyMenuFilter()
			return m, cmd, true
		}
		return m, nil, true
	case fileAttached:
		if msg.epoch != m.sessionEpoch {
			return m, nil, true
		}
		if msg.err != nil {
			m.notify("Could not attach file: " + msg.err.Error())
			return m, nil, true
		}
		m.recordUndo(m.captureDraft())
		found := false
		for i, file := range m.files {
			if file.Path == msg.file.Path {
				m.files[i] = msg.file
				found = true
				break
			}
		}
		if !found {
			if len(m.files) >= 5 {
				m.notify("Attach up to five text files per prompt.")
				return m, nil, true
			}
			m.files = append(m.files, msg.file)
		}
		ref := "@" + msg.file.Path
		if strings.ContainsAny(msg.file.Path, " \t") {
			ref = "@\"" + msg.file.Path + "\""
		}
		if msg.query.Inline && m.prompt.Value() == msg.query.Original {
			runes := []rune(m.prompt.Value())
			m.prompt.SetValue(string(runes[:msg.query.Start]) + ref + " " + string(runes[msg.query.End:]))
			m.setCursorIndex(msg.query.Start + len([]rune(ref)) + 1)
		} else {
			m.prompt.InsertString(ref + " ")
		}
		m.markDraftChanged()
		m.layout()
		m.notify("Attached " + msg.file.Path + ". /detach removes attachments.")
		return m, nil, true
	case tickMsg:
		if m.gateway != nil && !m.modelsStarted && m.startupDone && !m.loadingHistory && !m.loadingSession && !m.modelsFocused {
			m.modelsStarted = true
			return m, tea.Batch(tick(), m.loadModels(), m.loadPrompts()), true
		}
		if m.renderPending {
			m.renderMessages()
		}
		var save tea.Cmd
		if m.dirty && !m.busy && !m.loadingSession && time.Now().After(m.nextSave) && time.Since(m.lastEdit) > 700*time.Millisecond {
			save = m.persist()
		}
		if save == nil && !m.busy && !m.saving && !m.modelsFocused && len(m.queue) > 0 && !m.queuePaused {
			next, cmd := m.startQueued()
			return next, tea.Batch(tick(), cmd), true
		}
		return m, tea.Batch(tick(), save), true
	case reconnectMsg:
		if m.ready || m.loadingModels {
			return m, nil, true
		}
		m.loadingModels = true
		return m, m.loadModels(), true
	case actionResult:
		if msg.err != nil {
			m.notify("Could not complete the action: " + msg.err.Error())
		} else {
			m.notify(msg.text)
		}
		return m, nil, true
	case historyLoaded:
		if msg.epoch != m.sessionEpoch {
			return m, nil, true
		}
		m.loadingHistory = false
		m.startupDone = true
		if msg.err != nil {
			m.notify("Could not load conversations. Press Alt+H to try again.")
			return m, nil, true
		}
		m.allHistory = msg.records
		m.history = client.ScopeConversations(msg.records, m.workdir, m.allSessions)
		if !msg.initial && m.historyQuery != "" {
			query := m.historyQuery
			m.historyQuery = ""
			matches := client.FindConversations(msg.records, query)
			if query == "--last" {
				matches = nil
				if latest, ok := client.LatestConversation(m.history, m.workdir, m.allSessions); ok {
					matches = []client.ConversationSummary{latest}
				}
			}
			if len(matches) == 1 {
				next, cmd := m.navigate("open:" + matches[0].ID)
				return next, cmd, true
			}
			if len(matches) > 1 {
				m.history = matches
			} else {
				m.notify("No matching session. Choose another saved conversation.")
			}
		}
		if msg.initial {
			if m.startMode == "new" || m.startNew {
				return m, nil, true
			}
			if m.resumeID != "" {
				matches := client.FindConversations(msg.records, m.resumeID)
				if len(matches) == 1 {
					return m, m.loadConversation(matches[0].ID), true
				}
				if len(matches) > 0 {
					m.history = matches
				} else {
					m.notify("No matching session. Choose a saved conversation from the list.")
				}
				next, cmd := m.openMenu("history")
				return next, cmd, true
			}
			if m.startMode == "resume" {
				if len(m.history) == 0 {
					m.notify("No saved sessions here. Start a conversation, or use freeway resume --all.")
					return m, nil, true
				}
				next, cmd := m.openMenu("history")
				return next, cmd, true
			}
			if latest, ok := client.LatestConversation(m.history, m.workdir, m.allSessions); ok && m.prompt.Value() == "" && len(m.messages) == 0 {
				return m, m.loadConversation(latest.ID), true
			}
			m.notify("No previous session in this project. Starting a new conversation.")
			return m, nil, true
		}
		if len(msg.records) == 0 {
			m.notify("No saved conversations yet. Conversations and drafts are saved automatically.")
			return m, nil, true
		}
		next, cmd := m.openMenu("history")
		return next, cmd, true
	case conversationLoaded:
		if msg.epoch != m.sessionEpoch {
			return m, nil, true
		}
		m.loadingSession = false
		if msg.err != nil {
			m.notify("Could not open conversation: " + friendlyError(msg.err))
			return m, nil, true
		}
		m.session = msg.chat
		m.fileEpoch++
		m.fileItems = nil
		m.filesLoading = false
		m.undoDrafts, m.redoDrafts = nil, nil
		if msg.chat.Workspace != "" {
			if info, err := os.Stat(msg.chat.Workspace); err == nil && info.IsDir() {
				m.workdir = msg.chat.Workspace
			}
		}
		m.messages = append([]client.Message(nil), msg.chat.Messages...)
		m.files = append([]client.Attachment(nil), msg.chat.DraftFiles...)
		m.queue = append([]client.PendingPrompt(nil), msg.chat.Queued...)
		m.queuePaused = true
		m.editingAt = -1
		m.selected = msg.chat.Model
		m.settings = msg.chat.Settings
		if m.settings.MaxTokens == 0 {
			m.settings = client.Settings{MaxTokens: 4096, Temperature: 0.6}
		}
		for i := range m.messages {
			if m.messages[i].Status == "streaming" {
				m.messages[i].Status = "stopped"
			}
			if m.messages[i].Role == "user" {
				m.rememberPrompt(m.messages[i].Content, m.messages[i].Files)
			}
		}
		m.ensureSelected()
		draft := msg.chat.Draft
		edited := m.prompt.Value() != msg.draftAtLoad
		if edited {
			draft = m.prompt.Value()
		}
		m.prompt.SetValue(draft)
		m.prompt.CursorEnd()
		m.dirty = edited
		m.saveError = ""
		m.notice = ""
		m.layout()
		m.renderMessages()
		m.chat.GotoBottom()
		m.notify("Conversation restored.")
		if len(m.queue) > 0 {
			m.notify(fmt.Sprintf("Conversation restored · %d follow-up(s) paused · /queue resume", len(m.queue)))
		}
		return m, projectContext(m.workdir), true
	case conversationSaved:
		if msg.epoch != m.sessionEpoch {
			return m, nil, true
		}
		m.saving = false
		action := m.afterSave
		m.afterSave = ""
		if msg.err != nil {
			m.saveError = friendlyError(msg.err)
			m.nextSave = time.Now().Add(10 * time.Second)
			m.dirty = true
			if action == "generate" {
				next, cmd := m.beginGeneration()
				return next, cmd, true
			}
			m.quitting = false
			m.notify("Could not save. Your work is still here. Use /save to retry or /export to keep a copy.")
			return m, nil, true
		}
		m.session.ID, m.session.Revision, m.session.Title = msg.chat.ID, msg.chat.Revision, msg.chat.Title
		m.saveError = ""
		m.nextSave = time.Time{}
		if action != "" && action != "generate" && m.dirty {
			m.afterSave = action
			return m, m.persist(), true
		}
		if action != "" {
			next, cmd := m.performNavigation(action)
			return next, cmd, true
		}
		return m, nil, true
	}
	return m, nil, false
}

func (m *Model) ensureSelected() {
	if !m.ready {
		return
	}
	if m.selected == "free/auto" || strings.HasPrefix(m.selected, "preview/") {
		return
	}
	for _, model := range m.availableModels {
		if model.ID == m.selected {
			return
		}
	}
	m.selected = "free/auto"
	m.notify("Your previous model is unavailable. Switched to Auto.")
}

func (m *Model) snapshot() client.Conversation {
	chat := m.session
	chat.Model = m.selected
	chat.Workspace = m.workdir
	chat.Settings = m.settings
	chat.Draft = m.prompt.Value()
	chat.DraftFiles = append([]client.Attachment(nil), m.files...)
	chat.Queued = append([]client.PendingPrompt(nil), m.queue...)
	if m.commandPending() {
		chat.Draft = ""
	}
	for i := range m.messages {
		if m.messages[i].ID == "" {
			m.messages[i].ID = newMessage(m.messages[i].Role, m.messages[i].Content).ID
		}
		if m.messages[i].Status == "" {
			m.messages[i].Status = "done"
		}
	}
	chat.Messages = append([]client.Message{}, m.messages...)
	if chat.Title == "" {
		title := chat.Draft
		for _, message := range m.messages {
			if message.Role == "user" {
				title = message.Content
				break
			}
		}
		title = strings.Join(strings.Fields(terminalSafe(title)), " ")
		runes := []rune(title)
		if len(runes) > 45 {
			title = string(runes[:45]) + "…"
		}
		if title == "" {
			title = "New conversation"
		}
		chat.Title = title
	}
	return chat
}

func (m *Model) persist() tea.Cmd {
	backend, ok := m.gateway.(conversationGateway)
	if !ok || !m.saveEnabled {
		m.dirty = false
		return nil
	}
	if m.saving {
		return nil
	}
	if len(m.messages) == 0 && m.prompt.Value() == "" && len(m.files) == 0 && len(m.queue) == 0 && m.session.ID == "" {
		m.dirty = false
		return nil
	}
	snapshot := m.snapshot()
	epoch := m.sessionEpoch
	m.saving = true
	m.dirty = false
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
		defer cancel()
		chat, err := backend.SaveConversation(ctx, snapshot)
		return conversationSaved{chat: chat, epoch: epoch, err: err}
	}
}

func (m Model) showHistory(initial bool) (tea.Model, tea.Cmd) {
	if m.busy {
		m.notify("Press Esc to stop the response first.")
		return m, nil
	}
	backend, ok := m.gateway.(conversationGateway)
	if !ok {
		m.startupDone = true
		m.notify("Saved conversations are not available in preview mode.")
		return m, nil
	}
	if m.loadingHistory && !initial {
		return m, nil
	}
	m.loadingHistory = true
	epoch := m.sessionEpoch
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
		defer cancel()
		records, err := backend.Conversations(ctx)
		return historyLoaded{records: records, initial: initial, epoch: epoch, err: err}
	}
}

func (m *Model) loadConversation(id string) tea.Cmd {
	backend, ok := m.gateway.(conversationGateway)
	if !ok {
		return nil
	}
	m.loadingSession = true
	epoch := m.sessionEpoch
	draftAtLoad := m.prompt.Value()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
		defer cancel()
		chat, err := backend.Conversation(ctx, id)
		return conversationLoaded{chat: chat, epoch: epoch, err: err, draftAtLoad: draftAtLoad}
	}
}

func (m Model) navigate(action string) (tea.Model, tea.Cmd) {
	if m.busy {
		if action == "new" || action == "quit" || action == "fork" {
			m.queuePaused = true
			m.stopResponse()
			m.dirty = true
			m.renderMessages()
		} else {
			m.notify("Press Esc to stop the response first. Your draft will stay here.")
			return m, nil
		}
	}
	if m.loadingSession {
		m.notify("Loading conversation…")
		return m, nil
	}
	if m.saving {
		m.afterSave = action
		return m, nil
	}
	if m.dirty {
		cmd := m.persist()
		if cmd != nil {
			m.afterSave = action
			return m, cmd
		}
	}
	return m.performNavigation(action)
}

func (m Model) performNavigation(action string) (tea.Model, tea.Cmd) {
	switch {
	case action == "generate":
		return m.beginGeneration()
	case action == "quit":
		return m, tea.Quit
	case action == "new":
		m.sessionEpoch++
		m.session = client.Conversation{}
		m.messages = nil
		m.renderCache = nil
		m.prompt.Reset()
		m.dirty = false
		m.saveError = ""
		m.notice = ""
		m.inputIndex = -1
		m.loadingHistory = false
		m.files = nil
		m.queue = nil
		m.queuePaused = false
		m.editingAt = -1
		m.pendingEdited = nil
		m.undoDrafts = nil
		m.redoDrafts = nil
		m.layout()
		m.renderMessages()
		m.chat.GotoBottom()
		m.notify("New conversation · Press Alt+H to reopen earlier work.")
		return m, nil
	case strings.HasPrefix(action, "open:"):
		m.sessionEpoch++
		m.prompt.Reset()
		return m, m.loadConversation(strings.TrimPrefix(action, "open:"))
	case action == "fork" || action == "fork-edit":
		original := m.session
		title := []rune(original.Title)
		if len(title) > 110 {
			title = title[:110]
		}
		m.session = client.Conversation{Model: m.selected, SystemPrompt: original.SystemPrompt, Settings: m.settings, Title: string(title) + " (fork)"}
		m.sessionEpoch++
		m.renderCache = nil
		m.queue = nil
		m.queuePaused = false
		m.dirty = true
		if action == "fork-edit" && m.pendingEdited != nil {
			edited := *m.pendingEdited
			index := max(0, min(m.editingAt, len(m.messages)))
			m.messages = append([]client.Message(nil), m.messages[:index]...)
			m.messages = append(m.messages, newMessage("user", edited.Text))
			m.messages[len(m.messages)-1].Files = edited.Files
			m.files = nil
			m.prompt.Reset()
			m.editingAt = -1
			m.pendingEdited = nil
			m.layout()
			return m.startResponse()
		}
		m.notify("Fork created. The original conversation is preserved.")
		m.renderMessages()
		return m, m.persist()
	}
	return m, nil
}

func (m Model) requestQuit() (tea.Model, tea.Cmd) {
	if m.busy {
		m.stopResponse()
		m.status = "Stopped"
		m.dirty = true
		m.renderMessages()
		return m, m.persist()
	}
	if m.quitting {
		return m, tea.Quit
	}
	m.quitting = true
	return m.navigate("quit")
}
