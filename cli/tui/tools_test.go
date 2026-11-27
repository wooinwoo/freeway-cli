package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wooinwoo/freeway-cli/cli/client"
)

// withoutToolRules removes the tool rules Auto adds, for tests about the conversation itself.
func withoutToolRules(messages []client.Message) []client.Message {
	if len(messages) == 0 || messages[0].Role != "system" {
		return messages
	}
	rules := strings.Index(messages[0].Content, "You are running inside Freeway")
	if rules < 0 {
		return messages
	}
	if rules == 0 {
		return messages[1:]
	}
	trimmed := append([]client.Message(nil), messages...)
	trimmed[0].Content = strings.TrimSuffix(messages[0].Content[:rules], "\n\n")
	return trimmed
}

func TestAutoRunsAToolAndShowsOnlyTheAnswer(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("회의는 3시"), 0o600); err != nil {
		t.Fatal(err)
	}
	box := &toolbox{root: root, tr: english}
	var requests [][]client.Message
	replies := []string{`@tool {"name": "read_file", "path": "notes.md"}`, "회의는 3시에 해요."}
	stream := func(messages []client.Message, emit func(client.Event)) error {
		reply := replies[len(requests)]
		requests = append(requests, messages)
		for _, part := range []string{reply[:2], reply[2:]} {
			emit(client.Event{Type: "text", Text: part})
		}
		emit(client.Event{Type: "done", Text: reply, Model: "fixture"})
		return nil
	}
	var shown strings.Builder
	var tools []string
	var final client.Event
	err := streamWithTools(context.Background(), stream, []client.Message{{Role: "user", Content: "회의 몇 시야?"}}, box, func(event client.Event) {
		switch event.Type {
		case "text":
			shown.WriteString(event.Text)
		case "tool":
			tools = append(tools, event.Text)
		case "done":
			final = event
		}
	})
	if err != nil || len(requests) != 2 {
		t.Fatalf("err = %v after %d requests", err, len(requests))
	}
	if shown.String() != "회의는 3시에 해요." || final.Text != "회의는 3시에 해요." || len(tools) != 1 || tools[0] != "reading notes.md" {
		t.Fatalf("shown %q, final %q, tools %q", shown.String(), final.Text, tools)
	}
	result := requests[1][len(requests[1])-1].Content
	if !strings.HasPrefix(result, "Tool result for @tool") || !strings.Contains(result, "회의는 3시") || requests[1][0].Role != "system" {
		t.Fatalf("the second request lacked the tool result: %q", result)
	}
}

func TestToolsStayInsideTheProjectAndSkipKeyFiles(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	for path, content := range map[string]string{
		filepath.Join(root, "src", "login.go"): "func Login() {}",
		filepath.Join(root, ".env"):            "TOKEN=login-secret",
		filepath.Join(outside, "other.txt"):    "login elsewhere",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	box := &toolbox{root: root, tr: english}
	ctx := context.Background()
	if found := box.run(ctx, toolCall{Name: "search_files", Text: "LOGIN"}); found != "src/login.go:1: func Login() {}" {
		t.Fatalf("search found %q", found)
	}
	for _, call := range []toolCall{
		{Name: "read_file", Path: filepath.Join(outside, "other.txt")},
		{Name: "read_file", Path: "../" + filepath.Base(outside) + "/other.txt"},
		{Name: "read_file", Path: ".env"},
		{Name: "list_files", Path: outside},
		{Name: "web_search", Query: "news"},
	} {
		if result := box.run(ctx, call); !strings.HasPrefix(result, "Error: ") {
			t.Fatalf("%+v was allowed: %q", call, result)
		}
	}
	if listed := box.run(ctx, toolCall{Name: "list_files"}); !strings.Contains(listed, "src/") {
		t.Fatalf("list_files gave %q", listed)
	}
	if _, ok := parseToolCall("먼저 볼게요\n@tool {\"name\":\"read_file\"}"); ok {
		t.Fatal("prose before a tool line was taken as a tool call")
	}
}

func TestWebSearchSendsTheKeyAndKeepsTitlesLinksAndSnippets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" || r.Method != http.MethodPost {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"results":[{"title":"Go 1.27 released","url":"https://go.dev/blog/go1.27","content":"Go 1.27 adds ..."}]}`))
	}))
	defer server.Close()
	previous := tavilyURL
	tavilyURL = server.URL
	defer func() { tavilyURL = previous }()
	t.Setenv("TAVILY_API_KEY", "test-key")
	box := newToolbox(t.TempDir(), Preferences{})
	if !strings.Contains(box.instructions(), "web_search") {
		t.Fatal("a configured key did not offer web search")
	}
	got := box.run(context.Background(), toolCall{Name: "web_search", Query: "go release"})
	if want := "1. Go 1.27 released\nhttps://go.dev/blog/go1.27\nGo 1.27 adds ..."; !strings.HasPrefix(got, want) {
		t.Fatalf("search result = %q", got)
	}
	t.Setenv("TAVILY_API_KEY", "")
	if strings.Contains(newToolbox(t.TempDir(), Preferences{}).instructions(), "web_search") {
		t.Fatal("web search was offered without a key")
	}
}
