package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
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
	_ = setUserPath(`Environment`, filepath.Join(root, "bin"), false)
}

// AddUserPath puts the launcher folder on the user's PATH so new terminals can run freeway.
func AddUserPath(root string) error {
	return setUserPath(`Environment`, filepath.Join(root, "bin"), true)
}

// setUserPath edits one entry of the user's Path value and leaves every other entry as written.
func setUserPath(subkey, dir string, add bool) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, subkey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	current, kind, err := key.GetStringValue("Path")
	if errors.Is(err, registry.ErrNotExist) {
		current, kind, err = "", registry.EXPAND_SZ, nil
	}
	if err != nil {
		return err
	}
	var kept []string
	found := false
	for _, entry := range strings.Split(current, ";") {
		if entry != "" && strings.EqualFold(filepath.Clean(entry), filepath.Clean(dir)) {
			found = true
			continue
		}
		kept = append(kept, entry)
	}
	if found == add {
		return nil
	}
	value := strings.Join(kept, ";")
	if add {
		value = strings.TrimRight(current, ";")
		if value != "" {
			value += ";"
		}
		value += dir
	}
	if kind == registry.EXPAND_SZ {
		err = key.SetExpandStringValue("Path", value)
	} else {
		err = key.SetStringValue("Path", value)
	}
	if err == nil && subkey == `Environment` {
		// Explorer reloads the environment, so terminals opened afterwards see the change.
		name, _ := windows.UTF16PtrFromString("Environment")
		var result uintptr
		_, _, _ = sendMessageTimeout.Call(0xffff, 0x1a, 0, uintptr(unsafe.Pointer(name)), 2, 5000, uintptr(unsafe.Pointer(&result)))
	}
	return err
}

var sendMessageTimeout = windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")

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
