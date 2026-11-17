package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func nativeHTTPFixture(t *testing.T, handler http.HandlerFunc) (Adapter, Model) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	d := Definition{Provider: Provider{ID: "kilo"}, BaseURL: server.URL, Catalog: true, Proof: "catalog-zero"}
	model := Model{ID: "kilo/free-test", Provider: "kilo", UpstreamID: "free-test", Free: true, Ready: true,
		Reasoning: Reasoning{Parameter: "reasoning", Efforts: []string{"low", "high"}}}
	return NewHTTPAdapter(d, server.Client()), model
}

func nativeHTTPRequest(model Model) Request {
	return Request{Provider: model.Provider, Model: model.ID, Messages: []Message{{Role: "user", Content: "안녕하세요"}}}
}

func assertNativeHTTPError(t *testing.T, err error, code string, status int) *Error {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != code || typed.Status != status {
		t.Fatalf("wanted %s/%d; received %v", code, status, err)
	}
	return typed
}

func TestNativeHTTPCatalogUsesBearerAndRejectsPaidUnknownAndDisabledModels(t *testing.T) {
	a, _ := nativeHTTPFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" || r.Header.Get("Authorization") != "Bearer example-key" {
			t.Errorf("unexpected catalog request: %s / %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		fmt.Fprint(w, `{"data":[
{"id":"free","pricing":{"prompt":"0","completion":"0"},"supported_parameters":["reasoning"],"opencode":{"reasoning_options":[{"type":"effort","values":["low","high"]}]}},
{"id":"explicit-paid","isFree":false,"pricing":{"prompt":"0","completion":"0"}},
{"id":"paid","cost":{"input":1,"output":0}},
{"id":"unknown"},
{"id":"deprecated","status":"deprecated","cost":{"input":0,"output":0}},
{"id":"disabled","disabled":true,"cost":{"input":0,"output":0}},
{"id":"image","architecture":{"modality":"image->image"},"cost":{"input":0,"output":0}}
]}`)
	})
	models, err := a.Models(context.Background(), "example-key")
	if err != nil || len(models) != 1 || models[0].ID != "kilo/free" || models[0].Reasoning.Parameter != "reasoning" || len(models[0].Reasoning.Efforts) != 2 {
		t.Fatalf("catalog: %+v / %v", models, err)
	}
}

func TestNativeHTTPSeedDiscoveryNeedsNoKeyAndRemoteDiscoveryRequiresOne(t *testing.T) {
	var called atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called.Add(1) }))
	defer server.Close()
	d := Definition{Provider: Provider{ID: "zai"}, BaseURL: server.URL, Seed: []string{"free-flash"}, Proof: "allowlist"}
	models, err := NewHTTPAdapter(d, server.Client()).Models(context.Background(), "")
	if err != nil || len(models) != 1 || called.Load() != 0 {
		t.Fatalf("seed discovery: %+v / %v / %d calls", models, err, called.Load())
	}
	d.Catalog = true
	_, err = NewHTTPAdapter(d, server.Client()).Models(context.Background(), "")
	assertNativeHTTPError(t, err, "needs_key", 409)
	if called.Load() != 0 {
		t.Fatal("missing key reached the remote catalog")
	}
}

func TestNativeHTTPStreamsExactModelAndBothReasoningParameters(t *testing.T) {
	for _, parameter := range []string{"reasoning", "reasoning_effort"} {
		t.Run(parameter, func(t *testing.T) {
			a, model := nativeHTTPFixture(t, func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Fatal("invalid request body")
				}
				if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer example-key" || body["model"] != "free-test" || body["max_tokens"] != float64(4096) || body["temperature"] != float64(0) || body["stream"] != true {
					t.Errorf("unexpected upstream request: %+v", body)
				}
				if parameter == "reasoning" {
					if body["reasoning"].(map[string]any)["effort"] != "high" || body["reasoning_effort"] != nil {
						t.Errorf("wrong native reasoning: %+v", body)
					}
				} else if body["reasoning_effort"] != "high" || body["reasoning"] != nil {
					t.Errorf("wrong reasoning_effort: %+v", body)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"안녕\"}}]}\r\n\r\ndata: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"하세요\"}}],\"usage\":{\"total_tokens\":9,\"cost\":0}}\r\n\r\ndata: [DONE]\r\n\r\n")
			})
			model.Reasoning.Parameter = parameter
			request := nativeHTTPRequest(model)
			temperature := 0.0
			request.MaxTokens, request.ReasoningEffort, request.Temperature = 4096, "high", &temperature
			var streamed strings.Builder
			result, err := a.Stream(context.Background(), model, "example-key", request, func(text string) { streamed.WriteString(text) })
			if err != nil || result.Content != "안녕하세요" || streamed.String() != result.Content || result.Model != model.ID || result.Provider != "kilo" || result.ReasoningEffort != "high" || !strings.Contains(string(result.Usage), `"cost":0`) {
				t.Fatalf("stream: %+v / %s / %v", result, streamed.String(), err)
			}
		})
	}
}

func TestNativeHTTPRejectsInvalidRouteAndReasoningBeforeSending(t *testing.T) {
	var called atomic.Int32
	a, model := nativeHTTPFixture(t, func(http.ResponseWriter, *http.Request) { called.Add(1) })
	for _, tc := range []struct {
		name string
		edit func(*Model, *Request)
		code string
	}{
		{"paid", func(m *Model, _ *Request) { m.Free = false }, "not_free_model"},
		{"foreign provider", func(m *Model, _ *Request) { m.Provider = "other" }, "unexpected_route"},
		{"foreign ID", func(m *Model, _ *Request) { m.ID = "other/free-test" }, "unexpected_route"},
		{"unsupported reasoning", func(_ *Model, r *Request) { r.ReasoningEffort = "max" }, "unsupported_reasoning"},
		{"unsupported role", func(_ *Model, r *Request) { r.Messages[0].Role = "tool" }, "invalid_role"},
		{"invalid max_tokens", func(_ *Model, r *Request) { r.MaxTokens = -1 }, "invalid_parameter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := model
			r := nativeHTTPRequest(model)
			tc.edit(&m, &r)
			_, err := a.Stream(context.Background(), m, "", r, nil)
			assertNativeHTTPError(t, err, tc.code, 400)
		})
	}
	if called.Load() != 0 {
		t.Fatal("validation reached upstream inference")
	}
}

func TestNativeHTTPTruncatedAndErrorStreamsDoNotReturnSuccess(t *testing.T) {
	for _, tc := range []struct {
		name, stream, code string
		status             int
	}{
		{"truncated", "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n", "incomplete_stream", 502},
		{"stream error", "data: {\"error\":{\"code\":\"private-error\",\"message\":\"do-not-expose-this-secret\"}}\n\n", "upstream_stream_error", 502},
		{"quota error", "data: {\"error\":{\"code\":\"rate_limit_exceeded\"}}\n\n", "rate_limit_exceeded", 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, model := nativeHTTPFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, tc.stream)
			})
			_, err := a.Stream(context.Background(), model, "", nativeHTTPRequest(model), nil)
			assertNativeHTTPError(t, err, tc.code, tc.status)
			if strings.Contains(err.Error(), "do-not-expose-this-secret") {
				t.Fatal("raw upstream error escaped")
			}
		})
	}
}

func TestNativeHTTPRateLimitPreservesRetryAfterWithoutExposingBody(t *testing.T) {
	a, model := nativeHTTPFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "1.2")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"do-not-expose-this-secret"}}`)
	})
	_, err := a.Stream(context.Background(), model, "", nativeHTTPRequest(model), nil)
	typed := assertNativeHTTPError(t, err, "rate_limit_exceeded", 429)
	if typed.RetryAfter != 2 || strings.Contains(err.Error(), "do-not-expose-this-secret") {
		t.Fatalf("rate limit: %+v", typed)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if got := httpRetryAfter(now.Add(10*time.Second).Format(http.TimeFormat), now); got != 10 {
		t.Fatalf("HTTP date delay: %d", got)
	}
	for _, value := range []string{"", "NaN", "Inf", "-1", "not-a-date"} {
		if httpRetryAfter(value, now) != 0 {
			t.Errorf("accepted invalid retry delay %q", value)
		}
	}
}

func TestNativeHTTPAcceptsJSONFallbackAndKeepsMissingUsageUnknown(t *testing.T) {
	a, model := nativeHTTPFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`)
	})
	result, err := a.Stream(context.Background(), model, "", nativeHTTPRequest(model), nil)
	if err != nil || result.Content != "hello" || result.Usage != nil {
		t.Fatalf("JSON fallback: %+v / %v", result, err)
	}
}

func TestNativeHTTPCancellationClosesTheUpstreamRequest(t *testing.T) {
	closed := make(chan struct{})
	a, model := nativeHTTPFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(closed)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := a.Stream(ctx, model, "", nativeHTTPRequest(model), func(string) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("wanted cancellation; got %v", err)
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream request remained open")
	}
}
