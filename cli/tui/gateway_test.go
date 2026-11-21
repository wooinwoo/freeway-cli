package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

type fixtureGateway struct {
	requests chan []client.Message
}

func (f fixtureGateway) Models(context.Context) ([]client.Model, error) {
	return []client.Model{{ID: "free/auto", Name: "Auto"}}, nil
}

func (f fixtureGateway) Stream(_ context.Context, _ string, messages []client.Message, emit func(client.Event)) error {
	f.requests <- messages
	emit(client.Event{Type: "text", Text: "안녕"})
	emit(client.Event{Type: "done", Text: "안녕 🌏", Model: "fixture/free", Provider: "fixture"})
	return nil
}

func TestConnectedConversationStreamsAndKeepsContext(t *testing.T) {
	backend := fixtureGateway{requests: make(chan []client.Message, 2)}
	m := NewConnected(context.Background(), backend)
	m = update(m, m.loadModels()())
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 28})
	for _, text := range []string{"Hello", "Remember my first message?"} {
		m.prompt.SetValue(text)
		next, command := m.Update(press(tea.KeyEnter))
		m = next.(Model)
		for m.busy {
			next, command = m.Update(command())
			m = next.(Model)
		}
	}
	first, second := <-backend.requests, <-backend.requests
	if len(first) != 1 || len(second) != 3 || second[1].Role != "assistant" || second[1].Content != "안녕 🌏" {
		t.Fatalf("conversation history was not sent on the next turn: %#v", second)
	}
	if len(m.messages) != 4 || m.messages[3].Model != "fixture/free" || strings.Contains(m.View().Content, "Not sent") {
		t.Fatal("connected response or routing status was not displayed")
	}
}

func TestCancelledGenerationIgnoresLateEvents(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 100, Height: 28})
	m.messages = []client.Message{{Role: "user", Content: "Hello"}, {Role: "assistant"}}
	m.busy, m.generation = true, 1
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m = update(m, press(tea.KeyEsc))
	if ctx.Err() == nil || m.busy || len(m.messages) != 1 {
		t.Fatal("Esc did not cancel the request and remove the empty response")
	}
	m.messages = append(m.messages, client.Message{Role: "assistant", Content: "new response"})
	m.busy, m.generation = true, 2
	m = update(m, responseEvent{generation: 1, event: client.Event{Type: "text", Text: "stale"}})
	if m.messages[1].Content != "new response" {
		t.Fatal("late chunk from a cancelled request corrupted the next response")
	}
}

func TestInitialConnectionDoesNotStartParallelRequests(t *testing.T) {
	m := NewConnected(context.Background(), fixtureGateway{})
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 28})
	m.prompt.SetValue("Keep this draft until connected")
	next, cmd := m.Update(press(tea.KeyEnter))
	m = next.(Model)
	if cmd != nil || m.busy || len(m.messages) != 0 || m.prompt.Value() != "Keep this draft until connected" {
		t.Fatal("request started before initial model loading completed")
	}
	_, cmd = m.changeFocus(true)
	if cmd != nil {
		t.Fatal("opening the menu started a second model-loading worker")
	}
}

type pausedReplyGateway struct {
	fixtureGateway
	release <-chan struct{}
}

func (f pausedReplyGateway) StreamWithSettings(ctx context.Context, model string, _ []client.Message, settings client.Settings, emit func(client.Event)) error {
	emit(client.Event{Type: "text", Text: model, Model: "kilo/fixture"})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.release:
	}
	emit(client.Event{Type: "done", Text: model, Model: "kilo/fixture", Usage: &client.Usage{TotalTokens: settings.MaxTokens}})
	return nil
}

func TestFirstChunkShowsTheModelAndKeepsSettingsFromSend(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	release := make(chan struct{})
	m := update(NewConnected(ctx, pausedReplyGateway{release: release}), tea.WindowSizeMsg{Width: 80, Height: 24})
	m.selected, m.settings.MaxTokens = "kilo/original", 4096
	m.saving = true
	m.messages = append(m.messages, newMessage("user", "Hello"))
	next, _ := m.startResponse()
	m = next.(Model)
	m.selected, m.settings.MaxTokens, m.saving = "opencode/next", 8192, false
	next, cmd := m.beginGeneration()
	m = next.(Model)
	defer m.cancel()
	next, cmd = m.Update(cmd())
	m = next.(Model)
	if len(m.messages) != 2 || m.messages[1].Content != "kilo/original" ||
		m.messages[1].Model != "kilo/fixture" || !strings.Contains(m.chat.GetContent(), "Kilo · fixture") {
		t.Fatalf("first chunk or model label was delayed: %s", m.chat.GetContent())
	}
	close(release)
	m = update(m, cmd())
	if m.selected != "opencode/next" || m.lastResponseTokens != 4096 {
		t.Fatal("changing the next model or settings altered the in-flight reply")
	}
}
