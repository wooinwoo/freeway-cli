// SPDX-License-Identifier: MIT

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wooinwoo/freeway-cli/backend/internal/contracts"
	"github.com/wooinwoo/freeway-cli/backend/internal/providers"
)

func (s *server) models(w http.ResponseWriter, r *http.Request) {
	load := s.inference.Models
	refreshed := false
	if r.URL.Query().Get("refresh") == "1" && s.manageProvider != nil {
		if refresher, ok := s.inference.(interface {
			RefreshModels(context.Context) ([]contracts.Model, error)
		}); ok {
			load = refresher.RefreshModels
			refreshed = true
		}
	}
	models, err := load(r.Context())
	if err != nil {
		writeProblem(w, publicError(err, problem(http.StatusServiceUnavailable, "catalog_unavailable", "Unable to refresh the gateway model catalog.")))
		return
	}
	if s.modelPrefix != "" {
		var routes []contracts.Model
		for _, model := range models {
			if strings.HasPrefix(model.ID, s.modelPrefix) {
				routes = append(routes, model)
			}
			for _, id := range model.RouteIDs {
				if strings.HasPrefix(id, s.modelPrefix) {
					route := model
					route.ID, route.RouteIDs = id, nil
					routes = append(routes, route)
				}
			}
		}
		models = routes
	}
	if models == nil {
		models = []contracts.Model{}
	}
	writeJSON(w, http.StatusOK, struct {
		Data      []contracts.Model `json:"data"`
		Refreshed bool              `json:"refreshed,omitempty"`
	}{models, refreshed})
}

func (s *server) providerStatuses(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("provider")
	var statuses []contracts.ProviderStatus
	var err error
	checker, scopedCheck := s.inference.(interface {
		ProviderStatus(context.Context, string) (contracts.ProviderStatus, error)
	})
	if id != "" && scopedCheck {
		var status contracts.ProviderStatus
		status, err = checker.ProviderStatus(r.Context(), id)
		statuses = []contracts.ProviderStatus{status}
	} else {
		statuses, err = s.inference.ProviderStatuses(r.Context())
		if err == nil && id != "" {
			var match []contracts.ProviderStatus
			for _, status := range statuses {
				if status.ID == id {
					match = []contracts.ProviderStatus{status}
					break
				}
			}
			if len(match) == 0 {
				err = &providers.Error{Code: "unknown_provider", Status: http.StatusNotFound}
			}
			statuses = match
		}
	}
	if err != nil {
		var providerError *providers.Error
		if errors.As(err, &providerError) && providerError.Code == "unknown_provider" {
			writeProblem(w, problem(http.StatusNotFound, "unknown_provider", "This provider is disabled in the gateway."))
			return
		}
		writeProblem(w, problem(http.StatusServiceUnavailable, "provider_status_unavailable", "Unable to check provider connections. Try again."))
		return
	}
	if statuses == nil {
		statuses = []contracts.ProviderStatus{}
	}
	for i := range statuses {
		statuses[i].ScopedCheck = scopedCheck
		if s.manageProvider == nil {
			statuses[i].Management = false
		} else if statuses[i].ID == "opencode" || statuses[i].ID == "cline" {
			statuses[i].CLIProfile = s.cliProfile
		}
	}
	writeJSON(w, http.StatusOK, struct {
		Data []contracts.ProviderStatus `json:"data"`
	}{statuses})
}

func (s *server) changeProvider(w http.ResponseWriter, r *http.Request) {
	var change contracts.ProviderChange
	if !decode(w, r, &change, "action") {
		return
	}
	if err := s.manageProvider(r.Context(), r.PathValue("id"), change); err != nil {
		var providerError *providers.Error
		if errors.As(err, &providerError) {
			writeProblem(w, problem(providerError.Status, providerError.Code, "Could not change this provider connection."))
			return
		}
		writeProblem(w, problem(http.StatusServiceUnavailable, "provider_management_unavailable", "Could not change this provider connection."))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type deltaChoice struct {
	Index int `json:"index"`
	Delta struct {
		Content string `json:"content"`
	} `json:"delta"`
}

type streamEvent struct {
	Model    string          `json:"model"`
	Provider string          `json:"provider"`
	Choices  []deltaChoice   `json:"choices"`
	Usage    json.RawMessage `json:"usage,omitempty"`
}

func (s *server) completions(w http.ResponseWriter, r *http.Request) {
	var input contracts.CompletionRequest
	if !decode(w, r, &input, "model", "messages", "stream") {
		return
	}
	if !validCompletion(input) {
		writeProblem(w, problem(http.StatusBadRequest, "invalid_request", "Provide a model, bounded text messages and stream=true."))
		return
	}
	if s.modelPrefix != "" && !strings.HasPrefix(input.Model, s.modelPrefix) {
		writeProblem(w, problem(http.StatusBadRequest, "invalid_model", "Select a specific OpenCode model."))
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	controller := http.NewResponseController(w)
	started := false
	sentBytes, sentRunes := 0, 0
	var outputError error
	emit := func(delta contracts.TextDelta) error {
		if outputError != nil {
			return outputError
		}
		if delta.Text == "" {
			return nil
		}
		sentBytes += len(delta.Text)
		sentRunes += utf8.RuneCountInString(delta.Text)
		if sentBytes > contracts.MaxBody || sentRunes > contracts.MaxMessageRunes {
			return problem(http.StatusBadGateway, "response_too_large", "The upstream reply exceeds the gateway's message size limit.")
		}
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			started = true
		}
		choice := deltaChoice{}
		choice.Delta.Content = delta.Text
		outputError = writeEvent(w, controller, streamEvent{Model: delta.Model, Provider: delta.Provider, Choices: []deltaChoice{choice}})
		if outputError != nil {
			cancel()
		}
		return outputError
	}
	result, err := s.inference.Stream(ctx, input, emit)
	if ctx.Err() != nil || outputError != nil {
		return
	}
	if err == nil && !started {
		err = problem(http.StatusBadGateway, "empty_response", "The upstream returned no response text.")
	}
	if err != nil {
		if !started {
			writeProblem(w, publicError(err, problem(http.StatusBadGateway, "upstream_unavailable", "The selected provider could not respond.")))
			return
		}
		_ = writeEvent(w, controller, errorBody{Error: publicError(err, problem(http.StatusBadGateway, "partial_response_failed", "The selected provider stopped after sending part of the reply."))})
		return
	}
	if err := writeEvent(w, controller, streamEvent{Model: result.Model, Provider: result.Provider, Choices: []deltaChoice{}, Usage: result.Usage}); err != nil {
		return
	}
	_ = flushEvent(w, controller, []byte("[DONE]"))
}

func validCompletion(input contracts.CompletionRequest) bool {
	if !input.Stream || strings.TrimSpace(input.Model) == "" || len(input.Model) > 512 || len(input.Messages) == 0 || len(input.Messages) > 200 {
		return false
	}
	if input.MaxTokens != nil && (*input.MaxTokens < 1 || *input.MaxTokens > 32768) {
		return false
	}
	if input.Temperature != nil && (*input.Temperature < 0 || *input.Temperature > 2) {
		return false
	}
	for _, message := range input.Messages {
		switch message.Role {
		case "system", "developer", "user", "assistant":
		default:
			return false
		}
	}
	return true
}

func writeEvent(w http.ResponseWriter, controller *http.ResponseController, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return flushEvent(w, controller, data)
}

func flushEvent(w http.ResponseWriter, controller *http.ResponseController, data []byte) error {
	_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
	defer controller.SetWriteDeadline(time.Time{})
	if _, err := io.WriteString(w, "data: "); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "\n\n"); err != nil {
		return err
	}
	return controller.Flush()
}
