package session

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/wooinwoo/freeway-cli/backend/internal/contracts"
	"github.com/wooinwoo/freeway-cli/backend/internal/storage"
)

func openStore(t *testing.T, path string) (*Store, *storage.Vault) {
	t.Helper()
	vault, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(vault)
	if err != nil {
		_ = vault.Close()
		t.Fatal(err)
	}
	return s, vault
}

func TestOversizedWireResponseDoesNotAdvanceRevision(t *testing.T) {
	s, vault := openStore(t, filepath.Join(t.TempDir(), "state"))
	defer vault.Close()
	chat, err := s.Create(testChat(), "")
	if err != nil {
		t.Fatal(err)
	}
	candidate := chat
	candidate.Messages = make([]contracts.Message, 5)
	content := strings.Repeat("<", contracts.MaxMessageRunes)
	for i := range candidate.Messages {
		candidate.Messages[i] = contracts.Message{ID: "large", Role: "user", Content: content, Status: "done"}
	}
	if _, err := s.Update(chat.ID, candidate, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized response was not refused: %v", err)
	}
	got, err := s.Get(chat.ID)
	if err != nil || got.Revision != chat.Revision || got.Messages[0].Content != chat.Messages[0].Content {
		t.Fatalf("rejected save changed state: %+v, %v", got, err)
	}
}

func TestAClearedDraftIsNotListedAsADraft(t *testing.T) {
	s, vault := openStore(t, filepath.Join(t.TempDir(), "state"))
	t.Cleanup(func() { _ = vault.Close() })
	chat := testChat()
	chat.Draft = ""
	if _, err := s.Create(chat, ""); err != nil {
		t.Fatal(err)
	}
	if summaries := s.List(); len(summaries) != 1 || summaries[0].HasDraft {
		t.Fatalf("a conversation whose input was cleared is listed as having a draft: %+v", summaries)
	}
}

func testChat() contracts.Conversation {
	return contracts.Conversation{
		Title: "테스트", Model: "fixture/model", Workspace: "/test/project",
		Settings: contracts.Settings{MaxTokens: 4096, Temperature: 0.6},
		Messages: []contracts.Message{
			{ID: "user", Role: "user", Content: "fixture prompt is private", Status: "done"},
			{ID: "assistant", Role: "assistant", Content: "partial answer", Status: "streaming", Model: "fixture/model"},
		},
		Draft: "unfinished", DraftFiles: []contracts.Attachment{},
		Queued: []contracts.Prompt{{Text: "queued"}}, Stash: &contracts.Prompt{Text: "stashed"},
	}
}

func TestConversationRestartAndRevision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	s, vault := openStore(t, path)
	chat, err := s.Create(testChat(), "")
	if err != nil || chat.Revision != 1 || chat.ID == "" {
		t.Fatalf("create: %+v, %v", chat, err)
	}
	data, err := os.ReadFile(filepath.Join(path, filename(chat.ID)))
	if err != nil || bytes.Contains(data, []byte(chat.Messages[0].Content)) {
		t.Fatalf("encrypted record invalid: %v", err)
	}
	if err := vault.Close(); err != nil {
		t.Fatal(err)
	}
	s, vault = openStore(t, path)
	t.Cleanup(func() { _ = vault.Close() })
	restored, err := s.Get(chat.ID)
	if err != nil || restored.Revision != 1 || restored.Draft != chat.Draft || restored.Stash == nil || restored.Stash.Text != "stashed" ||
		len(restored.Queued) != 1 || len(restored.Messages) != 2 || restored.Messages[1].Status != "stopped" || restored.Messages[1].Content != "partial answer" {
		t.Fatalf("restore: %+v, %v", restored, err)
	}
	summaries := s.List()
	if len(summaries) != 1 || summaries[0].Workspace != chat.Workspace || !summaries[0].HasDraft || summaries[0].QueuedCount != 1 {
		t.Fatalf("list: %+v", summaries)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, title := range []string{"first", "second"} {
		wg.Go(func() {
			candidate := restored
			candidate.Title = title
			_, err := s.Update(candidate.ID, candidate, "")
			results <- err
		})
	}
	wg.Wait()
	close(results)
	var updated, conflicts int
	for err := range results {
		switch {
		case err == nil:
			updated++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	saved, err := s.Get(chat.ID)
	if updated != 1 || conflicts != 1 || err != nil || saved.Revision != 2 {
		t.Fatalf("revision after competing saves: %+v, %v; successes=%d conflicts=%d", saved, err, updated, conflicts)
	}
}

func TestRejectedWritePreservesConversation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("injects a Unix permission failure")
	}
	path := filepath.Join(t.TempDir(), "state")
	s, vault := openStore(t, path)
	t.Cleanup(func() { _ = vault.Close() })
	chat, err := s.Create(testChat(), "")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(path, filename(chat.ID))
	if err := os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	candidate := chat
	candidate.Title = "unsaved replacement"
	if _, err := s.Update(chat.ID, candidate, ""); err == nil {
		t.Fatal("unsafe file permitted update")
	}
	if err := os.Chmod(file, 0600); err != nil {
		t.Fatal(err)
	}
	after, err := s.Get(chat.ID)
	if err != nil || after.Title != chat.Title || after.Revision != 1 {
		t.Fatalf("digest changed on failure: %+v, %v", after, err)
	}
	if _, err := s.Update(chat.ID, candidate, ""); err != nil {
		t.Fatalf("retry from original revision failed: %v", err)
	}
}

func TestSaveReplaySurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	s, vault := openStore(t, path)
	original := testChat()
	created, err := s.Create(original, "fixture-create-key")
	if err != nil {
		t.Fatal(err)
	}
	edited := created
	edited.Draft = "updated draft"
	updated, err := s.Update(created.ID, edited, "fixture-update-key")
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Close(); err != nil {
		t.Fatal(err)
	}
	s, vault = openStore(t, path)
	defer vault.Close()
	replayedCreate, err := s.Create(original, "fixture-create-key")
	if err != nil || replayedCreate.ID != created.ID || replayedCreate.Revision != 1 || len(s.List()) != 1 {
		t.Fatalf("create replay: %+v %v", replayedCreate, err)
	}
	replayedUpdate, err := s.Update(created.ID, edited, "fixture-update-key")
	if err != nil || replayedUpdate.Revision != updated.Revision || replayedUpdate.Draft != edited.Draft {
		t.Fatalf("update replay: %+v %v", replayedUpdate, err)
	}
	edited.Draft = "different payload"
	if _, err := s.Update(created.ID, edited, "fixture-update-key"); !errors.Is(err, ErrKeyReuse) {
		t.Fatalf("reused key accepted new data: %v", err)
	}
	if _, err := s.Update(created.ID, edited, "another-update-key"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update was accepted: %v", err)
	}
}

func TestUnreadableConversationDoesNotBlockOthers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	s, vault := openStore(t, path)
	broken, err := s.Create(testChat(), "")
	if err != nil {
		t.Fatal(err)
	}
	valid, err := s.Create(testChat(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Close(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(path, filename(broken.ID))
	if err := os.Truncate(file, 12); err != nil {
		t.Fatal(err)
	}
	s, vault = openStore(t, path)
	defer vault.Close()
	if got, err := s.Get(valid.ID); err != nil || got.ID != valid.ID {
		t.Fatalf("could not read healthy chat: %+v %v", got, err)
	}
	if _, err := s.Get(broken.ID); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("unexpected read error: %v", err)
	}
	if rows := s.List(); len(rows) != 2 || !rows[1].Damaged || rows[1].ID != broken.ID {
		t.Fatalf("damaged chat missing from the recovery list: %+v", rows)
	}
	if _, err := s.Update(broken.ID, broken, ""); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("unexpected write to damaged record: %v", err)
	}
	if info, err := os.Stat(file); err != nil || info.Size() != 12 {
		t.Fatalf("damaged record was changed: %v %v", info, err)
	}
}

func TestExplicitContextSurvivesReopeningAndRejectsAnOmittingWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	s, vault := openStore(t, path)
	original := testChat()
	original.Context = &contracts.ContextSelection{RecentTurns: 3, IncludePriorAttachments: true,
		ExcludedPriorFiles: []string{"notes/private.txt", "file with spaces.md"}}
	created, err := s.Create(original, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Close(); err != nil {
		t.Fatal(err)
	}
	s, vault = openStore(t, path)
	defer vault.Close()
	got, err := s.Get(created.ID)
	if err != nil || got.Context == nil || got.Context.RecentTurns != original.Context.RecentTurns ||
		!got.Context.IncludePriorAttachments || !slices.Equal(got.Context.ExcludedPriorFiles, original.Context.ExcludedPriorFiles) {
		t.Fatalf("context did not round-trip: %+v %v", got.Context, err)
	}
	got.Context = nil
	if _, err := s.Update(created.ID, got, ""); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("omitting writer removed context settings: %v", err)
	}
	got.Context = &contracts.ContextSelection{RecentTurns: -1, IncludePriorAttachments: true}
	if reset, err := s.Update(created.ID, got, ""); err != nil || len(reset.Context.ExcludedPriorFiles) > 0 {
		t.Fatalf("explicit reset failed: %v", err)
	}
}
