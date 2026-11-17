package providers

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var installLockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")

func acquireInstallLock(root string) (func(), error) {
	return acquireFileLock(filepath.Join(root, ".native-install.lock"))
}

func acquireFileLock(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	var overlapped syscall.Overlapped
	result, _, _ := installLockFileEx.Call(file.Fd(), 3, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if result == 0 {
		file.Close()
		return nil, failure("installation_in_progress", 409)
	}
	return func() { _ = file.Close() }, nil
}
