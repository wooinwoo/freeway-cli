// SPDX-License-Identifier: MIT

// Package auth verifies gateway client credentials without exposing the secret.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"strings"
)

type ClientKey struct {
	digest [sha256.Size]byte
}

func NewClientKey(key string) (ClientKey, error) {
	if !valid(key) {
		return ClientKey{}, errors.New("invalid gateway client key")
	}
	return ClientKey{digest: sha256.Sum256([]byte(key))}, nil
}

func (k ClientKey) Matches(header string) bool {
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || !valid(token) {
		return false
	}
	provided := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(k.digest[:], provided[:]) == 1
}

func valid(key string) bool {
	if len(key) < 20 || len(key) > 128 {
		return false
	}
	for _, ch := range key {
		if ch <= ' ' || ch > '~' || ch == ',' {
			return false
		}
	}
	return true
}
