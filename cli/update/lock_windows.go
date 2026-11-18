package update

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var lockFile = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")

func lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	var overlapped syscall.Overlapped
	result, _, _ := lockFile.Call(f.Fd(), 3, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if result == 0 {
		f.Close()
		return nil, fmt.Errorf("another installation is in progress")
	}
	return func() { _ = f.Close() }, nil
}
