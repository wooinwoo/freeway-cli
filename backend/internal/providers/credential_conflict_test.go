package providers

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

type auditBlockedCredentialStore struct {
	CredentialStore
	loaded chan struct{}
	resume chan struct{}
	once   sync.Once
}

func (s *auditBlockedCredentialStore) CompareAndSwap(ctx context.Context, id string, previous, value Credential) error {
	return s.CredentialStore.(interface {
		CompareAndSwap(context.Context, string, Credential, Credential) error
	}).CompareAndSwap(ctx, id, previous, value)
}

func (s *auditBlockedCredentialStore) Load(ctx context.Context, id string) (Credential, error) {
	value, err := s.CredentialStore.Load(ctx, id)
	s.once.Do(func() {
		close(s.loaded)
		<-s.resume
	})
	return value, err
}

func TestConcurrentConfirmationPreservesReplacementKey(t *testing.T) {
	ctx := context.Background()
	store, err := NewFileCredentials(filepath.Join(t.TempDir(), "auth"))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Save(ctx, "groq", Credential{Key: "old-fixture-key"}); err != nil {
		t.Fatal(err)
	}
	blocked := &auditBlockedCredentialStore{CredentialStore: store, loaded: make(chan struct{}), resume: make(chan struct{})}
	first, err := New(Config{Credentials: blocked})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := New(Config{Credentials: store})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	done := make(chan error, 1)
	go func() { _, err := first.Authenticate(ctx, "groq", AuthOptions{ConfirmFree: true}); done <- err }()
	<-blocked.loaded
	if _, err = second.Authenticate(ctx, "groq", AuthOptions{Key: "new-fixture-key"}); err != nil {
		t.Fatal(err)
	}
	close(blocked.resume)
	err = <-done
	var conflict *Error
	if !errors.As(err, &conflict) || conflict.Code != "credential_conflict" || conflict.Status != 409 {
		t.Fatalf("wanted conflict, got %v", err)
	}
	value, err := store.Load(ctx, "groq")
	if err != nil {
		t.Fatal(err)
	}
	if value.Key != "new-fixture-key" || value.FreeConfirmed {
		t.Fatal("stale confirmation replaced or confirmed the newer key")
	}
	t.Log("VERIFIED: stale confirmation returns credential_conflict/409 and newer key remains unconfirmed")
}

func TestEnvironmentSnapshotAndMemoryCredentialCompareAndSwap(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "environment-fixture-key")
	for _, kind := range []string{"file", "memory"} {
		t.Run(kind, func(t *testing.T) {
			var store CredentialStore = &memoryCredentials{values: map[string]Credential{}}
			if kind == "file" {
				var err error
				store, err = NewFileCredentials(filepath.Join(t.TempDir(), "auth"))
				if err != nil {
					t.Fatal(err)
				}
			}
			client, err := New(Config{Credentials: store})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if auth, err := client.Authenticate(context.Background(), "groq", AuthOptions{ConfirmFree: true}); err != nil || !auth.Ready {
				t.Fatalf("environment snapshot: %v", err)
			}
			value, err := store.Load(context.Background(), "groq")
			if err != nil || value.Key != "environment-fixture-key" || !value.FreeConfirmed {
				t.Fatal("environment credential was not persisted with confirmation")
			}
			cas := store.(interface {
				CompareAndSwap(context.Context, string, Credential, Credential) error
			})
			if err := cas.CompareAndSwap(context.Background(), "groq", Credential{}, Credential{Key: "must-not-be-saved"}); err == nil {
				t.Fatal("stale empty snapshot replaced current credential")
			}
		})
	}
}
