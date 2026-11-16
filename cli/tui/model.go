package tui

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const minWidth, minHeight = 44, 12

// Model contains only transient screen state. It does not connect to a provider.
type Model struct {
	width, height int
	chat          viewport.Model
	prompt        textarea.Model
	models        modelsList
	modelsFocused bool
	selected      string
	messages      []string
}

func New() Model {
	return Model{
		chat: viewport.New(), prompt: newPrompt(), models: newModelsList(),
		selected: "preview/auto",
	}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
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
			if !m.usable() {
				return m, nil
			}
			if m.modelsFocused {
				if m.models.list.SettingFilter() {
					break
				}
				if item, ok := m.models.list.SelectedItem().(modelItem); ok {
					m.selected = string(item)
					return m.changeFocus(false)
				}
				return m, nil
			}
			if text := strings.TrimSpace(m.prompt.Value()); text != "" {
				if text == "/model" || text == "/models" || m.modelCommandPending() {
					m.prompt.Reset()
					return m.changeFocus(true)
				}
				m.messages = append(m.messages, text)
				m.prompt.Reset()
				m.layout()
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
	return !m.modelsFocused && strings.HasPrefix(value, "/") && strings.HasPrefix("/model", value)
}

func (m Model) changeFocus(models bool) (tea.Model, tea.Cmd) {
	m.modelsFocused = models
	var cmd tea.Cmd
	if models {
		m.prompt.Blur()
		m.models.list.ResetFilter()
		for i, item := range m.models.list.Items() {
			if string(item.(modelItem)) == m.selected {
				m.models.list.Select(i)
				break
			}
		}
	} else {
		cmd = m.prompt.Focus()
	}
	m.layout()
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
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, marker, text.Render(message)))
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
		bottom = lipgloss.JoinVertical(lipgloss.Left, bottom, m.selected+" · Preview · Not sent")
		help := "/model Models  Enter Add  Ctrl+J Newline  PgUp/Dn Scroll  Ctrl+C Quit"
		if width < 76 {
			help = "/model Models  Ctrl+J Newline  ^C Quit"
		}
		if m.modelCommandPending() {
			help = "Enter Open  Tab Complete  Ctrl+C Quit"
		}
		if m.modelsFocused {
			bottom = lipgloss.JoinVertical(lipgloss.Left, line, "Select model · Preview",
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
