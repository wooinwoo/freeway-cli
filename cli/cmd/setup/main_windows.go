package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
	"unsafe"

	"github.com/wooinwoo/freeway-cli/cli/update"
	"golang.org/x/sys/windows"
)

var version, archiveSize, archiveSHA256 string

var messageBox = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")

const (
	buttonOK    = 0
	buttonYesNo = 4
	iconError   = 0x10
	iconInfo    = 0x40
	answerYes   = 6
)

func message(text string, style uintptr) uintptr {
	body, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return 0
	}
	title, _ := windows.UTF16PtrFromString("Freeway Setup")
	selected, _, _ := messageBox.Call(0, uintptr(unsafe.Pointer(body)), uintptr(unsafe.Pointer(title)), style)
	return selected
}

func main() {
	root := flag.String("root", "", "dedicated installation directory")
	quiet := flag.Bool("quiet", false, "install without dialogs, shortcuts or launching Freeway")
	flag.Parse()
	if err := install(*root, *quiet); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if !*quiet {
			message(err.Error(), buttonOK|iconError)
		}
		os.Exit(1)
	}
}

func install(root string, quiet bool) error {
	if flag.NArg() != 0 {
		return errors.New("unexpected setup argument")
	}
	if root == "" {
		var err error
		root, err = update.DefaultRoot()
		if err != nil {
			return err
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	file, err := os.Open(self)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	size, err := strconv.ParseInt(archiveSize, 10, 64)
	if err != nil || size <= 0 || size > update.MaxArchive || size >= info.Size() {
		return errors.New("The Freeway setup file is incomplete. Download it again from the Releases page.")
	}
	if !quiet && message("Install Freeway "+version+" for your account?\n\n"+root, buttonYesNo|iconInfo) != answerYes {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	bundle := io.NewSectionReader(file, info.Size()-size, size)
	state, err := (update.Manager{Root: root}).InstallBundle(ctx, bundle, archiveSHA256, version)
	if err != nil {
		return fmt.Errorf("Could not install Freeway.\n\n%w", err)
	}
	launcher := filepath.Join(root, "bin", "freeway.exe")
	if quiet {
		fmt.Println(state.Active)
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = root
	}
	installed := "Freeway " + state.Active + " is installed. Find it in the Start menu.\n\nOpen Freeway now?"
	if err := update.AddSetupShortcut(root, home); err != nil {
		installed = "Freeway " + state.Active + " is installed. Windows could not add a Start menu shortcut.\n\n" +
			launcher + "\n\nOpen Freeway now?"
	}
	if message(installed, buttonYesNo|iconInfo) != answerYes {
		return nil
	}
	executable, err := windows.UTF16PtrFromString(launcher)
	if err != nil {
		return err
	}
	directory, err := windows.UTF16PtrFromString(home)
	if err != nil {
		return err
	}
	if err := windows.ShellExecute(0, nil, executable, nil, directory, windows.SW_SHOWNORMAL); err != nil {
		return fmt.Errorf("Freeway is installed at %s.\n\nCould not open the terminal: %w", launcher, err)
	}
	return nil
}
