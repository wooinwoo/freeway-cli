//go:build linux || darwin

// SPDX-License-Identifier: MIT

package storage

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockExclusive(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return ErrLocked
	}
	return err
}

func mkdirPrivate(path string) error { return os.MkdirAll(path, 0700) }

func requirePrivate(_ *os.Root, _ string, info os.FileInfo) error {
	if info.Mode().Perm()&0077 != 0 {
		return errors.New("gateway data must use owner-only permissions (0700 directories, 0600 files)")
	}
	return nil
}
