package router

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/wooinwoo/freeway-cli/backend/internal/contracts"
	"github.com/wooinwoo/freeway-cli/backend/internal/providers"
	"github.com/wooinwoo/freeway-cli/backend/internal/quota"
	"github.com/wooinwoo/freeway-cli/backend/internal/storage"
)

type visionUpstream struct {
	fakeUpstream
	images [][]string
}

func (*visionUpstream) Models(_ context.Context, id string) ([]providers.Model, error) {
	model := map[string]providers.Model{
		"text":    {UpstreamID: "reflection/beam"},
		"vl":      {UpstreamID: "nvidia/nemotron-3.5-super-vl", Vision: true},
		"glimmer": {UpstreamID: "nvidia/muse-glimmer-30b", Vision: true},
	}[id]
	model.ID, model.Provider, model.Free, model.Ready = id+"/"+model.UpstreamID, id, true, true
	return []providers.Model{model}, nil
}

func (f *visionUpstream) Stream(ctx context.Context, req providers.Request, emit func(string)) (providers.Completion, error) {
	f.images = append(f.images, req.Messages[len(req.Messages)-1].Images)
	return f.fakeUpstream.Stream(ctx, req, emit)
}

func TestImagesGoOnlyToRoutesThatReadThemBestScoreFirst(t *testing.T) {
	vault, err := storage.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	ledger, err := quota.Open(vault, nil)
	if err != nil {
		t.Fatal(err)
	}
	upstream := &visionUpstream{}
	r := New(upstream, ledger, []Source{{ID: "text", Priority: 1}, {ID: "vl", Priority: 2}, {ID: "glimmer", Priority: 3}}, nil, nil)
	image := "data:image/png;base64,iVBORw0KGgo="
	ask := func(model string, images []string) (contracts.Completion, error) {
		return r.Stream(t.Context(), contracts.CompletionRequest{Model: model, Messages: []contracts.InputMessage{{Role: "user", Content: "이 그림 설명해줘", Images: images}}}, nil)
	}
	result, err := ask(Auto, []string{image})
	if err != nil || result.Provider != "glimmer" || len(upstream.images) != 1 || len(upstream.images[0]) != 1 || upstream.images[0][0] != image {
		t.Fatalf("image request went to %q with %v: %v", result.Provider, upstream.images, err)
	}
	if result, err := ask(Auto, nil); err != nil || result.Provider != "text" {
		t.Fatalf("a text question left the general order: %q %v", result.Provider, err)
	}
	var problem *contracts.Problem
	if _, err := ask("model/reflection/beam", []string{image}); !errors.As(err, &problem) || problem.Code != "no_vision_route" {
		t.Fatalf("a text-only model accepted an image: %v", err)
	}
}
