// SPDX-License-Identifier: MIT

package session

import (
	"encoding/base64"
	"errors"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/wooinwoo/freeway-cli/backend/internal/contracts"
)

var ErrHistoryCursor = errors.New("invalid conversation page cursor")

type HistoryQuery struct {
	Workspace string
	Search    string
	Mode      string
	Cursor    string
	Limit     int
}

type HistoryPage struct {
	Records    []contracts.ConversationSummary `json:"data"`
	NextCursor string                          `json:"nextCursor,omitempty"`
}

func (s *Store) History(query HistoryQuery) (HistoryPage, error) {
	page := HistoryPage{Records: []contracts.ConversationSummary{}}
	if query.Limit < 1 || query.Limit > 100 {
		return page, errors.New("conversation page limit must be from 1 to 100")
	}
	after, err := decodeCursor(query.Cursor)
	if err != nil {
		return page, err
	}
	records := s.summaries()
	if query.Mode == "latest" {
		legacy := -1
		for i, record := range records {
			if record.Damaged {
				continue
			}
			if query.Workspace == "" || sameWorkspace(record.Workspace, query.Workspace) {
				page.Records = append(page.Records, record)
				return page, nil
			}
			if legacy < 0 && record.Workspace == "" {
				legacy = i
			}
		}
		if legacy >= 0 {
			page.Records = append(page.Records, records[legacy])
		}
		return page, nil
	}
	term := strings.ToLower(strings.TrimSpace(query.Search))
	bestRank := 0
	if query.Mode == "lookup" {
		for _, record := range records {
			if acceptsWorkspace(record, query.Workspace) {
				bestRank = max(bestRank, lookupRank(record, term))
			}
		}
		if bestRank == 0 {
			return page, nil
		}
	}
	start := 0
	if after.ID != "" {
		start = sort.Search(len(records), func(i int) bool { return compareSummaries(records[i], after) > 0 })
	}
	for _, record := range records[start:] {
		if !acceptsWorkspace(record, query.Workspace) {
			continue
		}
		if query.Mode == "lookup" {
			if lookupRank(record, term) != bestRank {
				continue
			}
		} else if term != "" && !strings.Contains(strings.ToLower(record.Title), term) &&
			!strings.Contains(strings.ToLower(record.ID), term) && !strings.Contains(strings.ToLower(record.Preview), term) &&
			!strings.Contains(strings.ToLower(record.Workspace), term) {
			continue
		}
		if len(page.Records) == query.Limit {
			last := page.Records[len(page.Records)-1]
			page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(last.UpdatedAt + "\n" + last.ID))
			break
		}
		page.Records = append(page.Records, record)
	}
	return page, nil
}

func compareSummaries(a, b contracts.ConversationSummary) int {
	if a.UpdatedAt == b.UpdatedAt {
		return strings.Compare(a.ID, b.ID)
	}
	return strings.Compare(b.UpdatedAt, a.UpdatedAt)
}

func acceptsWorkspace(summary contracts.ConversationSummary, workspace string) bool {
	return workspace == "" || summary.Workspace == "" || sameWorkspace(summary.Workspace, workspace)
}

func sameWorkspace(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func lookupRank(record contracts.ConversationSummary, term string) int {
	id, title := strings.ToLower(record.ID), strings.ToLower(record.Title)
	switch {
	case id == term:
		return 4
	case len(term) >= 4 && strings.HasPrefix(id, term):
		return 3
	case title == term:
		return 2
	case term != "" && strings.Contains(title, term):
		return 1
	default:
		return 0
	}
}

func decodeCursor(cursor string) (contracts.ConversationSummary, error) {
	if cursor == "" {
		return contracts.ConversationSummary{}, nil
	}
	if len(cursor) > 200 {
		return contracts.ConversationSummary{}, ErrHistoryCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	date, id, hasID := strings.Cut(string(data), "\n")
	if err != nil || !hasID || len(id) < 1 || len(id) > 128 || strings.ContainsAny(id, "\r\n") {
		return contracts.ConversationSummary{}, ErrHistoryCursor
	}
	if date != "" {
		if _, err := time.Parse(timestamp, date); err != nil {
			return contracts.ConversationSummary{}, ErrHistoryCursor
		}
	}
	return contracts.ConversationSummary{UpdatedAt: date, ID: id}, nil
}
