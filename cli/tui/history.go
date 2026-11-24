package tui

import (
	"context"
	"fmt"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

const historyPageSize = 50
const historyMoreID = "@more"

type historyPageGateway interface {
	HistoryPage(context.Context, client.HistoryQuery) (client.HistoryPage, error)
}

type historyPager struct {
	paged    bool
	ready    bool
	cursor   string
	filter   string
	lookup   string
	loading  bool
	err      string
	sequence uint64
}

type historySearch struct {
	sequence uint64
	epoch    uint64
}

type historyPageFetched struct {
	page     client.HistoryPage
	sequence uint64
	epoch    uint64
	more     bool
	filter   string
	err      error
}

func (m Model) historyWorkspace() string {
	if m.allSessions {
		return ""
	}
	return m.workdir
}

func (m Model) initialHistoryQuery(initial bool) client.HistoryQuery {
	query := client.HistoryQuery{Workspace: m.historyWorkspace(), Limit: historyPageSize}
	term := m.historyQuery
	if initial {
		term = m.resumeID
	}
	switch {
	case term == "--last" || initial && term == "" && m.startMode != "resume":
		query.Mode, query.Limit = "latest", 1
	case term != "":
		query.Mode, query.Search = "lookup", term
	}
	return query
}

func (m Model) historyItems() []list.Item {
	items := make([]list.Item, 0, len(m.history)+1)
	for _, chat := range m.history {
		when := chat.UpdatedAt
		if t, err := time.Parse(time.RFC3339, when); err == nil {
			when = t.Local().Format("01/02 15:04")
		}
		detail := fmt.Sprintf(m.tr("%s · %d messages", "%s · 메시지 %d개"), when, chat.MessageCount)
		if chat.Workspace != "" {
			detail += " · " + chat.Workspace
		}
		if chat.HasDraft {
			detail += m.tr(" · draft", " · 초안")
		}
		if chat.QueuedCount > 0 {
			detail += fmt.Sprintf(m.tr(" · %d queued", " · 예약 %d개"), chat.QueuedCount)
		}
		if chat.Preview != "" {
			detail += " · " + oneLine(chat.Preview)
		}
		items = append(items, modelItem{ID: chat.ID, Name: chat.Title, Detail: detail})
	}
	if m.historyPage.cursor != "" {
		items = append(items, modelItem{ID: historyMoreID, Name: m.tr("Load more conversations", "대화 더 불러오기"), Detail: m.tr("Enter to show the next page", "Enter로 다음 페이지 보기")})
	}
	return items
}

func (m *Model) filterPagedHistory() tea.Cmd {
	if m.historyPage.ready && m.menuQuery == m.historyPage.filter {
		if m.historyPage.loading {
			m.historyPage.sequence++
			m.historyPage.loading = false
		}
		cmd := m.models.list.SetItems(m.menuAll)
		m.models.list.Select(0)
		return cmd
	}
	m.historyPage.sequence++
	m.historyPage.loading, m.historyPage.err = true, ""
	sequence, epoch := m.historyPage.sequence, m.sessionEpoch
	return tea.Tick(180*time.Millisecond, func(time.Time) tea.Msg {
		return historySearch{sequence: sequence, epoch: epoch}
	})
}

func (m Model) fetchHistoryPage(more bool) (tea.Model, tea.Cmd) {
	backend, ok := m.gateway.(historyPageGateway)
	if !ok || more && (m.historyPage.loading || m.historyPage.cursor == "") {
		return m, nil
	}
	query := client.HistoryQuery{Workspace: m.historyWorkspace(), Search: m.menuQuery, Limit: historyPageSize}
	if more {
		query.Cursor = m.historyPage.cursor
		if m.historyPage.lookup != "" && m.menuQuery == "" {
			query.Mode, query.Search = "lookup", m.historyPage.lookup
		}
	}
	m.historyPage.sequence++
	m.historyPage.loading, m.historyPage.err = true, ""
	sequence, epoch, filter := m.historyPage.sequence, m.sessionEpoch, m.menuQuery
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
		defer cancel()
		page, err := backend.HistoryPage(ctx, query)
		return historyPageFetched{page: page, sequence: sequence, epoch: epoch, more: more, filter: filter, err: err}
	}
}

func (m Model) receiveHistoryPage(msg historyPageFetched) (tea.Model, tea.Cmd, bool) {
	if !m.modelsFocused || m.menu != "history" || msg.sequence != m.historyPage.sequence || msg.epoch != m.sessionEpoch {
		return m, nil, true
	}
	m.historyPage.loading = false
	m.historyPage.ready = true
	m.historyPage.filter = msg.filter
	if msg.err != nil {
		m.historyPage.err = "Could not load conversations. Ctrl+R to retry."
		if msg.more {
			return m, nil, true
		}
		m.history = nil
		m.historyPage.cursor, m.historyPage.lookup = "", ""
		cmd := m.refreshMenuItems(m.historyItems())
		return m, cmd, true
	}
	requestedCursor := m.historyPage.cursor
	m.historyPage.cursor = msg.page.NextCursor
	if !msg.more {
		m.historyPage.lookup = ""
	}
	if !msg.page.Paged {
		m.historyPage.paged = false
		m.allHistory = msg.page.Records
		m.history = client.ScopeConversations(msg.page.Records, m.workdir, m.allSessions)
		cmd := m.refreshMenuItems(m.historyItems())
		return m, cmd, true
	}
	if !msg.more {
		m.history = msg.page.Records
		cmd := m.refreshMenuItems(m.historyItems())
		return m, cmd, true
	}
	if requestedCursor == msg.page.NextCursor {
		m.historyPage.cursor = ""
		m.historyPage.err = "The gateway repeated the cursor. Ctrl+R to refresh."
	}
	seen := make(map[string]bool, len(m.history))
	for _, record := range m.history {
		seen[record.ID] = true
	}
	oldLength := len(m.history)
	m.history = append([]client.ConversationSummary(nil), m.history...)
	for _, record := range msg.page.Records {
		if !seen[record.ID] {
			m.history = append(m.history, record)
			seen[record.ID] = true
		}
	}
	next := m.refreshMenuItems(m.historyItems())
	if len(m.history) > oldLength {
		m.selectMenuItem(m.history[oldLength].ID)
	}
	return m, next, true
}
