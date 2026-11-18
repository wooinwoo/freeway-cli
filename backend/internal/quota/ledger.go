// SPDX-License-Identifier: MIT

// Package quota persists local usage guards independently of conversations.
package quota

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/wooinwoo/freeway-cli/backend/internal/storage"
)

const (
	ledgerFile      = "quota.enc"
	ledgerSchema    = 1
	ledgerLimit     = 8 * 1024 * 1024
	defaultDayLimit = 1000
)

type Limits struct {
	Minute, Hour, Day, DayTokens int
}

type Blocked struct {
	Until      time.Time
	RetryAfter time.Duration
	Cooldown   bool
}

func (b *Blocked) Error() string { return "route is outside its local allowance" }

type Ledger struct {
	vault *storage.Vault
	clock func() time.Time
	mu    sync.Mutex
	state state
}

type state struct {
	Version  int                  `json:"version"`
	Last     time.Time            `json:"last"`
	Attempts []attempt            `json:"attempts"`
	Cooldown map[string]time.Time `json:"cooldown"`
}

type attempt struct {
	ID        string    `json:"id"`
	Provider  string    `json:"provider"`
	Started   time.Time `json:"started"`
	Tokens    int       `json:"tokens"`
	Estimated bool      `json:"estimated"`
	Pending   bool      `json:"pending"`
}

type Usage struct {
	Requests        int `json:"requests"`
	Tokens          int `json:"reportedTokens"`
	EstimatedTokens int `json:"estimatedTokens"`
	Pending         int `json:"pendingRequests"`
}

type Outcome struct {
	// Unsent releases a reservation only when no upstream call began.
	Unsent bool
	// Nil means the upstream's token consumption is unknown.
	Tokens   *int
	Cooldown time.Duration
}

func Open(vault *storage.Vault, now func() time.Time) (*Ledger, error) {
	if now == nil {
		now = time.Now
	}
	l := &Ledger{vault: vault, clock: now, state: state{Version: ledgerSchema, Cooldown: map[string]time.Time{}}}
	data, err := vault.Read(ledgerFile, ledgerLimit)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &l.state); err != nil || l.state.Version != ledgerSchema || l.state.Cooldown == nil {
		return nil, errors.New("invalid gateway usage ledger")
	}
	seen := make(map[string]bool)
	recovered := false
	for i := range l.state.Attempts {
		item := &l.state.Attempts[i]
		if item.ID == "" || seen[item.ID] || item.Provider == "" || item.Tokens < 0 || item.Started.IsZero() || item.Started.After(l.state.Last) {
			return nil, errors.New("invalid attempt in gateway usage ledger")
		}
		seen[item.ID] = true
		if item.Pending {
			item.Pending = false
			item.Estimated = true
			recovered = true
		}
	}
	if recovered {
		if err := l.commit(l.state); err != nil {
			return nil, err
		}
	}
	return l, nil
}

func (l *Ledger) Available(provider string, limits Limits, tokens int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state.check(l.now(), provider, limits, tokens)
}

func (l *Ledger) Reserve(provider string, limits Limits, tokens int) (string, error) {
	if provider == "" || tokens < 0 {
		return "", errors.New("invalid usage reservation")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if err := l.state.check(now, provider, limits, tokens); err != nil {
		return "", err
	}
	next := l.snapshot(now)
	id := rand.Text()
	next.Attempts = append(next.Attempts, attempt{ID: id, Provider: provider, Started: now, Tokens: tokens, Estimated: true, Pending: true})
	if err := l.commit(next); err != nil {
		return "", err
	}
	return id, nil
}

func (l *Ledger) Finish(id string, outcome Outcome) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	next := l.snapshot(now)
	for i := range next.Attempts {
		item := &next.Attempts[i]
		if item.ID != id || !item.Pending {
			continue
		}
		if outcome.Unsent {
			next.Attempts = slices.Delete(next.Attempts, i, i+1)
		} else {
			item.Pending = false
			if outcome.Tokens != nil && *outcome.Tokens >= 0 {
				item.Tokens, item.Estimated = *outcome.Tokens, false
			}
			if until := now.Add(outcome.Cooldown); until.After(now) && until.After(next.Cooldown[item.Provider]) {
				next.Cooldown[item.Provider] = until
			}
		}
		return l.commit(next)
	}
	return nil
}

// Today reports local UTC-day usage. It makes no claim about the account's total.
func (l *Ledger) Today(provider string) Usage {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state.usage(provider, l.now().Truncate(24*time.Hour))
}

func (l *Ledger) now() time.Time {
	now := l.clock().UTC()
	if now.Before(l.state.Last) {
		return l.state.Last
	}
	return now
}

func (l *Ledger) snapshot(now time.Time) state {
	next := state{Version: ledgerSchema, Last: now, Cooldown: make(map[string]time.Time)}
	start := now.Truncate(24 * time.Hour)
	for _, item := range l.state.Attempts {
		if item.Pending || !item.Started.Before(start) {
			next.Attempts = append(next.Attempts, item)
		}
	}
	for provider, until := range l.state.Cooldown {
		if until.After(now) {
			next.Cooldown[provider] = until
		}
	}
	return next
}

func (l *Ledger) commit(next state) error {
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if len(data) > ledgerLimit {
		return fmt.Errorf("gateway usage ledger exceeds %d bytes", ledgerLimit)
	}
	if err := l.vault.Write(ledgerFile, data); err != nil {
		return err
	}
	l.state = next
	return nil
}

func (s state) check(now time.Time, provider string, limits Limits, tokens int) error {
	if until := s.Cooldown[provider]; until.After(now) {
		return &Blocked{Until: until, RetryAfter: until.Sub(now), Cooldown: true}
	}
	if limits.Day <= 0 {
		limits.Day = defaultDayLimit
	}
	var retryAt time.Time
	for _, window := range []struct {
		duration time.Duration
		requests int
		tokens   int
	}{{time.Minute, limits.Minute, 0}, {time.Hour, limits.Hour, 0}, {24 * time.Hour, limits.Day, limits.DayTokens}} {
		if window.requests <= 0 && window.tokens <= 0 {
			continue
		}
		start := now.Truncate(window.duration)
		used := s.usage(provider, start)
		if (window.requests > 0 && used.Requests >= window.requests) ||
			(window.tokens > 0 && int64(used.Tokens)+int64(used.EstimatedTokens)+int64(tokens) > int64(window.tokens)) {
			retryAt = start.Add(window.duration)
		}
	}
	if !retryAt.IsZero() {
		return &Blocked{Until: retryAt, RetryAfter: retryAt.Sub(now)}
	}
	return nil
}

func (s state) usage(provider string, start time.Time) (total Usage) {
	for _, item := range s.Attempts {
		if item.Provider != provider || (!item.Pending && item.Started.Before(start)) {
			continue
		}
		total.Requests++
		if item.Estimated {
			total.EstimatedTokens += item.Tokens
		} else {
			total.Tokens += item.Tokens
		}
		if item.Pending {
			total.Pending++
		}
	}
	return total
}
