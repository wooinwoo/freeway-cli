package tui

import (
	"context"
	"fmt"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

type providerStatusesLoaded struct {
	statuses []client.ProviderStatus
	request  uint64
	err      error
}

func (m Model) showProviders() (tea.Model, tea.Cmd) {
	gateway, ok := m.gateway.(interface {
		ProviderStatuses(context.Context) ([]client.ProviderStatus, error)
	})
	if !ok {
		m.notify("Provider status requires a connected gateway.")
		return m, nil
	}
	var focus tea.Cmd
	if !m.modelsFocused || m.menu != "providers" {
		m.menu = "providers"
		m.models.list.SetItems(nil)
		next, cmd := m.changeFocus(true)
		m, focus = next.(Model), cmd
	}
	m.providersRequest++
	request := m.providersRequest
	m.providersLoading, m.providersError = true, ""
	return m, tea.Batch(focus, func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
		defer cancel()
		statuses, err := gateway.ProviderStatuses(ctx)
		return providerStatusesLoaded{statuses: statuses, request: request, err: err}
	})
}

func (m Model) receiveProviderStatuses(msg providerStatusesLoaded) (tea.Model, tea.Cmd) {
	if !m.modelsFocused || m.menu != "providers" || msg.request != m.providersRequest {
		return m, nil
	}
	m.providersLoading = false
	if msg.err != nil {
		m.providersError = "Could not check this gateway. Ctrl+R to retry."
		return m, nil
	}
	m.providers = make(map[string]client.ProviderStatus, len(msg.statuses))
	m.menuAll = make([]list.Item, 0, len(msg.statuses))
	for _, provider := range msg.statuses {
		m.providers[provider.ID] = provider
		m.menuAll = append(m.menuAll, modelItem{ID: provider.ID, Name: provider.Name, Detail: providerStatusText(provider)})
	}
	cmd := m.applyMenuFilter()
	return m, cmd
}

func providerStatusText(provider client.ProviderStatus) string {
	auth := "Guest"
	if provider.KeyConfigured {
		auth = "Key set"
	}
	switch provider.State {
	case "ready":
		if provider.LocalTokensRemaining != nil {
			return fmt.Sprintf("%s · %d free models · %d local tokens", auth, provider.FreeModels, *provider.LocalTokensRemaining)
		}
		return fmt.Sprintf("%s · %d free models", auth, provider.FreeModels)
	case "short_replies":
		if provider.LocalTokensRemaining != nil {
			return fmt.Sprintf("%s · Short replies · %d local tokens", auth, *provider.LocalTokensRemaining)
		}
		return auth + " · Short replies"
	case "needs_key":
		return "Key required"
	case "confirm_free_account":
		return "Key set · Confirm free plan"
	case "connection_rejected":
		return "Connection rejected"
	case "limited":
		if provider.RetryAfter > 0 {
			wait := time.Duration(min(provider.RetryAfter, 86400)) * time.Second
			return fmt.Sprintf("%s · Limited for %s", auth, wait)
		}
		return auth + " · Temporarily limited"
	case "no_free_models":
		return auth + " · No eligible free models"
	case "credential_store_failed":
		return "Cannot read saved credentials"
	default:
		return auth + " · Catalog unavailable"
	}
}

func providerStatusHelp(provider client.ProviderStatus) string {
	switch provider.State {
	case "needs_key", "connection_rejected":
		return "freeway providers auth " + provider.ID
	case "confirm_free_account":
		return "freeway providers auth " + provider.ID + " --confirm-free"
	default:
		return "freeway providers status " + provider.ID
	}
}

func (m Model) replyBudgetHint() string {
	if m.busy || m.commandPending() {
		return ""
	}
	for _, model := range m.availableModels {
		if model.ID != m.selected || model.LocalTokensRemaining == nil {
			continue
		}
		fit := *model.LocalTokensRemaining - m.nextRequestContext().inputEstimate()
		if fit <= 0 {
			return "Local budget estimate · shorten the context or open a new chat"
		}
		if fit < m.settings.MaxTokens {
			return fmt.Sprintf("Local estimate fits up to %d output tokens · /settings %d", fit, fit)
		}
		break
	}
	return ""
}
