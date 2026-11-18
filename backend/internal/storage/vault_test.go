package storage

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVaultLockAndMissingKey(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "state")
	vault, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	alias := path
	if runtime.GOOS != "windows" {
		alias = filepath.Join(parent, "alias")
		if err := os.Symlink(path, alias); err != nil {
			t.Fatal(err)
		}
	}
	if second, err := Open(alias); !errors.Is(err, ErrLocked) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("second writer: %v", err)
	}
	if err := vault.Write("chat-test.enc", []byte("private content")); err != nil {
		t.Fatal(err)
	}
	if err := vault.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(path, keyName)); err != nil {
		t.Fatal(err)
	}
	if fresh, err := Open(path); err == nil {
		_ = fresh.Close()
		t.Fatal("vault silently generated a key for existing records")
	}
	if _, err := os.Stat(filepath.Join(path, keyName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing key should remain untouched: %v", err)
	}
}
