package router

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wooinwoo/freeway-cli/backend/internal/contracts"
	"github.com/wooinwoo/freeway-cli/backend/internal/providers"
	"github.com/wooinwoo/freeway-cli/backend/internal/quota"
	"github.com/wooinwoo/freeway-cli/backend/internal/storage"
)

func TestOpenCodeAliasesKeepExistingKiloGroups(t *testing.T) {
	for _, tc := range []struct {
		opencode, kilo, group string
	}{
		{"beam-free", "reflection/beam-free", "model/reflection/beam"},
		{"flint-40b-free", "recursive/flint-v1.0:free", "model/recursive/flint-v1.0"},
	} {
		if got := modelIdentity("opencode/"+tc.opencode, tc.opencode); got != tc.group {
			t.Errorf("OpenCode route %q: got %q, want %q", tc.opencode, got, tc.group)
		}
		if got := modelIdentity("kilo/"+tc.kilo, tc.kilo); got != tc.group {
			t.Errorf("Kilo route %q: got %q, want %q", tc.kilo, got, tc.group)
		}
	}
	for _, id := range []string{"other/beam-free", "opencode/beam-preview-free", "opencode/flint-40b-tuned-free"} {
		_, slug, _ := strings.Cut(id, "/")
		if got := modelIdentity(id, slug); got != id {
			t.Errorf("unverified alias %q was merged into %q", id, got)
		}
	}
	if got, want := modelQualityRank("model/reflection/beam"), modelQualityRank("model/reflection/beam-1.0"); got != want || got == len(modelQualityOrder) {
		t.Errorf("verified Beam alias lost comparison rank: got %d, want %d", got, want)
	}
}

type publishedAliases struct{ fakeUpstream }

func (*publishedAliases) Models(_ context.Context, id string) ([]providers.Model, error) {
	slugs := map[string][]string{
		"kilo":     {"reflection/beam-free", "recursive/flint-v1.0:free"},
		"opencode": {"beam-free", "flint-40b-free"},
	}
	var models []providers.Model
	for _, slug := range slugs[id] {
		models = append(models, providers.Model{
			ID: id + "/" + slug, Provider: id, UpstreamID: slug, Name: slug, Free: true, Ready: true,
		})
	}
	return models, nil
}

func (f *publishedAliases) Stream(ctx context.Context, req providers.Request, emit func(string)) (providers.Completion, error) {
	if req.Provider == "kilo" {
		f.called = append(f.called, req.Model)
		return providers.Completion{}, &providers.Error{Code: "upstream_unavailable", Status: 503}
	}
	return f.fakeUpstream.Stream(ctx, req, emit)
}

func TestPublishedAliasesListOnceAndFailOverFromSavedRoute(t *testing.T) {
	vault, err := storage.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	ledger, err := quota.Open(vault, nil)
	if err != nil {
		t.Fatal(err)
	}
	upstream := &publishedAliases{}
	sources := []Source{
		{ID: "kilo", Name: "Kilo", Priority: 10},
		{ID: "opencode", Name: "OpenCode", Priority: 20, ExplicitOnly: true},
	}
	r := New(upstream, ledger, sources, nil, nil).WithSources(sources, true)
	models, err := r.Models(t.Context())
	if err != nil || len(models) != 3 {
		t.Fatalf("provider aliases duplicated entries: %+v, %v", models, err)
	}
	wantRoutes := []string{"kilo/reflection/beam-free", "opencode/beam-free"}
	beam, flint := models[1], models[2]
	if beam.ID != "model/reflection/beam" || beam.QualityRank == 0 || !slices.Equal(beam.RouteIDs, wantRoutes) ||
		flint.ID != "model/recursive/flint-v1.0" || flint.QualityRank != 0 || len(flint.RouteIDs) != 2 {
		t.Fatalf("unexpected alias catalog: %+v", models)
	}
	for _, selection := range []string{beam.ID, "opencode/beam-free"} {
		upstream.called = nil
		result, err := r.Stream(t.Context(), contracts.CompletionRequest{Model: selection}, nil)
		if err != nil || !slices.Equal(upstream.called, wantRoutes) || result.Model != "opencode/beam-free" || result.Provider != "opencode" {
			t.Fatalf("selection %q returned %+v, %v after %v", selection, result, err, upstream.called)
		}
	}
}
