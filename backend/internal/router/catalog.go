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
	ID       string
	Name     string
	Priority int
	Limits   quota.Limits
}

type Router struct {
	upstream      Upstream
	ledger        *quota.Ledger
	sources       []Source
	prefer        map[string]int
	allowed       map[string]bool
	active        chan struct{}
	firstTextWait time.Duration
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
	source Source
	model  providers.Model
}

func New(upstream Upstream, ledger *quota.Ledger, sources []Source, preferred, allowed []string) *Router {
	r := &Router{
		upstream: upstream, ledger: ledger, sources: slices.Clone(sources),
		prefer: map[string]int{}, allowed: map[string]bool{},
		active: make(chan struct{}, 4), firstTextWait: 35 * time.Second,
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

func (r *Router) Models(ctx context.Context) ([]contracts.Model, error) {
	catalog, err := r.discover(ctx, false)
	if err != nil {
		return nil, err
	}
	models := make([]contracts.Model, 0, 1+len(catalog.routes))
	readiness := make(map[string]bool, len(r.sources))
	anyReady := false
	for _, candidate := range catalog.routes {
		ready, checked := readiness[candidate.source.ID]
		if !checked {
			ready = r.ledger.Available(candidate.source.ID, candidate.source.Limits, 1) == nil
			readiness[candidate.source.ID] = ready
		}
		anyReady = anyReady || ready
		models = append(models, contracts.Model{ID: candidate.model.ID, Name: candidate.model.Name, Detail: candidate.source.Name, Free: true, Ready: ready})
	}
	if anyReady {
		models = append([]contracts.Model{{ID: Auto, Name: "Auto", Detail: "Available free routes", Free: true, Ready: true}}, models...)
	}
	return models, nil
}

func (r *Router) discover(ctx context.Context, refresh bool) (catalogSnapshot, error) {
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
				refresh = false
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
	ctx, cancel := context.WithTimeout(ctx, 7*time.Second)
	defer cancel()
	completed := make(chan sourceDiscovery, len(r.sources))
	for _, source := range r.sources {
		go func() {
			models, err := r.upstream.Models(ctx, source.ID)
			var found []route
			if err == nil {
				seen := make(map[string]bool)
				for _, model := range models {
					if !model.Free || !model.Ready || model.Provider != source.ID || model.UpstreamID == "" || model.ID != source.ID+"/"+model.UpstreamID || seen[model.ID] {
						continue
					}
					if len(r.allowed) > 0 && !r.allowed[model.ID] {
						continue
					}
					seen[model.ID] = true
					found = append(found, route{source: source, model: model})
				}
			}
			completed <- sourceDiscovery{id: source.ID, routes: found, err: err}
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

func (r *Router) compare(a, b route) int {
	x, xPreferred := r.prefer[a.model.ID]
	y, yPreferred := r.prefer[b.model.ID]
	switch {
	case xPreferred && !yPreferred:
		return -1
	case yPreferred && !xPreferred:
		return 1
	case x != y:
		return x - y
	case a.source.Priority != b.source.Priority:
		return a.source.Priority - b.source.Priority
	default:
		return strings.Compare(a.model.ID, b.model.ID)
	}
}
