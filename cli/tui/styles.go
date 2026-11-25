// Adapted from Nekot util/shared-colors.go. See LICENSE in this directory.
package tui

import (
	"image/color"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type schemeColors struct {
	text, accent, border, active, code, muted, selected color.Color
	userAccent, userPanel                               color.Color
	warn                                                color.Color
}

var lightBackground atomic.Bool
var themeOverride atomic.Int32

const (
	themeSystem = iota
	themeDark
	themeLight
	themeIndigo
	themeAmber
)

func applyTheme(name string) {
	themeOverride.Store(map[string]int32{"dark": themeDark, "light": themeLight, "indigo": themeIndigo, "amber": themeAmber}[name])
}

func themedView(view tea.View, noColor bool) tea.View {
	if noColor {
		return view
	}
	switch themeOverride.Load() {
	case themeDark:
		view.BackgroundColor = lipgloss.Color("#111813")
	case themeLight:
		view.BackgroundColor = lipgloss.Color("#F5FAF6")
	default:
		return view
	}
	view.ForegroundColor = grooveColors().text
	return view
}

// keyHints draws "Key action · Key action" help with each key brighter than its action.
func keyHints(text string, width int) string {
	c := grooveColors()
	key, muted := lipgloss.NewStyle().Foreground(c.text), lipgloss.NewStyle().Foreground(c.muted)
	parts := strings.Split(ansi.Truncate(text, width, ""), "·")
	for i, part := range parts {
		core := strings.TrimLeft(part, " ")
		lead := part[:len(part)-len(core)]
		first, rest, _ := strings.Cut(core, " ")
		r, _ := utf8.DecodeRuneInString(first)
		// Keys start with a capital, digit or symbol (Enter, F4, ↑↓, /models); words such as "type" do not.
		if first == "" || unicode.IsLower(r) || unicode.Is(unicode.Hangul, r) {
			parts[i] = muted.Render(part)
			continue
		}
		if rest != "" {
			rest = " " + rest
		}
		parts[i] = muted.Render(lead) + key.Render(first) + muted.Render(rest)
	}
	return strings.Join(parts, muted.Render("·"))
}

func grooveColors() schemeColors {
	theme := themeOverride.Load()
	lightMode := theme == themeLight || theme != themeDark && lightBackground.Load()
	adaptive := func(dark, light string) color.Color {
		if lightMode {
			return lipgloss.Color(light)
		}
		return lipgloss.Color(dark)
	}
	colors := schemeColors{
		text:       adaptive("#DEE8E0", "#23372B"),
		accent:     adaptive("#99D2A7", "#236844"),
		border:     adaptive("#3D5344", "#8BAA94"),
		active:     adaptive("#99D2A7", "#236844"),
		code:       adaptive("#A7CBD7", "#245D77"),
		muted:      adaptive("#748C7B", "#607468"),
		selected:   adaptive("#263E2C", "#E2EFE5"),
		userAccent: adaptive("#B9D9FF", "#234F79"),
		userPanel:  adaptive("#243447", "#E5F0FC"),
		warn:       adaptive("#F08C7A", "#B23A2B"),
	}
	switch theme {
	case themeIndigo:
		colors.accent = adaptive("#B6BFFF", "#403E9D")
		colors.border = adaptive("#4D5076", "#888AC2")
		colors.selected = adaptive("#34385D", "#EBEAFF")
	case themeAmber:
		colors.accent = adaptive("#F2C879", "#77500E")
		colors.border = adaptive("#614C2D", "#A69978")
		colors.selected = adaptive("#4B381E", "#FAEDCB")
	}
	colors.active = colors.accent
	return colors
}
