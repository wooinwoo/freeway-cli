package router

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/wooinwoo/freeway-cli/backend/internal/contracts"
	"github.com/wooinwoo/freeway-cli/backend/internal/providers"
	"github.com/wooinwoo/freeway-cli/backend/internal/quota"
	"github.com/wooinwoo/freeway-cli/backend/internal/storage"
)

type fakeUpstream struct {
	called  []string
	partial bool
}

func (f *fakeUpstream) Models(_ context.Context, id string) ([]providers.Model, error) {
	return []providers.Model{
		{ID: id + "/paid", Provider: id, UpstreamID: "paid", Free: false, Ready: true},
		{ID: id + "/free", Provider: id, UpstreamID: "free", Free: true, Ready: true},
	}, nil
}

func (f *fakeUpstream) Stream(_ context.Context, req providers.Request, emit func(string)) (providers.Completion, error) {
	f.called = append(f.called, req.Model)
	if req.Provider == "first" {
		if f.partial {
			emit("partial")
		}
		return providers.Completion{}, &providers.Error{Code: "upstream_unavailable", Status: 502}
	}
	emit("complete")
	return providers.Completion{Provider: req.Provider, Model: req.Model, Usage: json.RawMessage(`{"total_tokens":42,"ignored":"private upstream field"}`)}, nil
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
			if err != nil || len(models) != 3 || models[0].ID != Auto || models[1].ID != "first/free" {
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
