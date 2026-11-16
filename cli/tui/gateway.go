package tui

import (
	"context"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

type gateway interface {
	Models(context.Context) ([]client.Model, error)
	Stream(context.Context, string, []client.Message, func(client.Event)) error
}

type modelsLoaded struct {
	models []client.Model
	err    error
}

type responseEvent struct {
	event      client.Event
	generation uint64
}

// NewConnected uses the temporary POC adapter; it does not implement the planned Go gateway.
func NewConnected(ctx context.Context, backend gateway) Model {
	m := New()
	m.gateway, m.ctx = backend, ctx
	m.loadingModels = true
	m.selected, m.status = "free/auto", "Connecting to POC..."
	m.models.list.SetItems(nil)
	return m
}

func (m Model) loadModels() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 45*time.Second)
		defer cancel()
		models, err := m.gateway.Models(ctx)
		return modelsLoaded{models: models, err: err}
	}
}

func (m Model) receiveModels(msg modelsLoaded) (tea.Model, tea.Cmd) {
	m.loadingModels = false
	var cmd tea.Cmd
	if msg.err != nil {
		m.status, m.notice = "Connection failed · /model to retry", msg.err.Error()
	} else {
		items := make([]list.Item, 0, len(msg.models))
		for _, model := range msg.models {
			items = append(items, modelItem(model))
		}
		cmd = m.models.list.SetItems(items)
		m.ready = len(items) > 0
		m.status, m.notice = "Free models only", ""
	}
	m.renderMessages()
	return m, cmd
}

func (m Model) startResponse() (tea.Model, tea.Cmd) {
	request := append([]client.Message(nil), m.messages...)
	m.messages = append(m.messages, client.Message{Role: "assistant"})
	m.busy, m.status, m.notice = true, "Generating · Esc to stop", ""
	m.generation++
	ctx, cancel := context.WithTimeout(m.ctx, 150*time.Second)
	m.cancel = cancel
	events := make(chan client.Event, 16)
	backend, model := m.gateway, m.selected
	go func() {
		defer close(events)
		emit := func(event client.Event) {
			select {
			case events <- event:
			case <-ctx.Done():
			}
		}
		if err := backend.Stream(ctx, model, request, emit); err != nil {
			// Timeout must still reach the screen after its request context expires.
			select {
			case events <- client.Event{Type: "error", Error: err.Error()}:
			default:
			}
		}
	}()
	m.events = events
	m.renderMessages()
	m.chat.GotoBottom()
	return m, waitResponse(events, m.generation)
}

func waitResponse(events <-chan client.Event, generation uint64) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			event = client.Event{Type: "error", Error: "Response ended before completion."}
		}
		return responseEvent{event: event, generation: generation}
	}
}

func (m Model) receiveResponse(msg responseEvent) (tea.Model, tea.Cmd) {
	if !m.busy || msg.generation != m.generation {
		return m, nil
	}
	follow := m.chat.AtBottom()
	switch msg.event.Type {
	case "text":
		m.messages[len(m.messages)-1].Content += msg.event.Text
	case "done":
		m.messages[len(m.messages)-1].Content = msg.event.Text
		m.stopResponse()
		m.status = "Free · " + msg.event.Provider
	case "error":
		m.stopResponse()
		m.status, m.notice = "Request failed · Try again or change model", msg.event.Error
	}
	m.renderMessages()
	if follow {
		m.chat.GotoBottom()
	}
	if m.busy {
		return m, waitResponse(m.events, m.generation)
	}
	return m, nil
}

func (m *Model) stopResponse() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.busy = false
	if len(m.messages) > 0 {
		last := m.messages[len(m.messages)-1]
		if last.Role == "assistant" && last.Content == "" {
			m.messages = m.messages[:len(m.messages)-1]
		}
	}
}
