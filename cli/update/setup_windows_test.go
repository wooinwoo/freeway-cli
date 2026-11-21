package update

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStartMenuShortcutSafety(t *testing.T) {
	programs := t.TempDir()
	target := filepath.Join(programs, "Freeway with spaces", "freeway.exe")
	link := filepath.Join(programs, "Freeway.lnk")
	if err := setStartMenuShortcut(programs, target, programs, "add"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(link); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(programs, "other.exe")
	if err := setStartMenuShortcut(programs, other, programs, "add"); err == nil {
		t.Fatal("an existing shortcut was replaced")
	}
	if err := setStartMenuShortcut(programs, other, programs, "remove"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(link); err != nil {
		t.Fatal("an unrelated shortcut was removed")
	}
	if err := setStartMenuShortcut(programs, target, programs, "remove"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(link); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("shortcut removal: %v", err)
	}
}
