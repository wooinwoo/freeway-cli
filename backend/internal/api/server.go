// SPDX-License-Identifier: MIT

// Package api implements the authenticated HTTP and SSE gateway boundary.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/wooinwoo/freeway-cli/backend/internal/auth"
	"github.com/wooinwoo/freeway-cli/backend/internal/contracts"
	"github.com/wooinwoo/freeway-cli/backend/internal/session"
)

type Inference interface {
	Models(context.Context) ([]contracts.Model, error)
	Stream(context.Context, contracts.CompletionRequest, func(contracts.TextDelta) error) (contracts.Completion, error)
}

type server struct {
	chats     *session.Store
	inference Inference
}

func New(key auth.ClientKey, chats *session.Store, inference Inference) http.Handler {
	s := &server{chats: chats, inference: inference}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", s.models)
	mux.HandleFunc("POST /v1/chat/completions", s.completions)
	mux.HandleFunc("GET /v1/conversations", s.listConversations)
	mux.HandleFunc("POST /v1/conversations", s.createConversation)
	mux.HandleFunc("GET /v1/conversations/{id}", s.getConversation)
	mux.HandleFunc("PUT /v1/conversations/{id}", s.updateConversation)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeProblem(w, problem(http.StatusNotFound, "not_found", "Unknown gateway endpoint."))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !localRequest(r) {
			writeProblem(w, problem(http.StatusForbidden, "local_only", "The gateway accepts only local, same-origin requests."))
			return
		}
		credentials := r.Header.Values("Authorization")
		if len(credentials) != 1 || !key.Matches(credentials[0]) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeProblem(w, problem(http.StatusUnauthorized, "invalid_api_key", "Supply the gateway's local client key."))
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func localRequest(r *http.Request) bool {
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(remote).IsLoopback() {
		return false
	}
	host := strings.Trim(r.Host, "[]")
	if parsed, _, err := net.SplitHostPort(r.Host); err == nil {
		host = parsed
	}
	if !strings.EqualFold(host, "localhost") && !net.ParseIP(host).IsLoopback() {
		return false
	}
	origins := r.Header.Values("Origin")
	return len(origins) == 0 || (len(origins) == 1 && origins[0] == "http://"+r.Host)
}

func problem(status int, code, message string) *contracts.Problem {
	return &contracts.Problem{Status: status, Code: code, Message: message}
}

type errorBody struct {
	Error *contracts.Problem `json:"error"`
}

func writeProblem(w http.ResponseWriter, p *contracts.Problem) {
	if p.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(p.RetryAfter))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(errorBody{Error: p})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil || len(data) > contracts.MaxBody {
		writeProblem(w, problem(http.StatusInternalServerError, "response_too_large", "Unable to serialize this gateway response within its size limit."))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func decode(w http.ResponseWriter, r *http.Request, value any, required ...string) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeProblem(w, problem(http.StatusUnsupportedMediaType, "unsupported_media_type", "Send an application/json request."))
		return false
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, contracts.MaxBody))
	if limit := new(http.MaxBytesError); errors.As(err, &limit) {
		writeProblem(w, problem(http.StatusRequestEntityTooLarge, "request_too_large", "The gateway request exceeds 8 MiB."))
		return false
	}
	invalid := problem(http.StatusBadRequest, "invalid_request", "Invalid or unsupported gateway request fields.")
	if err != nil || !utf8.Valid(data) {
		writeProblem(w, invalid)
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		writeProblem(w, invalid)
		return false
	}
	for _, name := range required {
		field, exists := fields[name]
		if !exists || (name != "stash" && bytes.Equal(field, []byte("null"))) {
			writeProblem(w, invalid)
			return false
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeProblem(w, invalid)
		return false
	}
	return true
}

func publicError(err error, fallback *contracts.Problem) *contracts.Problem {
	var p *contracts.Problem
	if errors.As(err, &p) && p.Status >= 400 && p.Status <= 599 {
		return p
	}
	return fallback
}
