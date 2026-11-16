package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/client"
	"github.com/wooinwoo/freeway-cli/cli/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Freeway:", err)
		os.Exit(1)
	}
}

func run() error {
	preview := flag.Bool("preview", false, "run the offline UI preview")
	pocDir := flag.String("poc-dir", "", "path to the existing POC source directory")
	flag.Parse()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := tui.New()
	if !*preview {
		gateway, err := client.NewPOC(*pocDir)
		if err != nil {
			return err
		}
		defer func() {
			cancel()
			gateway.Close()
		}()
		model = tui.NewConnected(ctx, gateway)
	}
	_, err := tea.NewProgram(model).Run()
	return err
}
