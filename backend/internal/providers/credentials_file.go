package providers

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

const credentialsMagic = "FWPROVIDERS\x01"
const credentialsLimit = 2 * 1024 * 1024
const credentialsKeyFile = "provider-auth.key"
const credentialsDataFile = "provider-auth.enc"
const credentialsLockFile = ".provider-auth.lock"

type fileCredentials struct {
	dir   string
	known map[string]bool
	mu    sync.Mutex
}

// credentialWire is private: the public Credential deliberately never serializes its key.
type credentialWire struct {
	Key           string `json:"key"`
	FreeConfirmed bool   `json:"freeConfirmed"`
}

type credentialsVault struct {
	Version   int                       `json:"version"`
	Providers map[string]credentialWire `json:"providers"`
}

// NewFileCredentials provides optional encrypted API-key persistence for standalone tools.
// Construction and Load never create files; the backend can supply its own CredentialStore.
func NewFileCredentials(dir string) (CredentialStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, failure("credential_directory_required", 400)
	}
	absolute, err := filepath.Abs(dir)
	if err != nil || absolute == filepath.VolumeName(absolute)+string(filepath.Separator) {
		return nil, failure("invalid_credential_directory", 400)
	}
	if home, err := os.UserHomeDir(); err == nil && absolute == filepath.Clean(home) {
		return nil, failure("invalid_credential_directory", 400)
	}
	known := make(map[string]bool)
	for _, definition := range Definitions() {
		known[definition.ID] = true
	}
	return &fileCredentials{dir: absolute, known: known}, nil
}

func (s *fileCredentials) Load(ctx context.Context, id string) (Credential, error) {
	if err := ctx.Err(); err != nil {
		return Credential{}, err
	}
	if !s.known[id] {
		return Credential{}, failure("unknown_provider", 404)
	}
	if err := credentialDirectory(s.dir); errors.Is(err, os.ErrNotExist) {
		return Credential{}, nil
	} else if err != nil {
		return Credential{}, err
	}
	// Readers need no lock: ciphertext is atomically replaced and its key is immutable.
	vault, _, err := s.read()
	if err != nil {
		return Credential{}, err
	}
	if err := ctx.Err(); err != nil {
		return Credential{}, err
	}
	value := vault.Providers[id]
	return Credential{Key: value.Key, FreeConfirmed: value.FreeConfirmed}, nil
}

func (s *fileCredentials) Save(ctx context.Context, id string, value Credential) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.known[id] {
		return failure("unknown_provider", 404)
	}
	if len(value.Key) > 4096 || !utf8.ValidString(value.Key) || strings.IndexFunc(value.Key, unicode.IsControl) >= 0 ||
		(value.Key != "" && strings.TrimSpace(value.Key) != value.Key) {
		return failure("invalid_key", 400)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return failure("credential_store_failed", 500)
	}
	if err := credentialDirectory(s.dir); err != nil {
		return err
	}
	release, err := acquireFileLock(filepath.Join(s.dir, credentialsLockFile))
	if err != nil {
		var typed *Error
		if errors.As(err, &typed) && typed.Code == "installation_in_progress" {
			return failure("credential_store_busy", 409)
		}
		return failure("credential_store_failed", 500)
	}
	defer release()
	vault, key, err := s.read()
	if err != nil {
		return err
	}
	if key == nil {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return failure("credential_store_failed", 500)
		}
		if err := writeCredentialFile(ctx, filepath.Join(s.dir, credentialsKeyFile), key); err != nil {
			return err
		}
	}
	vault.Providers[id] = credentialWire{Key: value.Key, FreeConfirmed: value.FreeConfirmed}
	plaintext, err := json.Marshal(vault)
	if err != nil || len(plaintext) > credentialsLimit-128 {
		return failure("credential_store_failed", 500)
	}
	aead, err := credentialCipher(key)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return failure("credential_store_failed", 500)
	}
	encrypted := append([]byte(credentialsMagic), nonce...)
	encrypted = aead.Seal(encrypted, nonce, plaintext, []byte(credentialsMagic))
	return writeCredentialFile(ctx, filepath.Join(s.dir, credentialsDataFile), encrypted)
}

func (s *fileCredentials) read() (credentialsVault, []byte, error) {
	empty := credentialsVault{Version: 1, Providers: make(map[string]credentialWire)}
	// Read ciphertext first: seeing a newly published file guarantees its key was already published.
	encrypted, dataErr := readCredentialFile(filepath.Join(s.dir, credentialsDataFile), credentialsLimit)
	key, keyErr := readCredentialFile(filepath.Join(s.dir, credentialsKeyFile), 32)
	if errors.Is(dataErr, os.ErrNotExist) && errors.Is(keyErr, os.ErrNotExist) {
		return empty, nil, nil
	}
	if keyErr != nil || len(key) != 32 {
		return empty, nil, failure("credential_store_corrupt", 500)
	}
	if errors.Is(dataErr, os.ErrNotExist) {
		return empty, key, nil
	}
	if dataErr != nil {
		return empty, nil, dataErr
	}
	aead, err := credentialCipher(key)
	if err != nil {
		return empty, nil, err
	}
	if !bytes.HasPrefix(encrypted, []byte(credentialsMagic)) || len(encrypted) < len(credentialsMagic)+aead.NonceSize()+aead.Overhead() {
		return empty, nil, failure("credential_store_corrupt", 500)
	}
	nonce := encrypted[len(credentialsMagic) : len(credentialsMagic)+aead.NonceSize()]
	plaintext, err := aead.Open(nil, nonce, encrypted[len(credentialsMagic)+aead.NonceSize():], []byte(credentialsMagic))
	if err != nil {
		return empty, nil, failure("credential_store_corrupt", 500)
	}
	var vault credentialsVault
	if json.Unmarshal(plaintext, &vault) != nil || vault.Version != 1 || vault.Providers == nil {
		return empty, nil, failure("credential_store_corrupt", 500)
	}
	return vault, key, nil
}

func credentialCipher(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, failure("credential_store_corrupt", 500)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, failure("credential_store_failed", 500)
	}
	return aead, nil
}

func credentialDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.ErrNotExist
	}
	if err != nil {
		return failure("credential_store_failed", 500)
	}
	if !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return failure("insecure_credential_directory", 500)
	}
	return nil
}

func readCredentialFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, failure("credential_store_failed", 500)
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return nil, failure("insecure_credential_permissions", 500)
	}
	file, err := openCredentialRead(path)
	if err != nil {
		return nil, failure("credential_store_failed", 500)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, failure("credential_store_failed", 500)
	}
	if int64(len(data)) > limit {
		return nil, failure("credential_store_corrupt", 500)
	}
	return data, nil
}

func writeCredentialFile(ctx context.Context, path string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".provider-auth-*")
	if err != nil {
		return failure("credential_store_failed", 500)
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if file.Chmod(0600) != nil {
		return failure("credential_store_failed", 500)
	}
	if _, err := file.Write(data); err != nil {
		return failure("credential_store_failed", 500)
	}
	if file.Sync() != nil || file.Close() != nil {
		return failure("credential_store_failed", 500)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := replaceCredentialFile(ctx, file.Name(), path); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		directory, err := os.Open(filepath.Dir(path))
		if err != nil {
			return failure("credential_store_failed", 500)
		}
		defer directory.Close()
		if directory.Sync() != nil {
			return failure("credential_store_failed", 500)
		}
	}
	return nil
}

func replaceCredentialFile(ctx context.Context, source, target string) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := os.Rename(source, target)
		if err == nil {
			return nil
		}
		// Windows readers briefly deny file replacement; retry without weakening atomic publication.
		if runtime.GOOS != "windows" || attempt >= 25 || (!errors.Is(err, syscall.Errno(32)) && !errors.Is(err, syscall.Errno(5))) {
			return failure("credential_store_failed", 500)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
