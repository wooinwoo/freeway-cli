package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wooinwoo/freeway-cli/backend/internal/storage"
)

func TestConnectRetriesWhilePreviousGatewayReleasesVault(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	starts := 0
	expected := storage.Connection{URL: "http://127.0.0.1:12345"}
	connection, err := waitForConnection(ctx, func(context.Context) (storage.Connection, bool) {
		return expected, starts == 2
	}, func() (<-chan error, error) {
		starts++
		exit := make(chan error, 1)
		if starts == 1 {
			exit <- storage.ErrLocked
		}
		return exit, nil
	})
	if err != nil || connection != expected || starts != 2 {
		t.Fatalf("connect after shutdown: endpoint=%q, starts=%d, err=%v", connection.URL, starts, err)
	}
}

func TestConnectWaitsForAnOldGatewayToLeaveWithoutProbingIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gateway")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	vault, err := storage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	stale := storage.Connection{URL: "http://127.0.0.1:12345", APIKey: "fw_fixture"}
	if err := vault.PublishConnection(stale); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		vault.ClearConnection()
	}()
	start := time.Now()
	waitForExit(t.Context(), dir, stale, 5*time.Second)
	if waited := time.Since(start); waited < 250*time.Millisecond || waited > 2*time.Second {
		t.Fatalf("the exit of the old gateway was not noticed promptly: %v", waited)
	}
	if err := vault.PublishConnection(stale); err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	waitForExit(t.Context(), dir, stale, 400*time.Millisecond)
	if waited := time.Since(start); waited < 400*time.Millisecond {
		t.Fatalf("stopped waiting while the old gateway was still listed: %v", waited)
	}
}
