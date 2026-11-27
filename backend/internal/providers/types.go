package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Config struct {
	HTTPClient     *http.Client
	Credentials    CredentialStore
	WorkDir        string
	OpenCodeBinary string
	ClineBinary    string
	// CatalogDir keeps CLI catalogs between gateway runs. Empty disables the cache.
	CatalogDir string
}

// Credential binds a free-account confirmation to one specific API key.
// Key is never included when this value is encoded as JSON.
type Credential struct {
	Key           string `json:"-"`
	FreeConfirmed bool   `json:"freeConfirmed"`
	Disconnected  bool   `json:"disconnected,omitempty"`
	AutoExcluded  bool   `json:"autoExcluded,omitempty"`
}

// CredentialStore is supplied by the backend's existing secret store.
// Load returns a zero value and nil when no credential has been saved.
type CredentialStore interface {
	Load(context.Context, string) (Credential, error)
	Save(context.Context, string, Credential) error
}

type Provider struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Deprecated: use Transport and Protocol to identify the connection format.
	Kind        string `json:"kind"`
	AuthURL     string `json:"authUrl"`
	Env         string `json:"env"`
	Transport   string `json:"transport"`
	Protocol    string `json:"protocol"`
	RequiresCLI bool   `json:"requiresCLI"`
}

type AuthOptions struct {
	// Key is passed directly to the adapter, never to CLI arguments. Empty preserves existing credentials.
	Key          string `json:"key,omitempty"`
	ConfirmFree  bool   `json:"confirmFree,omitempty"`
	ResumeCLI    bool   `json:"resumeCLI,omitempty"`
	RequireReady bool   `json:"-"`
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
	ID           string    `json:"id"`
	Provider     string    `json:"provider"`
	UpstreamID   string    `json:"upstreamId"`
	Name         string    `json:"name"`
	Free         bool      `json:"free"`
	Context      int       `json:"context,omitempty"`
	DiscoveredAt time.Time `json:"discoveredAt"`
	Ready        bool      `json:"ready"`
	Reason       string    `json:"reason"`
	Reasoning    Reasoning `json:"reasoning"`
	Vision       bool      `json:"vision,omitempty"`
}

type Bucket struct {
	Requests        int `json:"requests"`
	Tokens          int `json:"tokens"`
	EstimatedTokens int `json:"estimatedTokens"`
}

type Quota struct {
	Provider            string            `json:"provider"`
	KeyConfigured       bool              `json:"keyConfigured"`
	Disconnected        bool              `json:"disconnected,omitempty"`
	AutoExcluded        bool              `json:"autoExcluded,omitempty"`
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
	// Images are data URLs, sent only to routes whose catalog says they read images.
	Images []string `json:"images,omitempty"`
}

// MarshalJSON writes images as OpenAI content parts, the form vision routes accept.
func (m Message) MarshalJSON() ([]byte, error) {
	if len(m.Images) == 0 {
		return json.Marshal(struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{m.Role, m.Content})
	}
	parts := []any{map[string]string{"type": "text", "text": m.Content}}
	for _, image := range m.Images {
		parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]string{"url": image}})
	}
	return json.Marshal(map[string]any{"role": m.Role, "content": parts})
}

// ImageBytes is the size of a request's images, which a request may add to its text limit.
func ImageBytes(messages []Message) int {
	total := 0
	for _, message := range messages {
		for _, image := range message.Images {
			total += len(image)
		}
	}
	return total
}

// MaxStreamDuration caps one reply. Routes that stop producing output end sooner.
const MaxStreamDuration = 15 * time.Minute

// Stream callbacks receive reply text. An empty string reports reasoning progress
// without visible text, so callers can tell a thinking model from a stalled one.
type Request struct {
	Provider        string    `json:"provider"`
	Model           string    `json:"model"`
	Messages        []Message `json:"messages"`
	MaxTokens       int       `json:"maxTokens,omitempty"`
	ReasoningEffort string    `json:"reasoningEffort,omitempty"`
	Temperature     *float64  `json:"temperature,omitempty"`
	Automatic       bool      `json:"-"`
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
