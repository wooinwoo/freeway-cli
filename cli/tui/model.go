package tui

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

const minWidth, minHeight = 44, 12

// Model owns transient screen state and delegates requests to its gateway adapter.
type Model struct {
	width, height int
	chat          viewport.Model
	prompt        textarea.Model
	models        modelsList
	modelsFocused bool
	selected      string
	messages      []client.Message
	gateway       gateway
	ctx           context.Context
	busy          bool
	cancel        context.CancelFunc
	events        <-chan client.Event
	generation    uint64
	status        string
	notice        string
	loadingModels bool
	ready         bool
}

func New() Model {
	return Model{
		chat: viewport.New(), prompt: newPrompt(), models: newModelsList(),
		selected: "preview/auto",
	}
}

func (m Model) Init() tea.Cmd {
	if m.gateway != nil {
		return m.loadModels()
	}
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case modelsLoaded:
		return m.receiveModels(msg)
	case responseEvent:
		return m.receiveResponse(msg)
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		if !m.modelsFocused && m.usable() {
			return m, m.prompt.Focus()
		}
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			m.stopResponse()
			return m, tea.Quit
		case "tab":
			if m.usable() && m.modelCommandPending() {
				m.prompt.SetValue("/model")
				m.layout()
				return m, nil
			}
			if m.usable() {
				return m.changeFocus(!m.modelsFocused)
			}
		case "ctrl+p":
			if m.usable() {
				return m.changeFocus(!m.modelsFocused)
			}
		case "esc":
			if m.busy {
				m.stopResponse()
				m.status = "Cancelled"
				m.renderMessages()
				return m, nil
			}
			if m.modelsFocused {
				return m.changeFocus(false)
			}
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
			if !m.usable() || m.busy {
				return m, nil
			}
			if m.modelsFocused {
				if m.models.list.SettingFilter() {
					break
				}
				if item, ok := m.models.list.SelectedItem().(modelItem); ok {
					m.selected = item.ID
					return m.changeFocus(false)
				}
				return m, nil
			}
			if text := strings.TrimSpace(m.prompt.Value()); text != "" {
				if text == "/model" || text == "/models" || m.modelCommandPending() {
					m.prompt.Reset()
					return m.changeFocus(true)
				}
				if m.gateway != nil && !m.ready {
					return m, nil
				}
				m.messages = append(m.messages, client.Message{Role: "user", Content: text})
				m.prompt.Reset()
				m.layout()
				if m.gateway != nil {
					return m.startResponse()
				}
				m.renderMessages()
				m.chat.GotoBottom()
			}
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
		m.prompt, cmd = m.prompt.Update(msg)
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
	if m.busy {
		return m, nil
	}
	m.modelsFocused = models
	var cmd tea.Cmd
	if models {
		m.prompt.Blur()
		m.models.list.ResetFilter()
		for i, item := range m.models.list.Items() {
			if item.(modelItem).ID == m.selected {
				m.models.list.Select(i)
				break
			}
		}
	} else {
		cmd = m.prompt.Focus()
	}
	m.layout()
	if models && m.gateway != nil && !m.loadingModels {
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
	m.prompt.SetWidth(m.width - 4)
	m.models.list.SetSize(m.width-4, min(8, m.height-8))
	m.layout()
	m.renderMessages()
}

func (m *Model) layout() {
	if !m.usable() {
		return
	}
	// The textarea keeps unlimited content; only its visible height is capped.
	m.prompt.SetHeight(min(6, m.height-7, m.prompt.Height()))
	bottomHeight := m.prompt.Height() + 4
	if m.modelCommandPending() {
		bottomHeight++
	}
	if m.modelsFocused {
		bottomHeight = m.models.list.Height() + 4
	}
	m.chat.SetHeight(max(1, m.height-2-bottomHeight))
}

func (m *Model) renderMessages() {
	colors := grooveColors()
	marker := lipgloss.NewStyle().Foreground(colors.accent).Render("› ")
	text := lipgloss.NewStyle().Width(m.chat.Width() - 2)
	rows := make([]string, 0, len(m.messages))
	for _, message := range m.messages {
		content := ansi.Strip(message.Content)
		if message.Role == "assistant" {
			if content == "" && m.busy {
				content = "Thinking..."
			}
			rows = append(rows, lipgloss.NewStyle().Width(m.chat.Width()).Render(content))
		} else {
			rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, marker, text.Render(content)))
		}
	}
	if m.notice != "" {
		rows = append(rows, lipgloss.NewStyle().Width(m.chat.Width()).Render("Error: "+ansi.Strip(m.notice)))
	}
	m.chat.SetContent(strings.Join(rows, "\n\n"))
}

func (m Model) View() tea.View {
	var screen string
	if !m.usable() {
		screen = lipgloss.NewStyle().MaxWidth(max(1, m.width)).MaxHeight(max(1, m.height)).
			Render("Resize to 44×12 or larger. Ctrl+C to quit.")
	} else {
		colors := grooveColors()
		width := m.width - 4
		line := lipgloss.NewStyle().Foreground(colors.border).Render(strings.Repeat("─", width))
		heading := lipgloss.NewStyle().Foreground(colors.accent).Bold(true).Render("Freeway")
		bottom := lipgloss.JoinVertical(lipgloss.Left, line, m.prompt.View(), line)
		if m.modelCommandPending() {
			bottom = lipgloss.JoinVertical(lipgloss.Left, bottom,
				lipgloss.NewStyle().Foreground(colors.active).Render("› /model  Choose a model"))
		}
		status := m.selected + " · Preview · Not sent"
		if m.gateway != nil {
			status = m.selected + " · " + m.status
		}
		bottom = lipgloss.JoinVertical(lipgloss.Left, bottom, status)
		help := "/model Models  Enter Send  Ctrl+J Newline  PgUp/Dn Scroll  Ctrl+C Quit"
		if width < 76 {
			help = "/model Models  Ctrl+J Newline  ^C Quit"
		}
		if m.modelCommandPending() {
			help = "Enter Open  Tab Complete  Ctrl+C Quit"
		}
		if m.modelsFocused {
			title := "Select model · Preview"
			if m.gateway != nil {
				title = "Select model · Free only"
			}
			bottom = lipgloss.JoinVertical(lipgloss.Left, line, title,
				m.models.View(), m.selected+" · Current")
			help = "↑↓ Move  Enter Select  / Search  Esc Back"
			if m.models.list.SettingFilter() {
				help = "Enter Apply filter  Esc Back"
			}
		}
		bottom = lipgloss.JoinVertical(lipgloss.Left, bottom, help)
		screen = lipgloss.NewStyle().Width(m.width).Padding(0, 2).Render(
			lipgloss.JoinVertical(lipgloss.Left, heading, "", m.chat.View(),
				lipgloss.NewStyle().MaxWidth(width).Render(bottom)))
	}
	view := tea.NewView(screen)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	return view
}
