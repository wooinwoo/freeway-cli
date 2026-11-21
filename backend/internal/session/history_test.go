package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/wooinwoo/freeway-cli/backend/internal/contracts"
)

func historyLibrary(size int) *Store {
	store := &Store{index: make(map[string]entry, size)}
	for i := range size {
		workspace := filepath.Join("projects", "alpha")
		if i%2 == 1 {
			workspace = filepath.Join("projects", "beta")
		}
		id := fmt.Sprintf("session-%06d", i)
		record := contracts.ConversationSummary{
			ID: id, Workspace: workspace, Title: fmt.Sprintf("Planning conversation %d", i),
			Preview:   fmt.Sprintf("Checks for release milestone %d with outstanding follow-ups", i),
			UpdatedAt: time.Unix(int64(i/4+1), 0).UTC().Format(timestamp), MessageCount: 8,
		}
		store.index[id] = entry{summary: record}
	}
	return store
}

func TestHistoryPagesAndSearchAcrossLibrary(t *testing.T) {
	store := historyLibrary(20_000)
	query := HistoryQuery{Workspace: filepath.Join("projects", "alpha"), Limit: 49}
	first, err := store.History(query)
	if err != nil || len(first.Records) != 49 || first.NextCursor == "" || first.Records[0].ID != "session-019996" {
		t.Fatalf("first page: records=%d cursor=%q err=%v", len(first.Records), first.NextCursor, err)
	}
	query.Cursor = first.NextCursor
	second, err := store.History(query)
	if err != nil || len(second.Records) != 49 || second.Records[0].ID != "session-019902" {
		t.Fatalf("second page: %d records, first=%v, err=%v", len(second.Records), second.Records[:min(1, len(second.Records))], err)
	}
	query.Cursor, query.Search = "", "MILESTONE 0"
	found, err := store.History(query)
	if err != nil || len(found.Records) != 1 || found.Records[0].ID != "session-000000" || found.NextCursor != "" {
		t.Fatalf("search of an old record by preview: %+v, %v", found, err)
	}
	query.Search = "session-000001"
	found, err = store.History(query)
	if err != nil || len(found.Records) != 0 {
		t.Fatalf("search crossed project boundary: %+v, %v", found, err)
	}
	query.Cursor = "not-a-cursor"
	if _, err := store.History(query); !errors.Is(err, ErrHistoryCursor) {
		t.Fatalf("invalid cursor: %v", err)
	}
}

func TestHistoryLatestAndRankedLookup(t *testing.T) {
	store := historyLibrary(4)
	workspace := filepath.Join("projects", "alpha")
	add := func(id, title, scope string, damaged bool) {
		store.index[id] = entry{summary: contracts.ConversationSummary{
			ID: id, Title: title, Workspace: scope, Damaged: damaged,
			UpdatedAt: time.Unix(100, 0).UTC().Format(timestamp),
		}}
	}
	add("legacy", "Imported conversation", "", false)
	add("damaged", "Needs recovery", workspace, true)
	add("alias-title", "session-000000", workspace, true)
	check := func(query HistoryQuery, want string) {
		t.Helper()
		page, err := store.History(query)
		if err != nil || len(page.Records) != 1 || page.Records[0].ID != want {
			t.Fatalf("query %+v: %+v, %v; want %s", query, page, err, want)
		}
	}
	check(HistoryQuery{Workspace: workspace, Mode: "latest", Limit: 1}, "session-000000")
	check(HistoryQuery{Workspace: "unknown", Mode: "latest", Limit: 1}, "legacy")
	check(HistoryQuery{Workspace: workspace, Mode: "lookup", Search: "session-000000", Limit: 50}, "session-000000")
}

func TestHistoryPromptScopeFiltersBeforeRecentLimit(t *testing.T) {
	store, vault := openStore(t, filepath.Join(t.TempDir(), "state"))
	defer vault.Close()
	work := testChat()
	work.Messages[0].Content = "alpha prompt"
	if _, err := store.Create(work, ""); err != nil {
		t.Fatal(err)
	}
	other := testChat()
	other.Workspace = filepath.Join("projects", "beta")
	other.Messages[0].Content = "beta prompt"
	for range 31 {
		if _, err := store.Create(other, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []string{work.Workspace, ""} {
		got, incomplete := store.RecentPrompts(t.Context(), scope)
		if incomplete || scope != "" && !slices.Contains(got, "alpha prompt") ||
			slices.Contains(got, "beta prompt") != (scope == "") {
			t.Fatalf("prompts for %q: %v, incomplete=%t", scope, got, incomplete)
		}
	}
}

func BenchmarkHistoryLargeLibrary(b *testing.B) {
	store := historyLibrary(20_000)
	store.summaries()
	measure := func(b *testing.B, get func() any) {
		b.ReportAllocs()
		size := 0
		for b.Loop() {
			body, err := json.Marshal(get())
			if err != nil {
				b.Fatal(err)
			}
			size = len(body)
		}
		b.ReportMetric(float64(size), "response-B")
	}
	b.Run("LegacyFullList", func(b *testing.B) {
		measure(b, func() any {
			return HistoryPage{Records: store.List()}
		})
	})
	workspace := filepath.Join("projects", "alpha")
	for _, item := range []struct{ name, search string }{
		{"ProjectFirstPage", ""}, {"ProjectSearch", "milestone 0"},
	} {
		b.Run(item.name, func(b *testing.B) {
			measure(b, func() any {
				page, _ := store.History(HistoryQuery{Workspace: workspace, Search: item.search, Limit: 50})
				return page
			})
		})
	}
}
