// Package providers exposes the tested Node provider implementations to the Go backend.
// It reuses one running local POC gateway rather than owning another secret or quota store.
package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type Config struct {
	NodePath   string
	BridgePath string
	BaseURL    string
}

type Client struct{ config Config }

func New(config Config) (*Client, error) {
	if config.NodePath == "" {
		var err error
		config.NodePath, err = exec.LookPath("node")
		if err != nil {
			return nil, err
		}
	}
	if !filepath.IsAbs(config.NodePath) || !filepath.IsAbs(config.BridgePath) {
		return nil, errors.New("node and bridge paths must be absolute")
	}
	if _, err := os.Stat(config.BridgePath); err != nil {
		return nil, err
	}
	if config.BaseURL == "" {
		config.BaseURL = "http://127.0.0.1:4141"
	}
	u, err := url.Parse(config.BaseURL)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
		return nil, errors.New("base URL must be a loopback HTTP origin")
	}
	return &Client{config: config}, nil
}

type Provider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	AuthURL string `json:"authUrl"`
	Env     string `json:"env"`
}

type AuthOptions struct {
	// Key travels only over stdin. Empty leaves existing credentials unchanged.
	Key         string `json:"key,omitempty"`
	ConfirmFree bool   `json:"confirmFree,omitempty"`
}

type AuthResult struct {
	ID                string `json:"id"`
	Status            string `json:"status"`
	Ready             bool   `json:"ready"`
	Reason            string `json:"reason"`
	InferenceVerified bool   `json:"inferenceVerified"`
	ModelCount        int    `json:"modelCount"`
}

type Reasoning struct {
	Parameter string   `json:"parameter"`
	Efforts   []string `json:"efforts"`
}

type Model struct {
	ID         string    `json:"id"`
	UpstreamID string    `json:"upstreamId"`
	Name       string    `json:"name"`
	Ready      bool      `json:"ready"`
	Reason     string    `json:"reason"`
	Reasoning  Reasoning `json:"reasoning"`
}

type Bucket struct {
	Requests        int `json:"requests"`
	Tokens          int `json:"tokens"`
	EstimatedTokens int `json:"estimatedTokens"`
}

type Quota struct {
	Provider            string            `json:"provider"`
	Source              string            `json:"source"`
	Authoritative       bool              `json:"authoritative"`
	ObservedAt          time.Time         `json:"observedAt"`
	Ready               bool              `json:"ready"`
	Reason              string            `json:"reason"`
	Limits              map[string]int    `json:"limits"`
	Usage               map[string]Bucket `json:"usage"`
	LocalRemaining      map[string]int    `json:"localRemaining"`
	LocalTokenRemaining map[string]int    `json:"localTokenRemaining"`
	// Nil means the provider has not reported an authoritative remaining allowance.
	ProviderRemaining *int            `json:"providerRemaining"`
	LastFailure       json.RawMessage `json:"lastFailure"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Request struct {
	Provider        string    `json:"provider"`
	Model           string    `json:"model"`
	Messages        []Message `json:"messages"`
	MaxTokens       int       `json:"maxTokens,omitempty"`
	ReasoningEffort string    `json:"reasoningEffort,omitempty"`
}

type Completion struct {
	Model           string          `json:"model"`
	Provider        string          `json:"provider"`
	Content         string          `json:"content"`
	ReasoningEffort string          `json:"reasoningEffort"`
	Usage           json.RawMessage `json:"usage"`
	ElapsedMS       int64           `json:"elapsedMs"`
}

type Error struct {
	Code       string `json:"code"`
	Status     int    `json:"status"`
	RetryAfter int    `json:"retryAfter"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("provider request failed: %s (HTTP %d)", e.Code, e.Status)
}

func (c *Client) Providers(ctx context.Context) ([]Provider, error) {
	var result []Provider
	err := c.run(ctx, map[string]any{"action": "providers"}, &result, nil)
	return result, err
}

func (c *Client) Authenticate(ctx context.Context, provider string, options AuthOptions) (AuthResult, error) {
	var result AuthResult
	input := map[string]any{"action": "authenticate", "provider": provider, "confirmFree": options.ConfirmFree}
	if options.Key != "" {
		input["key"] = options.Key
	}
	err := c.run(ctx, input, &result, nil)
	return result, err
}

// Login opens the official interactive login path; use it in a terminal, not a request handler.
func (c *Client) Login(ctx context.Context, provider string, stdin io.Reader, stdout, stderr io.Writer) error {
	if provider != "opencode" && provider != "cline" {
		return errors.New("interactive login requires opencode or cline")
	}
	cmd := exec.CommandContext(ctx, c.config.NodePath, filepath.Join(filepath.Dir(c.config.BridgePath), "manage.mjs"), "auth", provider, "--login")
	cmd.Env = append(os.Environ(), "FREEWAY_URL="+c.config.BaseURL)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	if err := configureLoginCancellation(cmd); err != nil {
		return err
	}
	err := cmd.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (c *Client) Models(ctx context.Context, provider string) ([]Model, error) {
	var result []Model
	err := c.run(ctx, map[string]any{"action": "models", "provider": provider}, &result, nil)
	return result, err
}

func (c *Client) Quota(ctx context.Context, provider string) (Quota, error) {
	var result Quota
	err := c.run(ctx, map[string]any{"action": "quota", "provider": provider}, &result, nil)
	return result, err
}

func (c *Client) Complete(ctx context.Context, request Request) (Completion, error) {
	return c.Stream(ctx, request, nil)
}

func (c *Client) Stream(ctx context.Context, request Request, emit func(string)) (Completion, error) {
	var result Completion
	input := map[string]any{"action": "complete", "provider": request.Provider, "model": request.Model,
		"messages": request.Messages}
	if request.MaxTokens != 0 {
		input["maxTokens"] = request.MaxTokens
	}
	if request.ReasoningEffort != "" {
		input["reasoningEffort"] = request.ReasoningEffort
	}
	err := c.run(ctx, input, &result, emit)
	return result, err
}

func (c *Client) run(ctx context.Context, input map[string]any, result any, emit func(string)) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	input["baseURL"] = c.config.BaseURL
	encoded, err := json.Marshal(input)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, c.config.NodePath, c.config.BridgePath)
	cmd.Stdin = bytes.NewReader(encoded)
	// Child stderr is deliberately not included in returned errors: it can contain credentials.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 2*1024*1024)
	var protocolError error
	terminal := false
	for scanner.Scan() {
		var event struct {
			Type   string          `json:"type"`
			Text   string          `json:"text"`
			Result json.RawMessage `json:"result"`
			Error  *Error          `json:"error"`
		}
		if terminal || json.Unmarshal(scanner.Bytes(), &event) != nil {
			protocolError = errors.New("invalid provider bridge protocol")
			_ = cmd.Process.Kill()
			break
		}
		switch event.Type {
		case "text":
			if emit != nil && ctx.Err() == nil {
				emit(event.Text)
			}
		case "result":
			terminal = true
			protocolError = json.Unmarshal(event.Result, result)
		case "error":
			terminal = true
			if event.Error == nil {
				protocolError = errors.New("missing provider error")
			} else {
				protocolError = event.Error
			}
		default:
			protocolError = errors.New("unknown provider bridge event")
			_ = cmd.Process.Kill()
		}
		if protocolError != nil {
			_ = cmd.Process.Kill()
			break
		}
	}
	if scanner.Err() != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if protocolError != nil {
		return protocolError
	}
	if scanner.Err() != nil {
		return scanner.Err()
	}
	if waitErr != nil {
		return fmt.Errorf("provider bridge failed: %w", waitErr)
	}
	if !terminal {
		return errors.New("provider bridge ended without a result")
	}
	return nil
}
