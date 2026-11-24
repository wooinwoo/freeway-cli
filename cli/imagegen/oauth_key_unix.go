//go:build !windows

// SPDX-License-Identifier: MIT

package imagegen

func protectOAuthKey(key []byte) ([]byte, error)   { return key, nil }
func unprotectOAuthKey(key []byte) ([]byte, error) { return key, nil }
