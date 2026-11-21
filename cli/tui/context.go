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
	for _, message := range p.history[p.start:] {
		if !includesModelContext(message) {
			continue
		}
		message.Files = p.priorFiles(message.Files)
		visit(message)
	}
	if p.current != nil {
		visit(*p.current)
	}
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
		return fmt.Sprintf("Context · %d earlier files included · %d excluded · F8", included, excluded)
	}
	if included > 0 {
		return fmt.Sprintf("Context · %d earlier attachments will be sent · F8", included)
	}
	if len(plan.history) > 0 && (selection.RecentTurns != -1 || !selection.IncludePriorAttachments || len(selection.ExcludedPriorFiles) > 0) {
		return "Context restricted for this conversation · F8 to review"
	}
	return ""
}

func (m Model) contextItems() []list.Item {
	selection := m.contextSelection()
	plan := m.nextRequestContext()
	turns := "All earlier turns"
	if selection.RecentTurns >= 0 {
		turns = fmt.Sprintf("Earlier turns · last %d", selection.RecentTurns)
	}
	attachmentMode := "On"
	if !selection.IncludePriorAttachments {
		attachmentMode = "Off"
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
		modelItem{ID: "summary", Name: "Next request", Detail: fmt.Sprintf(
			"%s · %d/%d earlier turns · %d earlier files · %d draft files", m.modelLabel(), includedTurns, totalTurns, earlierFiles, draftFiles)},
		modelItem{ID: "turns", Name: turns, Detail: "Enter cycles all, last 8, last 3, and no earlier turns. /context last N sets a custom count."},
		modelItem{ID: "attachments", Name: "Earlier attachments · " + attachmentMode, Detail: "Enter toggles earlier file contents. Draft attachments stay in the next request."},
	}
	if len(selection.ExcludedPriorFiles) > 0 {
		items = append(items, modelItem{ID: "clear-file-exclusions", Name: "Clear file exclusions",
			Detail: fmt.Sprintf("Enter to clear %d individual file exclusions.", len(selection.ExcludedPriorFiles))})
	}
	if plan.system != "" {
		items = append(items, modelItem{ID: "system", Name: "System prompt", Detail: "Included with every request in this conversation."})
	}
	if plan.current != nil {
		for _, file := range plan.current.Files {
			items = append(items, modelItem{ID: "draft-file", Name: file.Path, Detail: "Draft attachment · included"})
		}
	}
	for i := len(plan.history) - 1; i >= 0; i-- {
		if plan.history[i].Role == "user" {
			if len(plan.history[i].Files) > 0 {
				items = append(items, modelItem{ID: "retry", Name: "Retry attachments", Detail: "Alt+R and F5 include the files of the question being retried."})
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
		access := "Included"
		if i < plan.start {
			access = "Excluded"
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
			items = append(items, modelItem{ID: "earlier-turn", Name: string(preview), Detail: access + " · earlier question"})
		}
		for _, file := range message.Files {
			if seenFiles[file.Path] {
				continue
			}
			seenFiles[file.Path] = true
			row := modelItem{ID: "inactive-file:" + file.Path, Name: file.Path}
			switch {
			case i < plan.start:
				row.Detail = "Excluded · outside selected turns"
			case !plan.includeAttachments:
				row.Detail = "Excluded · earlier attachments are off"
			case file.Path == "":
				row.Name, row.Detail = "Unnamed attachment", "Included · no stored file path"
			default:
				row.ID = priorFileRow + file.Path
				row.Detail = "Included · Enter to exclude"
				if !plan.includesPriorFile(file.Path) {
					row.Detail = "Excluded · Enter to include"
				}
			}
			items = append(items, row)
		}
	}
	return items
}

func (m Model) updateContextSelection(selection client.ContextSelection) (tea.Model, tea.Cmd) {
	if m.loadingSession || m.loadingHistory {
		m.notify("Wait for the conversation to finish loading.")
		return m, nil
	}
	if backend, ok := m.gateway.(interface{ SupportsContextSelection() bool }); m.saveEnabled && ok && !backend.SupportsContextSelection() {
		m.notify("Reconnect with an updated gateway to save context settings.")
		return m, nil
	}
	if backend, ok := m.gateway.(interface{ SupportsContextFileSelection() bool }); m.saveEnabled && ok &&
		len(selection.ExcludedPriorFiles) > 0 && !backend.SupportsContextFileSelection() {
		m.notify("Reconnect with an updated gateway to save individual attachment exclusions.")
		return m, nil
	}
	m.session.Context = &selection
	m.dirty = true
	m.layout()
	if m.modelsFocused && m.menu == "context" {
		cmd := m.refreshMenuItems(m.contextItems())
		return m, tea.Batch(cmd, m.persist())
	}
	m.notify("Context updated for this conversation. F8 to review the next request.")
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
			m.notify("Enter /context last followed by a number from 0 to 198.")
			return m, nil
		}
		selection.RecentTurns = n
	case len(args) == 2 && args[0] == "attachments" && (args[1] == "on" || args[1] == "off"):
		selection.IncludePriorAttachments = args[1] == "on"
	default:
		m.notify("Use /context, /context all, /context last N, or /context attachments on or off.")
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
