// SPDX-License-Identifier: MIT

package router

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/wooinwoo/freeway-cli/backend/internal/contracts"
	"github.com/wooinwoo/freeway-cli/backend/internal/providers"
	"github.com/wooinwoo/freeway-cli/backend/internal/quota"
)

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
	statuses := make([]contracts.ProviderStatus, 0, len(r.sources))
	for _, source := range r.sources {
		status := contracts.ProviderStatus{ID: source.ID, Name: source.Name, FreeModels: counts[source.ID]}
		connection, err := r.upstream.Quota(ctx, source.ID)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		status.KeyConfigured = connection.KeyConfigured
		switch {
		case err != nil:
			status.State = "credential_store_failed"
		case connection.Reason == "needs_key", connection.Reason == "confirm_free_account":
			status.State = connection.Reason
		case catalog.errors[source.ID] != nil:
			status.State, status.RetryAfter = catalogStatus(catalog.errors[source.ID])
		case status.FreeModels == 0:
			status.State = "no_free_models"
		default:
			status.State = "ready"
			var blocked *quota.Blocked
			if errors.As(r.ledger.Available(source.ID, source.Limits, 1), &blocked) {
				status.State = "limited"
				status.RetryAfter = budgetError(blocked).RetryAfter
			}
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func catalogStatus(err error) (string, int) {
	var upstream *providers.Error
	if errors.As(err, &upstream) {
		switch upstream.Code {
		case "needs_key", "confirm_free_account", "credential_store_failed":
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
