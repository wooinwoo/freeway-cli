package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/wooinwoo/freeway-cli/cli/tui"
)

func TestOpenRouterPKCEURLsKeepVerifierPrivate(t *testing.T) {
	grant := newOpenRouterGrant()
	callback := "http://localhost:12345/freeway/" + grant.state
	for _, target := range []string{callback, ""} {
		address, err := url.Parse(grant.authorizationURL(target))
		if err != nil {
			t.Fatal(err)
		}
		query := address.Query()
		hash := sha256.Sum256([]byte(grant.verifier))
		if address.Host != "openrouter.ai" || address.Scheme != "https" || address.Path != "/auth" ||
			query.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(hash[:]) ||
			query.Get("code_challenge_method") != "S256" || strings.Contains(address.String(), grant.verifier) {
			t.Fatal("invalid PKCE authorization URL")
		}
		if query.Get("callback_url") != target || (query.Get("state") == grant.state) != (target != "") {
			t.Fatal("callback or headless authorization parameters are wrong")
		}
	}
}

func TestOpenRouterCallbackChecksHostAndStateBeforeAccepting(t *testing.T) {
	grant := newOpenRouterGrant()
	callback, err := startOpenRouterCallback(grant.state, tui.Preferences{})
	if err != nil {
		t.Fatal(err)
	}
	defer callback.close()
	address, err := url.Parse(callback.url)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{}}
	defer client.CloseIdleConnections()
	send := func(host, query string) int {
		t.Helper()
		endpoint := "http://" + net.JoinHostPort("127.0.0.1", address.Port()) + address.Path + "?" + query
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		result, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer result.Body.Close()
		return result.StatusCode
	}
	valid := "code=fixture-code&state=" + grant.state
	if send("external.example", valid) != 404 || send(address.Host, "code=fixture-code&state=incorrect") != 400 ||
		send(address.Host, valid+"&code=second") != 400 {
		t.Fatal("untrusted callback was accepted")
	}
	select {
	case <-callback.finished:
		t.Fatal("invalid callbacks finished approval")
	default:
	}
	if send(address.Host, valid) != 200 || send(address.Host, valid) != 409 {
		t.Fatal("the callback should accept a valid grant once")
	}
	select {
	case <-callback.finished:
		if callback.code != "fixture-code" || callback.denied {
			t.Fatal("incorrect callback result")
		}
	case <-time.After(time.Second):
		t.Fatal("valid callback did not finish")
	}
}

func TestImageBrowserCallbackRequiresStateAndKeepsIssuer(t *testing.T) {
	callback, err := startBrowserCallback("127.0.0.1", "fixture-private-path", "", false, tui.Preferences{})
	if err != nil {
		t.Fatal(err)
	}
	defer callback.close()
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{}}
	defer client.CloseIdleConnections()
	get := func(query string) int {
		t.Helper()
		response, err := client.Get(callback.url + "?" + query)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		return response.StatusCode
	}
	if get("code=fixture-code&state=before-approval") != 400 || get("error=access_denied") != 400 {
		t.Fatal("a callback was accepted before the browser opener approved the state")
	}
	callback.state.Store("expected-state")
	if get("error=access_denied") != 400 || get("code=fixture-code&state=expected-state&iss=first&iss=second") != 400 {
		t.Fatal("a missing state or ambiguous issuer was accepted")
	}
	if get("code=fixture-code&state=expected-state&iss=https%3A%2F%2Flogin.fixture.test") != 200 {
		t.Fatal("valid OAuth callback was refused")
	}
	<-callback.finished
	if callback.code != "fixture-code" || callback.issuer != "https://login.fixture.test" || callback.denied {
		t.Fatal("the accepted browser callback lost the code or issuer")
	}
}

func TestOpenRouterExchangeNeverFollowsRedirect(t *testing.T) {
	grant := newOpenRouterGrant()
	redirected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("authorization data followed a redirect")
		w.WriteHeader(200)
	}))
	defer redirected.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || r.Method != http.MethodPost ||
			body["code"] != "fixture-code" || body["code_verifier"] != grant.verifier || body["code_challenge_method"] != "S256" {
			t.Error("invalid code exchange request")
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, redirected.URL, http.StatusTemporaryRedirect)
			return
		}
		_, _ = w.Write([]byte(`{"key":"fixture-key"}`))
	}))
	defer server.Close()
	key, err := exchangeOpenRouterCode(t.Context(), server.URL, server.Client(), "fixture-code", grant.verifier)
	if err != nil || key != "fixture-key" {
		t.Fatal("valid fixture code exchange failed")
	}
	key, err = exchangeOpenRouterCode(t.Context(), server.URL+"/redirect", server.Client(), "fixture-code", grant.verifier)
	if err == nil || key != "" {
		t.Fatal("a redirected exchange should fail")
	}
}
