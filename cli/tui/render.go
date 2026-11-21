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

var inlineMarkup = regexp.MustCompile("`([^`\\n]+)`|\\*\\*([^*\\n]+)\\*\\*")
var listMarker = regexp.MustCompile(`^( *)([-+*]|[0-9]{1,9}[.)]) +(.+)$`)

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
				output = append(output, heading.Render(ansi.Wrap(strings.TrimSpace(content), width, "")))
				continue
			}
		}
		prefix, continuation := "", ""
		if text, ok := strings.CutPrefix(trimmed, "> "); ok && width >= 4 {
			line = text
			prefix = lipgloss.NewStyle().Foreground(colors.muted).Render("│ ")
			continuation = prefix
		} else if match := listMarker.FindStringSubmatch(line); len(match) != 0 {
			marker := match[2]
			if len(marker) == 1 {
				marker = "•"
			}
			if indent := len(match[1]) + ansi.StringWidth(marker) + 1; indent <= width-2 {
				prefix, line = match[1]+marker+" ", match[3]
				continuation = strings.Repeat(" ", indent)
			}
		}
		line = inlineMarkup.ReplaceAllStringFunc(line, func(s string) string {
			if s[0] == '`' {
				return codeStyle.Render(s[1 : len(s)-1])
			}
			return lipgloss.NewStyle().Bold(true).Render(s[2 : len(s)-2])
		})
		wrapped := ansi.Wrap(line, width-ansi.StringWidth(prefix), "")
		output = append(output, prefix+strings.ReplaceAll(wrapped, "\n", "\n"+continuation))
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

func renderUserMessage(message client.Message, width int, colors schemeColors) string {
	inset := min(16, max(2, width/8))
	panelWidth := width - inset - 1
	gutter := strings.Repeat(" ", inset)
	edge := lipgloss.NewStyle().Foreground(colors.userAccent).Background(colors.userPanel).Render("▎")
	panel := lipgloss.NewStyle().Background(colors.userPanel).Foreground(colors.text)
	title := panel.Foreground(colors.userAccent).Bold(true)
	rows := []string{gutter + edge + title.Render(padCells(" ● YOU", panelWidth))}
	bodyWidth := panelWidth - 2
	for _, line := range strings.Split(ansi.Hardwrap(terminalSafe(message.Content), bodyWidth, true), "\n") {
		rows = append(rows, gutter+edge+panel.Render(padCells(" "+line, panelWidth)))
	}
	for _, file := range message.Files {
		label := ansi.Truncate("[file] "+terminalSafe(file.Path), bodyWidth, "…")
		rows = append(rows, gutter+edge+title.Bold(false).Render(padCells(" "+label, panelWidth)))
	}
	return strings.Join(rows, "\n")
}

func (m *Model) renderConversation() {
	if m.chat.Width() < minWidth-4 {
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
			rendered = renderUserMessage(message, width, colors)
		} else {
			rendered = markdown(message.Content, bodyWidth)
			if message.Content == "" && m.busy {
				rendered = lipgloss.NewStyle().Foreground(colors.muted).Render("Thinking…")
			}
			if message.Status == "stopped" {
				rendered += "\n" + lipgloss.NewStyle().Foreground(colors.border).Render("Stopped · Alt+R to regenerate")
			}
			identity := label.Render("✦ FREEWAY")
			if message.Model != "" {
				route := " · " + routeLabel(message.Model)
				identity += lipgloss.NewStyle().Foreground(colors.muted).Render(ansi.Truncate(route, width-ansi.StringWidth(identity), "…"))
			}
			rendered = identity + "\n\n    " + strings.ReplaceAll(rendered, "\n", "\n    ")
		}
		cache[i] = renderedMessage{message: message, width: width, text: rendered}
		rows = append(rows, rendered)
	}
	m.renderCache = cache
	if m.notice != "" {
		rows = append(rows, lipgloss.NewStyle().Foreground(colors.accent).Width(width).Render(terminalSafe(m.notice)))
	}
	m.findTranscriptContent(strings.Join(rows, "\n\n"), false)
	if m.find == nil {
		if follow {
			m.chat.GotoBottom()
		} else {
			m.chat.SetYOffset(offset)
		}
	}
	m.lastRender, m.renderPending = time.Now(), false
}

func (m Model) modelLabel() string {
	if m.selected == "free/auto" {
		return "Auto"
	}
	for _, model := range m.availableModels {
		if model.ID == m.selected {
			return oneLine(model.Name)
		}
	}
	for _, model := range m.openCodeModels {
		if model.ID == m.selected {
			return "OpenCode · " + oneLine(model.Name)
		}
	}
	return routeLabel(m.selected)
}

func routeLabel(model string) string {
	model = oneLine(model)
	provider, name, ok := strings.Cut(model, "/")
	if !ok || provider == "preview" || provider == "free" {
		return model
	}
	switch provider {
	case "opencode":
		provider = "OpenCode"
	case "kilo":
		provider = "Kilo"
	case "openrouter":
		provider = "OpenRouter"
	case "groq":
		provider = "Groq"
	case "gemini_api":
		provider = "Gemini"
	}
	return provider + " · " + name
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
	if m.saveError != "" {
		status += " · Save failed · F6 recovery"
	} else if m.saving {
		status += " · Saving…"
	} else if m.saveEnabled && m.session.ID != "" && !m.dirty && m.pendingSave == nil {
		status += " · Saved locally"
	}
	if m.nearSaveLimit {
		status += " · Near 8 MiB save limit"
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
