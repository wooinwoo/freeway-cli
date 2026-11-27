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
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

const MaxBody = 8 * 1024 * 1024
const CatalogTimeout = 25 * time.Second

type Model struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Detail               string   `json:"detail,omitempty"`
	RouteIDs             []string `json:"routeIds,omitempty"`
	ReadyProviders       []string `json:"readyProviders,omitempty"`
	QualityRank          int      `json:"qualityRank,omitempty"`
	CodingRank           int      `json:"codingRank,omitempty"`
	ReasoningRank        int      `json:"reasoningRank,omitempty"`
	Free                 bool     `json:"free"`
	Ready                bool     `json:"ready"`
	LocalTokensRemaining *int     `json:"localTokensRemaining,omitempty"`
}

type ProviderStatus struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	KeyConfigured        bool   `json:"keyConfigured"`
	CatalogChecked       bool   `json:"catalogChecked,omitempty"`
	KeyChecked           bool   `json:"keyChecked,omitempty"`
	ScopedCheck          bool   `json:"scopedCheck,omitempty"`
	State                string `json:"state"`
	FreeModels           int    `json:"freeModels"`
	RetryAfter           int    `json:"retryAfter,omitempty"`
	Management           bool   `json:"management,omitempty"`
	AutoSupported        bool   `json:"autoSupported,omitempty"`
	AutoExcluded         bool   `json:"autoExcluded,omitempty"`
	CheckedAt            string `json:"checkedAt,omitempty"`
	LocalRequests        int    `json:"localRequests,omitempty"`
	ProviderRemaining    *int   `json:"providerRemaining,omitempty"`
	CLIProfile           string `json:"cliProfile,omitempty"`
	LocalTokensRemaining *int   `json:"localTokensRemaining,omitempty"`
}

type Message struct {
	ID       string       `json:"id,omitempty"`
	Role     string       `json:"role"`
	Content  string       `json:"content"`
	Status   string       `json:"status,omitempty"`
	Model    string       `json:"model,omitempty"`
	Provider string       `json:"provider,omitempty"`
	Error    string       `json:"error,omitempty"`
	Files    []Attachment `json:"files,omitempty"`
	// Images are data URLs read from disk for one request. They are never saved.
	Images []string `json:"-"`
}

type Event struct {
	Type            string `json:"type"`
	Text            string `json:"text"`
	Model           string `json:"model"`
	Provider        string `json:"provider"`
	Error           string `json:"error"`
	Usage           *Usage `json:"usage,omitempty"`
	MaxOutputTokens *int   `json:"maxOutputTokens,omitempty"`
}

type Config struct {
	URL        string
	APIKey     string
	HTTPClient *http.Client
	Managed    bool
}

// HTTP connects only to an authenticated loopback gateway. It owns no user storage or routing policy.
type HTTP struct {
	base         string
	key          string
	client       *http.Client
	managed      bool
	features     atomic.Uint32
	featuresSeen atomic.Bool
	supervisor   *gatewaySupervisor
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
	return &HTTP{base: strings.TrimRight(u.String(), "/"), key: config.APIKey, client: &copyClient, managed: config.Managed}, nil
}

func (c *HTTP) Close() {
	if c.supervisor != nil {
		c.supervisor.close()
		return
	}
	c.client.CloseIdleConnections()
}

// Lease keeps an automatically started gateway alive until this client exits.
func (c *HTTP) Lease(ctx context.Context) (func(), error) {
	ctx, cancel := context.WithCancel(ctx)
	startup := time.AfterFunc(5*time.Second, cancel)
	response, err := c.request(ctx, http.MethodGet, "/v1/lease", nil, "")
	timely := startup.Stop()
	if err != nil {
		cancel()
		return nil, err
	}
	if !timely || response.StatusCode != http.StatusOK || response.Header.Get("X-Freeway-API") != "1" {
		cancel()
		response.Body.Close()
		return nil, errors.New("the local gateway did not acknowledge the connection")
	}
	return func() {
		cancel()
		_ = response.Body.Close()
	}, nil
}

func (c *HTTP) request(ctx context.Context, method, path string, body any, saveKey string) (*http.Response, error) {
	if c.supervisor != nil {
		return c.supervisor.request(ctx, method, path, body, saveKey)
	}
	if c.key == "" {
		return nil, &GatewayError{Message: "Set FREEWAY_API_KEY to the selected gateway's client key.", Code: "gateway_auth_required", Status: 401}
	}
	if chat, ok := body.(Conversation); ok && chat.Context != nil {
		if !c.SupportsContextSelection() {
			return nil, &GatewayError{Code: "context_settings_unavailable", Status: 409, Message: "This gateway cannot save the conversation's context settings. Reconnect with an updated gateway."}
		}
		if len(chat.Context.ExcludedPriorFiles) > 0 && !c.SupportsContextFileSelection() {
			return nil, &GatewayError{Code: "context_settings_unavailable", Status: 409, Message: "Reconnect with an updated gateway to save individual attachment exclusions."}
		}
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
	if saveKey != "" && c.supportsSaveKeys() {
		req.Header.Set("Idempotency-Key", saveKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &GatewayError{Message: "Cannot reach the local gateway. Check FREEWAY_URL or reopen Freeway.", Code: "gateway_unavailable", Status: 503}
	}
	if resp.Header.Get("X-Freeway-Features") != "" || path == "/v1/lease" || path == "/v1/status" {
		c.features.Store(gatewayFeatures(resp.Header.Get("X-Freeway-Features")))
		c.featuresSeen.Store(true)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		var payload struct {
			Error struct {
				Message         string `json:"message"`
				Code            string `json:"code"`
				RetryAfter      int    `json:"retryAfter"`
				MaxOutputTokens *int   `json:"maxOutputTokens"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&payload)
		if payload.Error.Message == "" {
			payload.Error.Message = fmt.Sprintf("Gateway request failed (HTTP %d).", resp.StatusCode)
		}
		return nil, &GatewayError{Message: payload.Error.Message, Code: payload.Error.Code, Status: resp.StatusCode, RetryAfter: payload.Error.RetryAfter, MaxOutputTokens: payload.Error.MaxOutputTokens}
	}
	return resp, nil
}

func (c *HTTP) json(ctx context.Context, method, path string, body, out any, saveKey string) error {
	timeout := 12 * time.Second
	if path == "/v1/models?refresh=1" {
		timeout = 45 * time.Second
	} else if path == "/v1/models" || path == "/v1/providers" || strings.HasPrefix(path, "/v1/providers?") {
		timeout = CatalogTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := c.request(ctx, method, path, body, saveKey)
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
	return c.models(ctx, false)
}

func (c *HTTP) RefreshModels(ctx context.Context) ([]Model, error) {
	return c.models(ctx, c.isManaged())
}

// isManaged follows the supervised endpoint, whose handle changes after a reconnect.
func (c *HTTP) isManaged() bool {
	active := c.endpoint()
	return active != nil && active.managed
}

func (c *HTTP) models(ctx context.Context, refresh bool) ([]Model, error) {
	path := "/v1/models"
	if refresh {
		path += "?refresh=1"
	}
	var result struct {
		Data      []Model `json:"data"`
		Refreshed bool    `json:"refreshed"`
	}
	if err := c.json(ctx, "GET", path, nil, &result, ""); err != nil {
		return nil, err
	}
	if refresh && !result.Refreshed {
		// A gateway from before the update can still be serving other windows.
		if _, err := c.ProviderStatuses(ctx); err != nil {
			return nil, err
		}
		return c.Models(ctx)
	}
	models := []Model{}
	for _, model := range result.Data {
		if model.Free && model.Ready {
			models = append(models, model)
		}
	}
	if len(models) == 0 {
		return models, &GatewayError{Message: "No free models are ready. Press F4 to check providers.", Code: "no_free_models", Status: 503}
	}
	return models, nil
}

func openCodeRequiresLocal() error {
	return &GatewayError{Message: "OpenCode is available through the automatically started Freeway gateway.", Code: "local_client_required", Status: 403}
}

func (c *HTTP) ProviderStatuses(ctx context.Context) ([]ProviderStatus, error) {
	return c.providerStatuses(ctx, "")
}

func (c *HTTP) ProviderStatus(ctx context.Context, id string) (ProviderStatus, error) {
	if id == "" {
		return ProviderStatus{}, errors.New("provider ID is required")
	}
	statuses, err := c.providerStatuses(ctx, "?provider="+url.QueryEscape(id))
	if err != nil {
		return ProviderStatus{}, err
	}
	// Older gateways ignore the query parameter and return the full list.
	for _, status := range statuses {
		if status.ID == id {
			return status, nil
		}
	}
	return ProviderStatus{}, &GatewayError{Code: "unknown_provider", Status: http.StatusNotFound, Message: "This provider is disabled in the gateway."}
}

func (c *HTTP) providerStatuses(ctx context.Context, query string) ([]ProviderStatus, error) {
	var result struct {
		Data []ProviderStatus `json:"data"`
	}
	err := c.json(ctx, "GET", "/v1/providers"+query, nil, &result, "")
	return result.Data, err
}

func (c *HTTP) ChangeProvider(ctx context.Context, id, action, key string, confirmFree bool) error {
	if !c.isManaged() {
		return &GatewayError{Message: "Provider management requires the local Freeway client.", Code: "local_client_required", Status: 403}
	}
	if id == "" || strings.ContainsAny(id, "/\\?#") {
		return errors.New("invalid provider ID")
	}
	body := struct {
		Action      string `json:"action"`
		APIKey      string `json:"apiKey,omitempty"`
		ConfirmFree bool   `json:"confirmFree,omitempty"`
	}{Action: action, APIKey: key, ConfirmFree: confirmFree}
	ctx, cancel := context.WithTimeout(ctx, CatalogTimeout)
	defer cancel()
	response, err := c.request(ctx, http.MethodPost, "/v1/local/providers/"+url.PathEscape(id)+"/management", body, "")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return errors.New("gateway did not confirm the provider change")
	}
	return nil
}

func (c *HTTP) Conversations(ctx context.Context) ([]ConversationSummary, error) {
	var result struct {
		Data []ConversationSummary `json:"data"`
	}
	err := c.json(ctx, "GET", "/v1/conversations", nil, &result, "")
	sort.SliceStable(result.Data, func(i, j int) bool { return result.Data[i].UpdatedAt > result.Data[j].UpdatedAt })
	return result.Data, err
}

func (c *HTTP) Conversation(ctx context.Context, id string) (Conversation, error) {
	var result Conversation
	if id == "" || strings.ContainsAny(id, "/\\?#") {
		return result, errors.New("invalid conversation ID")
	}
	err := c.json(ctx, "GET", "/v1/conversations/"+url.PathEscape(id), nil, &result, "")
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
	if chat.SaveKey != "" || chat.Context != nil {
		c.probeGatewayFeatures(ctx)
	}
	keySupported := c.supportsSaveKeys()
	var result Conversation
	err := c.json(ctx, method, path, chat.wire(), &result, chat.SaveKey)
	var rejected *GatewayError
	if errors.As(err, &rejected) && rejected.Status == http.StatusBadRequest && rejected.Code == "invalid_request" &&
		slices.ContainsFunc(chat.Messages, func(message Message) bool { return message.Provider != "" }) {
		// Older v1 gateways reject unknown fields before changing the conversation.
		chat.Messages = slices.Clone(chat.Messages)
		for i := range chat.Messages {
			chat.Messages[i].Provider = ""
		}
		err = c.json(ctx, method, path, chat.wire(), &result, chat.SaveKey)
	}
	if err == nil && (result.ID == "" || result.Revision < 1 || (chat.ID != "" && result.ID != chat.ID)) {
		err = errors.New("gateway returned an invalid conversation identity")
	}
	if err != nil && chat.SaveKey != "" && (!keySupported || !c.supportsSaveKeys()) {
		var gateway *GatewayError
		if !errors.As(err, &gateway) || gateway.Code == "gateway_unavailable" {
			err = &GatewayError{Code: "save_uncertain", Message: "The gateway did not confirm this save. Use /recover to keep a copy before switching conversations."}
		}
	}
	return result, err
}

func (c *HTTP) PromptHistory(ctx context.Context) ([]string, error) {
	return c.ScopedPromptHistory(ctx, "")
}

func (c *HTTP) probeGatewayFeatures(ctx context.Context) {
	if active := c.endpoint(); active != nil && !active.featuresSeen.Load() {
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if response, err := c.request(probeCtx, http.MethodGet, "/v1/status", nil, ""); err == nil {
			response.Body.Close()
		}
	}
}

func (c *HTTP) ScopedPromptHistory(ctx context.Context, workspace string) ([]string, error) {
	if workspace != "" {
		c.probeGatewayFeatures(ctx)
	}
	if c.hasFeature(featurePromptHistory) && (workspace == "" || c.hasFeature(featureHistoryPages)) {
		var result struct {
			Data       []string `json:"data"`
			Incomplete bool     `json:"incomplete"`
		}
		path := "/v1/prompts"
		if workspace != "" {
			path += "?" + url.Values{"workspace": {workspace}}.Encode()
		}
		err := c.json(ctx, http.MethodGet, path, nil, &result, "")
		var gateway *GatewayError
		if (workspace == "" || c.hasFeature(featureHistoryPages)) &&
			(!errors.As(err, &gateway) || gateway.Status != http.StatusNotFound) {
			if err == nil && result.Incomplete {
				err = errIncompletePrompts
			}
			return result.Data, err
		}
	}
	records, err := c.Conversations(ctx)
	if err != nil {
		return nil, err
	}
	var prompts []string
	var partial error
	size := 0
	scanned := 0
collect:
	for _, record := range records {
		if workspace != "" && (record.Workspace == "" || !SameWorkspace(record.Workspace, workspace)) {
			continue
		}
		if scanned >= 30 {
			break
		}
		scanned++
		if record.Damaged {
			partial = errIncompletePrompts
			continue
		}
		chat, err := c.Conversation(ctx, record.ID)
		if err != nil {
			partial = err
			if ctx.Err() != nil {
				break
			}
			continue
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
	return prompts, partial
}

var errIncompletePrompts = errors.New("some saved conversations could not be searched")

func (c *HTTP) Stream(ctx context.Context, model string, messages []Message, emit func(Event)) error {
	return c.StreamWithSettings(ctx, model, messages, Settings{MaxTokens: 4096, Temperature: 0.6}, emit)
}

const attachmentPrefix = "\n\nAttached file (reference data):\n--- "
const attachmentDivider = " ---\n"
const attachmentEnd = "\n--- end file ---"

// InputTokenEstimate mirrors the gateway's conservative local byte budget.
func InputTokenEstimate(message Message) int {
	total := len(message.Content) + 16
	for _, file := range message.Files {
		total += len(attachmentPrefix) + len(file.Path) + len(attachmentDivider) + len(file.Content) + len(attachmentEnd)
	}
	return total
}

func (c *HTTP) StreamWithSettings(ctx context.Context, model string, messages []Message, settings Settings, emit func(Event)) error {
	type prompt struct {
		Role    string   `json:"role"`
		Content string   `json:"content"`
		Images  []string `json:"images,omitempty"`
	}
	prompts := make([]prompt, 0, len(messages))
	for _, message := range messages {
		content := message.Content
		for _, file := range message.Files {
			content += attachmentPrefix + file.Path + attachmentDivider + file.Content + attachmentEnd
		}
		prompts = append(prompts, prompt{message.Role, content, message.Images})
	}
	body := struct {
		Model       string   `json:"model"`
		Messages    []prompt `json:"messages"`
		Stream      bool     `json:"stream"`
		MaxTokens   int      `json:"max_tokens"`
		Temperature float64  `json:"temperature"`
	}{model, prompts, true, settings.MaxTokens, settings.Temperature}
	if strings.HasPrefix(model, "opencode/") && !c.isManaged() {
		return openCodeRequiresLocal()
	}
	resp, err := c.request(ctx, "POST", "/v1/chat/completions", body, "")
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
				Message         string `json:"message"`
				Code            string `json:"code"`
				Status          int    `json:"status"`
				RetryAfter      int    `json:"retryAfter"`
				MaxOutputTokens *int   `json:"maxOutputTokens"`
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
			return &GatewayError{Message: chunk.Error.Message, Code: chunk.Error.Code, Status: chunk.Error.Status, RetryAfter: chunk.Error.RetryAfter, MaxOutputTokens: chunk.Error.MaxOutputTokens}
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
					emit(Event{Type: "text", Text: choice.Delta.Content, Model: final.Model, Provider: final.Provider})
				}
			}
		}
		return nil
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), MaxBody)
	var event strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if event.Len() > 0 {
				if err := consume(strings.TrimSuffix(event.String(), "\n")); err != nil {
					return err
				}
				event.Reset()
				if finished {
					break
				}
			}
		} else if strings.HasPrefix(line, "data:") {
			value := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
			if event.Len()+len(value)+1 > MaxBody {
				return errors.New("gateway stream event exceeds 8 MB")
			}
			event.WriteString(value)
			event.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !finished && event.Len() > 0 {
		if err := consume(strings.TrimSuffix(event.String(), "\n")); err != nil {
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
