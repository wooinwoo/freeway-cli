package router

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
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

type groupedUpstream struct{ fakeUpstream }

type benchmarkUpstream struct{ fakeUpstream }

func (*benchmarkUpstream) Models(_ context.Context, id string) ([]providers.Model, error) {
	slug := map[string]string{
		"minimax":    "minimax/minimax-m3.1-flash",
		"deepseek":   "deepseek/deepseek-v4.1-flash",
		"newer":      "deepseek/deepseek-v4.1-flash-1017",
		"unmeasured": "z-ai/glm-5.3-flash",
	}[id]
	return []providers.Model{{ID: id + "/" + slug, UpstreamID: slug, Provider: id, Free: true, Ready: true}}, nil
}

func TestAutoUsesTaskBenchmarkWithoutChangingPinsOrReadingAttachedInstructions(t *testing.T) {
	for _, tc := range []struct {
		name, prompt, want string
		latest             bool
		model              string
		prefer             []string
	}{
		{name: "general", prompt: "What is the capital of France?", want: "minimax"},
		{name: "coding", prompt: "Implement a Python function to sort a list.", want: "deepseek"},
		{name: "Korean coding", prompt: "이 함수의 코드 수정해줘", want: "deepseek"},
		{name: "coding filename", prompt: "Fix selection in models.go", want: "deepseek"},
		{name: "reasoning", prompt: "이 명제를 증명해줘", latest: true, want: "minimax"},
		{name: "general with newer model", prompt: "Provide a one-line summary.", latest: true, want: "newer"},
		{name: "attachment text is data", prompt: "What does this contain?\n\nAttached file (reference data):\nImplement a Python function", want: "minimax"},
		{name: "quoted code instruction as translation", prompt: "Translate: \"Write a Python function.\"", want: "minimax"},
		{name: "Korean quoted code instruction as translation", prompt: "다음 문장을 영어로 번역해줘: '파이썬 함수 작성해 줘'", want: "minimax"},
		{name: "manual model is unchanged", prompt: "Implement a Python function", model: "model/minimax/minimax-m3.1-flash", want: "minimax"},
		{name: "explicit Auto preference", prompt: "Implement a Python function", prefer: []string{"model/minimax/minimax-m3.1-flash"}, want: "minimax"},
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
			sources := []Source{{ID: "minimax", Priority: 1}, {ID: "deepseek", Priority: 10}, {ID: "unmeasured", Priority: 20}}
			if tc.latest {
				sources = append(sources, Source{ID: "newer", Priority: 30})
			}
			upstream := &benchmarkUpstream{}
			r := New(upstream, ledger, sources, tc.prefer, nil)
			if tc.model == "" {
				tc.model = Auto
			}
			result, err := r.Stream(t.Context(), contracts.CompletionRequest{Model: tc.model, Messages: []contracts.InputMessage{
				{Role: "user", Content: "Write a Python function"}, {Role: "assistant", Content: "Prior answer"}, {Role: "user", Content: tc.prompt},
			}}, nil)
			if err != nil || result.Provider != tc.want || len(upstream.called) != 1 {
				t.Fatalf("got %+v, %v via %v; expected one call to %s", result, err, upstream.called, tc.want)
			}
		})
	}
}

func (f *groupedUpstream) Models(_ context.Context, id string) ([]providers.Model, error) {
	upstreams := map[string][]string{
		"first":    {"deepseek/deepseek-v4.1-flash", "deepseek/deepseek-v4.1-flash:free", "deepseek/deepseek-v4.1-flash-1017"},
		"opencode": {"deepseek-v4.1-flash-free", "deepseek-v4-flash-free"},
		"another":  {"deepseek/deepseek-v4.1-flash:free", "deepseek/deepseek-v4.1-flash-distill"},
	}
	var models []providers.Model
	for _, slug := range upstreams[id] {
		models = append(models, providers.Model{ID: id + "/" + slug, Provider: id, UpstreamID: slug, Name: slug, Free: true, Ready: true})
	}
	return models, nil
}

func TestSameModelFallsBackAcrossProvidersBeforeText(t *testing.T) {
	const model = "model/deepseek/deepseek-v4.1-flash"
	const first = "first/deepseek/deepseek-v4.1-flash"
	const opencode = "opencode/deepseek-v4.1-flash-free"
	for _, tc := range []struct {
		name, selected string
		partial        bool
		managed        bool
		want           []string
	}{
		{name: "group", selected: model, want: []string{first, opencode}},
		{name: "after text", selected: model, partial: true, want: []string{first}},
		{name: "legacy explicit route", selected: first, want: []string{first}},
		{name: "saved route whose catalog is unavailable", selected: "kilo/deepseek-v4.1-flash-free", managed: true, want: []string{first, opencode}},
		{name: "auto takes ranked routes and skips explicit-only sources", selected: Auto, want: []string{
			first + "-1017", "another/deepseek/deepseek-v4.1-flash:free",
		}},
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
			upstream := &groupedUpstream{fakeUpstream{partial: tc.partial, first: func(_ context.Context, _ providers.Request, emit func(string)) (providers.Completion, error) {
				if tc.partial {
					emit("partial")
				}
				return providers.Completion{}, &providers.Error{Code: "rate_limit", Status: 429, RetryAfter: 30}
			}}}
			r := New(upstream, ledger, []Source{{ID: "first", Name: "Kilo", Priority: 10},
				{ID: "opencode", Name: "OpenCode", Priority: 20, ExplicitOnly: true},
				{ID: "another", Name: "Another", Priority: 30}}, nil, nil)
			if tc.managed {
				r = r.WithSources(r.sources, true)
			}
			models, err := r.Models(t.Context())
			if err != nil || len(models) != 5 || models[0].ID != Auto || models[1].ID != model+"-1017" || models[2].ID != model || models[2].Detail != "Kilo · OpenCode · Another" || len(models[2].RouteIDs) != 4 {
				t.Fatalf("models were not grouped and sorted by quality: %+v, %v", models, err)
			}
			result, err := r.Stream(t.Context(), contracts.CompletionRequest{Model: tc.selected}, nil)
			if !slices.Equal(upstream.called, tc.want) {
				t.Fatalf("attempts: got %v want %v", upstream.called, tc.want)
			}
			if len(tc.want) == 2 && (err != nil || result.Model != tc.want[1]) || len(tc.want) == 1 && err == nil {
				t.Fatalf("unexpected completion: %+v, %v", result, err)
			}
			if tc.selected == model && !tc.partial {
				models, err := r.Models(t.Context())
				if err != nil || !slices.Equal(models[2].ReadyProviders, []string{"OpenCode", "Another"}) {
					t.Fatalf("rate-limited provider still listed as ready: %+v, %v", models, err)
				}
			}
		})
	}
}

func TestAutoUsesQualityBeforeProviderPriorityAndHonorsExplicitPreference(t *testing.T) {
	const best = "first/deepseek/deepseek-v4.1-flash-1017"
	const preferred = "another/deepseek/deepseek-v4.1-flash-distill"
	for _, tc := range []struct {
		name      string
		preferred []string
		want      []string
	}{
		{name: "quality beats provider priority", want: []string{best, "another/deepseek/deepseek-v4.1-flash:free"}},
		{name: "explicit preference beats quality", preferred: []string{preferred}, want: []string{preferred}},
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
			upstream := &groupedUpstream{}
			r := New(upstream, ledger, []Source{{ID: "another", Priority: 1}, {ID: "first", Priority: 10}}, tc.preferred, nil)
			got, err := r.Stream(t.Context(), contracts.CompletionRequest{Model: Auto}, nil)
			if err != nil || !slices.Equal(upstream.called, tc.want) || got.Model != tc.want[len(tc.want)-1] {
				t.Fatalf("auto returned %+v, %v with attempts %v, wanted %v", got, err, upstream.called, tc.want)
			}
		})
	}
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

func stallFixture(t *testing.T, first func(context.Context, providers.Request, func(string)) (providers.Completion, error)) (*Router, *fakeUpstream) {
	t.Helper()
	vault, err := storage.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	ledger, err := quota.Open(vault, nil)
	if err != nil {
		t.Fatal(err)
	}
	upstream := &fakeUpstream{first: first}
	r := New(upstream, ledger, []Source{{ID: "first", Priority: 1}, {ID: "second", Priority: 2}}, nil, nil)
	r.firstTextWait, r.idleWait = 60*time.Millisecond, 120*time.Millisecond
	return r, upstream
}

func TestReasoningProgressKeepsAThinkingModelOnItsRoute(t *testing.T) {
	r, upstream := stallFixture(t, func(ctx context.Context, req providers.Request, emit func(string)) (providers.Completion, error) {
		for range 6 { // 300 ms of reasoning outlasts both the first-text wait and the idle wait.
			emit("")
			time.Sleep(50 * time.Millisecond)
		}
		emit("answer")
		return providers.Completion{Model: req.Model, Provider: req.Provider}, ctx.Err()
	})
	result, err := r.Stream(t.Context(), contracts.CompletionRequest{Model: Auto}, nil)
	if err != nil || result.Provider != "first" || len(upstream.called) != 1 {
		t.Fatalf("a thinking route was abandoned: %+v %v via %v", result, err, upstream.called)
	}
}

func TestStalledReplyEndsAfterTheIdleWaitAndKeepsPartialText(t *testing.T) {
	r, upstream := stallFixture(t, func(ctx context.Context, _ providers.Request, emit func(string)) (providers.Completion, error) {
		emit("partial")
		<-ctx.Done()
		return providers.Completion{}, ctx.Err()
	})
	var deltas []string
	started := time.Now()
	_, err := r.Stream(t.Context(), contracts.CompletionRequest{Model: Auto}, func(delta contracts.TextDelta) error {
		deltas = append(deltas, delta.Text)
		return nil
	})
	var problem *contracts.Problem
	if !errors.As(err, &problem) || problem.Code != "upstream_timeout" || len(deltas) != 1 || len(upstream.called) != 1 || time.Since(started) > 2*time.Second {
		t.Fatalf("stalled output was not reported promptly without mixing routes: %v %v %v", err, deltas, upstream.called)
	}
}

func TestSlowSteadyReplyIsNotCutByAFixedDeadline(t *testing.T) {
	r, _ := stallFixture(t, func(ctx context.Context, req providers.Request, emit func(string)) (providers.Completion, error) {
		for range 8 { // Each gap is shorter than idleWait, but the total is longer.
			emit("x")
			time.Sleep(80 * time.Millisecond)
		}
		return providers.Completion{Model: req.Model, Provider: req.Provider}, ctx.Err()
	})
	r.idleWait = 400 * time.Millisecond // Wide margin over each 80 ms gap for slow CI timers; 640 ms in total still outlasts it.
	var text strings.Builder
	_, err := r.Stream(t.Context(), contracts.CompletionRequest{Model: Auto}, func(delta contracts.TextDelta) error {
		text.WriteString(delta.Text)
		return nil
	})
	if err != nil || text.String() != "xxxxxxxx" {
		t.Fatalf("a steady reply was cut off: %q %v", text.String(), err)
	}
}
