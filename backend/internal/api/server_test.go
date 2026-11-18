package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wooinwoo/freeway-cli/backend/internal/auth"
	"github.com/wooinwoo/freeway-cli/backend/internal/contracts"
	"github.com/wooinwoo/freeway-cli/backend/internal/session"
	"github.com/wooinwoo/freeway-cli/backend/internal/storage"
)

const fixtureKey = "fixture-key-for-loopback-tests"

type testInference struct {
	stream func(context.Context, contracts.CompletionRequest, func(contracts.TextDelta) error) (contracts.Completion, error)
}

func (f testInference) Models(context.Context) ([]contracts.Model, error) {
	return []contracts.Model{{ID: "fixture/model", Name: "Fixture", Free: true, Ready: true}}, nil
}

func (f testInference) Stream(ctx context.Context, req contracts.CompletionRequest, emit func(contracts.TextDelta) error) (contracts.Completion, error) {
	return f.stream(ctx, req, emit)
}

func testServer(t *testing.T, inference testInference) http.Handler {
	t.Helper()
	vault, err := storage.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	store, err := session.Open(vault)
	if err != nil {
		t.Fatal(err)
	}
	key, err := auth.NewClientKey(fixtureKey)
	if err != nil {
		t.Fatal(err)
	}
	return New(key, store, inference)
}

func request(t *testing.T, ctx context.Context, method, url, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+fixtureKey)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:5682"
	return req
}

const newChat = `{"title":"fixture","model":"fixture/model","settings":{"maxTokens":4096,"temperature":0.6},"messages":[],"draft":"kept","draftFiles":[],"queued":[],"stash":null}`

func TestLocalAuthenticationAndRequiredFields(t *testing.T) {
	handler := testServer(t, testInference{})
	for _, tc := range []struct {
		name, path, method, body, key, origin string
		status                                int
	}{
		{name: "missing auth", path: "/v1/models", method: "GET", status: 401},
		{name: "wrong auth", path: "/v1/models", key: "wrong-generated-client-key", method: "GET", status: 401},
		{name: "cross origin", path: "/v1/models", key: fixtureKey, origin: "https://external.example", method: "GET", status: 403},
		{name: "models", path: "/v1/models", key: fixtureKey, method: "GET", status: 200},
		{name: "missing content", path: "/v1/chat/completions", key: fixtureKey, method: "POST",
			body: `{"model":"fixture/model","stream":true,"messages":[{"role":"user"}]}`, status: 400},
		{name: "missing temperature", path: "/v1/conversations", key: fixtureKey, method: "POST",
			body: strings.Replace(newChat, `,"temperature":0.6`, "", 1), status: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := request(t, t.Context(), tc.method, "http://127.0.0.1:4141"+tc.path, tc.body)
			if tc.key == "" {
				req.Header.Del("Authorization")
			} else {
				req.Header.Set("Authorization", "Bearer "+tc.key)
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != tc.status || strings.Contains(recorder.Body.String(), fixtureKey) {
				t.Fatalf("got %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestConversationSaveDuringStreamAndDisconnect(t *testing.T) {
	disconnected := make(chan struct{})
	inference := testInference{stream: func(ctx context.Context, _ contracts.CompletionRequest, emit func(contracts.TextDelta) error) (contracts.Completion, error) {
		if err := emit(contracts.TextDelta{Text: "first chunk", Model: "fixture/model", Provider: "fixture"}); err != nil {
			return contracts.Completion{}, err
		}
		<-ctx.Done()
		close(disconnected)
		return contracts.Completion{}, ctx.Err()
	}}
	server := httptest.NewServer(testServer(t, inference))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req := request(t, ctx, "POST", server.URL+"/v1/chat/completions", `{"model":"fixture/model","stream":true,"messages":[{"role":"user","content":"stream"}]}`)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || resp.StatusCode != 200 || !strings.HasPrefix(line, "data: ") || !strings.Contains(line, "first chunk") {
		t.Fatalf("SSE status=%d line=%q err=%v", resp.StatusCode, line, err)
	}
	save, err := server.Client().Do(request(t, ctx, "POST", server.URL+"/v1/conversations", newChat))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(save.Body)
	save.Body.Close()
	var chat contracts.Conversation
	if err != nil || save.StatusCode != 201 || json.Unmarshal(body, &chat) != nil || chat.ID == "" || chat.Revision != 1 {
		t.Fatalf("concurrent save status=%d body=%s err=%v", save.StatusCode, body, err)
	}
	cancel()
	select {
	case <-disconnected:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP disconnect left the upstream running")
	}
}
