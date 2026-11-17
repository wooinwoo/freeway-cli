// This example configures API credentials or an official CLI login directly from Go.
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
