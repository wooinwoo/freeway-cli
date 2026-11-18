package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

const openCodePicker = "opencode"

type openCodeGateway interface {
	CanOpenCode() bool
	OpenCodeModels(context.Context) ([]client.Model, error)
}

type openCodeModelsLoaded struct {
	models  []client.Model
	request uint64
	err     error
}

func isOpenCodeModel(id string) bool { return strings.HasPrefix(id, "opencode/") }

func (m Model) modelPickerItems() []list.Item {
	items := make([]list.Item, 0, len(m.availableModels)+1)
	if gateway, ok := m.gateway.(openCodeGateway); ok && gateway.CanOpenCode() {
		items = append(items, modelItem{ID: openCodePicker, Name: "OpenCode…", Detail: "Browse models from the official CLI"})
	}
	for _, model := range m.availableModels {
		items = append(items, modelItem(model))
	}
	return items
}

func (m Model) showOpenCode() (tea.Model, tea.Cmd) {
	gateway, ok := m.gateway.(openCodeGateway)
	if !ok || !gateway.CanOpenCode() {
		m.notify("OpenCode requires the automatically started Freeway gateway.")
		return m, nil
	}
	if !m.modelsFocused || m.menu != openCodePicker {
		m.menu = openCodePicker
		m.models.list.SetItems(nil)
		next, _ := m.changeFocus(true)
		m = next.(Model)
	}
	m.openCodeRequest++
	request := m.openCodeRequest
	m.openCodeLoading, m.openCodeError = true, ""
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
		defer cancel()
		models, err := gateway.OpenCodeModels(ctx)
		return openCodeModelsLoaded{models: models, request: request, err: err}
	}
}

func (m Model) receiveOpenCodeModels(msg openCodeModelsLoaded) (tea.Model, tea.Cmd) {
	if !m.modelsFocused || m.menu != openCodePicker || msg.request != m.openCodeRequest {
		return m, nil
	}
	m.openCodeLoading = false
	if msg.err != nil {
		m.openCodeError = "Could not load OpenCode models. Ctrl+R to retry."
		var gateway *client.GatewayError
		if errors.As(msg.err, &gateway) && gateway.Status == 404 {
			m.openCodeError = "Restart the Freeway gateway to load OpenCode models."
		}
		return m, nil
	}
	m.menuAll = make([]list.Item, 0, len(msg.models))
	for _, model := range msg.models {
		if isOpenCodeModel(model.ID) && model.Free && model.Ready {
			m.menuAll = append(m.menuAll, modelItem(model))
		}
	}
	if len(m.menuAll) == 0 {
		m.openCodeError = "No free models found. Check the OpenCode CLI, then press Ctrl+R."
	}
	cmd := m.applyMenuFilter()
	for i, item := range m.models.list.Items() {
		if item.(modelItem).ID == m.selected {
			m.models.list.Select(i)
			break
		}
	}
	return m, cmd
}
