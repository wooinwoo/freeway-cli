package tui

import (
	"context"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

type providerGatewayFixture struct{ fixtureGateway }

type scopedProviderRefreshFixture struct {
	providerGatewayFixture
	checked string
	refresh bool
}

func (f *scopedProviderRefreshFixture) ProviderStatus(_ context.Context, id string) (client.ProviderStatus, error) {
	f.checked = id
	return client.ProviderStatus{ID: id, Name: "Groq", State: "ready", KeyConfigured: true, CatalogChecked: true}, nil
}

func (f *scopedProviderRefreshFixture) RefreshModels(ctx context.Context) ([]client.Model, error) {
	f.refresh = true
	return f.Models(ctx)
}

func (providerGatewayFixture) ProviderStatuses(context.Context) ([]client.ProviderStatus, error) {
	return []client.ProviderStatus{
		{ID: "kilo", Name: "Kilo", State: "ready", FreeModels: 2},
		{ID: "groq", Name: "Groq", State: "needs_key"},
		{ID: "opencode", Name: "OpenCode", State: "opencode_not_installed"},
		{ID: "gemini_api", Name: "Gemini API", State: "ready", FreeModels: 2, KeyConfigured: true},
	}, nil
}

func TestModelPickerSelectsGroupIncludingOpenCode(t *testing.T) {
	const picked = "model/deepseek/deepseek-v4.1-flash"
	const old = "opencode/deepseek-v4.1-flash-free"
	m := update(NewConnected(t.Context(), fixtureGateway{}), tea.WindowSizeMsg{Width: 80, Height: 24})
	m = update(m, modelsLoaded{models: []client.Model{
		{ID: "free/auto", Name: "Auto", Free: true, Ready: true},
		{ID: picked, Name: "DeepSeek V4.1 Flash", Detail: "Kilo · OpenCode", RouteIDs: []string{old}, Free: true, Ready: true},
	}})
	m.prompt.SetValue("Keep this draft")
	m = update(m, press(tea.KeyF2))
	view := ansi.Strip(m.View().Content)
	if strings.Count(view, "DeepSeek V4.1 Flash") != 1 || !strings.Contains(view, "Kilo · OpenCode") {
		t.Fatalf("the model group is missing: %s", view)
	}
	m = update(m, press(tea.KeyDown))
	m = update(m, modelsLoaded{models: m.availableModels})
	if m.models.list.SelectedItem().(modelItem).ID != picked {
		t.Fatal("model refresh moved the cursor away from the highlighted model")
	}
	m = update(m, press(tea.KeyEnter))
	if m.selected != picked || m.modelsFocused || m.prompt.Value() != "Keep this draft" {
		t.Fatal("group selection was lost or changed the draft")
	}
	m.selected = old
	m.ensureSelected()
	if m.selected != picked {
		t.Fatal("a saved OpenCode route did not resolve to the model group")
	}
}

func TestProviderManagementRefreshesItsRowIndependentlyOfModels(t *testing.T) {
	f := &scopedProviderRefreshFixture{}
	statuses, _ := f.ProviderStatuses(t.Context())
	m := update(NewConnected(t.Context(), f).WithProviderStatuses(statuses), tea.WindowSizeMsg{Width: 80, Height: 24})
	m = update(m, press(tea.KeyF4))
	m = update(m, press(tea.KeyDown))
	m.prompt.SetValue("Unsent draft")
	next, run := m.Update(providerSetupFinished{id: "groq"})
	m = next.(Model)
	batch, ok := run().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatal("account return must start independent provider and model commands")
	}
	m = update(m, batch[0]())
	if f.checked != "groq" || f.refresh || !m.loadingModels || m.providersLoading || m.providers["groq"].State != "ready" ||
		m.providers["kilo"].State != "ready" || m.models.list.SelectedItem().(modelItem).ID != "groq" || m.prompt.Value() != "Unsent draft" {
		t.Fatal("scoped provider refresh failed to preserve unrelated rows, selection, draft, or model loading state")
	}
	m = update(m, modelsLoaded{models: []client.Model{{ID: "old-result"}}})
	if len(m.availableModels) != 0 || !m.loadingModels {
		t.Fatal("model discovery started before account management was accepted")
	}
	m = update(m, batch[1]())
	if !f.refresh || m.loadingModels || len(m.availableModels) == 0 {
		t.Fatal("the forced model discovery did not complete independently")
	}
}

func TestModelPickerLoadsDuringStartupAndOpensFromProviders(t *testing.T) {
	m := update(NewConnected(t.Context(), providerGatewayFixture{}), tea.WindowSizeMsg{Width: 80, Height: 24})
	m = update(m, press(tea.KeyF2))
	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, "preview/") || !strings.Contains(view, "Loading models…") {
		t.Fatalf("startup model picker should show loading: %s", view)
	}
	m.startupDone = true
	m = update(m, tickMsg{})
	if !m.modelsStarted {
		t.Fatal("open picker prevented startup model loading")
	}
	m = update(m, m.loadModels()())
	next, cmd := m.Update(press(tea.KeyF4))
	m = update(next.(Model), cmd())
	m = update(m, press(tea.KeyF2))
	if m.menu != "models" || len(m.models.list.Items()) != 1 || m.models.list.Items()[0].(modelItem).ID != "free/auto" {
		t.Fatal("F2 on the provider screen did not open the available models")
	}
	m = update(m, modelsLoaded{err: &client.GatewayError{Code: "no_free_models", Status: 503}})
	if len(m.models.list.Items()) != 0 || !strings.Contains(ansi.Strip(m.View().Content), "No models available") {
		t.Fatal("an empty ready catalog left stale selectable models")
	}
}

func TestModelPickerSwitchesRanksWithoutChangingCursorOrDraft(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 44, Height: 12})
	m.availableModels = []client.Model{
		{ID: "free/auto", Name: "Auto"},
		{ID: "general-first", Name: "General leader", QualityRank: 1, CodingRank: 2, ReasoningRank: 1, ReadyProviders: []string{"Kilo", "OpenRouter"}},
		{ID: "unmeasured", Name: "No evidence"},
		{ID: "coding-first", Name: "Coding leader", QualityRank: 2, CodingRank: 1, ReasoningRank: 2},
	}
	m.prompt.SetValue("Unsent draft")
	m = update(m, press(tea.KeyF2))
	m = update(m, press(tea.KeyDown))
	m = update(m, ctrl('t'))
	items := m.models.list.Items()
	if items[0].(modelItem).ID != "free/auto" || items[1].(modelItem).ID != "coding-first" || items[2].(modelItem).ID != "unmeasured" ||
		m.models.list.SelectedItem().(modelItem).ID != "general-first" || m.prompt.Value() != "Unsent draft" {
		t.Fatal("coding view must reorder benchmarked slots while keeping the cursor and draft")
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Ctrl+T rank") || !strings.Contains(view, "Terminal-Bench 4.0") || !strings.Contains(view, "Kilo · OpenRouter") || lipgloss.Height(view) > 12 {
		t.Fatalf("narrow picker hid the benchmark or keyboard controls:\n%s", view)
	}
	m = update(m, ctrl('t'))
	if m.models.list.Items()[1].(modelItem).ID != "general-first" || !strings.Contains(ansi.Strip(m.View().Content), "HLE-Diamond") {
		t.Fatal("the reasoning benchmark was not displayed")
	}
}

func TestNarrowProviderListKeepsConnectionActionVisible(t *testing.T) {
	m := NewConnected(t.Context(), providerGatewayFixture{}).WithPreferences(Preferences{Language: "ko", Theme: "system"})
	m = update(m, tea.WindowSizeMsg{Width: 48, Height: 22})
	next, cmd := m.Update(press(tea.KeyF4))
	m = update(next.(Model), cmd())
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "CLI 설치 필요") || !strings.Contains(view, "API 키 필요") {
		t.Fatalf("narrow provider list clipped the required actions: %s", view)
	}
}

func TestExplicitImagePreservesDraftWhileResponding(t *testing.T) {
	m := update(NewConnected(t.Context(), fixtureGateway{}), tea.WindowSizeMsg{Width: 80, Height: 24})
	const draft = "/image make a cat logo"
	m.busy, m.generation = true, 42
	m.prompt.SetValue(draft)
	next, cmd := m.Update(press(tea.KeyEnter))
	m = next.(Model)
	if cmd != nil || !m.busy || m.generation != 42 || len(m.messages) != 0 || m.prompt.Value() != draft {
		t.Fatal("busy image submission started another response or changed the draft")
	}
}

func TestRenameWithAnImagePhraseKeepsCommandBehavior(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	m.selected = "free/auto"
	const title = "우주복 입은 강아지 그려줘"
	m.prompt.SetValue("/rename " + title)
	next, _ := m.submit()
	m = next.(Model)
	if m.session.Title != title || len(m.messages) != 0 || m.prompt.Value() != "" {
		t.Fatal("image words in a rename command must only update the conversation title")
	}
}

func TestModelMenuRefreshPreservesHighlightedChoice(t *testing.T) {
	m := update(NewConnected(t.Context(), fixtureGateway{}), tea.WindowSizeMsg{Width: 80, Height: 24})
	models := []client.Model{{ID: "free/auto", Name: "Auto"}, {ID: "kilo/one", Name: "One"}, {ID: "kilo/two", Name: "Two"}}
	m = update(m, modelsLoaded{models: models})
	m = update(m, press(tea.KeyF2))
	m = update(m, press(tea.KeyDown))
	m = update(m, modelsLoaded{models: []client.Model{models[0], models[2], models[1]}})
	if item := m.models.list.SelectedItem().(modelItem); item.ID != "kilo/one" || m.selected != "free/auto" {
		t.Fatal("a catalog refresh changed the highlighted choice or current model")
	}
	if !strings.Contains(m.View().Content, "✓ Current") {
		t.Fatal("the current model was not marked")
	}
}

func TestMarkdownWrapsListsAndQuotesWithoutBreakingWords(t *testing.T) {
	rendered, _ := markdown("- Keep the words together\n> Longer quoted text\n`**literal**`\n```go\n    call()\n```", 15)
	result := ansi.Strip(rendered)
	for _, want := range []string{"• Keep the\n  words", "│ Longer quoted\n│ text", "**literal**", "    call()"} {
		if !strings.Contains(result, want) {
			t.Fatalf("missing %q in %q", want, result)
		}
	}
}

func TestProviderStatusMenuPreservesDraft(t *testing.T) {
	m := update(NewConnected(t.Context(), providerGatewayFixture{}), tea.WindowSizeMsg{Width: 80, Height: 24})
	m.prompt.SetValue("Keep this draft")
	next, cmd := m.Update(press(tea.KeyF4))
	m = update(next.(Model), cmd())
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Connected · Guest · 2 free models") || !strings.Contains(view, "Key required · Disconnected") || !strings.Contains(view, "CLI required · Disconnected") || !strings.Contains(view, "Connection unchecked · Key saved") {
		t.Fatalf("provider status missing from view: %s", view)
	}
	m = update(m, press(tea.KeyDown))
	if !strings.Contains(ansi.Strip(m.View().Content), "freeway providers auth groq") {
		t.Fatal("missing key did not show the provider auth command")
	}
	next, cmd = m.showProviders()
	m = update(next.(Model), cmd())
	if m.models.list.SelectedItem().(modelItem).ID != "groq" {
		t.Fatal("refresh moved away from the provider being checked")
	}
	m = update(m, press(tea.KeyEsc))
	if m.modelsFocused || m.prompt.Value() != "Keep this draft" || !m.prompt.Focused() {
		t.Fatal("provider status changed the current draft")
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "Connected · Kilo · F4") {
		t.Fatal("the chat header did not show the connected provider")
	}
}
func TestJumpToLatestButton(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {44, 12}} {
		m := update(New(), tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.messages = append(m.messages, newMessage("assistant", strings.Repeat("Earlier reply\n\n", 35)+"Latest reply"))
		m.prompt.SetValue("Draft")
		m.busy = true
		m.renderMessages()
		m.chat.GotoBottom()
		if strings.Contains(m.View().Content, "Jump to latest") {
			t.Fatal("jump button appeared at the bottom")
		}
		m = update(m, press(tea.KeyPgUp))
		view := m.View().Content
		if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
			t.Fatalf("jump button overflowed at %v", size)
		}
		x, y := m.latestButtonPosition()
		line := ansi.Strip(strings.Split(view, "\n")[y])
		start := strings.Index(line, latestButtonLabel)
		if start < 0 || ansi.StringWidth(line[:start]) != x {
			t.Fatalf("jump button does not align with its hit area: %q", line)
		}
		m = update(m, tea.MouseClickMsg{X: x - 1, Y: y, Button: tea.MouseLeft})
		if m.chat.AtBottom() {
			t.Fatal("clicking outside the button jumped to the bottom")
		}
		m = update(m, tea.MouseClickMsg{X: x + 2, Y: y, Button: tea.MouseLeft})
		if !m.chat.AtBottom() || m.prompt.Value() != "Draft" || strings.Contains(m.View().Content, "Jump to latest") {
			t.Fatal("click did not jump to the bottom and preserve the draft")
		}
	}
}

func TestTranscriptSearchKeepsDraftAndRestoresOrJumps(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	m.messages = append(m.messages, newMessage("assistant", "Old answer: Result [one]"))
	for range 12 {
		m.messages = append(m.messages, newMessage("user", "More context"))
	}
	m.messages = append(m.messages, newMessage("assistant", "Recent answer: RESULT [ONE]"))
	m.prompt.SetValue("Unsent 🌏")
	m.renderMessages()
	m.chat.GotoBottom()
	bottom := m.chat.YOffset()

	openSearch := func(m Model) Model {
		m = update(m, press(tea.KeyF3))
		m = update(m, tea.PasteMsg{Content: "result [one]"})
		if m.find == nil || len(m.find.matches) != 2 || m.View().Cursor == nil {
			t.Fatalf("F3 did not find both literal matches or show the cursor: %s", m.View().Content)
		}
		return update(m, press(tea.KeyDown))
	}
	m = openSearch(m)
	if m.chat.YOffset() >= bottom || !strings.Contains(m.View().Content, "Old answer:") {
		t.Fatal("next result did not wrap back to the old answer")
	}
	m = update(m, press(tea.KeyEsc))
	if m.find != nil || m.chat.YOffset() != bottom || m.prompt.Value() != "Unsent 🌏" || !m.prompt.Focused() {
		t.Fatal("Escape did not restore the original position and composer")
	}
	m = openSearch(m)
	position := m.chat.YOffset()
	m = update(m, press(tea.KeyEnter))
	if m.find != nil || m.chat.YOffset() != position || m.prompt.Value() != "Unsent 🌏" || !m.prompt.Focused() {
		t.Fatal("Enter did not keep the result position and composer")
	}
}

func TestTranscriptSearchFindsSoftWrapInTextAndCode(t *testing.T) {
	const query = "한글 needle boundary"
	for _, tc := range []struct {
		role, before, after string
		padding             int
	}{
		{role: "user", padding: 29},
		{role: "assistant", padding: 33},
		{role: "assistant", before: "# ", padding: 33},
		{role: "assistant", before: "```txt\n", after: "\n```", padding: 29},
	} {
		m := update(New(), tea.WindowSizeMsg{Width: 44, Height: 12})
		padding := strings.Repeat("x", tc.padding)
		m.messages = []client.Message{
			newMessage(tc.role, tc.before+padding+query+tc.after),
			newMessage(tc.role, tc.before+padding+"한글 needle\nboundary"+tc.after),
		}
		m.renderMessages()
		expected := ansi.Strip(m.chat.GetContent())
		m.chat.GotoBottom()
		originalOffset := m.chat.YOffset()
		next, _ := m.findTranscript("한글 NEEDLE boundary")
		m = next.(Model)
		if len(m.find.matches) != 1 || m.find.matches[0].row >= m.find.matches[0].lastRow {
			t.Fatalf("expected one match across a soft wrap for %q; got %+v", tc.role+tc.before, m.find.matches)
		}
		if ansi.Strip(m.chat.GetContent()) != expected || !strings.Contains(m.View().Content, "1/1") || lipgloss.Height(m.View().Content) > 12 {
			t.Fatalf("highlight changed transcript content or hid the search hint for %q", tc.role+tc.before)
		}
		m = update(m, press(tea.KeyEsc))
		if m.find != nil || m.chat.YOffset() != originalOffset || ansi.Strip(m.chat.GetContent()) != expected {
			t.Fatal("cancelling a wrapped match must restore the view")
		}
	}
}

func TestFilePickerKeepsTypedPathAndFitsWindow(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {44, 12}} {
		m := update(New(), tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.messages = append(m.messages, newMessage("assistant", "Previous answer"))
		m.renderMessages()
		m.fileItems = []list.Item{modelItem{ID: "docs/index.md", Name: "docs/index.md"}, modelItem{ID: "lib/3main.go", Name: "lib/3main.go"}}
		m.prompt.SetValue("Read ")
		m = update(m, tea.KeyPressMsg{Code: '@', Text: "@"})
		m = update(m, tea.KeyPressMsg{Code: '3', Text: "3"})
		if len(m.models.list.Items()) != 1 || m.menuQuery != "3" || !m.modelsFocused {
			t.Fatal("a leading number should filter the file list")
		}
		m = update(m, ctrl('u'))
		m = update(m, tea.PasteMsg{Content: `lib\3ma`})
		if item, ok := m.models.list.SelectedItem().(modelItem); !ok || item.ID != "lib/3main.go" {
			t.Fatal("file search did not accept a Windows path")
		}
		view := m.View().Content
		if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
			t.Fatalf("file picker overflowed at %v: %dx%d", size, lipgloss.Width(view), lipgloss.Height(view))
		}
		if size[1] >= 18 && !strings.Contains(view, "Previous answer") {
			t.Fatal("inline picker hid the previous answer")
		}
		m = update(m, press(tea.KeyEsc))
		if m.modelsFocused || m.prompt.Value() != `Read @lib\3ma` || len(m.files) != 0 {
			t.Fatalf("Escape changed the draft or attached a file: %q", m.prompt.Value())
		}
	}
}

func TestPromptHistoryShowsPreviewAndStashesDraft(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	m.remember("First prompt")
	m.remember("2026 status\nSecond line")
	m.prompt.SetValue("Current draft")
	m = update(m, ctrl('r'))
	m = update(m, tea.PasteMsg{Content: "2026"})
	view := m.View().Content
	if !strings.Contains(view, "Prompt preview") || !strings.Contains(view, "Second line") || lipgloss.Height(view) > 24 {
		t.Fatal("prompt preview is missing or overflows")
	}
	m = update(m, press(tea.KeyEnter))
	if m.prompt.Value() != "2026 status\nSecond line" || m.stash == nil || m.stash.Text != "Current draft" {
		t.Fatal("loading a prompt did not stash the existing draft")
	}
	m = update(m, ctrl('s'))
	if m.prompt.Value() != "Current draft" {
		t.Fatal("the draft could not be recovered")
	}
}

func TestConversationPickerPreviewsAndSearchesLastSavedQuestion(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 72, Height: 24})
	m.history = []client.ConversationSummary{
		{ID: "first", Title: "Working session", Preview: "Compare two colors"},
		{ID: "second", Title: "Working session", Preview: "Recover the previous draft"},
	}
	next, _ := m.openMenu("history")
	m = update(next.(Model), press(tea.KeyDown))
	view := ansi.Strip(m.View().Content)
	_, preview, found := strings.Cut(view, "Last saved question")
	if !found || !strings.HasPrefix(strings.TrimSpace(preview), "Recover the previous draft") {
		t.Fatalf("the selected conversation preview is missing:\n%s", view)
	}
	m = update(m, tea.PasteMsg{Content: "two colors"})
	if row, ok := m.models.list.SelectedItem().(modelItem); !ok || row.ID != "first" {
		t.Fatal("search must match the last saved question even when session titles are identical")
	}
}

func TestProviderRowsRemainVisibleWhileRefreshing(t *testing.T) {
	gateway := &scopedProviderRefreshFixture{}
	m := NewConnected(t.Context(), gateway).WithProviderSetup("freeway").
		WithProviderStatuses([]client.ProviderStatus{{ID: "kilo", Name: "Kilo", State: "ready", FreeModels: 2}})
	m = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	next, cmd := m.Update(press(tea.KeyF4))
	m = next.(Model)
	if row, ok := m.models.list.SelectedItem().(modelItem); !ok || row.ID != "kilo" {
		t.Fatal("the cached provider must be selectable before the refresh response")
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Kilo") || !strings.Contains(view, "Checking provider connections") {
		t.Fatalf("provider rows and refresh indication must both be visible: %s", view)
	}
	msg := cmd().(providerStatusesLoaded)
	if msg.provider != "" || gateway.checked != "" || len(m.providerChecksPending) != 0 {
		t.Fatal("a gateway without scoped capability should receive one bulk check")
	}
}

type progressiveProviderFixture struct {
	providerGatewayFixture
	modelsRefreshed bool
}

func (g *progressiveProviderFixture) RefreshModels(ctx context.Context) ([]client.Model, error) {
	g.modelsRefreshed = true
	return g.Models(ctx)
}

func (progressiveProviderFixture) ProviderStatus(_ context.Context, id string) (client.ProviderStatus, error) {
	if id == "kilo" {
		return client.ProviderStatus{ID: id, Name: "Kilo", State: "ready", FreeModels: 14, CatalogChecked: true, ScopedCheck: true, Management: true}, nil
	}
	return client.ProviderStatus{}, &client.GatewayError{Code: "provider_status_unavailable", Status: 503}
}

func TestProviderRowsRefreshIndependentlyInManagedGateway(t *testing.T) {
	gateway := &progressiveProviderFixture{}
	m := connectedFixture(gateway).WithProviderSetup("freeway").
		WithProviderStatuses([]client.ProviderStatus{
			{ID: "kilo", Name: "Kilo", State: "ready", FreeModels: 2, ScopedCheck: true},
			{ID: "opencode", Name: "OpenCode", State: "ready", FreeModels: 10, ScopedCheck: true},
		})
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m.prompt.SetValue("Keep the draft")
	next, cmd := m.Update(press(tea.KeyF4))
	m = next.(Model)
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || !m.providersLoading || len(m.providerChecksPending) != 2 {
		t.Fatal("model did not start independent checks for the known provider rows")
	}
	results := map[string]providerStatusesLoaded{}
	for _, check := range batch {
		if msg, ok := check().(providerStatusesLoaded); ok {
			results[msg.provider] = msg
		}
	}
	if len(results) != 2 {
		t.Fatalf("expected both scoped requests: %+v", results)
	}
	m = update(m, results["kilo"])
	view := ansi.Strip(m.View().Content)
	if !m.providersLoading || !m.providerChecksPending["opencode"] || !strings.Contains(view, "14 free models") || !strings.Contains(view, "Checking…") || strings.Contains(m.providerSummary(), "OpenCode") {
		t.Fatalf("the completed row must update while the slow row is pending:\n%s", view)
	}
	request := m.providersRequest
	next, cmd = m.Update(ctrl('r'))
	m = next.(Model)
	if m.providersRequest != request || cmd != nil {
		t.Fatal("refresh restarted an active provider batch")
	}
	m = update(m, press(tea.KeyEsc))
	m = update(m, results["opencode"])
	if m.modelsFocused || m.providersLoading || m.prompt.Value() != "Keep the draft" || m.providers["kilo"].FreeModels != 14 || m.providers["opencode"].State != "status_check_failed" {
		t.Fatal("one failed provider or closing the view discarded remaining results or the draft")
	}
	next, cmd = m.Update(press(tea.KeyF2))
	m = update(next.(Model), cmd())
	if !gateway.modelsRefreshed || m.modelListNeedsRefresh || m.prompt.Value() != "Keep the draft" {
		t.Fatal("F2 did not refresh the model catalog after an observed provider change")
	}
}

func TestAutoWithoutTextRouteKeepsDraftBeforeSending(t *testing.T) {
	m := connectedFixture(&burstGateway{})
	m = update(m, modelsLoaded{models: []client.Model{{ID: "opencode/test", Name: "OpenCode"}}})
	m.prompt.SetValue("Unsent question")
	next, cmd := m.Update(press(tea.KeyEnter))
	m = next.(Model)
	if cmd != nil || m.prompt.Value() != "Unsent question" || len(m.messages) != 0 || !strings.Contains(m.toast, "Auto has no text route") {
		t.Fatal("text submission must stop at the composer when Auto has no text route")
	}
}

type modelRecoveryGateway struct {
	burstGateway
	refreshed bool
}

func (g *modelRecoveryGateway) RefreshModels(context.Context) ([]client.Model, error) {
	g.refreshed = true
	return []client.Model{
		{ID: "free/auto", Name: "Auto", Ready: true, Free: true},
		{ID: "model/deepseek/deepseek-v4.1-flash", Name: "DeepSeek V4.1 Flash", Ready: true, Free: true, ReadyProviders: []string{"OpenCode"}},
	}, nil
}

func TestUnavailableModelKeepsSelectionAndDraftUntilRefresh(t *testing.T) {
	const selected = "model/deepseek/deepseek-v4.1-flash"
	g := &modelRecoveryGateway{}
	m := connectedFixture(g)
	m.selected = selected
	m = update(m, modelsLoaded{models: []client.Model{{ID: "free/auto", Name: "Auto", Ready: true, Free: true}}})
	m.messages = []client.Message{newMessage("user", "Previous question"), newMessage("assistant", "Previous answer")}
	m.prompt.SetValue("Unsent follow-up")
	m.files = []client.Attachment{{Path: "context.txt", Content: "keep me"}}
	m.saveError = "out of space"
	if status := m.statusLabel(); !strings.Contains(status, "Selected model unavailable") || !strings.Contains(status, "Save failed") {
		t.Fatalf("outage status hid the draft save failure: %q", status)
	}
	m = update(m, tea.WindowSizeMsg{Width: 44, Height: 12})
	if !strings.Contains(ansi.Strip(m.View().Content), "/save to retry") {
		t.Fatal("a narrow terminal cut off the save recovery action")
	}

	next, cmd := m.submit()
	m = next.(Model)
	if cmd != nil || m.selected != selected || m.prompt.Value() != "Unsent follow-up" || len(m.files) != 1 || len(m.messages) != 2 || !strings.Contains(m.toast, "Selected model unavailable") {
		t.Fatalf("unavailable model did not preserve the draft: %q %q %q", m.selected, m.prompt.Value(), m.toast)
	}
	next, cmd = m.retry()
	m = next.(Model)
	if cmd != nil || len(m.messages) != 2 || m.messages[1].Content != "Previous answer" {
		t.Fatal("retry dropped the existing answer before checking the selected model")
	}
	m.queue = []client.PendingPrompt{{Text: "Saved follow-up"}}
	next, cmd = m.startQueued()
	m = next.(Model)
	if cmd != nil || !m.queuePaused || len(m.queue) != 1 || m.queue[0].Text != "Saved follow-up" || len(m.messages) != 2 || !strings.Contains(m.toast, "/queue resume") {
		t.Fatal("unavailable model did not pause the queue with its follow-up intact")
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "/queue resume") {
		t.Fatal("a narrow terminal cut off the action to resume the queue")
	}

	m = update(m, press(tea.KeyF2))
	m = update(m, modelsLoaded{models: m.availableModels, epoch: m.modelsEpoch})
	next, cmd = m.Update(ctrl('r'))
	m = next.(Model)
	if cmd == nil || !m.loadingModels || !strings.Contains(ansi.Strip(m.View().Content), "Checking models") {
		t.Fatal("Ctrl+R did not show a model refresh")
	}
	m = update(m, cmd())
	if !g.refreshed || m.selected != selected || !m.modelsFocused || m.prompt.Value() != "Unsent follow-up" || len(m.files) != 1 || !m.queuePaused {
		t.Fatal("refresh changed the selected model, draft, or paused queue")
	}
	m = update(m, press(tea.KeyEsc))
	next, cmd = m.submit()
	m, _ = drainResponse(t, next.(Model), cmd)
	if len(g.calls) != 1 || g.calls[0] != selected || len(g.requests[0][2].Files) != 1 || len(m.queue) != 1 {
		t.Fatalf("recovered model did not receive exactly the preserved draft: %v", g.calls)
	}
}

func TestCatalogOutageDistinguishesListedModelsFromLegacyIDs(t *testing.T) {
	const listed = "vikasit/vikasit-nova"
	m := connectedFixture(&burstGateway{})
	m = update(m, modelsLoaded{models: []client.Model{{ID: listed, Ready: true, Free: true}}})
	next, _ := m.execute("/model", listed)
	m = next.(Model)
	m = update(m, modelsLoaded{models: []client.Model{{ID: "free/auto", Ready: true, Free: true}}})
	if m.textRouteIssue() == "" {
		t.Fatal("the provider-only picker choice disappeared without an outage message")
	}

	m.selected = "opencode/deepseek-v4.1-flash-free"
	if m.textRouteIssue() != "" {
		t.Fatal("the client blocked an older provider ID before the gateway could resolve it")
	}
}

func TestFindMatchesWordsSplitByWordWrap(t *testing.T) {
	// Each padding moves the word-wrap break, including onto the removed space.
	for padding := 20; padding <= 45; padding++ {
		m := update(New(), tea.WindowSizeMsg{Width: 44, Height: 12})
		m.messages = []client.Message{newMessage("assistant", strings.Repeat("x", padding)+" needle across rows")}
		m.renderMessages()
		next, _ := m.findTranscript("needle across rows")
		m = next.(Model)
		if len(m.find.matches) != 1 {
			t.Fatalf("padding %d: expected one match, got %+v in %q", padding, m.find.matches, ansi.Strip(m.chat.GetContent()))
		}
	}
}

func TestEscFromSettingsSubmenuReturnsToSettings(t *testing.T) {
	m := update(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	next, _ := m.openMenu("settings")
	m = next.(Model)
	next, _ = m.chooseItem(modelItem{ID: "language"})
	m = update(next.(Model), press(tea.KeyEsc))
	if !m.modelsFocused || m.menu != "settings" {
		t.Fatalf("Esc from the language list left settings: focused=%v menu=%q", m.modelsFocused, m.menu)
	}
	m = update(m, press(tea.KeyEsc))
	m = update(m, press(tea.KeyF2))
	m = update(m, press(tea.KeyEsc))
	if m.modelsFocused {
		t.Fatal("a stale parent reopened settings from the model list")
	}
}
