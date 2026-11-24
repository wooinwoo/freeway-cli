package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/wooinwoo/freeway-cli/cli/imagegen"
	"github.com/wooinwoo/freeway-cli/cli/tui"
	"github.com/wooinwoo/freeway-cli/cli/update"
)

func nativeBinary(name string) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(filepath.Dir(self), name)
	if info, err := os.Stat(binary); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is missing; install the complete Freeway package", name)
	}
	return binary, nil
}

func runNativeCommand(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	command := args[0]
	if command == "providers" || command == "gateway" {
		name := "freeway-providers"
		if command == "gateway" {
			name = "freewayd"
		}
		binary, err := nativeBinary(name)
		if err != nil {
			return true, err
		}
		child := exec.Command(binary, args[1:]...)
		child.Env = imagegen.WithoutTokenEnv()
		child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
		return true, child.Run()
	}
	if command != "install" && command != "update" && command != "rollback" && command != "uninstall" && command != "__remove" {
		return false, nil
	}
	args = args[1:]
	check := false
	if command == "update" && len(args) > 0 && args[0] == "check" {
		check = true
		args = args[1:]
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	root := f.String("root", os.Getenv("FREEWAY_INSTALL_ROOT"), "dedicated program directory")
	archive := f.String("archive", "", "verified native archive")
	sha := f.String("sha256", "", "expected archive SHA256")
	releaseURL := f.String("release-url", update.DefaultReleaseURL, "trusted release manifest URL")
	if err := f.Parse(args); err != nil {
		return true, err
	}
	if f.NArg() != 0 {
		return true, errors.New("unexpected update argument")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	m := update.Manager{Root: *root, ReleaseURL: *releaseURL}
	var result any
	var err error
	switch command {
	case "install":
		if *archive == "" || *sha == "" {
			return true, errors.New("use freeway install --archive PATH --sha256 EXPECTED_SHA256 [--root PATH]")
		}
		result, err = m.Install(ctx, *archive, *sha)
		if err == nil && interactiveTerminal() {
			prefs := tui.LoadPreferences()
			if _, setupErr := prefs.ConfigureAppearance(terminalOptions(ctx, prefs.NoColor)...); setupErr != nil {
				fmt.Fprintln(os.Stderr, prefs.Text("Could not complete appearance setup. It will be offered on first launch.", "화면 설정을 마치지 못했습니다. 첫 실행 때 다시 선택할 수 있습니다."), setupErr)
			}
		}
	case "update":
		if check {
			result, err = m.Check(ctx)
		} else {
			result, err = m.Update(ctx)
		}
	case "rollback":
		result, err = m.Rollback(ctx)
	case "uninstall":
		var scheduled bool
		scheduled, err = m.Uninstall(ctx)
		if err == nil {
			if scheduled {
				fmt.Println("Removal scheduled. Close all Freeway processes; account and conversation data are preserved.")
			} else {
				fmt.Println("Freeway program removed. Provider accounts and gateway data were preserved.")
			}
			return true, nil
		}
	case "__remove":
		return true, m.RemoveFiles(ctx)
	}
	if err != nil {
		return true, err
	}
	return true, json.NewEncoder(os.Stdout).Encode(result)
}
