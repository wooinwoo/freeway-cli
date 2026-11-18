package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wooinwoo/freeway-cli/cli/client"
)

func TestSessionLoadRejectsOldSaveAndRegeneration(t *testing.T) {
	m := connectedFixture(&memoryGateway{})
	m.session = client.Conversation{ID: "session-A", Revision: 1}
	m.messages = []client.Message{newMessage("user", "A question")}
	next, _ := m.performNavigation("open:session-B")
	m = next.(Model)
	for _, command := range []string{"/save", "/retry"} {
		next, cmd := m.execute(command, "")
		m = next.(Model)
		if cmd != nil || m.busy {
			t.Fatalf("%s started during a session load", command)
		}
	}
	next, cmd := m.retry()
	m = next.(Model)
	if cmd != nil || m.busy {
		t.Fatal("retry shortcut started during a session load")
	}
	m = update(m, conversationLoaded{chat: client.Conversation{ID: "session-B", Revision: 7, Draft: "B draft"}, epoch: m.sessionEpoch})
	m = update(m, conversationSaved{chat: client.Conversation{ID: "session-A", Revision: 2}, epoch: m.sessionEpoch})
	if m.session.ID != "session-B" || m.session.Revision != 7 {
		t.Fatalf("old save replaced the loaded session: %#v", m.session)
	}
	m = update(m, responseEvent{generation: m.generation - 1, event: client.Event{Type: "text", Text: "old response"}})
	if len(m.messages) != 0 {
		t.Fatal("an old response modified the loaded session")
	}
}

func TestSubmissionWaitsForSelectedAttachment(t *testing.T) {
	g := &burstGateway{}
	m := connectedFixture(g)
	m.workdir = t.TempDir()
	if err := os.WriteFile(filepath.Join(m.workdir, "context.txt"), []byte("file content"), 0600); err != nil {
		t.Fatal(err)
	}
	m.prompt.SetValue("Read this context")
	next, attach := m.attachPath("context.txt", fileQuery{})
	m = next.(Model)
	next, response := m.submit()
	m = next.(Model)
	if response != nil || len(g.requests) != 0 || m.prompt.Value() == "" {
		t.Fatal("a prompt was sent before its attachment finished")
	}
	m = update(m, attach())
	next, response = m.submit()
	_, _ = drainResponse(t, next.(Model), response)
	if len(g.requests) != 1 || len(g.requests[0][0].Files) != 1 {
		t.Fatalf("attachment missing from the submitted prompt: %#v", g.requests)
	}
}

func TestFileSelectionCannotReadPreviousWorkspaceDuringLoad(t *testing.T) {
	m := connectedFixture(&memoryGateway{})
	m.loadingSession = true
	next, cmd := m.attachPath("private.txt", fileQuery{})
	m = next.(Model)
	if cmd != nil || m.attachmentsPending != 0 {
		t.Fatal("attachment read started in the previous workspace during session loading")
	}
}

func TestSaveResultPreservesTitleEditedWhileSaving(t *testing.T) {
	m := connectedFixture(&memoryGateway{})
	m.session = client.Conversation{ID: "chat", Revision: 1, Title: "New title"}
	m = update(m, conversationSaved{chat: client.Conversation{ID: "chat", Revision: 2, Title: "Old title"}, epoch: m.sessionEpoch, titleAtSave: "Old title"})
	if m.session.Title != "New title" || m.session.Revision != 2 {
		t.Fatalf("an older save overwrote a newly edited title: %#v", m.session)
	}
}

func TestStashedDraftAndFilesSurviveSessionSnapshot(t *testing.T) {
	m := connectedFixture(&memoryGateway{})
	m.prompt.SetValue("Only copy of this draft")
	m.files = []client.Attachment{{Path: "context.txt", Content: "context"}}
	next, _ := m.stashDraft()
	m = next.(Model)
	chat := m.snapshot()
	if chat.Stash == nil || chat.Stash.Text != "Only copy of this draft" || len(chat.Stash.Files) != 1 {
		t.Fatalf("stash missing from the saved conversation: %#v", chat)
	}
	chat.ID = "restored"
	m = update(m, conversationLoaded{chat: chat, epoch: m.sessionEpoch})
	next, _ = m.stashDraft()
	m = next.(Model)
	if m.prompt.Value() != "Only copy of this draft" || len(m.files) != 1 || m.snapshot().Stash != nil {
		t.Fatal("saved stash could not be restored and cleared")
	}
}

func TestResumeTitleRespectsProjectScope(t *testing.T) {
	g := &memoryGateway{chat: client.Conversation{ID: "foreign", Title: "Other project", Workspace: t.TempDir()}}
	m := connectedFixture(g)
	m.startMode, m.resumeID = "resume", "Other project"
	m.allSessions, m.loadingHistory = false, true
	records := []client.ConversationSummary{{ID: g.chat.ID, Title: g.chat.Title, Workspace: g.chat.Workspace}}
	next, load := m.Update(historyLoaded{records: records, initial: true, epoch: m.sessionEpoch})
	m = next.(Model)
	if load != nil {
		m = update(m, load())
	}
	if m.session.ID == g.chat.ID {
		t.Fatal("another project was resumed without --all")
	}
}
