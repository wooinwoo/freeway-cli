package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/wooinwoo/freeway-cli/cli/tui"
	"github.com/wooinwoo/freeway-cli/cli/update"
)

func interactiveTerminal() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

// The launcher restarts the same invocation after activating the approved build.
func startupUpdate(ctx context.Context, prefs tui.Preferences, options []tea.ProgramOption) (bool, error) {
	root := os.Getenv("FREEWAY_INSTALL_ROOT")
	if root == "" || version == "dev" {
		return false, nil
	}
	return offerStartupUpdate(ctx, update.Manager{Root: root}, version, prefs, options)
}

func offerStartupUpdate(ctx context.Context, manager update.Manager, current string, prefs tui.Preferences, options []tea.ProgramOption) (bool, error) {
	check, cancel := context.WithTimeout(ctx, 3*time.Second)
	release, err := manager.Check(check)
	cancel()
	if err != nil || !release.NewerThan(current) {
		return false, nil
	}
	t := prefs.Text
	dialog := tui.SetupDialog{
		Title:  t("Freeway update available", "Freeway 업데이트가 있습니다"),
		Detail: fmt.Sprintf("%s  →  %s", current, release.Version),
		Options: []tui.SetupOption{
			{ID: "update", Label: t("Update and restart", "업데이트 후 시작")},
			{ID: "later", Label: t("Continue with this version", "현재 버전으로 계속")},
		},
	}
	for {
		choice, err := dialog.Run(prefs, options...)
		if err != nil || choice != "update" {
			return false, err
		}
		fmt.Fprintln(os.Stderr, t("Updating Freeway…", "Freeway 업데이트 중…"))
		if _, err := manager.InstallRelease(ctx, release); err != nil {
			dialog.Notice = t("Update failed. Retry or continue with the installed version.", "업데이트에 실패했습니다. 다시 시도하거나 현재 버전으로 계속하세요.")
			continue
		}
		launcher := "freeway"
		if runtime.GOOS == "windows" {
			launcher += ".exe"
		}
		cmd := exec.CommandContext(ctx, filepath.Join(manager.Root, "bin", launcher), os.Args[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return true, cmd.Run()
	}
}
