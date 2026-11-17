//go:build !windows

package providers

import "os"

func openCredentialRead(path string) (*os.File, error) { return os.Open(path) }
