package providers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const clineCatalogURL = "https://api.cline.bot/api/v1/ai/cline/recommended-models"
const clineTextOnly = "You are a conversational assistant. Answer the last user message using the provided conversation. Do not use tools, access files, execute commands, or delegate tasks."

type clineAdapter struct {
	definition Definition
	config     CLIConfig
	client     *http.Client
	command    func(context.Context, string, ...string) *exec.Cmd
	mu         sync.Mutex
	closed     bool
	freeIDs    map[string]bool
	listedAt   time.Time
	children   map[*exec.Cmd]context.CancelFunc
	workers    sync.WaitGroup
}

// NewCline uses the official CLI's ACP protocol directly; no intermediary runtime is started.
func NewCline(definition Definition, config CLIConfig) Adapter {
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &clineAdapter{definition: definition, config: config, client: client,
		command: exec.CommandContext, freeIDs: make(map[string]bool), children: make(map[*exec.Cmd]context.CancelFunc)}
}

func clineAuthenticated() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	file, err := os.Open(filepath.Join(home, ".cline", "data", "settings", "providers.json"))
	if err != nil {
		return false
	}
	defer file.Close()
	var settings struct {
		Providers struct {
			Cline struct {
				Settings struct {
					Auth struct {
						AccessToken string `json:"accessToken"`
					} `json:"auth"`
				} `json:"settings"`
			} `json:"cline"`
		} `json:"providers"`
	}
	return json.NewDecoder(io.LimitReader(file, 2*1024*1024)).Decode(&settings) == nil &&
		strings.TrimSpace(settings.Providers.Cline.Settings.Auth.AccessToken) != ""
}

func (a *clineAdapter) Models(ctx context.Context, key string) ([]Model, error) {
	if key != "" {
		return nil, failure("cli_login_required", 409)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	closed := a.closed
	a.mu.Unlock()
	if closed {
		return nil, failure("cli_disconnected", 503)
	}
	if a.config.Binary == "" {
		return nil, failure("cli_not_installed", 409)
	}
	if _, err := explicitCLIPath(a.config.Binary); err != nil {
		return nil, err
	}
	if !clineAuthenticated() {
		return nil, failure("cli_login_required", 409)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, clineCatalogURL, nil)
	if err != nil {
		return nil, failure("catalog_failed", 502)
	}
	response, err := a.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, failure("catalog_failed", 502)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, failure("catalog_failed", 502)
	}
	var catalog struct {
		Free []map[string]any `json:"free"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4*1024*1024)).Decode(&catalog); err != nil || catalog.Free == nil {
		return nil, failure("invalid_catalog", 502)
	}
	models := make([]Model, 0, len(catalog.Free))
	freeIDs := make(map[string]bool, len(catalog.Free))
	for _, raw := range catalog.Free {
		// Recommended and ClinePass arrays are deliberately never considered.
		if model, free := NormalizeModel(a.definition, raw); free && !freeIDs[model.UpstreamID] {
			model.Reasoning = Reasoning{}
			models = append(models, model)
			freeIDs[model.UpstreamID] = true
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil, failure("cli_disconnected", 503)
	}
	a.freeIDs, a.listedAt = freeIDs, time.Now()
	return models, nil
}

func (a *clineAdapter) Stream(ctx context.Context, model Model, key string, request Request, emit func(string)) (Completion, error) {
	if request.ReasoningEffort != "" {
		return Completion{}, failure("unsupported_reasoning", 400)
	}
	if key != "" {
		return Completion{}, failure("cli_login_required", 409)
	}
	if model.Provider != a.definition.ID || !model.Free || model.UpstreamID == "" || model.ID != a.definition.ID+"/"+model.UpstreamID ||
		(request.Provider != "" && request.Provider != a.definition.ID) ||
		(request.Model != "" && request.Model != model.ID && request.Model != model.UpstreamID) {
		return Completion{}, failure("model_not_found", 404)
	}
	if len(request.Messages) == 0 || len(request.Messages) > 200 {
		return Completion{}, failure("invalid_messages", 400)
	}
	for _, message := range request.Messages {
		if message.Role != "user" && message.Role != "assistant" && message.Role != "system" && message.Role != "developer" {
			return Completion{}, failure("invalid_role", 400)
		}
		if !utf8.ValidString(message.Content) {
			return Completion{}, failure("invalid_messages", 400)
		}
	}
	if err := ctx.Err(); err != nil {
		return Completion{}, err
	}
	ctx, requestCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer requestCancel()
	a.mu.Lock()
	stale := a.listedAt.IsZero() || time.Since(a.listedAt) > time.Minute
	allowed, closed := a.freeIDs[model.UpstreamID], a.closed
	a.mu.Unlock()
	if closed {
		return Completion{}, failure("cli_disconnected", 503)
	}
	if stale {
		if _, err := a.Models(ctx, ""); err != nil {
			return Completion{}, err
		}
		a.mu.Lock()
		allowed = a.freeIDs[model.UpstreamID]
		a.mu.Unlock()
	}
	if !allowed {
		return Completion{}, failure("not_free_model", 400)
	}
	binary, err := explicitCLIPath(a.config.Binary)
	if err != nil {
		return Completion{}, err
	}
	if a.config.WorkDir == "" {
		return Completion{}, failure("cli_workspace_required", 500)
	}
	if err := os.MkdirAll(a.config.WorkDir, 0700); err != nil {
		return Completion{}, failure("cli_workspace_failed", 500)
	}
	workspace, err := os.MkdirTemp(a.config.WorkDir, "request-")
	if err != nil {
		return Completion{}, failure("cli_workspace_failed", 500)
	}
	var trackedCmd *exec.Cmd
	defer func() {
		if trackedCmd != nil {
			a.mu.Lock()
			delete(a.children, trackedCmd)
			a.mu.Unlock()
			a.workers.Done()
		}
	}()
	defer os.RemoveAll(workspace)
	var history strings.Builder
	for i, message := range request.Messages {
		if i > 0 {
			history.WriteString("\n\n")
		}
		history.WriteString("[" + message.Role + "]\n" + message.Content)
	}
	args := []string{"--acp", "--auto-approve", "false", "--provider", "cline", "--compaction", "off",
		"--retries", "1", "--timeout", "120", "--cwd", workspace, "--hooks-dir", workspace, "--system", clineTextOnly}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := a.command(childCtx, binary, args...)
	cmd.Dir = workspace
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if !strings.EqualFold(name, "CLINE_API_KEY") && !strings.EqualFold(name, "CLINE_PROVIDER") {
			cmd.Env = append(cmd.Env, item)
		}
	}
	cmd.Env = append(cmd.Env, "CLINE_PROVIDER=cline")
	cleanup, err := configureOwnedProcess(cmd)
	if err != nil {
		return Completion{}, failure("cli_start_failed", 503)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Completion{}, failure("cli_start_failed", 503)
	}
	defer stdin.Close()
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		return Completion{}, failure("cli_start_failed", 503)
	}
	defer stdout.Close()
	defer stdoutWriter.Close()
	cmd.Stdout = stdoutWriter
	var stderr clineTail
	cmd.Stderr = &stderr
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return Completion{}, failure("cli_disconnected", 503)
	}
	started := time.Now()
	if err := cmd.Start(); err != nil {
		a.mu.Unlock()
		return Completion{}, failure("cli_start_failed", 503)
	}
	a.children[cmd] = cancel
	a.workers.Add(1)
	trackedCmd = cmd
	a.mu.Unlock()
	_ = stdoutWriter.Close()
	exited := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = cmd.Wait()
		cleanup()
		close(exited)
	}()
	defer func() {
		cancel()
		_ = stdin.Close()
		<-exited
	}()
	result, streamErr := clineConversation(stdout, stdin, workspace, model, history.String(), emit)
	// The official CLI can remain alive after end_turn; always stop it and wait before removing its workspace.
	cancel()
	_ = stdin.Close()
	<-exited
	if ctx.Err() != nil {
		return Completion{}, ctx.Err()
	}
	if streamErr != nil {
		var typed *Error
		if waitErr != nil && errors.As(streamErr, &typed) && typed.Code == "incomplete_stream" {
			if classified := clineFailure(stderr.String()); classified.Code != "cli_error" {
				return Completion{}, classified
			}
		}
		return Completion{}, streamErr
	}
	result.ElapsedMS = time.Since(started).Milliseconds()
	return result, nil
}

func clineConversation(stdout io.Reader, stdin io.Writer, workspace string, model Model, history string, emit func(string)) (Completion, error) {
	encoder := json.NewEncoder(stdin)
	send := func(value map[string]any) error {
		value["jsonrpc"] = "2.0"
		if encoder.Encode(value) != nil {
			return failure("cli_disconnected", 502)
		}
		return nil
	}
	if err := send(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}}}); err != nil {
		return Completion{}, err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 2*1024*1024)
	step, sessionID := 1, ""
	var text strings.Builder
	for scanner.Scan() {
		var item struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Params struct {
				SessionID string `json:"sessionId"`
				Update    struct {
					SessionUpdate string `json:"sessionUpdate"`
					Content       struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"update"`
			} `json:"params"`
			Error *struct {
				Message string `json:"message"`
				Data    struct {
					Message string `json:"message"`
				} `json:"data"`
			} `json:"error"`
		}
		if json.Unmarshal(scanner.Bytes(), &item) != nil {
			return Completion{}, failure("invalid_stream", 502)
		}
		if item.Method != "" {
			if len(item.ID) != 0 {
				response := map[string]any{"id": item.ID}
				if item.Method == "session/request_permission" {
					response["result"] = map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
				} else {
					response["error"] = map[string]any{"code": -32601, "message": "Client tools are disabled."}
				}
				if err := send(response); err != nil {
					return Completion{}, err
				}
			} else if item.Method == "session/update" && step == 4 && item.Params.SessionID == sessionID &&
				item.Params.Update.SessionUpdate == "agent_message_chunk" && item.Params.Update.Content.Type == "text" {
				delta := item.Params.Update.Content.Text
				text.WriteString(delta)
				if emit != nil && delta != "" {
					emit(delta)
				}
			}
			continue
		}
		var id int
		if json.Unmarshal(item.ID, &id) != nil || id != step {
			return Completion{}, failure("invalid_stream", 502)
		}
		if item.Error != nil {
			return Completion{}, clineFailure(item.Error.Message + " " + item.Error.Data.Message)
		}
		if len(item.Result) == 0 || string(item.Result) == "null" {
			return Completion{}, failure("invalid_stream", 502)
		}
		var next map[string]any
		switch step {
		case 1:
			next = map[string]any{"id": 2, "method": "session/new", "params": map[string]any{"cwd": workspace, "mcpServers": []any{}}}
		case 2:
			var result struct {
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(item.Result, &result) != nil || result.SessionID == "" {
				return Completion{}, failure("invalid_stream", 502)
			}
			sessionID = result.SessionID
			next = map[string]any{"id": 3, "method": "session/set_model", "params": map[string]any{"sessionId": sessionID, "modelId": model.UpstreamID}}
		case 3:
			next = map[string]any{"id": 4, "method": "session/prompt", "params": map[string]any{"sessionId": sessionID,
				"prompt": []any{map[string]any{"type": "text", "text": clineTextOnly + "\nThis is a text-only chat, not a coding task. Do not execute a command to print an answer.\n\n" + history}}}}
		case 4:
			var result struct {
				StopReason string          `json:"stopReason"`
				Usage      json.RawMessage `json:"usage"`
			}
			if json.Unmarshal(item.Result, &result) != nil {
				return Completion{}, failure("invalid_stream", 502)
			}
			if result.StopReason != "end_turn" {
				return Completion{}, clineFailure(result.StopReason)
			}
			if strings.TrimSpace(text.String()) == "" {
				return Completion{}, failure("empty_completion", 502)
			}
			return Completion{Provider: model.Provider, Model: model.ID, Content: text.String(), Usage: result.Usage}, nil
		}
		if err := send(next); err != nil {
			return Completion{}, err
		}
		step++
	}
	if scanner.Err() != nil {
		return Completion{}, failure("invalid_stream", 502)
	}
	return Completion{}, failure("incomplete_stream", 502)
}

func (a *clineAdapter) Close() error {
	a.mu.Lock()
	a.closed = true
	stops := make([]context.CancelFunc, 0, len(a.children))
	for _, stop := range a.children {
		stops = append(stops, stop)
	}
	a.mu.Unlock()
	for _, stop := range stops {
		stop()
	}
	a.workers.Wait()
	return nil
}

func clineFailure(message string) *Error {
	text := strings.ToLower(message)
	for _, token := range []string{"quota", "rate_limit", "rate limit", "rate-limit", "free usage limit", "free tier limit", "capacity", "resource_exhausted", "429"} {
		if strings.Contains(text, token) {
			return &Error{Code: "rate_limit_exceeded", Status: 429, RetryAfter: 600}
		}
	}
	for _, token := range []string{"auth", "sign in", "sign-in", "login", "credential", "401"} {
		if strings.Contains(text, token) {
			return failure("cli_login_required", 401)
		}
	}
	return failure("cli_error", 502)
}

type clineTail struct {
	mu   sync.Mutex
	text string
}

func (b *clineTail) Write(data []byte) (int, error) {
	b.mu.Lock()
	b.text += string(data)
	if len(b.text) > 8192 {
		b.text = b.text[len(b.text)-8192:]
	}
	b.mu.Unlock()
	return len(data), nil
}

func (b *clineTail) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text
}
