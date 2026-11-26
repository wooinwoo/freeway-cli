package tui

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const latestButtonLabel = " ↓ Jump to latest "

func (m Model) latestLabel() string { return m.tr(latestButtonLabel, " ↓ 최신 메시지로 ") }

func (m Model) showLatestButton() bool {
	return m.usable() && !m.modelsFocused && m.find == nil && len(m.messages) > 0 && !m.chat.AtBottom()
}

func (m Model) latestButtonPosition() (x, y int) {
	// Leave one border cell and the right corner at the end of the composer.
	return m.width - 4 - ansi.StringWidth(m.latestLabel()), 3 + m.chat.Height()
}

func padCells(text string, width int) string {
	text = ansi.Truncate(text, max(0, width), "…")
	return text + strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
}

func (m Model) workspaceView() tea.View {
	if !m.usable() {
		view := tea.NewView(lipgloss.NewStyle().Width(max(1, m.width)).MaxHeight(max(1, m.height)).Render(m.tr("Resize to at least 44×12. Ctrl+C to quit.", "44×12 이상으로 늘리세요. Ctrl+C로 종료합니다.")))
		view.AltScreen = true
		return view
	}
	c := grooveColors()
	width := m.width - 4
	accent := lipgloss.NewStyle().Foreground(c.accent)
	muted := lipgloss.NewStyle().Foreground(c.muted)
	brand := accent.Bold(true).Render("↗ freeway") + muted.Render(m.tr("  /  AI workspace", "  /  AI 작업 공간"))
	if m.projectLabel != "" {
		brand = accent.Bold(true).Render("↗ freeway") + muted.Render("  /  "+terminalSafe(m.projectLabel))
	}
	title := m.session.Title
	if title == "" {
		title = m.tr("New conversation", "새 대화")
	}
	title = terminalSafe(title)
	if width >= 70 {
		caption := ansi.Truncate(title, min(36, width/3), "…")
		brand = padCells(brand, width-ansi.StringWidth(caption)) + muted.Render(caption)
	}
	modelID := m.selected
	if m.busy && !m.modelsFocused && m.responseTarget.model != "" {
		modelID = m.responseTarget.model
	}
	model := lipgloss.NewStyle().Foreground(c.text).Render(ansi.Truncate(m.modelLabel(modelID), max(10, width-16), "…")) + accent.Bold(true).Render(m.tr("  FREE", "  무료"))
	// The status line already shows that a reply is running; the header only adds a model change.
	if m.busy && !m.modelsFocused && modelID != m.selected {
		next := m.tr(" · Next ", " · 다음 ")
		currentWidth := (width - ansi.StringWidth(next)) / 2
		text := lipgloss.NewStyle().Foreground(c.text)
		model = text.Render(ansi.Truncate(m.modelLabel(modelID), currentWidth, "…")) + muted.Render(next) +
			text.Render(ansi.Truncate(m.modelLabel(m.selected), width-currentWidth-ansi.StringWidth(next), "…"))
	}
	// The model sits on the left and the connection on the right, so a rule can close the header.
	modelRow := ansi.Truncate(model, width, "…")
	if summary := terminalSafe(m.providerSummary()); summary != "" {
		dot := accent.Render("● ")
		if m.providersLoading || m.loadingModels {
			dot = muted.Render("● ")
		} else if !m.ready {
			dot = lipgloss.NewStyle().Foreground(c.warn).Render("● ")
		}
		if room := width - ansi.StringWidth(modelRow) - 4; room >= 10 {
			connection := dot + muted.Render(ansi.Truncate(summary, room, "…"))
			modelRow = padCells(modelRow, width-ansi.StringWidth(connection)) + connection
		}
	}
	rule := lipgloss.NewStyle().Foreground(c.border).Render(strings.Repeat("─", max(0, width)))
	header := lipgloss.JoinVertical(lipgloss.Left, ansi.Truncate(brand, width, "…"), modelRow, rule)
	body := m.chat.View()
	if len(m.messages) == 0 && (!m.modelsFocused || m.inlineFilePicker() || m.themePicker()) {
		body = m.welcomeView(width, m.chat.Height())
	}
	composer := m.composerView(width)
	if m.themePicker() {
		rows := []string{header, body}
		if m.height-3-m.chat.Height()-m.models.list.Height()-2 >= lipgloss.Height(composer) {
			rows = append(rows, composer)
		}
		hint := m.tr("↑↓ preview · Enter save · Esc cancel", "↑↓ 미리보기 · Enter 저장 · Esc 취소")
		items := lipgloss.NewStyle().Height(m.models.list.Height()).MaxHeight(m.models.list.Height()).Render(m.menuItemsView())
		rows = append(rows, accent.Bold(true).Render(m.menuTitle()), items, keyHints(hint, width))
		screen := lipgloss.NewStyle().Width(m.width).MaxHeight(m.height).Padding(0, 2).Render(lipgloss.JoinVertical(lipgloss.Left, rows...))
		view := tea.NewView(screen)
		view.AltScreen, view.MouseMode = true, tea.MouseModeCellMotion
		return view
	}
	help := m.tr("Enter send · Ctrl+R prompts · F3 find · F4 providers · F8 context · ? help", "Enter 보내기 · Ctrl+R 기록 · F3 찾기 · F4 공급처 · F8 문맥 · ? 도움말")
	if width < 76 {
		help = m.tr("↑↓ history · F4 providers · F8 context · ? help", "↑↓ 기록 · F4 공급처 · F8 문맥 · ? 도움말")
	}
	if m.commandPending() {
		help = m.tr("↑↓ choose command · Tab complete · Enter run · Esc close", "↑↓ 명령 선택 · Tab 완성 · Enter 실행 · Esc 닫기")
	}
	if m.busy {
		// The status line and the composer title already say Esc stops the reply.
		help = m.tr("Tab queue follow-up · ? help", "Tab 후속 질문 예약 · ? 도움말")
		if len(m.queue) > 0 {
			help = m.tr("Tab queue follow-up · Up take back · ? help", "Tab 후속 질문 예약 · ↑ 되돌리기 · ? 도움말")
		}
	}
	if m.inlineFilePicker() {
		query := m.menuQuery
		if query == "" {
			query = m.tr("Type a project path…", "프로젝트 경로를 입력하세요…")
		}
		label := m.tr("File @", "파일 @")
		labelWidth := ansi.StringWidth(label)
		search := accent.Bold(true).Render(label) + ansi.Truncate(terminalSafe(query), width-labelWidth, "…")
		items := lipgloss.NewStyle().Height(m.models.list.Height()).MaxHeight(m.models.list.Height()).Render(m.menuItemsView())
		context := keyHints(m.tr("↑↓ choose · Enter/Tab attach · Esc keep typing", "↑↓ 선택 · Enter/Tab 첨부 · Esc 돌아가기"), width)
		screen := lipgloss.NewStyle().Width(m.width).Padding(0, 2).Render(lipgloss.JoinVertical(lipgloss.Left, header, body, composer, search, items, context))
		view := tea.NewView(screen)
		view.AltScreen, view.MouseMode = true, tea.MouseModeCellMotion
		view.Cursor = tea.NewCursor(2+min(width-1, labelWidth+ansi.StringWidth(m.menuQuery)), 3+m.chat.Height()+lipgloss.Height(composer))
		return view
	}
	if m.modelsFocused {
		query := m.menuQuery
		if query == "" {
			query = m.tr("Type to search…", "입력해서 검색…")
		}
		title := accent.Bold(true).Render(m.menuTitle())
		columns := m.tr("MODEL", "모델")
		detail := m.tr("CONNECTION / DESCRIPTION", "공급처 / 설명")
		switch m.menu {
		case "providers":
			columns, detail = m.tr("PROVIDER", "공급처"), m.tr("KEY / AVAILABILITY", "인증 / 사용 가능 여부")
		case "files":
			columns, detail = m.tr("FILE", "파일"), m.tr("PROJECT CONTEXT", "프로젝트 경로")
		case "prompts":
			columns, detail = m.tr("PROMPT", "질문"), m.tr("FULL TEXT", "전체 내용")
		case "queue":
			columns, detail = m.tr("FOLLOW-UP", "후속 질문"), m.tr("ACTION", "동작")
		case "queue-clear":
			columns, detail = m.tr("CHOICE", "선택"), m.tr("DESCRIPTION", "설명")
		case "history":
			columns, detail = m.tr("CONVERSATION", "대화"), m.tr("UPDATED / PREVIEW", "수정 시간 / 미리보기")
		case "settings":
			columns, detail = m.tr("PRESET", "설정"), m.tr("DESCRIPTION", "설명")
		case "language":
			columns, detail = m.tr("LANGUAGE", "언어"), m.tr("DESCRIPTION", "설명")
		case "theme":
			columns, detail = m.tr("THEME", "테마"), m.tr("DESCRIPTION", "설명")
		case "context":
			columns, detail = m.tr("ITEM", "항목"), m.tr("INCLUSION", "포함 여부")
		case "help":
			columns, detail = m.tr("KEY / COMMAND", "키 / 명령"), m.tr("ACTION", "동작")
		}
		nameWidth := menuNameWidth(width)
		columnLine := muted.Render(padCells("    "+columns, nameWidth+6) + "  " + detail)
		search := muted.Render("⌕ ") + lipgloss.NewStyle().Foreground(c.text).Render(ansi.Truncate(terminalSafe(query), width-2, "…"))
		help = m.tr("↑↓ move · Enter select · type to search · Esc back", "↑↓ 이동 · Enter 선택 · 입력해서 검색 · Esc 뒤로")
		if m.menuQuickSelect() {
			help = m.tr("↑↓ move · Enter select · 1–9 quick select · Esc back", "↑↓ 이동 · Enter 선택 · 1–9 빠른 선택 · Esc 뒤로")
		}
		if m.menu == "models" {
			help = m.tr("Ctrl+T ranking · Enter select · Esc back", "Ctrl+T 순위 · Enter 선택 · Esc 뒤로")
			if m.gateway != nil {
				help = m.tr("Enter select · Ctrl+R refresh · Ctrl+T ranking · F4 providers · Esc back", "Enter 선택 · Ctrl+R 갱신 · Ctrl+T 순위 · F4 공급처 · Esc 뒤로")
				if width < 62 {
					help = m.tr("Ctrl+R refresh · Ctrl+T rank · F4", "Ctrl+R 갱신 · Ctrl+T 순위 · F4")
				}
			}
		}
		if m.menu == "providers" {
			help = m.tr("F2 models · Ctrl+R refresh · Esc back", "F2 모델 · Ctrl+R 새로고침 · Esc 뒤로")
			if m.providerSetup != "" {
				help = m.tr("Enter manage · F2 models · Ctrl+R refresh · Esc back", "Enter 관리 · F2 모델 · Ctrl+R 새로고침 · Esc 뒤로")
				if m.busy {
					help = m.tr("Esc back, then Esc to stop the reply", "Esc로 채팅 복귀 · 다시 Esc로 응답 중지")
				}
			}
		}
		if m.menu == "history" {
			scope := m.tr("all projects", "모든 프로젝트 보기")
			if m.allSessions {
				scope = m.tr("this project only", "현재 프로젝트만")
			}
			help = m.tr("Enter resume · Ctrl+A ", "Enter 이어서 · Ctrl+A ") + scope + m.tr(" · type to search · Esc back", " · 입력해서 검색 · Esc 뒤로")
			if m.historyPage.paged {
				help = m.tr("Enter open · Ctrl+A ", "Enter 열기 · Ctrl+A ") + scope + m.tr(" · Ctrl+R refresh · Esc back", " · Ctrl+R 새로고침 · Esc 뒤로")
				if m.historyPage.cursor != "" {
					help = m.tr("Enter open · Ctrl+A ", "Enter 열기 · Ctrl+A ") + scope + m.tr(" · End more · Esc back", " · End 더 보기 · Esc 뒤로")
				}
			}
		}
		if m.menu == "prompts" {
			scope := m.tr("all projects", "모든 프로젝트 보기")
			if m.allPrompts {
				scope = m.tr("this project only", "현재 프로젝트만")
			}
			help = m.tr("Enter restore · Esc back", "Enter 복원 · Esc 뒤로")
			if _, ok := m.gateway.(scopedPromptGateway); ok {
				help = m.tr("Enter restore · Ctrl+A ", "Enter 복원 · Ctrl+A ") + scope + m.tr(" · Esc back", " · Esc 뒤로")
			}
			if m.promptsIncomplete && !m.promptsLoading {
				help = m.tr("Some saved prompts unavailable · Ctrl+R retry · Esc back", "일부 저장된 질문을 읽지 못함 · Ctrl+R 다시 시도 · Esc 뒤로")
			}
		}
		if m.menu == "context" {
			help = m.tr("↑↓ inspect · Enter change setting · Esc back", "↑↓ 확인 · Enter 설정 변경 · Esc 뒤로")
		}
		// Keep two cells between the detail and the position counter.
		footer := padCells(muted.Render(ansi.Truncate(oneLine(m.menuDetail()), max(1, width-14), "…")), max(1, width-12)) + muted.Render(padCells(menuPosition(m), 12))
		rows := []string{header, title, search, ansi.Truncate(columnLine, width, ""), m.menuItemsView()}
		if m.menu == "models" {
			var evidence string
			if item, ok := m.models.list.SelectedItem().(modelItem); ok && item.ID != "free/auto" {
				evidence = m.rankingEvidence(item)
			}
			rows = append(rows, muted.Render(padCells(evidence, width)))
		}
		if height := m.menuPreviewHeight(); height > 0 {
			label := m.tr("Prompt preview", "질문 미리보기")
			if m.menu == "history" {
				label = m.tr("Last saved question", "마지막으로 저장한 질문")
			}
			preview := []string{accent.Render(label)}
			if item, ok := m.models.list.SelectedItem().(modelItem); ok {
				text := item.Detail
				if m.menu == "history" {
					text = m.historyPreview(item.ID)
				}
				preview = append(preview, strings.Split(ansi.Wrap(terminalSafe(text), max(1, width-2), ""), "\n")...)
			}
			rows = append(rows, muted.Height(height).MaxHeight(height).Render(strings.Join(preview[:min(height, len(preview))], "\n")))
		}
		rows = append(rows, footer, keyHints(help, width))
		screen := lipgloss.NewStyle().Width(m.width).Padding(0, 2).Render(lipgloss.JoinVertical(lipgloss.Left, rows...))
		view := tea.NewView(screen)
		view.AltScreen = true
		view.MouseMode = tea.MouseModeCellMotion
		view.Cursor = tea.NewCursor(4+min(ansi.StringWidth(m.menuQuery), max(0, width-4)), 4)
		return view
	}
	status := m.statusLabel()
	if m.gateway == nil {
		status = m.tr("Preview · Not sent", "미리보기 · 전송 안 함")
	}
	if m.toast != "" && time.Now().Before(m.toastUntil) {
		status = m.toast
	}
	if m.find != nil {
		cursor := m.find.input.Cursor()
		if cursor != nil {
			cursor.X += 2
			cursor.Y += 3 + m.chat.Height() + lipgloss.Height(composer)
		}
		composer = lipgloss.JoinVertical(lipgloss.Left, composer, m.find.input.View(), muted.Render(ansi.Truncate(m.transcriptHint(), width, "")))
		screen := lipgloss.NewStyle().Width(m.width).Padding(0, 2).Render(lipgloss.JoinVertical(lipgloss.Left, header, body, composer))
		view := tea.NewView(screen)
		view.AltScreen, view.MouseMode, view.Cursor = true, tea.MouseModeCellMotion, cursor
		return view
	}
	if !m.modelsFocused {
		// Activity uses the accent and problems the warning color; a fresh notice reads at full strength.
		tone := muted
		switch {
		case m.toast != "" && time.Now().Before(m.toastUntil):
			tone = lipgloss.NewStyle().Foreground(c.text)
		case m.busy:
			tone = accent
		case m.saveError != "" || m.status == "Connection needs attention" || m.status == "Ready to retry" ||
			m.ready && !m.loadingModels && m.textRouteIssue() != "":
			tone = lipgloss.NewStyle().Foreground(c.warn)
		}
		composer = lipgloss.JoinVertical(lipgloss.Left, composer, keyHints(help, width), tone.Render(ansi.Truncate(terminalSafe(status), width, "…")))
	} else {
		composer = lipgloss.JoinVertical(lipgloss.Left, composer, keyHints(help, width))
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
	lines := []string{lipgloss.NewStyle().Foreground(c.text).Bold(true).Render(m.tr("What would you like to make?", "무엇을 만들어 볼까요?")), "", muted.Render(m.tr("Think, build, and find answers with free AI.", "무료 AI로 생각하고 만들고 답을 찾아보세요.")), "", accent.Render("/models") + muted.Render(m.tr("  choose a model     ", "  모델 선택     ")) + accent.Render("/history") + muted.Render(m.tr("  pick up where you left off", "  이전 대화 이어서"))}
	if width < 65 {
		lines = []string{lipgloss.NewStyle().Foreground(c.text).Bold(true).Render(m.tr("What would you like to make?", "무엇을 만들어 볼까요?")), "", muted.Render(m.tr("/models choose · /history resume", "/models 모델 선택 · /history 대화 기록"))}
	}
	// Startup problems, such as no reachable provider, would otherwise hide behind the welcome.
	notice := m.notice
	if notice != "" && m.status == "Connection needs attention" && m.providersUnreachable() {
		// Say the same as the header: the cause is the network, not the model choice.
		notice = m.tr("Can't reach the providers. Check your internet connection, then refresh in F4.", "공급처 서버에 연결할 수 없습니다. 인터넷 연결을 확인한 뒤 F4에서 새로고침하세요.")
	}
	if notice != "" {
		lines = append(lines, "")
		for _, line := range strings.Split(ansi.Wrap(terminalSafe(notice), min(width, 72), ""), "\n") {
			lines = append(lines, lipgloss.NewStyle().Foreground(m.noticeColor()).Render(line))
		}
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
	title := lipgloss.NewStyle().Foreground(c.accent).Bold(true)
	label := m.tr(" Message ", " 메시지 ")
	if m.busy {
		label = m.tr(" Next message · Esc to stop response ", " 다음 메시지 · Esc로 응답 중지 ")
	}
	if m.commandPending() {
		label = m.tr(" Command ", " 명령 ")
	}
	// Past the visible rows, the count tells how much of a long paste is out of view.
	if lines := m.prompt.LineCount(); lines > m.prompt.Height() {
		label = strings.TrimSuffix(label, " ") + fmt.Sprintf(m.tr(" · %d lines ", " · %d줄 "), lines)
	}
	button, buttonWidth := "", 0
	rightBorder := "╮"
	if m.showLatestButton() {
		button = lipgloss.NewStyle().Foreground(c.active).Background(c.selected).Bold(true).Render(m.latestLabel())
		buttonWidth = ansi.StringWidth(m.latestLabel())
		rightBorder = "─╮"
		label = ansi.Truncate(label, width-5-buttonWidth, "…")
	}
	fillWidth := width - 2 - ansi.StringWidth(label) - buttonWidth - ansi.StringWidth(rightBorder)
	top := border.Render("╭─") + title.Render(label) + border.Render(strings.Repeat("─", max(0, fillWidth))) + button + border.Render(rightBorder)
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

func oneLine(text string) string { return strings.Join(strings.Fields(terminalSafe(text)), " ") }

func (m Model) menuPreviewHeight() int {
	if (m.menu == "prompts" || m.menu == "history") && m.height >= 18 {
		return 4
	}
	return 0
}

func (m Model) historyPreview(id string) string {
	for _, chat := range m.history {
		if chat.ID == id {
			return chat.Preview
		}
	}
	return ""
}

func (m Model) menuItemsView() string {
	if len(m.models.list.Items()) > 0 {
		return m.models.View()
	}
	message := m.tr("No matches. Try another search.", "검색 결과가 없습니다. 다른 검색어를 입력하세요.")
	if m.menu == "models" && m.gateway != nil {
		switch {
		case m.loadingModels:
			message = m.tr("Loading models…", "모델 불러오는 중…")
		case len(m.availableModels) == 0:
			message = m.tr("No models available · F4 providers", "사용 가능한 모델 없음 · F4 공급처")
		}
	}
	if m.menu == "history" {
		switch {
		case m.historyPage.loading:
			message = m.tr("Searching saved conversations…", "저장된 대화 검색 중…")
		case m.historyPage.err != "":
			message = m.historyPage.err
		case m.menuQuery == "":
			message = m.tr("No saved conversations in this scope.", "이 범위에 저장된 대화가 없습니다.")
		}
	}
	if m.menu == "prompts" {
		switch {
		case m.promptsLoading:
			message = m.tr("Loading saved prompts…", "저장된 질문 불러오는 중…")
		case m.promptsIncomplete:
			message = m.tr("Saved prompts could not be read. Ctrl+R to retry.", "저장된 질문을 읽지 못했습니다. Ctrl+R로 다시 시도하세요.")
		case m.menuQuery == "":
			message = m.tr("No saved prompts in this scope.", "이 범위에 저장된 질문이 없습니다.")
		}
	}
	if m.menu == "files" && m.filesLoading {
		message = m.tr("Scanning project files…", "프로젝트 파일 검색 중…")
	}
	if m.menu == "providers" && m.menuQuery == "" {
		message = m.menuDetail()
		if message == "" {
			message = m.tr("No providers enabled in this gateway.", "이 게이트웨이에 설정된 공급처가 없습니다.")
		}
	}
	style := lipgloss.NewStyle().Foreground(grooveColors().muted)
	return style.Width(m.models.list.Width()).Height(m.models.list.Height()).Render(message)
}

func (m Model) menuQuickSelect() bool {
	return m.menu == "models" || m.menu == "help" || m.menu == "settings"
}

func (m *Model) applyMenuFilter() tea.Cmd {
	if m.menu == "history" && m.historyPage.paged {
		return m.filterPagedHistory()
	}
	query := strings.ToLower(m.menuQuery)
	items := m.menuAll
	if query != "" && m.menu == "files" {
		items = nil
		query = strings.ReplaceAll(query, "\\", "/")
		rank := make(map[string]int)
		for _, item := range m.menuAll {
			file := item.(modelItem)
			name := strings.ToLower(file.ID)
			base := path.Base(name)
			switch {
			case base == query || name == query:
				rank[file.ID] = 0
			case strings.HasPrefix(base, query):
				rank[file.ID] = 1
			case strings.HasPrefix(name, query):
				rank[file.ID] = 2
			case strings.Contains(base, query):
				rank[file.ID] = 3
			case strings.Contains(name, query):
				rank[file.ID] = 4
			default:
				continue
			}
			items = append(items, item)
		}
		sort.SliceStable(items, func(i, j int) bool {
			return rank[items[i].(modelItem).ID] < rank[items[j].(modelItem).ID]
		})
	} else if query != "" {
		items = nil
		for _, item := range m.menuAll {
			if strings.Contains(strings.ToLower(item.FilterValue()), query) {
				items = append(items, item)
			}
		}
	}
	m.models.list.ResetFilter()
	cmd := m.models.list.SetItems(items)
	m.models.list.Select(0)
	return cmd
}

func (m *Model) selectMenuItem(id string) {
	for i, item := range m.models.list.Items() {
		if row, ok := item.(modelItem); ok && row.ID == id {
			m.models.list.Select(i)
			return
		}
	}
}

func (m *Model) refreshMenuItems(items []list.Item) tea.Cmd {
	selected, ok := m.models.list.SelectedItem().(modelItem)
	m.menuAll = items
	cmd := m.applyMenuFilter()
	if ok {
		m.selectMenuItem(selected.ID)
	} else {
		m.selectMenuItem(m.selected)
	}
	return cmd
}

func (m Model) appendMenuQuery(text string) (tea.Model, tea.Cmd) {
	text = terminalSafe(text)
	text = strings.ReplaceAll(text, "\n", " ")
	limit := 512
	if m.menu == "history" && m.historyPage.paged {
		limit = 256
	}
	room := max(0, limit-len([]rune(m.menuQuery)))
	runes := []rune(text)
	m.menuQuery += string(runes[:min(room, len(runes))])
	m.menuSearching = true
	cmd := m.applyMenuFilter()
	return m, cmd
}

func (m Model) menuKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if m.themePicker() {
		return m, nil, false
	}
	name := msg.String()
	if name == "ctrl+u" {
		m.menuQuery = ""
		cmd := m.applyMenuFilter()
		return m, cmd, true
	}
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
	if text == "/" && m.menu != "files" && m.menuQuery == "" {
		m.menuSearching = true
		return m, nil, true
	}
	if m.menuQuickSelect() && m.menuQuery == "" && !m.menuSearching && len(text) == 1 && text[0] >= '1' && text[0] <= '9' {
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
	next, cmd := m.appendMenuQuery(text)
	return next, cmd, true
}

func menuPosition(m Model) string {
	if len(m.models.list.Items()) == 0 {
		return "0 / 0"
	}
	return fmt.Sprintf("%d / %d", m.models.list.Index()+1, len(m.models.list.Items()))
}
