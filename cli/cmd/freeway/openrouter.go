// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/tui"
)

const openRouterExchangeURL = "https://openrouter.ai/api/v1/auth/keys"

type openRouterGrant struct {
	verifier, state string
}

func newOpenRouterGrant() openRouterGrant {
	secret := make([]byte, 32)
	rand.Read(secret)
	return openRouterGrant{verifier: base64.RawURLEncoding.EncodeToString(secret), state: rand.Text()}
}

func (g openRouterGrant) authorizationURL(callback string) string {
	hash := sha256.Sum256([]byte(g.verifier))
	query := url.Values{
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(hash[:])},
		"code_challenge_method": {"S256"},
		"key_label":             {"Freeway"},
	}
	if callback != "" {
		query.Set("state", g.state)
		query.Set("callback_url", callback)
	}
	return "https://openrouter.ai/auth?" + query.Encode()
}

type browserCallback struct {
	url      string
	finished chan struct{}
	code     string
	denied   bool
	issuer   string
	state    atomic.Value
	server   *http.Server
}

func startOpenRouterCallback(state string, prefs tui.Preferences) (*browserCallback, error) {
	return startBrowserCallback("localhost", state, state, true, prefs)
}

func startBrowserCallback(hostname, pathSecret, expectedState string, allowUnstatedDenial bool, prefs tui.Preferences) (*browserCallback, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	host := fmt.Sprintf("%s:%d", hostname, listener.Addr().(*net.TCPAddr).Port)
	path := "/freeway/" + pathSecret
	callback := &browserCallback{url: "http://" + host + path, finished: make(chan struct{})}
	callback.state.Store(expectedState)
	var received atomic.Bool
	callback.server = &http.Server{
		ReadHeaderTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, MaxHeaderBytes: 8 << 10,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			if r.Method != http.MethodGet || r.Host != host || r.URL.Path != path {
				http.NotFound(w, r)
				return
			}
			query, err := url.ParseQuery(r.URL.RawQuery)
			code := query.Get("code")
			var denied bool
			state := callback.state.Load().(string)
			stateMatches := state != "" && len(query["state"]) == 1 && subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(state)) == 1
			if err == nil && query.Get("error") == "access_denied" && len(query["code"]) == 0 {
				// OpenRouter omits state on denial. The callback path is unique and unguessable.
				denied = stateMatches || allowUnstatedDenial && len(query["state"]) == 0
			}
			if !denied && (err != nil || !stateMatches || len(query["code"]) != 1 || len(query["iss"]) > 1 || query.Has("error") || !validOpenRouterToken(code, 2048)) {
				http.Error(w, prefs.Text("Invalid browser callback.", "브라우저 승인 응답을 확인할 수 없습니다."), http.StatusBadRequest)
				return
			}
			if !received.CompareAndSwap(false, true) {
				http.Error(w, prefs.Text("This approval was already handled.", "이미 처리된 승인입니다."), http.StatusConflict)
				return
			}
			callback.code, callback.denied, callback.issuer = code, denied, query.Get("iss")
			message := prefs.Text("Approval received. Return to Freeway to finish connecting.", "승인을 받았습니다. Freeway로 돌아가 연결을 마치세요.")
			if denied {
				message = prefs.Text("Approval cancelled. Return to Freeway.", "승인이 취소됐습니다. Freeway로 돌아가세요.")
			}
			fmt.Fprintln(w, message)
			_ = http.NewResponseController(w).Flush()
			close(callback.finished)
		}),
	}
	go func() { _ = callback.server.Serve(listener) }()
	return callback, nil
}

func (c *browserCallback) close() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.server.Shutdown(ctx); err != nil {
		_ = c.server.Close()
	}
}

func validOpenRouterToken(value string, limit int) bool {
	return value != "" && len(value) <= limit && strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r)
	}) < 0
}

func exchangeOpenRouterCode(ctx context.Context, endpoint string, httpClient *http.Client, code, verifier string) (string, error) {
	if !validOpenRouterToken(code, 2048) {
		return "", errors.New("invalid OpenRouter authorization code")
	}
	body, err := json.Marshal(map[string]string{
		"code": code, "code_verifier": verifier, "code_challenge_method": "S256",
	})
	if err != nil {
		return "", errors.New("could not prepare OpenRouter authorization")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", errors.New("could not prepare OpenRouter authorization")
	}
	request.Header.Set("Content-Type", "application/json")
	client := *httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	reply, err := client.Do(request)
	if err != nil {
		return "", errors.New("could not reach OpenRouter")
	}
	defer reply.Body.Close()
	if reply.StatusCode != http.StatusOK {
		return "", errors.New("OpenRouter rejected the authorization code")
	}
	var result struct {
		Key string `json:"key"`
	}
	if json.NewDecoder(io.LimitReader(reply.Body, 16<<10)).Decode(&result) != nil || !validOpenRouterToken(result.Key, 4096) {
		return "", errors.New("OpenRouter did not return a usable key")
	}
	return result.Key, nil
}

func loginOpenRouter(ctx context.Context, prefs tui.Preferences, options []tea.ProgramOption, manual bool) (string, error) {
	t := prefs.Text
	grant := newOpenRouterGrant()
	var code string
	if !manual {
		callback, err := startOpenRouterCallback(grant.state, prefs)
		if err == nil {
			waitCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
			address := grant.authorizationURL(callback.url)
			fmt.Fprintln(os.Stderr, t("Opening OpenRouter in your browser…", "브라우저에서 OpenRouter를 여는 중입니다…"))
			if err := openProviderURL(waitCtx, address); err != nil {
				fmt.Fprintln(os.Stderr, address)
			}
			dialog := tui.SetupDialog{
				Title:  t("Approve Freeway in OpenRouter", "OpenRouter에서 Freeway 승인"),
				Detail: t("Freeway uses the new key for eligible free models. Return here after approving.", "Freeway는 새 키로 무료 모델을 이용합니다. 브라우저에서 승인한 뒤 돌아오세요."),
				Options: []tui.SetupOption{
					{ID: "manual", Label: t("Use an authorization code instead", "인증 코드로 연결")},
					{ID: "cancel", Label: t("Cancel login", "로그인 취소")},
				},
				Wait: func() string {
					select {
					case <-callback.finished:
						return "finished"
					case <-waitCtx.Done():
						return "timeout"
					}
				},
			}
			choice, runErr := dialog.Run(prefs, options...)
			cancel()
			callback.close()
			if runErr != nil {
				return "", runErr
			}
			switch choice {
			case "finished":
				if callback.denied {
					return "", nil
				}
				code = callback.code
			case "manual":
				manual = true
				grant = newOpenRouterGrant()
			case "timeout":
				return "", context.DeadlineExceeded
			default:
				return "", nil
			}
		} else {
			manual = true
		}
	}
	if manual {
		address := grant.authorizationURL("")
		fmt.Fprintln(os.Stderr, t("Open this address if the browser does not open.", "브라우저가 열리지 않으면 이 주소를 여세요."))
		fmt.Fprintln(os.Stderr, address)
		_ = openProviderURL(ctx, address)
		dialog := tui.SetupDialog{
			Title:  t("Enter the OpenRouter authorization code", "OpenRouter 인증 코드 입력"),
			Detail: t("Copy the code shown after approving Freeway.", "OpenRouter에서 승인한 뒤 표시된 코드를 붙여넣으세요."),
			Secret: true, Input: t("Paste the authorization code", "인증 코드 붙여넣기"),
		}
		var err error
		code, err = dialog.Run(prefs, options...)
		if err != nil || code == "" {
			return "", err
		}
	}
	verify, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return exchangeOpenRouterCode(verify, openRouterExchangeURL, http.DefaultClient, code, grant.verifier)
}
