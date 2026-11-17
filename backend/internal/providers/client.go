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

type Client struct{ config Config }

// New validates local configuration without starting or contacting the gateway.
func New(config Config) (*Client, error) {
	if config.NodePath == "" {
		config.NodePath = managedNode()
		if config.NodePath == "" {
			var err error
			config.NodePath, err = exec.LookPath("node")
			if err != nil {
				return nil, err
			}
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

// Providers lists public provider metadata without contacting the gateway.
func (c *Client) Providers(ctx context.Context) ([]Provider, error) {
	var result []Provider
	err := c.run(ctx, map[string]any{"action": "providers"}, &result, nil)
	return result, err
}

// Authenticate configures credentials or connects an existing official CLI login.
// A ready connection is not evidence that inference has succeeded.
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

// Models refreshes the provider's free catalog and advertised reasoning choices.
func (c *Client) Models(ctx context.Context, provider string) ([]Model, error) {
	var result []Model
	err := c.run(ctx, map[string]any{"action": "models", "provider": provider}, &result, nil)
	return result, err
}

// Quota reads local observations without issuing an inference request.
func (c *Client) Quota(ctx context.Context, provider string) (Quota, error) {
	var result Quota
	err := c.run(ctx, map[string]any{"action": "quota", "provider": provider}, &result, nil)
	return result, err
}

// Complete collects the same validated response as Stream without a callback.
func (c *Client) Complete(ctx context.Context, request Request) (Completion, error) {
	return c.Stream(ctx, request, nil)
}

// Stream pins the provider and model. Partial text does not imply success:
// callers must still check the returned error, and callbacks should return promptly.
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
