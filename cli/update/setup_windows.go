package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func hideVersionProbe(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}

// AddSetupShortcut adds a per-user Start menu entry for the stable launcher.
func AddSetupShortcut(root, home string) error {
	return setupShortcut(root, home, "add")
}

func removeSetupShortcut(root string) {
	_ = setupShortcut(root, "", "remove")
}

func setupShortcut(root, home, action string) error {
	programs, err := windows.KnownFolderPath(windows.FOLDERID_Programs, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return err
	}
	return setStartMenuShortcut(programs, filepath.Join(root, "bin", "freeway.exe"), home, action)
}

func setStartMenuShortcut(programs, launcher, home, action string) error {
	shortcut := filepath.Join(programs, "Freeway.lnk")
	if _, err := os.Lstat(shortcut); action == "remove" && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	powershell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	cmd := exec.CommandContext(ctx, powershell, "-NoProfile", "-NonInteractive", "-Command", `
$ErrorActionPreference = 'Stop'
$shortcut = $env:FREEWAY_SETUP_SHORTCUT
$target = $env:FREEWAY_SETUP_TARGET
$shell = New-Object -ComObject WScript.Shell
$entry = $shell.CreateShortcut($shortcut)
if (Test-Path -LiteralPath $shortcut) {
  if ($entry.TargetPath -ine $target) {
    if ($env:FREEWAY_SETUP_ACTION -eq 'remove') { exit 0 }
    throw 'Another Freeway shortcut already uses this Start menu entry.'
  }
  if ($env:FREEWAY_SETUP_ACTION -eq 'remove') {
    Remove-Item -LiteralPath $shortcut -Force
    exit 0
  }
}
if ($env:FREEWAY_SETUP_ACTION -eq 'remove') { exit 0 }
$entry.TargetPath = $target
$entry.WorkingDirectory = $env:FREEWAY_SETUP_HOME
$entry.Description = 'Open Freeway'
$entry.Save()
`)
	hideVersionProbe(cmd)
	cmd.Env = append(os.Environ(), "FREEWAY_SETUP_SHORTCUT="+shortcut, "FREEWAY_SETUP_TARGET="+launcher,
		"FREEWAY_SETUP_HOME="+home, "FREEWAY_SETUP_ACTION="+action)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("Start menu shortcut: %w: %s", err, output)
	}
	return nil
}
