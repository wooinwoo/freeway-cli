// SPDX-License-Identifier: MIT

package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/wooinwoo/freeway-cli/backend/internal/contracts"
	"github.com/wooinwoo/freeway-cli/backend/internal/providers"
	"github.com/wooinwoo/freeway-cli/backend/internal/quota"
)

const (
	maxAttempts      = 3
	defaultMaxTokens = 4096
	minInputTokens   = 17
)

var (
	errFirstTextTimeout = errors.New("provider did not start its reply in time")
	errIdleTimeout      = errors.New("provider stopped sending output")
)

// A reply may take as long as it keeps producing output; idleWait ends a stalled route.
const idleWait = 90 * time.Second

func (r *Router) Stream(ctx context.Context, input contracts.CompletionRequest, emit func(contracts.TextDelta) error) (contracts.Completion, error) {
	select {
	case <-ctx.Done():
		return contracts.Completion{}, ctx.Err()
	case r.active <- struct{}{}:
		defer func() { <-r.active }()
	default:
		return contracts.Completion{}, &contracts.Problem{Code: "gateway_busy", Message: "The gateway already has four active requests.", Status: http.StatusTooManyRequests, RetryAfter: 1}
	}
	ctx, cancel := context.WithTimeout(ctx, providers.MaxStreamDuration)
	defer cancel()
	discovered, err := r.discover(ctx, false)
	if err != nil {
		return contracts.Completion{}, executionError(err)
	}
	selected := input.Model
	auto := selected == Auto
	if r.resolveLegacy && !auto {
		provider, upstreamID, _ := strings.Cut(selected, "/")
		if providers.IsProvider(provider) {
			selected = modelIdentity(selected, upstreamID)
		}
	}
	grouped := strings.HasPrefix(selected, "model/")
	catalog := slices.DeleteFunc(slices.Clone(discovered.routes), func(candidate route) bool {
		if auto {
			return candidate.source.ExplicitOnly
		}
		return selected != candidate.groupID && selected != candidate.model.ID
	})
	policies := r.connectionPolicies(ctx, catalog)
	if err := ctx.Err(); err != nil {
		return contracts.Completion{}, executionError(err)
	}
	catalog = slices.DeleteFunc(catalog, func(candidate route) bool {
		policy := policies[candidate.source.ID]
		return !policy.enabled || (auto && !policy.auto)
	})
	if auto {
		if order := taskBenchmark(input.Messages); len(order) != 0 {
			r.prioritizeBenchmark(catalog, order)
		}
		catalog = prioritizeDistinctProviders(catalog)
	}
	attemptLimit := 1
	if auto {
		attemptLimit = maxAttempts
	} else if grouped {
		attemptLimit = len(catalog)
	}
	request := providerRequest(input)
	request.Automatic = auto
	estimate := estimatedTokens(request)
	var attempts int
	var lastAttempt error
	var blockedRoute *contracts.Problem
	tried := make(map[string]bool)
	for i, candidate := range catalog {
		if grouped && tried[candidate.source.ID] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return contracts.Completion{}, executionError(err)
		}
		reservation, err := r.ledger.Reserve(candidate.source.ID, candidate.source.Limits, estimate)
		if err != nil {
			var blocked *quota.Blocked
			if !errors.As(err, &blocked) {
				return contracts.Completion{}, accountingError()
			}
			limit := budgetError(blocked)
			if blocked.TokensRemaining != nil {
				fits := max(0, *blocked.TokensRemaining-(estimate-request.MaxTokens))
				limit.Code = "local_token_budget"
				limit.Message = "The prediction exceeds this route's remaining local token budget."
				limit.MaxOutputTokens = &fits
			}
			if blockedRoute == nil || outputFit(limit) > outputFit(blockedRoute) ||
				outputFit(limit) == outputFit(blockedRoute) && limit.RetryAfter < blockedRoute.RetryAfter {
				blockedRoute = limit
			}
			continue
		}
		attempts++
		if grouped {
			tried[candidate.source.ID] = true
		}
		request.Provider, request.Model = candidate.source.ID, candidate.model.ID
		var firstTextWait time.Duration
		if attempts < attemptLimit && r.hasFallbackProvider(catalog[i+1:], candidate.source.ID, estimate, tried) {
			firstTextWait = r.firstTextWait
		}
		completion, started, err := r.try(ctx, request, reservation, firstTextWait, emit)
		if err == nil || started || ctx.Err() != nil || attempts >= attemptLimit || !retryable(err) {
			return completion, err
		}
		lastAttempt = err
	}
	if lastAttempt != nil {
		return contracts.Completion{}, lastAttempt
	}
	if blockedRoute != nil {
		return contracts.Completion{}, blockedRoute
	}
	return contracts.Completion{}, &contracts.Problem{Code: "no_eligible_free_route", Message: "No eligible free route is ready for this model. Check the provider connection and free catalog.", Status: http.StatusServiceUnavailable}
}

func outputFit(problem *contracts.Problem) int {
	if problem.MaxOutputTokens == nil {
		return 0
	}
	return *problem.MaxOutputTokens
}

func (r *Router) hasFallbackProvider(routes []route, provider string, estimate int, tried map[string]bool) bool {
	checked := map[string]bool{provider: true}
	for _, candidate := range routes {
		source := candidate.source
		if checked[source.ID] || tried[source.ID] {
			continue
		}
		checked[source.ID] = true
		if r.ledger.Available(source.ID, source.Limits, estimate) == nil {
			return true
		}
	}
	return false
}

// A provider outage should consume one attempt before trying the other providers.
func prioritizeDistinctProviders(catalog []route) []route {
	ranked := make([]route, 0, len(catalog))
	var alternates []route
	seen := make(map[string]bool)
	for _, candidate := range catalog {
		if seen[candidate.source.ID] {
			alternates = append(alternates, candidate)
			continue
		}
		ranked = append(ranked, candidate)
		seen[candidate.source.ID] = true
	}
	return append(ranked, alternates...)
}

func (r *Router) try(ctx context.Context, request providers.Request, reservation string, firstTextWait time.Duration, emit func(contracts.TextDelta) error) (contracts.Completion, bool, error) {
	if err := ctx.Err(); err != nil {
		if finishErr := r.ledger.Finish(reservation, quota.Outcome{Unsent: true}); finishErr != nil {
			return contracts.Completion{}, false, accountingError()
		}
		return contracts.Completion{}, false, executionError(err)
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var firstTextTimer *time.Timer
	if firstTextWait > 0 {
		firstTextTimer = time.AfterFunc(firstTextWait, func() { cancel(errFirstTextTimeout) })
	}
	idle := time.AfterFunc(r.idleWait, func() { cancel(errIdleTimeout) })
	defer idle.Stop()
	started := false
	var emitErr error
	response, err := r.upstream.Stream(ctx, request, func(text string) {
		if emitErr != nil || ctx.Err() != nil {
			return
		}
		idle.Reset(r.idleWait)
		if !started && firstTextTimer != nil && !firstTextTimer.Stop() {
			return
		}
		if text == "" {
			// Reasoning progress keeps a thinking model on its route, but only text starts the reply.
			firstTextTimer = nil
			return
		}
		started = true
		if emit != nil {
			emitErr = emit(contracts.TextDelta{Model: request.Model, Provider: request.Provider, Text: text})
			if emitErr != nil {
				cancel(emitErr)
			}
		}
	})
	if !started && firstTextTimer != nil {
		firstTextTimer.Stop()
	}
	usage, tokens := normalizeUsage(response.Usage)
	if finishErr := r.ledger.Finish(reservation, quota.Outcome{Tokens: tokens, Cooldown: retryDelay(err)}); finishErr != nil {
		return contracts.Completion{}, started, accountingError()
	}
	if emitErr != nil {
		return contracts.Completion{}, started, emitErr
	}
	cause := context.Cause(ctx)
	if errors.Is(cause, errFirstTextTimeout) || !started && errors.Is(cause, errIdleTimeout) {
		return contracts.Completion{}, false, &contracts.Problem{Code: "upstream_first_text_timeout", Message: "The provider did not start its reply in time.", Status: http.StatusGatewayTimeout}
	}
	if errors.Is(cause, errIdleTimeout) {
		return contracts.Completion{}, started, &contracts.Problem{Code: "upstream_timeout", Message: "The provider stopped sending output.", Status: http.StatusGatewayTimeout}
	}
	if ctx.Err() != nil {
		return contracts.Completion{}, started, executionError(ctx.Err())
	}
	if err != nil {
		return contracts.Completion{}, started, executionError(err)
	}
	if !started || response.Model != request.Model || response.Provider != request.Provider {
		return contracts.Completion{}, started, &contracts.Problem{Code: "invalid_upstream_response", Message: "The provider returned an unexpected model or an empty reply.", Status: http.StatusBadGateway}
	}
	return contracts.Completion{Model: response.Model, Provider: response.Provider, Usage: usage}, started, nil
}

func providerRequest(input contracts.CompletionRequest) providers.Request {
	request := providers.Request{Temperature: input.Temperature, MaxTokens: defaultMaxTokens, Messages: make([]providers.Message, 0, len(input.Messages))}
	if input.MaxTokens != nil {
		request.MaxTokens = *input.MaxTokens
	}
	for _, message := range input.Messages {
		request.Messages = append(request.Messages, providers.Message{Role: message.Role, Content: message.Content})
	}
	return request
}

func estimatedTokens(request providers.Request) int {
	result := request.MaxTokens
	for _, message := range request.Messages {
		// Bytes plus framing is a conservative local estimate, not provider tokenization.
		result += len(message.Content) + 16
	}
	return result
}

func normalizeUsage(raw json.RawMessage) (json.RawMessage, *int) {
	var usage struct {
		Prompt     *int `json:"prompt_tokens,omitempty"`
		Completion *int `json:"completion_tokens,omitempty"`
		Total      *int `json:"total_tokens,omitempty"`
	}
	if json.Unmarshal(raw, &usage) != nil {
		return nil, nil
	}
	for _, tokens := range []*int{usage.Prompt, usage.Completion, usage.Total} {
		if tokens != nil && (*tokens < 0 || *tokens > 10_000_000) {
			return nil, nil
		}
	}
	if usage.Total == nil && usage.Prompt != nil && usage.Completion != nil {
		total := *usage.Prompt + *usage.Completion
		usage.Total = &total
	}
	if usage.Total == nil {
		return nil, nil
	}
	data, _ := json.Marshal(usage)
	return data, usage.Total
}

func accountingError() *contracts.Problem {
	return &contracts.Problem{Code: "usage_storage_failed", Message: "The gateway could not record local usage safely.", Status: http.StatusInternalServerError}
}

func executionError(err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &contracts.Problem{Code: "upstream_timeout", Message: "The gateway inference deadline expired.", Status: http.StatusGatewayTimeout}
	}
	var remote *providers.Error
	if errors.As(err, &remote) {
		switch remote.Status {
		case http.StatusTooManyRequests:
			return &contracts.Problem{Code: "quota_exhausted", Message: "The provider is rate limited or has exhausted its free allowance.", Status: remote.Status, RetryAfter: int(retryDelay(err) / time.Second)}
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusPaymentRequired, http.StatusConflict:
			return &contracts.Problem{Code: "provider_connection_required", Message: "Review the provider connection and free-account eligibility.", Status: http.StatusServiceUnavailable}
		case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
			return &contracts.Problem{Code: "upstream_request_rejected", Message: "The provider rejected these request settings or the input size.", Status: remote.Status}
		case http.StatusNotFound:
			return &contracts.Problem{Code: "model_unavailable", Message: "The selected model is no longer available on this route.", Status: http.StatusServiceUnavailable}
		}
	}
	return &contracts.Problem{Code: "upstream_unavailable", Message: "The selected provider could not respond.", Status: http.StatusBadGateway}
}

func retryable(err error) bool {
	var p *contracts.Problem
	if !errors.As(err, &p) {
		return false
	}
	return p.Code == "upstream_first_text_timeout" || p.Status == http.StatusServiceUnavailable || p.Status == http.StatusBadGateway || p.Status == http.StatusTooManyRequests
}

func retryDelay(err error) time.Duration {
	var p *providers.Error
	if !errors.As(err, &p) || p.Status != http.StatusTooManyRequests {
		return 0
	}
	if p.RetryAfter < 1 {
		return 30 * time.Second
	}
	return time.Duration(min(p.RetryAfter, 86400)) * time.Second
}

func budgetError(blocked *quota.Blocked) *contracts.Problem {
	code, message := "local_budget_guard", "The gateway's local request or token guard blocked this route."
	if blocked.Cooldown {
		code, message = "provider_cooldown", "The provider asked the gateway to wait before another attempt."
	}
	seconds := int((blocked.RetryAfter + time.Second - 1) / time.Second)
	return &contracts.Problem{Code: code, Message: message, Status: http.StatusTooManyRequests, RetryAfter: max(1, seconds)}
}
