// This example inspects models and local quota without issuing inference requests.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
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
