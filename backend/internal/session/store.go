// SPDX-License-Identifier: MIT

// Package session owns durable conversation revisions and interrupted replies.
package session

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wooinwoo/freeway-cli/backend/internal/contracts"
	"github.com/wooinwoo/freeway-cli/backend/internal/storage"
)

const (
	recordPrefix = "chat-"
	recordSuffix = ".enc"
	recordLimit  = contracts.MaxBody + 1024
	timestamp    = "2006-01-02T15:04:05.000000000Z"
)

var (
	ErrNotFound = errors.New("conversation not found")
	ErrConflict = errors.New("conversation has changed; reload it before saving")
	ErrInvalid  = errors.New("invalid conversation")
)

type Store struct {
	vault *storage.Vault
	mu    sync.RWMutex
	index map[string]entry
}

type record struct {
	Chat      contracts.Conversation `json:"conversation"`
	UpdatedAt time.Time              `json:"updatedAt"`
}

type entry struct {
	summary  contracts.ConversationSummary
	revision int
}

func Open(vault *storage.Vault) (*Store, error) {
	s := &Store{vault: vault, index: make(map[string]entry)}
	names, err := vault.Names(recordPrefix)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		id := strings.TrimSuffix(strings.TrimPrefix(name, recordPrefix), recordSuffix)
		item, err := s.read(id)
		if err != nil {
			return nil, fmt.Errorf("load conversation %q: %w", id, err)
		}
		s.index[id] = indexEntry(item)
	}
	return s, nil
}

func (s *Store) List() []contracts.ConversationSummary {
	s.mu.RLock()
	list := make([]contracts.ConversationSummary, 0, len(s.index))
	for _, item := range s.index {
		list = append(list, item.summary)
	}
	s.mu.RUnlock()
	slices.SortFunc(list, func(a, b contracts.ConversationSummary) int {
		if a.UpdatedAt == b.UpdatedAt {
			return strings.Compare(a.ID, b.ID)
		}
		return strings.Compare(b.UpdatedAt, a.UpdatedAt)
	})
	return list
}

func (s *Store) Get(id string) (contracts.Conversation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, exists := s.index[id]; !exists {
		return contracts.Conversation{}, ErrNotFound
	}
	item, err := s.read(id)
	if err != nil {
		return contracts.Conversation{}, err
	}
	// A newly loaded chat has no connection to the original in-flight stream.
	for i := range item.Chat.Messages {
		message := &item.Chat.Messages[i]
		if message.Role == "assistant" && message.Status == "streaming" {
			message.Status = "stopped"
		}
	}
	return item.Chat, nil
}

func (s *Store) Create(chat contracts.Conversation) (contracts.Conversation, error) {
	if chat.ID != "" || chat.Revision != 0 {
		return contracts.Conversation{}, fmt.Errorf("%w: identity is assigned by the gateway", ErrInvalid)
	}
	if err := validate(chat); err != nil {
		return contracts.Conversation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	chat.ID = strings.ToLower(rand.Text())
	if _, exists := s.index[chat.ID]; exists {
		return contracts.Conversation{}, errors.New("conversation ID collision")
	}
	chat.Revision = 1
	return s.commit(chat)
}

func (s *Store) Update(id string, chat contracts.Conversation) (contracts.Conversation, error) {
	if (chat.ID != "" && chat.ID != id) || chat.Revision < 1 {
		return contracts.Conversation{}, fmt.Errorf("%w: identity and revision must match the resource", ErrInvalid)
	}
	if err := validate(chat); err != nil {
		return contracts.Conversation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, exists := s.index[id]
	if !exists {
		return contracts.Conversation{}, ErrNotFound
	}
	if chat.Revision != previous.revision {
		return contracts.Conversation{}, ErrConflict
	}
	if chat.Revision == math.MaxInt {
		return contracts.Conversation{}, errors.New("conversation revision limit reached")
	}
	chat.ID = id
	chat.Revision++
	return s.commit(chat)
}

func (s *Store) read(id string) (record, error) {
	var item record
	data, err := s.vault.Read(filename(id), recordLimit)
	if err != nil {
		return item, err
	}
	if err := json.Unmarshal(data, &item); err != nil || item.Chat.ID != id || item.Chat.Revision < 1 || item.UpdatedAt.IsZero() {
		return record{}, errors.New("invalid stored conversation")
	}
	if err := validate(item.Chat); err != nil {
		return record{}, err
	}
	return item, nil
}

// commit runs under mu. The index changes only once the new record is published.
func (s *Store) commit(chat contracts.Conversation) (contracts.Conversation, error) {
	wire, err := json.Marshal(chat)
	if err != nil {
		return contracts.Conversation{}, err
	}
	if len(wire) > contracts.MaxBody {
		return contracts.Conversation{}, fmt.Errorf("%w: conversation exceeds 8 MiB when encoded", ErrInvalid)
	}
	item := record{Chat: chat, UpdatedAt: time.Now().UTC()}
	data, err := json.Marshal(item)
	if err != nil {
		return contracts.Conversation{}, err
	}
	if len(data) > recordLimit {
		return contracts.Conversation{}, fmt.Errorf("%w: conversation exceeds the storage size limit", ErrInvalid)
	}
	if err := s.vault.Write(filename(chat.ID), data); err != nil {
		return contracts.Conversation{}, err
	}
	s.index[chat.ID] = indexEntry(item)
	return chat, nil
}

func filename(id string) string { return recordPrefix + id + recordSuffix }

func indexEntry(item record) entry {
	chat := item.Chat
	preview := ""
	for i := len(chat.Messages) - 1; i >= 0; i-- {
		if message := chat.Messages[i]; message.Role == "user" && strings.TrimSpace(message.Content) != "" {
			preview = previewText(message.Content)
			break
		}
	}
	return entry{revision: chat.Revision, summary: contracts.ConversationSummary{
		ID: chat.ID, Title: chat.Title, Model: chat.Model, UpdatedAt: item.UpdatedAt.UTC().Format(timestamp),
		MessageCount: len(chat.Messages), Preview: preview, Workspace: chat.Workspace,
		HasDraft: chat.Draft != "" || len(chat.DraftFiles) > 0 || chat.Stash != nil, QueuedCount: len(chat.Queued),
	}}
}

func previewText(text string) string {
	text = strings.TrimSpace(text)
	count := 0
	for offset := range text {
		if count == 160 {
			return text[:offset]
		}
		count++
	}
	return text
}
