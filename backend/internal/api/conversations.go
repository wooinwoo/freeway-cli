// SPDX-License-Identifier: MIT

package api

import (
	"errors"
	"net/http"

	"github.com/wooinwoo/freeway-cli/backend/internal/contracts"
	"github.com/wooinwoo/freeway-cli/backend/internal/session"
)

func (s *server) listConversations(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		Data []contracts.ConversationSummary `json:"data"`
	}{s.chats.List()})
}

func (s *server) getConversation(w http.ResponseWriter, r *http.Request) {
	chat, err := s.chats.Get(r.PathValue("id"))
	writeConversation(w, http.StatusOK, chat, err)
}

func (s *server) createConversation(w http.ResponseWriter, r *http.Request) {
	var chat contracts.Conversation
	if !decodeConversation(w, r, &chat) {
		return
	}
	created, err := s.chats.Create(chat)
	writeConversation(w, http.StatusCreated, created, err)
}

func (s *server) updateConversation(w http.ResponseWriter, r *http.Request) {
	var chat contracts.Conversation
	if !decodeConversation(w, r, &chat) {
		return
	}
	updated, err := s.chats.Update(r.PathValue("id"), chat)
	writeConversation(w, http.StatusOK, updated, err)
}

func decodeConversation(w http.ResponseWriter, r *http.Request, chat *contracts.Conversation) bool {
	return decode(w, r, chat, "title", "model", "settings", "messages", "draft", "draftFiles", "queued", "stash")
}

func writeConversation(w http.ResponseWriter, success int, chat contracts.Conversation, err error) {
	switch {
	case err == nil:
		writeJSON(w, success, chat)
	case errors.Is(err, session.ErrNotFound):
		writeProblem(w, problem(http.StatusNotFound, "chat_not_found", "Conversation not found."))
	case errors.Is(err, session.ErrConflict):
		writeProblem(w, problem(http.StatusConflict, "chat_conflict", "Conversation changed in another client. Reload it before saving."))
	case errors.Is(err, session.ErrInvalid):
		writeProblem(w, problem(http.StatusBadRequest, "invalid_conversation", err.Error()))
	default:
		writeProblem(w, problem(http.StatusInternalServerError, "chat_storage_failed", "Unable to access gateway conversation storage."))
	}
}
