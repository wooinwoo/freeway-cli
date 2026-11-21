// SPDX-License-Identifier: MIT

package contracts

import (
	"bytes"
	"encoding/json"
	"errors"
)

type Model struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Detail               string `json:"detail,omitempty"`
	Free                 bool   `json:"free"`
	Ready                bool   `json:"ready"`
	LocalTokensRemaining *int   `json:"localTokensRemaining,omitempty"`
}

type ProviderStatus struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	KeyConfigured        bool   `json:"keyConfigured"`
	State                string `json:"state"`
	FreeModels           int    `json:"freeModels"`
	RetryAfter           int    `json:"retryAfter,omitempty"`
	LocalTokensRemaining *int   `json:"localTokensRemaining,omitempty"`
}

type InputMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (m *InputMessage) UnmarshalJSON(data []byte) error {
	var input struct {
		Role    *string `json:"role"`
		Content *string `json:"content"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return err
	}
	if input.Role == nil || input.Content == nil {
		return errors.New("completion messages require role and content")
	}
	*m = InputMessage{Role: *input.Role, Content: *input.Content}
	return nil
}

type CompletionRequest struct {
	Model       string         `json:"model"`
	Messages    []InputMessage `json:"messages"`
	Stream      bool           `json:"stream"`
	MaxTokens   *int           `json:"max_tokens,omitempty"`
	Temperature *float64       `json:"temperature,omitempty"`
}

type Completion struct {
	Model    string
	Provider string
	Usage    json.RawMessage
}

type TextDelta struct {
	Model    string
	Provider string
	Text     string
}

// Problem is safe to send to clients. Upstream diagnostics must be translated to this type.
type Problem struct {
	Code            string `json:"code"`
	Message         string `json:"message"`
	Status          int    `json:"status"`
	RetryAfter      int    `json:"retryAfter,omitempty"`
	MaxOutputTokens *int   `json:"maxOutputTokens,omitempty"`
}

func (p *Problem) Error() string { return p.Message }
