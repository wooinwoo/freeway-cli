// SPDX-License-Identifier: MIT

package imagegen

import (
	"errors"
	"slices"
	"unsafe"

	"golang.org/x/sys/windows"
)

func protectOAuthKey(key []byte) ([]byte, error) {
	input := windows.DataBlob{Size: uint32(len(key)), Data: unsafe.SliceData(key)}
	var output windows.DataBlob
	if err := windows.CryptProtectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	return copyProtectedKey(output)
}

func unprotectOAuthKey(key []byte) ([]byte, error) {
	input := windows.DataBlob{Size: uint32(len(key)), Data: unsafe.SliceData(key)}
	var output windows.DataBlob
	if err := windows.CryptUnprotectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	return copyProtectedKey(output)
}

func copyProtectedKey(output windows.DataBlob) ([]byte, error) {
	if output.Data == nil {
		return nil, errors.New("empty Windows image login key")
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	if output.Size == 0 || output.Size > 4096 {
		return nil, errors.New("invalid Windows image login key")
	}
	return slices.Clone(unsafe.Slice(output.Data, output.Size)), nil
}
