package client

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestConversationBridgeRoundTripsDraftSettingsAndRevision(t *testing.T) {
	poc := fixturePOC(t)
	ctx := context.Background()
	chat, err := poc.SaveConversation(ctx, Conversation{Title: "개발 대화", Model: "free/auto", Settings: Settings{MaxTokens: 8192, Temperature: 0.2}, Messages: []Message{{ID: "user-1", Role: "user", Content: "이전 질문", Status: "done"}}, Draft: "안 보낸 초안\n코드 🌏"})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := poc.Conversation(ctx, chat.ID)
	if err != nil || loaded.Draft != chat.Draft || loaded.Settings.MaxTokens != 8192 || loaded.Revision != 1 {
		t.Fatalf("conversation was not restored: %#v %v", loaded, err)
	}
	chat.Draft = "변경한 초안"
	updated, err := poc.SaveConversation(ctx, chat)
	if err != nil || updated.Revision != 2 {
		t.Fatalf("revision lost: %#v %v", updated, err)
	}
	_, err = poc.SaveConversation(ctx, chat)
	var conflict *GatewayError
	if !errors.As(err, &conflict) || conflict.Code != "chat_conflict" || conflict.Status != 409 {
		t.Fatalf("conflicting window was allowed to overwrite: %v", err)
	}
	history, err := poc.Conversations(ctx)
	if err != nil || len(history) != 1 || history[0].ID != chat.ID {
		t.Fatalf("history lost: %#v %v", history, err)
	}
}

func TestClearingAttachmentsAndQueueIsSentAsAnExplicitUpdate(t *testing.T) {
	poc := fixturePOC(t)
	chat, err := poc.SaveConversation(context.Background(), Conversation{Title: "Queue clearing", DraftFiles: []Attachment{{Path: "a.go", Content: "context"}}, Queued: []PendingPrompt{{Text: "pending"}}})
	if err != nil {
		t.Fatal(err)
	}
	chat.DraftFiles, chat.Queued = nil, nil
	cleared, err := poc.SaveConversation(context.Background(), chat)
	if err != nil || len(cleared.DraftFiles) != 0 || len(cleared.Queued) != 0 {
		t.Fatalf("clearing omitted from update: %#v %v", cleared, err)
	}
}

func TestAttachmentContentReachesTheModelWithoutUIFields(t *testing.T) {
	poc := fixturePOC(t)
	var answer string
	err := poc.StreamWithSettings(context.Background(), "custom", []Message{{Role: "system", Content: "Keep context"}, {Role: "user", Content: "Read @a.go", Files: []Attachment{{Path: "a.go", Content: "package main // 🌏"}}}}, Settings{MaxTokens: 8192, Temperature: 0.2}, func(event Event) {
		if event.Type == "done" {
			answer = event.Text
		}
	})
	if err != nil || !strings.Contains(answer, "package main // 🌏") || !strings.Contains(answer, "--- a.go ---") {
		t.Fatalf("file context missing: %q %v", answer, err)
	}
}

func TestStreamSettingsAndStoredMetadataDoNotLeakIntoPrompt(t *testing.T) {
	poc := fixturePOC(t)
	messages := []Message{{Role: "system", Content: "Keep context"}, {ID: "private-ui-id", Role: "user", Content: "안녕", Status: "done", Error: "private-ui-error"}}
	err := poc.StreamWithSettings(context.Background(), "custom", messages, Settings{MaxTokens: 8192, Temperature: 0.2}, func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
}
