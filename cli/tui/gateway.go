package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/client"
	"github.com/wooinwoo/freeway-cli/cli/imagegen"
)

type gateway interface {
	Models(context.Context) ([]client.Model, error)
	Stream(context.Context, string, []client.Message, func(client.Event)) error
}

type modelsLoaded struct {
	models []client.Model
	err    error
	epoch  uint64
}

type responseEvent struct {
	event      client.Event
	generation uint64
}

type responseTarget struct {
	model    string
	settings client.Settings
	context  client.ContextSelection
}

// NewConnected uses the gateway interface without owning routing or persistent storage.
func NewConnected(ctx context.Context, backend gateway) Model {
	m := New()
	m.gateway, m.ctx = backend, ctx
	m.loadingModels = true
	m.selected, m.status = "free/auto", "Connecting"
	m.availableModels = nil
	m.models.list.SetItems(nil)
	if _, ok := backend.(conversationGateway); ok {
		m.loadingHistory = true
	}
	return m
}

func (m Model) loadModels() tea.Cmd {
	return m.modelLoad(false, false)
}

// modelLoad can refresh the provider catalog. Reconnect also replaces a supervised gateway handle first.
func (m Model) modelLoad(refresh, reconnect bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 45*time.Second)
		defer cancel()
		if gateway, ok := m.gateway.(interface{ Reconnect(context.Context) error }); reconnect && ok {
			if err := gateway.Reconnect(ctx); err != nil {
				return modelsLoaded{err: err, epoch: m.modelsEpoch}
			}
		}
		load := m.gateway.Models
		if refresher, ok := m.gateway.(interface {
			RefreshModels(context.Context) ([]client.Model, error)
		}); refresh && ok {
			load = refresher.RefreshModels
		}
		models, err := load(ctx)
		return modelsLoaded{models: models, err: err, epoch: m.modelsEpoch}
	}
}

func (m Model) textRouteIssue() string {
	if m.gateway == nil {
		return ""
	}
	if !m.ready {
		return m.tr("Check models with F2, Ctrl+R.", "F2, Ctrl+R로 모델을 다시 확인하세요.")
	}
	knownSelection := m.listedSelection != "" && m.selected == m.listedSelection
	if m.selected != "free/auto" && !knownSelection && !strings.HasPrefix(m.selected, "model/") {
		// A legacy provider ID can resolve to another route on the gateway.
		return ""
	}
	if slices.ContainsFunc(m.availableModels, func(model client.Model) bool { return model.ID == m.selected }) {
		return ""
	}
	if m.selected == "free/auto" {
		return m.tr("Auto has no text route · F2 models · F9 images", "자동 텍스트 경로 없음 · F2 모델 · F9 이미지")
	}
	return m.tr("Selected model unavailable · F2, Ctrl+R", "선택한 모델 사용 불가 · F2, Ctrl+R")
}

func (m Model) receiveModels(msg modelsLoaded) (tea.Model, tea.Cmd) {
	if msg.epoch != m.modelsEpoch {
		return m, nil
	}
	status, notice := m.status, m.notice
	m.modelsStarted = true
	m.loadingModels = false
	var retry tea.Cmd
	if msg.err != nil {
		m.status, m.notice = "Connection needs attention", friendlyError(msg.err, m.prefs)
		m.ready = false
		var failure *client.GatewayError
		if errors.As(msg.err, &failure) && failure.Code == "no_free_models" {
			m.availableModels = nil
			if _, err := imagegen.Load(); err == nil {
				m.status, m.notice = "Image tool configured · F9", ""
			}
		}
		if m.reconnectAttempts < 2 {
			m.reconnectAttempts++
			retry = tea.Tick(time.Duration(m.reconnectAttempts*2)*time.Second, func(time.Time) tea.Msg { return reconnectMsg{} })
		}
	} else {
		m.availableModels = append([]client.Model(nil), msg.models...)
		if !m.busy {
			// A running reply keeps its target; F5 compares that target with the selection.
			m.ensureSelected()
		}
		m.ready = len(msg.models) > 0
		m.status, m.notice = "Ready", ""
		m.reconnectAttempts = 0
		m.layout()
	}
	var picker tea.Cmd
	if m.modelsFocused && m.menu == "models" {
		selected, ok := m.models.list.SelectedItem().(modelItem)
		m.menuAll = m.modelPickerItems()
		picker = m.applyMenuFilter()
		if ok {
			m.selectMenuItem(selected.ID)
		} else {
			m.selectMenuItem(m.selected)
		}
	} else if !m.modelsFocused {
		picker = m.models.list.SetItems(m.modelPickerItems())
	}
	if m.busy {
		m.status, m.notice = status, notice
	}
	m.renderMessages()
	return m, tea.Batch(picker, retry)
}

func (m Model) startResponse() (tea.Model, tea.Cmd) {
	return m.startResponseWithSettings(m.settings)
}

func (m Model) startResponseWithSettings(settings client.Settings) (tea.Model, tea.Cmd) {
	m.responseTarget = responseTarget{model: m.selected, settings: settings, context: m.contextSelection()}
	m.shorterMax = 0
	m.responseElapsed, m.lastResponseTokens = 0, 0
	m.imagePrompt = ""
	if len(m.messages) > 0 && m.messages[len(m.messages)-1].Role == "user" {
		m.imagePrompt, _ = imagegen.Prompt(m.messages[len(m.messages)-1].Content, m.responseTarget.model == "free/auto")
	}
	assistant := newMessage("assistant", "")
	assistant.Status = "streaming"
	m.messages = append(m.messages, assistant)
	m.busy, m.status, m.notice = true, "Preparing response", ""
	m.startedAt = time.Now()
	m.dirty = true
	m.renderMessages()
	m.chat.GotoBottom()
	if cmd := m.persist(); cmd != nil {
		m.afterSave = "generate"
		return m, cmd
	}
	if m.saving {
		m.afterSave = "generate"
		return m, nil
	}
	return m.beginGeneration()
}

func (m Model) beginGeneration() (tea.Model, tea.Cmd) {
	if m.imagePrompt != "" && len(m.messages) >= 2 {
		return m.beginImage(m.imagePrompt, m.messages[len(m.messages)-2])
	}
	if len(m.messages) >= 2 && m.messages[len(m.messages)-2].Role == "user" {
		if question, ok := mcpQuestion(m.messages[len(m.messages)-2].Content); ok {
			return m.beginMCPPlan(question)
		}
	}
	return m.beginTextStream(m.responseTarget.model, m.responseContext().messages(), "")
}

func (m Model) beginTextStream(model string, request []client.Message, prefix string) (tea.Model, tea.Cmd) {
	m.generation++
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel = cancel
	events := make(chan client.Event, 16)
	backend, settings, prefs := m.gateway, m.responseTarget.settings, m.prefs
	mcpRunning := m.mcpRunning
	go func() {
		defer close(events)
		// The gateway ends stalled routes after 90 seconds of silence and caps a reply at 15 minutes.
		// This outer limit only covers a gateway that stops responding. It ends just the
		// request, so the error can still reach the screen.
		requestCtx, stopRequest := context.WithTimeout(ctx, 16*time.Minute)
		defer stopRequest()
		incoming := make(chan client.Event, 16)
		go func() {
			defer close(incoming)
			emit := func(event client.Event) {
				select {
				case incoming <- event:
				case <-ctx.Done():
				}
			}
			if prefix != "" {
				emit(client.Event{Type: "text", Text: prefix})
			}
			callback := func(event client.Event) {
				switch event.Type {
				case "error": // The returned error is displayed after pending text.
				case "done":
					event.Text = prefix + event.Text
					emit(event)
				default:
					emit(event)
				}
			}
			var err error
			if configured, ok := backend.(interface {
				StreamWithSettings(context.Context, string, []client.Message, client.Settings, func(client.Event)) error
			}); ok {
				err = configured.StreamWithSettings(requestCtx, model, request, settings, callback)
			} else {
				err = backend.Stream(requestCtx, model, request, callback)
			}
			if err != nil {
				event := client.Event{Type: "error", Error: friendlyError(err, prefs)}
				if mcpRunning {
					event.Error = prefs.Text("The MCP tool ran, but the model could not finish. Check the tool before trying again.", "MCP 도구는 실행됐지만 모델 응답이 중단됐습니다. 다시 시도하기 전에 도구를 확인하세요.")
				}
				var problem *client.GatewayError
				if errors.As(err, &problem) && problem.Code == "local_token_budget" {
					event.MaxOutputTokens = problem.MaxOutputTokens
				}
				emit(event)
			}
		}()
		coalesceResponse(ctx, incoming, events)
	}()
	m.events = events
	m.renderMessages()
	m.chat.GotoBottom()
	return m, waitResponse(events, m.generation)
}

func includesModelContext(message client.Message) bool {
	return message.Role == "user" || message.Role == "assistant" && (message.Status == "" || message.Status == "done") && message.Content != ""
}

// coalesceResponse sends the first text at once, then batches text for about one frame.
func coalesceResponse(ctx context.Context, incoming <-chan client.Event, events chan<- client.Event) {
	emit := func(event client.Event) {
		select {
		case events <- event:
		case <-ctx.Done():
		}
	}
	var buffer strings.Builder
	var pending client.Event
	var deadline <-chan time.Time
	timer := time.NewTimer(33 * time.Millisecond)
	timer.Stop()
	defer timer.Stop()
	flush := func() {
		timer.Stop()
		deadline = nil
		if buffer.Len() > 0 {
			pending.Text = buffer.String()
			emit(pending)
			buffer.Reset()
		}
	}
	first := true
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			flush()
		case event, ok := <-incoming:
			if !ok {
				flush()
				return
			}
			if event.Type != "text" {
				flush()
				emit(event)
				continue
			}
			if event.Text == "" {
				continue
			}
			if first {
				first = false
				emit(event)
				continue
			}
			pending.Type = "text"
			if event.Model != "" {
				pending.Model, pending.Provider = event.Model, event.Provider
			}
			buffer.WriteString(event.Text)
			if deadline == nil {
				timer.Reset(33 * time.Millisecond)
				deadline = timer.C
			}
		}
	}
}

func waitResponse(events <-chan client.Event, generation uint64) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			event = client.Event{Type: "error", Error: "Response ended before completion."}
		}
		return responseEvent{event: event, generation: generation}
	}
}

func (m Model) receiveResponse(msg responseEvent) (tea.Model, tea.Cmd) {
	if !m.busy || msg.generation != m.generation {
		return m, nil
	}
	follow := m.find == nil && m.chat.AtBottom()
	firstText := false
	switch msg.event.Type {
	case "text":
		if len(m.messages) == 0 || m.messages[len(m.messages)-1].Role != "assistant" {
			m.stopResponse()
			return m, nil
		}
		firstText = m.messages[len(m.messages)-1].Content == ""
		m.messages[len(m.messages)-1].Content += msg.event.Text
		if msg.event.Model != "" {
			m.messages[len(m.messages)-1].Model = msg.event.Model
		}
		m.dirty = true
		m.status = "Writing response"
		m.renderPending = true
	case "done":
		m.responseElapsed = time.Since(m.startedAt)
		if msg.event.Usage != nil {
			m.lastResponseTokens = msg.event.Usage.TotalTokens
		}
		m.messages[len(m.messages)-1].Content = msg.event.Text
		m.messages[len(m.messages)-1].Status = "done"
		if msg.event.Model != "" {
			m.messages[len(m.messages)-1].Model = msg.event.Model
		}
		if msg.event.Provider != "" {
			m.messages[len(m.messages)-1].Provider = msg.event.Provider
		}
		m.stopResponse()
		m.status = "Response complete"
		if msg.event.Provider != "" {
			m.status += " · " + providerLabel(terminalSafe(msg.event.Provider))
		}
		m.dirty = true
	case "error":
		mcpRunning := m.mcpRunning
		m.queuePaused = m.queuePaused || len(m.queue) > 0
		if len(m.messages) > 0 && m.messages[len(m.messages)-1].Role == "assistant" {
			m.messages[len(m.messages)-1].Status = "error"
			m.messages[len(m.messages)-1].Error = msg.event.Error
		}
		m.stopResponse()
		m.status, m.notice = "Ready to retry", msg.event.Error+"\n"+m.tr("Alt+R retry · /auto switch model · Your question and draft are safe.", "Alt+R 다시 시도 · /auto 모델 자동 선택 · 질문과 초안은 보존됩니다.")
		if msg.event.MaxOutputTokens != nil && *msg.event.MaxOutputTokens > 0 && m.selected == m.responseTarget.model {
			m.shorterMax = min(32768, *msg.event.MaxOutputTokens)
			m.notice = fmt.Sprintf(m.tr("%s\nF5 retry with up to %d output tokens · /settings", "%s\nF5 출력 토큰 %d개 이하로 다시 시도 · /settings"), msg.event.Error, m.shorterMax)
		}
		if mcpRunning {
			m.notice = msg.event.Error
		}
		if strings.HasPrefix(msg.event.Model, "mcp/") {
			hint := m.tr("Alt+R retry · F9 check connection", "Alt+R 다시 시도 · F9 연결 확인")
			if msg.event.Provider != "" {
				hint = msg.event.Provider + " · " + m.tr("F9 check connection", "F9 연결 확인")
			}
			m.notice = msg.event.Error + "\n" + hint
		}
		m.dirty = true
	}
	if firstText || msg.event.Type != "text" || time.Since(m.lastRender) >= 33*time.Millisecond {
		m.renderMessages()
	}
	if follow {
		m.chat.GotoBottom()
	}
	if m.busy {
		return m, waitResponse(m.events, m.generation)
	}
	return m, m.persist()
}

func (m *Model) stopResponse() {
	if m.busy {
		m.generation++
	}
	m.imagePrompt = ""
	m.mcpApproval = nil
	m.mcpRunning = false
	if m.afterSave == "generate" {
		m.afterSave = ""
	}
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.busy = false
	if len(m.messages) > 0 {
		last := &m.messages[len(m.messages)-1]
		if last.Role == "assistant" && last.Status == "streaming" {
			last.Status = "stopped"
		}
	}
	if len(m.messages) > 0 {
		last := m.messages[len(m.messages)-1]
		if last.Role == "assistant" && last.Content == "" {
			m.messages = m.messages[:len(m.messages)-1]
		}
	}
}

func friendlyError(err error, prefs Preferences) string {
	t := prefs.Text
	var gateway *client.GatewayError
	if errors.As(err, &gateway) {
		switch {
		case gateway.Code == "chat_conflict":
			return t("This conversation changed in another window.", "다른 창에서 이 대화를 수정했습니다.")
		case gateway.Code == "request_too_large":
			return t("This request exceeds the 8 MiB gateway limit. Use F8 to reduce the earlier context.", "요청이 게이트웨이 8 MiB 제한을 넘었습니다. F8로 이전 문맥을 줄이세요.")
		case gateway.Code == "chat_unreadable" || gateway.Code == "conversation_too_large" ||
			gateway.Code == "context_settings_unavailable" || gateway.Code == "context_settings_required" ||
			gateway.Code == "invalid_conversation" || gateway.Code == "save_uncertain" || gateway.Code == "save_key_reused":
			return terminalSafe(gateway.Message)
		case gateway.Code == "chat_storage_failed" || gateway.Code == "usage_storage_failed":
			return terminalSafe(gateway.Message)
		case gateway.Code == "gateway_busy":
			return t("All gateway response slots are in use. Press Alt+R when another response finishes.", "게이트웨이가 처리 중입니다. 다른 응답이 끝나면 Alt+R로 다시 시도하세요.")
		case gateway.Code == "provider_connection_required":
			return t("This provider needs a valid free account. Press F4 to check connections.", "이 공급처에 유효한 무료 계정이 필요합니다. F4로 연결을 확인하세요.")
		case gateway.Code == "model_unavailable":
			return t("This model is no longer available. Press F2 to choose a model.", "이 모델을 사용할 수 없습니다. F2로 모델을 선택하세요.")
		case gateway.Code == "upstream_timeout" || gateway.Code == "upstream_first_text_timeout":
			return t("The provider timed out. Press Alt+R to retry.", "공급처 응답 시간이 초과됐습니다. Alt+R로 다시 시도하세요.")
		case gateway.Code == "response_too_large":
			return t("The reply exceeded the gateway's size limit. Ask for a shorter reply or lower the /settings response limit.", "응답이 게이트웨이 크기 제한을 넘었습니다. 짧은 답변을 요청하거나 /settings에서 응답 길이를 줄이세요.")
		case gateway.Code == "local_token_budget":
			if gateway.MaxOutputTokens != nil && *gateway.MaxOutputTokens > 0 {
				return t("The current context fits with a shorter reply under the local token budget.", "더 짧은 응답으로 줄이면 로컬 토큰 예산 안에 들어갑니다.")
			}
			return t("The current context exceeds the local token budget. Use F8 to reduce it, or shorten the current draft.", "현재 문맥이 로컬 토큰 예산을 넘었습니다. F8로 문맥을 줄이거나 초안을 줄이세요.")
		case gateway.Code == "provider_busy":
			return t("This provider is unavailable now: its free servers are busy for everyone, not because of your usage. /auto moves to another provider. Retry shortly with Alt+R.", "지금 이 공급처는 쓸 수 없습니다. 무료 서버가 모두에게 붐비는 상태이고 내 사용량 때문이 아닙니다. /auto는 다른 공급처로 넘어갑니다. 잠시 후 Alt+R로 다시 시도하세요.")
		case gateway.Code == "provider_cooldown":
			wait := time.Duration(min(max(gateway.RetryAfter, 1), 86400)) * time.Second
			return fmt.Sprintf(t("This provider is unavailable for %s after it turned a request away. /auto moves to another provider.", "지금 이 공급처는 쓸 수 없습니다. 방금 요청을 거절해서 %s 뒤에 다시 씁니다. /auto는 다른 공급처로 넘어갑니다."), wait)
		case gateway.Status == 429 && gateway.RetryAfter > 0:
			wait := time.Duration(min(gateway.RetryAfter, 86400)) * time.Second
			return fmt.Sprintf(t("Free usage is temporarily limited. Try again in %s with Alt+R.", "무료 사용이 일시 제한됐습니다. %s 후 Alt+R로 다시 시도하세요."), wait)
		case gateway.Status == 429 || strings.Contains(gateway.Code, "limit"):
			return t("Free usage is temporarily limited. Try again later with Alt+R.", "무료 사용이 일시 제한됐습니다. 잠시 후 Alt+R로 다시 시도하세요.")
		case gateway.Code == "provider_not_ready" || gateway.Code == "no_free_models" || gateway.Code == "no_eligible_free_route":
			return t("No free connection is ready for this model. Press F4 to check providers or F2 to choose a model.", "이 모델에 연결된 무료 공급처가 없습니다. F4로 공급처를 확인하거나 F2로 모델을 선택하세요.")
		case gateway.Code == "gateway_unavailable" || gateway.Code == "gateway_auth_required" || gateway.Code == "invalid_api_key" || gateway.Code == "local_only" || gateway.Code == "local_client_required":
			return terminalSafe(gateway.Message)
		case gateway.Status == 401 || gateway.Status == 403:
			return t("This connection needs a login. Press F4 to connect.", "로그인이 필요합니다. F4로 공급처를 연결하세요.")
		case gateway.Status >= 500:
			return t("The model connection is unstable. Press Alt+R to retry.", "모델 연결이 불안정합니다. Alt+R로 다시 시도하세요.")
		case gateway.Status == 413:
			return t("The conversation and attachments exceed the provider's input limit. Start a new conversation with Alt+N or detach files.", "대화와 첨부가 공급처 입력 제한을 넘었습니다. Alt+N으로 새 대화를 시작하거나 첨부를 제거하세요.")
		case gateway.Status == 400:
			return t("Check your message length or start a new conversation with Alt+N. Your work is still here.", "메시지 길이를 확인하거나 Alt+N으로 새 대화를 시작하세요. 작업 내용은 보존됩니다.")
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return t("The response timed out. Press Alt+R to retry.", "응답 시간이 초과됐습니다. Alt+R로 다시 시도하세요.")
	}
	return terminalSafe(err.Error())
}
