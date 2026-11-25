package providers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type openCodeAdapter struct {
	definition Definition
	config     CLIConfig
	http       *http.Client
	lifetime   context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	base       string
	password   string
	cmd        *exec.Cmd
	wait       chan error
	closed     bool
	primed     atomic.Bool
}

// NewOpenCode controls one private official CLI server and its text-only sessions.
func NewOpenCode(definition Definition, config CLIConfig) Adapter {
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	lifetime, cancel := context.WithCancel(context.Background())
	secret := make([]byte, 24)
	password := ""
	if _, err := rand.Read(secret); err == nil {
		password = hex.EncodeToString(secret)
	}
	return &openCodeAdapter{definition: definition, config: config, http: client, lifetime: lifetime, cancel: cancel, password: password}
}

func openCodeConfiguration() map[string]any {
	permission := map[string]string{"*": "deny", "read": "ask"}
	return map[string]any{
		"enabled_providers": []string{"opencode"}, "share": "disabled",
		"compaction": map[string]bool{"auto": false}, "permission": permission,
		"agent": map[string]any{
			"title": map[string]bool{"disable": true}, "summary": map[string]bool{"disable": true},
			"compaction": map[string]bool{"disable": true},
			"freeway": map[string]any{
				"description": "Conversational gateway without filesystem or command access.",
				"mode":        "primary", "permission": permission,
				"prompt": "Answer the user conversationally. Do not use any tools, inspect local files, or execute commands.",
			},
		},
	}
}

func (a *openCodeAdapter) operation(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithTimeout(ctx, MaxStreamDuration)
	stop := context.AfterFunc(a.lifetime, cancel)
	return ctx, func() { stop(); cancel() }
}

func (a *openCodeAdapter) start(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return "", failure("adapter_closed", 503)
	}
	if a.base != "" {
		return a.base, nil
	}
	binary := a.config.Binary
	if binary == "" {
		binary, _ = CLIExecutable("opencode")
	} else {
		var err error
		binary, err = explicitCLIPath(binary)
		if err != nil {
			return "", err
		}
	}
	if binary == "" {
		return "", failure("opencode_not_installed", 409)
	}
	dir := a.config.WorkDir
	if dir == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return "", failure("opencode_start_failed", 503)
		}
		dir = filepath.Join(cache, "freeway", "opencode")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", failure("opencode_start_failed", 503)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", failure("opencode_start_failed", 503)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	if a.password == "" {
		return "", failure("opencode_start_failed", 503)
	}
	config, _ := json.Marshal(openCodeConfiguration())
	overrides := map[string]string{
		"OPENCODE_SERVER_PASSWORD": a.password, "OPENCODE_SERVER_USERNAME": "opencode",
		"OPENCODE_DISABLE_AUTOUPDATE": "true", "OPENCODE_DISABLE_SHARE": "true",
		"OPENCODE_CONFIG_CONTENT": string(config),
	}
	env := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[key]; !replaced {
			env = append(env, entry)
		}
	}
	for key, value := range overrides {
		env = append(env, key+"="+value)
	}
	cmd := exec.CommandContext(a.lifetime, binary, "serve", "--hostname", "127.0.0.1", "--port", strconv.Itoa(port))
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, env, io.Discard, io.Discard
	cleanup, err := configureOwnedProcess(cmd)
	if err != nil {
		return "", failure("opencode_start_failed", 503)
	}
	if err := cmd.Start(); err != nil {
		cleanup()
		return "", failure("opencode_start_failed", 503)
	}
	done := make(chan error, 1)
	a.cmd, a.wait = cmd, done
	go func() {
		err := cmd.Wait()
		cleanup()
		done <- err
		close(done)
		a.mu.Lock()
		if a.cmd == cmd {
			a.cmd, a.base = nil, ""
		}
		a.mu.Unlock()
	}()
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	startup, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		probe, cancelProbe := context.WithTimeout(startup, time.Second)
		response, err := a.request(probe, base, http.MethodGet, "/global/health", nil)
		if err == nil {
			_ = response.Body.Close()
			cancelProbe()
			a.base = base
			return base, nil
		}
		cancelProbe()
		select {
		case <-done:
			a.cmd = nil
			return "", failure("opencode_start_failed", 503)
		case <-startup.Done():
			_ = cmd.Process.Kill()
			<-done
			a.cmd = nil
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", failure("opencode_start_failed", 503)
		case <-ticker.C:
		}
	}
}

func (a *openCodeAdapter) request(ctx context.Context, base, method, path string, body any) (*http.Response, error) {
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, failure("invalid_request", 400)
		}
		input = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, base+path, input)
	if err != nil {
		return nil, failure("opencode_error", 502)
	}
	request.SetBasicAuth("opencode", a.password)
	request.Header.Set("Content-Type", "application/json")
	response, err := a.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, failure("opencode_error", 502)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_ = response.Body.Close()
		if response.StatusCode == http.StatusTooManyRequests {
			return nil, &Error{Code: "rate_limit_exceeded", Status: 429, RetryAfter: 600}
		}
		return nil, failure("opencode_error", response.StatusCode)
	}
	return response, nil
}

func (a *openCodeAdapter) Models(ctx context.Context, _ string) ([]Model, error) {
	// A cold `opencode serve` takes several seconds. The first lookup answers from the last
	// catalog and starts the server in the background. Later lookups use the live catalog.
	if a.primed.CompareAndSwap(false, true) {
		if models, ok := a.cachedModels(); ok {
			go func() { _, _ = a.start(a.lifetime) }()
			return models, nil
		}
	}
	ctx, cancel := a.operation(ctx)
	defer cancel()
	base, err := a.start(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancelCatalog := context.WithTimeout(ctx, 20*time.Second)
	defer cancelCatalog()
	response, err := a.request(ctx, base, http.MethodGet, "/provider", nil)
	if err != nil && ctx.Err() == nil {
		response, err = a.request(ctx, base, http.MethodGet, "/config/providers", nil)
	}
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var catalog struct {
		All       []openCodeProvider `json:"all"`
		Providers []openCodeProvider `json:"providers"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8*1024*1024)).Decode(&catalog); err != nil {
		return nil, failure("invalid_catalog", 502)
	}
	all := append(catalog.All, catalog.Providers...)
	models := []Model{}
	seen := map[string]bool{}
	for _, provider := range all {
		if provider.ID != "opencode" {
			continue
		}
		for _, raw := range provider.Models {
			cost, _ := raw["cost"].(map[string]any)
			input, inputKnown := numeric(cost["input"])
			output, outputKnown := numeric(cost["output"])
			if !inputKnown || !outputKnown || input != 0 || output != 0 {
				continue
			}
			model, free := NormalizeModel(a.definition, raw)
			if free && !seen[model.ID] {
				seen[model.ID] = true
				models = append(models, model)
			}
		}
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	a.saveModels(models)
	return models, nil
}

const openCodeCatalogMaxAge = 7 * 24 * time.Hour

func (a *openCodeAdapter) cachedModels() ([]Model, bool) {
	path := a.config.CatalogCache
	if path == "" || a.config.Binary == "" {
		return nil, false
	}
	info, err := os.Stat(path)
	if err != nil || time.Since(info.ModTime()) > openCodeCatalogMaxAge {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var models []Model
	if json.Unmarshal(data, &models) != nil || len(models) == 0 {
		return nil, false
	}
	for _, model := range models {
		if model.Provider != a.definition.ID || !model.Free {
			return nil, false
		}
	}
	return models, true
}

func (a *openCodeAdapter) saveModels(models []Model) {
	path := a.config.CatalogCache
	if path == "" || len(models) == 0 {
		return
	}
	data, err := json.Marshal(models)
	if err != nil || os.MkdirAll(filepath.Dir(path), 0700) != nil {
		return
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".opencode-models-*")
	if err != nil {
		return
	}
	_, err = temp.Write(data)
	if closeErr := temp.Close(); err == nil && closeErr == nil {
		err = os.Rename(temp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(temp.Name())
	}
}

type openCodeProvider struct {
	ID     string                    `json:"id"`
	Models map[string]map[string]any `json:"models"`
}

type openCodeResult struct {
	Info struct {
		Error  json.RawMessage `json:"error"`
		Cost   *float64        `json:"cost"`
		Tokens *struct {
			Input     int `json:"input"`
			Output    int `json:"output"`
			Reasoning int `json:"reasoning"`
			Cache     struct {
				Read  int `json:"read"`
				Write int `json:"write"`
			} `json:"cache"`
		} `json:"tokens"`
	} `json:"info"`
	Parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"parts"`
}

type openCodeUpdate struct {
	text   string
	result *openCodeResult
	err    error
}

var openCodeQuotaError = regexp.MustCompile(`(?i)free usage (?:limit|exceeded)|free_tier_limit|rate.?limit|quota|too many requests|usage limit reached`)

func openCodeModelError(raw json.RawMessage) error {
	if openCodeQuotaError.Match(raw) {
		return &Error{Code: "rate_limit_exceeded", Status: 429, RetryAfter: 600}
	}
	var data struct {
		StatusCode int `json:"statusCode"`
		Data       struct {
			StatusCode int `json:"statusCode"`
		} `json:"data"`
	}
	_ = json.Unmarshal(raw, &data)
	status := data.Data.StatusCode
	if status == 0 {
		status = data.StatusCode
	}
	if status != 401 && status != 403 && status != 429 {
		status = 502
	}
	return failure("opencode_model_error", status)
}

func (a *openCodeAdapter) cleanupSession(base, session string, abort bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	path := "/session/" + url.PathEscape(session)
	if abort {
		if response, err := a.request(ctx, base, http.MethodPost, path+"/abort", nil); err == nil {
			_ = response.Body.Close()
		}
	}
	if response, err := a.request(ctx, base, http.MethodDelete, path, nil); err == nil {
		_ = response.Body.Close()
	}
}

func (a *openCodeAdapter) Stream(ctx context.Context, model Model, _ string, input Request, emit func(string)) (Completion, error) {
	started := time.Now()
	if model.Provider != a.definition.ID || !model.Free || model.UpstreamID == "" || model.ID != a.definition.ID+"/"+model.UpstreamID {
		return Completion{}, failure("model_not_found", 404)
	}
	if input.ReasoningEffort != "" {
		supported := false
		for _, effort := range model.Reasoning.Efforts {
			if effort == input.ReasoningEffort {
				supported = true
			}
		}
		if !supported {
			return Completion{}, failure("unsupported_reasoning", 400)
		}
	}
	var system, history []string
	for _, message := range input.Messages {
		switch message.Role {
		case "system", "developer":
			system = append(system, message.Content)
		case "user", "assistant":
			history = append(history, "["+message.Role+"]\n"+message.Content)
		default:
			return Completion{}, failure("unsupported_capability", 400)
		}
	}
	ctx, cancel := a.operation(ctx)
	defer cancel()
	base, err := a.start(ctx)
	if err != nil {
		return Completion{}, err
	}
	response, err := a.request(ctx, base, http.MethodPost, "/session", map[string]any{
		"title": "Freeway chat", "permission": []map[string]string{
			{"permission": "*", "pattern": "*", "action": "deny"}, {"permission": "read", "pattern": "*", "action": "ask"},
		},
	})
	if err != nil {
		return Completion{}, err
	}
	var session struct {
		ID string `json:"id"`
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024)).Decode(&session)
	_ = response.Body.Close()
	if err != nil || session.ID == "" {
		return Completion{}, failure("invalid_stream", 502)
	}
	complete := false
	defer func() { a.cleanupSession(base, session.ID, !complete) }()
	eventCtx, stopEvents := context.WithCancel(ctx)
	defer stopEvents()
	events, err := a.request(eventCtx, base, http.MethodGet, "/event", nil)
	if err != nil {
		return Completion{}, err
	}
	defer events.Body.Close()
	updates := make(chan openCodeUpdate, 32)
	var blocked atomic.Bool
	send := func(update openCodeUpdate) {
		select {
		case updates <- update:
		case <-ctx.Done():
		}
	}
	eventsDone := make(chan struct{})
	go func() {
		defer close(eventsDone)
		assistants := map[string]bool{}
		parts := map[string]string{}
		reasoning := map[string]bool{}
		_ = readSSE(events.Body, func(raw []byte) error {
			var envelope struct {
				Payload json.RawMessage `json:"payload"`
			}
			if json.Unmarshal(raw, &envelope) != nil {
				return nil
			}
			if len(envelope.Payload) > 0 {
				raw = envelope.Payload
			}
			var event struct {
				Type       string `json:"type"`
				Properties struct {
					SessionID string          `json:"sessionID"`
					ID        string          `json:"id"`
					MessageID string          `json:"messageID"`
					PartID    string          `json:"partID"`
					Field     string          `json:"field"`
					Delta     string          `json:"delta"`
					Error     json.RawMessage `json:"error"`
					Status    json.RawMessage `json:"status"`
					Info      struct {
						ID        string          `json:"id"`
						SessionID string          `json:"sessionID"`
						Role      string          `json:"role"`
						Error     json.RawMessage `json:"error"`
					} `json:"info"`
					Part struct {
						ID        string `json:"id"`
						SessionID string `json:"sessionID"`
						MessageID string `json:"messageID"`
						Type      string `json:"type"`
						Text      string `json:"text"`
					} `json:"part"`
				} `json:"properties"`
			}
			if json.Unmarshal(raw, &event) != nil {
				return nil
			}
			props := event.Properties
			if event.Type == "permission.asked" && props.SessionID == session.ID {
				blocked.Store(true)
				err := error(failure("tool_rejection_failed", 502))
				if props.ID != "" {
					response, rejectErr := a.request(ctx, base, http.MethodPost, "/permission/"+url.PathEscape(props.ID)+"/reply", map[string]string{"reply": "reject"})
					if rejectErr == nil {
						_ = response.Body.Close()
						err = failure("unsupported_capability", 400)
					}
				}
				send(openCodeUpdate{err: err})
				return err
			}
			if event.Type == "session.error" && props.SessionID == session.ID {
				send(openCodeUpdate{err: openCodeModelError(props.Error)})
			}
			if event.Type == "session.status" && props.SessionID == session.ID {
				var status struct {
					Type string `json:"type"`
				}
				_ = json.Unmarshal(props.Status, &status)
				if status.Type == "retry" {
					err := openCodeModelError(props.Status)
					var typed *Error
					if !errors.As(err, &typed) || typed.Status != 429 {
						err = &Error{Code: "upstream_retry", Status: 503, RetryAfter: 60}
					}
					send(openCodeUpdate{err: err})
				}
			}
			if event.Type == "message.updated" && props.Info.SessionID == session.ID && props.Info.Role == "assistant" {
				assistants[props.Info.ID] = true
				if len(props.Info.Error) > 0 && string(props.Info.Error) != "null" {
					send(openCodeUpdate{err: openCodeModelError(props.Info.Error)})
				}
			}
			if event.Type == "message.part.updated" {
				part := props.Part
				if part.SessionID == session.ID && part.Type == "reasoning" && assistants[part.MessageID] {
					reasoning[part.ID] = true
					send(openCodeUpdate{})
					return nil
				}
				if part.SessionID != session.ID || part.Type != "text" || !assistants[part.MessageID] {
					return nil
				}
				previous := parts[part.ID]
				if strings.HasPrefix(part.Text, previous) && len(part.Text) > len(previous) {
					send(openCodeUpdate{text: part.Text[len(previous):]})
				}
				parts[part.ID] = part.Text
			}
			if event.Type == "message.part.delta" && props.SessionID == session.ID && props.Field == "text" && assistants[props.MessageID] {
				if reasoning[props.PartID] {
					send(openCodeUpdate{})
				} else if previous, ok := parts[props.PartID]; ok {
					parts[props.PartID] = previous + props.Delta
					send(openCodeUpdate{text: props.Delta})
				}
			}
			return nil
		})
	}()
	postDone := make(chan struct{})
	go func() {
		defer close(postDone)
		body := map[string]any{
			"model": map[string]string{"providerID": "opencode", "modelID": model.UpstreamID},
			"agent": "freeway", "system": strings.Join(system, "\n\n"),
			"parts": []map[string]string{{"type": "text", "text": "Continue this conversation and answer the last user message:\n\n" + strings.Join(history, "\n\n")}},
		}
		if input.ReasoningEffort != "" {
			body["variant"] = input.ReasoningEffort
		}
		response, err := a.request(ctx, base, http.MethodPost, "/session/"+url.PathEscape(session.ID)+"/message", body)
		if err != nil {
			send(openCodeUpdate{err: err})
			return
		}
		defer response.Body.Close()
		var result openCodeResult
		if json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024)).Decode(&result) != nil {
			send(openCodeUpdate{err: failure("invalid_stream", 502)})
			return
		}
		if len(result.Info.Error) > 0 && string(result.Info.Error) != "null" {
			send(openCodeUpdate{err: openCodeModelError(result.Info.Error)})
			return
		}
		if !blocked.Load() {
			send(openCodeUpdate{result: &result})
		}
	}()
	defer func() {
		cancel()
		stopEvents()
		_ = events.Body.Close()
		<-postDone
		<-eventsDone
	}()
	var emitted strings.Builder
	for {
		select {
		case <-ctx.Done():
			return Completion{}, ctx.Err()
		case update := <-updates:
			if update.err != nil {
				return Completion{}, update.err
			}
			if update.result == nil {
				if !blocked.Load() {
					emitted.WriteString(update.text)
					if emit != nil {
						emit(update.text) // Empty text reports reasoning progress.
					}
				}
				continue
			}
			if blocked.Load() {
				continue
			}
			var texts []string
			for _, part := range update.result.Parts {
				if part.Type == "text" {
					texts = append(texts, part.Text)
				}
			}
			final := strings.Join(texts, "\n")
			if final != "" {
				if !strings.HasPrefix(final, emitted.String()) {
					return Completion{}, failure("invalid_stream", 502)
				}
				delta := final[emitted.Len():]
				emitted.WriteString(delta)
				if emit != nil && delta != "" {
					emit(delta)
				}
			}
			if strings.TrimSpace(emitted.String()) == "" {
				return Completion{}, failure("empty_completion", 502)
			}
			var usage json.RawMessage
			if tokens := update.result.Info.Tokens; tokens != nil {
				prompt, completion := tokens.Input+tokens.Cache.Read+tokens.Cache.Write, tokens.Output+tokens.Reasoning
				values := map[string]any{"prompt_tokens": prompt, "completion_tokens": completion, "total_tokens": prompt + completion}
				if update.result.Info.Cost != nil {
					values["cost"] = *update.result.Info.Cost
				}
				usage = rawJSON(values)
			}
			complete = true
			return Completion{Model: model.ID, Provider: a.definition.ID, Content: emitted.String(), ReasoningEffort: input.ReasoningEffort, Usage: usage, ElapsedMS: time.Since(started).Milliseconds()}, nil
		}
	}
}

func (a *openCodeAdapter) Close() error {
	a.cancel()
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	cmd, done := a.cmd, a.wait
	a.cmd, a.base = nil, ""
	a.mu.Unlock()
	if cmd != nil {
		if cmd.Cancel != nil {
			_ = cmd.Cancel()
		} else {
			_ = cmd.Process.Kill()
		}
		<-done
	}
	return nil
}
