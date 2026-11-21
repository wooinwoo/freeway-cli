package router

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/wooinwoo/freeway-cli/backend/internal/contracts"
	"github.com/wooinwoo/freeway-cli/backend/internal/providers"
	"github.com/wooinwoo/freeway-cli/backend/internal/quota"
	"github.com/wooinwoo/freeway-cli/backend/internal/storage"
)

type fakeUpstream struct {
	called  []string
	partial bool
	first   func(context.Context, providers.Request, func(string)) (providers.Completion, error)
}

func (f *fakeUpstream) Quota(context.Context, string) (providers.Quota, error) {
	return providers.Quota{}, nil
}

func (f *fakeUpstream) Models(_ context.Context, id string) ([]providers.Model, error) {
	models := []providers.Model{
		{ID: id + "/paid", Provider: id, UpstreamID: "paid", Free: false, Ready: true},
		{ID: id + "/free", Provider: id, UpstreamID: "free", Free: true, Ready: true},
	}
	if id == "first" {
		for _, name := range []string{"free-alt1", "free-alt2", "free-alt3"} {
			models = append(models, providers.Model{ID: id + "/" + name, Provider: id, UpstreamID: name, Free: true, Ready: true})
		}
	}
	return models, nil
}

func (f *fakeUpstream) Stream(ctx context.Context, req providers.Request, emit func(string)) (providers.Completion, error) {
	f.called = append(f.called, req.Model)
	if req.Provider == "first" {
		if f.first != nil {
			return f.first(ctx, req, emit)
		}
		if f.partial {
			emit("partial")
		}
		return providers.Completion{}, &providers.Error{Code: "upstream_unavailable", Status: 502}
	}
	emit("complete")
	return providers.Completion{Provider: req.Provider, Model: req.Model, Usage: json.RawMessage(`{"total_tokens":42,"ignored":"private upstream field"}`)}, nil
}

func TestAutoOnlyRetriesAStalledProviderBeforeText(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sendText bool
		want     string
	}{
		{name: "silent provider", want: "second/free"},
		{name: "long reply", sendText: true, want: "first/free"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vault, err := storage.Open(filepath.Join(t.TempDir(), "state"))
			if err != nil {
				t.Fatal(err)
			}
			defer vault.Close()
			ledger, err := quota.Open(vault, nil)
			if err != nil {
				t.Fatal(err)
			}
			upstream := &fakeUpstream{first: func(ctx context.Context, req providers.Request, emit func(string)) (providers.Completion, error) {
				if !tc.sendText {
					<-ctx.Done()
					return providers.Completion{}, ctx.Err()
				}
				emit("first text")
				select {
				case <-time.After(150 * time.Millisecond):
					return providers.Completion{Model: req.Model, Provider: req.Provider}, nil
				case <-ctx.Done():
					return providers.Completion{}, ctx.Err()
				}
			}}
			r := New(upstream, ledger, []Source{{ID: "first", Priority: 1}, {ID: "second", Priority: 2}}, nil, nil)
			r.firstTextWait = 50 * time.Millisecond
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			var deltas []string
			result, err := r.Stream(ctx, contracts.CompletionRequest{Model: Auto}, func(delta contracts.TextDelta) error {
				deltas = append(deltas, delta.Text)
				return nil
			})
			if err != nil || result.Model != tc.want || len(deltas) != 1 || ledger.Today("first").Pending != 0 {
				t.Fatalf("result=%+v deltas=%v attempts=%v err=%v", result, deltas, upstream.called, err)
			}
		})
	}
}

func TestLocalTokenBudgetReportsHowMuchOfTheReplyFits(t *testing.T) {
	vault, err := storage.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	ledger, err := quota.Open(vault, nil)
	if err != nil {
		t.Fatal(err)
	}
	upstream := &fakeUpstream{}
	r := New(upstream, ledger, []Source{{ID: "fixture", Limits: quota.Limits{DayTokens: 4000}}}, nil, nil)
	models, err := r.Models(t.Context())
	if err != nil || len(models) != 2 || models[0].LocalTokensRemaining == nil || *models[0].LocalTokensRemaining != 4000 {
		t.Fatalf("model budget missing: %+v %v", models, err)
	}
	statuses, err := r.ProviderStatuses(t.Context())
	if err != nil || statuses[0].State != "short_replies" {
		t.Fatalf("provider readiness did not disclose the limit: %+v %v", statuses, err)
	}
	input := contracts.CompletionRequest{Model: Auto, Messages: []contracts.InputMessage{{Role: "user", Content: "안녕"}}}
	_, err = r.Stream(t.Context(), input, nil)
	var problem *contracts.Problem
	const fits = 4000 - len("안녕") - 16
	if !errors.As(err, &problem) || problem.Code != "local_token_budget" ||
		problem.MaxOutputTokens == nil || *problem.MaxOutputTokens != fits || len(upstream.called) != 0 {
		t.Fatalf("incorrect local budget or an upstream call was already made: %+v %v", problem, upstream.called)
	}
	maxOutput := fits
	input.MaxTokens = &maxOutput
	if _, err := r.Stream(t.Context(), input, nil); err != nil || len(upstream.called) != 1 {
		t.Fatalf("adjusted request did not fit: %v %v", err, upstream.called)
	}
}

func TestRouteSelectionAndAccounting(t *testing.T) {
	for _, tc := range []struct {
		name    string
		model   string
		partial bool
		want    []string
	}{
		{name: "auto before output", model: Auto, want: []string{"first/free", "second/free"}},
		{name: "auto after output", model: Auto, partial: true, want: []string{"first/free"}},
		{name: "exact", model: "first/free", want: []string{"first/free"}},
		{name: "paid", model: "first/paid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vault, err := storage.Open(filepath.Join(t.TempDir(), "state"))
			if err != nil {
				t.Fatal(err)
			}
			defer vault.Close()
			ledger, err := quota.Open(vault, nil)
			if err != nil {
				t.Fatal(err)
			}
			upstream := &fakeUpstream{partial: tc.partial}
			r := New(upstream, ledger, []Source{{ID: "second", Priority: 2}, {ID: "first", Priority: 1}}, nil, nil)
			models, err := r.Models(t.Context())
			if err != nil || len(models) != 6 || models[0].ID != Auto || models[1].ID != "first/free" {
				t.Fatalf("catalog: %+v, %v", models, err)
			}
			var deltas []contracts.TextDelta
			result, err := r.Stream(t.Context(), contracts.CompletionRequest{Model: tc.model, Stream: true,
				Messages: []contracts.InputMessage{{Role: "user", Content: "hello"}},
			}, func(delta contracts.TextDelta) error { deltas = append(deltas, delta); return nil })
			if !slices.Equal(upstream.called, tc.want) {
				t.Fatalf("routes: got %v want %v", upstream.called, tc.want)
			}
			if len(tc.want) == 2 {
				if err != nil || result.Model != "second/free" || string(result.Usage) != `{"total_tokens":42}` ||
					len(deltas) != 1 || deltas[0].Provider != "second" || ledger.Today("second").Tokens != 42 {
					t.Fatalf("fallback result: %+v, %v; deltas=%+v; usage=%+v", result, err, deltas, ledger.Today("second"))
				}
			} else {
				var p *contracts.Problem
				if !errors.As(err, &p) || (tc.partial && (len(deltas) != 1 || deltas[0].Text != "partial")) {
					t.Fatalf("failed request: %v, deltas=%+v", err, deltas)
				}
			}
			if got := ledger.Today("first"); got.Pending != 0 || got.Requests != min(1, len(tc.want)) {
				t.Fatalf("first route usage: %+v", got)
			}
		})
	}
}
