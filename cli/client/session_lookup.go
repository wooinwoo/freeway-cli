package client

import (
	"path/filepath"
	"runtime"
	"strings"
)

func SameWorkspace(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func ScopeConversations(records []ConversationSummary, workspace string, all bool) []ConversationSummary {
	result := []ConversationSummary{}
	for _, record := range records {
		if all || record.Workspace == "" || SameWorkspace(record.Workspace, workspace) {
			result = append(result, record)
		}
	}
	return result
}

func LatestConversation(records []ConversationSummary, workspace string, all bool) (ConversationSummary, bool) {
	for _, record := range records {
		if !record.Damaged && (all || record.Workspace != "" && SameWorkspace(record.Workspace, workspace)) {
			return record, true
		}
	}
	for _, record := range records {
		if !record.Damaged && record.Workspace == "" {
			return record, true
		}
	}
	return ConversationSummary{}, false
}

func FindConversations(records []ConversationSummary, query string) []ConversationSummary {
	for _, record := range records {
		if strings.EqualFold(record.ID, query) {
			return []ConversationSummary{record}
		}
	}
	matches := []ConversationSummary{}
	for _, record := range records {
		if len(query) >= 4 && strings.HasPrefix(strings.ToLower(record.ID), strings.ToLower(query)) {
			matches = append(matches, record)
		}
	}
	if len(matches) > 0 {
		return matches
	}
	for _, record := range records {
		if strings.EqualFold(record.Title, query) {
			matches = append(matches, record)
		}
	}
	if len(matches) > 0 {
		return matches
	}
	for _, record := range records {
		if strings.Contains(strings.ToLower(record.Title), strings.ToLower(query)) {
			matches = append(matches, record)
		}
	}
	return matches
}
