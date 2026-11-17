package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func openCodeFixture(t *testing.T, handler http.HandlerFunc) *openCodeAdapter {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	var definition Definition
	for _, d := range Definitions() {
		if d.ID == "opencode" {
			definition = d
		}
	}
	adapter := NewOpenCode(definition, CLIConfig{HTTPClient: server.Client()}).(*openCodeAdapter)
	adapter.base = server.URL
	t.Cleanup(func() { _ = adapter.Close() })
	return adapter
}

func openCodeTestModel() Model {
	return Model{ID: "opencode/free-test", Provider: "opencode", UpstreamID: "free-test", Free: true, Ready: true,
		Reasoning: Reasoning{Parameter: "variant", Efforts: []string{"low", "max"}}}
}

func openCodeEvent(w http.ResponseWriter, event any) {
	data, _ := json.Marshal(event)
	fmt.Fprintf(w, "data: %s\n\n", data)
	w.(http.Flusher).Flush()
}

func TestNativeOpenCodeCatalogKeepsOnlyItsOwnFreeModels(t *testing.T) {
	adapter := openCodeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "opencode" || password == "" {
			t.Error("missing private server credentials")
		}
		fmt.Fprint(w, `{"all":[{"id":"paid-account","models":{"paid":{"id":"outside","cost":{"input":0,"output":0}}}},{"id":"opencode","models":{"free":{"id":"free-test","cost":{"input":0,"output":0},"variants":{"low":{},"max":{}}},"paid":{"id":"paid","cost":{"input":1,"output":0}},"flag":{"id":"flag","isFree":false,"cost":{"input":0,"output":0}},"old":{"id":"old","status":"deprecated","cost":{"input":0,"output":0}}}}]}`)
	})
	models, err := adapter.Models(context.Background(), "")
	if err != nil || len(models) != 1 || models[0].ID != "opencode/free-test" || len(models[0].Reasoning.Efforts) != 2 {
		t.Fatalf("catalog: %+v %v", models, err)
	}
}

func TestNativeOpenCodeStreamsAndUsesAuthoritativeFinalSuffix(t *testing.T) {
	events := make(chan any, 8)
	firstText := make(chan struct{})
	var once sync.Once
	var deleted atomic.Bool
	adapter := openCodeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/session":
			var body struct {
				Permission []map[string]string `json:"permission"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Permission) != 2 || body.Permission[1]["action"] != "ask" {
				t.Error("session must retain only approval-gated read")
			}
			fmt.Fprint(w, `{"id":"own"}`)
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			openCodeEvent(w, map[string]any{"type": "server.connected"})
			for {
				select {
				case event := <-events:
					openCodeEvent(w, event)
				case <-r.Context().Done():
					return
				}
			}
		case "/session/own/message":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["variant"] != "low" || body["agent"] != "freeway" || body["model"].(map[string]any)["modelID"] != "free-test" {
				t.Error("wrong exact model or reasoning variant")
			}
			if _, ok := body["tools"]; ok {
				t.Error("legacy tools booleans would override permissions")
			}
			if !strings.Contains(body["system"].(string), "system instruction") {
				t.Error("system message missing")
			}
			events <- map[string]any{"type": "message.updated", "properties": map[string]any{"info": map[string]any{"id": "assistant", "sessionID": "own", "role": "assistant"}}}
			events <- map[string]any{"type": "message.part.updated", "properties": map[string]any{"part": map[string]any{"id": "part", "messageID": "assistant", "sessionID": "own", "type": "text", "text": "안"}}}
			select {
			case <-firstText:
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, `{"info":{"cost":0,"tokens":{"input":2,"output":3,"reasoning":4,"cache":{"read":5,"write":6}}},"parts":[{"type":"text","text":"안녕"}]}`)
		case "/session/own":
			deleted.Store(r.Method == http.MethodDelete)
			fmt.Fprint(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	})
	var output strings.Builder
	result, err := adapter.Stream(context.Background(), openCodeTestModel(), "", Request{ReasoningEffort: "low", Messages: []Message{{Role: "system", Content: "system instruction"}, {Role: "user", Content: "안녕"}}}, func(text string) { output.WriteString(text); once.Do(func() { close(firstText) }) })
	if err != nil || result.Content != "안녕" || output.String() != "안녕" || result.ReasoningEffort != "low" || !deleted.Load() {
		t.Fatalf("stream: %+v %q deleted=%v %v", result, output.String(), deleted.Load(), err)
	}
	var usage map[string]int
	if json.Unmarshal(result.Usage, &usage) != nil || usage["prompt_tokens"] != 13 || usage["completion_tokens"] != 7 || usage["total_tokens"] != 20 {
		t.Fatalf("usage: %s", result.Usage)
	}
	configuration := openCodeConfiguration()
	permission := configuration["permission"].(map[string]string)
	if permission["*"] != "deny" || permission["read"] != "ask" {
		t.Fatal("tool permission boundary changed")
	}
}

func TestNativeOpenCodeRejectsPermissionsAndFailsClosed(t *testing.T) {
	for _, rejectStatus := range []int{200, 500} {
		t.Run(fmt.Sprint(rejectStatus), func(t *testing.T) {
			events := make(chan any, 2)
			var rejected, aborted, deleted atomic.Bool
			adapter := openCodeFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/session":
					fmt.Fprint(w, `{"id":"own"}`)
				case "/event":
					w.Header().Set("Content-Type", "text/event-stream")
					openCodeEvent(w, map[string]any{"type": "server.connected"})
					for {
						select {
						case event := <-events:
							openCodeEvent(w, event)
						case <-r.Context().Done():
							return
						}
					}
				case "/session/own/message":
					_, _ = io.Copy(io.Discard, r.Body)
					events <- map[string]any{"type": "permission.asked", "properties": map[string]any{"sessionID": "own", "id": "permission"}}
					<-r.Context().Done()
				case "/permission/permission/reply":
					var body map[string]string
					_ = json.NewDecoder(r.Body).Decode(&body)
					rejected.Store(body["reply"] == "reject")
					w.WriteHeader(rejectStatus)
					fmt.Fprint(w, `{}`)
				case "/session/own/abort":
					aborted.Store(true)
					fmt.Fprint(w, `{}`)
				case "/session/own":
					deleted.Store(true)
					fmt.Fprint(w, `{}`)
				default:
					http.NotFound(w, r)
				}
			})
			_, err := adapter.Stream(context.Background(), openCodeTestModel(), "", Request{Messages: []Message{{Role: "user", Content: "Do not read files."}}}, nil)
			var typed *Error
			expected := "unsupported_capability"
			if rejectStatus != 200 {
				expected = "tool_rejection_failed"
			}
			if !errors.As(err, &typed) || typed.Code != expected || !rejected.Load() || !aborted.Load() || !deleted.Load() {
				t.Fatalf("permission: rejected=%v aborted=%v deleted=%v err=%v", rejected.Load(), aborted.Load(), deleted.Load(), err)
			}
		})
	}
}

func TestNativeOpenCodeIgnoresOtherSessionPermissions(t *testing.T) {
	var replied atomic.Bool
	adapter := openCodeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/session":
			fmt.Fprint(w, `{"id":"own"}`)
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			openCodeEvent(w, map[string]any{"type": "permission.asked", "properties": map[string]any{"sessionID": "other", "id": "permission"}})
			<-r.Context().Done()
		case "/session/own/message":
			fmt.Fprint(w, `{"parts":[{"type":"text","text":"answer"}]}`)
		case "/permission/permission/reply":
			replied.Store(true)
			fmt.Fprint(w, `{}`)
		case "/session/own":
			fmt.Fprint(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	})
	result, err := adapter.Stream(context.Background(), openCodeTestModel(), "", Request{Messages: []Message{{Role: "user", Content: "answer"}}}, nil)
	if err != nil || result.Content != "answer" || replied.Load() {
		t.Fatalf("other permission: %+v replied=%v err=%v", result, replied.Load(), err)
	}
}

func TestNativeOpenCodePreservesQuotaErrorsWithoutRawSecrets(t *testing.T) {
	adapter := openCodeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/session":
			fmt.Fprint(w, `{"id":"own"}`)
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			openCodeEvent(w, map[string]any{"type": "server.connected"})
			<-r.Context().Done()
		case "/session/own/message":
			fmt.Fprint(w, `{"info":{"error":{"data":{"message":"Free usage exceeded private-secret"}}}}`)
		case "/session/own/abort", "/session/own":
			fmt.Fprint(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	})
	_, err := adapter.Stream(context.Background(), openCodeTestModel(), "", Request{Messages: []Message{{Role: "user", Content: "test"}}}, nil)
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != "rate_limit_exceeded" || typed.Status != 429 || typed.RetryAfter != 600 || strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("quota: %v", err)
	}
}

func TestNativeOpenCodeCancellationAbortsAndDeletesSession(t *testing.T) {
	var aborted, deleted atomic.Bool
	adapter := openCodeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/session":
			fmt.Fprint(w, `{"id":"own"}`)
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			openCodeEvent(w, map[string]any{"type": "message.updated", "properties": map[string]any{"info": map[string]any{"id": "assistant", "sessionID": "own", "role": "assistant"}}})
			openCodeEvent(w, map[string]any{"type": "message.part.updated", "properties": map[string]any{"part": map[string]any{"id": "part", "messageID": "assistant", "sessionID": "own", "type": "text", "text": "partial"}}})
			<-r.Context().Done()
		case "/session/own/message":
			_, _ = io.Copy(io.Discard, r.Body)
			<-r.Context().Done()
		case "/session/own/abort":
			aborted.Store(true)
			fmt.Fprint(w, `{}`)
		case "/session/own":
			deleted.Store(true)
			fmt.Fprint(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := adapter.Stream(ctx, openCodeTestModel(), "", Request{Messages: []Message{{Role: "user", Content: "test"}}}, func(string) { cancel() })
	if !errors.Is(err, context.Canceled) || !aborted.Load() || !deleted.Load() {
		t.Fatalf("cancel: abort=%v delete=%v err=%v", aborted.Load(), deleted.Load(), err)
	}
}

func TestNativeOpenCodeRejectsUnsupportedModelsBeforeStarting(t *testing.T) {
	var definition Definition
	for _, d := range Definitions() {
		if d.ID == "opencode" {
			definition = d
		}
	}
	adapter := NewOpenCode(definition, CLIConfig{})
	defer adapter.Close()
	model := openCodeTestModel()
	_, err := adapter.Stream(context.Background(), model, "", Request{ReasoningEffort: "impossible"}, nil)
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != "unsupported_reasoning" {
		t.Fatalf("reasoning: %v", err)
	}
	model.Free = false
	_, err = adapter.Stream(context.Background(), model, "", Request{}, nil)
	if !errors.As(err, &typed) || typed.Code != "model_not_found" {
		t.Fatalf("paid: %v", err)
	}
}
