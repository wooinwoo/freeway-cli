// SPDX-License-Identifier: MIT

// Package contracts defines the gateway's versioned wire values.
package contracts

import (
	"bytes"
	"encoding/json"
	"errors"
)

const (
	MaxBody         = 8 * 1024 * 1024
	MaxMessageRunes = 300000
)

type Settings struct {
	MaxTokens   int     `json:"maxTokens"`
	Temperature float64 `json:"temperature"`
}

func (s *Settings) UnmarshalJSON(data []byte) error {
	var input struct {
		MaxTokens   *int     `json:"maxTokens"`
		Temperature *float64 `json:"temperature"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return err
	}
	if input.MaxTokens == nil || input.Temperature == nil {
		return errors.New("settings require maxTokens and temperature")
	}
	*s = Settings{MaxTokens: *input.MaxTokens, Temperature: *input.Temperature}
	return nil
}

type Attachment struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type Prompt struct {
	Text  string       `json:"text"`
	Files []Attachment `json:"files,omitempty"`
}

type ContextSelection struct {
	RecentTurns             int      `json:"recentTurns"`
	IncludePriorAttachments bool     `json:"includePriorAttachments"`
	ExcludedPriorFiles      []string `json:"excludedPriorFiles,omitempty"`
}

type Message struct {
	ID       string       `json:"id"`
	Role     string       `json:"role"`
	Content  string       `json:"content"`
	Status   string       `json:"status"`
	Model    string       `json:"model,omitempty"`
	Provider string       `json:"provider,omitempty"`
	Error    string       `json:"error,omitempty"`
	Files    []Attachment `json:"files,omitempty"`
}

type Conversation struct {
	ID           string            `json:"id,omitempty"`
	Revision     int               `json:"revision,omitempty"`
	Title        string            `json:"title"`
	Model        string            `json:"model"`
	SystemPrompt string            `json:"systemPrompt,omitempty"`
	Settings     Settings          `json:"settings"`
	Context      *ContextSelection `json:"context,omitempty"`
	Messages     []Message         `json:"messages"`
	Draft        string            `json:"draft"`
	DraftFiles   []Attachment      `json:"draftFiles"`
	Queued       []Prompt          `json:"queued"`
	Stash        *Prompt           `json:"stash"`
	Workspace    string            `json:"workspace,omitempty"`
}

type ConversationSummary struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Model        string `json:"model"`
	UpdatedAt    string `json:"updatedAt"`
	MessageCount int    `json:"messageCount"`
	Preview      string `json:"preview"`
	Workspace    string `json:"workspace,omitempty"`
	HasDraft     bool   `json:"hasDraft"`
	QueuedCount  int    `json:"queuedCount"`
	Damaged      bool   `json:"damaged,omitempty"`
}
