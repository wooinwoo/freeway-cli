package client

import "encoding/json"

type Settings struct {
	MaxTokens   int     `json:"maxTokens"`
	Temperature float64 `json:"temperature"`
}

type ContextSelection struct {
	RecentTurns             int      `json:"recentTurns"`
	IncludePriorAttachments bool     `json:"includePriorAttachments"`
	ExcludedPriorFiles      []string `json:"excludedPriorFiles,omitempty"`
}

type Attachment struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}
type PendingPrompt struct {
	Text  string       `json:"text"`
	Files []Attachment `json:"files,omitempty"`
}
type Usage struct {
	TotalTokens int `json:"total_tokens"`
}

type Conversation struct {
	SaveKey      string            `json:"-"`
	ID           string            `json:"id,omitempty"`
	Title        string            `json:"title"`
	Model        string            `json:"model"`
	SystemPrompt string            `json:"systemPrompt"`
	Settings     Settings          `json:"settings"`
	Context      *ContextSelection `json:"context,omitempty"`
	Messages     []Message         `json:"messages"`
	Draft        string            `json:"draft"`
	DraftFiles   []Attachment      `json:"draftFiles"`
	Queued       []PendingPrompt   `json:"queued"`
	Stash        *PendingPrompt    `json:"stash"`
	Workspace    string            `json:"workspace,omitempty"`
	Revision     int               `json:"revision,omitempty"`
}

func (chat Conversation) wire() Conversation {
	if chat.Messages == nil {
		chat.Messages = []Message{}
	}
	if chat.DraftFiles == nil {
		chat.DraftFiles = []Attachment{}
	}
	if chat.Queued == nil {
		chat.Queued = []PendingPrompt{}
	}
	return chat
}

func (chat Conversation) SaveSize() (int, error) {
	data, err := json.Marshal(chat.wire())
	return len(data), err
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

type GatewayError struct {
	Message         string
	Code            string
	Status          int
	RetryAfter      int
	MaxOutputTokens *int
}

func (e *GatewayError) Error() string { return e.Message }
