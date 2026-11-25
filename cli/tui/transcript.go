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
	row, lastRow, start, end int
	wrap                     *transcriptWrap
}

type transcriptWrap struct {
	row, prefix int
	lengths     []int
	gaps        []int // Spaces that word wrapping removed after each row.
}

func (w transcriptWrap) span(i int) int {
	if i < len(w.gaps) {
		return w.lengths[i] + w.gaps[i]
	}
	return w.lengths[i]
}

// Record only the line breaks introduced by wrapping. Search can join these
// segments without including panel margins or joining real paragraph breaks.
func recordTranscriptWrap(wraps *[]transcriptWrap, row, prefix int, parts []string) {
	if len(parts) < 2 {
		return
	}
	lengths := make([]int, len(parts))
	for i, part := range parts {
		lengths[i] = len(ansi.Strip(part))
	}
	*wraps = append(*wraps, transcriptWrap{row: row, prefix: prefix, lengths: lengths})
}

// recordWordWrap also remembers the spaces that ansi.Wrap drops at each break,
// so a search for words split across rows still matches.
func recordWordWrap(wraps *[]transcriptWrap, row, prefix int, source string, parts []string) {
	if len(parts) < 2 {
		return
	}
	plain := ansi.Strip(source)
	wrap := transcriptWrap{row: row, prefix: prefix, lengths: make([]int, len(parts)), gaps: make([]int, len(parts))}
	for i, part := range parts {
		segment := ansi.Strip(part)
		rest, ok := strings.CutPrefix(plain, segment)
		if !ok {
			return
		}
		wrap.lengths[i] = len(segment)
		trimmed := strings.TrimLeft(rest, " ")
		wrap.gaps[i], plain = len(rest)-len(trimmed), trimmed
	}
	*wraps = append(*wraps, wrap)
}

type transcriptSearch struct {
	input           textinput.Model
	lines           []string
	matches         []transcriptMatch
	index, startRow int
}

func (m Model) findTranscript(query string) (tea.Model, tea.Cmd) {
	if m.loadingSession || m.loadingHistory {
		m.notify(m.tr("Wait for the conversation to load before searching.", "대화를 다 불러온 뒤 검색하세요."))
		return m, nil
	}
	if len(m.messages) == 0 {
		m.notify(m.tr("Send a message to search this conversation.", "메시지를 보낸 뒤 대화에서 검색할 수 있습니다."))
		return m, nil
	}
	input := textinput.New()
	input.Prompt, input.Placeholder, input.CharLimit = m.tr("Find: ", "찾기 "), m.tr("Search this conversation…", "이 대화에서 검색…"), 256
	input.SetVirtualCursor(false)
	input.SetWidth(max(1, m.width-10))
	input.SetValue(query)
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
		search.matches = m.transcriptMatches(search.lines, pattern)
	}
	if queryChanged {
		search.index = slices.IndexFunc(search.matches, func(item transcriptMatch) bool { return item.lastRow >= search.startRow })
		if search.index < 0 {
			search.index = len(search.matches) - 1
		}
	} else {
		search.index = min(max(0, search.index), len(search.matches)-1)
	}
	m.showTranscriptMatch()
}

func (m Model) transcriptMatches(lines []string, pattern *regexp.Regexp) []transcriptMatch {
	var matches []transcriptMatch
	row := 0
	searchLines := func(before int) {
		for ; row < before; row++ {
			for _, pair := range pattern.FindAllStringIndex(ansi.Strip(lines[row]), -1) {
				matches = append(matches, transcriptMatch{row: row, lastRow: row, start: pair[0], end: pair[1]})
			}
		}
	}
	first := 1
	for _, item := range m.renderCache {
		for _, wrap := range item.wraps {
			wrap.row += first
			end := wrap.row + len(wrap.lengths)
			if wrap.row < row || end > len(lines) {
				continue
			}
			searchLines(wrap.row)
			var text strings.Builder
			for i, length := range wrap.lengths {
				line := ansi.Strip(lines[wrap.row+i])
				if wrap.prefix+length > len(line) {
					searchLines(end)
					break
				}
				text.WriteString(line[wrap.prefix : wrap.prefix+length])
				if i < len(wrap.gaps) {
					text.WriteString(strings.Repeat(" ", wrap.gaps[i]))
				}
			}
			if row == end {
				continue
			}
			start, last := wrap.row, wrap.row
			startOffset, lastOffset := 0, 0
			for _, pair := range pattern.FindAllStringIndex(text.String(), -1) {
				for pair[0]-startOffset >= wrap.span(start-wrap.row) {
					startOffset += wrap.span(start - wrap.row)
					start++
				}
				for pair[1]-lastOffset > wrap.span(last-wrap.row) {
					lastOffset += wrap.span(last - wrap.row)
					last++
				}
				// A match edge inside a removed space highlights up to the row's text.
				matches = append(matches, transcriptMatch{
					row: start, lastRow: last, wrap: &wrap,
					start: wrap.prefix + min(pair[0]-startOffset, wrap.lengths[start-wrap.row]),
					end:   wrap.prefix + min(pair[1]-lastOffset, wrap.lengths[last-wrap.row]),
				})
			}
			row = end
		}
		first += strings.Count(item.text, "\n") + 2
	}
	searchLines(len(lines))
	return matches
}

func (m *Model) showTranscriptMatch() {
	search := m.find
	lines := slices.Clone(search.lines)
	if search.index >= 0 && search.index < len(search.matches) {
		match := search.matches[search.index]
		style := lipgloss.NewStyle().Reverse(true).Bold(true)
		for row := match.row; row <= match.lastRow; row++ {
			start, end := match.start, match.end
			if row > match.row {
				start = match.wrap.prefix
			}
			if row < match.lastRow {
				end = match.wrap.prefix + match.wrap.lengths[row-match.wrap.row]
			}
			line := ansi.Strip(lines[row])
			if start >= end {
				continue
			}
			// Use cell offsets to preserve the surrounding ANSI styles and wide glyphs.
			left, right := ansi.StringWidth(line[:start]), ansi.StringWidth(line[:end])
			highlight := style.Render(line[start:end])
			styled := ansi.Cut(lines[row], 0, left) + highlight + ansi.Cut(lines[row], right, ansi.StringWidth(line))
			if ansi.Strip(styled) != line {
				styled = line[:start] + highlight + line[end:]
			}
			lines[row] = styled
		}
		m.chat.SetContentLines(lines)
		if match.lastRow-match.row < m.chat.Height() && match.lastRow >= m.chat.YOffset()+m.chat.Height() {
			m.chat.EnsureVisible(match.lastRow, 0, 0)
		}
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
		return m.tr("Type to find · Esc cancel", "입력해서 찾기 · Esc 취소")
	case len(m.find.matches) == 0:
		return m.tr("No matches · Esc cancel", "결과 없음 · Esc 취소")
	default:
		return fmt.Sprintf(m.tr("%d/%d · ↑↓ browse · Enter jump · Esc cancel", "%d/%d · ↑↓ 이동 · Enter 선택 · Esc 취소"), m.find.index+1, len(m.find.matches))
	}
}
