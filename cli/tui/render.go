package tui

import (
	"fmt"
	"image/color"
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
	wraps   []transcriptWrap
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

// inlineMarkup matches `code`, **bold**, [text](url) and *italic*.
// An italic star must follow a space or punctuation, so 2*3*4 stays as typed.
var inlineMarkup = regexp.MustCompile("`([^`\\n]+)`|\\*\\*([^*\\n]+)\\*\\*|\\[([^\\]\\n]+)\\]\\(([^)\\s]+)\\)|(^|[^\\w*])\\*([^*\\s](?:[^*\\n]*[^*\\s])?)\\*")
var listMarker = regexp.MustCompile(`^( *)([-+*]|[0-9]{1,9}[.)]) +(.+)$`)
var horizontalRule = regexp.MustCompile(`^(-{3,}|\*{3,}|_{3,})$`)
var tableDivider = regexp.MustCompile(`^\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?$`)

// inline draws inline markup. With plain set it only removes the markers, as headings need.
func inline(line string, codeStyle lipgloss.Style, plain bool) string {
	var out strings.Builder
	last := 0
	for _, match := range inlineMarkup.FindAllStringSubmatchIndex(line, -1) {
		group := func(i int) string {
			if match[2*i] < 0 {
				return ""
			}
			return line[match[2*i]:match[2*i+1]]
		}
		style := func(s lipgloss.Style, text string) string {
			if plain {
				return text
			}
			return s.Render(text)
		}
		out.WriteString(line[last:match[0]])
		last = match[1]
		switch {
		case match[2] >= 0:
			out.WriteString(style(codeStyle, group(1)))
		case match[4] >= 0:
			out.WriteString(style(lipgloss.NewStyle().Bold(true), group(2)))
		case match[6] >= 0:
			text, url := group(3), group(4)
			out.WriteString(style(lipgloss.NewStyle().Underline(true), text))
			if !plain && text != url {
				out.WriteString(lipgloss.NewStyle().Foreground(grooveColors().muted).Render(" (" + url + ")"))
			}
		default:
			out.WriteString(group(5) + style(lipgloss.NewStyle().Italic(true), group(6)))
		}
	}
	out.WriteString(line[last:])
	return out.String()
}

func markdown(text string, width int) (string, []transcriptWrap) {
	width = max(1, width)
	colors := grooveColors()
	codeStyle := lipgloss.NewStyle().Foreground(colors.code)
	heading := lipgloss.NewStyle().Foreground(colors.accent).Bold(true)
	lines := strings.Split(terminalSafe(text), "\n")
	var output []string
	var wraps []transcriptWrap
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fence := trimmed[:3]
			// A fence nested in a list item is indented; its code lines carry the same indent.
			indent := len(line) - len(strings.TrimLeft(line, " "))
			language := strings.TrimSpace(trimmed[3:])
			var code []string
			i++
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), fence) {
				codeLine := lines[i]
				for n := 0; n < indent && strings.HasPrefix(codeLine, " "); n++ {
					codeLine = codeLine[1:]
				}
				code = append(code, codeLine)
				i++
			}
			output = append(output, codeBlock(code, language, width, len(output), &wraps)...)
			continue
		}
		if horizontalRule.MatchString(trimmed) {
			output = append(output, lipgloss.NewStyle().Foreground(colors.border).Render(strings.Repeat("─", width)))
			continue
		}
		if strings.Contains(trimmed, "|") && i+1 < len(lines) && strings.Contains(lines[i+1], "|") && tableDivider.MatchString(strings.TrimSpace(lines[i+1])) {
			end := i + 2
			for end < len(lines) && strings.Contains(lines[end], "|") && strings.TrimSpace(lines[end]) != "" {
				end++
			}
			if rows := markdownTable(append([]string{line}, lines[i+2:end]...), width, codeStyle); rows != nil {
				output = append(output, rows...)
				i = end - 1
				continue
			}
		}
		if strings.HasPrefix(trimmed, "#") {
			content := strings.TrimLeft(trimmed, "#")
			// Levels differ the way rendered Markdown does: # is underlined, ## is accented, ### and below are plain bold.
			style := heading
			switch len(trimmed) - len(content) {
			case 1:
				style = heading.Underline(true)
			case 2:
			default:
				style = lipgloss.NewStyle().Foreground(colors.text).Bold(true)
			}
			if strings.HasPrefix(content, " ") {
				// The heading is already bold in one color, so inline marks only lose their markers.
				content = inline(strings.TrimSpace(content), codeStyle, true)
				// A blank row above a heading starts a new section, as in rendered Markdown.
				if len(output) > 0 && output[len(output)-1] != "" {
					output = append(output, "")
				}
				parts := strings.Split(ansi.Wrap(content, width, ""), "\n")
				recordWordWrap(&wraps, len(output), 0, content, parts)
				for _, part := range parts {
					output = append(output, style.Render(part))
				}
				continue
			}
		}
		prefix, continuation := "", ""
		if trimmed == ">" {
			trimmed = "> "
		}
		if text, ok := strings.CutPrefix(trimmed, "> "); ok && width >= 4 {
			line = text
			prefix = lipgloss.NewStyle().Foreground(colors.muted).Render("│ ")
			continuation = prefix
		} else if match := listMarker.FindStringSubmatch(line); len(match) != 0 {
			marker, item := match[2], match[3]
			if len(marker) == 1 {
				marker = "•"
				// A task item shows its box in place of the bullet.
				if box, rest, ok := strings.Cut(item, "] "); ok && (box == "[ " || box == "[x" || box == "[X") {
					marker, item = "[ ]", rest
					if box != "[ " {
						marker = "[✓]"
					}
				}
			}
			if indent := len(match[1]) + ansi.StringWidth(marker) + 1; indent <= width-2 {
				prefix, line = match[1]+marker+" ", item
				continuation = strings.Repeat(" ", indent)
			}
		}
		line = inline(line, codeStyle, false)
		parts := strings.Split(ansi.Wrap(line, width-ansi.StringWidth(prefix), ""), "\n")
		// ponytail: search joins wrapped rows at one byte offset, so a "•" bullet (wider in bytes than its indent) skips cross-row matches.
		if skip := len(ansi.Strip(prefix)); skip == len(ansi.Strip(continuation)) {
			recordWordWrap(&wraps, len(output), skip, line, parts)
		}
		for i, part := range parts {
			if i == 0 {
				output = append(output, prefix+part)
			} else {
				output = append(output, continuation+part)
			}
		}
	}
	return strings.Join(output, "\n"), wraps
}

func codeBlock(lines []string, language string, width, row int, wraps *[]transcriptWrap) []string {
	c := grooveColors()
	border := lipgloss.NewStyle().Foreground(c.border)
	code := lipgloss.NewStyle().Foreground(c.code)
	top := border.Render("╭" + strings.Repeat("─", max(0, width-2)) + "╮")
	if language != "" {
		label := " " + ansi.Truncate(language, max(1, width-8), "…") + " "
		top = border.Render("╭─") + code.Render(label) + border.Render(strings.Repeat("─", max(0, width-3-ansi.StringWidth(label)))+"╮")
	}
	rows := []string{top}
	for _, line := range lines {
		parts := strings.Split(ansi.Hardwrap(line, width-4, true), "\n")
		recordTranscriptWrap(wraps, row+len(rows), len("│ "), parts)
		for _, part := range parts {
			rows = append(rows, border.Render("│ ")+code.Render(padCells(part, width-4))+border.Render(" │"))
		}
	}
	rows = append(rows, border.Render("╰"+strings.Repeat("─", width-2)+"╯"))
	return rows
}

// markdownTable draws a pipe table in a box. Wide tables wrap inside their cells.
// It returns nil when the columns cannot fit, so the caller keeps the raw lines.
func markdownTable(lines []string, width int, codeStyle lipgloss.Style) []string {
	var table [][]string
	columns := 0
	for _, line := range lines {
		line = strings.TrimSpace(strings.ReplaceAll(line, `\|`, "\x00"))
		line = strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|")
		var cells []string
		for _, cell := range strings.Split(line, "|") {
			cells = append(cells, inline(strings.TrimSpace(strings.ReplaceAll(cell, "\x00", "|")), codeStyle, false))
		}
		table = append(table, cells)
		columns = max(columns, len(cells))
	}
	natural := make([]int, columns)
	for _, cells := range table {
		for j, cell := range cells {
			natural[j] = max(natural[j], ansi.StringWidth(cell), 1)
		}
	}
	// Borders take one cell before each column and three between and after them.
	room := width - 3*columns - 1
	if room < 4*columns {
		return nil
	}
	widths := append([]int(nil), natural...)
	if sumInts(natural) > room {
		// Columns within an even share keep their width; the wider ones split what is left.
		share, wide := room, 0
		for _, w := range natural {
			if w*columns <= room {
				share -= w
			} else {
				wide++
			}
		}
		for j, w := range natural {
			if w*columns > room {
				widths[j] = min(w, share/wide)
			}
		}
	}
	c := grooveColors()
	border := lipgloss.NewStyle().Foreground(c.border)
	line := func(left, middle, right string) string {
		parts := make([]string, columns)
		for j, w := range widths {
			parts[j] = strings.Repeat("─", w+2)
		}
		return border.Render(left + strings.Join(parts, middle) + right)
	}
	var cellRows [][][]string
	tall := false
	for _, cells := range table {
		wrapped := make([][]string, columns)
		for j := range widths {
			text := ""
			if j < len(cells) {
				text = cells[j]
			}
			wrapped[j] = strings.Split(ansi.Wrap(text, widths[j], ""), "\n")
			tall = tall || len(wrapped[j]) > 1
		}
		cellRows = append(cellRows, wrapped)
	}
	header := lipgloss.NewStyle().Bold(true)
	rows := []string{line("╭", "┬", "╮")}
	for r, wrapped := range cellRows {
		if r == 1 || r > 1 && tall {
			rows = append(rows, line("├", "┼", "┤"))
		}
		height := 0
		for _, cell := range wrapped {
			height = max(height, len(cell))
		}
		for k := range height {
			row := border.Render("│")
			for j, cell := range wrapped {
				text := ""
				if k < len(cell) {
					text = cell[k]
				}
				if r == 0 {
					text = header.Render(text)
				}
				row += " " + padCells(text, widths[j]) + border.Render(" │")
			}
			rows = append(rows, row)
		}
	}
	return append(rows, line("╰", "┴", "╯"))
}

func sumInts(values []int) int {
	total := 0
	for _, v := range values {
		total += v
	}
	return total
}

func (m Model) renderUserMessage(message client.Message, width int, colors schemeColors) (string, []transcriptWrap) {
	// A tinted band across the conversation column, with text in the same column as answers.
	// Half blocks above and below pad it by half a row.
	bodyWidth := max(1, width-4)
	var lines [][]string
	var sources []string
	if message.Content != "" || len(message.Files) == 0 {
		for _, line := range strings.Split(terminalSafe(message.Content), "\n") {
			lines = append(lines, strings.Split(ansi.Wrap(line, bodyWidth, ""), "\n"))
			sources = append(sources, line)
		}
	}
	edge := lipgloss.NewStyle().Foreground(colors.selected)
	panel := lipgloss.NewStyle().Background(colors.selected).Foreground(colors.text)
	top, bottom, lead := strings.Repeat("▄", width), strings.Repeat("▀", width), "  "
	if m.prefs.NoColor {
		// Without a tint the half blocks would draw two heavy bars, so a side rule marks the question.
		top, bottom, lead = "", "", "│ "
	}
	var rows []string
	if top != "" {
		rows = append(rows, edge.Render(top))
	}
	var wraps []transcriptWrap
	for i, parts := range lines {
		recordWordWrap(&wraps, len(rows), len(lead), sources[i], parts)
		for _, part := range parts {
			rows = append(rows, panel.Render(lead+padCells(part, bodyWidth)+"  "))
		}
	}
	for _, file := range message.Files {
		label := ansi.Truncate(m.tr("[file] ", "[파일] ")+terminalSafe(file.Path), bodyWidth, "…")
		rows = append(rows, panel.Foreground(colors.accent).Render(lead+padCells(label, bodyWidth)+"  "))
	}
	if bottom != "" {
		rows = append(rows, edge.Render(bottom))
	}
	return strings.Join(rows, "\n"), wraps
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
		var wraps []transcriptWrap
		bodyWidth := max(1, width-2)
		label := lipgloss.NewStyle().Foreground(colors.accent).Bold(true)
		if message.Role == "user" {
			rendered, wraps = m.renderUserMessage(message, width, colors)
		} else {
			rendered, wraps = markdown(message.Content, bodyWidth)
			for i := range wraps {
				wraps[i].row++
				wraps[i].prefix += 2
			}
			if message.Content == "" && m.busy {
				rendered = lipgloss.NewStyle().Foreground(colors.muted).Render(m.tr("Thinking…", "응답을 기다리는 중…"))
			}
			if message.Status == "stopped" {
				rendered += "\n" + lipgloss.NewStyle().Foreground(colors.muted).Render(m.tr("Stopped · Alt+R to regenerate", "응답 중지됨 · Alt+R로 다시 생성"))
			}
			// The marker alone says this is an answer; the route appears with the first chunk,
			// so Auto shows which free connection answered.
			identity := label.Render("✦")
			if message.Model != "" {
				route := " " + routeLabel(message.Model)
				// Keep provider detail the model ID lacks, such as which image server answered.
				if prefix, _, _ := strings.Cut(message.Model, "/"); message.Provider != "" && !strings.EqualFold(message.Provider, prefix) {
					route += " · " + oneLine(message.Provider)
				}
				identity += lipgloss.NewStyle().Foreground(colors.muted).Render(ansi.Truncate(route, width-ansi.StringWidth(identity), "…"))
			}
			if message.Content == "" && m.busy {
				// Waiting needs no body row: the label row says it.
				rendered = identity + "  " + rendered
			} else {
				rendered = identity + "\n  " + strings.ReplaceAll(rendered, "\n", "\n  ")
			}
		}
		cache[i] = renderedMessage{message: message, width: width, text: rendered, wraps: wraps}
		rows = append(rows, rendered)
	}
	m.renderCache = cache
	// A question left without an answer, as after a stop before the first word, keeps a way to retry.
	// An error notice already says how to retry, so the row would only repeat it.
	if last := len(m.messages) - 1; last >= 0 && m.messages[last].Role == "user" && !m.busy && m.notice == "" {
		rows = append(rows, lipgloss.NewStyle().Foreground(colors.muted).Render(m.tr("✦  No reply · Alt+R to regenerate", "✦  답변 없음 · Alt+R로 다시 생성")))
	}
	if m.notice != "" {
		rows = append(rows, lipgloss.NewStyle().Foreground(m.noticeColor()).Width(width).Render(terminalSafe(m.notice)))
	}
	// Blank first and last rows keep messages off the header rule and the composer border.
	// transcriptMatches starts counting rows after that first blank row.
	m.findTranscriptContent("\n"+strings.Join(rows, "\n\n")+"\n", false)
	if m.find == nil {
		if follow {
			m.chat.GotoBottom()
		} else {
			m.chat.SetYOffset(offset)
		}
	}
	m.lastRender, m.renderPending = time.Now(), false
}

func routeLabel(model string) string {
	model = oneLine(model)
	provider, name, ok := strings.Cut(model, "/")
	if !ok || provider == "preview" || provider == "free" || provider == "model" {
		return model
	}
	return providerLabel(provider) + " · " + name
}

// providerLabel turns a gateway provider ID into the name people see in F4.
func providerLabel(id string) string {
	names := map[string]string{"mcp": "MCP", "opencode": "OpenCode", "kilo": "Kilo", "openrouter": "OpenRouter", "groq": "Groq",
		"gemini_api": "Gemini", "zai": "Z.ai", "requesty": "Requesty", "nvidia": "NVIDIA", "cline": "Cline"}
	if name, ok := names[id]; ok {
		return name
	}
	return id
}

func (m Model) modelLabel(id string) string {
	if id == "free/auto" {
		return m.tr("Auto", "자동")
	}
	for _, model := range m.availableModels {
		if model.ID == id {
			return oneLine(model.Name)
		}
	}
	return id
}

func (m Model) statusLabel() string {
	status := m.status
	if m.prefs.Language == "ko" {
		message, provider, _ := strings.Cut(status, " · ")
		if localized := koreanStatus[message]; localized != "" {
			status = localized
			if provider != "" {
				status += " · " + provider
			}
		}
	}
	if !m.busy && m.responseElapsed > 0 {
		status += fmt.Sprintf(m.tr(" · %.1fs", " · %.1f초"), m.responseElapsed.Seconds())
		if m.lastResponseTokens > 0 {
			status += fmt.Sprintf(m.tr(" · %d tokens", " · %d토큰"), m.lastResponseTokens)
		}
	}
	if m.ready && !m.busy && !m.loadingModels {
		if issue := m.textRouteIssue(); issue != "" {
			status = issue
		}
	}
	if m.busy {
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		elapsed := max(0, time.Since(m.startedAt).Seconds())
		status = fmt.Sprintf(m.tr("%s %s · %.0fs · Esc to stop", "%s %s · %.0f초 · Esc 중지"), frames[int(elapsed*8)%len(frames)], status, elapsed)
	}
	if m.saveError != "" {
		status = m.tr("Save failed · /save to retry · F6 recovery · ", "저장 실패 · /save로 다시 시도 · F6 복구 · ") + status
	} else if m.saving {
		status += m.tr(" · Saving…", " · 저장 중…")
	} else if m.saveEnabled && m.session.ID != "" && !m.dirty && m.pendingSave == nil {
		status += m.tr(" · Saved locally", " · 로컬에 저장됨")
	}
	if m.nearSaveLimit {
		status += m.tr(" · Near 8 MiB save limit", " · 저장 한도 8 MiB에 가까움")
	}
	return status
}

var koreanStatus = map[string]string{
	"Connecting": "연결 중", "Connection needs attention": "연결 확인 필요", "Ready": "준비 완료",
	"Preparing response": "응답 준비 중", "Writing response": "응답 생성 중", "Response complete": "응답 완료",
	"Generating image":      "이미지 생성 중",
	"Image tool configured": "이미지 도구 설정됨",
	"Ready to retry":        "다시 시도 가능", "Stopped": "중지됨",
}

func sameRenderedMessage(a, b client.Message) bool {
	if a.ID != b.ID || a.Role != b.Role || a.Content != b.Content || a.Status != b.Status || a.Model != b.Model || a.Provider != b.Provider || a.Error != b.Error || len(a.Files) != len(b.Files) {
		return false
	}
	for i := range a.Files {
		if a.Files[i] != b.Files[i] {
			return false
		}
	}
	return true
}

// noticeColor marks failures in the warning color and other notices in the accent.
func (m Model) noticeColor() color.Color {
	c := grooveColors()
	if m.saveError != "" || m.status == "Connection needs attention" || m.status == "Ready to retry" {
		return c.warn
	}
	return c.accent
}

func (m *Model) notify(text string) { m.toast, m.toastUntil = text, time.Now().Add(4*time.Second) }
