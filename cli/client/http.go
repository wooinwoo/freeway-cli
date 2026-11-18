package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const MaxBody = 8 * 1024 * 1024

type Model struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	Free   bool   `json:"free"`
	Ready  bool   `json:"ready"`
}

type Message struct {
	ID      string       `json:"id,omitempty"`
	Role    string       `json:"role"`
	Content string       `json:"content"`
	Status  string       `json:"status,omitempty"`
	Model   string       `json:"model,omitempty"`
	Error   string       `json:"error,omitempty"`
	Files   []Attachment `json:"files,omitempty"`
}

type Event struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Model    string `json:"model"`
	Provider string `json:"provider"`
	Error    string `json:"error"`
	Usage    *Usage `json:"usage,omitempty"`
}

type Config struct {
	URL        string
	APIKey     string
	HTTPClient *http.Client
}

// HTTP connects only to an authenticated loopback gateway. It owns no user storage or routing policy.
type HTTP struct {
	base   string
	key    string
	client *http.Client
}

func NewHTTP(config Config) (*HTTP, error) {
	if config.URL == "" {
		config.URL = "http://127.0.0.1:4141"
	}
	u, err := url.Parse(config.URL)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("gateway URL must be an HTTP loopback address without a path or credentials")
	}
	host := u.Hostname()
	if host != "localhost" && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()) {
		return nil, errors.New("gateway URL must point to localhost or a loopback IP")
	}
	if strings.ContainsAny(config.APIKey, "\r\n") {
		return nil, errors.New("invalid gateway API key")
	}
	c := config.HTTPClient
	if c == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			if host == "localhost" {
				host = "127.0.0.1"
			}
			if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
				return nil, errors.New("non-loopback connection rejected")
			}
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(host, port))
		}
		c = &http.Client{Transport: transport}
	}
	copyClient := *c
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HTTP{base: strings.TrimRight(u.String(), "/"), key: config.APIKey, client: &copyClient}, nil
}

func (c *HTTP) Close() { c.client.CloseIdleConnections() }

func (c *HTTP) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	if c.key == "" {
		return nil, &GatewayError{Message: "Set FREEWAY_API_KEY to the local gateway's client key. The Go backend is required for connected mode.", Code: "gateway_auth_required", Status: 401}
	}
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	if len(data) > MaxBody {
		return nil, &GatewayError{Message: "Request exceeds 8 MB; export this conversation and start a new one.", Code: "request_too_large", Status: 413}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &GatewayError{Message: "Cannot reach the local Go gateway. Start freewayd or check FREEWAY_URL.", Code: "gateway_unavailable", Status: 503}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		var payload struct {
			Error struct {
				Message    string `json:"message"`
				Code       string `json:"code"`
				RetryAfter int    `json:"retryAfter"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&payload)
		if payload.Error.Message == "" {
			payload.Error.Message = fmt.Sprintf("Gateway request failed (HTTP %d).", resp.StatusCode)
		}
		return nil, &GatewayError{Message: payload.Error.Message, Code: payload.Error.Code, Status: resp.StatusCode, RetryAfter: payload.Error.RetryAfter}
	}
	return resp, nil
}

func (c *HTTP) json(ctx context.Context, method, path string, body, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	resp, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		return err
	}
	if len(data) > MaxBody {
		return errors.New("gateway response exceeds 8 MB")
	}
	return json.Unmarshal(data, out)
}

func (c *HTTP) Models(ctx context.Context) ([]Model, error) {
	var result struct {
		Data []Model `json:"data"`
	}
	if err := c.json(ctx, "GET", "/v1/models", nil, &result); err != nil {
		return nil, err
	}
	models := []Model{}
	for _, model := range result.Data {
		if model.Free && model.Ready {
			models = append(models, model)
		}
	}
	if len(models) == 0 {
		return nil, &GatewayError{Message: "No free models are ready. Connect a provider in the gateway and retry.", Code: "no_free_models", Status: 503}
	}
	return models, nil
}

func (c *HTTP) Conversations(ctx context.Context) ([]ConversationSummary, error) {
	var result struct {
		Data []ConversationSummary `json:"data"`
	}
	err := c.json(ctx, "GET", "/v1/conversations", nil, &result)
	sort.SliceStable(result.Data, func(i, j int) bool { return result.Data[i].UpdatedAt > result.Data[j].UpdatedAt })
	return result.Data, err
}

func (c *HTTP) Conversation(ctx context.Context, id string) (Conversation, error) {
	var result Conversation
	if id == "" || strings.ContainsAny(id, "/\\?#") {
		return result, errors.New("invalid conversation ID")
	}
	err := c.json(ctx, "GET", "/v1/conversations/"+url.PathEscape(id), nil, &result)
	return result, err
}

func (c *HTTP) SaveConversation(ctx context.Context, chat Conversation) (Conversation, error) {
	path, method := "/v1/conversations", "POST"
	if chat.ID != "" {
		if strings.ContainsAny(chat.ID, "/\\?#") {
			return Conversation{}, errors.New("invalid conversation ID")
		}
		path += "/" + url.PathEscape(chat.ID)
		method = "PUT"
	}
	if chat.Messages == nil {
		chat.Messages = []Message{}
	}
	if chat.DraftFiles == nil {
		chat.DraftFiles = []Attachment{}
	}
	if chat.Queued == nil {
		chat.Queued = []PendingPrompt{}
	}
	var result Conversation
	err := c.json(ctx, method, path, chat, &result)
	if err == nil && (result.ID == "" || result.Revision < 1 || (chat.ID != "" && result.ID != chat.ID)) {
		err = errors.New("gateway returned an invalid conversation identity")
	}
	return result, err
}

func (c *HTTP) PromptHistory(ctx context.Context) ([]string, error) {
	records, err := c.Conversations(ctx)
	if err != nil {
		return nil, err
	}
	var prompts []string
	size := 0
collect:
	for i, record := range records {
		if i >= 30 {
			break
		}
		chat, err := c.Conversation(ctx, record.ID)
		if err != nil {
			return nil, err
		}
		for j := len(chat.Messages) - 1; j >= 0; j-- {
			message := chat.Messages[j]
			if message.Role != "user" {
				continue
			}
			if size+len(message.Content) > 600000 || len(prompts) >= 200 {
				break collect
			}
			prompts = append(prompts, message.Content)
			size += len(message.Content)
		}
	}
	for i, j := 0, len(prompts)-1; i < j; i, j = i+1, j-1 {
		prompts[i], prompts[j] = prompts[j], prompts[i]
	}
	return prompts, nil
}

func (c *HTTP) Stream(ctx context.Context, model string, messages []Message, emit func(Event)) error {
	return c.StreamWithSettings(ctx, model, messages, Settings{MaxTokens: 4096, Temperature: 0.6}, emit)
}

func (c *HTTP) StreamWithSettings(ctx context.Context, model string, messages []Message, settings Settings, emit func(Event)) error {
	type prompt struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	prompts := make([]prompt, 0, len(messages))
	for _, message := range messages {
		content := message.Content
		for _, file := range message.Files {
			content += "\n\nAttached file (reference data):\n--- " + file.Path + " ---\n" + file.Content + "\n--- end file ---"
		}
		prompts = append(prompts, prompt{message.Role, content})
	}
	body := struct {
		Model       string   `json:"model"`
		Messages    []prompt `json:"messages"`
		Stream      bool     `json:"stream"`
		MaxTokens   int      `json:"max_tokens"`
		Temperature float64  `json:"temperature"`
	}{model, prompts, true, settings.MaxTokens, settings.Temperature}
	resp, err := c.request(ctx, "POST", "/v1/chat/completions", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return errors.New("gateway must return an SSE stream")
	}
	var content strings.Builder
	var final Event
	final.Type = "done"
	finished := false
	consume := func(data string) error {
		if data == "[DONE]" {
			finished = true
			return nil
		}
		var chunk struct {
			Model    string `json:"model"`
			Provider string `json:"provider"`
			Usage    *Usage `json:"usage"`
			Error    *struct {
				Message string `json:"message"`
				Code    string `json:"code"`
				Status  int    `json:"status"`
			} `json:"error"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return errors.New("invalid gateway stream event")
		}
		if chunk.Error != nil {
			return &GatewayError{Message: chunk.Error.Message, Code: chunk.Error.Code, Status: chunk.Error.Status}
		}
		if chunk.Model != "" {
			final.Model = chunk.Model
		}
		if chunk.Provider != "" {
			final.Provider = chunk.Provider
		}
		if chunk.Usage != nil {
			final.Usage = chunk.Usage
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				content.WriteString(choice.Delta.Content)
				if content.Len() > MaxBody {
					return errors.New("response exceeds 8 MB")
				}
				if emit != nil {
					emit(Event{Type: "text", Text: choice.Delta.Content})
				}
			}
		}
		return nil
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), MaxBody)
	var lines []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(lines) > 0 {
				if err := consume(strings.Join(lines, "\n")); err != nil {
					return err
				}
				lines = nil
				if finished {
					break
				}
			}
		} else if strings.HasPrefix(line, "data:") {
			lines = append(lines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !finished && len(lines) > 0 {
		if err := consume(strings.Join(lines, "\n")); err != nil {
			return err
		}
	}
	if !finished {
		return errors.New("response ended before completion")
	}
	if strings.TrimSpace(content.String()) == "" {
		return errors.New("model returned no text response")
	}
	if final.Model == "" {
		return errors.New("gateway omitted the actual response model")
	}
	final.Text = content.String()
	if emit != nil {
		emit(final)
	}
	return nil
}
