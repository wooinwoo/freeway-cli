//go:build !windows

package providers

import (
	"os"
	"path/filepath"
	"syscall"
)

func acquireInstallLock(root string) (func(), error) {
	return acquireFileLock(filepath.Join(root, ".native-install.lock"))
}

func acquireFileLock(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, failure("installation_in_progress", 409)
	}
	return func() { _ = file.Close() }, nil
}
