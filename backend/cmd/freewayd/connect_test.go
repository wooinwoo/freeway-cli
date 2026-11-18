package main

import (
	"context"
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
