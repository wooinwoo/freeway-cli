package update

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

func TestUserPathAddsOnceAndRemovesOnlyFreeway(t *testing.T) {
	subkey := `Software\FreewayPathTest` + strconv.FormatInt(time.Now().UnixNano(), 10)
	key, _, err := registry.CreateKey(registry.CURRENT_USER, subkey, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { key.Close(); _ = registry.DeleteKey(registry.CURRENT_USER, subkey) })
	if err := key.SetExpandStringValue("Path", `%USERPROFILE%\tools;C:\Other;`); err != nil {
		t.Fatal(err)
	}
	dir := `C:\Users\me\.freeway\app\bin`
	for range 2 {
		if err := setUserPath(subkey, dir, true); err != nil {
			t.Fatal(err)
		}
	}
	value, kind, err := key.GetStringValue("Path")
	if err != nil || kind != registry.EXPAND_SZ || value != `%USERPROFILE%\tools;C:\Other;`+dir {
		t.Fatalf("added: %q %d %v", value, kind, err)
	}
	if err := setUserPath(subkey, dir+`\`, false); err != nil {
		t.Fatal(err)
	}
	if value, _, _ := key.GetStringValue("Path"); value != `%USERPROFILE%\tools;C:\Other` {
		t.Fatalf("removed: %q", value)
	}
}

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
