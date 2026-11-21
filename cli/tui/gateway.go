package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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

type responseTarget struct {
	model    string
	settings client.Settings
	context  client.ContextSelection
}

// NewConnected uses the gateway interface without owning routing or persistent storage.
func NewConnected(ctx context.Context, backend gateway) Model {
	m := New()
	m.gateway, m.ctx = backend, ctx
	m.loadingModels = true
	m.selected, m.status = "free/auto", "Connecting"
	m.models.list.SetItems(nil)
	if _, ok := backend.(conversationGateway); ok {
		m.loadingHistory = true
	}
	return m
}

func (m Model) loadModels() tea.Cmd {
	return m.refreshModels(false)
}

func (m Model) refreshModels(reconnect bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 45*time.Second)
		defer cancel()
		if gateway, ok := m.gateway.(interface{ Reconnect(context.Context) error }); reconnect && ok {
			if err := gateway.Reconnect(ctx); err != nil {
				return modelsLoaded{err: err}
			}
		}
		models, err := m.gateway.Models(ctx)
		return modelsLoaded{models: models, err: err}
	}
}

func (m Model) receiveModels(msg modelsLoaded) (tea.Model, tea.Cmd) {
	m.modelsStarted = true
	m.loadingModels = false
	var cmd tea.Cmd
	if msg.err != nil {
		if isOpenCodeModel(m.selected) || m.busy {
			m.ready = false
			return m, nil
		}
		m.status, m.notice = "Connection needs attention", friendlyError(msg.err)
		m.ready = false
		if m.reconnectAttempts < 2 {
			m.reconnectAttempts++
			m.renderMessages()
			return m, tea.Tick(time.Duration(m.reconnectAttempts*2)*time.Second, func(time.Time) tea.Msg { return reconnectMsg{} })
		}
	} else {
		m.availableModels = append([]client.Model(nil), msg.models...)
		m.ready = len(msg.models) > 0
		if !m.busy {
			m.status, m.notice = "Ready", ""
			m.ensureSelected()
		}
		items := m.modelPickerItems()
		if m.modelsFocused && m.menu == "models" {
			cmd = m.refreshMenuItems(items)
		} else if !m.modelsFocused {
			cmd = m.models.list.SetItems(items)
		}
		m.reconnectAttempts = 0
		m.layout()
	}
	m.renderMessages()
	return m, cmd
}

func (m Model) startResponse() (tea.Model, tea.Cmd) {
	return m.startResponseWithSettings(m.settings)
}

func (m Model) startResponseWithSettings(settings client.Settings) (tea.Model, tea.Cmd) {
	assistant := newMessage("assistant", "")
	assistant.Status = "streaming"
	m.messages = append(m.messages, assistant)
	m.busy, m.status, m.notice = true, "Preparing response", ""
	m.startedAt = time.Now()
	m.responseTarget = responseTarget{model: m.selected, settings: settings, context: m.contextSelection()}
	m.shorterMax = 0
	m.responseElapsed, m.lastResponseTokens = 0, 0
	m.dirty = true
	m.renderMessages()
	m.chat.GotoBottom()
	if cmd := m.persist(); cmd != nil {
		m.afterSave = "generate"
		return m, cmd
	}
	if m.saving {
		m.afterSave = "generate"
		return m, nil
	}
	return m.beginGeneration()
}

func (m Model) beginGeneration() (tea.Model, tea.Cmd) {
	request := m.responseContext().messages()
	m.generation++
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel = cancel
	events := make(chan client.Event, 16)
	backend, target := m.gateway, m.responseTarget
	go func() {
		defer close(events)
		requestCtx, stopRequest := context.WithTimeout(ctx, 150*time.Second)
		defer stopRequest()
		incoming := make(chan client.Event, 16)
		go func() {
			defer close(incoming)
			emit := func(event client.Event) {
				select {
				case incoming <- event:
				case <-ctx.Done():
				}
			}
			callback := func(event client.Event) {
				if event.Type != "error" {
					emit(event)
				}
			}
			var err error
			if configured, ok := backend.(interface {
				StreamWithSettings(context.Context, string, []client.Message, client.Settings, func(client.Event)) error
			}); ok {
				err = configured.StreamWithSettings(requestCtx, target.model, request, target.settings, callback)
			} else {
				err = backend.Stream(requestCtx, target.model, request, callback)
			}
			if err != nil {
				event := client.Event{Type: "error", Error: friendlyError(err)}
				var problem *client.GatewayError
				if errors.As(err, &problem) && problem.Code == "local_token_budget" {
					event.MaxOutputTokens = problem.MaxOutputTokens
				}
				emit(event)
			}
		}()
		coalesceResponse(ctx, incoming, events)
	}()
	m.events = events
	m.renderMessages()
	m.chat.GotoBottom()
	return m, waitResponse(events, m.generation)
}

func includesModelContext(message client.Message) bool {
	return message.Role == "user" || message.Role == "assistant" && (message.Status == "" || message.Status == "done") && message.Content != ""
}

func coalesceResponse(ctx context.Context, incoming <-chan client.Event, events chan<- client.Event) {
	emit := func(event client.Event) {
		select {
		case events <- event:
		case <-ctx.Done():
		}
	}
	var buffer strings.Builder
	var pending client.Event
	var deadline <-chan time.Time
	timer := time.NewTimer(33 * time.Millisecond)
	timer.Stop()
	defer timer.Stop()
	flush := func() {
		timer.Stop()
		deadline = nil
		if buffer.Len() > 0 {
			pending.Text = buffer.String()
			emit(pending)
			buffer.Reset()
		}
	}
	first := true
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			flush()
		case event, ok := <-incoming:
			if !ok {
				flush()
				return
			}
			if event.Type != "text" {
				flush()
				emit(event)
				continue
			}
			if event.Text == "" {
				continue
			}
			if first {
				first = false
				emit(event)
				continue
			}
			pending.Type = "text"
			if event.Model != "" {
				pending.Model, pending.Provider = event.Model, event.Provider
			}
			buffer.WriteString(event.Text)
			if deadline == nil {
				timer.Reset(33 * time.Millisecond)
				deadline = timer.C
			}
		}
	}
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
	follow := m.find == nil && m.chat.AtBottom()
	firstText := false
	switch msg.event.Type {
	case "text":
		if len(m.messages) == 0 || m.messages[len(m.messages)-1].Role != "assistant" {
			m.stopResponse()
			return m, nil
		}
		firstText = m.messages[len(m.messages)-1].Content == ""
		m.messages[len(m.messages)-1].Content += msg.event.Text
		if msg.event.Model != "" {
			m.messages[len(m.messages)-1].Model = msg.event.Model
		}
		m.dirty = true
		m.status = "Writing response"
		m.renderPending = true
	case "done":
		m.responseElapsed = time.Since(m.startedAt)
		if msg.event.Usage != nil {
			m.lastResponseTokens = msg.event.Usage.TotalTokens
		}
		m.messages[len(m.messages)-1].Content = msg.event.Text
		m.messages[len(m.messages)-1].Status = "done"
		if msg.event.Model != "" {
			m.messages[len(m.messages)-1].Model = msg.event.Model
		}
		m.stopResponse()
		m.status = "Response complete"
		m.dirty = true
	case "error":
		m.queuePaused = true
		if len(m.messages) > 0 && m.messages[len(m.messages)-1].Role == "assistant" {
			m.messages[len(m.messages)-1].Status = "error"
			m.messages[len(m.messages)-1].Error = msg.event.Error
		}
		m.stopResponse()
		m.status, m.notice = "Ready to retry", msg.event.Error+"\nAlt+R retry · /auto then Alt+R to use Auto."
		if msg.event.MaxOutputTokens != nil && *msg.event.MaxOutputTokens > 0 && m.selected == m.responseTarget.model {
			m.shorterMax = min(32768, *msg.event.MaxOutputTokens)
			m.notice = fmt.Sprintf("%s\nF5 retry with up to %d output tokens · /settings", msg.event.Error, m.shorterMax)
		}
		m.dirty = true
	}
	if firstText || msg.event.Type != "text" || time.Since(m.lastRender) >= 33*time.Millisecond {
		m.renderMessages()
	}
	if follow {
		m.chat.GotoBottom()
	}
	if m.busy {
		return m, waitResponse(m.events, m.generation)
	}
	return m, m.persist()
}

func (m *Model) stopResponse() {
	if m.afterSave == "generate" {
		m.afterSave = ""
	}
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.busy = false
	if len(m.messages) > 0 {
		last := &m.messages[len(m.messages)-1]
		if last.Role == "assistant" && last.Status == "streaming" {
			last.Status = "stopped"
		}
	}
	if len(m.messages) > 0 {
		last := m.messages[len(m.messages)-1]
		if last.Role == "assistant" && last.Content == "" {
			m.messages = m.messages[:len(m.messages)-1]
		}
	}
}

func friendlyError(err error) string {
	var gateway *client.GatewayError
	if errors.As(err, &gateway) {
		switch {
		case gateway.Code == "chat_conflict":
			return "This conversation changed in another window."
		case gateway.Code == "chat_unreadable":
			return terminalSafe(gateway.Message)
		case gateway.Code == "request_too_large":
			return "This request exceeds the 8 MiB gateway limit. Use F8 to reduce the earlier context."
		case gateway.Code == "conversation_too_large" || gateway.Code == "context_settings_unavailable" || gateway.Code == "context_settings_required":
			return terminalSafe(gateway.Message)
		case gateway.Code == "invalid_conversation" || gateway.Code == "save_uncertain" || gateway.Code == "save_key_reused":
			return terminalSafe(gateway.Message)
		case gateway.Code == "chat_storage_failed" || gateway.Code == "usage_storage_failed":
			return terminalSafe(gateway.Message)
		case gateway.Code == "gateway_busy":
			return "All gateway response slots are in use. Press Alt+R when another response finishes."
		case gateway.Code == "provider_connection_required":
			return "This provider needs a valid free-account connection. Press F4 to check providers, or use /auto."
		case gateway.Code == "model_unavailable":
			return "This model is no longer available. Press F2 to pick another, or use /auto."
		case gateway.Code == "upstream_timeout" || gateway.Code == "upstream_first_text_timeout":
			return "The provider timed out. Press Alt+R to retry, or use /auto."
		case gateway.Code == "response_too_large":
			return "The reply exceeded the gateway's size limit. Ask for a shorter reply or lower the /settings response limit."
		case gateway.Code == "local_token_budget":
			if gateway.MaxOutputTokens != nil && *gateway.MaxOutputTokens > 0 {
				return "The current context fits with a shorter reply under the local token budget."
			}
			return "The current context exceeds the local token budget. Use F8 to reduce it, or shorten the current draft."
		case gateway.Status == 429 && gateway.RetryAfter > 0:
			wait := time.Duration(min(gateway.RetryAfter, 86400)) * time.Second
			return fmt.Sprintf("Free usage is temporarily limited. Try again in %s with Alt+R.", wait)
		case gateway.Status == 429 || strings.Contains(gateway.Code, "limit"):
			return "Free usage is temporarily limited. Try again later with Alt+R."
		case gateway.Code == "provider_not_ready" || gateway.Code == "no_free_models" || gateway.Code == "no_eligible_free_route":
			return "No free models are ready. Press F4 to check providers, or use /reconnect."
		case gateway.Code == "gateway_unavailable" || gateway.Code == "gateway_auth_required" || gateway.Code == "invalid_api_key" || gateway.Code == "local_only" || gateway.Code == "local_client_required":
			return terminalSafe(gateway.Message)
		case gateway.Status == 401 || gateway.Status == 403:
			return "This connection needs a login. Use /auto to try another free connection."
		case gateway.Status >= 500:
			return "The model connection is unstable. Press Alt+R to retry, or choose /auto."
		case gateway.Status == 413:
			return "The conversation and attachments exceed the provider's input limit. Start a new conversation with Alt+N or detach files."
		case gateway.Status == 400:
			return "Check your message length or start a new conversation with Alt+N. Your work is still here."
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "The response timed out. Press Alt+R to retry."
	}
	return terminalSafe(err.Error())
}
