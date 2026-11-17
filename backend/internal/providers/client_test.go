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
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("FREEWAY_TEST_LOGIN_HELPER") == "1" {
		if len(os.Args) != 3 || os.Args[1] != "auth" || os.Args[2] != "cline" {
			os.Exit(81)
		}
		fmt.Fprintln(os.Stdout, "READY")
		for {
			time.Sleep(time.Hour)
		}
	}
	os.Exit(m.Run())
}

type clientLoginOutput struct {
	ready chan struct{}
	once  sync.Once
	text  string
}

func (w *clientLoginOutput) Write(data []byte) (int, error) {
	w.text += string(data)
	if strings.Contains(w.text, "READY\n") {
		w.once.Do(func() { close(w.ready) })
	}
	return len(data), nil
}

type clientCredentials struct {
	mu               sync.Mutex
	values           map[string]Credential
	loadErr, saveErr error
}

func (s *clientCredentials) Load(_ context.Context, id string) (Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[id], s.loadErr
}
func (s *clientCredentials) Save(_ context.Context, id string, value Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	s.values[id] = value
	return nil
}

type clientFixture struct {
	client             *Client
	catalog, inference atomic.Int32
	lastKey            atomic.Value
}

func newClientFixture(t *testing.T, credentials CredentialStore, complete http.HandlerFunc) *clientFixture {
	t.Helper()
	f := &clientFixture{}
	f.lastKey.Store("")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.lastKey.Store(r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/models":
			f.catalog.Add(1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"free-test","name":"Test","pricing":{"prompt":"0","completion":"0"},"supported_parameters":["reasoning"],"opencode":{"reasoning_options":[{"type":"effort","values":["low","high"]}]}},{"id":"paid-test","isFree":false,"pricing":{"prompt":"0","completion":"0"}}]}`)
		case "/chat/completions":
			f.inference.Add(1)
			complete(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{HTTPClient: server.Client(), Credentials: credentials})
	if err != nil {
		t.Fatal(err)
	}
	f.client = client
	d := client.definitions["kilo"]
	d.BaseURL = server.URL
	client.definitions["kilo"] = d
	client.adapters["kilo"] = NewHTTPAdapter(d, server.Client())
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	return f
}
func clientReply(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"안녕\"}}]}\n\ndata: [DONE]\n\n")
}
func clientRequest() Request {
	return Request{Provider: "kilo", Model: "free-test", Messages: []Message{{Role: "user", Content: "안녕"}}}
}
func clientError(t *testing.T, err error, code string) *Error {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("wanted %s, got %v", code, err)
	}
	return typed
}
func TestNativeClientListsProvidersWithoutNodeOrNetwork(t *testing.T) {
	t.Setenv("PATH", "")
	f := newClientFixture(t, nil, clientReply)
	listed, err := f.client.Providers(context.Background())
	if err != nil || len(listed) != 11 || f.catalog.Load() != 0 || f.inference.Load() != 0 {
		t.Fatalf("metadata: %+v / %v", listed, err)
	}
	var apiCount, cliCount int
	for _, p := range listed {
		if p.Transport == "http" && p.Protocol == "openai_compatible" && !p.RequiresCLI {
			apiCount++
		}
		if p.RequiresCLI && p.Transport == "cli" {
			cliCount++
		}
	}
	if apiCount != 9 || cliCount != 2 {
		t.Fatalf("metadata: HTTP=%d CLI=%d", apiCount, cliCount)
	}
}
func TestNativeClientAuthenticatesAndStreamsExactModelAndReasoning(t *testing.T) {
	store := &clientCredentials{values: map[string]Credential{}}
	f := newClientFixture(t, store, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model     string `json:"model"`
			Reasoning struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "free-test" || body.Reasoning.Effort != "high" {
			t.Errorf("request changed: %+v", body)
		}
		clientReply(w, r)
	})
	auth, err := f.client.Authenticate(context.Background(), "kilo", AuthOptions{Key: "private-example-key"})
	if err != nil || !auth.Ready || auth.InferenceVerified || auth.ModelCount != 1 || f.inference.Load() != 0 {
		t.Fatalf("auth: %+v / %v", auth, err)
	}
	credential, _ := store.Load(context.Background(), "kilo")
	if credential.Key != "private-example-key" || f.lastKey.Load() != "Bearer private-example-key" {
		t.Fatal("backend-owned credential not used")
	}
	encoded, _ := json.Marshal(struct {
		Auth       AuthResult
		Credential Credential
	}{auth, credential})
	if strings.Contains(string(encoded), "private-example-key") {
		t.Fatal("public JSON exposed credential")
	}
	request := clientRequest()
	request.ReasoningEffort = "high"
	var output strings.Builder
	result, err := f.client.Stream(context.Background(), request, func(text string) { output.WriteString(text) })
	if err != nil || result.Model != "kilo/free-test" || result.Provider != "kilo" || result.ReasoningEffort != "high" || result.Content != "안녕" || output.String() != "안녕" {
		t.Fatalf("stream: %+v / %v", result, err)
	}
	if f.catalog.Load() != 2 || f.inference.Load() != 1 {
		t.Fatal("catalog not refreshed once or inference retried")
	}
}
func TestNativeClientBindsFreeConfirmationToBackendCredential(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "")
	store := &clientCredentials{values: map[string]Credential{"groq": {Key: "old-key", FreeConfirmed: true}}}
	f := newClientFixture(t, store, clientReply)
	auth, err := f.client.Authenticate(context.Background(), "groq", AuthOptions{Key: "new-key"})
	if err != nil || auth.Ready || auth.Reason != "confirm_free_account" {
		t.Fatalf("replacement: %+v / %v", auth, err)
	}
	value, _ := store.Load(context.Background(), "groq")
	if value.Key != "new-key" || value.FreeConfirmed {
		t.Fatal("old confirmation survived replacement")
	}
	auth, err = f.client.Authenticate(context.Background(), "groq", AuthOptions{ConfirmFree: true})
	if err != nil || !auth.Ready || auth.InferenceVerified {
		t.Fatalf("confirmation: %+v / %v", auth, err)
	}
	auth, err = f.client.Authenticate(context.Background(), "groq", AuthOptions{Key: "third-key", ConfirmFree: true})
	value, _ = store.Load(context.Background(), "groq")
	if err != nil || !auth.Ready || value.Key != "third-key" || !value.FreeConfirmed {
		t.Fatalf("replacement confirmation: %+v / %v", auth, err)
	}
	if f.inference.Load() != 0 {
		t.Fatal("credential setup performed inference")
	}
}
func TestNativeClientEnvironmentKeysNeedOwnFreeConfirmation(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "environment-key")
	f := newClientFixture(t, nil, clientReply)
	auth, err := f.client.Authenticate(context.Background(), "groq", AuthOptions{})
	if err != nil || auth.Ready || auth.Reason != "confirm_free_account" {
		t.Fatalf("silent confirmation: %+v / %v", auth, err)
	}
	auth, err = f.client.Authenticate(context.Background(), "groq", AuthOptions{ConfirmFree: true})
	if err != nil || !auth.Ready {
		t.Fatalf("confirmation: %+v / %v", auth, err)
	}
	other := newClientFixture(t, nil, clientReply)
	auth, err = other.client.Authenticate(context.Background(), "groq", AuthOptions{})
	if err != nil || auth.Ready {
		t.Fatal("default in-memory credentials leaked into another client")
	}
}
func TestNativeClientCredentialErrorsAndInvalidKeysDoNotLeakSecrets(t *testing.T) {
	for _, store := range []*clientCredentials{{values: map[string]Credential{}, loadErr: errors.New("private-store-detail")}, {values: map[string]Credential{}, saveErr: errors.New("private-store-detail")}} {
		f := newClientFixture(t, store, clientReply)
		_, err := f.client.Authenticate(context.Background(), "kilo", AuthOptions{Key: "private-example-key"})
		clientError(t, err, "credential_store_failed")
		if strings.Contains(err.Error(), "private") || f.inference.Load() != 0 {
			t.Fatal("store detail escaped or performed inference")
		}
	}
	f := newClientFixture(t, nil, clientReply)
	for _, key := range []string{"   ", "bad\nkey", "bad\x1bkey", strings.Repeat("x", 4097)} {
		_, err := f.client.Authenticate(context.Background(), "kilo", AuthOptions{Key: key})
		clientError(t, err, "invalid_key")
	}
	_, err := f.client.Authenticate(context.Background(), "cline", AuthOptions{Key: "private-example-key"})
	clientError(t, err, "cli_login_required")
	if f.catalog.Load() != 0 || f.inference.Load() != 0 {
		t.Fatal("invalid credentials reached vendor")
	}
}
func TestNativeClientRejectsPaidUnknownAndForeignModelsBeforeInference(t *testing.T) {
	f := newClientFixture(t, nil, clientReply)
	for _, tc := range []struct{ model, effort, code string }{{"paid-test", "", "model_not_found"}, {"other/free-test", "", "model_not_found"}, {"unknown", "", "model_not_found"}, {"free-test", "max", "unsupported_reasoning"}} {
		request := clientRequest()
		request.Model, request.ReasoningEffort = tc.model, tc.effort
		_, err := f.client.Complete(context.Background(), request)
		clientError(t, err, tc.code)
	}
	if f.inference.Load() != 0 {
		t.Fatal("ineligible route reached inference")
	}
}
func TestNativeClientQuotaDoesNotInventAccountingOrMakeRequests(t *testing.T) {
	f := newClientFixture(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "1.2")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	quota, err := f.client.Quota(context.Background(), "kilo")
	if err != nil || quota.Source != "provider_adapter" || quota.Authoritative || quota.ProviderRemaining != nil || quota.Limits != nil || quota.Usage != nil || quota.LocalRemaining != nil || quota.LocalTokenRemaining != nil || quota.ObservedAt.IsZero() {
		t.Fatalf("fabricated quota: %+v / %v", quota, err)
	}
	if f.catalog.Load() != 0 || f.inference.Load() != 0 {
		t.Fatal("quota made vendor request")
	}
	_, err = f.client.Complete(context.Background(), clientRequest())
	typed := clientError(t, err, "rate_limit_exceeded")
	if typed.RetryAfter != 2 {
		t.Fatalf("retry delay: %+v", typed)
	}
	catalog, inference := f.catalog.Load(), f.inference.Load()
	quota, err = f.client.Quota(context.Background(), "kilo")
	var observed observedFailure
	if err != nil || json.Unmarshal(quota.LastFailure, &observed) != nil || observed.Code != typed.Code || observed.Status != 429 || observed.RetryAfter != 2 {
		t.Fatalf("lost observation: %+v / %v", quota, err)
	}
	if catalog != f.catalog.Load() || inference != f.inference.Load() || quota.Usage != nil {
		t.Fatal("quota performed requests or fabricated counts")
	}
}
func TestNativeClientPartialQuotaErrorAndTruncatedStreamRemainFailures(t *testing.T) {
	for _, tc := range []struct{ terminal, code string }{{"data: {\"error\":{\"code\":\"rate_limit_exceeded\",\"message\":\"private-upstream-detail\"}}\n\ndata: [DONE]\n\n", "rate_limit_exceeded"}, {"", "incomplete_stream"}} {
		f := newClientFixture(t, nil, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"+tc.terminal)
		})
		var output strings.Builder
		_, err := f.client.Stream(context.Background(), clientRequest(), func(text string) { output.WriteString(text) })
		clientError(t, err, tc.code)
		if output.String() != "partial" || strings.Contains(err.Error(), "private-upstream-detail") || f.inference.Load() != 1 {
			t.Fatal("partial output hid failure, leaked detail or caused retry")
		}
	}
}
func TestNativeClientCloseCancelsInflightAndPreservesCallerWorkspace(t *testing.T) {
	started, closed := make(chan struct{}), make(chan struct{})
	f := newClientFixture(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(closed)
	})
	workspace := f.client.workDir
	finished := make(chan error, 1)
	go func() {
		_, err := f.client.Stream(context.Background(), clientRequest(), func(string) { close(started) })
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	if err := f.client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("close did not cancel: %v", err)
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream remained open")
	}
	if _, err := os.Stat(workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("temporary workspace retained")
	}
	if _, err := f.client.Complete(context.Background(), clientRequest()); err == nil {
		t.Fatal("closed client accepted call")
	}
	workDir := t.TempDir()
	marker := filepath.Join(workDir, "user-file")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{WorkDir: workDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "keep" {
		t.Fatal("caller workspace deleted")
	}
}
func TestNativeCLIManagedDiscoveryAndOverridesNeedNoNode(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FREEWAY_PROVIDER_HOME", root)
	t.Setenv("PATH", "")
	t.Setenv("OPENCODE_BIN", "")
	managed, override := filepath.Join(root, "managed-opencode"), filepath.Join(root, "override-opencode")
	for _, path := range []string{managed, override} {
		if err := os.WriteFile(path, []byte("fixture"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := json.Marshal(map[string]any{"schemaVersion": 1, "tools": map[string]any{"opencode": map[string]string{"path": managed}}})
	if err := os.WriteFile(filepath.Join(root, "install.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	path, err := CLIExecutable("opencode")
	if err != nil || path != managed {
		t.Fatalf("managed path: %s / %v", path, err)
	}
	t.Setenv("OPENCODE_BIN", override)
	path, err = CLIExecutable("opencode")
	if err != nil || path != override {
		t.Fatalf("override path: %s / %v", path, err)
	}
	t.Setenv("OPENCODE_BIN", filepath.Join(root, "missing"))
	_, err = CLIExecutable("opencode")
	clientError(t, err, "cli_not_installed")
}

func TestNativeClientCloseCancelsAndWaitsForInteractiveLogin(t *testing.T) {
	t.Setenv("FREEWAY_TEST_LOGIN_HELPER", "1")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{ClineBinary: binary})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	output := &clientLoginOutput{ready: make(chan struct{})}
	finished := make(chan error, 1)
	go func() { finished <- client.Login(ctx, "cline", nil, output, io.Discard) }()
	select {
	case <-output.ready:
	case err := <-finished:
		t.Fatalf("login failed before readiness: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("login did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- client.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not stop and wait for the login")
	}
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("login was not cancelled: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close returned while Login was still running")
	}
	if err := client.Login(context.Background(), "cline", nil, io.Discard, io.Discard); err == nil || err.Error() != "provider_client_closed" {
		t.Fatalf("closed client accepted a new login: %v", err)
	}
}

func TestNativeClientInteractiveLoginRespectsCallerDeadline(t *testing.T) {
	t.Setenv("FREEWAY_TEST_LOGIN_HELPER", "1")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{ClineBinary: binary})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = client.Login(ctx, "cline", nil, io.Discard, io.Discard)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("login did not use the caller deadline: %v", err)
	}
}
