// Adapted from Nekot panes/prompt-pane.go. See LICENSE in this directory.
package tui

import (
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"
)

func newPrompt() textarea.Model {
	colors := grooveColors()
	input := textarea.New()
	input.SetVirtualCursor(false)
	input.Placeholder = "Message…  / commands  ↑ history"
	input.SetPromptFunc(2, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
			return "› "
		}
		return "  "
	})
	styles := input.Styles()
	styles.Focused.Prompt = lipgloss.NewStyle().Foreground(colors.userAccent)
	styles.Focused.CursorLine = styles.Focused.CursorLine.Background(lipgloss.NoColor{})
	styles.Focused.EndOfBuffer = lipgloss.NewStyle().Foreground(colors.active)
	styles.Focused.LineNumber = lipgloss.NewStyle().Foreground(colors.accent)
	input.SetStyles(styles)
	input.EndOfBufferCharacter = ' '
	input.ShowLineNumbers = false
	input.CharLimit = 0
	input.MaxHeight = 0
	input.DynamicHeight = true
	input.MinHeight = 1
	input.SetHeight(1)
	input.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("ctrl+j", "ctrl+o", "alt+enter", "shift+enter"))
	return input
}

func applyPromptTheme(input *textarea.Model) {
	c := grooveColors()
	styles := input.Styles()
	styles.Focused.Prompt = lipgloss.NewStyle().Foreground(c.userAccent)
	styles.Focused.CursorLine = styles.Focused.CursorLine.Background(lipgloss.NoColor{})
	styles.Focused.EndOfBuffer = lipgloss.NewStyle().Foreground(c.muted)
	input.SetStyles(styles)
}
