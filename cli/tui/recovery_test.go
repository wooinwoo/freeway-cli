package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wooinwoo/freeway-cli/cli/client"
)

type lostSaveResponseGateway struct {
	memoryGateway
}

type unavailableConversationGateway struct {
	memoryGateway
}

func (*unavailableConversationGateway) Conversation(context.Context, string) (client.Conversation, error) {
	return client.Conversation{}, errors.New("fixture conversation unavailable")
}

func TestFailedOpenAfterRecoveryPreservesTheCurrentDraft(t *testing.T) {
	m := connectedFixture(&unavailableConversationGateway{})
	m.session.ID = "current"
	m.prompt.SetValue("현재 초안 🌏")
	m.files = []client.Attachment{{Path: "notes.md", Content: "current attachment"}}
	m.saveError, m.saveBlocked, m.dirty = "conflict", "chat_conflict", true
	next, _ := m.navigate("open:missing")
	m = next.(Model)
	next, load, _ := m.receiveRecovery(recoverySaved{chat: m.snapshot(), epoch: m.sessionEpoch, path: "recovery.json"})
	m = update(next.(Model), load())
	if m.session.ID != "current" || m.prompt.Value() != "현재 초안 🌏" || len(m.files) != 1 ||
		m.saveBlocked != "chat_conflict" || !m.dirty || !strings.Contains(m.notice, "recovery.json") {
		t.Fatalf("failed conversation switch lost recoverable state: %q, %q, %q", m.prompt.Value(), m.saveBlocked, m.notice)
	}
}

func TestRestoreRecoveryDraftKeepsAttachmentsAndUndo(t *testing.T) {
	path, err := writeRecovery(t.TempDir(), client.Conversation{Draft: "Recovered", DraftFiles: []client.Attachment{
		{Path: "notes.md", Content: "attachment"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	m := connectedFixture(&memoryGateway{})
	m.prompt.SetValue("Existing draft")
	next, restore := m.restoreRecovery(path)
	m = update(next.(Model), restore())
	if m.prompt.Value() != "Recovered" || len(m.files) != 1 || m.files[0].Content != "attachment" ||
		len(m.undoDrafts) == 0 || m.undoDrafts[len(m.undoDrafts)-1].Text != "Existing draft" {
		t.Fatalf("restored draft or previous edit missing: %q %+v", m.prompt.Value(), m.files)
	}
}

func TestDraftEditedWhileOpeningAnotherConversationStaysInCurrentSession(t *testing.T) {
	gateway := &memoryGateway{chat: client.Conversation{ID: "other", Draft: "other saved draft"}}
	m := connectedFixture(gateway)
	m.session.ID = "current"
	next, load := m.navigate("open:other")
	m = next.(Model)
	m.prompt.SetValue("still writing")
	m.markDraftChanged()
	m = update(m, load())
	if m.session.ID != "current" || m.prompt.Value() != "still writing" || m.loadingSession {
		t.Fatalf("session switched while its draft was being edited: %q, %q", m.session.ID, m.prompt.Value())
	}
}

func (g *lostSaveResponseGateway) SaveConversation(ctx context.Context, chat client.Conversation) (client.Conversation, error) {
	if len(g.saved) == 0 {
		g.saved = append(g.saved, chat)
		return client.Conversation{}, errors.New("connection closed before acknowledgment")
	}
	return g.memoryGateway.SaveConversation(ctx, chat)
}

func TestSaveRetryKeepsOriginalKeyAndEdits(t *testing.T) {
	gateway := &lostSaveResponseGateway{}
	m := connectedFixture(gateway)
	m.prompt.SetValue("first draft")
	m = update(m, m.persist()())
	m.prompt.SetValue("later edits")
	m.dirty = true
	m = update(m, m.persist()())
	if len(gateway.saved) != 2 || gateway.saved[0].SaveKey == "" || gateway.saved[0].SaveKey != gateway.saved[1].SaveKey ||
		gateway.saved[1].Draft != "first draft" || !m.dirty {
		t.Fatalf("save retry changed the original operation or lost edits: %+v", gateway.saved)
	}
	m = update(m, m.persist()())
	if len(gateway.saved) != 3 || gateway.saved[2].SaveKey == gateway.saved[0].SaveKey || gateway.saved[2].Draft != "later edits" {
		t.Fatalf("later edits did not get their own save: %+v", gateway.saved)
	}
}

func TestFailedSaveRecoveryKeepsFullSnapshotBeforeNewConversation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	m := connectedFixture(&memoryGateway{})
	m.prompt.SetValue("unsaved draft")
	m.files = []client.Attachment{{Path: "notes.txt", Content: "attachment text"}}
	m.queue = []client.PendingPrompt{{Text: "queued question"}}
	m.dirty = true
	m.saveBlocked, m.saveError = "chat_conflict", "Conversation changed"
	next, cmd := m.navigate("new")
	m = next.(Model)
	if cmd != nil || m.prompt.Value() == "" {
		t.Fatal("navigation proceeded before recovery")
	}
	next, cmd = m.recoverConversation()
	m = next.(Model)
	m = update(m, cmd())
	if m.prompt.Value() != "" || m.saveError != "" {
		t.Fatal("recovery did not finish navigation")
	}
	files, err := filepath.Glob(filepath.Join(home, ".freeway", "recovery", "conversation-*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("missing recovery file: %v %v", files, err)
	}
	wire, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var chat client.Conversation
	if err := json.Unmarshal(wire, &chat); err != nil || chat.Draft != "unsaved draft" ||
		len(chat.DraftFiles) != 1 || chat.DraftFiles[0].Content != "attachment text" || len(chat.Queued) != 1 {
		t.Fatalf("recovery file did not preserve all inputs: %+v %v", chat, err)
	}
}
