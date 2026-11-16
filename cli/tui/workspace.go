package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func padCells(text string, width int) string {
	text = ansi.Truncate(text, max(0, width), "…")
	return text + strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
}

func (m Model) workspaceView() tea.View {
	if !m.usable() {
		view := tea.NewView(lipgloss.NewStyle().MaxWidth(max(1, m.width)).MaxHeight(max(1, m.height)).Render("Resize to at least 44×12. Ctrl+C to quit."))
		view.AltScreen = true
		return view
	}
	c := grooveColors()
	width := m.width - 4
	accent := lipgloss.NewStyle().Foreground(c.accent)
	muted := lipgloss.NewStyle().Foreground(c.muted)
	brand := accent.Bold(true).Render("↗ freeway") + muted.Render("  /  AI workspace")
	if m.projectLabel != "" {
		brand = accent.Bold(true).Render("↗ freeway") + muted.Render("  /  "+terminalSafe(m.projectLabel))
	}
	title := m.session.Title
	if title == "" {
		title = "New conversation"
	}
	title = terminalSafe(title)
	if width >= 70 {
		caption := ansi.Truncate(title, min(36, width/3), "…")
		brand = padCells(brand, width-ansi.StringWidth(caption)) + muted.Render(caption)
	}
	model := lipgloss.NewStyle().Foreground(c.text).Render(ansi.Truncate(m.modelLabel(), max(10, width-16), "…")) + accent.Bold(true).Render("  FREE")
	if m.modelsFocused {
		model += muted.Render("  ·  " + m.menuTitle())
	} else if m.busy {
		model += muted.Render("  ·  generating")
	}
	header := lipgloss.JoinVertical(lipgloss.Left, ansi.Truncate(brand, width, "…"), ansi.Truncate(model, width, "…"), "")
	body := m.chat.View()
	if len(m.messages) == 0 && !m.modelsFocused {
		body = m.welcomeView(width, m.chat.Height())
	}
	composer := m.composerView(width)
	help := "Enter send · Ctrl+R history · Ctrl+G editor · Alt+P models · F1 help"
	if width < 76 {
		help = "↑↓ history · Ctrl+R search · F1 help"
	}
	if m.commandPending() {
		help = "↑↓ choose command · Tab complete · Enter run"
	}
	if m.busy {
		help = "Tab queue follow-up · Up take back · Esc stop · F1 help"
	}
	if m.modelsFocused {
		query := m.menuQuery
		if query == "" {
			query = "Type to search…"
		}
		title := accent.Bold(true).Render(m.menuTitle())
		columns := "MODEL"
		detail := "CONNECTION / DESCRIPTION"
		switch m.menu {
		case "files":
			columns, detail = "FILE", "PROJECT CONTEXT"
		case "prompts":
			columns, detail = "PROMPT", "FULL TEXT"
		case "queue":
			columns, detail = "FOLLOW-UP", "ACTION"
		case "history":
			columns, detail = "CONVERSATION", "UPDATED / PREVIEW"
		case "settings":
			columns, detail = "PRESET", "DESCRIPTION"
		case "help":
			columns, detail = "KEY / COMMAND", "ACTION"
		}
		nameWidth := menuNameWidth(width)
		columnLine := muted.Render(padCells("    "+columns, nameWidth+6) + "  " + detail)
		search := muted.Render("⌕ ") + lipgloss.NewStyle().Foreground(c.text).Render(ansi.Truncate(terminalSafe(query), width-2, "…"))
		composer = lipgloss.JoinVertical(lipgloss.Left, title, search, ansi.Truncate(columnLine, width, ""), m.models.View(), muted.Render(ansi.Truncate(m.menuDetail(), width, "…")))
		help = "↑↓ move · Enter select · type to search · 1–9 quick select · Esc back"
		if m.menu == "history" {
			scope := "this project"
			if m.allSessions {
				scope = "all projects"
			}
			help = "Enter resume · Ctrl+A " + scope + " · type to search · Esc back"
		}
		footer := padCells(muted.Render(ansi.Truncate(m.menuDetail(), max(1, width-12), "…")), max(1, width-12)) + muted.Render(padCells(menuPosition(m), 12))
		items := m.models.View()
		if len(m.models.list.Items()) == 0 {
			items = muted.Width(width).Height(m.models.list.Height()).Render("No matches. Try another search.")
		}
		screen := lipgloss.NewStyle().Width(m.width).Padding(0, 2).Render(lipgloss.JoinVertical(lipgloss.Left, header, title, search, ansi.Truncate(columnLine, width, ""), items, footer, muted.Render(ansi.Truncate(help, width, ""))))
		view := tea.NewView(screen)
		view.AltScreen = true
		view.MouseMode = tea.MouseModeCellMotion
		view.Cursor = tea.NewCursor(4+min(ansi.StringWidth(m.menuQuery), max(0, width-4)), 4)
		return view
	}
	status := m.statusLabel()
	if m.gateway == nil {
		status = "Preview · Not sent"
	}
	if m.toast != "" && time.Now().Before(m.toastUntil) {
		status = m.toast
	}
	if !m.modelsFocused {
		composer = lipgloss.JoinVertical(lipgloss.Left, composer, muted.Render(ansi.Truncate(help, width, "")), muted.Render(ansi.Truncate(terminalSafe(status), width, "…")))
	} else {
		composer = lipgloss.JoinVertical(lipgloss.Left, composer, muted.Render(ansi.Truncate(help, width, "")))
	}
	screen := lipgloss.NewStyle().Width(m.width).Padding(0, 2).Render(lipgloss.JoinVertical(lipgloss.Left, header, body, composer))
	view := tea.NewView(screen)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	if !m.modelsFocused {
		if cursor := m.prompt.Cursor(); cursor != nil {
			cursor.X += 4
			cursor.Y += 3 + m.chat.Height() + 1
			view.Cursor = cursor
		}
	}
	return view
}

func (m Model) welcomeView(width, height int) string {
	c := grooveColors()
	accent := lipgloss.NewStyle().Foreground(c.accent)
	muted := lipgloss.NewStyle().Foreground(c.muted)
	lines := []string{accent.Bold(true).Render("↗"), lipgloss.NewStyle().Foreground(c.text).Bold(true).Render("What would you like to make?"), "", muted.Render("Think, build, and find answers with free AI."), "", accent.Render("/models") + muted.Render("  choose a model     ") + accent.Render("/history") + muted.Render("  pick up where you left off")}
	if width < 65 {
		lines = []string{accent.Bold(true).Render("↗ freeway"), lipgloss.NewStyle().Foreground(c.text).Bold(true).Render("What would you like to make?"), "", muted.Render("/models choose · /history resume")}
	}
	for i, line := range lines {
		line = ansi.Truncate(line, width, "…")
		lines[i] = strings.Repeat(" ", max(0, (width-ansi.StringWidth(line))/2)) + line
	}
	top := max(0, (height-len(lines))/2)
	text := strings.Repeat("\n", top) + strings.Join(lines, "\n")
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(text)
}

func (m Model) composerView(width int) string {
	c := grooveColors()
	border := lipgloss.NewStyle().Foreground(c.border)
	muted := lipgloss.NewStyle().Foreground(c.muted)
	label := " Message "
	if m.busy {
		label = " Next message · Esc to stop response "
	}
	if m.commandPending() {
		label = " Command "
	}
	top := border.Render("╭─") + muted.Render(label) + border.Render(strings.Repeat("─", max(0, width-3-ansi.StringWidth(label)))+"╮")
	lines := []string{top}
	inner := width - 4
	for _, line := range strings.Split(m.prompt.View(), "\n") {
		lines = append(lines, border.Render("│ ")+padCells(line, inner)+border.Render(" │"))
	}
	lines = append(lines, border.Render("╰"+strings.Repeat("─", width-2)+"╯"))
	for _, row := range m.composerExtraRows(width) {
		style := muted
		if strings.HasPrefix(row, "› ") {
			style = lipgloss.NewStyle().Foreground(c.accent).Background(c.selected)
		}
		lines = append(lines, style.Render(ansi.Truncate(row, width, "…")))
	}
	return strings.Join(lines, "\n")
}

func menuNameWidth(width int) int { return min(38, max(14, (width-8)*44/100)) }

func (m *Model) applyMenuFilter() tea.Cmd {
	query := strings.ToLower(m.menuQuery)
	items := m.menuAll
	if query != "" {
		items = nil
		for _, item := range m.menuAll {
			if strings.Contains(strings.ToLower(item.FilterValue()), query) {
				items = append(items, item)
			}
		}
	}
	m.models.list.ResetFilter()
	return m.models.list.SetItems(items)
}

func (m Model) menuKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	name := msg.String()
	if name == "backspace" {
		runes := []rune(m.menuQuery)
		if len(runes) > 0 {
			m.menuQuery = string(runes[:len(runes)-1])
		}
		cmd := m.applyMenuFilter()
		return m, cmd, true
	}
	if msg.Mod&(tea.ModCtrl|tea.ModAlt) != 0 {
		return m, nil, false
	}
	text := msg.Text
	if text == "" {
		return m, nil, false
	}
	if text == "/" && m.menuQuery == "" {
		m.menuSearching = true
		return m, nil, true
	}
	if m.menuQuery == "" && !m.menuSearching && len(text) == 1 && text[0] >= '1' && text[0] <= '9' {
		i := int(text[0] - '1')
		items := m.models.list.Items()
		if i < len(items) {
			if item, ok := items[i].(modelItem); ok {
				next, cmd := m.chooseItem(item)
				return next, cmd, true
			}
		}
		return m, nil, true
	}
	m.menuQuery += text
	m.menuSearching = true
	cmd := m.applyMenuFilter()
	return m, cmd, true
}

func menuPosition(m Model) string {
	if len(m.models.list.Items()) == 0 {
		return "0 / 0"
	}
	return fmt.Sprintf("%d / %d", m.models.list.Index()+1, len(m.models.list.Items()))
}
