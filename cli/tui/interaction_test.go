package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

func ctrl(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Mod: tea.ModCtrl} }

func TestEditingKillYankUndoAndStashPreserveUnicode(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	m.prompt.SetValue("hello 한글 🌏")
	m.prompt.MoveToEnd()
	m = update(m, ctrl('w'))
	if m.prompt.Value() != "hello 한글 " {
		t.Fatalf("word delete: %q", m.prompt.Value())
	}
	m = update(m, ctrl('y'))
	if m.prompt.Value() != "hello 한글 🌏" {
		t.Fatalf("yank: %q", m.prompt.Value())
	}
	m = update(m, ctrl('z'))
	if m.prompt.Value() != "hello 한글 " {
		t.Fatalf("undo: %q", m.prompt.Value())
	}
	m = update(m, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl | tea.ModShift})
	if m.prompt.Value() != "hello 한글 🌏" {
		t.Fatalf("redo: %q", m.prompt.Value())
	}
	m.files = []client.Attachment{{Path: "hello.go", Content: "package main"}}
	m = update(m, ctrl('s'))
	if m.prompt.Value() != "" || len(m.files) != 0 {
		t.Fatal("stash did not clear composer")
	}
	m = update(m, ctrl('s'))
	if m.prompt.Value() != "hello 한글 🌏" || len(m.files) != 1 {
		t.Fatal("stash lost text or attachment")
	}
	m = update(m, ctrl('c'))
	if m.prompt.Value() != "" || m.stash == nil {
		t.Fatal("interrupt lost recoverable draft")
	}
	m = update(m, ctrl('s'))
	if m.prompt.Value() != "hello 한글 🌏" {
		t.Fatal("cleared draft could not be restored")
	}
}

func TestQueuedFollowUpsStayFIFOAndPreserveComposer(t *testing.T) {
	g := &burstGateway{}
	m := connectedFixture(g)
	m.busy = true
	for _, text := range []string{"first follow-up", "second follow-up"} {
		m.prompt.SetValue(text)
		m.prompt.MoveToEnd()
		m = update(m, press(tea.KeyTab))
	}
	if len(m.queue) != 2 || m.prompt.Value() != "" {
		t.Fatal("follow-ups were not queued")
	}
	m = update(m, press(tea.KeyUp))
	if m.prompt.Value() != "second follow-up" || len(m.queue) != 1 {
		t.Fatal("Up did not take last queued message back")
	}
	m = update(m, press(tea.KeyTab))
	m.busy = false
	m.prompt.SetValue("next draft")
	next, cmd := m.startQueued()
	m, _ = drainResponse(t, next.(Model), cmd)
	if m.messages[0].Content != "first follow-up" || m.prompt.Value() != "next draft" || len(m.queue) != 1 {
		t.Fatal("queue changed order or composer")
	}
	next, cmd = m.startQueued()
	m, _ = drainResponse(t, next.(Model), cmd)
	if m.messages[2].Content != "second follow-up" || len(m.queue) != 0 {
		t.Fatal("second follow-up was lost")
	}
}

func TestMenusDuringGenerationDoNotQueueOrCancelByAccident(t *testing.T) {
	m := connectedFixture(&burstGateway{})
	m.busy = true
	m.prompt.SetValue("draft while generating")
	next, _ := m.changeFocus(true)
	m = next.(Model)
	m = update(m, press(tea.KeyDown))
	m = update(m, press(tea.KeyEnter))
	if m.modelsFocused || m.selected != "free/chosen" || !m.busy || len(m.queue) != 0 || m.prompt.Value() != "draft while generating" {
		t.Fatal("model menu interfered with active generation")
	}
	next, _ = m.openMenu("help")
	m = next.(Model)
	m = update(m, press(tea.KeyEsc))
	if !m.busy || m.modelsFocused {
		t.Fatal("closing help cancelled generation")
	}
	m.prompt.SetValue("/mod")
	m = update(m, press(tea.KeyTab))
	if m.prompt.Value() != "/model" || len(m.queue) > 0 {
		t.Fatal("Tab queued a command instead of completing it")
	}
}

func TestRestoredSessionKeepsDraftAttachmentsAndPausesQueue(t *testing.T) {
	m := connectedFixture(&memoryGateway{})
	m.sessionEpoch = 2
	chat := client.Conversation{ID: "restored", Workspace: m.workdir, Draft: "unsent 🌏", DraftFiles: []client.Attachment{{Path: "a.go", Content: "a"}}, Queued: []client.PendingPrompt{{Text: "queued"}}}
	m = update(m, conversationLoaded{chat: chat, epoch: 2})
	if m.prompt.Value() != chat.Draft || len(m.files) != 1 || len(m.queue) != 1 || !m.queuePaused {
		t.Fatal("resume lost draft/files/queue or started queued work")
	}
	got := m.snapshot()
	if got.Workspace != m.workdir || got.DraftFiles[0].Content != "a" || got.Queued[0].Text != "queued" {
		t.Fatal("session snapshot lost context")
	}
	m = update(m, editorFinished{text: "wrong session editor output", epoch: 1})
	if m.prompt.Value() != chat.Draft || m.stash == nil || m.stash.Text != "wrong session editor output" {
		t.Fatal("late editor overwrote another session")
	}
}

func TestSessionPickerCanChangeProjectScope(t *testing.T) {
	m := connectedFixture(&memoryGateway{})
	records := []client.ConversationSummary{{ID: "local", Title: "Local", Workspace: m.workdir}, {ID: "remote", Title: "Other project", Workspace: filepath.Join(m.workdir, "other")}}
	m = update(m, historyLoaded{records: records, epoch: m.sessionEpoch})
	if !m.modelsFocused || len(m.models.list.Items()) != 1 {
		t.Fatal("picker did not scope to current project")
	}
	m = update(m, ctrl('a'))
	if !m.allSessions || len(m.models.list.Items()) != 2 {
		t.Fatal("all-project toggle failed")
	}
	m = update(m, ctrl('a'))
	if m.allSessions || len(m.models.list.Items()) != 1 {
		t.Fatal("current-project toggle failed")
	}
}

func TestDoubleEscapeEditPreservesOriginalUntilFork(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	m.messages = []client.Message{newMessage("user", "original"), newMessage("assistant", "answer")}
	m.lastEscape = time.Now()
	m = update(m, press(tea.KeyEsc))
	if m.editingAt != 0 || m.prompt.Value() != "original" {
		t.Fatal("double escape did not edit last prompt")
	}
	m.prompt.SetValue("revised")
	m = update(m, press(tea.KeyEsc))
	if m.editingAt != -1 || m.messages[0].Content != "original" {
		t.Fatal("cancel changed original history")
	}
}

func TestProjectFileAttachmentIsBoundedAndRestorable(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "hello.go"), []byte("package main\n// 한글 🌏"), 0600)
	os.Mkdir(filepath.Join(root, "node_modules"), 0700)
	os.WriteFile(filepath.Join(root, "node_modules", "hidden.js"), []byte("hidden"), 0600)
	items, err := indexProjectFiles(root)
	if err != nil || len(items) != 1 {
		t.Fatalf("file index: %v %v", items, err)
	}
	file, err := readProjectAttachment(root, "hello.go")
	if err != nil || !strings.Contains(file.Content, "한글 🌏") {
		t.Fatalf("attachment: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	os.WriteFile(outside, []byte("outside"), 0600)
	if _, err = readProjectAttachment(root, outside); err == nil {
		t.Fatal("outside-project file accepted")
	}
	os.WriteFile(filepath.Join(root, "binary"), []byte{0, 1}, 0600)
	if _, err = readProjectAttachment(root, "binary"); err == nil {
		t.Fatal("binary attachment accepted")
	}
	m := update(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	m.workdir = root
	m.prompt.SetValue("Read @he")
	m.prompt.MoveToEnd()
	query, ok := m.fileToken()
	if !ok || query.Text != "he" {
		t.Fatal("inline file token not recognized")
	}
	m = update(m, fileAttached{file: file, epoch: m.sessionEpoch, query: query})
	if m.prompt.Value() != "Read @hello.go " || len(m.files) != 1 {
		t.Fatalf("inline file replacement: %q", m.prompt.Value())
	}
}
