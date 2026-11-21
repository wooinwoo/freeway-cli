package tui

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

type fileQuery struct {
	Start, End     int
	Text, Original string
	Inline         bool
}
type filesIndexed struct {
	items []list.Item
	epoch uint64
	err   error
	query fileQuery
}
type fileAttached struct {
	file  client.Attachment
	epoch uint64
	err   error
	query fileQuery
}

func (m Model) fileToken() (fileQuery, bool) {
	runes := []rune(m.prompt.Value())
	end := min(m.cursorIndex(), len(runes))
	start := end
	for start > 0 && !unicode.IsSpace(runes[start-1]) {
		start--
	}
	if start == end || runes[start] != '@' {
		return fileQuery{}, false
	}
	return fileQuery{Start: start, End: end, Text: string(runes[start+1 : end]), Original: string(runes), Inline: true}, true
}

func indexProjectFiles(root string) ([]list.Item, error) {
	items := []list.Item{}
	visited := 0
	skip := map[string]bool{"node_modules": true, ".git": true, ".data": true, "build": true, "dist": true, "target": true, "vendor": true, "AppData": true, "go": true}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		visited++
		if visited > 12000 || len(items) >= 3000 {
			return fs.SkipAll
		}
		if path == root {
			return nil
		}
		name := entry.Name()
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if skip[name] || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(name))
		switch ext {
		case ".exe", ".dll", ".zip", ".png", ".jpg", ".jpeg", ".webp", ".ico", ".gif", ".mp4", ".mp3", ".pdf", ".db", ".key", ".pem", ".p12", ".pfx":
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		items = append(items, modelItem{ID: rel, Name: rel, Detail: "Attach text file from this project"})
		return nil
	})
	return items, err
}

func (m Model) openFiles(query fileQuery) (tea.Model, tea.Cmd) {
	if m.loadingSession || m.loadingHistory {
		m.notify("Wait for the conversation to load before browsing files.")
		return m, nil
	}
	m.menu = "files"
	m.fileSelection = query
	m.models.list.SetItems(m.fileItems)
	next, _ := m.changeFocus(true)
	m = next.(Model)
	m.menuQuery = query.Text
	m.menuSearching = query.Text != ""
	m.applyMenuFilter()
	if m.filesLoading {
		return m, nil
	}
	m.filesLoading = true
	m.fileEpoch++
	epoch := m.fileEpoch
	root := m.workdir
	return m, func() tea.Msg {
		items, err := indexProjectFiles(root)
		return filesIndexed{items: items, epoch: epoch, err: err, query: query}
	}
}

func (m Model) inlineFilePicker() bool {
	return m.modelsFocused && m.menu == "files" && m.fileSelection.Inline && m.height >= 18
}

func (m Model) dismissMenu() (tea.Model, tea.Cmd) {
	m.lastEscape = time.Time{}
	if m.menu == "files" {
		query := m.fileSelection
		if query.Inline && m.menuQuery != query.Text && m.prompt.Value() == query.Original {
			m.recordUndo(m.captureDraft())
			runes := []rune(query.Original)
			ref := "@" + m.menuQuery
			m.prompt.SetValue(string(runes[:query.Start]) + ref + string(runes[query.End:]))
			m.setCursorIndex(query.Start + len([]rune(ref)))
			m.markDraftChanged()
		}
	}
	return m.changeFocus(false)
}

func readProjectAttachment(root, path string) (client.Attachment, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, filepath.FromSlash(path))
	}
	absolute, err := filepath.EvalSymlinks(path)
	if err != nil {
		return client.Attachment{}, err
	}
	absoluteRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return client.Attachment{}, err
	}
	rel, err := filepath.Rel(absoluteRoot, absolute)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return client.Attachment{}, fmt.Errorf("choose a file inside the current project")
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return client.Attachment{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 128*1024 {
		return client.Attachment{}, fmt.Errorf("choose a text file no larger than 128 KB")
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return client.Attachment{}, err
	}
	if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return client.Attachment{}, fmt.Errorf("only UTF-8 text files are supported")
	}
	return client.Attachment{Path: filepath.ToSlash(rel), Content: strings.ReplaceAll(string(data), "\r\n", "\n")}, nil
}

func (m Model) attachPath(path string, query fileQuery) (tea.Model, tea.Cmd) {
	if m.loadingSession || m.loadingHistory {
		m.notify("Wait for the conversation to load before choosing files.")
		return m, nil
	}
	if len(m.files) >= 5 {
		m.notify("Attach up to five text files per prompt.")
		return m, nil
	}
	epoch := m.sessionEpoch
	root := m.workdir
	m.attachmentsPending++
	m.notify("Attaching " + path + "…")
	return m, func() tea.Msg {
		file, err := readProjectAttachment(root, path)
		return fileAttached{file: file, epoch: epoch, err: err, query: query}
	}
}

func (m Model) reviewQueue(action string) (tea.Model, tea.Cmd) {
	switch action {
	case "resume":
		m.queuePaused = false
		m.dirty = true
		if !m.busy {
			return m.startQueued()
		}
		m.notify("Queued messages will continue after this response.")
		return m, nil
	case "pause":
		m.queuePaused = true
		m.dirty = true
		m.notify("Follow-up queue paused.")
		return m, nil
	case "clear":
		if len(m.queue) > 0 {
			parts := []string{}
			for _, item := range m.queue {
				parts = append(parts, item.Text)
			}
			saved := draftState{Text: strings.Join(parts, "\n\n")}
			m.stash = &saved
		}
		m.queue = nil
		m.dirty = true
		m.layout()
		m.notify("Queue cleared. Ctrl+S can restore its text.")
		return m, nil
	}
	if len(m.queue) == 0 {
		m.notify("No follow-ups queued. Tab queues a draft while responding.")
		return m, nil
	}
	items := []list.Item{}
	for i, item := range m.queue {
		items = append(items, modelItem{ID: fmt.Sprint(i), Name: strings.Join(strings.Fields(item.Text), " "), Detail: "Enter to take back and edit this follow-up"})
	}
	m.menu = "queue"
	m.models.list.SetItems(items)
	return m.changeFocus(true)
}

func (m Model) composerExtraRows(width int) []string {
	rows := []string{}
	if m.editingAt >= 0 {
		rows = append(rows, "Editing previous prompt · Enter forks · Esc cancels")
	}
	if len(m.queue) > 0 {
		state := "waiting"
		if m.queuePaused {
			state = "paused"
		}
		rows = append(rows, fmt.Sprintf("Queued %d · %s · Up to edit · /queue", len(m.queue), state))
	}
	if len(m.files) > 0 {
		names := []string{}
		for _, file := range m.files {
			names = append(names, "@"+file.Path)
		}
		rows = append(rows, "Attached: "+strings.Join(names, "  ")+" · /detach")
	}
	if matches := m.commandMatches(); len(matches) > 0 {
		start := max(0, min(m.commandIndex-3, len(matches)-4))
		for j, item := range matches[start:min(start+4, len(matches))] {
			i := start + j
			marker := "  "
			if i == m.commandIndex {
				marker = "› "
			}
			rows = append(rows, marker+item.Name+"  "+item.Description)
		}
	}
	if hint := m.replyBudgetHint(); hint != "" {
		rows = append(rows, hint)
	}
	if hint := m.contextHint(); hint != "" {
		rows = append(rows, hint)
	}
	return rows[:min(len(rows), max(0, min(6, m.height-9)))]
}
