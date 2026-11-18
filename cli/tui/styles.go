// Adapted from Nekot util/shared-colors.go. See LICENSE in this directory.
package tui

import (
	"charm.land/lipgloss/v2"
	"image/color"
	"sync/atomic"
)

type schemeColors struct {
	text, accent, border, active, code, muted, selected color.Color
	userAccent, userPanel                               color.Color
}

var lightBackground atomic.Bool

func grooveColors() schemeColors {
	adaptive := func(dark, light string) color.Color {
		if lightBackground.Load() {
			return lipgloss.Color(light)
		}
		return lipgloss.Color(dark)
	}
	return schemeColors{
		text:       adaptive("#DEE8E0", "#23372B"),
		accent:     adaptive("#99D2A7", "#236844"),
		border:     adaptive("#3D5344", "#8BAA94"),
		active:     adaptive("#99D2A7", "#236844"),
		code:       adaptive("#A7CBD7", "#245D77"),
		muted:      adaptive("#748C7B", "#607468"),
		selected:   adaptive("#263E2C", "#E2EFE5"),
		userAccent: adaptive("#B9D9FF", "#234F79"),
		userPanel:  adaptive("#243447", "#E5F0FC"),
	}
}
