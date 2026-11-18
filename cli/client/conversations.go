package client

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
	Stash        *PendingPrompt  `json:"stash"`
	Workspace    string          `json:"workspace,omitempty"`
	Revision     int             `json:"revision,omitempty"`
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

type GatewayError struct {
	Message    string
	Code       string
	Status     int
	RetryAfter int
}

func (e *GatewayError) Error() string { return e.Message }
