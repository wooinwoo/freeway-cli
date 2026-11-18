package tui

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type transcriptMatch struct {
	row, start, end int
}

type transcriptSearch struct {
	input           textinput.Model
	lines           []string
	matches         []transcriptMatch
	index, startRow int
}

func (m Model) findTranscript(query string) (tea.Model, tea.Cmd) {
	if m.loadingSession || m.loadingHistory {
		m.notify("Wait for the conversation to load before searching.")
		return m, nil
	}
	if len(m.messages) == 0 {
		m.notify("Send a message to search this conversation.")
		return m, nil
	}
	input := textinput.New()
	input.Prompt, input.Placeholder, input.CharLimit = "Find: ", "Search this conversation…", 256
	input.SetVirtualCursor(false)
	input.SetWidth(max(1, m.width-10))
	input.SetValue(query)
	m.promptSearchWanted = false
	m.prompt.Blur()
	m.find = &transcriptSearch{input: input, startRow: m.chat.YOffset()}
	m.findTranscriptContent(m.chat.GetContent(), true)
	return m, m.find.input.Focus()
}

func (m *Model) findTranscriptContent(content string, queryChanged bool) {
	search := m.find
	if search == nil {
		m.chat.SetContent(content)
		return
	}
	search.lines = strings.Split(content, "\n")
	search.matches = nil
	if query := search.input.Value(); query != "" {
		pattern := regexp.MustCompile("(?i)" + regexp.QuoteMeta(query))
		for row, line := range search.lines {
			for _, pair := range pattern.FindAllStringIndex(ansi.Strip(line), -1) {
				search.matches = append(search.matches, transcriptMatch{row, pair[0], pair[1]})
			}
		}
	}
	if queryChanged {
		search.index = slices.IndexFunc(search.matches, func(item transcriptMatch) bool { return item.row >= search.startRow })
		if search.index < 0 {
			search.index = len(search.matches) - 1
		}
	} else {
		search.index = min(max(0, search.index), len(search.matches)-1)
	}
	m.showTranscriptMatch()
}

func (m *Model) showTranscriptMatch() {
	search := m.find
	lines := slices.Clone(search.lines)
	if search.index >= 0 && search.index < len(search.matches) {
		match := search.matches[search.index]
		line := ansi.Strip(lines[match.row])
		highlight := lipgloss.NewStyle().Reverse(true).Bold(true).Render(line[match.start:match.end])
		lines[match.row] = line[:match.start] + highlight + line[match.end:]
		m.chat.SetContentLines(lines)
		m.chat.EnsureVisible(match.row, 0, 0)
		return
	}
	m.chat.SetContentLines(lines)
}

func (m Model) updateTranscriptSearch(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	if m.find == nil {
		return m, nil, false
	}
	// Copy the state holder so Bubble Tea's model updates do not mutate a prior model.
	state := *m.find
	m.find = &state
	switch key := msg.(type) {
	case tea.KeyPressMsg:
		switch key.String() {
		case "esc", "ctrl+c", "enter":
			m.chat.SetContentLines(slices.Clone(state.lines))
			if key.String() != "enter" {
				m.chat.SetYOffset(state.startRow)
			}
			m.find = nil
			return m, m.prompt.Focus(), true
		case "up", "ctrl+p", "shift+f3", "down", "ctrl+n", "f3":
			if n := len(state.matches); n > 0 {
				delta := 1
				switch key.String() {
				case "up", "ctrl+p", "shift+f3":
					delta = -1
				}
				m.find.index = (state.index + delta + n) % n
				m.showTranscriptMatch()
			}
			return m, nil, true
		}
	case tea.PasteMsg:
	default:
		return m, nil, false
	}
	previous := state.input.Value()
	var cmd tea.Cmd
	m.find.input, cmd = m.find.input.Update(msg)
	if previous != m.find.input.Value() {
		m.findTranscriptContent(strings.Join(state.lines, "\n"), true)
	}
	return m, cmd, true
}

func (m Model) transcriptHint() string {
	switch {
	case m.find.input.Value() == "":
		return "Type to find · Esc cancel"
	case len(m.find.matches) == 0:
		return "No matches · Esc cancel"
	default:
		return fmt.Sprintf("%d/%d · ↑↓ browse · Enter jump · Esc cancel", m.find.index+1, len(m.find.matches))
	}
}
