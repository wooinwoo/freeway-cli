package tui

import (
	"context"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

const minWidth, minHeight = 44, 12

// Model owns transient screen state and delegates requests to its gateway adapter.
type Model struct {
	interactionState
	width, height                                                           int
	chat                                                                    viewport.Model
	find                                                                    *transcriptSearch
	prompt                                                                  textarea.Model
	models                                                                  modelsList
	modelsFocused                                                           bool
	selected                                                                string
	messages                                                                []client.Message
	gateway                                                                 gateway
	ctx                                                                     context.Context
	busy                                                                    bool
	cancel                                                                  context.CancelFunc
	events                                                                  <-chan client.Event
	generation                                                              uint64
	status                                                                  string
	notice                                                                  string
	loadingModels                                                           bool
	modelsStarted                                                           bool
	ready                                                                   bool
	availableModels                                                         []client.Model
	menu                                                                    string
	menuQuery                                                               string
	menuAll                                                                 []list.Item
	menuSearching                                                           bool
	providers                                                               map[string]client.ProviderStatus
	providersLoading                                                        bool
	providersError                                                          string
	providersRequest                                                        uint64
	openCodeLoading                                                         bool
	openCodeError                                                           string
	openCodeRequest                                                         uint64
	inputHistory                                                            []string
	inputIndex                                                              int
	inputDraft, lastRecalled                                                string
	inputFiles                                                              map[string][]client.Attachment
	inputDraftFiles                                                         []client.Attachment
	historyQuery                                                            string
	commandIndex                                                            int
	startedAt, lastRender, lastEdit                                         time.Time
	nextSave                                                                time.Time
	renderPending                                                           bool
	renderCache                                                             []renderedMessage
	toast                                                                   string
	toastUntil                                                              time.Time
	settings                                                                client.Settings
	session                                                                 client.Conversation
	sessionEpoch                                                            uint64
	dirty, saving, saveEnabled, loadingHistory, loadingSession, startupDone bool
	saveError, afterSave, resumeID                                          string
	startNew                                                                bool
	startMode                                                               string
	allSessions                                                             bool
	reconnectAttempts                                                       int
	history                                                                 []client.ConversationSummary
	allHistory                                                              []client.ConversationSummary
	copyText                                                                func(string) error
	quitting                                                                bool
}

func New() Model {
	dir, _ := os.Getwd()
	m := Model{
		interactionState: interactionState{editingAt: -1, workdir: dir},
		chat:             viewport.New(), prompt: newPrompt(), models: newModelsList(),
		selected:    "preview/auto",
		inputIndex:  -1,
		settings:    client.Settings{MaxTokens: 4096, Temperature: 0.6},
		saveEnabled: true,
	}
	for _, item := range m.models.list.Items() {
		if model, ok := item.(modelItem); ok {
			m.availableModels = append(m.availableModels, client.Model(model))
		}
	}
	return m
}

func (m Model) Init() tea.Cmd {
	if m.gateway != nil {
		next, history := m.showHistory(true)
		m = next.(Model)
		if history != nil {
			return tea.Batch(tick(), tea.RequestBackgroundColor, projectContext(m.workdir), history)
		}
		return tea.Batch(m.loadModels(), tick(), tea.RequestBackgroundColor, projectContext(m.workdir))
	}
	return tea.Batch(tick(), tea.RequestBackgroundColor, projectContext(m.workdir))
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if next, cmd, handled := m.updateConvenience(msg); handled {
		return next, cmd
	}
	if next, cmd, handled := m.updateTranscriptSearch(msg); handled {
		return next, cmd
	}
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		lightBackground.Store(!msg.IsDark())
		m.renderCache = nil
		applyPromptTheme(&m.prompt)
		m.renderMessages()
		return m, nil
	case modelsLoaded:
		return m.receiveModels(msg)
	case providerStatusesLoaded:
		return m.receiveProviderStatuses(msg)
	case openCodeModelsLoaded:
		return m.receiveOpenCodeModels(msg)
	case responseEvent:
		return m.receiveResponse(msg)
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		if m.find == nil && !m.modelsFocused && m.usable() {
			return m, m.prompt.Focus()
		}
		return m, nil
	case tea.KeyPressMsg:
		if next, cmd, handled := m.shortcut(msg); handled {
			return next, cmd
		}
		switch msg.String() {
		case "ctrl+c":
			return m.handleInterrupt()
		case "tab":
			if m.usable() && m.commandPending() && !m.modelsFocused {
				m.prompt.SetValue(m.commandSuggestion().Name)
				m.layout()
				return m, nil
			}
			if m.busy && !m.modelsFocused {
				return m.queuePrompt()
			}
			if m.usable() {
				if m.modelsFocused {
					if m.menu == "files" {
						if item, ok := m.models.list.SelectedItem().(modelItem); ok {
							return m.chooseItem(item)
						}
						return m, nil
					}
					return m.dismissMenu()
				}
				if token, ok := m.fileToken(); ok {
					return m.openFiles(token)
				}
				before := m.captureDraft()
				m.recordUndo(before)
				m.prompt.InsertString("    ")
				m.markDraftChanged()
				m.layout()
				return m, nil
			}
		case "esc":
			if m.modelsFocused {
				return m.dismissMenu()
			}
			if m.busy {
				m.queuePaused = true
				m.stopResponse()
				m.status = "Stopped"
				m.dirty = true
				m.renderMessages()
				return m, m.persist()
			}
			if m.editingAt >= 0 {
				m.editingAt = -1
				m.prompt.Reset()
				m.files = nil
				m.markDraftChanged()
				m.layout()
				m.notify("Edit cancelled. The original conversation is unchanged.")
				return m, nil
			}
			if time.Since(m.lastEscape) < 500*time.Millisecond {
				m.lastEscape = time.Time{}
				return m.doubleEscape()
			}
			m.lastEscape = time.Now()
			return m, nil
		case "pgup":
			if !m.modelsFocused {
				m.chat.HalfPageUp()
				return m, nil
			}
		case "pgdown":
			if !m.modelsFocused {
				m.chat.HalfPageDown()
				return m, nil
			}
		case "enter":
			if !m.usable() {
				return m, nil
			}
			if m.modelsFocused {
				if m.models.list.SettingFilter() {
					break
				}
				if item, ok := m.models.list.SelectedItem().(modelItem); ok {
					return m.chooseItem(item)
				}
				return m, nil
			}
			if m.busy {
				if strings.HasPrefix(strings.TrimSpace(m.prompt.Value()), "/") {
					return m.submit()
				}
				return m.queuePrompt()
			}
			return m.submit()
		}
	case tea.PasteMsg:
		if m.modelsFocused {
			return m.appendMenuQuery(msg.Content)
		}
	case tea.MouseClickMsg:
		if m.showLatestButton() && msg.Button == tea.MouseLeft {
			x, y := m.latestButtonPosition()
			if msg.Y == y && msg.X >= x && msg.X < m.width-4 {
				m.chat.GotoBottom()
				return m, nil
			}
		}
	case tea.MouseWheelMsg:
		if m.modelsFocused {
			var cmd tea.Cmd
			m.models, cmd = m.models.Update(msg)
			return m, cmd
		}
		var cmd tea.Cmd
		m.chat, cmd = m.chat.Update(msg)
		return m, cmd
	}
	if !m.usable() {
		return m, nil
	}
	var cmd tea.Cmd
	if m.modelsFocused {
		m.models, cmd = m.models.Update(msg)
	} else {
		before := m.captureDraft()
		m.prompt, cmd = m.prompt.Update(msg)
		if key, ok := msg.(tea.KeyPressMsg); ok && before.Text == m.prompt.Value() && key.String() != "" {
			m.lastEditAction = "navigation"
		}
		if before.Text != m.prompt.Value() {
			m.recordUndo(before)
			m.lastEditAction = "typing"
			m.dirty, m.lastEdit = true, time.Now()
			m.commandIndex = 0
			if m.prompt.Value() != m.lastRecalled {
				m.inputIndex = -1
			}
			if key, ok := msg.(tea.KeyPressMsg); ok && key.Text == "@" {
				if token, found := m.fileToken(); found {
					return m.openFiles(token)
				}
			}
		}
		m.layout()
	}
	return m, cmd
}

func (m Model) usable() bool { return m.width >= minWidth && m.height >= minHeight }

func (m Model) modelCommandPending() bool {
	value := m.prompt.Value()
	return !m.modelsFocused && !m.busy && strings.HasPrefix(value, "/") && strings.HasPrefix("/model", value)
}

func (m Model) changeFocus(models bool) (tea.Model, tea.Cmd) {
	m.modelsFocused = models
	var cmd tea.Cmd
	if models {
		if m.menu == "" {
			m.menu = "models"
		}
		if m.menu == "models" {
			m.models.list.SetItems(m.modelPickerItems())
		}
		m.prompt.Blur()
		m.menuAll = append([]list.Item(nil), m.models.list.Items()...)
		m.menuQuery = ""
		m.menuSearching = false
		m.models.list.ResetFilter()
		for i, item := range m.models.list.Items() {
			if item.(modelItem).ID == m.selected {
				m.models.list.Select(i)
				break
			}
		}
	} else {
		m.menu = ""
		cmd = m.prompt.Focus()
	}
	m.layout()
	if models && m.menu == "models" && m.gateway != nil && !m.loadingModels && !m.busy {
		m.loadingModels = true
		return m, tea.Batch(cmd, m.loadModels())
	}
	return m, cmd
}

func (m *Model) resize() {
	if !m.usable() {
		return
	}
	m.chat.SetWidth(m.width - 4)
	if m.find != nil {
		m.find.input.SetWidth(max(1, m.width-10))
	}
	m.prompt.SetWidth(m.width - 8)
	m.layout()
	m.renderMessages()
}

func (m *Model) layout() {
	if !m.usable() {
		return
	}
	// The textarea keeps unlimited content; only its visible height is capped.
	extra := len(m.composerExtraRows(max(1, m.width-4)))
	visibleLimit := min(8, max(1, m.height-11-extra))
	minimum := 1
	if m.height >= 22 {
		minimum = 3
	} else if m.height >= 16 {
		minimum = 2
	}
	m.prompt.MinHeight = min(minimum, visibleLimit)
	promptHeight := min(visibleLimit, max(m.prompt.MinHeight, m.prompt.Height()))
	if m.prompt.Height() != promptHeight {
		m.prompt.SetHeight(promptHeight)
	}
	bottomHeight := m.prompt.Height() + 4 + extra
	menuHeight := max(1, m.height-8)
	if m.modelsFocused {
		if m.inlineFilePicker() {
			composerHeight := m.prompt.Height() + 3 + extra
			menuHeight = max(1, min(5, m.height-6-composerHeight))
			bottomHeight = composerHeight + menuHeight + 2
		} else {
			menuHeight -= m.promptPreviewHeight()
			bottomHeight = menuHeight + 4
		}
	}
	if m.models.list.Width() != m.width-4 || m.models.list.Height() != menuHeight {
		m.models.list.SetSize(m.width-4, menuHeight)
	}
	height := max(1, m.height-3-bottomHeight)
	if m.chat.Height() != height {
		follow := m.find == nil && m.chat.AtBottom()
		m.chat.SetHeight(height)
		if follow {
			m.chat.GotoBottom()
		}
	}
}

func (m *Model) renderMessages() {
	m.renderConversation()
}

func (m Model) View() tea.View { return m.workspaceView() }
