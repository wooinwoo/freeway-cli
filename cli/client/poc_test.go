package client

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixturePOC(t *testing.T) *POC {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node.js is required for bridge integration tests")
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"model-groups.mjs": `export const stateModelGroups = s => s.groups; export const rankedGroups = g => g;`,
		"client.mjs": `import {open,unlink} from 'node:fs/promises';
export class Client {
  async state() {
    const lock = await open('bootstrap.lock','wx');
    await new Promise(r=>setTimeout(r,40));
    await lock.close(); await unlink('bootstrap.lock');
    return {settings:{preferredModels:[]},groups:[
    {id:'free/ready',name:'Ready',free:true,ready:true},
    {id:'paid/ready',name:'Paid',free:false,ready:true},
    {id:'free/offline',name:'Offline',free:true,ready:false}
  ]}; }
  async complete(body, {onText}) {
    if (body.model === 'wait') await new Promise(r => setTimeout(r,60000));
    if (body.model === 'fail') throw new Error('quota exceeded');
    if (body.messages.map(m=>m.role).join(',') !== 'system,user' || body.messages[0].content !== 'Keep context' || body.max_tokens !== 4096) throw new Error('request context lost');
    const content = 'Echo: ' + body.messages[1].content;
    onText('Echo: '); onText(body.messages[1].content);
    return {content,model:'fixture/free',provider:'fixture'};
  }
}`,
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(dir, "src", name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	poc, err := NewPOC(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(poc.Close)
	return poc
}

func TestBridgeSerializesBootstrap(t *testing.T) {
	poc := fixturePOC(t)
	errors := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := poc.Models(context.Background())
			errors <- err
		}()
	}
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatalf("concurrent gateway bootstrap: %v", err)
		}
	}
}

func TestBridgeExcludesPaidAndUnavailableModels(t *testing.T) {
	models, err := fixturePOC(t).Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "free/auto" || models[1].ID != "free/ready" {
		t.Fatalf("model selector exposed paid or unavailable routes: %#v", models)
	}
}

func TestBridgeStreamsUnicodeAndConversation(t *testing.T) {
	var events []Event
	err := fixturePOC(t).Stream(context.Background(), "free/ready", []Message{
		{Role: "system", Content: "Keep context"}, {Role: "user", Content: "안녕 🌏\n두 번째 줄"},
	}, func(event Event) { events = append(events, event) })
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Type != "text" || events[1].Text != "안녕 🌏\n두 번째 줄" ||
		events[2].Type != "done" || events[2].Text != "Echo: 안녕 🌏\n두 번째 줄" || events[2].Provider != "fixture" {
		t.Fatalf("stream or final route metadata was lost: %#v", events)
	}
}

func TestBridgeReturnsErrorsAndCancelsWorker(t *testing.T) {
	poc := fixturePOC(t)
	if err := poc.Stream(context.Background(), "fail", nil, func(Event) {}); err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("upstream error was lost: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := poc.Stream(ctx, "wait", nil, func(Event) {})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 2*time.Second {
		t.Fatalf("worker did not stop on cancellation: %v", err)
	}
}
