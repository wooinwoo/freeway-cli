package tui

import (
	"strings"
	"testing"

	"github.com/wooinwoo/freeway-cli/cli/client"
)

func TestContextKeepsWholeRecentTurnsAndOnlyCurrentAttachments(t *testing.T) {
	history := []client.Message{
		{Role: "user", Content: "older question"},
		{Role: "assistant", Content: "older answer", Status: "done"},
		{Role: "user", Content: "recent question", Files: []client.Attachment{{Path: "old.txt", Content: "old attachment"}}},
		{Role: "assistant", Content: "recent answer", Status: "done"},
		{Role: "assistant", Content: "incomplete response", Status: "stopped"},
	}
	current := client.Message{Role: "user", Content: "current question", Files: []client.Attachment{{Path: "new.txt", Content: "new attachment"}}}
	plan := planContext(history, &current, "system instructions", client.ContextSelection{RecentTurns: 1})
	messages := plan.messages()
	if len(messages) != 4 || messages[0].Role != "system" || messages[1].Content != "recent question" ||
		len(messages[1].Files) != 0 || messages[2].Content != "recent answer" ||
		messages[3].Content != "current question" || len(messages[3].Files) != 1 || messages[3].Files[0].Path != "new.txt" {
		t.Fatalf("unexpected context: %+v", messages)
	}
	if len(history[2].Files) != 1 {
		t.Fatal("filter changed the stored transcript")
	}
	estimate := 0
	for _, message := range messages {
		estimate += client.InputTokenEstimate(message)
	}
	if plan.inputEstimate() != estimate {
		t.Fatal("the budget estimate used a different message set")
	}
}

func TestContextSetWhileSavingAppliesToTheFollowingRequest(t *testing.T) {
	gateway := &memoryGateway{}
	m := connectedFixture(gateway)
	m.session.Context = &client.ContextSelection{RecentTurns: 0}
	m.messages = []client.Message{
		{Role: "user", Content: "older question", Files: []client.Attachment{{Path: "old.txt", Content: "old attachment"}}},
		{Role: "assistant", Content: "older answer", Status: "done"},
		{Role: "user", Content: "new question", Files: []client.Attachment{{Path: "new.txt", Content: "new attachment"}}},
	}
	next, save := m.startResponse()
	next, _ = next.(Model).updateContextSelection(defaultContext)
	next, stream := next.(Model).Update(save())
	m, _ = drainResponse(t, next.(Model), stream)
	if len(gateway.requests) != 1 || len(gateway.requests[0]) != 1 || gateway.requests[0][0].Content != "new question" ||
		len(gateway.requests[0][0].Files) != 1 || m.contextSelection().RecentTurns != -1 ||
		!m.contextSelection().IncludePriorAttachments || len(m.contextSelection().ExcludedPriorFiles) != 0 {
		t.Fatalf("pending request was not isolated from a later edit: %+v", gateway.requests)
	}
}

func TestContextCanExcludeOnePathWithoutDroppingDraftOrRetryFiles(t *testing.T) {
	m := connectedFixture(&memoryGateway{})
	sensitive := client.Attachment{Path: "notes/private.txt", Content: "private notes"}
	reference := client.Attachment{Path: "notes/reference.txt", Content: "public reference"}
	m.messages = []client.Message{
		{Role: "user", Content: "original", Files: []client.Attachment{sensitive, reference}},
		{Role: "user", Content: "recent", Files: []client.Attachment{sensitive}},
	}
	m.prompt.SetValue("follow-up")
	m.files = []client.Attachment{sensitive}
	next, _ := m.openMenu("context")
	m = next.(Model)
	rows := 0
	for _, item := range m.menuAll {
		if item.(modelItem).ID == priorFileRow+sensitive.Path {
			rows++
		}
	}
	if rows != 1 {
		t.Fatalf("the same stored path has %d exclusion controls", rows)
	}
	next, _ = m.chooseItem(modelItem{ID: priorFileRow + sensitive.Path})
	m = next.(Model)
	plan := m.nextRequestContext()
	messages := plan.messages()
	if len(messages) != 3 || len(messages[0].Files) != 1 || messages[0].Files[0] != reference ||
		len(messages[1].Files) != 0 || len(messages[2].Files) != 1 || messages[2].Files[0] != sensitive ||
		len(m.messages[0].Files) != 2 || !strings.Contains(m.contextHint(), "1 earlier files included · 2 excluded") {
		t.Fatalf("unexpected per-file context: %+v", messages)
	}
	estimate := 0
	for _, message := range messages {
		estimate += client.InputTokenEstimate(message)
	}
	if plan.inputEstimate() != estimate {
		t.Fatal("excluded files counted toward the request estimate")
	}
	m.responseTarget.context = m.contextSelection()
	next, _ = m.chooseItem(modelItem{ID: priorFileRow + sensitive.Path})
	m = next.(Model)
	retry := m.responseContext().messages()
	if len(retry[0].Files) != 1 || retry[0].Files[0] != reference || len(retry[1].Files) != 1 || retry[1].Files[0] != sensitive ||
		len(m.nextRequestContext().messages()[0].Files) != 2 {
		t.Fatalf("draft and pending retry used the wrong selection: %+v", retry)
	}
}

func TestContextInspectorShowsExcludedEarlierFileAndKeepsTheDraft(t *testing.T) {
	m := connectedFixture(&memoryGateway{})
	m.messages = []client.Message{{Role: "user", Content: "past prompt", Files: []client.Attachment{{Path: "past.txt"}}}}
	m.prompt.SetValue("keep this draft")
	m.files = []client.Attachment{{Path: "current.txt"}}
	next, _ := m.openMenu("context")
	m = next.(Model)
	next, _ = m.chooseItem(modelItem{ID: "attachments"})
	m = next.(Model)
	info := map[string]string{}
	for _, item := range m.menuAll {
		row := item.(modelItem)
		info[row.Name] = row.Detail
	}
	if m.prompt.Value() != "keep this draft" || !strings.Contains(info["past.txt"], "Excluded") ||
		!strings.Contains(info["current.txt"], "included") {
		t.Fatalf("context preview hid or altered the draft: %q %+v", m.prompt.Value(), info)
	}
}

func TestAttachmentDoesNotRewriteTheDraftOutsideAnUnchangedInlineToken(t *testing.T) {
	m := connectedFixture(&memoryGateway{})
	m.prompt.SetValue("New text typed while a file was loading")
	m = update(m, fileAttached{
		file: client.Attachment{Path: "inline.txt"}, epoch: m.sessionEpoch,
		query: fileQuery{Inline: true, Original: "@in", Start: 0, End: 3},
	})
	m = update(m, fileAttached{file: client.Attachment{Path: "explicit.txt"}, epoch: m.sessionEpoch})
	if m.prompt.Value() != "New text typed while a file was loading" || len(m.files) != 2 {
		t.Fatalf("attachment changed the draft: %q %+v", m.prompt.Value(), m.files)
	}
}
