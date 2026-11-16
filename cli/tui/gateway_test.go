package tui

import (
	"context"
	"strings"
	"testing"

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
	m = update(m, m.Init()())
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
	if len(m.messages) != 4 || !strings.Contains(m.status, "fixture") || strings.Contains(m.View().Content, "Not sent") {
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
