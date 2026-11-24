// Adapted from Nekot components/models-list.go. See LICENSE in this directory.
package tui

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

type modelsList struct {
	list list.Model
}

type modelItem client.Model

type modelRanking uint8

const (
	generalRanking modelRanking = iota
	codingRanking
	reasoningRanking
)

func (order modelRanking) rank(model client.Model) int {
	switch order {
	case codingRanking:
		return model.CodingRank
	case reasoningRanking:
		return model.ReasoningRank
	default:
		return model.QualityRank
	}
}

func (m Model) rankedModels() []client.Model {
	if m.modelRanking == generalRanking {
		return m.availableModels
	}
	var indexes []int
	var comparable []client.Model
	for i, model := range m.availableModels {
		if model.ID != "free/auto" && m.modelRanking.rank(model) > 0 {
			indexes = append(indexes, i)
			comparable = append(comparable, model)
		}
	}
	if len(comparable) < 2 {
		return m.availableModels
	}
	models := slices.Clone(m.availableModels)
	slices.SortStableFunc(comparable, func(a, b client.Model) int {
		return m.modelRanking.rank(a) - m.modelRanking.rank(b)
	})
	for i, index := range indexes {
		models[index] = comparable[i]
	}
	return models
}

func (m Model) changeModelRanking() (tea.Model, tea.Cmd) {
	selected, _ := m.models.list.SelectedItem().(modelItem)
	m.modelRanking = (m.modelRanking + 1) % (reasoningRanking + 1)
	m.menuAll = m.modelPickerItems()
	cmd := m.applyMenuFilter()
	m.selectMenuItem(selected.ID)
	return m, cmd
}

func (m Model) rankingTitle() string {
	switch m.modelRanking {
	case codingRanking:
		return m.tr("Models · Coding", "모델 · 코딩")
	case reasoningRanking:
		return m.tr("Models · Reasoning", "모델 · 추론")
	default:
		return m.tr("Models · General", "모델 · 종합")
	}
}

func (m Model) rankingEvidence(item modelItem) string {
	if m.modelRanking.rank(client.Model(item)) <= 0 {
		return m.tr("No benchmark comparison", "이 기준의 비교 자료 없음")
	}
	switch m.modelRanking {
	case codingRanking:
		return "Terminal-Bench 4.0 · Artificial Analysis"
	case reasoningRanking:
		return "HLE-Diamond · Artificial Analysis"
	default:
		return "Intelligence Index · Artificial Analysis"
	}
}

func (i modelItem) FilterValue() string { return i.Name + " " + i.ID + " " + i.Detail }

func (m Model) modelPickerItems() []list.Item {
	items := make([]list.Item, 0, len(m.availableModels))
	for _, model := range m.rankedModels() {
		if model.ID == "free/auto" {
			model.Name = m.tr("Auto", "자동")
			model.Detail = m.tr("Pick a free route for the request", "요청에 맞는 무료 경로 선택")
		}
		if m.gateway != nil && !m.ready {
			model.Detail = m.tr("Connection unchecked", "연결 미확인") + " · " + model.Detail
		} else if len(model.ReadyProviders) > 0 {
			model.Detail = fmt.Sprintf(m.tr("Ready routes (%d) · %s", "사용 가능 %d곳 · %s"), len(model.ReadyProviders), strings.Join(model.ReadyProviders, " · "))
		} else if !model.Ready {
			model.Detail = m.tr("Unavailable now · ", "지금 사용 불가 · ") + model.Detail
		}
		if model.LocalTokensRemaining != nil {
			model.Detail += fmt.Sprintf(m.tr(" · %d local tokens", " · 로컬 토큰 %d개"), *model.LocalTokensRemaining)
		}
		if model.ID == m.selected {
			model.Detail = strings.TrimSuffix(m.tr("✓ Current · ", "✓ 현재 · ")+model.Detail, " · ")
		}
		items = append(items, modelItem(model))
	}
	return items
}

type modelItemDelegate struct{}

func (d modelItemDelegate) Height() int                             { return 1 }
func (d modelItemDelegate) Spacing() int                            { return 0 }
func (d modelItemDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d modelItemDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	i, ok := item.(modelItem)
	if !ok {
		return
	}
	colors := grooveColors()
	style := lipgloss.NewStyle().Foreground(colors.text)
	marker := "  "
	if index == m.Index() {
		marker = "> "
		style = style.Foreground(colors.active)
		style = style.Background(colors.selected)
	}
	nameWidth := menuNameWidth(m.Width())
	prefix := fmt.Sprintf("%s%2d. ", marker, index+1)
	row := prefix + padCells(oneLine(i.Name), nameWidth) + "  " + oneLine(i.Detail)
	fmt.Fprint(w, style.MaxWidth(m.Width()).Render(padCells(ansi.Truncate(row, m.Width(), "…"), m.Width())))
}

func newModelsList() modelsList {
	items := []list.Item{modelItem{ID: "preview/auto", Name: "preview/auto"},
		modelItem{ID: "preview/deepseek", Name: "preview/deepseek"}, modelItem{ID: "preview/local", Name: "preview/local"}}
	l := list.New(items, modelItemDelegate{}, 26, 12)
	l.SetShowStatusBar(false)
	l.SetShowPagination(false)
	l.SetShowTitle(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)
	l.DisableQuitKeybindings()
	return modelsList{list: l}
}

func (l modelsList) View() string {
	return l.list.View()
}

func (l modelsList) Update(msg tea.Msg) (modelsList, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		if msg.Button == tea.MouseWheelUp {
			l.list.CursorUp()
			return l, nil
		}
		if msg.Button == tea.MouseWheelDown {
			l.list.CursorDown()
			return l, nil
		}
	}
	var cmd tea.Cmd
	l.list, cmd = l.list.Update(msg)
	return l, cmd
}
