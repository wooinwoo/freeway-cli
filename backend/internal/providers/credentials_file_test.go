package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func fileCredentialFixture(t *testing.T) (CredentialStore, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "auth")
	store, err := NewFileCredentials(dir)
	if err != nil {
		t.Fatal(err)
	}
	return store, dir
}

func TestFileCredentialsMissingLoadsAndCancelledCallsDoNotCreateFiles(t *testing.T) {
	store, dir := fileCredentialFixture(t)
	value, err := store.Load(context.Background(), "groq")
	if err != nil || value != (Credential{}) {
		t.Fatalf("missing credential=%+v err=%v", value, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Load(ctx, "groq"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Load: %v", err)
	}
	if err := store.Save(ctx, "groq", Credential{Key: "unused"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Save: %v", err)
	}
	if _, err := store.Load(context.Background(), "../unknown"); err == nil {
		t.Fatal("unknown provider was accepted")
	}
	if err := store.Save(context.Background(), "../unknown", Credential{}); err == nil {
		t.Fatal("unknown provider was saved")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("read-only calls created the auth directory: %v", err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(context.Background(), "kilo"); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 0 {
		t.Fatalf("empty directory Load created files: %v %v", files, err)
	}
}

func TestFileCredentialsEncryptKeysAndConfirmationAndSurviveRestart(t *testing.T) {
	store, dir := fileCredentialFixture(t)
	secret := "private-api-key-that-must-never-appear-in-json"
	value := Credential{Key: secret, FreeConfirmed: true}
	if err := store.Save(context.Background(), "groq", value); err != nil {
		t.Fatal(err)
	}
	public, err := json.Marshal(value)
	if err != nil || bytes.Contains(public, []byte(secret)) || bytes.Contains(public, []byte(`"key"`)) {
		t.Fatal("public Credential JSON exposed its key")
	}
	key, err := os.ReadFile(filepath.Join(dir, credentialsKeyFile))
	if err != nil || len(key) != 32 {
		t.Fatalf("private cipher key length=%d err=%v", len(key), err)
	}
	encrypted, err := os.ReadFile(filepath.Join(dir, credentialsDataFile))
	if err != nil || bytes.Contains(encrypted, []byte(secret)) || bytes.Contains(encrypted, []byte("freeConfirmed")) {
		t.Fatal("persisted credential is not encrypted")
	}
	if runtime.GOOS != "windows" {
		for _, entry := range []struct {
			path string
			mode os.FileMode
		}{
			{dir, 0700}, {filepath.Join(dir, credentialsKeyFile), 0600}, {filepath.Join(dir, credentialsDataFile), 0600},
		} {
			info, err := os.Stat(entry.path)
			if err != nil || info.Mode().Perm() != entry.mode {
				t.Fatalf("insecure permissions on credential storage: %v", err)
			}
		}
	}
	restarted, err := NewFileCredentials(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := restarted.Load(context.Background(), "groq")
	if err != nil || loaded != value {
		t.Fatalf("restart did not restore credentials: err=%v", err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 3 {
		t.Fatalf("temporary files survived save: %v %v", files, err)
	}
}

func TestFileCredentialsRefreshBeforeMergeAndBindConfirmationToTheSavedKey(t *testing.T) {
	first, dir := fileCredentialFixture(t)
	second, err := NewFileCredentials(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := first.Save(ctx, "groq", Credential{Key: "first-groq-key", FreeConfirmed: true}); err != nil {
		t.Fatal(err)
	}
	if err := second.Save(ctx, "nvidia", Credential{Key: "nvidia-key"}); err != nil {
		t.Fatal(err)
	}
	if err := second.Save(ctx, "groq", Credential{Key: "replacement-groq-key"}); err != nil {
		t.Fatal(err)
	}
	groq, err := first.Load(ctx, "groq")
	if err != nil || groq.Key != "replacement-groq-key" || groq.FreeConfirmed {
		t.Fatalf("replacement inherited an earlier key's confirmation: %v", err)
	}
	nvidia, err := first.Load(ctx, "nvidia")
	if err != nil || nvidia.Key != "nvidia-key" {
		t.Fatalf("another provider's credential was lost: %v", err)
	}
	groq.FreeConfirmed = true
	if err := first.Save(ctx, "groq", groq); err != nil {
		t.Fatal(err)
	}
	confirmed, err := second.Load(ctx, "groq")
	if err != nil || !confirmed.FreeConfirmed || confirmed.Key != groq.Key {
		t.Fatalf("explicit confirmation was not bound to the current key: %v", err)
	}
}

func TestFileCredentialsRejectCorruptionWithoutReplacingExistingFiles(t *testing.T) {
	for _, corruption := range []string{"tampered", "short-key", "missing-key", "missing-header", "oversized"} {
		t.Run(corruption, func(t *testing.T) {
			store, dir := fileCredentialFixture(t)
			if err := store.Save(context.Background(), "groq", Credential{Key: "private-saved-key"}); err != nil {
				t.Fatal(err)
			}
			keyPath, dataPath := filepath.Join(dir, credentialsKeyFile), filepath.Join(dir, credentialsDataFile)
			data, err := os.ReadFile(dataPath)
			if err != nil {
				t.Fatal(err)
			}
			switch corruption {
			case "tampered":
				data[len(data)-1] ^= 1
			case "short-key":
				if err := os.WriteFile(keyPath, []byte("invalid"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-key":
				if err := os.Remove(keyPath); err != nil {
					t.Fatal(err)
				}
			case "missing-header":
				data[0] ^= 1
			case "oversized":
				data = make([]byte, credentialsLimit+1)
			}
			if err := os.WriteFile(dataPath, data, 0600); err != nil {
				t.Fatal(err)
			}
			beforeKey, beforeKeyErr := os.ReadFile(keyPath)
			if _, err := store.Load(context.Background(), "groq"); err == nil || strings.Contains(err.Error(), "private-saved-key") {
				t.Fatalf("corruption was accepted or exposed a key: %v", err)
			}
			if err := store.Save(context.Background(), "nvidia", Credential{Key: "new-key"}); err == nil {
				t.Fatal("save replaced corrupt credentials")
			}
			after, err := os.ReadFile(dataPath)
			if err != nil || !bytes.Equal(data, after) {
				t.Fatal("failed save replaced existing ciphertext")
			}
			afterKey, afterKeyErr := os.ReadFile(keyPath)
			if !bytes.Equal(beforeKey, afterKey) || os.IsNotExist(beforeKeyErr) != os.IsNotExist(afterKeyErr) {
				t.Fatal("failed save replaced or regenerated the private key")
			}
		})
	}
}

func TestFileCredentialsObserveTheCrossProcessLockAndPreserveOtherVendors(t *testing.T) {
	store, dir := fileCredentialFixture(t)
	ctx := context.Background()
	if err := store.Save(ctx, "groq", Credential{Key: "groq-key"}); err != nil {
		t.Fatal(err)
	}
	release, err := acquireFileLock(filepath.Join(dir, credentialsLockFile))
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewFileCredentials(dir)
	if err != nil {
		release()
		t.Fatal(err)
	}
	err = second.Save(ctx, "nvidia", Credential{Key: "nvidia-key"})
	child := func() error {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		command := exec.Command(executable, "-test.run=^TestFileCredentialsProcessHelper$")
		command.Env = append(os.Environ(), "FREEWAY_TEST_CREDENTIAL_HELPER=1", "FREEWAY_TEST_CREDENTIAL_DIR="+dir)
		return command.Run()
	}
	childErr := child()
	release()
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != "credential_store_busy" {
		t.Fatalf("a concurrent writer was not rejected: %v", err)
	}
	var childExit *exec.ExitError
	if !errors.As(childErr, &childExit) || childExit.ExitCode() != 42 {
		t.Fatalf("another process bypassed the write lock: %v", childErr)
	}
	if err := child(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"groq", "nvidia"} {
		value, err := store.Load(ctx, id)
		if err != nil || value.Key != id+"-key" {
			t.Fatalf("merge lost %s: %v", id, err)
		}
	}
}

func TestFileCredentialsProcessHelper(t *testing.T) {
	if os.Getenv("FREEWAY_TEST_CREDENTIAL_HELPER") != "1" {
		return
	}
	store, err := NewFileCredentials(os.Getenv("FREEWAY_TEST_CREDENTIAL_DIR"))
	if err != nil {
		os.Exit(43)
	}
	err = store.Save(context.Background(), "nvidia", Credential{Key: "nvidia-key"})
	var typed *Error
	if errors.As(err, &typed) && typed.Code == "credential_store_busy" {
		os.Exit(42)
	}
	if err != nil {
		os.Exit(44)
	}
	os.Exit(0)
}

func TestFileCredentialsConcurrentReadsNeverSeePartiallyWrittenCredentials(t *testing.T) {
	store, dir := fileCredentialFixture(t)
	ctx := context.Background()
	if err := store.Save(ctx, "groq", Credential{Key: "key-0", FreeConfirmed: true}); err != nil {
		t.Fatal(err)
	}
	reader, err := NewFileCredentials(dir)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	failures := make(chan error, 2)
	group.Add(2)
	go func() {
		defer group.Done()
		for i := 1; i < 30; i++ {
			if err := store.Save(ctx, "groq", Credential{Key: fmt.Sprintf("key-%d", i), FreeConfirmed: i%2 == 0}); err != nil {
				failures <- err
				return
			}
		}
	}()
	go func() {
		defer group.Done()
		for i := 0; i < 100; i++ {
			value, err := reader.Load(ctx, "groq")
			if err != nil {
				failures <- err
				return
			}
			var index int
			if _, err := fmt.Sscanf(value.Key, "key-%d", &index); err != nil || value.FreeConfirmed != (index%2 == 0) {
				failures <- errors.New("a reader observed an inconsistent key and confirmation")
				return
			}
		}
	}()
	group.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}

func TestFileCredentialsRejectInvalidKeysAndUnsafePermissionsWithoutMutation(t *testing.T) {
	store, dir := fileCredentialFixture(t)
	for _, key := range []string{" key ", "bad\nkey", strings.Repeat("x", 4097), string([]byte{0xff})} {
		if err := store.Save(context.Background(), "groq", Credential{Key: key}); err == nil {
			t.Fatal("invalid key was saved")
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("invalid key created storage files")
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), "groq", Credential{Key: "valid-key"}); err == nil {
		t.Fatal("public credential directory was accepted")
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("existing directory permissions were changed")
	}
}
