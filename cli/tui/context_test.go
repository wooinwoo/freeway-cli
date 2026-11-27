package tui

import (
	"fmt"
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
	plan := planContext(history, &current, "system instructions", client.ContextSelection{RecentTurns: 1}, false)
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

func TestContextMarksAQuestionWhoseReplyWasStopped(t *testing.T) {
	history := []client.Message{
		{Role: "user", Content: "answered question"},
		{Role: "assistant", Content: "answer", Status: "done"},
		{Role: "user", Content: "stopped before any text"},
		{Role: "user", Content: "stopped midway"},
		{Role: "assistant", Content: "partial", Status: "stopped"},
	}
	current := client.Message{Role: "user", Content: "say hi"}
	var got []string
	for _, message := range planContext(history, &current, "", client.ContextSelection{RecentTurns: -1}, false).messages() {
		got = append(got, message.Role+": "+message.Content)
	}
	want := []string{"user: answered question", "assistant: answer",
		"user: stopped before any text", "assistant: " + interruptedReply,
		"user: stopped midway", "assistant: " + interruptedReply, "user: say hi"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("two questions in a row make the model answer both:\n%s", strings.Join(got, "\n"))
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
	if len(messages) != 5 || len(messages[0].Files) != 1 || messages[0].Files[0] != reference ||
		len(messages[2].Files) != 0 || len(messages[4].Files) != 1 || messages[4].Files[0] != sensitive ||
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
	if len(retry[0].Files) != 1 || retry[0].Files[0] != reference || len(retry[2].Files) != 1 || retry[2].Files[0] != sensitive ||
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

func TestAutoResendsOnlyRecentTurnsThatFitItsBudget(t *testing.T) {
	var history []client.Message
	for i := range 12 {
		history = append(history,
			client.Message{Role: "user", Content: fmt.Sprintf("질문 %d %s", i, strings.Repeat("가", 1000))},
			client.Message{Role: "assistant", Content: strings.Repeat("답", 1000)})
	}
	history[len(history)-2].Files = []client.Attachment{{Path: "big.txt", Content: strings.Repeat("x", 100000)}}
	current := client.Message{Role: "user", Content: "그 파일 다시 설명해줘"}
	plan := planContext(history, &current, "", defaultContext, true)
	sent := plan.messages()
	if first := sent[0].Content; !strings.HasPrefix(first, "질문 11 ") || len(sent[0].Files) != 1 {
		t.Fatalf("Auto dropped the latest exchange or its file; first message: %.20q", first)
	}
	if whole := planContext(history, &current, "", defaultContext, false).messages(); len(whole) != len(history)+1 {
		t.Fatalf("a chosen model got %d of %d messages", len(whole), len(history)+1)
	}
	history[len(history)-2].Files = nil
	sent = planContext(history, &current, "", defaultContext, true).messages()
	if kept := (len(sent) - 1) / 2; kept < 2 || kept >= 12 {
		t.Fatalf("Auto kept %d of 12 earlier turns under its budget", kept)
	}
	if limited := planContext(history, &current, "", client.ContextSelection{RecentTurns: 8, IncludePriorAttachments: true}, true).messages(); len(limited) != 17 {
		t.Fatalf("a context set with F8 was overridden by Auto: %d messages", len(limited))
	}
}
