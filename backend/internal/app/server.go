// SPDX-License-Identifier: MIT

// Package app assembles and supervises a local gateway process.
package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/wooinwoo/freeway-cli/backend/internal/api"
	"github.com/wooinwoo/freeway-cli/backend/internal/auth"
	"github.com/wooinwoo/freeway-cli/backend/internal/providers"
	"github.com/wooinwoo/freeway-cli/backend/internal/quota"
	"github.com/wooinwoo/freeway-cli/backend/internal/router"
	"github.com/wooinwoo/freeway-cli/backend/internal/session"
	"github.com/wooinwoo/freeway-cli/backend/internal/storage"
)

const DefaultAddress = "127.0.0.1:4141"

type Config struct {
	Address   string
	DataDir   string
	Providers []string
	Preferred []string
	Allowed   []string
	Managed   bool
	Version   string
}

func DataDir() (string, error) {
	if path := os.Getenv("FREEWAY_GATEWAY_HOME"); path != "" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".freeway", "gateway"), nil
}

func Serve(ctx context.Context, cfg Config) error {
	address, err := loopbackAddress(cfg.Address)
	if err != nil {
		return err
	}
	sources, err := configuredSources(cfg.Providers, cfg.Managed)
	if err != nil {
		return err
	}
	vault, err := storage.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer vault.Close()
	key, err := vault.ClientKey()
	if err != nil {
		return err
	}
	clientKey, err := auth.NewClientKey(key)
	if err != nil {
		return err
	}
	chats, err := session.Open(vault)
	if err != nil {
		return err
	}
	ledger, err := quota.Open(vault, nil)
	if err != nil {
		return err
	}
	credentials, err := providers.NewFileCredentials(filepath.Join(providers.ProviderHome(), "auth"))
	if err != nil {
		return err
	}
	upstream, err := providers.New(providers.Config{Credentials: credentials})
	if err != nil {
		return err
	}
	defer upstream.Close()
	commonSources := slices.DeleteFunc(slices.Clone(sources), func(source router.Source) bool { return source.ExplicitOnly })
	models := router.New(upstream, ledger, commonSources, cfg.Preferred, cfg.Allowed)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	endpoint := "http://" + listener.Addr().String()
	connectionKey := "fw_" + rand.Text()
	managedKey, err := auth.NewClientKey(connectionKey)
	if err != nil {
		return err
	}
	if err := vault.PublishConnection(storage.Connection{URL: endpoint, APIKey: connectionKey}); err != nil {
		return err
	}
	defer vault.ClearConnection()
	local := &api.LocalClient{Key: managedKey}
	if cfg.Managed {
		personal := models.WithSources(sources, true)
		local.Inference, local.ManageProvider = personal, personal.ManageProvider
		local.CLIProfile = providers.CLIProfile()
		for _, source := range sources {
			if source.ID == "opencode" {
				local.OpenCode = models.WithSources([]router.Source{source}, false)
				break
			}
		}
	}
	handler := withVersion(api.New(clientKey, chats, models, local), cfg.Version)
	if cfg.Managed {
		var stop func()
		ctx, handler, stop = autoShutdown(ctx, handler)
		defer stop()
	} else {
		slog.Info("gateway listening", "url", endpoint)
	}
	return serveHTTP(ctx, listener, handler)
}

// withVersion lets a newly updated client recognize a gateway that still runs the previous version.
func withVersion(next http.Handler, version string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Freeway-Version", version)
		next.ServeHTTP(w, r)
	})
}

func serveHTTP(ctx context.Context, listener net.Listener, handler http.Handler) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	server := &http.Server{
		Handler:           handler,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
	stopped := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		err := server.Shutdown(shutdown)
		if err != nil {
			err = errors.Join(err, server.Close())
		}
		stopped <- err
	}()
	err := server.Serve(listener)
	cancel()
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return errors.Join(err, <-stopped)
}

func loopbackAddress(address string) (string, error) {
	if address == "" {
		address = DefaultAddress
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", errors.New("bind a local IP address with a port, such as 127.0.0.1:4141")
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	if !net.ParseIP(host).IsLoopback() {
		return "", errors.New("the gateway listen address must be loopback")
	}
	return net.JoinHostPort(host, port), nil
}

func configuredSources(ids []string, managed bool) ([]router.Source, error) {
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	var sources []router.Source
	for _, item := range providers.Definitions() {
		localOpenCode := managed && item.ID == "opencode"
		if (item.RequiresCLI && !localOpenCode) || item.ID == "nvidia" || (len(ids) > 0 && !wanted[item.ID]) {
			continue
		}
		limits := item.Limits
		sources = append(sources, router.Source{ID: item.ID, Name: item.Name, Priority: item.Priority, ExplicitOnly: localOpenCode, RemoteCatalog: item.Catalog || localOpenCode, KeyCheck: item.KeyCheck != "",
			Limits: quota.Limits{Minute: limits["minute"], Hour: limits["hour"], Day: limits["day"], DayTokens: limits["dayTokens"]}})
		delete(wanted, item.ID)
	}
	for _, id := range ids {
		if wanted[id] {
			return nil, fmt.Errorf("provider %q is unavailable in this gateway", id)
		}
	}
	return sources, nil
}
