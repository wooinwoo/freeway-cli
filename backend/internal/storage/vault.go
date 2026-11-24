// SPDX-License-Identifier: MIT

// Package storage owns the gateway data directory and its encrypted records.
package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	keyName       = "state.key"
	clientKeyName = "client.key"
)

var ErrLocked = errors.New("another gateway owns this data directory")

type Vault struct {
	root *os.Root
	lock *os.File
	aead cipher.AEAD
}

// Open takes the directory lock before reading or creating persistent state.
func Open(path string) (*Vault, error) {
	if path == "" {
		return nil, errors.New("gateway data directory is required")
	}
	if err := mkdirPrivate(path); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	v := &Vault{root: root}
	if err := v.initialize(); err != nil {
		_ = v.Close()
		return nil, err
	}
	return v, nil
}

func (v *Vault) initialize() error {
	info, err := v.root.Stat(".")
	if err != nil {
		return err
	}
	if err := requirePrivate(v.root, ".", info); err != nil {
		return fmt.Errorf("%s: %w", v.root.Name(), err)
	}
	v.lock, err = v.openLock()
	if err != nil {
		return err
	}
	if err := lockExclusive(v.lock); err != nil {
		return err
	}
	key, err := v.read(keyName, 32)
	if errors.Is(err, os.ErrNotExist) {
		names, err := v.Names("")
		if err != nil {
			return err
		}
		if len(names) > 0 {
			return errors.New("gateway encryption key is missing; restore it from backup")
		}
		key = make([]byte, 32)
		rand.Read(key)
		if err := v.write(keyName, key); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if len(key) != 32 {
		return errors.New("invalid gateway encryption key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	v.aead, err = cipher.NewGCMWithRandomNonce(block)
	return err
}

func (v *Vault) openLock() (*os.File, error) {
	const name = ".lock"
	err := v.checkFile(name)
	if errors.Is(err, os.ErrNotExist) {
		// On macOS, competing create-or-open calls can return ENOENT.
		// Create exclusively, then validate and open a competing creator's file.
		file, createErr := v.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if !errors.Is(createErr, os.ErrExist) {
			return file, createErr
		}
		err = v.checkFile(name)
	}
	if err != nil {
		return nil, err
	}
	return v.root.OpenFile(name, os.O_RDWR, 0)
}

func (v *Vault) Close() error {
	var lockError error
	if v.lock != nil {
		lockError = v.lock.Close()
	}
	return errors.Join(v.root.Close(), lockError)
}

// ClientKey creates a local authentication secret under the vault's writer lock.
func (v *Vault) ClientKey() (string, error) {
	key, err := v.read(clientKeyName, 128)
	if !errors.Is(err, os.ErrNotExist) {
		return string(key), err
	}
	created := "fw_" + rand.Text()
	return created, v.write(clientKeyName, []byte(created))
}

// ReadClientKey reads the immutable client secret without taking the writer lock.
func ReadClientKey(path string) (string, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return "", err
	}
	defer root.Close()
	v := &Vault{root: root}
	data, err := v.read(clientKeyName, 128)
	return string(data), err
}

// Names returns encrypted record names with the given prefix.
func (v *Vault) Names(prefix string) ([]string, error) {
	dir, err := v.root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	var matches []string
	for _, name := range names {
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".enc") {
			matches = append(matches, name)
		}
	}
	return matches, nil
}

func (v *Vault) Read(name string, limit int64) ([]byte, error) {
	if !recordName(name) {
		return nil, errors.New("invalid gateway record name")
	}
	data, err := v.read(name, limit+int64(v.aead.Overhead()))
	if err != nil {
		return nil, err
	}
	plain, err := v.aead.Open(data[:0], nil, data, associatedData(name))
	if err != nil {
		return nil, errors.New("gateway record cannot be decrypted")
	}
	return plain, nil
}

func (v *Vault) Write(name string, data []byte) error {
	if !recordName(name) {
		return errors.New("invalid gateway record name")
	}
	return v.write(name, v.aead.Seal(nil, nil, data, associatedData(name)))
}

func recordName(name string) bool {
	return strings.HasSuffix(name, ".enc") && filepath.IsLocal(name) && !strings.ContainsAny(name, "/\\:")
}

func associatedData(name string) []byte { return []byte("freeway/state/v1/" + name) }

func (v *Vault) checkFile(name string) error {
	info, err := v.root.Lstat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("gateway state file %q must be private and regular", name)
	}
	if err := requirePrivate(v.root, name, info); err != nil {
		return fmt.Errorf("%s: %w", filepath.Join(v.root.Name(), name), err)
	}
	return nil
}

func (v *Vault) read(name string, limit int64) ([]byte, error) {
	if err := v.checkFile(name); err != nil {
		return nil, err
	}
	file, err := v.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("gateway state file %q exceeds its size limit", name)
	}
	return data, nil
}

func (v *Vault) write(name string, data []byte) error {
	if err := v.checkFile(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temp := ".write-" + rand.Text()
	file, err := v.root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer v.root.Remove(temp)
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := v.root.Rename(temp, name); err != nil {
		return err
	}
	// Rename publishes the new record. A failed directory sync cannot undo it.
	if runtime.GOOS != "windows" && v.syncDirectory() != nil {
		slog.Warn("gateway record committed, but directory sync failed")
	}
	return nil
}

func (v *Vault) syncDirectory() error {
	dir, err := v.root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
