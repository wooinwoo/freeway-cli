package tui

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"slices"
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
	cursor  string
	paged   bool
	lookup  string
	err     error
}
type conversationLoaded struct {
	chat        client.Conversation
	epoch       uint64
	err         error
	draftAtLoad string
	filesAtLoad []client.Attachment
}
type conversationSaved struct {
	chat        client.Conversation
	epoch       uint64
	err         error
	titleAtSave string
	size        int
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
		if msg.request != m.promptRequest {
			return m, nil, true
		}
		m.promptsLoading = false
		m.promptsIncomplete = msg.err != nil
		m.savedPrompts = msg.prompts
		if m.modelsFocused && m.menu == "prompts" {
			cmd := m.refreshMenuItems(m.promptItems())
			return m, cmd, true
		}
		return m, nil, true
	case editorFinished:
		m.editorOpen = false
		if msg.epoch != m.sessionEpoch {
			if msg.err == nil {
				m.stash = &draftState{Text: msg.text}
				m.notify(m.tr("Editor draft kept in stash. Ctrl+S restores it.", "편집한 초안을 보관했습니다. Ctrl+S로 복원할 수 있습니다."))
			}
			return m, nil, true
		}
		if msg.err != nil {
			m.notify(m.tr("Editor did not finish: ", "편집기를 완료하지 못했습니다. ") + msg.err.Error())
			return m, nil, true
		}
		m.recordUndo(m.captureDraft())
		m.prompt.SetValue(msg.text)
		m.prompt.MoveToEnd()
		m.markDraftChanged()
		m.layout()
		m.notify(m.tr("Draft updated from editor. Enter to send.", "편집기의 초안을 불러왔습니다. Enter로 보내세요."))
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
		m.attachmentsPending = max(0, m.attachmentsPending-1)
		if msg.err != nil {
			m.notify(m.tr("Could not attach file: ", "파일을 첨부하지 못했습니다. ") + msg.err.Error())
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
				m.notify(m.tr("Attach up to five text files per prompt.", "질문에 텍스트 파일을 최대 5개까지 첨부할 수 있습니다."))
				return m, nil, true
			}
			m.files = append(m.files, msg.file)
		}
		if msg.query.Inline && m.prompt.Value() == msg.query.Original {
			ref := "@" + msg.file.Path
			if strings.ContainsAny(msg.file.Path, " \t") {
				ref = "@\"" + msg.file.Path + "\""
			}
			runes := []rune(m.prompt.Value())
			m.prompt.SetValue(string(runes[:msg.query.Start]) + ref + " " + string(runes[msg.query.End:]))
			m.setCursorIndex(msg.query.Start + len([]rune(ref)) + 1)
		}
		m.markDraftChanged()
		m.layout()
		m.notify(fmt.Sprintf(m.tr("Attached %s. /detach removes attachments.", "첨부 완료 · %s · /detach로 제거"), msg.file.Path))
		return m, nil, true
	case tickMsg:
		if m.gateway != nil && !m.modelsStarted && m.startupDone && !m.loadingHistory && !m.loadingSession {
			m.modelsStarted = true
			return m, tea.Batch(tick(), m.loadModels()), true
		}
		if m.renderPending {
			m.renderMessages()
		}
		var save tea.Cmd
		if (m.dirty || m.pendingSave != nil) && !m.loadingSession && time.Now().After(m.nextSave) && time.Since(m.lastEdit) > 700*time.Millisecond {
			save = m.persist()
		}
		if save == nil && !m.busy && !m.saving && !m.recovering && m.saveBlocked == "" && !m.modelsFocused && len(m.queue) > 0 && !m.queuePaused {
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
			m.notify(m.tr("Could not complete the action: ", "작업을 완료하지 못했습니다. ") + msg.err.Error())
		} else {
			m.notify(msg.text)
		}
		return m, nil, true
	case recoverySaved:
		return m.receiveRecovery(msg)
	case recoveryDraftLoaded:
		if msg.err != nil {
			m.notify("Could not restore a draft: " + terminalSafe(msg.err.Error()))
			return m, nil, true
		}
		current := m.captureDraft()
		if msg.epoch != m.sessionEpoch || current.Text != msg.before.Text || !slices.Equal(current.Files, msg.before.Files) {
			m.notify("The draft changed while loading. Press F7 to restore again.")
			return m, nil, true
		}
		m.recordUndo(current)
		m.prompt.SetValue(msg.draft.Text)
		m.prompt.CursorEnd()
		m.files = append([]client.Attachment(nil), msg.draft.Files...)
		m.markDraftChanged()
		m.layout()
		m.notify("Draft restored from recovery. Ctrl+Z to undo.")
		return m, nil, true
	case historyLoaded:
		if msg.epoch != m.sessionEpoch {
			return m, nil, true
		}
		m.loadingHistory = false
		m.startupDone = true
		if msg.err != nil {
			m.notify(m.tr("Could not load conversations. Press Alt+H to try again.", "대화를 불러오지 못했습니다. Alt+H로 다시 시도하세요."))
			return m, nil, true
		}
		m.historyPage = historyPager{paged: msg.paged, ready: msg.paged, cursor: msg.cursor, lookup: msg.lookup, sequence: m.historyPage.sequence + 1}
		if msg.paged {
			m.allHistory = nil
			m.history = msg.records
		} else {
			m.allHistory = msg.records
			m.history = client.ScopeConversations(msg.records, m.workdir, m.allSessions)
		}
		if !msg.initial && m.historyQuery != "" {
			query := m.historyQuery
			m.historyQuery = ""
			matches := m.history
			if !msg.paged && query == "--last" {
				matches = nil
				if latest, ok := client.LatestConversation(m.history, m.workdir, m.allSessions); ok {
					matches = []client.ConversationSummary{latest}
				}
			} else if !msg.paged {
				matches = client.FindConversations(m.history, query)
			}
			if len(matches) == 1 {
				next, cmd := m.navigate("open:" + matches[0].ID)
				return next, cmd, true
			}
			if len(matches) > 1 {
				m.history = matches
			} else {
				m.notify(m.tr("No matching session. Choose another saved conversation.", "일치하는 세션이 없습니다. 다른 대화를 선택하세요."))
			}
		}
		if msg.initial {
			if m.startMode == "new" || m.startNew {
				return m, nil, true
			}
			if m.resumeID != "" {
				matches := m.history
				if !msg.paged {
					matches = client.FindConversations(m.history, m.resumeID)
				}
				if len(matches) == 1 {
					return m, m.loadConversation(matches[0].ID), true
				}
				if len(matches) > 0 {
					m.history = matches
				} else {
					m.notify(m.tr("No matching session. Choose a saved conversation from the list.", "일치하는 세션이 없습니다. 목록에서 저장된 대화를 선택하세요."))
				}
				next, cmd := m.openMenu("history")
				return next, cmd, true
			}
			if m.startMode == "resume" {
				if len(m.history) == 0 && !msg.paged {
					m.notify(m.tr("No saved sessions here. Start a conversation, or use freeway resume --all.", "저장된 세션이 없습니다. 새 대화를 시작하거나 freeway resume --all로 확인하세요."))
					return m, nil, true
				}
				next, cmd := m.openMenu("history")
				return next, cmd, true
			}
			if latest, ok := client.LatestConversation(m.history, m.workdir, m.allSessions); ok && m.prompt.Value() == "" && len(m.messages) == 0 {
				return m, m.loadConversation(latest.ID), true
			}
			m.notify(m.tr("No previous session in this project. Starting a new conversation.", "이 프로젝트의 이전 세션이 없어 새 대화를 시작합니다."))
			return m, nil, true
		}
		if len(msg.records) == 0 && !msg.paged {
			m.notify(m.tr("No saved conversations yet. Conversations and drafts are saved automatically.", "저장된 대화가 없습니다. 대화와 초안은 자동으로 저장됩니다."))
			return m, nil, true
		}
		next, cmd := m.openMenu("history")
		return next, cmd, true
	case historySearch:
		if m.modelsFocused && m.menu == "history" && msg.sequence == m.historyPage.sequence && msg.epoch == m.sessionEpoch {
			next, cmd := m.fetchHistoryPage(false)
			return next, cmd, true
		}
		return m, nil, true
	case historyPageFetched:
		return m.receiveHistoryPage(msg)
	case conversationLoaded:
		if msg.epoch != m.sessionEpoch {
			return m, nil, true
		}
		m.loadingSession = false
		if msg.err != nil {
			message := m.tr("Could not open conversation: ", "대화를 열지 못했습니다. ") + friendlyError(msg.err, m.prefs)
			if m.recoveryPath != "" && m.recoveryAction != "" {
				m.notice = m.tr("Recovery copy: ", "복구 사본: ") + terminalSafe(m.recoveryPath)
				m.renderMessages()
			}
			m.notify(message)
			return m, nil, true
		}
		if m.attachmentsPending > 0 || m.prompt.Value() != msg.draftAtLoad || !slices.Equal(m.files, msg.filesAtLoad) {
			m.notify(m.tr("Kept the current draft. Reopen the saved conversation when you finish editing.", "현재 초안을 유지했습니다. 편집을 마친 뒤 저장된 대화를 다시 여세요."))
			return m, nil, true
		}
		m.sessionEpoch++
		m.session = msg.chat
		m.shorterMax = 0
		m.fileEpoch++
		m.fileItems = nil
		m.filesLoading = false
		m.undoDrafts, m.redoDrafts = nil, nil
		oldWorkspace := m.workdir
		if msg.chat.Workspace != "" {
			if info, err := os.Stat(msg.chat.Workspace); err == nil && info.IsDir() {
				m.workdir = msg.chat.Workspace
			}
		}
		if !client.SameWorkspace(oldWorkspace, m.workdir) {
			m.promptRequest++
			m.allPrompts, m.promptsLoading, m.promptsIncomplete = false, false, false
			m.savedPrompts, m.inputHistory, m.inputFiles = nil, nil, nil
			m.inputIndex, m.inputDraft, m.lastRecalled, m.inputDraftFiles = -1, "", "", nil
		}
		m.messages = append([]client.Message(nil), msg.chat.Messages...)
		m.files = append([]client.Attachment(nil), msg.chat.DraftFiles...)
		m.attachmentsPending = 0
		m.stash = nil
		if msg.chat.Stash != nil {
			m.stash = &draftState{Text: msg.chat.Stash.Text, Files: append([]client.Attachment(nil), msg.chat.Stash.Files...)}
		}
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
		m.prompt.SetValue(msg.chat.Draft)
		m.prompt.CursorEnd()
		m.dirty = false
		m.clearSaveState()
		m.notice = ""
		m.layout()
		m.renderMessages()
		m.chat.GotoBottom()
		m.notify(m.tr("Conversation restored.", "대화를 복원했습니다."))
		if len(m.queue) > 0 {
			m.notify(fmt.Sprintf(m.tr("Conversation restored · %d follow-up(s) paused · /queue resume", "대화를 복원했습니다 · 후속 질문 %d개 일시정지 · /queue resume"), len(m.queue)))
		}
		return m, projectContext(m.workdir), true
	case conversationSaved:
		if msg.epoch != m.sessionEpoch {
			return m, nil, true
		}
		if msg.err == nil && m.session.ID != "" && msg.chat.ID != m.session.ID {
			return m, nil, true
		}
		m.saving = false
		action := m.afterSave
		m.afterSave = ""
		if msg.err != nil {
			m.saveError = friendlyError(msg.err, m.prefs)
			m.nextSave = time.Now().Add(10 * time.Second)
			m.saveBlocked = blockedSave(msg.err)
			if m.saveBlocked != "" {
				m.pendingSave = nil
				m.dirty = true
				m.queuePaused = true
			}
			if action == "generate" {
				next, cmd := m.beginGeneration()
				return next, cmd, true
			}
			if action != "" {
				m.recoveryAction = action
			}
			m.notify(m.saveError + m.tr(" F6 saves a plain-text recovery file before moving on.", " F6으로 일반 텍스트 복구 파일을 저장한 뒤 이동하세요."))
			return m, nil, true
		}
		m.pendingSave = nil
		m.session.ID, m.session.Revision = msg.chat.ID, msg.chat.Revision
		if m.session.Title == "" || m.session.Title == msg.titleAtSave {
			m.session.Title = msg.chat.Title
		}
		m.saveError = ""
		m.saveBlocked, m.recoveryAction = "", ""
		m.nearSaveLimit = msg.size >= client.MaxBody-1024*1024
		m.nextSave = time.Time{}
		if m.busy {
			m.nextSave = time.Now().Add(time.Second)
		}
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
	for _, model := range m.availableModels {
		if model.ID == m.selected || slices.Contains(model.RouteIDs, m.selected) {
			m.selected, m.listedSelection = model.ID, model.ID
			return
		}
	}
}

func (m *Model) snapshot() client.Conversation {
	chat := m.session
	chat.Model = m.selected
	chat.Workspace = m.workdir
	chat.Settings = m.settings
	chat.Draft = m.prompt.Value()
	chat.DraftFiles = append([]client.Attachment(nil), m.files...)
	chat.Queued = append([]client.PendingPrompt(nil), m.queue...)
	chat.Stash = nil
	if m.stash != nil {
		chat.Stash = &client.PendingPrompt{Text: m.stash.Text, Files: append([]client.Attachment(nil), m.stash.Files...)}
	}
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
	if m.loadingSession || m.loadingHistory || m.recovering || m.saveBlocked != "" {
		return nil
	}
	backend, ok := m.gateway.(conversationGateway)
	if !ok || !m.saveEnabled {
		m.dirty = false
		return nil
	}
	if m.saving {
		return nil
	}
	if len(m.messages) == 0 && m.prompt.Value() == "" && len(m.files) == 0 && len(m.queue) == 0 && m.stash == nil && m.session.ID == "" {
		m.dirty = false
		return nil
	}
	if m.pendingSave == nil {
		snapshot := m.snapshot()
		snapshot.SaveKey = rand.Text()
		m.pendingSave = &snapshot
		m.dirty = false
	}
	snapshot := *m.pendingSave
	epoch := m.sessionEpoch
	m.saving = true
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
		defer cancel()
		size, err := snapshot.SaveSize()
		if size > client.MaxBody {
			err = &client.GatewayError{Code: "conversation_too_large", Status: 413, Message: "This conversation exceeds the 8 MiB save limit. Press F6 to keep a recovery file."}
		}
		var chat client.Conversation
		if err == nil {
			chat, err = backend.SaveConversation(ctx, snapshot)
		}
		return conversationSaved{chat: chat, epoch: epoch, err: err, titleAtSave: snapshot.Title, size: size}
	}
}

func blockedSave(err error) string {
	var gateway *client.GatewayError
	if errors.As(err, &gateway) {
		if gateway.Code == "save_uncertain" {
			return gateway.Code
		}
		if gateway.Status >= 400 && gateway.Status < 500 && gateway.Status != 408 && gateway.Status != 429 {
			if gateway.Code != "" {
				return gateway.Code
			}
			return "save_rejected"
		}
	}
	return ""
}

func (m *Model) clearSaveState() {
	m.pendingSave = nil
	m.saveError, m.saveBlocked, m.recoveryAction = "", "", ""
	m.nearSaveLimit = false
	m.nextSave = time.Time{}
}

func (m Model) showHistory(initial bool) (tea.Model, tea.Cmd) {
	if m.loadingSession {
		m.notify("Wait for the conversation to finish loading.")
		return m, nil
	}
	if m.busy {
		m.notify(m.tr("Press Esc to stop the response first.", "먼저 Esc로 응답을 중지하세요."))
		return m, nil
	}
	backend, ok := m.gateway.(conversationGateway)
	if !ok {
		m.startupDone = true
		m.notify(m.tr("Saved conversations are not available in preview mode.", "미리보기에서 저장된 대화를 열 수 없습니다."))
		return m, nil
	}
	if m.loadingHistory && !initial {
		return m, nil
	}
	m.loadingHistory = true
	epoch := m.sessionEpoch
	if initial && (m.startNew || m.startMode == "new") {
		return m, func() tea.Msg { return historyLoaded{initial: true, epoch: epoch} }
	}
	query := m.initialHistoryQuery(initial)
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
		defer cancel()
		if pager, ok := m.gateway.(historyPageGateway); ok {
			page, err := pager.HistoryPage(ctx, query)
			lookup := ""
			if query.Mode == "lookup" {
				lookup = query.Search
			}
			return historyLoaded{records: page.Records, initial: initial, epoch: epoch, cursor: page.NextCursor, paged: page.Paged, lookup: lookup, err: err}
		}
		records, err := backend.Conversations(ctx)
		return historyLoaded{records: records, initial: initial, epoch: epoch, err: err}
	}
}

func (m *Model) loadConversation(id string) tea.Cmd {
	backend, ok := m.gateway.(conversationGateway)
	if !ok {
		return nil
	}
	m.stopResponse()
	m.generation++
	m.loadingSession = true
	epoch := m.sessionEpoch
	draftAtLoad := m.prompt.Value()
	filesAtLoad := slices.Clone(m.files)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
		defer cancel()
		chat, err := backend.Conversation(ctx, id)
		return conversationLoaded{chat: chat, epoch: epoch, err: err, draftAtLoad: draftAtLoad, filesAtLoad: filesAtLoad}
	}
}

func (m Model) navigate(action string) (tea.Model, tea.Cmd) {
	if m.recovering {
		m.notify("Writing recovery file…")
		return m, nil
	}
	if m.busy {
		if action == "new" || action == "quit" || action == "fork" {
			m.queuePaused = true
			m.stopResponse()
			m.dirty = true
			m.renderMessages()
		} else {
			m.notify(m.tr("Press Esc to stop the response first. Your draft will stay here.", "Esc로 응답을 먼저 중지하세요. 초안은 보존됩니다."))
			return m, nil
		}
	}
	if m.loadingSession {
		m.notify(m.tr("Loading conversation…", "대화를 불러오는 중…"))
		return m, nil
	}
	if m.saving {
		m.afterSave = action
		return m, nil
	}
	if m.saveBlocked != "" {
		m.recoveryAction = action
		m.notify(m.saveError + " Press F6 to save a plain-text recovery file and continue.")
		return m, nil
	}
	if m.dirty || m.pendingSave != nil {
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
		m.shorterMax = 0
		m.messages = nil
		m.renderCache = nil
		m.prompt.Reset()
		m.dirty = false
		m.clearSaveState()
		m.notice = ""
		m.inputIndex = -1
		m.loadingHistory = false
		m.files = nil
		m.attachmentsPending = 0
		m.stash = nil
		m.queue = nil
		m.queuePaused = false
		m.editingAt = -1
		m.pendingEdited = nil
		m.undoDrafts = nil
		m.redoDrafts = nil
		m.layout()
		m.renderMessages()
		m.chat.GotoBottom()
		m.notify(m.tr("New conversation · Press Alt+H to reopen earlier work.", "새 대화 · Alt+H로 이전 대화 열기"))
		return m, nil
	case strings.HasPrefix(action, "open:"):
		return m, m.loadConversation(strings.TrimPrefix(action, "open:"))
	case action == "fork" || action == "fork-edit":
		original := m.session
		title := []rune(original.Title)
		if len(title) > 110 {
			title = title[:110]
		}
		m.session = client.Conversation{Model: m.selected, SystemPrompt: original.SystemPrompt, Settings: m.settings, Context: original.Context, Title: string(title) + " (fork)"}
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
		m.notify(m.tr("Fork created. The original conversation is preserved.", "대화를 분기했습니다. 원본은 보존됩니다."))
		m.renderMessages()
		return m, m.persist()
	}
	return m, nil
}
