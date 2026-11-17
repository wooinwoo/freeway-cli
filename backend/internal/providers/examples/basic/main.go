// This example calls vendor APIs and official CLIs directly from Go.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/wooinwoo/freeway-cli/backend/internal/providers"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	// The standalone tool uses this optional store. A backend supplies its own CredentialStore.
	credentials, err := providers.NewFileCredentials(filepath.Join(providers.ProviderHome(), "auth"))
	if err != nil {
		return err
	}
	client, err := providers.New(providers.Config{Credentials: credentials})
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	provider := os.Getenv("FREEWAY_PROVIDER")
	if provider == "" {
		provider = "kilo"
	}
	auth, err := client.Authenticate(ctx, provider, providers.AuthOptions{})
	if err != nil {
		return err
	}
	if !auth.Ready {
		return fmt.Errorf("provider not ready: %s", auth.Reason)
	}
	models, err := client.Models(ctx, provider)
	if err != nil {
		return err
	}
	model := os.Getenv("FREEWAY_MODEL")
	if model == "" {
		for _, candidate := range models {
			if candidate.Ready {
				model = candidate.ID
				break
			}
		}
	}
	if model == "" {
		return fmt.Errorf("no ready free models")
	}
	quota, err := client.Quota(ctx, provider)
	if err != nil {
		return err
	}
	// ProviderRemaining is null when the upstream has not reported an exact allowance.
	if err := json.NewEncoder(os.Stderr).Encode(quota); err != nil {
		return err
	}
	prompt := os.Getenv("FREEWAY_PROMPT")
	if prompt == "" {
		prompt = "Reply with exactly FREEWAY_PROVIDER_OK. Do not use tools."
	}
	_, err = client.Stream(ctx, providers.Request{
		Provider: provider, Model: model, ReasoningEffort: os.Getenv("FREEWAY_REASONING_EFFORT"),
		MaxTokens: 2048, Messages: []providers.Message{{Role: "user", Content: prompt}},
	}, func(text string) { fmt.Print(text) })
	if err != nil {
		return err
	}
	fmt.Println()
	return nil
}
