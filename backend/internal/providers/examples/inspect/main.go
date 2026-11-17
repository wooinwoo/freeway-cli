// This example inspects free models and provider quota observations without inference.
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
	available, err := client.Providers(ctx)
	if err != nil {
		return err
	}
	models, err := client.Models(ctx, provider)
	if err != nil {
		return err
	}
	quota, err := client.Quota(ctx, provider)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(struct {
		Provider  string               `json:"provider"`
		Providers []providers.Provider `json:"providers"`
		Models    []providers.Model    `json:"models"`
		Quota     providers.Quota      `json:"quota"`
	}{provider, available, models, quota})
}
