package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/tui"
)

func main() {
	if _, err := tea.NewProgram(tui.New()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "Freeway TUI:", err)
		os.Exit(1)
	}
}
