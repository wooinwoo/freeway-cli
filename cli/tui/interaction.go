package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

type draftState struct {
	Text     string
	Row, Col int
	Files    []client.Attachment
}
type interactionState struct {
	undoDrafts, redoDrafts           []draftState
	killRing                         []string
	lastEditAction                   string
	yankStart, yankLength, yankIndex int
	stash                            *draftState
	files                            []client.Attachment
	attachmentsPending               int
	queue                            []client.PendingPrompt
	queuePaused                      bool
	lastEscape, lastInterrupt        time.Time
	editingAt                        int
	pendingEdited                    *client.PendingPrompt
	workdir                          string
	projectLabel                     string
	fileItems                        []list.Item
	fileEpoch                        uint64
	filesLoading                     bool
	fileSelection                    fileQuery
	promptsLoading                   bool
	promptSearchWanted               bool
	lastResponseTokens               int
	responseElapsed                  time.Duration
	editorOpen                       bool
}

func (m Model) captureDraft() draftState {
	info := m.prompt.LineInfo()
	return draftState{Text: m.prompt.Value(), Row: m.prompt.Line(), Col: info.StartColumn + info.ColumnOffset, Files: append([]client.Attachment(nil), m.files...)}
}
func (m *Model) restoreDraft(d draftState) {
	m.prompt.SetValue(d.Text)
	m.prompt.MoveToBegin()
	for i := 0; i <= len([]rune(d.Text)) && m.prompt.Line() < d.Row; i++ {
		m.prompt.CursorDown()
	}
	m.prompt.SetCursorColumn(d.Col)
	m.files = append([]client.Attachment(nil), d.Files...)
	m.markDraftChanged()
	m.layout()
}
func (m *Model) markDraftChanged() {
	m.dirty, m.lastEdit = true, time.Now()
	m.inputIndex = -1
	m.commandIndex = 0
}
func (m *Model) recordUndo(d draftState) {
	if len(m.undoDrafts) > 0 && m.undoDrafts[len(m.undoDrafts)-1].Text == d.Text {
		return
	}
	m.undoDrafts = append(m.undoDrafts, d)
	m.redoDrafts = nil
	size := 0
	for i := len(m.undoDrafts) - 1; i >= 0; i-- {
		size += len(m.undoDrafts[i].Text)
		if len(m.undoDrafts)-i > 80 || size > 2*1024*1024 {
			m.undoDrafts = append([]draftState(nil), m.undoDrafts[i+1:]...)
			break
		}
	}
}
func (m Model) undoInput(redo bool) (tea.Model, tea.Cmd) {
	source := &m.undoDrafts
	target := &m.redoDrafts
	if redo {
		source, target = &m.redoDrafts, &m.undoDrafts
	}
	if len(*source) == 0 {
		m.notify("Nothing to undo.")
		return m, nil
	}
	*target = append(*target, m.captureDraft())
	d := (*source)[len(*source)-1]
	*source = (*source)[:len(*source)-1]
	m.restoreDraft(d)
	m.lastEditAction = "undo"
	return m, nil
}
func (m Model) cursorIndex() int {
	lines := strings.Split(m.prompt.Value(), "\n")
	index := 0
	for i := 0; i < min(m.prompt.Line(), len(lines)); i++ {
		index += len([]rune(lines[i])) + 1
	}
	info := m.prompt.LineInfo()
	return index + info.StartColumn + info.ColumnOffset
}
func (m *Model) setCursorIndex(index int) {
	m.prompt.MoveToBegin()
	runes := []rune(m.prompt.Value())
	index = min(max(0, index), len(runes))
	row, col := 0, 0
	for _, r := range runes[:index] {
		if r == '\n' {
			row++
			col = 0
		} else {
			col++
		}
	}
	for i := 0; i <= len(runes) && m.prompt.Line() < row; i++ {
		m.prompt.CursorDown()
	}
	m.prompt.SetCursorColumn(col)
}

func (m Model) killInput(key string) (tea.Model, tea.Cmd) {
	d := m.captureDraft()
	runes := []rune(d.Text)
	cursor := min(m.cursorIndex(), len(runes))
	start, end := cursor, cursor
	switch key {
	case "ctrl+u":
		for start > 0 && runes[start-1] != '\n' {
			start--
		}
		if start == cursor && start > 0 {
			start--
		}
	case "ctrl+k":
		for end < len(runes) && runes[end] != '\n' {
			end++
		}
		if end == cursor && end < len(runes) {
			end++
		}
	case "ctrl+w":
		for start > 0 && unicode.IsSpace(runes[start-1]) {
			start--
		}
		for start > 0 && !unicode.IsSpace(runes[start-1]) {
			start--
		}
	case "alt+backspace", "ctrl+backspace":
		for start > 0 && unicode.IsSpace(runes[start-1]) {
			start--
		}
		if start > 0 {
			word := unicode.IsLetter(runes[start-1]) || unicode.IsDigit(runes[start-1]) || runes[start-1] == '_'
			for start > 0 && !unicode.IsSpace(runes[start-1]) && (unicode.IsLetter(runes[start-1]) || unicode.IsDigit(runes[start-1]) || runes[start-1] == '_') == word {
				start--
			}
		}
	case "alt+d":
		for end < len(runes) && unicode.IsSpace(runes[end]) {
			end++
		}
		for end < len(runes) && !unicode.IsSpace(runes[end]) {
			end++
		}
	}
	if start == end {
		return m, nil
	}
	killed := string(runes[start:end])
	m.recordUndo(d)
	if m.lastEditAction == "kill" && len(m.killRing) > 0 {
		if start < cursor {
			m.killRing[0] = killed + m.killRing[0]
		} else {
			m.killRing[0] += killed
		}
	} else {
		m.killRing = append([]string{killed}, m.killRing...)
		if len(m.killRing) > 20 {
			m.killRing = m.killRing[:20]
		}
	}
	m.prompt.SetValue(string(runes[:start]) + string(runes[end:]))
	m.setCursorIndex(start)
	m.markDraftChanged()
	m.lastEditAction = "kill"
	m.layout()
	return m, nil
}
func (m Model) yank(cycle bool) (tea.Model, tea.Cmd) {
	if len(m.killRing) == 0 {
		m.notify("Nothing deleted yet. /copy copies the last response.")
		return m, nil
	}
	d := m.captureDraft()
	runes := []rune(d.Text)
	start, end := min(m.cursorIndex(), len(runes)), min(m.cursorIndex(), len(runes))
	if cycle {
		if m.lastEditAction != "yank" {
			return m, nil
		}
		start = m.yankStart
		end = min(len(runes), start+m.yankLength)
		m.yankIndex = (m.yankIndex + 1) % len(m.killRing)
	} else {
		m.yankIndex = 0
	}
	m.recordUndo(d)
	text := m.killRing[m.yankIndex]
	m.prompt.SetValue(string(runes[:start]) + text + string(runes[end:]))
	m.setCursorIndex(start + len([]rune(text)))
	m.yankStart, m.yankLength = start, len([]rune(text))
	m.markDraftChanged()
	m.lastEditAction = "yank"
	m.layout()
	return m, nil
}
func (m Model) stashDraft() (tea.Model, tea.Cmd) {
	if m.attachmentsPending > 0 || m.loadingSession || m.loadingHistory {
		m.notify("Wait for the current load before stashing your draft.")
		return m, nil
	}
	current := m.captureDraft()
	if current.Text == "" && len(current.Files) == 0 {
		if m.stash == nil {
			m.notify("No stashed draft.")
			return m, nil
		}
		m.recordUndo(current)
		m.restoreDraft(*m.stash)
		m.stash = nil
		m.notify("Draft restored.")
		return m, nil
	}
	m.recordUndo(current)
	if m.stash != nil {
		old := *m.stash
		m.stash = &current
		m.restoreDraft(old)
		m.notify("Drafts swapped.")
	} else {
		m.stash = &current
		m.prompt.Reset()
		m.files = nil
		m.markDraftChanged()
		m.layout()
		m.notify("Draft stashed. Ctrl+S to restore.")
	}
	return m, nil
}

func (m Model) openPromptSearch() (tea.Model, tea.Cmd) {
	if m.modelsFocused && m.menu == "prompts" {
		m.models.list.CursorDown()
		return m, nil
	}
	items := []list.Item{}
	seen := map[string]bool{}
	for i := len(m.inputHistory) - 1; i >= 0; i-- {
		text := m.inputHistory[i]
		if seen[text] {
			continue
		}
		seen[text] = true
		items = append(items, modelItem{ID: fmt.Sprint(i), Name: strings.Join(strings.Fields(text), " "), Detail: text})
	}
	if len(items) == 0 {
		m.promptSearchWanted = true
		m.notify("Loading prompt history…")
		if m.promptsLoading {
			return m, nil
		}
		return m, m.loadPrompts()
	}
	m.menu = "prompts"
	m.models.list.SetItems(items)
	return m.changeFocus(true)
}

func (m Model) queuePrompt() (tea.Model, tea.Cmd) {
	if m.attachmentsPending > 0 {
		m.notify("Wait for the selected files to finish attaching before queueing.")
		return m, nil
	}
	text := strings.TrimSpace(m.prompt.Value())
	if text == "" {
		m.notify("Write a follow-up, then press Tab to queue it.")
		return m, nil
	}
	if strings.HasPrefix(text, "/") {
		m.notify("Run commands directly. Tab queues follow-up messages.")
		return m, nil
	}
	if len(m.queue) >= 20 {
		m.notify("The queue is full. Use /queue to review it.")
		return m, nil
	}
	m.queue = append(m.queue, client.PendingPrompt{Text: text, Files: append([]client.Attachment(nil), m.files...)})
	m.rememberPrompt(text, m.files)
	m.prompt.Reset()
	m.files = nil
	m.queuePaused = false
	m.markDraftChanged()
	m.layout()
	m.notify(fmt.Sprintf("Queued %d follow-up(s). Up takes the latest one back.", len(m.queue)))
	return m, nil
}
func (m *Model) takeQueued() bool {
	info := m.prompt.LineInfo()
	if len(m.queue) == 0 || m.prompt.Line() != 0 || info.RowOffset != 0 {
		return false
	}
	item := m.queue[len(m.queue)-1]
	m.queue = m.queue[:len(m.queue)-1]
	d := m.captureDraft()
	m.recordUndo(d)
	if d.Text != "" {
		m.stash = &d
	}
	m.prompt.SetValue(item.Text)
	m.prompt.MoveToEnd()
	m.files = append([]client.Attachment(nil), item.Files...)
	m.markDraftChanged()
	m.layout()
	m.notify("Follow-up removed from queue. Edit it and press Tab to queue again.")
	return true
}
func (m Model) startQueued() (tea.Model, tea.Cmd) {
	if m.busy || m.saving || m.queuePaused || len(m.queue) == 0 || m.loadingSession || m.loadingHistory {
		return m, nil
	}
	if len(m.messages) > 194 {
		m.queuePaused = true
		m.dirty = true
		m.notify("Conversation is full. Start a new conversation before sending queued follow-ups.")
		return m, nil
	}
	item := m.queue[0]
	m.queue = m.queue[1:]
	m.messages = append(m.messages, newMessage("user", item.Text))
	m.messages[len(m.messages)-1].Files = append([]client.Attachment(nil), item.Files...)
	m.dirty = true
	return m.startResponse()
}

func (m Model) handleInterrupt() (tea.Model, tea.Cmd) {
	if m.busy {
		m.queuePaused = true
		m.stopResponse()
		m.status = "Stopped"
		m.dirty = true
		m.renderMessages()
		m.notify("Stopped. Queued messages are paused; /queue to review.")
		return m, m.persist()
	}
	if m.modelsFocused {
		return m.changeFocus(false)
	}
	if m.prompt.Value() != "" || len(m.files) > 0 {
		d := m.captureDraft()
		m.recordUndo(d)
		m.stash = &d
		m.rememberPrompt(d.Text, d.Files)
		m.prompt.Reset()
		m.files = nil
		m.editingAt = -1
		m.markDraftChanged()
		m.layout()
		m.lastInterrupt = time.Now()
		m.notify("Input cleared. Ctrl+S restores it; Ctrl+C again quits.")
		return m, nil
	}
	if !m.lastInterrupt.IsZero() && time.Since(m.lastInterrupt) < 2*time.Second {
		return m.navigate("quit")
	}
	m.lastInterrupt = time.Now()
	m.notify("Press Ctrl+C again to save and quit.")
	return m, nil
}
func (m Model) doubleEscape() (tea.Model, tea.Cmd) {
	if m.prompt.Value() != "" || len(m.files) > 0 {
		d := m.captureDraft()
		m.recordUndo(d)
		m.stash = &d
		m.rememberPrompt(d.Text, d.Files)
		m.prompt.Reset()
		m.files = nil
		m.editingAt = -1
		m.markDraftChanged()
		m.layout()
		m.notify("Draft cleared. Up recalls it; Ctrl+S restores it.")
		return m, nil
	}
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Role == "user" {
			m.editingAt = i
			m.prompt.SetValue(m.messages[i].Content)
			m.prompt.MoveToEnd()
			m.files = append([]client.Attachment(nil), m.messages[i].Files...)
			m.markDraftChanged()
			m.layout()
			m.notify("Editing a previous prompt. Enter creates a fork; Esc keeps the original.")
			return m, nil
		}
	}
	m.notify("No previous prompt to edit.")
	return m, nil
}

type editorFinished struct {
	text  string
	epoch uint64
	err   error
}
type promptsLoaded struct {
	prompts []string
	err     error
}

func (m *Model) loadPrompts() tea.Cmd {
	backend, ok := m.gateway.(interface {
		PromptHistory(context.Context) ([]string, error)
	})
	if !ok {
		m.notify("No prompt history yet.")
		return nil
	}
	m.promptsLoading = true
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 20*time.Second)
		defer cancel()
		items, err := backend.PromptHistory(ctx)
		return promptsLoaded{prompts: items, err: err}
	}
}
func (m Model) openEditor() (tea.Model, tea.Cmd) {
	if m.loadingSession || m.loadingHistory {
		m.notify("Wait for the conversation to load before opening the editor.")
		return m, nil
	}
	if m.editorOpen {
		m.notify("The external editor is already open.")
		return m, nil
	}
	file, err := os.CreateTemp("", "freeway-prompt-*.md")
	if err != nil {
		m.notify(err.Error())
		return m, nil
	}
	path := file.Name()
	_, err = file.WriteString(m.prompt.Value())
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path)
		m.notify(err.Error())
		return m, nil
	}
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	var command *exec.Cmd
	if editor == "" {
		if runtime.GOOS == "windows" {
			if code, err := exec.LookPath("code.cmd"); err == nil {
				command = exec.Command("cmd.exe", "/d", "/s", "/c", "\""+code+"\" --wait \""+path+"\"")
			} else {
				command = exec.Command("notepad.exe", path)
			}
		} else {
			command = exec.Command("vi", path)
		}
	} else if runtime.GOOS == "windows" {
		// EDITOR/VISUAL is a user-configured command; the temporary path is quoted.
		command = exec.Command("cmd.exe", "/d", "/s", "/c", editor+" \""+path+"\"")
	} else {
		command = exec.Command("sh", "-c", editor+" \"$1\"", "freeway-editor", path)
	}
	epoch := m.sessionEpoch
	m.editorOpen = true
	finished := func(runErr error) tea.Msg {
		defer os.Remove(path)
		if runErr != nil {
			return editorFinished{epoch: epoch, err: runErr}
		}
		info, err := os.Stat(path)
		if err != nil {
			return editorFinished{epoch: epoch, err: err}
		}
		if info.Size() > 600000 {
			return editorFinished{epoch: epoch, err: fmt.Errorf("editor content exceeds 600 KB")}
		}
		data, err := os.ReadFile(path)
		if err == nil && !utf8.Valid(data) {
			err = fmt.Errorf("save the prompt as UTF-8")
		}
		return editorFinished{text: strings.ReplaceAll(string(data), "\r\n", "\n"), epoch: epoch, err: err}
	}
	if runtime.GOOS == "windows" && editor == "" {
		return m, func() tea.Msg { return finished(command.Run()) }
	}
	return m, tea.ExecProcess(command, finished)
}

type projectLoaded struct{ label, directory string }

func projectContext(dir string) tea.Cmd {
	return func() tea.Msg {
		label := filepath.Base(dir)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", "-C", dir, "branch", "--show-current")
		if output, err := cmd.Output(); err == nil && strings.TrimSpace(string(output)) != "" {
			label += " · " + strings.TrimSpace(string(output))
		}
		return projectLoaded{label: label, directory: dir}
	}
}
