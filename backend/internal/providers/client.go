// Package providers connects Go callers directly to vendor APIs and official CLIs.
// The caller owns routing, quota accounting, persistent credentials and HTTP serving.
package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type Client struct {
	config      Config
	adapters    map[string]Adapter
	definitions map[string]Definition
	mu          sync.Mutex
	closed      bool
	workDir     string
	temporary   bool
	ctx         context.Context
	cancel      context.CancelFunc
	workers     sync.WaitGroup
	connected   map[string]bool
	failures    map[string]observedFailure
}
type observedFailure struct {
	Code       string    `json:"code"`
	Status     int       `json:"status"`
	RetryAfter int       `json:"retryAfter"`
	ObservedAt time.Time `json:"observedAt"`
}

// New creates a native provider client. It never starts a gateway or a Node process.
// Without Credentials, keys and free-account confirmations live only in this client.
func New(config Config) (*Client, error) {
	for _, binary := range []*string{&config.OpenCodeBinary, &config.ClineBinary} {
		if *binary != "" {
			resolved, err := explicitCLIPath(*binary)
			if err != nil {
				return nil, err
			}
			*binary = resolved
		}
	}
	if config.Credentials == nil {
		config.Credentials = &memoryCredentials{values: map[string]Credential{}}
	}
	workDir := config.WorkDir
	temporary := workDir == ""
	var err error
	if temporary {
		workDir, err = os.MkdirTemp("", "freeway-providers-")
	} else {
		workDir, err = filepath.Abs(workDir)
		if err == nil {
			err = os.MkdirAll(workDir, 0700)
		}
	}
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{config: config, adapters: map[string]Adapter{}, definitions: map[string]Definition{}, workDir: workDir, temporary: temporary, ctx: ctx, cancel: cancel, connected: map[string]bool{}, failures: map[string]observedFailure{}}
	for _, d := range Definitions() {
		c.definitions[d.ID] = d
		if !d.RequiresCLI {
			c.adapters[d.ID] = NewHTTPAdapter(d, config.HTTPClient)
			continue
		}
		binary := config.OpenCodeBinary
		if d.ID == "cline" {
			binary = config.ClineBinary
		}
		if binary == "" {
			binary, _ = CLIExecutable(d.ID)
		}
		cli := CLIConfig{Binary: binary, WorkDir: filepath.Join(workDir, d.ID), HTTPClient: config.HTTPClient}
		if d.ID == "opencode" {
			c.adapters[d.ID] = NewOpenCode(d, cli)
		} else {
			c.adapters[d.ID] = NewCline(d, cli)
		}
	}
	return c, nil
}

func (c *Client) call(ctx context.Context) (context.Context, func(), error) {
	return c.begin(ctx, 2*time.Minute)
}

func (c *Client) begin(ctx context.Context, timeout time.Duration) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, nil, errors.New("provider_client_closed")
	}
	c.workers.Add(1)
	c.mu.Unlock()
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	stop := context.AfterFunc(c.ctx, cancel)
	return ctx, func() { stop(); cancel(); c.workers.Done() }, nil
}

// Close cancels active operations, waits for owned CLI processes and removes temporary workspaces.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.cancel()
	c.mu.Unlock()
	var result error
	for _, adapter := range c.adapters {
		result = errors.Join(result, adapter.Close())
	}
	c.workers.Wait()
	if c.temporary {
		result = errors.Join(result, os.RemoveAll(c.workDir))
	}
	return result
}
func (c *Client) definition(id string) (Definition, error) {
	d, ok := c.definitions[id]
	if !ok {
		return d, failure("unknown_provider", 404)
	}
	return d, nil
}

// Providers returns public vendor metadata without contacting a vendor or starting a CLI.
func (c *Client) Providers(ctx context.Context) ([]Provider, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := make([]Provider, 0, len(c.definitions))
	for _, d := range Definitions() {
		result = append(result, d.Provider)
	}
	return result, nil
}

func (c *Client) credential(ctx context.Context, d Definition) (Credential, error) {
	value, err := c.config.Credentials.Load(ctx, d.ID)
	if err != nil {
		if ctx.Err() != nil {
			return value, ctx.Err()
		}
		return value, failure("credential_store_failed", 500)
	}
	if value.Key == "" {
		value.Key = os.Getenv(d.Env)
		value.FreeConfirmed = false
	}
	return value, nil
}
func readiness(d Definition, value Credential) string {
	if !d.RequiresCLI && d.ID != "kilo" && value.Key == "" {
		return "needs_key"
	}
	if d.NeedsConfirmation && !value.FreeConfirmed {
		return "confirm_free_account"
	}
	return ""
}

// Authenticate configures this client's credentials, or delegates persistence to CredentialStore.
// Ready reports connection setup, not successful inference or an authoritative free allowance.
func (c *Client) Authenticate(ctx context.Context, id string, options AuthOptions) (AuthResult, error) {
	ctx, done, err := c.call(ctx)
	if err != nil {
		return AuthResult{}, err
	}
	defer done()
	d, err := c.definition(id)
	if err != nil {
		return AuthResult{}, err
	}
	result := AuthResult{ID: id, Status: "configured"}
	if d.RequiresCLI && options.Key != "" {
		return result, failure("cli_login_required", 409)
	}
	previous, err := c.config.Credentials.Load(ctx, d.ID)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, failure("credential_store_failed", 500)
	}
	value := previous
	if value.Key == "" {
		value.Key = os.Getenv(d.Env)
		value.FreeConfirmed = false
	}
	if options.Key != "" {
		if len(options.Key) > 4096 || !utf8.ValidString(options.Key) || strings.TrimSpace(options.Key) == "" || strings.IndexFunc(options.Key, unicode.IsControl) >= 0 {
			return result, failure("invalid_key", 400)
		}
		value.Key = strings.TrimSpace(options.Key)
		value.FreeConfirmed = false
	}
	if options.ConfirmFree {
		value.FreeConfirmed = true
	}
	if options.Key != "" || options.ConfirmFree {
		if store, ok := c.config.Credentials.(interface {
			CompareAndSwap(context.Context, string, Credential, Credential) error
		}); ok {
			err = store.CompareAndSwap(ctx, id, previous, value)
		} else {
			err = c.config.Credentials.Save(ctx, id, value)
		}
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			var typed *Error
			if errors.As(err, &typed) && typed.Code == "credential_conflict" {
				return result, failure("credential_conflict", 409)
			}
			return result, failure("credential_store_failed", 500)
		}
	}
	result.Reason = readiness(d, value)
	if result.Reason != "" {
		return result, nil
	}
	models, err := c.models(ctx, d, value)
	if err != nil {
		return result, err
	}
	result.Ready = true
	result.ModelCount = len(models)
	return result, nil
}

// Login runs the official CLI's interactive authentication directly from Go.
// Use it in a terminal, never a web request handler.
func (c *Client) Login(ctx context.Context, id string, stdin io.Reader, stdout, stderr io.Writer) error {
	// Interactive browser/device flows use only the caller's deadline.
	ctx, done, err := c.begin(ctx, 0)
	if err != nil {
		return err
	}
	defer done()
	if err := ctx.Err(); err != nil {
		return err
	}
	d, err := c.definition(id)
	if err != nil {
		return err
	}
	if !d.RequiresCLI {
		return errors.New("interactive login requires opencode or cline")
	}
	binary := c.config.OpenCodeBinary
	if id == "cline" {
		binary = c.config.ClineBinary
	}
	if binary == "" {
		binary, err = CLIExecutable(id)
		if err != nil {
			return err
		}
	}
	var metadata struct {
		Args []string `json:"loginArgs"`
	}
	data, _ := authMetadata.ReadFile(id + "/auth.json")
	if json.Unmarshal(data, &metadata) != nil || len(metadata.Args) == 0 {
		return failure("invalid_login_configuration", 500)
	}
	cmd := exec.CommandContext(ctx, binary, metadata.Args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	if err = configureLoginCancellation(cmd); err != nil {
		return err
	}
	err = cmd.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (c *Client) models(ctx context.Context, d Definition, value Credential) ([]Model, error) {
	if reason := readiness(d, value); reason != "" {
		return nil, failure(reason, 409)
	}
	models, err := c.adapters[d.ID].Models(ctx, value.Key)
	if err != nil {
		c.observe(d.ID, err)
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	for _, m := range models {
		if !m.Free || m.Provider != d.ID || m.ID != d.ID+"/"+m.UpstreamID {
			return nil, failure("invalid_catalog", 502)
		}
	}
	c.mu.Lock()
	c.connected[d.ID] = true
	c.mu.Unlock()
	return models, nil
}

// Models refreshes the current free catalog and vendor-advertised reasoning choices.
func (c *Client) Models(ctx context.Context, id string) ([]Model, error) {
	ctx, done, err := c.call(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	d, err := c.definition(id)
	if err != nil {
		return nil, err
	}
	value, err := c.credential(ctx, d)
	if err != nil {
		return nil, err
	}
	return c.models(ctx, d, value)
}

// Quota reports only this adapter's observations. Local accounting belongs to the caller.
// Nil remaining values mean unknown; this function never makes an inference request.
func (c *Client) Quota(ctx context.Context, id string) (Quota, error) {
	ctx, done, err := c.call(ctx)
	if err != nil {
		return Quota{}, err
	}
	defer done()
	d, err := c.definition(id)
	if err != nil {
		return Quota{}, err
	}
	value, err := c.credential(ctx, d)
	if err != nil {
		return Quota{}, err
	}
	q := Quota{Provider: id, KeyConfigured: strings.TrimSpace(value.Key) != "", Source: "provider_adapter", ObservedAt: time.Now().UTC(), LastFailure: json.RawMessage("null")}
	q.Reason = readiness(d, value)
	c.mu.Lock()
	defer c.mu.Unlock()
	if d.RequiresCLI && !c.connected[id] && q.Reason == "" {
		q.Reason = "not_connected"
	}
	q.Ready = q.Reason == ""
	if observed, ok := c.failures[id]; ok {
		q.LastFailure, _ = json.Marshal(observed)
	}
	return q, nil
}

func validate(request Request) error {
	if len(request.Messages) < 1 || len(request.Messages) > 200 {
		return failure("invalid_messages", 400)
	}
	for _, message := range request.Messages {
		switch message.Role {
		case "system", "developer", "user", "assistant":
		default:
			return failure("invalid_role", 400)
		}
		if !utf8.ValidString(message.Content) {
			return failure("invalid_messages", 400)
		}
	}
	if request.MaxTokens < 0 || request.MaxTokens > 32768 {
		return failure("invalid_parameter", 400)
	}
	if request.Temperature != nil && (*request.Temperature < 0 || *request.Temperature > 2) {
		return failure("invalid_parameter", 400)
	}
	data, err := json.Marshal(request)
	if err != nil {
		return failure("invalid_request", 400)
	}
	if len(data) > 2*1024*1024 {
		return failure("request_too_large", 413)
	}
	return nil
}
func (c *Client) observe(id string, err error) {
	var typed *Error
	if errors.As(err, &typed) {
		c.mu.Lock()
		c.failures[id] = observedFailure{Code: typed.Code, Status: typed.Status, RetryAfter: typed.RetryAfter, ObservedAt: time.Now().UTC()}
		c.mu.Unlock()
	}
}

// Complete collects the same response as Stream without a text callback.
func (c *Client) Complete(ctx context.Context, request Request) (Completion, error) {
	return c.Stream(ctx, request, nil)
}

// Stream calls exactly one provider/model and never falls back or retries.
// The caller must check the final error even after receiving partial text.
func (c *Client) Stream(ctx context.Context, request Request, emit func(string)) (Completion, error) {
	if err := validate(request); err != nil {
		return Completion{}, err
	}
	ctx, done, err := c.call(ctx)
	if err != nil {
		return Completion{}, err
	}
	defer done()
	d, err := c.definition(request.Provider)
	if err != nil {
		return Completion{}, err
	}
	value, err := c.credential(ctx, d)
	if err != nil {
		return Completion{}, err
	}
	models, err := c.models(ctx, d, value)
	if err != nil {
		return Completion{}, err
	}
	var selected *Model
	for i := range models {
		if models[i].ID == request.Model || models[i].UpstreamID == request.Model {
			selected = &models[i]
			break
		}
	}
	if selected == nil {
		return Completion{}, failure("model_not_found", 404)
	}
	if request.ReasoningEffort != "" {
		valid := false
		for _, effort := range selected.Reasoning.Efforts {
			if effort == request.ReasoningEffort {
				valid = true
			}
		}
		if !valid {
			return Completion{}, failure("unsupported_reasoning", 400)
		}
	}
	result, err := c.adapters[d.ID].Stream(ctx, *selected, value.Key, request, emit)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		c.observe(d.ID, err)
		return result, err
	}
	if result.Provider != d.ID || result.Model != selected.ID {
		return Completion{}, failure("unexpected_route", 502)
	}
	return result, nil
}

type memoryCredentials struct {
	mu     sync.Mutex
	values map[string]Credential
}

func (m *memoryCredentials) Load(ctx context.Context, id string) (Credential, error) {
	if err := ctx.Err(); err != nil {
		return Credential{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.values[id], nil
}
func (m *memoryCredentials) Save(ctx context.Context, id string, value Credential) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[id] = value
	return nil
}

func (m *memoryCredentials) CompareAndSwap(ctx context.Context, id string, previous, value Credential) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.values[id] != previous {
		return failure("credential_conflict", 409)
	}
	m.values[id] = value
	return nil
}
