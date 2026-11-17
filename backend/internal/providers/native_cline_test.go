package providers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type clineRoundTrip func(*http.Request) (*http.Response, error)

func (f clineRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func nativeClineFixture(t *testing.T, mode string) *clineAdapter {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	settings := filepath.Join(home, ".cline", "data", "settings")
	if err := os.MkdirAll(settings, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settings, "providers.json"), []byte(`{"providers":{"cline":{"settings":{"auth":{"accessToken":"fixture-token"}}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	var definition Definition
	for _, d := range Definitions() {
		if d.ID == "cline" {
			definition = d
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: clineRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != clineCatalogURL || request.Method != http.MethodGet {
			t.Errorf("unexpected catalog request: %s %s", request.Method, request.URL)
		}
		if _, ok := request.Context().Deadline(); !ok {
			t.Error("catalog request has no deadline")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{
"free":[{"id":"free-test","name":"Free test"},{"id":"free-other"},{"id":"explicit-paid","isFree":false},{}],
"recommended":[{"id":"paid-recommended"}],"clinePass":[{"id":"paid-pass"}]}`))}, nil
	})}
	a := NewCline(definition, CLIConfig{Binary: executable, WorkDir: filepath.Join(t.TempDir(), "requests"), HTTPClient: client}).(*clineAdapter)
	a.command = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, executable, append([]string{"-test.run=^TestNativeClineACPHelperProcess$", "--"}, args...)...)
	}
	t.Setenv("FREEWAY_TEST_CLINE_HELPER", "1")
	t.Setenv("FREEWAY_TEST_CLINE_MODE", mode)
	t.Setenv("FREEWAY_TEST_CLINE_TRACE", filepath.Join(t.TempDir(), "trace.json"))
	t.Setenv("FREEWAY_TEST_CLINE_HEARTBEAT", filepath.Join(t.TempDir(), "heartbeat"))
	t.Setenv("CLINE_API_KEY", "must-not-reach-official-cli")
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func nativeClineModel(t *testing.T, a *clineAdapter) Model {
	t.Helper()
	models, err := a.Models(context.Background(), "")
	if err != nil || len(models) != 2 {
		t.Fatalf("models=%+v err=%v", models, err)
	}
	return models[0]
}

func nativeClineRequest(model Model) Request {
	return Request{Provider: "cline", Model: model.UpstreamID,
		Messages: []Message{{Role: "system", Content: "Text only."}, {Role: "user", Content: "안녕 --model 유니코드 질문"}}}
}

func TestNativeClineModelsRequireLoginAndOnlyAdmitTheOfficialFreeArray(t *testing.T) {
	a := nativeClineFixture(t, "normal")
	models, err := a.Models(context.Background(), "")
	if err != nil || len(models) != 2 || models[0].ID != "cline/free-test" || !models[0].Free || len(models[0].Reasoning.Efforts) != 0 {
		t.Fatalf("unexpected models=%+v err=%v", models, err)
	}
	home, _ := os.UserHomeDir()
	if err := os.WriteFile(filepath.Join(home, ".cline", "data", "settings", "providers.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = a.Models(context.Background(), "")
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != "cli_login_required" {
		t.Fatalf("expected a login requirement, got %v", err)
	}
}

func TestNativeClineSelectsModelBeforeUnicodePromptAndDeniesAllTools(t *testing.T) {
	a := nativeClineFixture(t, "normal")
	model := nativeClineModel(t, a)
	var streamed strings.Builder
	result, err := a.Stream(context.Background(), model, "", nativeClineRequest(model), func(text string) { streamed.WriteString(text) })
	if err != nil || result.Content != "안녕 세상" || streamed.String() != result.Content || result.Model != model.ID || result.Provider != "cline" {
		t.Fatalf("completion=%+v streamed=%q err=%v", result, streamed.String(), err)
	}
	var trace struct {
		Args       []string `json:"args"`
		Methods    []string `json:"methods"`
		Prompt     string   `json:"prompt"`
		Model      string   `json:"model"`
		Permission bool     `json:"permission"`
		Tools      bool     `json:"tools"`
	}
	data, err := os.ReadFile(os.Getenv("FREEWAY_TEST_CLINE_TRACE"))
	if err != nil || json.Unmarshal(data, &trace) != nil {
		t.Fatalf("trace read: %v", err)
	}
	if strings.Join(trace.Methods, ",") != "initialize,session/new,session/set_model,session/prompt" || trace.Model != model.UpstreamID || !trace.Permission || !trace.Tools {
		t.Fatalf("unsafe ACP exchange: %+v", trace)
	}
	if !strings.Contains(trace.Prompt, "[user]\n안녕 --model 유니코드 질문") || strings.Contains(strings.Join(trace.Args, " "), "유니코드") {
		t.Fatal("conversation did not travel exclusively through stdin")
	}
	if !strings.Contains(strings.Join(trace.Args, " "), "--auto-approve false") || !strings.Contains(strings.Join(trace.Args, " "), "--hooks-dir") {
		t.Fatalf("missing safe CLI flags: %v", trace.Args)
	}
	files, err := os.ReadDir(a.config.WorkDir)
	if err != nil || len(files) != 0 {
		t.Fatalf("request workspace was not removed: %v %v", files, err)
	}
}

func TestNativeClineRejectsReasoningAndUnlistedModelsBeforeExecution(t *testing.T) {
	a := nativeClineFixture(t, "normal")
	a.command = func(context.Context, string, ...string) *exec.Cmd { t.Fatal("CLI unexpectedly started"); return nil }
	_, err := a.Stream(context.Background(), Model{}, "", Request{ReasoningEffort: "high"}, nil)
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != "unsupported_reasoning" {
		t.Fatalf("expected unsupported reasoning, got %v", err)
	}
	if _, err := os.Stat(a.config.WorkDir); !os.IsNotExist(err) {
		t.Fatalf("reasoning rejection created a workspace: %v", err)
	}
	model := nativeClineModel(t, a)
	model.ID, model.UpstreamID = "cline/paid", "paid"
	_, err = a.Stream(context.Background(), model, "", nativeClineRequest(model), nil)
	if !errors.As(err, &typed) || typed.Code != "not_free_model" {
		t.Fatalf("expected free catalog rejection, got %v", err)
	}
}

func TestNativeClineFailuresNeverTurnPartialTextIntoSuccessOrExposeSecrets(t *testing.T) {
	for _, tc := range []struct {
		mode, code string
		status     int
	}{
		{"quota", "rate_limit_exceeded", 429}, {"auth", "cli_login_required", 401},
		{"truncated", "incomplete_stream", 502}, {"invalid", "invalid_stream", 502}, {"bad-finish", "cli_error", 502},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			a := nativeClineFixture(t, tc.mode)
			model := nativeClineModel(t, a)
			_, err := a.Stream(context.Background(), model, "", nativeClineRequest(model), nil)
			var typed *Error
			if !errors.As(err, &typed) || typed.Code != tc.code || typed.Status != tc.status || strings.Contains(err.Error(), "private-secret-detail") {
				t.Fatalf("expected %s/%d, got %v", tc.code, tc.status, err)
			}
			if tc.mode == "quota" && typed.RetryAfter != 600 {
				t.Fatalf("quota retry time was lost: %+v", typed)
			}
		})
	}
}

func TestNativeClineCancellationStopsTheChildBeforeRemovingItsWorkspace(t *testing.T) {
	for _, mode := range []string{"hold", "descendant"} {
		t.Run(mode, func(t *testing.T) { nativeClineCancellation(t, mode) })
	}
}

func nativeClineCancellation(t *testing.T, mode string) {
	a := nativeClineFixture(t, mode)
	model := nativeClineModel(t, a)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := a.Stream(ctx, model, "", nativeClineRequest(model), func(string) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	files, err := os.ReadDir(a.config.WorkDir)
	if err != nil || len(files) != 0 {
		t.Fatalf("workspace survived cancellation: %v %v", files, err)
	}
	heartbeat := os.Getenv("FREEWAY_TEST_CLINE_HEARTBEAT")
	before, err := os.ReadFile(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	after, err := os.ReadFile(heartbeat)
	if err != nil || string(before) != string(after) {
		t.Fatal("CLI still runs after Stream returned")
	}
}

func TestNativeClineCloseStopsAndWaitsForItsActiveRequest(t *testing.T) {
	a := nativeClineFixture(t, "hold")
	model := nativeClineModel(t, a)
	ready := make(chan struct{})
	finished := make(chan error, 1)
	var once sync.Once
	go func() {
		_, err := a.Stream(context.Background(), model, "", nativeClineRequest(model), func(string) { once.Do(func() { close(ready) }) })
		finished <- err
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("CLI did not start")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("interrupted request succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close retained an active request")
	}
	files, err := os.ReadDir(a.config.WorkDir)
	if err != nil || len(files) != 0 {
		t.Fatal("Close returned before workspace cleanup")
	}
}

func TestNativeClineACPHelperProcess(t *testing.T) {
	if os.Getenv("FREEWAY_TEST_CLINE_HELPER") != "1" {
		return
	}
	if os.Getenv("FREEWAY_TEST_CLINE_DESCENDANT") == "1" {
		signal.Ignore(syscall.SIGTERM)
		heartbeat, _ := os.OpenFile(os.Getenv("FREEWAY_TEST_CLINE_HEARTBEAT"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		for {
			_, _ = heartbeat.WriteString("x")
			time.Sleep(20 * time.Millisecond)
		}
	}
	if os.Getenv("CLINE_API_KEY") != "" || os.Getenv("CLINE_PROVIDER") != "cline" {
		os.Exit(91)
	}
	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	trace := struct {
		Args       []string `json:"args"`
		Methods    []string `json:"methods"`
		Prompt     string   `json:"prompt"`
		Model      string   `json:"model"`
		Permission bool     `json:"permission"`
		Tools      bool     `json:"tools"`
	}{Args: args}
	save := func() {
		data, _ := json.Marshal(trace)
		_ = os.WriteFile(os.Getenv("FREEWAY_TEST_CLINE_TRACE"), data, 0600)
	}
	encoder := json.NewEncoder(os.Stdout)
	send := func(item any) { _ = encoder.Encode(item) }
	chunk := func(session, text string) {
		send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": session, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}}}})
	}
	finish := func() {
		save()
		chunk("other-session", "must-not-leak")
		chunk("session", "안녕 ")
		chunk("session", "세상")
		send(map[string]any{"jsonrpc": "2.0", "id": 4, "result": map[string]any{"stopReason": "end_turn"}})
		os.Exit(0)
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 2*1024*1024)
	for scanner.Scan() {
		var request struct {
			ID     int                        `json:"id"`
			Method string                     `json:"method"`
			Params map[string]json.RawMessage `json:"params"`
			Result json.RawMessage            `json:"result"`
			Error  struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(92)
		}
		if request.Method != "" {
			trace.Methods = append(trace.Methods, request.Method)
		}
		switch request.Method {
		case "initialize":
			send(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"protocolVersion": 1}})
		case "session/new":
			send(map[string]any{"jsonrpc": "2.0", "id": 2, "result": map[string]any{"sessionId": "session"}})
		case "session/set_model":
			_ = json.Unmarshal(request.Params["modelId"], &trace.Model)
			save()
			send(map[string]any{"jsonrpc": "2.0", "id": 3, "result": map[string]any{}})
		case "session/prompt":
			var prompt []struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(request.Params["prompt"], &prompt)
			if len(prompt) != 1 || trace.Model != "free-test" {
				os.Exit(93)
			}
			trace.Prompt = prompt[0].Text
			save()
			switch os.Getenv("FREEWAY_TEST_CLINE_MODE") {
			case "quota", "auth":
				message := "free usage limit private-secret-detail"
				if os.Getenv("FREEWAY_TEST_CLINE_MODE") == "auth" {
					message = "login required private-secret-detail"
				}
				chunk("session", "partial")
				send(map[string]any{"jsonrpc": "2.0", "id": 4, "error": map[string]any{"code": -32000, "message": message}})
				os.Exit(0)
			case "truncated":
				chunk("session", "partial")
				os.Exit(0)
			case "invalid":
				fmt.Println("not-json")
				time.Sleep(time.Hour)
			case "bad-finish":
				chunk("session", "partial")
				send(map[string]any{"jsonrpc": "2.0", "id": 4, "result": map[string]any{"stopReason": "cancelled"}})
				os.Exit(0)
			case "descendant":
				executable, _ := os.Executable()
				child := exec.Command(executable, "-test.run=^TestNativeClineACPHelperProcess$")
				child.Env = append(os.Environ(), "FREEWAY_TEST_CLINE_DESCENDANT=1")
				child.Stdout, child.Stderr = os.Stdout, os.Stderr
				if child.Start() != nil {
					os.Exit(95)
				}
				for i := 0; i < 100; i++ {
					if _, err := os.Stat(os.Getenv("FREEWAY_TEST_CLINE_HEARTBEAT")); err == nil {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				chunk("session", "partial")
				time.Sleep(time.Hour)
			case "hold":
				heartbeat, _ := os.OpenFile(os.Getenv("FREEWAY_TEST_CLINE_HEARTBEAT"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
				_, _ = heartbeat.WriteString("x")
				chunk("session", "partial")
				for {
					time.Sleep(20 * time.Millisecond)
					_, _ = heartbeat.WriteString("x")
				}
			default:
				send(map[string]any{"jsonrpc": "2.0", "id": 77, "method": "session/request_permission", "params": map[string]any{"sessionId": "session"}})
				send(map[string]any{"jsonrpc": "2.0", "id": 78, "method": "fs/read_text_file", "params": map[string]any{"path": "private-file"}})
			}
		default:
			if request.ID == 77 {
				trace.Permission = strings.Contains(string(request.Result), `"cancelled"`)
			}
			if request.ID == 78 {
				trace.Tools = request.Error.Code == -32601
				finish()
			}
		}
	}
	os.Exit(94)
}
