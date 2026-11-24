package providers

import (
	"embed"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed */auth.json
var authMetadata embed.FS

var definitions = []Definition{
	{Provider: Provider{ID: "kilo", Name: "Kilo", Env: "KILO_API_KEY"}, BaseURL: "https://api.kilo.ai/api/gateway", Catalog: true, Proof: "catalog-zero", Priority: 10, Limits: map[string]int{"hour": 200}},
	{Provider: Provider{ID: "opencode", Name: "OpenCode"}, Proof: "catalog-zero", Priority: 20},
	{Provider: Provider{ID: "cline", Name: "Cline CLI"}, Proof: "cli-free-catalog", Priority: 21},
	{Provider: Provider{ID: "nvidia", Name: "NVIDIA NIM", Env: "NVIDIA_API_KEY"}, BaseURL: "https://integrate.api.nvidia.com/v1", Catalog: true, Proof: "free-prototype", NeedsConfirmation: true, Priority: 30, Limits: map[string]int{"minute": 40}},
	{Provider: Provider{ID: "zai", Name: "Z.ai", Env: "ZAI_API_KEY"}, BaseURL: "https://api.z.ai/api/paas/v4", Proof: "allowlist", Seed: []string{"glm-4.7-flash", "glm-4.5-flash"}, Priority: 40, KeyCheck: "/models"},
	{Provider: Provider{ID: "requesty", Name: "Requesty", Env: "REQUESTY_API_KEY"}, BaseURL: "https://router.requesty.ai/v1", Catalog: true, Proof: "catalog-zero", Priority: 50, Limits: map[string]int{"day": 200}},
	{Provider: Provider{ID: "vikasit", Name: "Vikasit", Env: "VIKASIT_API_KEY"}, BaseURL: "https://api.vikasit.ai/v1", Proof: "allowlist", Seed: []string{"vikasit-nova"}, NeedsConfirmation: true, Priority: 60, Limits: map[string]int{"dayTokens": 2000000}},
	{Provider: Provider{ID: "openrouter", Name: "OpenRouter", Env: "OPENROUTER_API_KEY"}, BaseURL: "https://openrouter.ai/api/v1", Catalog: true, Proof: "suffix-free", Priority: 70, Limits: map[string]int{"minute": 20, "day": 50}, KeyCheck: "/key"},
	{Provider: Provider{ID: "groq", Name: "Groq", Env: "GROQ_API_KEY"}, BaseURL: "https://api.groq.com/openai/v1", Proof: "free-account", Seed: []string{"openai/gpt-oss-120b", "openai/gpt-oss-20b"}, NeedsConfirmation: true, Priority: 80, Limits: map[string]int{"minute": 30, "day": 1000, "dayTokens": 200000}},
	{Provider: Provider{ID: "gemini_api", Name: "Gemini API", Env: "GEMINI_API_KEY"}, BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", Proof: "free-account", Seed: []string{"gemini-2.5-flash", "gemini-2.5-flash-lite"}, NeedsConfirmation: true, Priority: 90},
	{Provider: Provider{ID: "novita", Name: "Novita", Env: "NOVITA_API_KEY"}, BaseURL: "https://api.novita.ai/openai", Proof: "allowlist", Seed: []string{"apodex/apodex-1.1-mini"}, NeedsConfirmation: true, Priority: 100},
}

func IsProvider(id string) bool {
	for _, definition := range definitions {
		if definition.ID == id {
			return true
		}
	}
	return false
}

// Definitions returns independent copies of the supported vendor definitions.
func Definitions() []Definition {
	out := make([]Definition, len(definitions))
	for i, original := range definitions {
		d := original
		d.Seed = append([]string(nil), d.Seed...)
		d.Limits = make(map[string]int)
		for key, value := range original.Limits {
			d.Limits[key] = value
		}
		d.Kind, d.Transport, d.Protocol = "openai", "http", "openai_compatible"
		if d.ID == "opencode" {
			d.Kind, d.Transport, d.Protocol, d.RequiresCLI = "opencode", "cli", "opencode_session", true
		}
		if d.ID == "cline" {
			d.Kind, d.Transport, d.Protocol, d.RequiresCLI = "cli", "cli", "acp", true
		}
		var metadata struct {
			AuthURL string `json:"authUrl"`
		}
		data, _ := authMetadata.ReadFile(d.ID + "/auth.json")
		_ = json.Unmarshal(data, &metadata)
		d.AuthURL = metadata.AuthURL
		out[i] = d
	}
	return out
}

func numeric(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case json.Number:
		n, err := v.Float64()
		return n, err == nil
	case string:
		if strings.TrimSpace(v) == "" {
			return 0, false
		}
		n, err := strconv.ParseFloat(v, 64)
		return n, err == nil
	default:
		return 0, false
	}
}

// NormalizeModel admits only models that satisfy this vendor's explicit free policy.
func NormalizeModel(d Definition, raw map[string]any) (Model, bool) {
	id, _ := raw["id"].(string)
	if id == "" {
		id, _ = raw["name"].(string)
	}
	if id == "" || raw["isFree"] == false || raw["disabled"] == true || raw["status"] == "deprecated" {
		return Model{}, false
	}
	if !supportsTextChat(raw) {
		return Model{}, false
	}
	free := false
	switch d.Proof {
	case "catalog-zero":
		price, ok := raw["pricing"].(map[string]any)
		a, b := "prompt", "completion"
		if !ok {
			price, _ = raw["cost"].(map[string]any)
			a, b = "input", "output"
		}
		x, okX := numeric(price[a])
		y, okY := numeric(price[b])
		free = okX && okY && x == 0 && y == 0
	case "suffix-free":
		free = strings.HasSuffix(id, ":free") || id == "openrouter/free"
	case "free-prototype", "cli-free-catalog":
		free = true
	default:
		for _, seed := range d.Seed {
			if seed == id {
				free = true
				break
			}
		}
	}
	if !free {
		return Model{}, false
	}
	name, _ := raw["name"].(string)
	if name == "" {
		name = id
	}
	m := Model{ID: d.ID + "/" + id, Provider: d.ID, UpstreamID: id, Name: name, Free: true, Ready: true, DiscoveredAt: time.Now().UTC()}
	if n, ok := numeric(raw["context_length"]); ok {
		m.Context = int(n)
	}
	if limit, ok := raw["limit"].(map[string]any); ok {
		if n, ok := numeric(limit["context"]); ok {
			m.Context = int(n)
		}
	}
	if d.ID == "opencode" {
		m.Reasoning.Parameter = "variant"
		if variants, ok := raw["variants"].(map[string]any); ok {
			for value := range variants {
				m.Reasoning.Efforts = append(m.Reasoning.Efforts, value)
			}
		}
		sort.Strings(m.Reasoning.Efforts)
	} else {
		parameters, _ := raw["supported_parameters"].([]any)
		for _, parameter := range parameters {
			if parameter == "reasoning" {
				m.Reasoning.Parameter = "reasoning"
				break
			}
			if parameter == "reasoning_effort" {
				m.Reasoning.Parameter = "reasoning_effort"
			}
		}
		if metadata, ok := raw["opencode"].(map[string]any); ok && m.Reasoning.Parameter != "" {
			options, _ := metadata["reasoning_options"].([]any)
			seen := map[string]bool{}
			for _, option := range options {
				if o, ok := option.(map[string]any); ok && o["type"] == "effort" {
					values, _ := o["values"].([]any)
					for _, value := range values {
						if s, ok := value.(string); ok && !seen[s] {
							seen[s] = true
							m.Reasoning.Efforts = append(m.Reasoning.Efforts, s)
						}
					}
				}
			}
		}
	}
	return m, true
}

func supportsTextChat(raw map[string]any) bool {
	architecture, _ := raw["architecture"].(map[string]any)
	modalities, _ := raw["modalities"].(map[string]any)
	for _, value := range []any{architecture["input_modalities"], architecture["output_modalities"], modalities["input"], modalities["output"]} {
		if types, ok := value.([]any); ok {
			text := false
			for _, modality := range types {
				if modality == "text" || modality == "text/plain" {
					text = true
					break
				}
			}
			if !text {
				return false
			}
		}
	}
	legacy, _ := architecture["modality"].(string)
	if legacy == "" {
		return true
	}
	input, output, split := strings.Cut(legacy, "->")
	return legacyTextModality(input) && (!split || legacyTextModality(output))
}

func legacyTextModality(value string) bool {
	for _, modality := range strings.FieldsFunc(value, func(r rune) bool { return r == '+' || r == ',' || r == ' ' }) {
		if modality == "text" || modality == "text/plain" {
			return true
		}
	}
	return false
}
