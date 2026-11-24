// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/wooinwoo/freeway-cli/backend/internal/auth"
	"github.com/wooinwoo/freeway-cli/backend/internal/storage"
)

const vaultLocked = 3

// staleWait covers the managed gateway's 8-second idle grace, so a client that restarts right
// after an update does not attach to the previous version's gateway.
const staleWait = 10 * time.Second

func connect(ctx context.Context, dataDir string) (storage.Connection, error) {
	ctx, cancel := context.WithTimeout(ctx, 14*time.Second)
	defer cancel()
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	probe := &http.Client{
		Transport: transport, Timeout: 600 * time.Millisecond,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	if stale, running, ok := activeConnection(ctx, probe, dataDir); ok && running != version {
		// Another client may still use the old gateway. Then it stays, and this client shares it.
		waitForExit(ctx, dataDir, stale, staleWait)
	}
	connection, err := waitForConnection(ctx,
		func(ctx context.Context) (storage.Connection, bool) {
			connection, _, ok := activeConnection(ctx, probe, dataDir)
			return connection, ok
		},
		func() (<-chan error, error) { return startManaged(dataDir) },
	)
	if errors.Is(err, context.DeadlineExceeded) {
		return storage.Connection{}, errors.New("the local gateway did not become ready; run freeway gateway serve to inspect startup")
	}
	return connection, err
}

func waitForConnection(ctx context.Context, probe func(context.Context) (storage.Connection, bool), launch func() (<-chan error, error)) (storage.Connection, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var stopped <-chan error
	var retryAt time.Time
	for {
		if err := ctx.Err(); err != nil {
			return storage.Connection{}, err
		}
		if connection, ok := probe(ctx); ok {
			return connection, nil
		}
		if stopped == nil && ctx.Err() == nil && !time.Now().Before(retryAt) {
			started, err := launch()
			if err != nil {
				return storage.Connection{}, err
			}
			stopped = started
		}
		select {
		case err := <-stopped:
			if err != nil && !errors.Is(err, storage.ErrLocked) {
				return storage.Connection{}, err
			}
			// A stopping gateway can still hold the vault lock. Give it time to release it.
			stopped = nil
			retryAt = time.Now().Add(500 * time.Millisecond)
		case <-ctx.Done():
			return storage.Connection{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

// waitForExit watches the connection file only. A status request would restart the old gateway's idle grace.
func waitForExit(ctx context.Context, dataDir string, stale storage.Connection, limit time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if current, err := storage.ReadConnection(dataDir); err != nil || current != stale {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// activeConnection also returns the running gateway's version. Gateways before 0.6.3 report none.
func activeConnection(ctx context.Context, probe *http.Client, dataDir string) (storage.Connection, string, bool) {
	connection, err := storage.ReadConnection(dataDir)
	if err != nil {
		return storage.Connection{}, "", false
	}
	endpoint, err := url.Parse(connection.URL)
	if err != nil || endpoint.Scheme != "http" || endpoint.User != nil || endpoint.Path != "" ||
		endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Port() == "" || !net.ParseIP(endpoint.Hostname()).IsLoopback() {
		return storage.Connection{}, "", false
	}
	if _, err := auth.NewClientKey(connection.APIKey); err != nil {
		return storage.Connection{}, "", false
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, connection.URL+"/v1/status", nil)
	if err != nil {
		return storage.Connection{}, "", false
	}
	request.Header.Set("Authorization", "Bearer "+connection.APIKey)
	response, err := probe.Do(request)
	if err != nil {
		return storage.Connection{}, "", false
	}
	defer response.Body.Close()
	return connection, response.Header.Get("X-Freeway-Version"), response.StatusCode == http.StatusNoContent && response.Header.Get("X-Freeway-API") == "1"
}

func startManaged(dataDir string) (<-chan error, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer writer.Close()
	cmd := exec.Command(self, "serve", "--managed", "--data-dir", dataDir)
	cmd.Stderr = writer
	detach(cmd)
	if err := cmd.Start(); err != nil {
		reader.Close()
		return nil, fmt.Errorf("start local Freeway gateway: %w", err)
	}
	done := make(chan error, 1)
	go func() {
		defer reader.Close()
		message, _ := io.ReadAll(io.LimitReader(reader, 4096))
		_, _ = io.Copy(io.Discard, reader)
		err := cmd.Wait()
		if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.ExitCode() == vaultLocked {
			done <- storage.ErrLocked
		} else if err != nil && len(message) > 0 {
			// The child already prefixed its message; keep one prefix when relaying it.
			done <- errors.New(strings.TrimPrefix(strings.TrimSpace(string(message)), "Freeway gateway: "))
		} else {
			done <- err
		}
	}()
	return done, nil
}
