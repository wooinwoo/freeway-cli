package tui

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

type renderedMessage struct {
	message client.Message
	width   int
	text    string
}

func terminalSafe(s string) string {
	s = strings.ReplaceAll(ansi.Strip(s), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\t", "    ")
	return strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}
		if r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			return -1
		}
		return r
	}, s)
}

var inlineCode = regexp.MustCompile("`([^`\\n]+)`")
var boldText = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)

func markdown(text string, width int) string {
	width = max(1, width)
	colors := grooveColors()
	codeStyle := lipgloss.NewStyle().Foreground(colors.code)
	heading := lipgloss.NewStyle().Foreground(colors.accent).Bold(true)
	lines := strings.Split(terminalSafe(text), "\n")
	var output []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fence := trimmed[:3]
			language := strings.TrimSpace(trimmed[3:])
			if language == "" {
				language = "code"
			}
			var code []string
			i++
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), fence) {
				code = append(code, lines[i])
				i++
			}
			output = append(output, codeBlock(code, language, width))
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			content := strings.TrimLeft(trimmed, "#")
			if strings.HasPrefix(content, " ") {
				output = append(output, heading.Render(ansi.Hardwrap(strings.TrimSpace(content), width, true)))
				continue
			}
		}
		line = inlineCode.ReplaceAllStringFunc(line, func(s string) string { return codeStyle.Render(strings.Trim(s, "`")) })
		line = boldText.ReplaceAllStringFunc(line, func(s string) string { return lipgloss.NewStyle().Bold(true).Render(strings.Trim(s, "*")) })
		output = append(output, ansi.Hardwrap(line, width, true))
	}
	return strings.Join(output, "\n")
}

func codeBlock(lines []string, language string, width int) string {
	c := grooveColors()
	border := lipgloss.NewStyle().Foreground(c.border)
	code := lipgloss.NewStyle().Foreground(c.code)
	if width < 8 {
		return code.Render(ansi.Hardwrap(strings.Join(lines, "\n"), width, true))
	}
	label := " " + ansi.Truncate(language, max(1, width-8), "…") + " "
	rows := []string{border.Render("╭─") + code.Render(label) + border.Render(strings.Repeat("─", max(0, width-3-ansi.StringWidth(label)))+"╮")}
	for _, line := range lines {
		for _, part := range strings.Split(ansi.Hardwrap(line, width-4, true), "\n") {
			rows = append(rows, border.Render("│ ")+code.Render(padCells(part, width-4))+border.Render(" │"))
		}
	}
	rows = append(rows, border.Render("╰"+strings.Repeat("─", width-2)+"╯"))
	return strings.Join(rows, "\n")
}

func (m *Model) renderConversation() {
	if m.chat.Width() < 1 {
		return
	}
	follow, offset := m.chat.AtBottom(), m.chat.YOffset()
	width := m.chat.Width()
	colors := grooveColors()
	rows := make([]string, 0, len(m.messages)+2)
	cache := make([]renderedMessage, len(m.messages))
	for i, message := range m.messages {
		if i < len(m.renderCache) && m.renderCache[i].width == width && sameRenderedMessage(m.renderCache[i].message, message) && !(message.Content == "" && m.busy) {
			cache[i] = m.renderCache[i]
			rows = append(rows, cache[i].text)
			continue
		}
		var rendered string
		bodyWidth := max(1, width-4)
		label := lipgloss.NewStyle().Foreground(colors.accent).Bold(true)
		if message.Role == "user" {
			body := lipgloss.NewStyle().Foreground(colors.text).Render(ansi.Hardwrap(terminalSafe(message.Content), bodyWidth, true))
			rendered = label.Render("● YOU") + "\n\n    " + strings.ReplaceAll(body, "\n", "\n    ")
			for _, file := range message.Files {
				rendered += "\n    " + lipgloss.NewStyle().Foreground(colors.muted).Render(ansi.Truncate("[file] "+file.Path, width-4, "…"))
			}
		} else {
			rendered = markdown(message.Content, bodyWidth)
			if message.Content == "" && m.busy {
				rendered = lipgloss.NewStyle().Foreground(colors.muted).Render("Thinking…")
			}
			if message.Status == "stopped" {
				rendered += "\n" + lipgloss.NewStyle().Foreground(colors.border).Render("Stopped · Alt+R to regenerate")
			}
			rendered = label.Render("✦ FREEWAY") + "\n\n    " + strings.ReplaceAll(rendered, "\n", "\n    ")
		}
		cache[i] = renderedMessage{message: message, width: width, text: rendered}
		rows = append(rows, rendered)
	}
	m.renderCache = cache
	if m.notice != "" {
		rows = append(rows, lipgloss.NewStyle().Foreground(colors.accent).Width(width).Render(terminalSafe(m.notice)))
	}
	m.chat.SetContent(strings.Join(rows, "\n\n"))
	if follow {
		m.chat.GotoBottom()
	} else {
		m.chat.SetYOffset(offset)
	}
	m.lastRender, m.renderPending = time.Now(), false
}

func (m Model) modelLabel() string {
	if m.selected == "free/auto" {
		return "Auto"
	}
	for _, model := range m.availableModels {
		if model.ID == m.selected {
			return model.Name
		}
	}
	return m.selected
}

func (m Model) statusLabel() string {
	status := m.status
	if !m.busy && m.responseElapsed > 0 {
		status += fmt.Sprintf(" · %.1fs", m.responseElapsed.Seconds())
		if m.lastResponseTokens > 0 {
			status += fmt.Sprintf(" · %d tokens", m.lastResponseTokens)
		}
	}
	if m.busy {
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		elapsed := max(0, time.Since(m.startedAt).Seconds())
		status = fmt.Sprintf("%s %s · %.0fs · Esc to stop", frames[int(elapsed*8)%len(frames)], m.status, elapsed)
	}
	if !m.chat.AtBottom() && len(m.messages) > 0 {
		status += " · Reading earlier messages · Ctrl+End for latest"
	}
	if m.saveError != "" {
		status += " · Save failed — /save to retry"
	} else if m.saving {
		status += " · Saving…"
	} else if m.saveEnabled && m.session.ID != "" {
		status += " · Saved locally"
	}
	return status
}

func sameRenderedMessage(a, b client.Message) bool {
	if a.ID != b.ID || a.Role != b.Role || a.Content != b.Content || a.Status != b.Status || a.Model != b.Model || a.Error != b.Error || len(a.Files) != len(b.Files) {
		return false
	}
	for i := range a.Files {
		if a.Files[i] != b.Files[i] {
			return false
		}
	}
	return true
}

func (m *Model) notify(text string) { m.toast, m.toastUntil = text, time.Now().Add(4*time.Second) }
