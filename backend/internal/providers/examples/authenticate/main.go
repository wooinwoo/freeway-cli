// This example configures authentication through the existing local gateway.
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
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	provider := os.Getenv("FREEWAY_PROVIDER")
	if provider == "" {
		provider = "kilo"
	}
	key := os.Getenv("FREEWAY_API_KEY")
	if os.Getenv("FREEWAY_LOGIN") == "true" {
		if key != "" {
			return fmt.Errorf("choose official CLI login or an API key")
		}
		// Login is interactive; never put it inside a web request handler.
		if err := client.Login(ctx, provider, os.Stdin, os.Stdout, os.Stderr); err != nil {
			return err
		}
	}
	auth, err := client.Authenticate(ctx, provider, providers.AuthOptions{
		Key: key, ConfirmFree: os.Getenv("FREEWAY_CONFIRM_FREE") == "true",
	})
	if err != nil {
		return err
	}
	if err := json.NewEncoder(os.Stdout).Encode(auth); err != nil {
		return err
	}
	if !auth.Ready {
		return fmt.Errorf("provider not ready: %s", auth.Reason)
	}
	return nil
}
