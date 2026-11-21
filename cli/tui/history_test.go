package tui

import (
	"context"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

type pagedHistoryFixture struct {
	memoryGateway
	queries []client.HistoryQuery
	scopes  []string
}

func (g *pagedHistoryFixture) HistoryPage(_ context.Context, query client.HistoryQuery) (client.HistoryPage, error) {
	g.queries = append(g.queries, query)
	id, cursor := "first", "after-first"
	switch {
	case query.Cursor != "":
		id, cursor = "second", ""
	case query.Search != "":
		id, cursor = query.Search, ""
		if query.Workspace == "" {
			id = "all: " + id
		}
	case query.Workspace == "":
		id, cursor = "all-projects", ""
	}
	return client.HistoryPage{Paged: true, NextCursor: cursor, Records: []client.ConversationSummary{{ID: id, Title: id}}}, nil
}

func (g *pagedHistoryFixture) ScopedPromptHistory(_ context.Context, workspace string) ([]string, error) {
	g.scopes = append(g.scopes, workspace)
	if workspace == "" {
		return []string{"prompt from another project"}, nil
	}
	return nil, nil
}

func TestPagedHistoryLoadsOlderRecordsAndIgnoresStaleSearch(t *testing.T) {
	gateway := &pagedHistoryFixture{}
	m := connectedFixture(gateway)
	m.workdir = filepath.Join("projects", "alpha")
	next, cmd := m.showHistory(false)
	m = update(next.(Model), cmd())
	if len(gateway.queries) != 1 || gateway.queries[0].Limit != 50 || gateway.queries[0].Workspace != m.workdir {
		t.Fatalf("browse sent %+v", gateway.queries)
	}
	m = update(m, press(tea.KeyEnd))
	selected, ok := m.models.list.SelectedItem().(modelItem)
	if !ok || selected.ID != historyMoreID {
		t.Fatalf("End selected %+v", selected)
	}
	next, cmd = m.chooseItem(selected)
	m = update(next.(Model), cmd())
	selected = m.models.list.SelectedItem().(modelItem)
	if len(m.history) != 2 || selected.ID != "second" || gateway.queries[1].Cursor != "after-first" {
		t.Fatalf("next page: selection=%q history=%+v queries=%+v", selected.ID, m.history, gateway.queries)
	}
	next, _ = m.appendMenuQuery("old search")
	m = next.(Model)
	next, old, _ := m.updateConvenience(historySearch{sequence: m.historyPage.sequence, epoch: m.sessionEpoch})
	m = next.(Model)
	next, all := m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	m = update(next.(Model), all())
	m = update(m, old())
	if len(m.history) != 1 || m.history[0].ID != "all: old search" || gateway.queries[2].Workspace != "" ||
		gateway.queries[3].Workspace != m.workdir || !m.allSessions || m.historyPage.loading {
		t.Fatalf("scope race: history=%+v queries=%+v", m.history, gateway.queries)
	}
}

func TestStartupHistoryRequestsOnlyWhatTheModeNeeds(t *testing.T) {
	gateway := &pagedHistoryFixture{}
	m := connectedFixture(gateway)
	m.workdir = "alpha"
	got := m.initialHistoryQuery(true)
	if got.Mode != "latest" || got.Limit != 1 || got.Workspace != "alpha" {
		t.Fatalf("default startup: %+v", got)
	}
	m.resumeID = "older title"
	got = m.initialHistoryQuery(true)
	if got.Mode != "lookup" || got.Search != m.resumeID {
		t.Fatalf("direct resume: %+v", got)
	}
	m.resumeID, m.startMode = "", "new"
	_, cmd := m.showHistory(true)
	if _, ok := cmd().(historyLoaded); !ok || len(gateway.queries) > 0 {
		t.Fatalf("new conversation queried history: %+v", gateway.queries)
	}
}

func TestPromptPickerAllowsAllProjectsFromAnEmptyProject(t *testing.T) {
	gateway := &pagedHistoryFixture{}
	m := connectedFixture(gateway)
	m.workdir = "alpha"
	m.remember("/help")
	next, scoped := m.openPromptSearch()
	m = next.(Model)
	if m.menu != "prompts" || !m.promptsLoading || len(m.menuAll) != 0 {
		t.Fatalf("empty picker: menu=%q, loading=%t, items=%+v", m.menu, m.promptsLoading, m.menuAll)
	}
	next, all := m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	m = update(next.(Model), all())
	m = update(m, scoped())
	items := m.promptItems()
	if !m.allPrompts || m.promptsLoading || len(items) != 1 ||
		items[0].(modelItem).Detail != "prompt from another project" ||
		len(m.menuAll) != 1 || gateway.scopes[0] != "" || gateway.scopes[1] != "alpha" {
		t.Fatalf("prompt picker: scopes=%v items=%+v", gateway.scopes, items)
	}
	next, _ = m.changeFocus(false)
	next, fresh := next.(Model).openPromptSearch()
	m = update(next.(Model), fresh())
	if len(gateway.scopes) != 3 || gateway.scopes[2] != "" || len(m.menuAll) != 1 {
		t.Fatalf("reopening prompt search did not refresh the current scope: %v", gateway.scopes)
	}
}
