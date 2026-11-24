// SPDX-License-Identifier: MIT

package imagegen

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/wooinwoo/freeway-cli/cli/internal/filelock"
)

var ErrBrowserRefreshUnavailable = errors.New("could not renew the image MCP browser login")

type oauthRefresh struct {
	Token    string `json:"token"`
	URL      string `json:"token_url"`
	ClientID string `json:"client_id"`
}

func (grant storedOAuth) usable(rejected string) bool {
	return grant.Token != rejected && (grant.Expires.IsZero() || time.Now().Before(grant.Expires))
}

func (r Route) oauthToken(ctx context.Context, network http.RoundTripper, rejected string) (string, error) {
	if !validOAuthID(r.OAuthID) {
		return "", ErrBrowserLoginRequired
	}
	dir, err := configDir()
	if err != nil {
		return "", ErrBrowserLoginRequired
	}
	root, err := oauthRoot(dir, false)
	if err != nil {
		return "", ErrBrowserLoginRequired
	}
	defer root.Close()
	aead, err := oauthCipher(root, false)
	if err != nil {
		return "", ErrBrowserLoginRequired
	}
	grant, err := r.readOAuthGrant(root, aead)
	if err != nil || grant.usable(rejected) {
		return grant.Token, err
	}
	if grant.Refresh == nil {
		return "", ErrBrowserLoginRequired
	}

	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	lockName := r.OAuthID + ".lock"
	release, err := lockOAuthRefresh(ctx, filepath.Join(dir, "image-mcp-auth", lockName))
	if err != nil {
		return "", err
	}
	defer func() {
		release()
		if _, err := root.Stat(r.OAuthID + ".enc"); errors.Is(err, os.ErrNotExist) {
			_ = root.Remove(lockName)
		}
	}()
	grant, err = r.readOAuthGrant(root, aead)
	if err != nil || grant.usable(rejected) {
		return grant.Token, err
	}
	grant, err = r.refreshOAuthGrant(ctx, network, grant)
	if err != nil {
		return "", err
	}
	temporary := ".refresh-" + rand.Text()
	if err := writeOAuthGrant(root, aead, r, temporary, grant); err != nil {
		return "", ErrBrowserRefreshUnavailable
	}
	defer root.Remove(temporary)
	// Hold the settings lock only while publishing so removal can proceed during renewal.
	// Once the server rotates the token, finish saving it even if the caller cancels.
	publish, finish := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer finish()
	releaseConfig, err := lockOAuthRefresh(publish, filepath.Join(dir, mcpConfigLock))
	if err != nil {
		return "", err
	}
	defer releaseConfig()
	matches := func(route Route) bool {
		return route.OAuthID == r.OAuthID && route.Endpoint == r.Endpoint
	}
	image, imageErr := Load()
	servers, serverErr := LoadMCPServers()
	if !(imageErr == nil && slices.ContainsFunc(image.Routes(), matches)) && !(serverErr == nil && slices.ContainsFunc(servers, matches)) {
		return "", ErrBrowserLoginRequired
	}
	if err := root.Rename(temporary, r.OAuthID+".enc"); err != nil {
		return "", ErrBrowserRefreshUnavailable
	}
	return grant.Token, nil
}

func lockOAuthRefresh(ctx context.Context, path string) (func(), error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		release, err := filelock.Try(path)
		if err == nil {
			return release, nil
		}
		if !errors.Is(err, filelock.ErrLocked) {
			return nil, ErrBrowserRefreshUnavailable
		}
		select {
		case <-ctx.Done():
			return nil, ErrBrowserRefreshUnavailable
		case <-ticker.C:
		}
	}
}

func (r Route) refreshOAuthGrant(ctx context.Context, network http.RoundTripper, grant storedOAuth) (storedOAuth, error) {
	refresh := grant.Refresh
	if refresh == nil || !validBearerToken(refresh.Token) || refresh.ClientID == "" {
		return storedOAuth{}, ErrBrowserLoginRequired
	}
	endpoint, err := url.Parse(refresh.URL)
	if err != nil || !validOAuthURL(endpoint) {
		return storedOAuth{}, ErrBrowserLoginRequired
	}
	values := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh.Token}, "client_id": {refresh.ClientID}, "resource": {r.Endpoint}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(values.Encode()))
	if err != nil {
		return storedOAuth{}, ErrBrowserLoginRequired
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Transport: network, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return storedOAuth{}, ErrBrowserRefreshUnavailable
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(http.MaxBytesReader(nil, resp.Body, oauthGrantLimit))
	var reply struct {
		Error        string      `json:"error"`
		AccessToken  string      `json:"access_token"`
		RefreshToken string      `json:"refresh_token"`
		TokenType    string      `json:"token_type"`
		ExpiresIn    json.Number `json:"expires_in"`
	}
	if err != nil || json.Unmarshal(data, &reply) != nil {
		return storedOAuth{}, ErrBrowserRefreshUnavailable
	}
	if (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized) &&
		(reply.Error == "invalid_grant" || reply.Error == "invalid_client") {
		return storedOAuth{}, ErrBrowserLoginRequired
	}
	if resp.StatusCode != http.StatusOK || reply.Error != "" || !validBearerToken(reply.AccessToken) || !strings.EqualFold(reply.TokenType, "Bearer") {
		return storedOAuth{}, ErrBrowserRefreshUnavailable
	}
	grant.Token, grant.Expires = reply.AccessToken, time.Time{}
	if reply.ExpiresIn != "" {
		seconds, err := reply.ExpiresIn.Int64()
		if err != nil || seconds <= 0 || seconds > int64((1<<63-1)/time.Second) {
			return storedOAuth{}, ErrBrowserRefreshUnavailable
		}
		grant.Expires = time.Now().Add(time.Duration(seconds) * time.Second)
	}
	if reply.RefreshToken != "" {
		if !validBearerToken(reply.RefreshToken) {
			return storedOAuth{}, ErrBrowserRefreshUnavailable
		}
		grant.Refresh.Token = reply.RefreshToken
	}
	return grant, nil
}
