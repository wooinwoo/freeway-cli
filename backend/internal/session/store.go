// SPDX-License-Identifier: MIT

// Package session owns durable conversation revisions and interrupted replies.
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	// Keep records inside the original size bound so a rollback can read them.
	recordLimit  = contracts.MaxBody + 1024
	receiptLimit = 4
	timestamp    = "2006-01-02T15:04:05.000000000Z"
)

var (
	ErrNotFound        = errors.New("conversation not found")
	ErrConflict        = errors.New("conversation has changed; reload it before saving")
	ErrInvalid         = errors.New("invalid conversation")
	ErrUnreadable      = errors.New("saved conversation needs recovery")
	ErrKeyReuse        = errors.New("save key already used for a different request")
	ErrContextRequired = errors.New("conversation has explicit context settings")
)

type Store struct {
	vault   *storage.Vault
	mu      sync.RWMutex
	index   map[string]entry
	ordered []contracts.ConversationSummary
}

type record struct {
	Chat         contracts.Conversation `json:"conversation"`
	UpdatedAt    time.Time              `json:"updatedAt"`
	CreateDigest string                 `json:"createDigest,omitempty"`
	Receipts     []saveReceipt          `json:"receipts,omitempty"`
}

type entry struct {
	summary      contracts.ConversationSummary
	revision     int
	createDigest string
	receipts     []saveReceipt
	hasContext   bool
}

type saveReceipt struct {
	Key      string `json:"key"`
	Digest   string `json:"digest"`
	Revision int    `json:"revision"`
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
			slog.Warn("saved conversation is unreadable", "id", id, "error", err)
			s.index[id] = entry{summary: contracts.ConversationSummary{ID: id, Title: "Conversation needs recovery", Damaged: true}}
			continue
		}
		s.index[id] = indexEntry(item)
	}
	return s, nil
}

func (s *Store) List() []contracts.ConversationSummary {
	return slices.Clone(s.summaries())
}

// summaries returns an immutable, sorted snapshot of the session index.
func (s *Store) summaries() []contracts.ConversationSummary {
	s.mu.RLock()
	cached := s.ordered
	s.mu.RUnlock()
	if cached != nil {
		return cached
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ordered != nil {
		return s.ordered
	}
	list := make([]contracts.ConversationSummary, 0, len(s.index))
	for _, item := range s.index {
		list = append(list, item.summary)
	}
	slices.SortFunc(list, compareSummaries)
	s.ordered = list
	return list
}

func (s *Store) RecentPrompts(ctx context.Context, workspace string) ([]string, bool) {
	const maxSessions, maxPrompts, maxBytes = 30, 200, 600000
	prompts := []string{}
	size, incomplete := 0, false
	scanned := 0
collect:
	for _, record := range s.summaries() {
		if ctx.Err() != nil {
			incomplete = true
			break
		}
		if workspace != "" && !sameWorkspace(record.Workspace, workspace) {
			continue
		}
		if scanned == maxSessions {
			break
		}
		scanned++
		if record.Damaged {
			incomplete = true
			continue
		}
		chat, err := s.Get(record.ID)
		if err != nil {
			incomplete = true
			continue
		}
		for i := len(chat.Messages) - 1; i >= 0; i-- {
			message := chat.Messages[i]
			if message.Role != "user" {
				continue
			}
			if len(prompts) >= maxPrompts || size+len(message.Content) > maxBytes {
				break collect
			}
			prompts = append(prompts, message.Content)
			size += len(message.Content)
		}
	}
	slices.Reverse(prompts)
	return prompts, incomplete
}

func (s *Store) Get(id string) (contracts.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, exists := s.index[id]
	if !exists {
		return contracts.Conversation{}, ErrNotFound
	}
	if previous.summary.Damaged {
		return contracts.Conversation{}, ErrUnreadable
	}
	item, err := s.read(id)
	if err != nil {
		slog.Warn("saved conversation is unreadable", "id", id, "error", err)
		previous.summary.Damaged = true
		s.index[id] = previous
		s.ordered = nil
		return contracts.Conversation{}, ErrUnreadable
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

func (s *Store) Create(chat contracts.Conversation, key string) (contracts.Conversation, error) {
	if chat.ID != "" || chat.Revision != 0 {
		return contracts.Conversation{}, fmt.Errorf("%w: identity is assigned by the gateway", ErrInvalid)
	}
	if err := validate(chat); err != nil {
		return contracts.Conversation{}, err
	}
	id := strings.ToLower(rand.Text())
	var digest string
	if key != "" {
		// The ID makes a create retry find its original conversation after a restart.
		sum := sha256.Sum256([]byte("freeway/conversation/create/" + key))
		id = hex.EncodeToString(sum[:16])
		var err error
		digest, err = conversationDigest(chat)
		if err != nil {
			return contracts.Conversation{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	chat.ID = id
	chat.Revision = 1
	if previous, exists := s.index[id]; exists {
		if previous.summary.Damaged {
			return contracts.Conversation{}, ErrUnreadable
		}
		if key != "" && previous.createDigest == digest {
			return chat, nil
		}
		if key != "" {
			return contracts.Conversation{}, ErrKeyReuse
		}
		return contracts.Conversation{}, errors.New("conversation ID collision")
	}
	return s.commit(record{Chat: chat, CreateDigest: digest})
}

func (s *Store) Update(id string, chat contracts.Conversation, key string) (contracts.Conversation, error) {
	if (chat.ID != "" && chat.ID != id) || chat.Revision < 1 {
		return contracts.Conversation{}, fmt.Errorf("%w: identity and revision must match the resource", ErrInvalid)
	}
	if err := validate(chat); err != nil {
		return contracts.Conversation{}, err
	}
	chat.ID = id
	var digest string
	if key != "" {
		var err error
		digest, err = conversationDigest(chat)
		if err != nil {
			return contracts.Conversation{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, exists := s.index[id]
	if !exists {
		return contracts.Conversation{}, ErrNotFound
	}
	if previous.summary.Damaged {
		return contracts.Conversation{}, ErrUnreadable
	}
	if key != "" {
		for _, receipt := range previous.receipts {
			if receipt.Key == key {
				if receipt.Digest != digest {
					return contracts.Conversation{}, ErrKeyReuse
				}
				chat.Revision = receipt.Revision
				return chat, nil
			}
		}
	}
	if chat.Revision != previous.revision {
		return contracts.Conversation{}, ErrConflict
	}
	if previous.hasContext && chat.Context == nil {
		return contracts.Conversation{}, ErrContextRequired
	}
	if chat.Revision == math.MaxInt {
		return contracts.Conversation{}, errors.New("conversation revision limit reached")
	}
	chat.Revision++
	receipts := previous.receipts
	if key != "" {
		receipts = slices.Clone(receipts[max(0, len(receipts)-receiptLimit+1):])
		receipts = append(receipts, saveReceipt{Key: key, Digest: digest, Revision: chat.Revision})
	}
	return s.commit(record{Chat: chat, CreateDigest: previous.createDigest, Receipts: receipts})
}

func conversationDigest(chat contracts.Conversation) (string, error) {
	data, err := json.Marshal(chat)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
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
func (s *Store) commit(item record) (contracts.Conversation, error) {
	chat := item.Chat
	wire, err := json.Marshal(chat)
	if err != nil {
		return contracts.Conversation{}, err
	}
	if len(wire) > contracts.MaxBody {
		return contracts.Conversation{}, fmt.Errorf("%w: conversation exceeds 8 MiB when encoded", ErrInvalid)
	}
	item.UpdatedAt = time.Now().UTC()
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
	s.ordered = nil
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
	return entry{revision: chat.Revision, createDigest: item.CreateDigest, receipts: item.Receipts, hasContext: chat.Context != nil, summary: contracts.ConversationSummary{
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
