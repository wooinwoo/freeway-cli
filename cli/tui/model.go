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
	prefs                                                                   Preferences
	renderedTheme                                                           string
	width, height                                                           int
	chat                                                                    viewport.Model
	find                                                                    *transcriptSearch
	prompt                                                                  textarea.Model
	models                                                                  modelsList
	modelsFocused                                                           bool
	selected                                                                string
	listedSelection                                                         string
	messages                                                                []client.Message
	gateway                                                                 gateway
	ctx                                                                     context.Context
	busy                                                                    bool
	cancel                                                                  context.CancelFunc
	events                                                                  <-chan client.Event
	generation                                                              uint64
	imagePrompt                                                             string
	mcpApproval                                                             *mcpApproval
	mcpRunning                                                              bool
	responseTarget                                                          responseTarget
	shorterMax                                                              int
	status                                                                  string
	notice                                                                  string
	loadingModels                                                           bool
	modelsStarted                                                           bool
	modelListNeedsRefresh                                                   bool
	ready                                                                   bool
	availableModels                                                         []client.Model
	modelRanking                                                            modelRanking
	menu                                                                    string
	menuParent                                                              string // Esc returns here from a submenu.
	menuQuery                                                               string
	menuAll                                                                 []list.Item
	menuSearching                                                           bool
	providers                                                               map[string]client.ProviderStatus
	providerOrder                                                           []string
	providersLoading                                                        bool
	providerChecksPending                                                   map[string]bool
	providersError                                                          string
	providerCheckID                                                         string
	providersRequest                                                        uint64
	modelsEpoch                                                             uint64
	providerSetup                                                           string
	inputHistory                                                            []string
	inputIndex                                                              int
	inputDraft, lastRecalled                                                string
	inputFiles                                                              map[string][]client.Attachment
	inputDraftFiles                                                         []client.Attachment
	historyQuery                                                            string
	historyPage                                                             historyPager
	commandIndex                                                            int
	startedAt, lastRender, lastEdit                                         time.Time
	nextSave                                                                time.Time
	renderPending                                                           bool
	renderCache                                                             []renderedMessage
	toast                                                                   string
	toastUntil                                                              time.Time
	settings                                                                client.Settings
	session                                                                 client.Conversation
	pendingSave                                                             *client.Conversation
	sessionEpoch                                                            uint64
	dirty, saving, saveEnabled, loadingHistory, loadingSession, startupDone bool
	saveError, afterSave, resumeID                                          string
	saveBlocked, recoveryAction, recoveryPath                               string
	nearSaveLimit, recovering                                               bool
	startNew                                                                bool
	startMode                                                               string
	allSessions                                                             bool
	reconnectAttempts                                                       int
	history                                                                 []client.ConversationSummary
	allHistory                                                              []client.ConversationSummary
	copyText                                                                func(string) error
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
	next, cmd := m.update(msg)
	m = next.(Model)
	if m.renderedTheme != m.activeTheme() {
		m.applyPreferences()
	}
	// Rows under the composer, such as the queue or context line, change without a resize.
	// Fit the transcript to them so the status line stays on screen.
	m.layout()
	return m, cmd
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if next, cmd, handled := m.updateMCPApproval(msg); handled {
		return next, cmd
	}
	if next, cmd, handled := m.updateConvenience(msg); handled {
		return next, cmd
	}
	if next, cmd, handled := m.updateTranscriptSearch(msg); handled {
		return next, cmd
	}
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		lightBackground.Store(!msg.IsDark())
		m.applyPreferences()
		return m, nil
	case modelsLoaded:
		return m.receiveModels(msg)
	case providerStatusesLoaded:
		return m.receiveProviderStatuses(msg)
	case providerSetupFinished:
		if msg.err != nil {
			m.notify(m.tr("Could not open provider setup.", "공급처 연결 안내를 열지 못했습니다."))
		}
		next, cmd := m.refreshProviders(msg.id)
		m = next.(Model)
		m.loadingModels = true
		m.modelListNeedsRefresh = false
		m.modelsEpoch++
		m.modelsStarted = true
		return m, tea.Batch(cmd, m.modelLoad(true, false))
	case responseEvent:
		return m.receiveResponse(msg)
	case mcpPlanLoaded:
		return m.receiveMCPPlan(msg)
	case mcpToolFinished:
		return m.receiveMCPToolResult(msg)
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		if m.find == nil && !m.modelsFocused && m.usable() {
			return m, m.prompt.Focus()
		}
		return m, nil
	case tea.KeyPressMsg:
		if msg.String() == "f5" && !m.modelsFocused {
			return m.retryShorter()
		}
		if msg.String() == "f6" {
			return m.recoverConversation()
		}
		if msg.String() == "f7" {
			return m.restoreRecovery("")
		}
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
				// Stopping holds follow-ups already queued. An empty queue stays open for new ones.
				m.queuePaused = m.queuePaused || len(m.queue) > 0
				mcpRunning := m.mcpRunning
				m.stopResponse()
				m.status = "Stopped"
				if mcpRunning {
					m.notify(m.mcpInterruptNotice())
				}
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
				m.notify(m.tr("Edit cancelled. The original conversation is unchanged.", "편집을 취소했습니다. 원래 대화를 유지합니다."))
				return m, nil
			}
			if len(m.commandMatches()) > 0 {
				// Esc closes the command list, as its hint says; Ctrl+Z brings the typed command back.
				m.recordUndo(m.captureDraft())
				m.prompt.Reset()
				m.markDraftChanged()
				m.layout()
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
			if m.themePicker() {
				return m, nil
			}
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
	case tea.MouseMotionMsg:
		if msg.Button == tea.MouseLeft {
			// Freeway takes the mouse for scrolling, so a plain drag cannot select text.
			m.notify(m.tr("To select text, hold Shift while dragging · /copy copies the reply", "글자를 선택하려면 Shift를 누른 채 드래그하세요 · /copy는 답변 복사"))
			return m, nil
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

func (m Model) openModels() (tea.Model, tea.Cmd) {
	m.menu = "models"
	return m.changeFocus(true)
}

func (m Model) refreshModelPicker(reconnect bool) (tea.Model, tea.Cmd) {
	if m.loadingModels {
		m.notify(m.tr("Already checking models…", "이미 모델을 확인 중입니다…"))
		return m, nil
	}
	m.reconnectAttempts = 0
	m.loadingModels = true
	m.modelListNeedsRefresh = false
	if !m.busy && !m.modelsFocused {
		m.status = "Connecting"
	}
	return m, m.modelLoad(true, reconnect)
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
		m.models.list.Select(0)
		switch m.menu {
		case "models":
			m.selectMenuItem(m.selected)
		case "context":
			m.selectMenuItem("turns")
		}
	} else {
		m.menu, m.menuParent = "", ""
		cmd = m.prompt.Focus()
	}
	m.layout()
	if models && m.menu == "models" && m.gateway != nil && !m.loadingModels && !m.busy {
		m.loadingModels = true
		refresh := m.modelListNeedsRefresh
		m.modelListNeedsRefresh = false
		return m, tea.Batch(cmd, m.modelLoad(refresh, false))
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
	// The composer starts at one row and grows with its text, as in most terminal chat tools.
	m.prompt.MinHeight = 1
	promptHeight := min(visibleLimit, max(m.prompt.MinHeight, m.prompt.Height()))
	if m.prompt.Height() != promptHeight {
		m.prompt.SetHeight(promptHeight)
	}
	bottomHeight := m.prompt.Height() + 4 + extra
	menuHeight := max(1, m.height-8)
	if m.modelsFocused {
		if m.themePicker() {
			menuHeight = len(m.models.list.Items())
			bottomHeight = menuHeight + 2
			composerHeight := m.prompt.Height() + 2 + extra
			if m.height-3-bottomHeight-composerHeight >= 3 {
				bottomHeight += composerHeight
			}
		} else if m.inlineFilePicker() {
			composerHeight := m.prompt.Height() + 3 + extra
			menuHeight = max(1, min(5, m.height-6-composerHeight))
			bottomHeight = composerHeight + menuHeight + 2
		} else {
			menuHeight -= m.menuPreviewHeight()
			if m.menu == "models" {
				menuHeight--
			}
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

func (m Model) View() tea.View {
	if m.mcpApproval != nil {
		return themedView(m.mcpApprovalView(), m.prefs.NoColor)
	}
	return themedView(m.workspaceView(), m.prefs.NoColor)
}
