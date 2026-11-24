// SPDX-License-Identifier: MIT

// Package router selects eligible free routes and owns attempt execution.
package router

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wooinwoo/freeway-cli/backend/internal/contracts"
	"github.com/wooinwoo/freeway-cli/backend/internal/providers"
	"github.com/wooinwoo/freeway-cli/backend/internal/quota"
)

const Auto = "free/auto"

type Upstream interface {
	Models(context.Context, string) ([]providers.Model, error)
	Quota(context.Context, string) (providers.Quota, error)
	Stream(context.Context, providers.Request, func(string)) (providers.Completion, error)
}

type Source struct {
	ID            string
	Name          string
	Priority      int
	Limits        quota.Limits
	ExplicitOnly  bool
	RemoteCatalog bool
}

type Router struct {
	upstream      Upstream
	ledger        *quota.Ledger
	sources       []Source
	prefer        map[string]int
	allowed       map[string]bool
	active        chan struct{}
	firstTextWait time.Duration
	idleWait      time.Duration
	resolveLegacy bool
	mu            sync.Mutex
	reload        chan struct{}
	fresh         time.Time
	catalog       catalogSnapshot
}

type catalogSnapshot struct {
	routes []route
	errors map[string]error
}

type sourceDiscovery struct {
	id     string
	routes []route
	err    error
}

type route struct {
	source  Source
	model   providers.Model
	groupID string
}

func New(upstream Upstream, ledger *quota.Ledger, sources []Source, preferred, allowed []string) *Router {
	r := &Router{
		upstream: upstream, ledger: ledger, sources: slices.Clone(sources),
		prefer: map[string]int{}, allowed: map[string]bool{},
		active: make(chan struct{}, 4), firstTextWait: 35 * time.Second, idleWait: idleWait,
	}
	for i, id := range preferred {
		if _, exists := r.prefer[id]; !exists {
			r.prefer[id] = i
		}
	}
	for _, id := range allowed {
		r.allowed[id] = true
	}
	return r
}

// WithSources shares execution capacity and quota policy across gateway views.
func (r *Router) WithSources(sources []Source, resolveLegacy bool) *Router {
	return &Router{
		upstream: r.upstream, ledger: r.ledger, sources: slices.Clone(sources),
		prefer: r.prefer, allowed: r.allowed, active: r.active, firstTextWait: r.firstTextWait, idleWait: r.idleWait,
		resolveLegacy: resolveLegacy,
	}
}

func (r *Router) Models(ctx context.Context) ([]contracts.Model, error) {
	return r.models(ctx, false)
}

func (r *Router) RefreshModels(ctx context.Context) ([]contracts.Model, error) {
	return r.models(ctx, true)
}

func (r *Router) models(ctx context.Context, refresh bool) ([]contracts.Model, error) {
	catalog, err := r.discoverCatalog(ctx, refresh, refresh)
	if err != nil {
		return nil, err
	}
	models := make([]contracts.Model, 0, 1+len(catalog.routes))
	groups := make(map[string]int, len(catalog.routes))
	groupProviders := make(map[string]map[string]bool, len(catalog.routes))
	type capacity struct {
		ready  bool
		tokens *int
	}
	readiness := make(map[string]capacity, len(r.sources))
	policies := r.connectionPolicies(ctx, catalog.routes)
	// A nil token estimate means at least one ready route has no local token cap.
	unbounded := make(map[string]bool, len(catalog.routes))
	autoReady, autoUnbounded, autoTokens := false, false, 0
	for _, candidate := range catalog.routes {
		policy := policies[candidate.source.ID]
		if !policy.enabled {
			continue
		}
		route, checked := readiness[candidate.source.ID]
		if !checked {
			route.ready = r.ledger.Available(candidate.source.ID, candidate.source.Limits, minInputTokens+1) == nil
			route.tokens = r.ledger.TokensRemaining(candidate.source.ID, candidate.source.Limits)
			readiness[candidate.source.ID] = route
		}
		if route.ready && policy.auto {
			autoReady = true
			if route.tokens == nil {
				autoUnbounded = true
			} else {
				autoTokens = max(autoTokens, *route.tokens)
			}
		}
		index, exists := groups[candidate.groupID]
		if !exists {
			index = len(models)
			groups[candidate.groupID] = index
			groupProviders[candidate.groupID] = make(map[string]bool)
			rank := modelQualityRank(candidate.groupID)
			if rank == len(modelQualityOrder) {
				rank = -1
			}
			models = append(models, contracts.Model{
				ID: candidate.groupID, Name: modelName(candidate), Free: true,
				QualityRank:   rank + 1,
				CodingRank:    slices.Index(codingQualityOrder, candidate.groupID) + 1,
				ReasoningRank: slices.Index(reasoningQualityOrder, candidate.groupID) + 1,
			})
		}
		model := &models[index]
		model.Ready = model.Ready || route.ready
		if route.ready && !unbounded[candidate.groupID] {
			if route.tokens == nil {
				unbounded[candidate.groupID] = true
				model.LocalTokensRemaining = nil
			} else if model.LocalTokensRemaining == nil || *route.tokens > *model.LocalTokensRemaining {
				tokens := *route.tokens
				model.LocalTokensRemaining = &tokens
			}
		}
		if candidate.groupID != candidate.model.ID {
			model.RouteIDs = append(model.RouteIDs, candidate.model.ID)
		}
		if seen := groupProviders[candidate.groupID]; !seen[candidate.source.ID] {
			seen[candidate.source.ID] = true
			if model.Detail != "" {
				model.Detail += " · "
			}
			model.Detail += candidate.source.Name
			if route.ready {
				model.ReadyProviders = append(model.ReadyProviders, candidate.source.Name)
			}
		}
	}
	slices.SortStableFunc(models, func(a, b contracts.Model) int {
		x, y := modelQualityRank(a.ID), modelQualityRank(b.ID)
		if x != y {
			return x - y
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	if autoReady {
		auto := contracts.Model{ID: Auto, Name: "Auto", Detail: "Available free routes", Free: true, Ready: true}
		if !autoUnbounded {
			auto.LocalTokensRemaining = &autoTokens
		}
		models = append([]contracts.Model{auto}, models...)
	}
	return models, ctx.Err()
}

type connectionPolicy struct {
	enabled, auto bool
}

// Check policy against current credentials even when the model catalog is cached.
func (r *Router) connectionPolicies(ctx context.Context, routes []route) map[string]connectionPolicy {
	policies := make(map[string]connectionPolicy)
	for _, candidate := range routes {
		source := candidate.source
		if _, checked := policies[source.ID]; checked {
			continue
		}
		current, err := r.upstream.Quota(ctx, source.ID)
		enabled := err == nil && !current.Disconnected && current.Reason != "needs_key" && current.Reason != "confirm_free_account"
		policies[source.ID] = connectionPolicy{
			enabled: enabled,
			auto:    enabled && !source.ExplicitOnly && !current.AutoExcluded,
		}
	}
	return policies
}

func (r *Router) discover(ctx context.Context, refresh bool) (catalogSnapshot, error) {
	return r.discoverCatalog(ctx, refresh, false)
}

func (r *Router) discoverCatalog(ctx context.Context, refresh, afterPending bool) (catalogSnapshot, error) {
	for {
		r.mu.Lock()
		if !refresh && time.Now().Before(r.fresh) {
			catalog := r.catalog
			r.mu.Unlock()
			return catalog, nil
		}
		if pending := r.reload; pending != nil {
			r.mu.Unlock()
			select {
			case <-ctx.Done():
				return catalogSnapshot{}, ctx.Err()
			case <-pending:
				// A catalog that started before account management may omit the changed provider.
				refresh = afterPending
				continue
			}
		}
		r.reload = make(chan struct{})
		r.mu.Unlock()
		catalog := r.refresh(ctx)
		r.mu.Lock()
		if ctx.Err() == nil {
			ttl := 15 * time.Second
			if len(catalog.routes) == 0 {
				ttl = 2 * time.Second
			}
			r.catalog, r.fresh = catalog, time.Now().Add(ttl)
		}
		close(r.reload)
		r.reload = nil
		r.mu.Unlock()
		return catalog, ctx.Err()
	}
}

func (r *Router) refresh(ctx context.Context) catalogSnapshot {
	completed := make(chan sourceDiscovery, len(r.sources))
	for _, source := range r.sources {
		go func() {
			completed <- r.discoverSource(ctx, source)
		}()
	}
	catalog := catalogSnapshot{errors: make(map[string]error, len(r.sources))}
	for range r.sources {
		// Adapters honor cancellation. Waiting for each call also bounds concurrent discovery.
		result := <-completed
		catalog.routes = append(catalog.routes, result.routes...)
		if result.err != nil {
			catalog.errors[result.id] = result.err
		}
	}
	slices.SortFunc(catalog.routes, r.compare)
	return catalog
}

func (r *Router) discoverSource(ctx context.Context, source Source) sourceDiscovery {
	timeout := 7 * time.Second
	if source.ExplicitOnly {
		// A cold CLI catalog can take longer to start than an HTTP request.
		timeout = 20 * time.Second
	}
	lookup, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	models, err := r.upstream.Models(lookup, source.ID)
	result := sourceDiscovery{id: source.ID, err: err}
	if err != nil {
		return result
	}
	seen := make(map[string]bool)
	for _, model := range models {
		if !model.Free || !model.Ready || model.Provider != source.ID || model.UpstreamID == "" || model.ID != source.ID+"/"+model.UpstreamID || seen[model.ID] {
			continue
		}
		seen[model.ID] = true
		group := modelIdentity(model.ID, model.UpstreamID)
		if len(r.allowed) == 0 || r.allowed[model.ID] || r.allowed[group] {
			result.routes = append(result.routes, route{source: source, model: model, groupID: group})
		}
	}
	return result
}

func (r *Router) compare(a, b route) int {
	x, xPreferred := r.preference(a)
	y, yPreferred := r.preference(b)
	switch {
	case xPreferred && !yPreferred:
		return -1
	case yPreferred && !xPreferred:
		return 1
	case x != y:
		return x - y
	}
	if x, y := modelQualityRank(a.groupID), modelQualityRank(b.groupID); x != y {
		return x - y
	}
	if a.source.Priority != b.source.Priority {
		return a.source.Priority - b.source.Priority
	}
	return strings.Compare(a.model.ID, b.model.ID)
}

func (r *Router) preference(candidate route) (int, bool) {
	routeRank, exact := r.prefer[candidate.model.ID]
	groupRank, grouped := r.prefer[candidate.groupID]
	if exact && (!grouped || routeRank < groupRank) {
		return routeRank, true
	}
	return groupRank, grouped
}
