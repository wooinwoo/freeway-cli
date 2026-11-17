// This example sends conversation history and collects a complete answer.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/wooinwoo/freeway-cli/backend/internal/providers"
)

func main() {
	if err := run(); err != nil {
		var providerError *providers.Error
		if errors.As(err, &providerError) {
			// RetryAfter is seconds. This example never retries or changes routes automatically.
			fmt.Fprintf(os.Stderr, "code=%s status=%d retryAfter=%d\n",
				providerError.Code, providerError.Status, providerError.RetryAfter)
		} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintln(os.Stderr, "request cancelled or timed out")
		} else {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}

func run() error {
	model := os.Getenv("FREEWAY_MODEL")
	if model == "" {
		return fmt.Errorf("set FREEWAY_MODEL to an ID returned by the inspect example")
	}
	bridge := os.Getenv("FREEWAY_PROVIDER_BRIDGE")
	if bridge == "" {
		var err error
		bridge, err = filepath.Abs("internal/providers/bridge.mjs")
		if err != nil {
			return err
		}
	}
	client, err := providers.New(providers.Config{BridgePath: bridge, BaseURL: os.Getenv("FREEWAY_URL")})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	provider := os.Getenv("FREEWAY_PROVIDER")
	if provider == "" {
		provider = "kilo"
	}
	prompt := os.Getenv("FREEWAY_PROMPT")
	if prompt == "" {
		prompt = "What is my name? Reply with just the name."
	}
	result, err := client.Complete(ctx, providers.Request{
		Provider: provider, Model: model,
		ReasoningEffort: os.Getenv("FREEWAY_REASONING_EFFORT"), MaxTokens: 2048,
		Messages: []providers.Message{
			{Role: "user", Content: "My name is Freeway."},
			{Role: "assistant", Content: "Hello, Freeway."},
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return err
	}
	// Only a successful final result is emitted; absent Usage is not a zero-cost report.
	return json.NewEncoder(os.Stdout).Encode(result)
}
