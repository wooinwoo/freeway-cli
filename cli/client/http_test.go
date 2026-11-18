package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixtureHTTP(t *testing.T, handler http.HandlerFunc) *HTTP {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Error("client omitted gateway authentication")
			w.WriteHeader(401)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	c, err := NewHTTP(Config{URL: server.URL, APIKey: "fixture-key"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

func TestLoopbackAuthenticationAndRedirectBoundary(t *testing.T) {
	for _, address := range []string{"https://127.0.0.1:4141", "http://example.com", "http://user@localhost", "http://localhost/v1", "http://127.0.0.1?key=secret"} {
		if _, err := NewHTTP(Config{URL: address}); err == nil {
			t.Fatalf("unsafe URL accepted: %s", address)
		}
	}
	c := fixtureHTTP(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com", http.StatusFound)
	})
	_, err := c.Models(context.Background())
	var gateway *GatewayError
	if !errors.As(err, &gateway) || gateway.Status != 302 {
		t.Fatalf("redirect was not rejected: %v", err)
	}
	c.key = ""
	_, err = c.Models(context.Background())
	if !errors.As(err, &gateway) || gateway.Code != "gateway_auth_required" {
		t.Fatalf("missing key accepted: %v", err)
	}
}

func TestModelsExcludePaidAndUnavailableRoutes(t *testing.T) {
	c := fixtureHTTP(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"free/ready","free":true,"ready":true},{"id":"paid/ready","free":false,"ready":true},{"id":"free/offline","free":true,"ready":false}]}`)
	})
	models, err := c.Models(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "free/ready" {
		t.Fatalf("invalid model list: %#v %v", models, err)
	}
}

func TestPromptHistoryKeepsNewestPromptsInOldestFirstOrderAtLimit(t *testing.T) {
	c := fixtureHTTP(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/conversations" {
			fmt.Fprint(w, `{"data":[{"id":"chat","updatedAt":"2026-11-18"}]}`)
			return
		}
		chat := Conversation{}
		for i := 0; i < 205; i++ {
			chat.Messages = append(chat.Messages, Message{Role: "user", Content: fmt.Sprintf("prompt-%03d", i)})
		}
		json.NewEncoder(w).Encode(chat)
	})
	prompts, err := c.PromptHistory(context.Background())
	if err != nil || len(prompts) != 200 || prompts[0] != "prompt-005" || prompts[199] != "prompt-204" {
		t.Fatalf("bounded history lost ordering: %v %v", prompts, err)
	}
}

func TestConversationRoundTripIncludesClearsStashAndConflicts(t *testing.T) {
	var mu sync.Mutex
	var chat Conversation
	c := fixtureHTTP(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST":
			if err := json.NewDecoder(r.Body).Decode(&chat); err != nil {
				t.Error(err)
			}
			chat.ID = "conversation-1"
			chat.Revision = 1
		case r.Method == "PUT":
			var incoming Conversation
			json.NewDecoder(r.Body).Decode(&incoming)
			if incoming.Revision != chat.Revision {
				w.WriteHeader(409)
				fmt.Fprint(w, `{"error":{"message":"conflict","code":"chat_conflict"}}`)
				return
			}
			if incoming.DraftFiles == nil || incoming.Queued == nil || incoming.Messages == nil {
				t.Error("clear operation omitted an explicit array")
			}
			chat = incoming
			chat.Revision++
		}
		if r.URL.Path == "/v1/conversations" && r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]any{"data": []ConversationSummary{{ID: chat.ID, Title: chat.Title}}})
			return
		}
		json.NewEncoder(w).Encode(chat)
	})
	ctx := context.Background()
	original, err := c.SaveConversation(ctx, Conversation{Title: "개발 대화", Draft: "안 보낸 🌏 초안", Settings: Settings{MaxTokens: 8192, Temperature: 0.2}, Stash: &PendingPrompt{Text: "saved stash"}, DraftFiles: []Attachment{{Path: "a.go", Content: "context"}}, Queued: []PendingPrompt{{Text: "next"}}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := c.Conversation(ctx, original.ID)
	if err != nil || loaded.Draft != original.Draft || loaded.Settings.MaxTokens != 8192 || loaded.Stash.Text != "saved stash" {
		t.Fatalf("conversation context lost: %#v %v", loaded, err)
	}
	original.DraftFiles = nil
	original.Queued = nil
	original.Stash = nil
	cleared, err := c.SaveConversation(ctx, original)
	if err != nil || len(cleared.DraftFiles) != 0 || len(cleared.Queued) != 0 || cleared.Stash != nil || cleared.Revision != 2 {
		t.Fatalf("clear failed: %#v %v", cleared, err)
	}
	_, err = c.SaveConversation(ctx, original)
	var conflict *GatewayError
	if !errors.As(err, &conflict) || conflict.Code != "chat_conflict" {
		t.Fatalf("stale window accepted: %v", err)
	}
	list, err := c.Conversations(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("history missing: %#v %v", list, err)
	}
}

func TestStreamPreservesUnicodeSettingsAndFileContext(t *testing.T) {
	c := fixtureHTTP(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model       string           `json:"model"`
			MaxTokens   int              `json:"max_tokens"`
			Temperature float64          `json:"temperature"`
			Messages    []map[string]any `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.MaxTokens != 8192 || request.Temperature != 0.2 || len(request.Messages) != 2 || len(request.Messages[1]) != 2 || !strings.Contains(request.Messages[1]["content"].(string), "package main // 🌏") {
			t.Errorf("request context lost or UI fields leaked: %#v", request)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"model\":\"fixture/free\",\"provider\":\"fixture\",\"choices\":[{\"delta\":{\"content\":\"안녕 🌏\"}}]}\r\n\r\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"total_tokens\":12}}\n\ndata: [DONE]\n\n")
	})
	var events []Event
	err := c.StreamWithSettings(context.Background(), "free/ready", []Message{{Role: "system", Content: "Keep context"}, {ID: "private-ui-id", Role: "user", Content: "Read @a.go", Status: "done", Error: "private-ui-error", Files: []Attachment{{Path: "a.go", Content: "package main // 🌏"}}}}, Settings{MaxTokens: 8192, Temperature: 0.2}, func(e Event) { events = append(events, e) })
	if err != nil || len(events) != 2 || events[0].Text != "안녕 🌏" || events[1].Type != "done" || events[1].Usage.TotalTokens != 12 || events[1].Model != "fixture/free" {
		t.Fatalf("invalid stream: %#v %v", events, err)
	}
}

func TestActiveStreamDoesNotBlockSavingAndCancellation(t *testing.T) {
	started := make(chan struct{})
	c := fixtureHTTP(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			close(started)
			<-r.Context().Done()
			return
		}
		fmt.Fprint(w, `{"id":"saved","revision":1,"title":"draft"}`)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- c.Stream(ctx, "free/auto", []Message{{Role: "user", Content: "wait"}}, nil) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not start")
	}
	saveCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if _, err := c.SaveConversation(saveCtx, Conversation{Draft: "draft while generating"}); err != nil {
		t.Fatalf("stream blocked independent save: %v", err)
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stream ignored cancellation")
	}
}

func TestIncompleteAndErrorStreamsNeverFinishSuccessfully(t *testing.T) {
	for _, stream := range []string{`data: {"model":"fixture","choices":[{"delta":{"content":"partial"}}]}` + "\n\n", `data: {"error":{"message":"quota","code":"rate_limit_exceeded","status":429}}` + "\n\n"} {
		t.Run(stream[:12], func(t *testing.T) {
			c := fixtureHTTP(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, stream)
			})
			done := false
			err := c.Stream(context.Background(), "free/auto", nil, func(e Event) { done = done || e.Type == "done" })
			if err == nil || done {
				t.Fatalf("broken stream accepted: %v", err)
			}
		})
	}
}

func TestStreamKeepsRetryDelayAfterPartialResponse(t *testing.T) {
	c := fixtureHTTP(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"model":"fixture","choices":[{"delta":{"content":"partial"}}]}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"error":{"message":"quota","code":"quota_exhausted","status":429,"retryAfter":75}}`)
		fmt.Fprintln(w)
	})
	var events []Event
	err := c.Stream(t.Context(), "free/auto", nil, func(event Event) { events = append(events, event) })
	var quota *GatewayError
	if !errors.As(err, &quota) || quota.Status != 429 || quota.RetryAfter != 75 ||
		len(events) != 1 || events[0].Type != "text" || events[0].Text != "partial" {
		t.Fatalf("stream lost retry delay or partial text: events=%+v err=%v", events, err)
	}
}
