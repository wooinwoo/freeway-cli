package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixtureClient(t *testing.T, completion http.HandlerFunc) (*Client, func() string) {
	t.Helper()
	var mu sync.Mutex
	var importedKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			fmt.Fprint(w, `{"name":"Freeway","status":"ok"}`)
		case "/":
			w.Header().Set("Set-Cookie", "freeway_session=fixture; Path=/")
			fmt.Fprint(w, `{}`)
		case "/api/state":
			fmt.Fprint(w, `{"providers":[{"id":"kilo","enabled":true,"ready":true,"hasKey":false,"limits":{"hour":200},"usage":{"hour":{"requests":5}}}],"models":[{"id":"kilo/free-test","provider":"kilo","upstreamId":"free-test","name":"Test","free":true,"ready":true,"reasoning":{"parameter":"reasoning","efforts":["low","high"]}}],"settings":{},"logs":[]}`)
		case "/api/providers/kilo/sync":
			fmt.Fprint(w, `{"count":1}`)
		case "/api/providers/kilo":
			var body struct {
				Key string `json:"apiKey"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			mu.Lock()
			importedKey = body.Key
			mu.Unlock()
			fmt.Fprint(w, `{"ok":true}`)
		case "/v1/chat/completions":
			completion(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := filepath.Abs("bridge.mjs")
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{NodePath: node, BridgePath: bridge, BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	return client, func() string { mu.Lock(); defer mu.Unlock(); return importedKey }
}

func reply(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "data: {\"provider\":\"kilo\",\"model\":\"kilo/free-test\",\"choices\":[{\"delta\":{\"content\":\"안녕\"}}]}\n\ndata: [DONE]\n\n")
}

func TestClientModelsQuotaAuthenticationAndStream(t *testing.T) {
	client, imported := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model  string `json:"model"`
			Effort string `json:"reasoning_effort"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "kilo/free-test" || body.Effort != "high" {
			t.Errorf("unexpected request: %+v", body)
		}
		reply(w, r)
	})
	ctx := context.Background()
	providers, err := client.Providers(ctx)
	if err != nil || len(providers) != 11 {
		t.Fatalf("providers: %v %v", providers, err)
	}
	for _, provider := range providers {
		if provider.ID == "kilo" && (provider.Transport != "http" || provider.Protocol != "openai_compatible" || provider.RequiresCLI) {
			t.Fatalf("wrong API connection metadata: %+v", provider)
		}
		if provider.ID == "cline" && (provider.Transport != "cli" || provider.Protocol != "acp" || !provider.RequiresCLI) {
			t.Fatalf("wrong CLI connection metadata: %+v", provider)
		}
	}
	auth, err := client.Authenticate(ctx, "kilo", AuthOptions{Key: "private-test-key"})
	if err != nil || auth.Status != "configured" || auth.InferenceVerified {
		t.Fatalf("auth: %+v %v", auth, err)
	}
	if imported() != "private-test-key" {
		t.Fatal("key was not imported through stdin")
	}
	models, err := client.Models(ctx, "kilo")
	if err != nil || len(models) != 1 || len(models[0].Reasoning.Efforts) != 2 {
		t.Fatalf("models: %+v %v", models, err)
	}
	quota, err := client.Quota(ctx, "kilo")
	if err != nil || quota.Authoritative || quota.ProviderRemaining != nil || quota.LocalRemaining["hour"] != 195 {
		t.Fatalf("quota: %+v %v", quota, err)
	}
	var output strings.Builder
	result, err := client.Stream(ctx, Request{Provider: "kilo", Model: models[0].ID,
		Messages: []Message{{Role: "user", Content: "안녕"}}, ReasoningEffort: "high"}, func(text string) { output.WriteString(text) })
	if err != nil || result.Content != "안녕" || output.String() != "안녕" || result.ReasoningEffort != "high" {
		t.Fatalf("stream: %+v %v", result, err)
	}
}

func TestUnsupportedReasoningAndUnknownModelsDoNotReachInference(t *testing.T) {
	client, _ := fixtureClient(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected inference") })
	for _, tc := range []struct{ model, effort, code string }{
		{"free-test", "max", "unsupported_reasoning"}, {"paid-test", "", "model_not_found"},
	} {
		_, err := client.Complete(context.Background(), Request{Provider: "kilo", Model: tc.model, ReasoningEffort: tc.effort})
		var typed *Error
		if !errors.As(err, &typed) || typed.Code != tc.code {
			t.Errorf("expected %s, got %v", tc.code, err)
		}
	}
}

func TestCancelledStreamAbortsTheGatewayConnection(t *testing.T) {
	closed := make(chan struct{})
	client, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"provider\":\"kilo\",\"model\":\"kilo/free-test\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(closed)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := client.Stream(ctx, Request{Provider: "kilo", Model: "free-test", Messages: []Message{{Role: "user", Content: "test"}}}, func(string) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("gateway connection remained open")
	}
}

func TestBrokenStreamDoesNotReturnSuccess(t *testing.T) {
	client, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"provider\":\"kilo\",\"model\":\"kilo/free-test\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
	})
	_, err := client.Complete(context.Background(), Request{Provider: "kilo", Model: "free-test"})
	if err == nil {
		t.Fatal("incomplete stream was accepted")
	}
}

func TestStreamingQuotaErrorRemainsAnErrorAfterPartialText(t *testing.T) {
	client, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"provider\":\"kilo\",\"model\":\"kilo/free-test\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\ndata: {\"error\":{\"code\":\"rate_limit_exceeded\",\"message\":\"private-upstream-detail\"}}\n\ndata: [DONE]\n\n")
	})
	_, err := client.Complete(context.Background(), Request{Provider: "kilo", Model: "free-test"})
	var typed *Error
	if !errors.As(err, &typed) || typed.Status != 429 || typed.Code != "rate_limit_exceeded" {
		t.Fatalf("expected rate limit, got %v", err)
	}
	if strings.Contains(err.Error(), "private-upstream-detail") {
		t.Fatal("raw upstream error was exposed")
	}
}

func TestRejectsNonLocalOrCredentialBearingBaseURL(t *testing.T) {
	bridge, err := filepath.Abs("bridge.mjs")
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{"https://example.com", "http://user:pass@localhost:4141", "http://localhost:4141/v1"} {
		if _, err := New(Config{BridgePath: bridge, BaseURL: base}); err == nil {
			t.Errorf("accepted %s", base)
		}
	}
}

func TestManagedNodeIsUsedWithoutChangingTheGlobalPATH(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FREEWAY_PROVIDER_HOME", root)
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"schemaVersion": 1, "node": map[string]string{"path": node}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "install.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	bridge, err := filepath.Abs("bridge.mjs")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")
	client, err := New(Config{BridgePath: bridge})
	if err != nil {
		t.Fatal(err)
	}
	if client.config.NodePath != node {
		t.Fatalf("managed node not selected: %s", client.config.NodePath)
	}
}

type loginOutput struct {
	buffer string
	ready  chan string
	once   sync.Once
}

func (w *loginOutput) Write(data []byte) (int, error) {
	w.buffer += string(data)
	if strings.Contains(w.buffer, "\n") {
		w.once.Do(func() { w.ready <- strings.TrimSpace(w.buffer) })
	}
	return len(data), nil
}

func TestLoginCancellationStopsTheOfficialCLIChild(t *testing.T) {
	dir := t.TempDir()
	bridge := filepath.Join(dir, "bridge.mjs")
	if err := os.WriteFile(bridge, nil, 0600); err != nil {
		t.Fatal(err)
	}
	heartbeat := filepath.Join(dir, "heartbeat")
	if err := os.WriteFile(heartbeat, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FREEWAY_TEST_HEARTBEAT", heartbeat)
	script := `import {spawn} from 'node:child_process';
spawn(process.execPath, ['--input-type=module', '-e', "import{appendFileSync}from'node:fs';console.log('READY '+process.pid);setInterval(()=>appendFileSync(process.argv[1],'x'),20);", process.env.FREEWAY_TEST_HEARTBEAT], {stdio:['ignore','inherit','inherit']});`
	if err := os.WriteFile(filepath.Join(dir, "manage.mjs"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{BridgePath: bridge})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output := &loginOutput{ready: make(chan string, 1)}
	finished := make(chan error, 1)
	go func() { finished <- client.Login(ctx, "cline", strings.NewReader(""), output, io.Discard) }()
	select {
	case ready := <-output.ready:
		fields := strings.Fields(ready)
		if len(fields) != 2 {
			t.Fatalf("invalid readiness: %q", ready)
		}
		pid, err := strconv.Atoi(fields[1])
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if process, err := os.FindProcess(pid); err == nil {
				_ = process.Kill()
			}
		})
	case <-ctx.Done():
		t.Fatal("login child did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("login retained the child's output pipes")
	}
	before, err := os.ReadFile(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	after, err := os.ReadFile(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("official CLI child kept running after cancellation")
	}
}
