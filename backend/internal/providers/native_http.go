package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type httpAdapter struct {
	definition Definition
	client     *http.Client
}

// NewHTTPAdapter connects directly to an OpenAI-compatible vendor API.
func NewHTTPAdapter(d Definition, client *http.Client) Adapter {
	if client == nil {
		client = http.DefaultClient
	}
	return &httpAdapter{definition: d, client: client}
}

func (a *httpAdapter) Close() error { return nil }

// checkKey calls the vendor's KeyCheck path. A public catalog or a fixed allowlist cannot verify a key on its own.
func (a *httpAdapter) checkKey(ctx context.Context, key string) error {
	if err := a.validateKey(key); err != nil {
		return err
	}
	verify := *a
	client := *a.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	verify.client = &client
	reply, err := verify.request(ctx, a.definition.KeyCheck, key, nil)
	if err != nil {
		return err
	}
	defer reply.Body.Close()
	if reply.StatusCode != http.StatusOK {
		return httpStatusError(reply)
	}
	if a.definition.ID != "openrouter" {
		return nil
	}
	var payload struct {
		Data *struct {
			Label           string `json:"label"`
			IsManagementKey bool   `json:"is_management_key"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(reply.Body, 32<<10)).Decode(&payload) != nil || payload.Data == nil || payload.Data.Label == "" {
		return failure("invalid_key_details", http.StatusBadGateway)
	}
	if payload.Data.IsManagementKey {
		return failure("inference_key_required", http.StatusBadRequest)
	}
	return nil
}

func (a *httpAdapter) Models(ctx context.Context, key string) ([]Model, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !a.definition.Catalog {
		models := make([]Model, 0, len(a.definition.Seed))
		for _, id := range a.definition.Seed {
			if model, free := NormalizeModel(a.definition, map[string]any{"id": id}); free {
				models = append(models, model)
			}
		}
		return models, nil
	}
	if err := a.validateKey(key); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	response, err := a.request(ctx, "/models", key, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, httpStatusError(response)
	}
	var catalog any
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8*1024*1024))
	decoder.UseNumber()
	if decoder.Decode(&catalog) != nil {
		return nil, failure("invalid_catalog", 502)
	}
	if object, ok := catalog.(map[string]any); ok {
		catalog, ok = object["data"]
		if !ok {
			catalog = object["models"]
		}
	}
	entries, ok := catalog.([]any)
	if !ok {
		return nil, failure("invalid_catalog", 502)
	}
	models := make([]Model, 0, len(entries))
	for _, entry := range entries {
		if raw, ok := entry.(map[string]any); ok {
			if model, free := NormalizeModel(a.definition, raw); free {
				models = append(models, model)
			}
		}
	}
	return models, nil
}

func (a *httpAdapter) Stream(ctx context.Context, model Model, key string, input Request, emit func(string)) (Completion, error) {
	if err := ctx.Err(); err != nil {
		return Completion{}, err
	}
	if err := a.validateModel(model, input); err != nil {
		return Completion{}, err
	}
	if ImageBytes(input.Messages) > 0 && !model.Vision {
		return Completion{}, failure("unsupported_capability", 400)
	}
	if err := a.validateKey(key); err != nil {
		return Completion{}, err
	}
	maxTokens := input.MaxTokens
	if maxTokens == 0 {
		maxTokens = 2048
	}
	body := map[string]any{
		"model": model.UpstreamID, "messages": input.Messages, "stream": true,
		"stream_options": map[string]any{"include_usage": true}, "max_tokens": maxTokens,
	}
	if input.Temperature != nil {
		body["temperature"] = *input.Temperature
	}
	if input.ReasoningEffort != "" {
		if model.Reasoning.Parameter == "reasoning" {
			body["reasoning"] = map[string]string{"effort": input.ReasoningEffort}
		} else {
			body["reasoning_effort"] = input.ReasoningEffort
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) > 2*1024*1024+ImageBytes(input.Messages) {
		return Completion{}, failure("invalid_request", 400)
	}
	ctx, cancel := context.WithTimeout(ctx, MaxStreamDuration)
	defer cancel()
	started := time.Now()
	response, err := a.request(ctx, "/chat/completions", key, encoded)
	if err != nil {
		return Completion{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Completion{}, httpStatusError(response)
	}
	result := Completion{Model: model.ID, Provider: a.definition.ID, ReasoningEffort: input.ReasoningEffort}
	var content strings.Builder
	appendText := func(text string) {
		if text == "" {
			return
		}
		content.WriteString(text)
		if emit != nil {
			emit(text)
		}
	}
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "json") {
		var completion struct {
			Error   json.RawMessage `json:"error"`
			Usage   json.RawMessage `json:"usage"`
			Choices []struct {
				Message *struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024)).Decode(&completion) != nil {
			if ctx.Err() != nil {
				return Completion{}, ctx.Err()
			}
			return Completion{}, failure("invalid_upstream_response", 502)
		}
		if hasHTTPError(completion.Error) {
			return Completion{}, streamHTTPError(completion.Error)
		}
		if len(completion.Choices) == 0 || completion.Choices[0].Message == nil {
			return Completion{}, failure("invalid_upstream_response", 502)
		}
		appendText(completion.Choices[0].Message.Content)
		result.Usage = completion.Usage
	} else {
		done, sawChoice := false, false
		err := readSSE(response.Body, func(data []byte) error {
			if string(data) == "[DONE]" {
				done = true
				return io.EOF
			}
			var chunk struct {
				Error   json.RawMessage `json:"error"`
				Usage   json.RawMessage `json:"usage"`
				Choices []struct {
					Index int `json:"index"`
					Delta struct {
						Content          string `json:"content"`
						Reasoning        string `json:"reasoning"`
						ReasoningContent string `json:"reasoning_content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if json.Unmarshal(data, &chunk) != nil {
				return failure("invalid_stream", 502)
			}
			if hasHTTPError(chunk.Error) {
				return streamHTTPError(chunk.Error)
			}
			if len(chunk.Usage) > 0 {
				result.Usage = chunk.Usage
			}
			sawChoice = sawChoice || len(chunk.Choices) > 0
			for _, choice := range chunk.Choices {
				if choice.Index != 0 {
					continue
				}
				if choice.Delta.Content == "" && emit != nil && (choice.Delta.Reasoning != "" || choice.Delta.ReasoningContent != "") {
					emit("")
				}
				appendText(choice.Delta.Content)
			}
			return ctx.Err()
		})
		if ctx.Err() != nil {
			return Completion{}, ctx.Err()
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return Completion{}, err
		}
		if !done || !sawChoice {
			return Completion{}, failure("incomplete_stream", 502)
		}
	}
	if ctx.Err() != nil {
		return Completion{}, ctx.Err()
	}
	if strings.TrimSpace(content.String()) == "" {
		return Completion{}, failure("empty_completion", 502)
	}
	result.Content, result.ElapsedMS = content.String(), time.Since(started).Milliseconds()
	return result, nil
}

func (a *httpAdapter) validateKey(key string) error {
	if strings.TrimSpace(key) == "" && a.definition.ID != "kilo" {
		return failure("needs_key", 409)
	}
	if len(key) > 4096 || strings.ContainsAny(key, "\r\n\x00") {
		return failure("invalid_key", 400)
	}
	return nil
}

func (a *httpAdapter) validateModel(model Model, input Request) error {
	if !model.Free {
		return failure("not_free_model", 400)
	}
	if model.UpstreamID == "" || model.Provider != a.definition.ID || model.ID != a.definition.ID+"/"+model.UpstreamID || input.Provider != "" && input.Provider != a.definition.ID {
		return failure("unexpected_route", 400)
	}
	if input.ReasoningEffort != "" && (!slices.Contains(model.Reasoning.Efforts, input.ReasoningEffort) || model.Reasoning.Parameter != "reasoning" && model.Reasoning.Parameter != "reasoning_effort") {
		return failure("unsupported_reasoning", 400)
	}
	if input.MaxTokens < 0 || input.MaxTokens > 32768 {
		return failure("invalid_parameter", 400)
	}
	if input.Temperature != nil && (math.IsNaN(*input.Temperature) || math.IsInf(*input.Temperature, 0) || *input.Temperature < 0 || *input.Temperature > 2) {
		return failure("invalid_parameter", 400)
	}
	if len(input.Messages) == 0 || len(input.Messages) > 200 {
		return failure("invalid_messages", 400)
	}
	for _, message := range input.Messages {
		if !slices.Contains([]string{"system", "developer", "user", "assistant"}, message.Role) {
			return failure("invalid_role", 400)
		}
		if !utf8.ValidString(message.Content) {
			return failure("invalid_messages", 400)
		}
	}
	return nil
}

func (a *httpAdapter) request(ctx context.Context, path, key string, body []byte) (*http.Response, error) {
	method := http.MethodGet
	if body != nil {
		method = http.MethodPost
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.definition.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, failure("invalid_provider_url", 500)
	}
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := a.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, failure("upstream_unavailable", 502)
	}
	return response, nil
}

func httpStatusError(response *http.Response) *Error {
	status := response.StatusCode
	if status < 400 || status > 599 {
		status = 502
	}
	code := "upstream_error"
	if status == 429 {
		code = "rate_limit_exceeded"
		body, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
		if sharedLimit(body) {
			code = "provider_busy"
		}
	}
	return &Error{Code: code, Status: status, RetryAfter: httpRetryAfter(response.Header.Get("Retry-After"), time.Now())}
}

// sharedLimit recognizes a 429 that every user gets: a shared free pool or an overloaded server, not the caller's allowance.
// ponytail: vendor wording, add markers as vendors are seen.
func sharedLimit(body []byte) bool {
	text := strings.ToLower(string(body))
	for _, marker := range []string{"upstream_provider_shared_pool", "rate-limited upstream", "temporarily overloaded", `"code":"1305"`} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func httpRetryAfter(value string, now time.Time) int {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		date, err := http.ParseTime(value)
		if err != nil {
			return 0
		}
		seconds = date.Sub(now).Seconds()
	}
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds >= float64(math.MaxInt) {
		return 0
	}
	return int(math.Ceil(seconds))
}

func hasHTTPError(raw json.RawMessage) bool {
	return len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func streamHTTPError(raw json.RawMessage) *Error {
	var upstream struct {
		Code   string `json:"code"`
		Type   string `json:"type"`
		Status int    `json:"status"`
	}
	_ = json.Unmarshal(raw, &upstream)
	code := strings.ToLower(upstream.Code + " " + upstream.Type)
	if upstream.Status == 429 || strings.Contains(code, "rate_limit") || strings.Contains(code, "quota") {
		return failure("rate_limit_exceeded", 429)
	}
	return failure("upstream_stream_error", 502)
}
