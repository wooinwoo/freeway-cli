package filelock

import "errors"

var ErrLocked = errors.New("another process holds the file lock")
