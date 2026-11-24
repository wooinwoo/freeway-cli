// SPDX-License-Identifier: MIT

package imagegen

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var ErrBrowserLoginRequired = errors.New("the image MCP browser login is missing or expired")

type OAuthGrant struct {
	endpoint, token string
	expires         time.Time
	refresh         *oauthRefresh
}

type storedOAuth struct {
	Token   string        `json:"access_token"`
	Expires time.Time     `json:"expiry"`
	Refresh *oauthRefresh `json:"refresh,omitempty"`
}

const oauthGrantLimit = 128 << 10

func validOAuthID(id string) bool {
	return len(id) == 26 && strings.IndexFunc(id, func(r rune) bool {
		return !(r >= 'A' && r <= 'Z' || r >= '2' && r <= '7')
	}) == -1
}

func oauthAAD(route Route) []byte {
	return []byte("freeway/image-oauth/1\x00" + route.Endpoint + "\x00" + route.OAuthID)
}

func oauthRoot(dir string, create bool) (*os.Root, error) {
	path := filepath.Join(dir, "image-mcp-auth")
	if create {
		if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("image login directory must be private")
	}
	return os.OpenRoot(path)
}

func readOAuthFile(root *os.Root, name string, limit int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("image login files must be private and regular")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("invalid image login file")
	}
	return data, nil
}

func writeOAuthFile(root *os.Root, name string, data []byte) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = root.Remove(name)
	}
	return err
}

func oauthCipher(root *os.Root, create bool) (cipher.AEAD, error) {
	key, err := readOAuthFile(root, "key", 4096)
	if create && errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		rand.Read(key)
		protected, protectErr := protectOAuthKey(key)
		if protectErr != nil {
			return nil, protectErr
		}
		err = writeOAuthFile(root, "key", protected)
	} else if err == nil {
		key, err = unprotectOAuthKey(key)
	}
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("invalid image login encryption key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func saveOAuthGrant(dir string, route Route, grant *OAuthGrant) error {
	root, err := oauthRoot(dir, true)
	if err != nil {
		return err
	}
	defer root.Close()
	aead, err := oauthCipher(root, true)
	if err != nil {
		return err
	}
	return writeOAuthGrant(root, aead, route, route.OAuthID+".enc", storedOAuth{Token: grant.token, Expires: grant.expires, Refresh: grant.refresh})
}

func writeOAuthGrant(root *os.Root, aead cipher.AEAD, route Route, name string, grant storedOAuth) error {
	data, err := json.Marshal(grant)
	if err != nil {
		return err
	}
	if len(data)+aead.Overhead() > oauthGrantLimit {
		return errors.New("image login exceeds the local storage limit")
	}
	return writeOAuthFile(root, name, aead.Seal(nil, nil, data, oauthAAD(route)))
}

func removeOAuthGrant(dir, id string) {
	if !validOAuthID(id) {
		return
	}
	if root, err := oauthRoot(dir, false); err == nil {
		defer root.Close()
		_ = root.Remove(id + ".enc")
		_ = root.Remove(id + ".lock")
	}
}

func (r Route) readOAuthGrant(root *os.Root, aead cipher.AEAD) (storedOAuth, error) {
	data, err := readOAuthFile(root, r.OAuthID+".enc", oauthGrantLimit)
	if err != nil {
		return storedOAuth{}, ErrBrowserLoginRequired
	}
	plain, err := aead.Open(nil, nil, data, oauthAAD(r))
	var grant storedOAuth
	if err != nil || json.Unmarshal(plain, &grant) != nil || !validBearerToken(grant.Token) {
		return storedOAuth{}, ErrBrowserLoginRequired
	}
	return grant, nil
}
