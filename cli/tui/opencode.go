package tui

import (
	"context"
	"errors"
	"fmt"
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
	gateway, local := m.gateway.(openCodeGateway)
	start := 0
	if len(m.availableModels) > 0 && m.availableModels[0].ID == "free/auto" {
		items = append(items, m.modelRow(m.availableModels[0]))
		start = 1
	}
	if local && gateway.CanOpenCode() {
		detail := "Browse models from the official CLI"
		if isOpenCodeModel(m.selected) {
			detail = "Current connection · Browse OpenCode models"
		}
		items = append(items, modelItem{ID: openCodePicker, Name: "OpenCode…", Detail: detail})
	}
	for _, model := range m.availableModels[start:] {
		items = append(items, m.modelRow(model))
	}
	return items
}

func (m Model) modelRow(model client.Model) modelItem {
	if model.LocalTokensRemaining != nil {
		model.Detail = strings.TrimPrefix(model.Detail+fmt.Sprintf(" · %d local tokens", *model.LocalTokensRemaining), " · ")
	}
	if model.ID == m.selected {
		model.Detail = strings.TrimSuffix("✓ Current · "+model.Detail, " · ")
	}
	return modelItem(model)
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
	m.openCodeModels = nil
	items := make([]list.Item, 0, len(msg.models))
	for _, model := range msg.models {
		if isOpenCodeModel(model.ID) && model.Free && model.Ready {
			m.openCodeModels = append(m.openCodeModels, model)
			items = append(items, m.modelRow(model))
		}
	}
	if len(items) == 0 {
		m.openCodeError = "No free models found. Check the OpenCode CLI, then press Ctrl+R."
	}
	cmd := m.refreshMenuItems(items)
	return m, cmd
}
