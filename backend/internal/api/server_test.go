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

func (f testInference) ProviderStatuses(context.Context) ([]contracts.ProviderStatus, error) {
	return []contracts.ProviderStatus{{ID: "fixture", KeyConfigured: true, State: "ready", FreeModels: 1}}, nil
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
	return New(key, store, inference, nil)
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
		{name: "provider status requires auth", path: "/v1/providers", method: "GET", status: 401},
		{name: "provider status", path: "/v1/providers", key: fixtureKey, method: "GET", status: 200},
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

type openCodeInference struct{ testInference }

type freshModelInference struct{ testInference }

func (freshModelInference) RefreshModels(context.Context) ([]contracts.Model, error) {
	return []contracts.Model{{ID: "fixture/new", Name: "New", Free: true, Ready: true}}, nil
}

func TestFreshCatalogRequiresManagedLocalClient(t *testing.T) {
	const localKey = "fixture-key-for-fresh-local-catalog"
	public, _ := auth.NewClientKey(fixtureKey)
	local, _ := auth.NewClientKey(localKey)
	localInference := freshModelInference{}
	handler := New(public, nil, freshModelInference{}, &LocalClient{
		Key: local, Inference: localInference,
		ManageProvider: func(context.Context, string, contracts.ProviderChange) error { return nil },
	})
	for _, tc := range []struct{ key, query, want string }{
		{fixtureKey, "?refresh=1", "fixture/model"},
		{localKey, "", "fixture/model"},
		{localKey, "?refresh=1", "fixture/new"},
	} {
		req := request(t, t.Context(), "GET", "http://127.0.0.1:4141/v1/models"+tc.query, "")
		req.Header.Set("Authorization", "Bearer "+tc.key)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != 200 || !strings.Contains(response.Body.String(), tc.want) {
			t.Fatalf("expected %s, got %d %s", tc.want, response.Code, response.Body.String())
		}
	}
}

func TestProviderManagementRequiresManagedLoopbackClient(t *testing.T) {
	const localKey = "fixture-key-for-local-management"
	public, _ := auth.NewClientKey(fixtureKey)
	local, _ := auth.NewClientKey(localKey)
	calls := 0
	handler := New(public, nil, testInference{}, &LocalClient{
		Key: local, Inference: testInference{},
		ManageProvider: func(_ context.Context, id string, change contracts.ProviderChange) error {
			calls++
			if id != "groq" || change.Action != "authenticate" || change.APIKey != "test-only-key" || !change.ConfirmFree {
				t.Errorf("unexpected change: provider=%q action=%q", id, change.Action)
			}
			return nil
		},
	})
	for _, tc := range []struct {
		key, origin string
		code        int
	}{
		{key: fixtureKey, code: 403},
		{key: localKey, origin: "https://outside.example", code: 403},
		{key: localKey, code: 204},
	} {
		req := request(t, t.Context(), "POST", "http://127.0.0.1:4141/v1/local/providers/groq/management",
			`{"action":"authenticate","apiKey":"test-only-key","confirmFree":true}`)
		req.Header.Set("Authorization", "Bearer "+tc.key)
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, req)
		if result.Code != tc.code || strings.Contains(result.Body.String(), "test-only-key") {
			t.Fatalf("unexpected management response: %d", result.Code)
		}
	}
	if calls != 1 {
		t.Fatalf("expected one authorized change, got %d", calls)
	}
}

type scopedStatusInference struct{ testInference }

func (scopedStatusInference) ProviderStatus(_ context.Context, id string) (contracts.ProviderStatus, error) {
	return contracts.ProviderStatus{ID: id, State: "ready", FreeModels: 2}, nil
}

func TestProviderStatusAdvertisesIndependentChecks(t *testing.T) {
	public, _ := auth.NewClientKey(fixtureKey)
	for _, tc := range []struct {
		inference Inference
		path, id  string
		scoped    bool
	}{
		{testInference{}, "/v1/providers", "fixture", false},
		{scopedStatusInference{}, "/v1/providers", "fixture", true},
		{scopedStatusInference{}, "/v1/providers?provider=test-provider", "test-provider", true},
	} {
		handler := New(public, nil, tc.inference, nil)
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request(t, t.Context(), "GET", "http://127.0.0.1:4141"+tc.path, ""))
		var response struct{ Data []contracts.ProviderStatus }
		if err := json.Unmarshal(result.Body.Bytes(), &response); result.Code != 200 || err != nil ||
			len(response.Data) != 1 || response.Data[0].ScopedCheck != tc.scoped || response.Data[0].ID != tc.id {
			t.Fatalf("unexpected provider status for %s: %d %s", tc.path, result.Code, result.Body.String())
		}
	}
}

func (openCodeInference) Models(context.Context) ([]contracts.Model, error) {
	return []contracts.Model{{ID: "free/auto"}, {ID: "model/deepseek/deepseek-fixture", RouteIDs: []string{"opencode/fixture"}, Free: true, Ready: true}}, nil
}

func (openCodeInference) ProviderStatuses(context.Context) ([]contracts.ProviderStatus, error) {
	return []contracts.ProviderStatus{{ID: "opencode", Name: "OpenCode", State: "ready", FreeModels: 1}}, nil
}

func TestOpenCodeRequiresLocalClientAndExplicitModel(t *testing.T) {
	localKey := "fixture-key-for-local-opencode"
	key, _ := auth.NewClientKey(fixtureKey)
	localToken, _ := auth.NewClientKey(localKey)
	inference := openCodeInference{testInference{stream: func(_ context.Context, req contracts.CompletionRequest, emit func(contracts.TextDelta) error) (contracts.Completion, error) {
		if req.Model != "opencode/fixture" {
			t.Fatalf("unexpected OpenCode request: %s", req.Model)
		}
		err := emit(contracts.TextDelta{Model: req.Model, Provider: "opencode", Text: "OpenCode reply"})
		return contracts.Completion{Model: req.Model, Provider: "opencode"}, err
	}}}
	h := New(key, nil, testInference{}, &LocalClient{Key: localToken, Inference: inference, OpenCode: inference})
	for _, tc := range []struct {
		method, endpoint, key, model, want string
		status                             int
	}{
		{method: "GET", endpoint: "/models", key: fixtureKey, status: 403},
		{method: "GET", endpoint: "/models", key: localKey, status: 200, want: "opencode/fixture"},
		{method: "POST", endpoint: "/chat/completions", key: localKey, model: "free/auto", status: 400},
		{method: "POST", endpoint: "/chat/completions", key: localKey, model: "opencode/fixture", status: 200, want: "OpenCode reply"},
	} {
		body := strings.Replace(`{"model":"MODEL","stream":true,"messages":[{"role":"user","content":"hello"}]}`, "MODEL", tc.model, 1)
		req := request(t, t.Context(), tc.method, "http://127.0.0.1:4141/v1/local/opencode"+tc.endpoint, body)
		req.Header.Set("Authorization", "Bearer "+tc.key)
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, req)
		if recorder.Code != tc.status || !strings.Contains(recorder.Body.String(), tc.want) ||
			tc.method == "GET" && strings.Contains(recorder.Body.String(), "free/auto") {
			t.Fatalf("%s %s: got %d %s", tc.method, tc.endpoint, recorder.Code, recorder.Body.String())
		}
	}
}

func TestOpenCodeCatalogIsScopedToTheManagedClient(t *testing.T) {
	const localKey = "fixture-key-for-local-opencode"
	key, _ := auth.NewClientKey(fixtureKey)
	localToken, _ := auth.NewClientKey(localKey)
	inference := openCodeInference{testInference{stream: func(_ context.Context, req contracts.CompletionRequest, emit func(contracts.TextDelta) error) (contracts.Completion, error) {
		if req.Model != "model/deepseek/deepseek-fixture" {
			t.Fatalf("unexpected selection: %s", req.Model)
		}
		completion := contracts.Completion{Model: "opencode/fixture", Provider: "opencode"}
		return completion, emit(contracts.TextDelta{Model: completion.Model, Provider: completion.Provider, Text: "fixture reply"})
	}}}
	common := testInference{stream: func(context.Context, contracts.CompletionRequest, func(contracts.TextDelta) error) (contracts.Completion, error) {
		return contracts.Completion{}, problem(http.StatusServiceUnavailable, "no_eligible_free_route", "No route")
	}}
	h := New(key, nil, common, &LocalClient{Key: localToken, Inference: inference})
	const body = `{"model":"model/deepseek/deepseek-fixture","stream":true,"messages":[{"role":"user","content":"hello"}]}`
	for _, tc := range []struct {
		path, method, body, want string
	}{
		{"/v1/models", "GET", "", "model/deepseek/deepseek-fixture"},
		{"/v1/providers", "GET", "", `"id":"opencode"`},
		{"/v1/chat/completions", "POST", body, `"provider":"opencode"`},
	} {
		for _, clientKey := range []string{fixtureKey, localKey} {
			req := request(t, t.Context(), tc.method, "http://127.0.0.1:4141"+tc.path, tc.body)
			req.Header.Set("Authorization", "Bearer "+clientKey)
			r := httptest.NewRecorder()
			h.ServeHTTP(r, req)
			if strings.Contains(r.Body.String(), tc.want) != (clientKey == localKey) {
				t.Fatalf("scope of %s: status %d, response %s", tc.path, r.Code, r.Body.String())
			}
		}
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
