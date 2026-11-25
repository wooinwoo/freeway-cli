package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

func TestUserMessageWrapsBetweenWords(t *testing.T) {
	content := "Write a 400-word story about a lighthouse keeper."
	rendered, _ := New().renderUserMessage(client.Message{Role: "user", Content: content}, 40, grooveColors())
	text := ansi.Strip(rendered)
	for _, word := range strings.Fields(content) {
		if !strings.Contains(text, word) {
			t.Fatalf("%q was split across rows:\n%s", word, text)
		}
	}
}
