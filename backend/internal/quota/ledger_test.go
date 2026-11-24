package quota

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wooinwoo/freeway-cli/backend/internal/storage"
)

func TestReservationRaceAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	vault, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	now := time.Date(2026, 1, 1, 12, 12, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	l, err := Open(vault, clock)
	if err != nil {
		t.Fatal(err)
	}
	limits := Limits{Minute: 1}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, err := l.Reserve("fixture", limits, 50)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	var allowed, blocked int
	for err := range results {
		var limit *Blocked
		switch {
		case err == nil:
			allowed++
		case errors.As(err, &limit) && limit.RetryAfter == time.Minute:
			blocked++
		default:
			t.Fatal(err)
		}
	}
	if allowed != 1 || blocked != 1 {
		t.Fatalf("allowed=%d blocked=%d", allowed, blocked)
	}
	if err := vault.Close(); err != nil {
		t.Fatal(err)
	}
	vault, err = storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l, err = Open(vault, clock)
	if err != nil {
		t.Fatal(err)
	}
	if got := l.Today("fixture"); got.Requests != 1 || got.EstimatedTokens != 50 || got.Pending != 0 {
		t.Fatalf("recovered usage: %+v", got)
	}
	now = now.Add(-time.Minute)
	if err := l.Available("fixture", limits, 0); err == nil {
		t.Fatal("clock rollback reopened exhausted window")
	}
	now = now.Add(2 * time.Minute)
	id, err := l.Reserve("fixture", limits, 50)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Finish(id, Outcome{Unsent: true}); err != nil {
		t.Fatal(err)
	}
	id, err = l.Reserve("fixture", limits, 50)
	if err != nil {
		t.Fatal(err)
	}
	tokens := 26
	if err := l.Finish(id, Outcome{Tokens: &tokens, Cooldown: 2 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	tokens = 100
	if err := l.Finish(id, Outcome{Tokens: &tokens}); err != nil {
		t.Fatal(err)
	}
	if got := l.Today("fixture"); got.Requests != 2 || got.Tokens != 26 || got.EstimatedTokens != 50 || got.Pending != 0 {
		t.Fatalf("settled usage: %+v", got)
	}
	var cooldown *Blocked
	if err := l.Available("fixture", limits, 0); !errors.As(err, &cooldown) || !cooldown.Cooldown {
		t.Fatalf("cooldown not enforced: %v", err)
	}
}

func TestClockThatRanAheadDoesNotBlockARouteForYears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	vault, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	real := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	now := real.AddDate(3, 0, 0) // A misconfigured clock briefly runs three years ahead.
	l, err := Open(vault, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	limits := Limits{Day: 1}
	id, err := l.Reserve("fixture", limits, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Finish(id, Outcome{Cooldown: time.Hour}); err != nil {
		t.Fatal(err)
	}
	now = real
	if err := l.Available("fixture", limits, 1); err != nil {
		t.Fatalf("the corrected clock still sees the future window: %v", err)
	}
	if _, err := l.Reserve("fixture", limits, 10); err != nil {
		t.Fatalf("reservation after the clock correction: %v", err)
	}
	if err := vault.Close(); err != nil {
		t.Fatal(err)
	}
	if vault, err = storage.Open(path); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(vault, func() time.Time { return now }); err != nil {
		t.Fatalf("the rebased ledger did not reopen: %v", err)
	}
}
