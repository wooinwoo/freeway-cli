package router

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/wooinwoo/freeway-cli/backend/internal/contracts"
	"github.com/wooinwoo/freeway-cli/backend/internal/providers"
	"github.com/wooinwoo/freeway-cli/backend/internal/quota"
	"github.com/wooinwoo/freeway-cli/backend/internal/storage"
)

type providerStatusFixture struct {
	fakeUpstream
	reject bool
}

type providerManagementFixture struct {
	fakeUpstream
	quota map[string]providers.Quota
}

type scopedStatusFixture struct {
	fakeUpstream
	checked []string
}

func (f *scopedStatusFixture) Models(ctx context.Context, id string) ([]providers.Model, error) {
	f.checked = append(f.checked, id)
	return f.fakeUpstream.Models(ctx, id)
}

func TestProviderStatusOnlyChecksSelectedSourceAndKeepsAllowlist(t *testing.T) {
	vault, err := storage.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	ledger, err := quota.Open(vault, nil)
	if err != nil {
		t.Fatal(err)
	}
	upstream := &scopedStatusFixture{}
	r := New(upstream, ledger, []Source{{ID: "first"}, {ID: "unrelated", ExplicitOnly: true}}, nil, []string{"first/free"})
	status, err := r.ProviderStatus(t.Context(), "first")
	if err != nil || status.ID != "first" || status.State != "ready" || status.FreeModels != 1 || !slices.Equal(upstream.checked, []string{"first"}) {
		t.Fatalf("scoped status %+v, error %v, catalogs checked %v", status, err, upstream.checked)
	}
	if _, err := r.ProviderStatus(t.Context(), "missing"); err == nil || len(upstream.checked) != 1 {
		t.Fatalf("unknown source unexpectedly touched a catalog: %v, %v", err, upstream.checked)
	}
}

func (f *providerManagementFixture) Quota(_ context.Context, id string) (providers.Quota, error) {
	return f.quota[id], nil
}

func TestProviderPolicyTakesEffectWithCachedModels(t *testing.T) {
	vault, err := storage.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	ledger, err := quota.Open(vault, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &providerManagementFixture{quota: make(map[string]providers.Quota)}
	f.first = func(_ context.Context, req providers.Request, emit func(string)) (providers.Completion, error) {
		emit("fixture reply")
		return providers.Completion{Provider: req.Provider, Model: req.Model}, nil
	}
	r := New(f, ledger, []Source{{ID: "first", Priority: 1}, {ID: "second", Priority: 2}}, nil, nil)
	ctx := t.Context()
	if models, err := r.Models(ctx); err != nil || len(models) == 0 || models[0].ID != Auto {
		t.Fatalf("initial Auto model missing: %+v %v", models, err)
	}
	f.quota["first"] = providers.Quota{AutoExcluded: true}
	if result, err := r.Stream(ctx, contracts.CompletionRequest{Model: Auto}, nil); err != nil || result.Provider != "second" {
		t.Fatalf("Auto did not skip excluded provider: %+v %v", result, err)
	}
	if _, err := r.Stream(ctx, contracts.CompletionRequest{Model: "first/free"}, nil); err != nil {
		t.Fatalf("manual request incorrectly blocked by Auto exclusion: %v", err)
	}
	f.quota["first"] = providers.Quota{AutoExcluded: true, Disconnected: true}
	f.quota["second"] = providers.Quota{AutoExcluded: true}
	if models, err := r.Models(ctx); err != nil || len(models) != 1 || models[0].ID != "second/free" {
		t.Fatalf("cached models did not reflect both settings: %+v %v", models, err)
	}
	for _, model := range []string{Auto, "first/free"} {
		if _, err := r.Stream(ctx, contracts.CompletionRequest{Model: model}, nil); err == nil {
			t.Fatalf("unavailable %s still routed", model)
		}
	}
	if !slices.Equal(f.called, []string{"second/free", "first/free"}) {
		t.Fatalf("blocked routes were called: %v", f.called)
	}
	statuses, err := r.ProviderStatuses(ctx)
	if err != nil || statuses[0].State != "disconnected" || statuses[0].FreeModels != 0 ||
		!statuses[1].AutoExcluded || !statuses[1].Management || statuses[1].LocalRequests != 1 {
		t.Fatalf("policy and usage missing from provider status: %+v %v", statuses, err)
	}
}

func (f *providerStatusFixture) Models(ctx context.Context, id string) ([]providers.Model, error) {
	switch {
	case id == "unconfigured":
		return nil, &providers.Error{Code: "needs_key", Status: 409}
	case id == "private" && f.reject:
		return nil, &providers.Error{Code: "unauthorized", Status: 401}
	default:
		return f.fakeUpstream.Models(ctx, id)
	}
}

func (f *providerStatusFixture) Quota(_ context.Context, id string) (providers.Quota, error) {
	switch id {
	case "unconfigured":
		return providers.Quota{Reason: "needs_key"}, nil
	case "private":
		return providers.Quota{KeyConfigured: true, Ready: true}, nil
	default:
		return providers.Quota{Ready: true}, nil
	}
}

func TestProviderStatusesReportCredentialsAndRefreshCatalog(t *testing.T) {
	vault, err := storage.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	ledger, err := quota.Open(vault, nil)
	if err != nil {
		t.Fatal(err)
	}
	upstream := &providerStatusFixture{reject: true}
	r := New(upstream, ledger, []Source{{ID: "guest", RemoteCatalog: true}, {ID: "unconfigured"}, {ID: "private", RemoteCatalog: true}}, nil, nil)
	statuses, err := r.ProviderStatuses(t.Context())
	if err != nil || len(statuses) != 3 {
		t.Fatalf("statuses: %+v, %v", statuses, err)
	}
	if statuses[0].State != "ready" || statuses[0].KeyConfigured || !statuses[0].CatalogChecked || statuses[0].FreeModels != 1 ||
		statuses[1].State != "needs_key" || statuses[1].KeyConfigured ||
		statuses[2].State != "connection_rejected" || !statuses[2].KeyConfigured || statuses[2].CatalogChecked {
		t.Fatalf("statuses do not distinguish a guest, missing key and rejected key: %+v", statuses)
	}
	upstream.reject = false
	statuses, err = r.ProviderStatuses(t.Context())
	if err != nil || statuses[2].State != "ready" || !statuses[2].KeyConfigured || !statuses[2].CatalogChecked || statuses[2].FreeModels != 1 || len(upstream.called) != 0 {
		t.Fatalf("refresh did not discover the repaired connection without inference: %+v, %v", statuses, err)
	}
}
