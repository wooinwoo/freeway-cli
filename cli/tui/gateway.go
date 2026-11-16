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
	m.selected, m.status = "free/auto", "Connecting"
	m.models.list.SetItems(nil)
	if _, ok := backend.(conversationGateway); ok {
		m.loadingHistory = true
	}
	if _, ok := backend.(interface {
		PromptHistory(context.Context) ([]string, error)
	}); ok {
		m.promptsLoading = true
	}
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
	m.modelsStarted = true
	m.loadingModels = false
	var cmd tea.Cmd
	if msg.err != nil {
		m.status, m.notice = "Connection needs attention", friendlyError(msg.err)
		m.ready = false
		if m.reconnectAttempts < 2 {
			m.reconnectAttempts++
			m.renderMessages()
			return m, tea.Tick(time.Duration(m.reconnectAttempts*2)*time.Second, func(time.Time) tea.Msg { return reconnectMsg{} })
		}
	} else {
		m.availableModels = append([]client.Model(nil), msg.models...)
		items := make([]list.Item, 0, len(msg.models))
		for _, model := range msg.models {
			items = append(items, modelItem(model))
		}
		if m.modelsFocused && m.menu == "models" {
			m.menuAll = items
			cmd = m.applyMenuFilter()
		} else if !m.modelsFocused {
			cmd = m.models.list.SetItems(items)
		}
		m.ready = len(items) > 0
		m.status, m.notice = "Ready", ""
		m.reconnectAttempts = 0
		m.ensureSelected()
	}
	m.renderMessages()
	return m, cmd
}

func (m Model) startResponse() (tea.Model, tea.Cmd) {
	assistant := newMessage("assistant", "")
	assistant.Status = "streaming"
	m.messages = append(m.messages, assistant)
	m.busy, m.status, m.notice = true, "Preparing response", ""
	m.startedAt = time.Now()
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
	request := []client.Message{}
	if m.session.SystemPrompt != "" {
		request = append(request, client.Message{Role: "system", Content: m.session.SystemPrompt})
	}
	for _, message := range m.messages {
		if message.Role == "user" || message.Role == "assistant" && (message.Status == "" || message.Status == "done") && message.Content != "" {
			request = append(request, client.Message{Role: message.Role, Content: message.Content, Files: append([]client.Attachment(nil), message.Files...)})
		}
	}
	m.generation++
	ctx, cancel := context.WithTimeout(m.ctx, 150*time.Second)
	m.cancel = cancel
	events := make(chan client.Event, 16)
	backend, model := m.gateway, m.selected
	settings := m.settings
	go func() {
		defer close(events)
		emit := func(event client.Event) {
			select {
			case events <- event:
			case <-ctx.Done():
			}
		}
		var buffer strings.Builder
		lastFlush := time.Now()
		hasOutput, complete := false, false
		flush := func() {
			if buffer.Len() > 0 {
				emit(client.Event{Type: "text", Text: buffer.String()})
				buffer.Reset()
				lastFlush = time.Now()
			}
		}
		callback := func(event client.Event) {
			switch event.Type {
			case "text":
				hasOutput = hasOutput || event.Text != ""
				buffer.WriteString(event.Text)
				if time.Since(lastFlush) >= 33*time.Millisecond {
					flush()
				}
			case "error": // The returned error decides whether a safe automatic retry is possible.
			case "done":
				flush()
				complete = true
				emit(event)
			default:
				emit(event)
			}
		}
		stream := func(selected string) error {
			if configured, ok := backend.(interface {
				StreamWithSettings(context.Context, string, []client.Message, client.Settings, func(client.Event)) error
			}); ok {
				return configured.StreamWithSettings(ctx, selected, request, settings, callback)
			}
			return backend.Stream(ctx, selected, request, callback)
		}
		err := stream(model)
		if err != nil && ctx.Err() == nil && !hasOutput && !complete && model != "free/auto" && canRecover(err) {
			emit(client.Event{Type: "fallback"})
			err = stream("free/auto")
		}
		flush()
		if err != nil {
			// Timeout must still reach the screen after its request context expires.
			select {
			case events <- client.Event{Type: "error", Error: friendlyError(err)}:
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
		m.status = "Writing response"
		m.renderPending = true
	case "fallback":
		m.selected = "free/auto"
		m.status = "Finding another free connection"
		m.notify("This model is unavailable. Trying another free connection automatically.")
		m.dirty = true
	case "done":
		m.responseElapsed = time.Since(m.startedAt)
		if msg.event.Usage != nil {
			m.lastResponseTokens = msg.event.Usage.TotalTokens
		}
		m.messages[len(m.messages)-1].Content = msg.event.Text
		m.messages[len(m.messages)-1].Status = "done"
		m.messages[len(m.messages)-1].Model = msg.event.Model
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
		m.status, m.notice = "Ready to retry", msg.event.Error+"\nAlt+R retry · /auto switch model · Your question and draft are safe."
		m.dirty = true
	}
	if msg.event.Type != "text" || time.Since(m.lastRender) >= 33*time.Millisecond {
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

func canRecover(err error) bool {
	var gateway *client.GatewayError
	if errors.As(err, &gateway) {
		return gateway.Status == 401 || gateway.Status == 403 || gateway.Status == 404 || gateway.Status == 429 || gateway.Status >= 500
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func friendlyError(err error) string {
	var gateway *client.GatewayError
	if errors.As(err, &gateway) {
		switch {
		case gateway.Code == "chat_conflict":
			return "This conversation changed in another window. Export your work, then reopen it with Alt+H."
		case gateway.Status == 429 || strings.Contains(gateway.Code, "limit"):
			return "Free usage is temporarily limited. Try again later with Alt+R."
		case gateway.Code == "provider_not_ready" || gateway.Code == "no_free_models":
			return "No models are available right now. Your draft is safe. Use /reconnect to try again."
		case gateway.Status == 401 || gateway.Status == 403:
			return "This connection needs a login. Use /auto to try another free connection."
		case gateway.Status >= 500:
			return "The model connection is unstable. Press Alt+R to retry, or choose /auto."
		case gateway.Status == 400:
			return "Check your message length or start a new conversation with Alt+N. Your work is still here."
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "The response timed out. Press Alt+R to retry."
	}
	return terminalSafe(err.Error())
}
