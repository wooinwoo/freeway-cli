// SPDX-License-Identifier: MIT

package session

import (
	"fmt"
	"math"
	"unicode/utf8"

	"github.com/wooinwoo/freeway-cli/backend/internal/contracts"
)

func validate(chat contracts.Conversation) error {
	content := []struct {
		value string
		limit int
	}{
		{chat.Title, 120}, {chat.SystemPrompt, 20000}, {chat.Draft, 150000},
	}
	for _, field := range content {
		if !boundedText(field.value, field.limit) {
			return fmt.Errorf("%w: text exceeds its limit or is invalid UTF-8", ErrInvalid)
		}
	}
	settings := chat.Settings
	if settings.MaxTokens < 1 || settings.MaxTokens > 32768 || math.IsNaN(settings.Temperature) || settings.Temperature < 0 || settings.Temperature > 2 {
		return fmt.Errorf("%w: settings are out of range", ErrInvalid)
	}
	if chat.Messages == nil || len(chat.Messages) > 198 || chat.DraftFiles == nil || chat.Queued == nil || len(chat.Queued) > 20 {
		return fmt.Errorf("%w: messages, draft files and queue must be bounded arrays", ErrInvalid)
	}
	if err := validateFiles(chat.DraftFiles); err != nil {
		return err
	}
	for _, message := range chat.Messages {
		if message.ID == "" || (message.Role != "user" && message.Role != "assistant") || !boundedText(message.Content, contracts.MaxMessageRunes) {
			return fmt.Errorf("%w: invalid message identity, role or content", ErrInvalid)
		}
		switch message.Status {
		case "done", "streaming", "stopped", "error":
		default:
			return fmt.Errorf("%w: invalid message status", ErrInvalid)
		}
		if err := validateFiles(message.Files); err != nil {
			return err
		}
	}
	for _, prompt := range chat.Queued {
		if err := validatePrompt(prompt); err != nil {
			return err
		}
	}
	if chat.Stash != nil {
		return validatePrompt(*chat.Stash)
	}
	return nil
}

func validatePrompt(prompt contracts.Prompt) error {
	if !boundedText(prompt.Text, 150000) {
		return fmt.Errorf("%w: stored prompt exceeds its limit or is invalid UTF-8", ErrInvalid)
	}
	return validateFiles(prompt.Files)
}

func validateFiles(files []contracts.Attachment) error {
	if len(files) > 5 {
		return fmt.Errorf("%w: at most five attachments are allowed", ErrInvalid)
	}
	for _, file := range files {
		if !boundedText(file.Path, 1000) || !boundedText(file.Content, 200000) {
			return fmt.Errorf("%w: attachment exceeds its limit or is invalid UTF-8", ErrInvalid)
		}
	}
	return nil
}

func boundedText(value string, limit int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= limit
}
