// SPDX-License-Identifier: MIT

package router

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/wooinwoo/freeway-cli/backend/internal/contracts"
	"github.com/wooinwoo/freeway-cli/backend/internal/providers"
	"github.com/wooinwoo/freeway-cli/backend/internal/quota"
)

func (r *Router) ManageProvider(ctx context.Context, id string, change contracts.ProviderChange) error {
	index := slices.IndexFunc(r.sources, func(source Source) bool { return source.ID == id })
	if index < 0 {
		return &providers.Error{Code: "unknown_provider", Status: http.StatusNotFound}
	}
	if r.sources[index].ExplicitOnly && (change.Action == "include-auto" || change.Action == "exclude-auto") {
		return &providers.Error{Code: "provider_auto_unavailable", Status: http.StatusConflict}
	}
	manager, ok := r.upstream.(interface {
		Manage(context.Context, string, string) error
		Authenticate(context.Context, string, providers.AuthOptions) (providers.AuthResult, error)
	})
	if !ok {
		return &providers.Error{Code: "provider_management_unavailable", Status: http.StatusServiceUnavailable}
	}
	if change.Action != "authenticate" {
		if change.APIKey != "" || change.ConfirmFree {
			return &providers.Error{Code: "invalid_connection_action", Status: http.StatusBadRequest}
		}
		return manager.Manage(ctx, id, change.Action)
	}
	if change.APIKey == "" && !change.ConfirmFree {
		return &providers.Error{Code: "invalid_key", Status: http.StatusBadRequest}
	}
	status, err := manager.Authenticate(ctx, id, providers.AuthOptions{Key: change.APIKey, ConfirmFree: change.ConfirmFree, RequireReady: true})
	if err != nil {
		return err
	}
	if !status.Ready {
		return &providers.Error{Code: status.Reason, Status: http.StatusConflict}
	}
	return nil
}

// ProviderStatuses refreshes model availability without sending an inference request.
func (r *Router) ProviderStatuses(ctx context.Context) ([]contracts.ProviderStatus, error) {
	catalog, err := r.discover(ctx, true)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(r.sources))
	for _, route := range catalog.routes {
		counts[route.source.ID]++
	}
	// Key checks are network calls, so check every provider at once.
	statuses := make([]contracts.ProviderStatus, len(r.sources))
	failures := make([]error, len(r.sources))
	var wait sync.WaitGroup
	for i, source := range r.sources {
		wait.Go(func() {
			statuses[i], failures[i] = r.providerStatus(ctx, source, counts[source.ID], catalog.errors[source.ID])
		})
	}
	wait.Wait()
	for _, err := range failures {
		if err != nil {
			return nil, err
		}
	}
	return statuses, nil
}

// ProviderStatus checks only the selected provider. It leaves the shared routing catalog alone.
func (r *Router) ProviderStatus(ctx context.Context, id string) (contracts.ProviderStatus, error) {
	index := slices.IndexFunc(r.sources, func(source Source) bool { return source.ID == id })
	if index < 0 {
		return contracts.ProviderStatus{}, &providers.Error{Code: "unknown_provider", Status: http.StatusNotFound}
	}
	source := r.sources[index]
	result := r.discoverSource(ctx, source)
	return r.providerStatus(ctx, source, len(result.routes), result.err)
}

func (r *Router) providerStatus(ctx context.Context, source Source, count int, catalogErr error) (contracts.ProviderStatus, error) {
	status := contracts.ProviderStatus{
		ID: source.ID, Name: source.Name, FreeModels: count, Management: true,
		AutoSupported: !source.ExplicitOnly, CheckedAt: time.Now().UTC().Format(time.RFC3339),
		LocalRequests: r.ledger.Today(source.ID).Requests,
	}
	connection, err := r.upstream.Quota(ctx, source.ID)
	if ctx.Err() != nil {
		return contracts.ProviderStatus{}, ctx.Err()
	}
	status.KeyConfigured = connection.KeyConfigured
	status.AutoExcluded = connection.AutoExcluded
	var keyErr error
	if err == nil && source.KeyCheck && connection.KeyConfigured && !connection.Disconnected && catalogErr == nil {
		if verifier, ok := r.upstream.(interface {
			CheckProviderKey(context.Context, string) error
		}); ok {
			check, cancel := context.WithTimeout(ctx, 3*time.Second)
			keyErr = verifier.CheckProviderKey(check, source.ID)
			cancel()
			status.KeyChecked = keyErr == nil
		}
	}
	if ctx.Err() != nil {
		return contracts.ProviderStatus{}, ctx.Err()
	}
	if connection.Authoritative {
		status.ProviderRemaining = connection.ProviderRemaining
	}
	switch {
	case err != nil:
		status.State = "credential_store_failed"
	case connection.Disconnected:
		status.State, status.FreeModels = "disconnected", 0
	case connection.Reason == "needs_key", connection.Reason == "confirm_free_account":
		status.State = connection.Reason
	case catalogErr != nil:
		status.State, status.RetryAfter = catalogStatus(catalogErr)
	case keyErr != nil:
		status.State, status.RetryAfter = catalogStatus(keyErr)
		if status.State == "unreachable" {
			status.State = "key_check_unavailable"
		}
	case status.FreeModels == 0:
		status.State = "no_free_models"
	default:
		status.State = "ready"
		status.CatalogChecked = source.RemoteCatalog
		status.LocalTokensRemaining = r.ledger.TokensRemaining(source.ID, source.Limits)
		var blocked *quota.Blocked
		if errors.As(r.ledger.Available(source.ID, source.Limits, minInputTokens+1), &blocked) {
			status.State = "limited"
			status.RetryAfter = budgetError(blocked).RetryAfter
		} else if status.LocalTokensRemaining != nil && *status.LocalTokensRemaining < defaultMaxTokens+minInputTokens {
			status.State = "short_replies"
		}
	}
	return status, nil
}

func catalogStatus(err error) (string, int) {
	var upstream *providers.Error
	if errors.As(err, &upstream) {
		switch upstream.Code {
		case "inference_key_required":
			return "connection_rejected", 0
		case "needs_key", "confirm_free_account", "credential_store_failed", "opencode_not_installed":
			return upstream.Code, 0
		}
		switch upstream.Status {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusPaymentRequired:
			return "connection_rejected", 0
		case http.StatusTooManyRequests:
			return "limited", int(retryDelay(err) / time.Second)
		}
	}
	return "unreachable", 0
}
