// Adapted from Nekot util/shared-colors.go. See LICENSE in this directory.
package tui

import (
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
)

type schemeColors struct {
	text, accent, border, active compat.AdaptiveColor
}

func grooveColors() schemeColors {
	adaptive := func(dark, light string) compat.AdaptiveColor {
		return compat.AdaptiveColor{Dark: lipgloss.Color(dark), Light: lipgloss.Color(light)}
	}
	return schemeColors{
		text:   adaptive("#EBDBB2", "#3d2e07"),
		accent: adaptive("#DD843B", "#a16f2a"),
		border: adaptive("#C0A568", "#917536"),
		active: adaptive("#98971A", "#7f9150"),
	}
}
