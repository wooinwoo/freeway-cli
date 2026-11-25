package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

var defaultContext = client.ContextSelection{RecentTurns: -1, IncludePriorAttachments: true}

const priorFileRow = "prior-file:"

type requestContext struct {
	history            []client.Message
	start              int
	current            *client.Message
	system             string
	includeAttachments bool
	excludedFiles      map[string]bool
}

func (m Model) contextSelection() client.ContextSelection {
	if m.session.Context != nil {
		return *m.session.Context
	}
	return defaultContext
}

func planContext(history []client.Message, current *client.Message, system string, selection client.ContextSelection) requestContext {
	start := 0
	switch {
	case selection.RecentTurns == 0:
		start = len(history)
	case selection.RecentTurns > 0:
		turns := 0
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].Role == "user" {
				turns++
				if turns == selection.RecentTurns {
					start = i
					break
				}
			}
		}
	}
	plan := requestContext{history: history, start: start, current: current, system: system, includeAttachments: selection.IncludePriorAttachments}
	if len(selection.ExcludedPriorFiles) > 0 {
		plan.excludedFiles = make(map[string]bool, len(selection.ExcludedPriorFiles))
		for _, path := range selection.ExcludedPriorFiles {
			plan.excludedFiles[path] = true
		}
	}
	return plan
}

func (p requestContext) includesPriorFile(path string) bool {
	return p.includeAttachments && !p.excludedFiles[path]
}

func (p requestContext) priorFiles(files []client.Attachment) []client.Attachment {
	if !p.includeAttachments {
		return nil
	}
	if len(p.excludedFiles) == 0 || len(files) == 0 {
		return files
	}
	included := make([]client.Attachment, 0, len(files))
	for _, file := range files {
		if p.includesPriorFile(file.Path) {
			included = append(included, file)
		}
	}
	return included
}

func (p requestContext) visit(visit func(client.Message)) {
	if p.system != "" {
		visit(client.Message{Role: "system", Content: p.system})
	}
	for i := p.start; i < len(p.history); i++ {
		message := p.history[i]
		if !includesModelContext(message) {
			continue
		}
		if message.Role == "assistant" && strings.HasPrefix(message.Model, "mcp/") {
			// Generated images stay local. The model only learns that one exists.
			message = client.Message{Role: "assistant", Content: "An image was generated and saved locally."}
		}
		message.Files = p.priorFiles(message.Files)
		visit(message)
		if message.Role == "user" && !p.answered(i) {
			visit(client.Message{Role: "assistant", Content: interruptedReply})
		}
	}
	if p.current != nil {
		visit(*p.current)
	}
}

// interruptedReply stands in for a reply that was stopped or failed. Without a reply between
// two questions, the model answers the earlier question again together with the next one.
const interruptedReply = "[The reply to this message was interrupted.]"

func (p requestContext) answered(i int) bool {
	for _, next := range p.history[i+1:] {
		if includesModelContext(next) {
			return next.Role == "assistant"
		}
	}
	return false
}

func (p requestContext) messages() []client.Message {
	request := make([]client.Message, 0, len(p.history)-p.start+2)
	p.visit(func(message client.Message) {
		request = append(request, client.Message{
			Role: message.Role, Content: message.Content,
			Files: append([]client.Attachment(nil), message.Files...),
		})
	})
	return request
}

func (p requestContext) inputEstimate() int {
	total := 0
	p.visit(func(message client.Message) { total += client.InputTokenEstimate(message) })
	return total
}

func (m Model) nextRequestContext() requestContext {
	var current *client.Message
	if text := strings.TrimSpace(m.prompt.Value()); text != "" || len(m.files) > 0 {
		current = &client.Message{Role: "user", Content: text, Files: m.files}
	}
	return planContext(m.messages, current, m.session.SystemPrompt, m.contextSelection())
}

func (m Model) responseContext() requestContext {
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Role == "user" {
			return planContext(m.messages[:i], &m.messages[i], m.session.SystemPrompt, m.responseTarget.context)
		}
	}
	return planContext(m.messages, nil, m.session.SystemPrompt, m.responseTarget.context)
}

func (m Model) contextHint() string {
	if m.busy || m.commandPending() {
		return ""
	}
	selection := m.contextSelection()
	plan := planContext(m.messages, nil, "", selection)
	included, excluded := 0, 0
	for _, message := range plan.history[plan.start:] {
		if includesModelContext(message) {
			for _, file := range message.Files {
				if plan.includesPriorFile(file.Path) {
					included++
				} else {
					excluded++
				}
			}
		}
	}
	if excluded > 0 {
		return fmt.Sprintf(m.tr("Context · %d earlier files included · %d excluded · F8", "문맥 · 이전 파일 %d개 포함 · %d개 제외 · F8"), included, excluded)
	}
	if included > 0 {
		return fmt.Sprintf(m.tr("Context · %d earlier attachments will be sent · F8", "문맥 · 이전 첨부 %d개 전송 예정 · F8"), included)
	}
	if len(plan.history) > 0 && (selection.RecentTurns != -1 || !selection.IncludePriorAttachments || len(selection.ExcludedPriorFiles) > 0) {
		return m.tr("Context restricted for this conversation · F8 to review", "이 대화의 문맥이 제한됨 · F8로 확인")
	}
	return ""
}

func (m Model) contextItems() []list.Item {
	selection := m.contextSelection()
	plan := m.nextRequestContext()
	turns := m.tr("All earlier turns", "이전 대화 전체")
	if selection.RecentTurns >= 0 {
		turns = fmt.Sprintf(m.tr("Earlier turns · last %d", "이전 대화 · 최근 %d개"), selection.RecentTurns)
	}
	attachmentMode := m.tr("On", "켬")
	if !selection.IncludePriorAttachments {
		attachmentMode = m.tr("Off", "끔")
	}
	totalTurns, includedTurns, earlierFiles := 0, 0, 0
	for i, message := range plan.history {
		if message.Role == "user" {
			totalTurns++
			if i >= plan.start {
				includedTurns++
			}
		}
		if i >= plan.start && includesModelContext(message) {
			for _, file := range message.Files {
				if plan.includesPriorFile(file.Path) {
					earlierFiles++
				}
			}
		}
	}
	draftFiles := 0
	if plan.current != nil {
		draftFiles = len(plan.current.Files)
	}
	items := []list.Item{
		modelItem{ID: "summary", Name: m.tr("Next request", "다음 요청"), Detail: fmt.Sprintf(
			m.tr("%s · %d/%d earlier turns · %d earlier files · %d draft files", "%s · 이전 대화 %d/%d개 · 이전 파일 %d개 · 초안 파일 %d개"), m.modelLabel(m.selected), includedTurns, totalTurns, earlierFiles, draftFiles)},
		modelItem{ID: "turns", Name: turns, Detail: m.tr("Enter: all → last 8 → last 3 → none · /context last N", "Enter: 전체 → 최근 8개 → 최근 3개 → 없음 · /context last N")},
		modelItem{ID: "attachments", Name: m.tr("Earlier attachments · ", "이전 첨부 · ") + attachmentMode, Detail: m.tr("Enter turns earlier files on or off · draft attachments always go", "Enter로 이전 파일 켜고 끄기 · 초안 첨부는 항상 포함")},
	}
	if len(selection.ExcludedPriorFiles) > 0 {
		items = append(items, modelItem{ID: "clear-file-exclusions", Name: m.tr("Clear file exclusions", "파일 제외 모두 해제"),
			Detail: fmt.Sprintf(m.tr("Enter to clear %d individual file exclusions.", "Enter로 파일 제외 %d개를 해제합니다."), len(selection.ExcludedPriorFiles))})
	}
	if plan.system != "" {
		items = append(items, modelItem{ID: "system", Name: m.tr("System prompt", "시스템 프롬프트"), Detail: m.tr("Included with every request in this conversation.", "이 대화의 모든 요청에 포함됩니다.")})
	}
	if plan.current != nil {
		for _, file := range plan.current.Files {
			items = append(items, modelItem{ID: "draft-file", Name: file.Path, Detail: m.tr("Draft attachment · included", "초안 첨부 · 포함")})
		}
	}
	for i := len(plan.history) - 1; i >= 0; i-- {
		if plan.history[i].Role == "user" {
			if len(plan.history[i].Files) > 0 {
				items = append(items, modelItem{ID: "retry", Name: m.tr("Retry attachments", "재시도 첨부"), Detail: m.tr("Alt+R and F5 include the files of the question being retried.", "Alt+R과 F5는 다시 시도하는 질문의 파일도 함께 보냅니다.")})
			}
			break
		}
	}
	seenFiles := map[string]bool{}
	for i := len(plan.history) - 1; i >= 0; i-- {
		message := plan.history[i]
		if !includesModelContext(message) {
			continue
		}
		access := m.tr("Included", "포함")
		if i < plan.start {
			access = m.tr("Excluded", "제외")
		}
		if message.Role == "user" {
			end := len(message.Content)
			for boundary := range message.Content {
				if boundary >= 512 {
					end = boundary
					break
				}
			}
			preview := []rune(oneLine(message.Content[:end]))
			if len(preview) > 90 {
				preview = append(preview[:89], '…')
			} else if end < len(message.Content) {
				preview = append(preview, '…')
			}
			items = append(items, modelItem{ID: "earlier-turn", Name: string(preview), Detail: access + m.tr(" · earlier question", " · 이전 질문")})
		}
		for _, file := range message.Files {
			if seenFiles[file.Path] {
				continue
			}
			seenFiles[file.Path] = true
			row := modelItem{ID: "inactive-file:" + file.Path, Name: file.Path}
			switch {
			case i < plan.start:
				row.Detail = m.tr("Excluded · outside selected turns", "제외 · 선택한 대화 범위 밖")
			case !plan.includeAttachments:
				row.Detail = m.tr("Excluded · earlier attachments are off", "제외 · 이전 첨부 꺼짐")
			case file.Path == "":
				row.Name, row.Detail = m.tr("Unnamed attachment", "이름 없는 첨부"), m.tr("Included · no stored file path", "포함 · 저장된 파일 경로 없음")
			default:
				row.ID = priorFileRow + file.Path
				row.Detail = m.tr("Included · Enter to exclude", "포함 · Enter로 제외")
				if !plan.includesPriorFile(file.Path) {
					row.Detail = m.tr("Excluded · Enter to include", "제외 · Enter로 포함")
				}
			}
			items = append(items, row)
		}
	}
	return items
}

func (m Model) updateContextSelection(selection client.ContextSelection) (tea.Model, tea.Cmd) {
	if m.loadingSession || m.loadingHistory {
		m.notify(m.tr("Wait for the conversation to finish loading.", "대화를 다 불러올 때까지 기다리세요."))
		return m, nil
	}
	if backend, ok := m.gateway.(interface{ SupportsContextSelection() bool }); m.saveEnabled && ok && !backend.SupportsContextSelection() {
		m.notify(m.tr("Reconnect with an updated gateway to save context settings.", "문맥 설정을 저장하려면 업데이트된 게이트웨이로 다시 연결하세요."))
		return m, nil
	}
	if backend, ok := m.gateway.(interface{ SupportsContextFileSelection() bool }); m.saveEnabled && ok &&
		len(selection.ExcludedPriorFiles) > 0 && !backend.SupportsContextFileSelection() {
		m.notify(m.tr("Reconnect with an updated gateway to save individual attachment exclusions.", "파일별 제외를 저장하려면 업데이트된 게이트웨이로 다시 연결하세요."))
		return m, nil
	}
	m.session.Context = &selection
	m.dirty = true
	m.layout()
	if m.modelsFocused && m.menu == "context" {
		cmd := m.refreshMenuItems(m.contextItems())
		return m, tea.Batch(cmd, m.persist())
	}
	m.notify(m.tr("Context updated for this conversation. F8 to review the next request.", "이 대화의 문맥을 바꿨습니다. F8로 다음 요청을 확인하세요."))
	return m, m.persist()
}

func (m Model) contextCommand(arg string) (tea.Model, tea.Cmd) {
	args := strings.Fields(arg)
	selection := m.contextSelection()
	switch {
	case len(args) == 0:
		return m.openMenu("context")
	case len(args) == 1 && args[0] == "all":
		selection.RecentTurns = -1
	case len(args) == 2 && args[0] == "last":
		n, err := strconv.Atoi(args[1])
		if err != nil || n < 0 || n > 198 {
			m.notify(m.tr("Enter /context last followed by a number from 0 to 198.", "/context last 뒤에 0부터 198 사이의 숫자를 입력하세요."))
			return m, nil
		}
		selection.RecentTurns = n
	case len(args) == 2 && args[0] == "attachments" && (args[1] == "on" || args[1] == "off"):
		selection.IncludePriorAttachments = args[1] == "on"
	default:
		m.notify(m.tr("Use /context, /context all, /context last N, or /context attachments on or off.", "/context, /context all, /context last N, /context attachments on 또는 off를 사용하세요."))
		return m, nil
	}
	return m.updateContextSelection(selection)
}

func (m Model) chooseContext(item modelItem) (tea.Model, tea.Cmd) {
	selection := m.contextSelection()
	switch item.ID {
	case "turns":
		switch selection.RecentTurns {
		case -1:
			selection.RecentTurns = 8
		case 8:
			selection.RecentTurns = 3
		case 3:
			selection.RecentTurns = 0
		default:
			selection.RecentTurns = -1
		}
	case "attachments":
		selection.IncludePriorAttachments = !selection.IncludePriorAttachments
	case "clear-file-exclusions":
		selection.ExcludedPriorFiles = nil
	default:
		path, file := strings.CutPrefix(item.ID, priorFileRow)
		if !file || path == "" {
			return m, nil
		}
		paths := slices.Clone(selection.ExcludedPriorFiles)
		if i := slices.Index(paths, path); i >= 0 {
			paths = slices.Delete(paths, i, i+1)
		} else {
			paths = append(paths, path)
		}
		selection.ExcludedPriorFiles = paths
	}
	return m.updateContextSelection(selection)
}
