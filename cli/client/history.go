package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

type HistoryQuery struct {
	Workspace string
	Search    string
	Mode      string
	Cursor    string
	Limit     int
}

type HistoryPage struct {
	Records    []ConversationSummary `json:"data"`
	NextCursor string                `json:"nextCursor"`
	Paged      bool                  `json:"-"`
}

func (c *HTTP) HistoryPage(ctx context.Context, query HistoryQuery) (HistoryPage, error) {
	c.probeGatewayFeatures(ctx)
	if !c.hasFeature(featureHistoryPages) {
		records, err := c.Conversations(ctx)
		return HistoryPage{Records: records}, err
	}
	values := url.Values{"limit": {strconv.Itoa(query.Limit)}}
	for key, value := range map[string]string{
		"workspace": query.Workspace, "q": query.Search, "mode": query.Mode, "cursor": query.Cursor,
	} {
		if value != "" {
			values.Set(key, value)
		}
	}
	var page HistoryPage
	err := c.json(ctx, http.MethodGet, "/v1/conversations?"+values.Encode(), nil, &page, "")
	page.Paged = c.hasFeature(featureHistoryPages)
	return page, err
}

func (c *HTTP) ScopedConversations(ctx context.Context, workspace string) ([]ConversationSummary, error) {
	records := []ConversationSummary{}
	seenIDs := map[string]bool{}
	seenCursors := map[string]bool{}
	query := HistoryQuery{Workspace: workspace, Limit: 100}
	for {
		page, err := c.HistoryPage(ctx, query)
		if err != nil {
			return nil, err
		}
		if !page.Paged {
			return ScopeConversations(page.Records, workspace, workspace == ""), nil
		}
		for _, record := range page.Records {
			if !seenIDs[record.ID] {
				records = append(records, record)
				seenIDs[record.ID] = true
			}
		}
		if page.NextCursor == "" {
			return records, nil
		}
		if seenCursors[page.NextCursor] {
			return nil, errors.New("gateway repeated a conversation page cursor")
		}
		seenCursors[page.NextCursor] = true
		query.Cursor = page.NextCursor
	}
}
