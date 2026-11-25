package tui

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

type providerStatusesLoaded struct {
	statuses []client.ProviderStatus
	provider string
	request  uint64
	err      error
}

type providerSetupFinished struct {
	id  string
	err error
}

func (m Model) WithProviderSetup(executable string) Model {
	m.providerSetup = executable
	return m
}

func (m Model) WithProviderStatuses(statuses []client.ProviderStatus) Model {
	if statuses != nil {
		m.providers = make(map[string]client.ProviderStatus, len(statuses))
		m.providerOrder = make([]string, 0, len(statuses))
		for _, provider := range statuses {
			m.providers[provider.ID] = provider
			m.providerOrder = append(m.providerOrder, provider.ID)
		}
	}
	return m
}

func (m Model) connectProvider(id string) (tea.Model, tea.Cmd) {
	if m.providerSetup == "" {
		return m, nil
	}
	if m.busy {
		m.notify(m.tr("Stop the current response to open provider setup.", "현재 응답을 중지한 뒤 공급처 연결을 여세요."))
		return m, nil
	}
	cmd := exec.CommandContext(m.ctx, m.providerSetup, "connect", id)
	if m.prefs.NoColor {
		cmd.Env = append(os.Environ(), "NO_COLOR=1")
	}
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return providerSetupFinished{id: id, err: err}
	})
}

func (m Model) showProviders() (tea.Model, tea.Cmd) {
	return m.refreshProviders("")
}

func (m Model) refreshProviders(id string) (tea.Model, tea.Cmd) {
	gateway, ok := m.gateway.(interface {
		ProviderStatuses(context.Context) ([]client.ProviderStatus, error)
	})
	if !ok {
		m.notify(m.tr("Provider status requires a connected gateway.", "게이트웨이에 연결해야 공급처를 확인할 수 있습니다."))
		return m, nil
	}
	var focus tea.Cmd
	if !m.modelsFocused || m.menu != "providers" {
		m.menu = "providers"
		m.models.list.SetItems(m.providerItems())
		next, cmd := m.changeFocus(true)
		m, focus = next.(Model), cmd
	}
	if id == "" && m.providersLoading {
		return m, focus
	}
	m.providersRequest++
	request := m.providersRequest
	m.providersLoading, m.providersError = true, ""
	m.providerCheckID = id
	m.providerChecksPending = nil
	if scoped, ok := m.gateway.(interface {
		ProviderStatus(context.Context, string) (client.ProviderStatus, error)
	}); ok {
		check := func(id string) tea.Cmd {
			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(m.ctx, client.CatalogTimeout)
				defer cancel()
				status, err := scoped.ProviderStatus(ctx, id)
				return providerStatusesLoaded{statuses: []client.ProviderStatus{status}, provider: id, request: request, err: err}
			}
		}
		if id != "" {
			rows := m.updateProviderRows()
			return m, tea.Batch(focus, rows, check(id))
		}
		// The managed gateway keeps the same provider roster for this session.
		if m.providerSetup != "" && len(m.providerOrder) > 0 &&
			!slices.ContainsFunc(m.providerOrder, func(id string) bool { return !m.providers[id].ScopedCheck }) {
			m.providerChecksPending = make(map[string]bool, len(m.providerOrder))
			checks := []tea.Cmd{focus}
			for _, id := range m.providerOrder {
				m.providerChecksPending[id] = true
				checks = append(checks, check(id))
			}
			checks = append(checks, m.updateProviderRows())
			return m, tea.Batch(checks...)
		}
	}
	return m, tea.Batch(focus, func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, client.CatalogTimeout)
		defer cancel()
		statuses, err := gateway.ProviderStatuses(ctx)
		return providerStatusesLoaded{statuses: statuses, request: request, err: err}
	})
}

func (m Model) receiveProviderStatuses(msg providerStatusesLoaded) (tea.Model, tea.Cmd) {
	if msg.request != m.providersRequest {
		return m, nil
	}
	if m.providerChecksPending[msg.provider] {
		m.providerChecksPending = maps.Clone(m.providerChecksPending)
		delete(m.providerChecksPending, msg.provider)
		m.providersLoading = len(m.providerChecksPending) > 0
		if msg.err == nil && len(msg.statuses) == 1 && msg.statuses[0].ID == msg.provider {
			previous, current := m.providers[msg.provider], msg.statuses[0]
			if previous.State != current.State || previous.FreeModels != current.FreeModels || previous.AutoExcluded != current.AutoExcluded {
				m.modelListNeedsRefresh = true
			}
			m.storeProviderStatus(current)
		} else {
			status := m.providers[msg.provider]
			status.State, status.CheckedAt = "status_check_failed", ""
			status.KeyChecked, status.CatalogChecked = false, false
			m.storeProviderStatus(status)
		}
		cmd := m.updateProviderRows()
		return m, cmd
	}
	m.providersLoading = false
	if msg.err != nil {
		if msg.provider == "" {
			m.providerCheckID = ""
			m.providersError = m.tr("Could not check this gateway. Ctrl+R to retry.", "게이트웨이를 확인하지 못했습니다. Ctrl+R로 다시 시도하세요.")
		} else {
			name := m.providers[msg.provider].Name
			if name == "" {
				name = msg.provider
			}
			m.providersError = name + m.tr(" · Check failed. Ctrl+R to retry.", " · 상태 확인 실패 · Ctrl+R 다시 시도")
		}
		return m, nil
	}
	if msg.provider == "" {
		m = m.WithProviderStatuses(msg.statuses)
	} else if len(msg.statuses) == 1 && msg.statuses[0].ID == msg.provider {
		m.storeProviderStatus(msg.statuses[0])
	}
	cmd := m.updateProviderRows()
	return m, cmd
}

func (m *Model) storeProviderStatus(status client.ProviderStatus) {
	providers := maps.Clone(m.providers)
	if providers == nil {
		providers = make(map[string]client.ProviderStatus)
	}
	if _, exists := providers[status.ID]; !exists {
		m.providerOrder = append(slices.Clone(m.providerOrder), status.ID)
	}
	providers[status.ID] = status
	m.providers = providers
}

func (m *Model) updateProviderRows() tea.Cmd {
	if !m.modelsFocused || m.menu != "providers" {
		return nil
	}
	selected, _ := m.models.list.SelectedItem().(modelItem)
	m.menuAll = m.providerItems()
	cmd := m.applyMenuFilter()
	m.selectMenuItem(selected.ID)
	return cmd
}

func (m Model) providerItems() []list.Item {
	items := make([]list.Item, 0, len(m.providerOrder))
	for _, id := range m.providerOrder {
		provider := m.providers[id]
		text := ProviderConnectionText(m.prefs, provider)
		if provider.Management && provider.AutoSupported && provider.AutoExcluded && provider.State != "disconnected" {
			text += m.tr(" · Auto off", " · Auto 제외")
		}
		if m.providerChecksPending[id] {
			text = m.tr("Checking… · ", "확인 중… · ") + text
		}
		items = append(items, modelItem{ID: provider.ID, Name: provider.Name, Detail: text})
	}
	return items
}

func (m Model) providerSummary() string {
	if m.providersError != "" && m.providerCheckID == "" {
		return m.tr("Connection status unavailable · F4", "연결 상태 확인 실패 · F4")
	}
	if m.providers == nil {
		if m.providerSetup != "" {
			return m.tr("Connection status unchecked · F4", "연결 상태 미확인 · F4")
		}
		return ""
	}
	var connected, catalogReady []string
	var unchecked, unreachable int
	for _, p := range m.providers {
		if m.providerChecksPending[p.ID] {
			continue
		}
		if p.State == "unreachable" {
			unreachable++
		}
		if p.ID == m.providerCheckID && m.providersError != "" {
			unchecked++
			continue
		}
		if p.State == "ready" {
			switch {
			case !p.KeyConfigured || p.KeyChecked:
				connected = append(connected, p.Name)
			case p.CatalogChecked:
				catalogReady = append(catalogReady, p.Name)
			default:
				unchecked++
			}
		}
	}
	var summary []string
	if len(connected) > 0 {
		slices.Sort(connected)
		summary = append(summary, m.tr("Connected · ", "연결됨 · ")+strings.Join(connected, ", "))
	}
	if len(catalogReady) > 0 {
		slices.Sort(catalogReady)
		summary = append(summary, m.tr("Catalog checked · ", "모델 목록 확인 · ")+strings.Join(catalogReady, ", "))
	}
	if len(summary) == 0 {
		label := m.tr("No providers ready", "사용 가능한 공급처 없음")
		if unreachable > 0 {
			label = m.tr("Can't reach providers · Check your internet", "공급처에 연결할 수 없음 · 인터넷 연결 확인")
		}
		if unchecked > 0 {
			label = fmt.Sprintf(m.tr("%d keys saved · connection unchecked", "키 %d개 등록 · 연결 미확인"), unchecked)
		}
		if m.providersLoading {
			label = m.tr("Checking provider connections…", "공급처 연결 확인 중…")
		}
		summary = append(summary, label)
	}
	return strings.Join(summary, " · ") + " · F4"
}

func ProviderConnectionText(prefs Preferences, provider client.ProviderStatus) string {
	t := prefs.Text
	auth := t("Guest", "게스트")
	if provider.KeyConfigured {
		auth = t("Key set", "키 등록됨")
	}
	if provider.ID == "opencode" {
		auth = t("Local CLI", "로컬 CLI")
	}
	switch provider.State {
	case "disconnected":
		return t("Disconnected in Freeway", "Freeway에서 연결 해제됨")
	case "ready":
		text := fmt.Sprintf(t("Connected · %s · %d free models", "연결됨 · %s · 무료 모델 %d개"), auth, provider.FreeModels)
		if provider.KeyConfigured {
			switch {
			case provider.KeyChecked:
				text = fmt.Sprintf(t("Key verified · %d free models", "키 인증 확인 · 무료 모델 %d개"), provider.FreeModels)
			case provider.CatalogChecked:
				text = fmt.Sprintf(t("Catalog checked · Key saved · %d models", "모델 목록 확인 · 키 등록됨 · %d개"), provider.FreeModels)
			default:
				text = t("Connection unchecked · Key saved", "연결 미확인 · 키 등록됨")
			}
		}
		return text + localTokensText(t, provider.LocalTokensRemaining)
	case "short_replies":
		return auth + t(" · Short replies", " · 짧은 답변만 가능") + localTokensText(t, provider.LocalTokensRemaining)
	case "needs_key":
		return t("API key required", "API 키 필요")
	case "confirm_free_account":
		return t("Confirm free plan · Key set", "무료 플랜 확인 필요 · 키 등록됨")
	case "connection_rejected":
		return t("Connection rejected", "인증 실패")
	case "key_check_unavailable":
		return t("Could not check the saved key", "저장된 키 확인 실패")
	case "limited":
		if provider.RetryAfter > 0 {
			wait := time.Duration(min(provider.RetryAfter, 86400)) * time.Second
			return fmt.Sprintf(t("Unavailable now · Retry in %s · Auto skips it", "지금 사용 불가 · %s 후 다시 시도 · Auto는 건너뜀"), wait)
		}
		return t("Unavailable now · Auto skips it", "지금 사용 불가 · Auto는 건너뜀")
	case "no_free_models":
		return auth + t(" · No eligible free models", " · 사용 가능한 무료 모델 없음")
	case "unreachable":
		return t("Can't reach the server · Check your internet", "서버에 연결할 수 없음 · 인터넷 연결 확인")
	case "credential_store_failed":
		return t("Cannot read saved credentials", "저장된 인증 정보를 읽을 수 없음")
	case "opencode_not_installed":
		return t("CLI required", "CLI 설치 필요")
	default:
		return t("Connection check failed", "연결 확인 실패")
	}
}

func ProviderUsageText(prefs Preferences, provider client.ProviderStatus) string {
	t := prefs.Text
	if !provider.Management {
		return ""
	}
	auto := t("Auto included", "Auto 포함")
	switch {
	case provider.State == "disconnected":
		auto = t("Auto unavailable", "Auto 사용 불가")
	case ProviderUnconnected(provider):
		auto = t("Auto after connecting", "연결 후 Auto 사용")
	case !provider.AutoSupported:
		auto = t("Manual selection only", "모델 직접 선택 전용")
	case provider.AutoExcluded:
		auto = t("Auto excluded", "Auto 제외")
	}
	quota := t("Provider allowance unknown", "공급처 잔여량 확인 불가")
	if provider.ProviderRemaining != nil {
		quota = fmt.Sprintf(t("Provider remaining %d", "공급처 잔여량 %d"), *provider.ProviderRemaining)
	}
	usage := fmt.Sprintf(t("Today (UTC) %d local requests", "오늘(UTC) 로컬 요청 %d회"), provider.LocalRequests)
	details := []string{auto, quota, usage}
	if checked, err := time.Parse(time.RFC3339, provider.CheckedAt); err == nil {
		details = append(details, fmt.Sprintf(t("Checked %s", "%s 확인"), checked.Local().Format("15:04")))
	}
	return strings.Join(details, " · ")
}

// ProviderUnconnected reports a provider that still needs a key or its CLI, so disconnect and Auto toggles mean nothing yet.
func ProviderUnconnected(provider client.ProviderStatus) bool {
	return provider.State == "needs_key" || provider.State == "opencode_not_installed"
}

func providerStatusHelp(provider client.ProviderStatus) string {
	switch provider.State {
	case "opencode_not_installed":
		return "freeway providers install --only opencode"
	case "needs_key", "connection_rejected":
		return "freeway providers auth " + provider.ID
	case "confirm_free_account":
		return "freeway providers auth " + provider.ID + " --confirm-free"
	default:
		return "freeway providers status " + provider.ID
	}
}

func (m Model) replyBudgetHint() string {
	if m.busy || m.commandPending() {
		return ""
	}
	for _, model := range m.availableModels {
		if model.ID != m.selected || model.LocalTokensRemaining == nil {
			continue
		}
		fit := *model.LocalTokensRemaining - m.nextRequestContext().inputEstimate()
		if fit <= 0 {
			return m.tr("Local budget estimate · shorten the context or open a new chat", "로컬 예산 부족 예상 · 문맥을 줄이거나 새 대화를 여세요")
		}
		if fit < m.settings.MaxTokens {
			return fmt.Sprintf(m.tr("Local estimate fits up to %d output tokens · /settings %d", "로컬 예산상 출력 토큰 %d개까지 가능 · /settings %d"), fit, fit)
		}
		break
	}
	return ""
}

func localTokensText(t func(string, string) string, remaining *int) string {
	if remaining == nil {
		return ""
	}
	return fmt.Sprintf(t(" · %d local tokens", " · 로컬 토큰 %d개"), *remaining)
}
