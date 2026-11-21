// SPDX-License-Identifier: MIT

package api

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/wooinwoo/freeway-cli/backend/internal/contracts"
	"github.com/wooinwoo/freeway-cli/backend/internal/session"
)

var saveKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

func (s *server) listConversations(w http.ResponseWriter, r *http.Request) {
	if query := r.URL.Query(); query.Has("limit") {
		limit, err := strconv.Atoi(query.Get("limit"))
		workspace, search, mode := query.Get("workspace"), query.Get("q"), query.Get("mode")
		if err != nil || limit < 1 || limit > 100 || len(workspace) > 4096 || len(search) > 1024 ||
			(mode != "" && mode != "lookup" && mode != "latest") ||
			(mode == "lookup" && search == "") || (mode == "latest" && query.Get("cursor") != "") {
			writeProblem(w, problem(http.StatusBadRequest, "invalid_history_query", "Check the conversation page parameters."))
			return
		}
		page, err := s.chats.History(session.HistoryQuery{Workspace: workspace, Search: search, Mode: mode, Limit: limit, Cursor: query.Get("cursor")})
		if err != nil {
			writeProblem(w, problem(http.StatusBadRequest, "invalid_history_cursor", "Refresh conversations to restart paging."))
			return
		}
		writeJSON(w, http.StatusOK, page)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Data []contracts.ConversationSummary `json:"data"`
	}{s.chats.List()})
}

func (s *server) promptHistory(w http.ResponseWriter, r *http.Request) {
	workspace := r.URL.Query().Get("workspace")
	if len(workspace) > 4096 {
		writeProblem(w, problem(http.StatusBadRequest, "invalid_workspace", "The workspace path is too long."))
		return
	}
	prompts, incomplete := s.chats.RecentPrompts(r.Context(), workspace)
	writeJSON(w, http.StatusOK, struct {
		Data       []string `json:"data"`
		Incomplete bool     `json:"incomplete"`
	}{prompts, incomplete})
}

func (s *server) getConversation(w http.ResponseWriter, r *http.Request) {
	chat, err := s.chats.Get(r.PathValue("id"))
	writeConversation(w, http.StatusOK, chat, err)
}

func (s *server) createConversation(w http.ResponseWriter, r *http.Request) {
	key, ok := saveKey(w, r)
	if !ok {
		return
	}
	var chat contracts.Conversation
	if !decodeConversation(w, r, &chat) {
		return
	}
	created, err := s.chats.Create(chat, key)
	writeConversation(w, http.StatusCreated, created, err)
}

func (s *server) updateConversation(w http.ResponseWriter, r *http.Request) {
	key, ok := saveKey(w, r)
	if !ok {
		return
	}
	var chat contracts.Conversation
	if !decodeConversation(w, r, &chat) {
		return
	}
	updated, err := s.chats.Update(r.PathValue("id"), chat, key)
	writeConversation(w, http.StatusOK, updated, err)
}

func saveKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) == 0 {
		return "", true
	}
	if len(values) == 1 && saveKeyPattern.MatchString(values[0]) {
		return values[0], true
	}
	writeProblem(w, problem(http.StatusBadRequest, "invalid_save_key", "Supply one valid conversation save key."))
	return "", false
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
	case errors.Is(err, session.ErrKeyReuse):
		writeProblem(w, problem(http.StatusConflict, "save_key_reused", "This save key has already committed different conversation data."))
	case errors.Is(err, session.ErrContextRequired):
		writeProblem(w, problem(http.StatusConflict, "context_settings_required", "Reload this conversation with a client that preserves its context settings."))
	case errors.Is(err, session.ErrUnreadable):
		writeProblem(w, problem(http.StatusUnprocessableEntity, "chat_unreadable", "This saved conversation needs recovery. Its original file is still available."))
	case errors.Is(err, session.ErrInvalid):
		writeProblem(w, problem(http.StatusBadRequest, "invalid_conversation", err.Error()))
	default:
		writeProblem(w, problem(http.StatusInternalServerError, "chat_storage_failed", "Unable to access gateway conversation storage."))
	}
}
