package client

import "context"

type Settings struct {
	MaxTokens   int     `json:"maxTokens"`
	Temperature float64 `json:"temperature"`
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
	ID           string          `json:"id,omitempty"`
	Title        string          `json:"title"`
	Model        string          `json:"model"`
	SystemPrompt string          `json:"systemPrompt"`
	Settings     Settings        `json:"settings"`
	Messages     []Message       `json:"messages"`
	Draft        string          `json:"draft"`
	DraftFiles   []Attachment    `json:"draftFiles"`
	Queued       []PendingPrompt `json:"queued"`
	Workspace    string          `json:"workspace,omitempty"`
	Revision     int             `json:"revision,omitempty"`
}

func (p *POC) PromptHistory(ctx context.Context) ([]string, error) {
	var result []string
	err := p.run(ctx, map[string]any{"action": "prompts"}, func(e Event) { result = e.Prompts })
	return result, err
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
}

func (p *POC) Conversations(ctx context.Context) ([]ConversationSummary, error) {
	var result []ConversationSummary
	err := p.run(ctx, map[string]any{"action": "history"}, func(e Event) { result = e.Conversations })
	return result, err
}

func (p *POC) Conversation(ctx context.Context, id string) (Conversation, error) {
	var result Conversation
	err := p.run(ctx, map[string]any{"action": "open", "id": id}, func(e Event) {
		if e.Conversation != nil {
			result = *e.Conversation
		}
	})
	return result, err
}

func (p *POC) SaveConversation(ctx context.Context, chat Conversation) (Conversation, error) {
	// Updates merge with the stored record. Explicit empty arrays clear old context.
	if chat.DraftFiles == nil {
		chat.DraftFiles = []Attachment{}
	}
	if chat.Queued == nil {
		chat.Queued = []PendingPrompt{}
	}
	if chat.Messages == nil {
		chat.Messages = []Message{}
	}
	var result Conversation
	err := p.run(ctx, map[string]any{"action": "save", "conversation": chat}, func(e Event) {
		if e.Conversation != nil {
			result = *e.Conversation
		}
	})
	return result, err
}

func (p *POC) StreamWithSettings(ctx context.Context, model string, messages []Message, settings Settings, emit func(Event)) error {
	return p.run(ctx, map[string]any{"action": "chat", "model": model, "messages": messages, "settings": settings}, emit)
}

type GatewayError struct {
	Message    string
	Code       string
	Status     int
	RetryAfter int
}

func (e *GatewayError) Error() string { return e.Message }
