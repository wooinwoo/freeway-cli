package router

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wooinwoo/freeway-cli/backend/internal/providers"
	"github.com/wooinwoo/freeway-cli/backend/internal/quota"
	"github.com/wooinwoo/freeway-cli/backend/internal/storage"
)

type providerStatusFixture struct {
	fakeUpstream
	reject bool
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
	r := New(upstream, ledger, []Source{{ID: "guest"}, {ID: "unconfigured"}, {ID: "private"}}, nil, nil)
	statuses, err := r.ProviderStatuses(t.Context())
	if err != nil || len(statuses) != 3 {
		t.Fatalf("statuses: %+v, %v", statuses, err)
	}
	if statuses[0].State != "ready" || statuses[0].KeyConfigured || statuses[0].FreeModels != 1 ||
		statuses[1].State != "needs_key" || statuses[1].KeyConfigured ||
		statuses[2].State != "connection_rejected" || !statuses[2].KeyConfigured {
		t.Fatalf("statuses do not distinguish a guest, missing key and rejected key: %+v", statuses)
	}
	upstream.reject = false
	statuses, err = r.ProviderStatuses(t.Context())
	if err != nil || statuses[2].State != "ready" || !statuses[2].KeyConfigured || statuses[2].FreeModels != 1 || len(upstream.called) != 0 {
		t.Fatalf("refresh did not discover the repaired connection without inference: %+v, %v", statuses, err)
	}
}
