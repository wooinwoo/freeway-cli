package providers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// TestNativeLive is opt-in because it consumes real vendor free allowances.
// Normal tests use local fixtures and never log in or contact vendor services.
func TestNativeLive(t *testing.T) {
	if os.Getenv("FREEWAY_LIVE") != "1" {
		t.Skip("set FREEWAY_LIVE=1 to exercise installed official CLIs and anonymous Kilo")
	}
	client, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	type record struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		Effort   string `json:"effort,omitempty"`
		Passed   bool   `json:"passed"`
		Code     string `json:"code,omitempty"`
	}
	var records []record
	defer func() {
		if path := os.Getenv("FREEWAY_LIVE_RESULTS"); path != "" {
			data, _ := json.MarshalIndent(struct {
				At      time.Time `json:"at"`
				Results []record  `json:"results"`
			}{time.Now().UTC(), records}, "", "  ")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Error(err)
			}
		}
	}()
	for _, id := range []string{"kilo", "opencode", "cline"} {
		if only := os.Getenv("FREEWAY_LIVE_PROVIDER"); only != "" && only != id {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		auth, err := client.Authenticate(ctx, id, AuthOptions{})
		if err != nil || !auth.Ready {
			cancel()
			t.Fatalf("%s authentication: ready=%v err=%v", id, auth.Ready, err)
		}
		models, err := client.Models(ctx, id)
		if err != nil || len(models) == 0 {
			cancel()
			t.Fatalf("%s models: %d %v", id, len(models), err)
		}
		t.Logf("%s: %d current free models", id, len(models))
		for _, model := range models {
			if wanted := os.Getenv("FREEWAY_LIVE_MODEL"); wanted != "" && model.ID != wanted && model.UpstreamID != wanted {
				continue
			}
			efforts := []string{""}
			if model.UpstreamID == "reflection/beam-free" {
				efforts = []string{"low", "high"}
			}
			if id == "opencode" && model.UpstreamID == "deepseek-v4.1-flash-free" {
				efforts = []string{"low", "max"}
			}
			for _, effort := range efforts {
				var streamed strings.Builder
				result, err := client.Stream(ctx, Request{Provider: id, Model: model.ID, ReasoningEffort: effort, MaxTokens: 2048, Messages: []Message{{Role: "user", Content: "Reply with exactly FREEWAY_PROVIDER_OK. Do not use tools."}}}, func(text string) { streamed.WriteString(text) })
				entry := record{Provider: id, Model: model.ID, Effort: effort, Passed: err == nil && strings.TrimSpace(result.Content) == "FREEWAY_PROVIDER_OK" && result.Content == streamed.String() && result.Model == model.ID && result.Provider == id}
				var typed *Error
				if errors.As(err, &typed) {
					entry.Code = typed.Code
				}
				records = append(records, entry)
				var usage struct {
					Cost *float64 `json:"cost"`
				}
				_ = json.Unmarshal(result.Usage, &usage)
				if usage.Cost != nil && *usage.Cost > 0 {
					cancel()
					t.Fatalf("%s unexpectedly reported positive cost", model.ID)
				}
				if entry.Passed {
					t.Logf("PASS %s effort=%q", model.ID, effort)
				} else {
					t.Errorf("FAIL %s effort=%q err=%v reply=%q", model.ID, effort, err, result.Content)
				}
			}
		}
		if id == "cline" {
			_, err := client.Complete(ctx, Request{Provider: id, Model: models[0].ID, ReasoningEffort: "high", Messages: []Message{{Role: "user", Content: "This request must be rejected before inference."}}})
			var typed *Error
			if !errors.As(err, &typed) || typed.Code != "unsupported_reasoning" {
				t.Errorf("Cline reasoning rejection: %v", err)
			}
		}
		cancel()
	}
	if len(records) == 0 {
		t.Fatal("no live models matched the selected provider/model filters")
	}
}
