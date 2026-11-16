// Adapted from Nekot components/models-list.go. See LICENSE in this directory.
package tui

import (
	"fmt"
	"io"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

type modelsList struct {
	list list.Model
}

type modelItem client.Model

func (i modelItem) FilterValue() string { return i.Name + " " + i.ID }

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
	}
	row := fmt.Sprintf("%s%d. %s", marker, index+1, i.Name)
	fmt.Fprint(w, style.MaxWidth(m.Width()).Render(row))
}

func newModelsList() modelsList {
	items := []list.Item{modelItem{ID: "preview/auto", Name: "preview/auto"},
		modelItem{ID: "preview/deepseek", Name: "preview/deepseek"}, modelItem{ID: "preview/local", Name: "preview/local"}}
	l := list.New(items, modelItemDelegate{}, 26, 12)
	l.SetShowStatusBar(false)
	l.SetShowPagination(false)
	l.SetShowTitle(false)
	l.SetShowHelp(false)
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
