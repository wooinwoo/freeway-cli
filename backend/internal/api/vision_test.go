package api

import (
	"strings"
	"testing"

	"github.com/wooinwoo/freeway-cli/backend/internal/contracts"
)

func TestCompletionImagesMustBeFewUserDataURLs(t *testing.T) {
	image := "data:image/jpeg;base64,/9j/4AAQ"
	request := func(role string, images ...string) contracts.CompletionRequest {
		return contracts.CompletionRequest{Model: "free/auto", Stream: true, Messages: []contracts.InputMessage{{Role: role, Content: "look", Images: images}}}
	}
	if !validCompletion(request("user", image)) {
		t.Fatal("a user image was refused")
	}
	for name, input := range map[string]contracts.CompletionRequest{
		"assistant image": request("assistant", image),
		"not a data URL":  request("user", "https://example.com/cat.png"),
		"not an image":    request("user", "data:text/plain;base64,aGk="),
		"five images":     request("user", image, image, image, image, image),
		"too large":       request("user", image+strings.Repeat("A", 4*1024*1024)),
	} {
		if validCompletion(input) {
			t.Fatalf("%s was accepted", name)
		}
	}
}
