// SPDX-License-Identifier: MIT

package imagegen

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type oauthTestTransport func(*http.Request) (*http.Response, error)

func (f oauthTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestImageOAuthBlocksPrivateAndTranslatedNetworkTargets(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "169.254.169.254", "192.168.1.20", "100.64.0.1", "64:ff9b::7f00:1", "168.63.129.16"} {
		if publicOAuthIP(netip.MustParseAddr(ip)) {
			t.Errorf("non-public OAuth address accepted: %s", ip)
		}
	}
}

func TestExpiredBrowserGrantRenewsOnceAcrossWindows(t *testing.T) {
	dir := isolatedImageConfig(t)
	route := Route{Endpoint: "https://mcp.fixture.test/mcp", Tool: "image", OAuthID: rand.Text()}
	data, err := json.Marshal(Config{Route: route})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeMCPConfig(dir, "image-mcp.json", data); err != nil {
		t.Fatal(err)
	}
	grant := OAuthGrant{endpoint: route.Endpoint, token: "expired-token", expires: time.Now().Add(-time.Minute), refresh: &oauthRefresh{
		Token: "original-refresh", URL: "https://login.fixture.test/token", ClientID: "fixture-freeway",
	}}
	if err := saveOAuthGrant(dir, route, &grant); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	var success atomic.Bool
	network := oauthTestTransport(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		defer req.Body.Close()
		if err := req.ParseForm(); err != nil || req.Method != http.MethodPost || req.URL.String() != grant.refresh.URL ||
			req.Form.Get("grant_type") != "refresh_token" || req.Form.Get("resource") != route.Endpoint ||
			req.Form.Get("refresh_token") != grant.refresh.Token || req.Form.Get("client_id") != grant.refresh.ClientID || req.Header.Get("Authorization") != "" {
			t.Error("refresh was not bound to the registered public client and MCP endpoint")
		}
		if !success.Load() {
			return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": {"https://unexpected.fixture.test/token"}}, Body: http.NoBody}, nil
		}
		time.Sleep(80 * time.Millisecond)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"access_token":"renewed-token","token_type":"Bearer","expires_in":3600,"refresh_token":"rotated-refresh"}`))}, nil
	})
	if _, err := route.oauthToken(t.Context(), network, ""); !errors.Is(err, ErrBrowserRefreshUnavailable) || calls.Load() != 1 {
		t.Fatalf("temporary refresh failure should not follow redirects: %v, requests %d", err, calls.Load())
	}
	success.Store(true)
	results := make(chan error, 2)
	for range 2 {
		go func() {
			token, err := route.oauthToken(t.Context(), network, "")
			if err == nil && token != "renewed-token" {
				err = errors.New("returned a stale token")
			}
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("concurrent windows must exchange the rotating refresh token once, got %d total requests", calls.Load())
	}
	root, err := oauthRoot(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	aead, err := oauthCipher(root, false)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := route.readOAuthGrant(root, aead)
	if err != nil || stored.Refresh == nil || stored.Refresh.Token != "rotated-refresh" || stored.Token != "renewed-token" {
		t.Fatal("renewed and rotated tokens were not written to the encrypted credential")
	}
	stored.Expires = time.Now().Add(-time.Minute)
	if err := writeOAuthGrant(root, aead, route, "test-expired.enc", stored); err != nil {
		t.Fatal(err)
	}
	if err := root.Rename("test-expired.enc", route.OAuthID+".enc"); err != nil {
		t.Fatal(err)
	}
	revoked := oauthTestTransport(func(req *http.Request) (*http.Response, error) {
		defer req.Body.Close()
		if req.ParseForm() != nil || req.Form.Get("refresh_token") != "rotated-refresh" {
			t.Error("next renewal did not use the rotated credential")
		}
		return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(`{"error":"invalid_grant"}`))}, nil
	})
	if _, err := route.oauthToken(t.Context(), revoked, ""); !errors.Is(err, ErrBrowserLoginRequired) {
		t.Fatalf("revoked credential should request browser approval: %v", err)
	}
	releaseConfig, err := lockOAuthRefresh(t.Context(), filepath.Join(dir, ".image-mcp.lock"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cancelled := oauthTestTransport(func(req *http.Request) (*http.Response, error) {
		defer req.Body.Close()
		cancel()
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"access_token":"cancelled-token","token_type":"Bearer","refresh_token":"replacement-refresh"}`))}, nil
	})
	go func() {
		_, err := route.oauthToken(ctx, cancelled, "")
		results <- err
	}()
	select {
	case err := <-results:
		releaseConfig()
		t.Fatalf("renewal ended before cancellation: %v", err)
	case <-ctx.Done():
	}
	time.Sleep(100 * time.Millisecond)
	releaseConfig()
	if err := <-results; err != nil {
		t.Fatalf("cancellation after rotation must preserve the replacement credentials: %v", err)
	}
	stored, err = route.readOAuthGrant(root, aead)
	if err != nil || stored.Refresh == nil || stored.Refresh.Token != "replacement-refresh" {
		t.Fatal("rotated credential was lost after cancellation")
	}
	started, respond := make(chan struct{}), make(chan struct{})
	blocked := oauthTestTransport(func(req *http.Request) (*http.Response, error) {
		defer req.Body.Close()
		close(started)
		select {
		case <-respond:
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"access_token":"later-token","token_type":"Bearer"}`))}, nil
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	})
	go func() {
		_, err := route.oauthToken(t.Context(), blocked, "cancelled-token")
		results <- err
	}()
	select {
	case err := <-results:
		t.Fatalf("renewal ended before disconnect: %v", err)
	case <-started:
	}
	disconnectErr := Disconnect()
	close(respond)
	if err := <-results; disconnectErr != nil || !errors.Is(err, ErrBrowserLoginRequired) {
		t.Fatalf("disconnect must succeed during renewal and prevent credential resurrection: refresh %v, disconnect %v", err, disconnectErr)
	}
	if _, err := root.Stat(route.OAuthID + ".enc"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refresh restored a disconnected credential")
	}
}

func TestBrowserApprovalBindsAndEncryptsVerifiedCredentials(t *testing.T) {
	const (
		resource = "https://mcp.fixture.test/mcp"
		issuer   = "https://login.fixture.test"
		redirect = "http://127.0.0.1:32145/freeway/callback"
		secret   = "fixture-browser-bearer-token"
		refresh  = "fixture-browser-refresh-token"
		renewed  = "fixture-renewed-bearer-token"
	)
	for _, scenario := range []string{"approved", "legacy registration", "cancelled", "different token origin"} {
		t.Run(scenario, func(t *testing.T) {
			dir := isolatedImageConfig(t)
			previous := []Route{{Endpoint: resource, Tool: "old_tool", TokenEnv: "FREEWAY_MCP_EXISTING"}}
			if err := SaveRoutes(nil, previous); err != nil {
				t.Fatal(err)
			}
			mcpServer := mcp.NewServer(&mcp.Implementation{Name: "image-fixture", Version: "1"}, nil)
			mcpServer.AddTool(&mcp.Tool{Name: "image", InputSchema: json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"}},"required":["prompt"]}`)},
				func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					t.Error("setup must never generate an image")
					return nil, errors.New("unexpected call")
				})
			mcpServer.AddTool(&mcp.Tool{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`)},
				func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					t.Error("discovery must not call a general tool")
					return nil, errors.New("unexpected call")
				})
			mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil)
			var tokenCalls atomic.Int32
			var registrations atomic.Int32
			var approvals atomic.Int32
			var originalRejected atomic.Bool
			var challenge atomic.Value
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/mcp":
					want := secret
					if originalRejected.Load() {
						want = renewed
					}
					if r.Header.Get("Authorization") != "Bearer "+want {
						w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://mcp.fixture.test/.well-known/oauth-protected-resource/mcp", scope="image:generate"`)
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					mcpHandler.ServeHTTP(w, r)
				case "/.well-known/oauth-protected-resource/mcp":
					_ = json.NewEncoder(w).Encode(map[string]any{"resource": resource, "authorization_servers": []string{issuer}})
				case "/.well-known/oauth-authorization-server":
					tokenEndpoint := issuer + "/token"
					if scenario == "different token origin" {
						tokenEndpoint = "https://unapproved.fixture.test/token"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{
						"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": tokenEndpoint,
						"registration_endpoint": issuer + "/register", "code_challenge_methods_supported": []string{"S256"},
						"scopes_supported": []string{"offline_access"},
					})
				case "/register":
					registrations.Add(1)
					var input struct {
						URIs   []string `json:"redirect_uris"`
						Grants []string `json:"grant_types"`
					}
					if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.URIs) != 1 || input.URIs[0] != redirect {
						t.Error("registration request had an unexpected redirect")
					}
					if scenario == "legacy registration" && len(input.Grants) > 1 {
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte(`{"error":"invalid_client_metadata"}`))
						return
					}
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"client_id":"fixture-freeway","token_endpoint_auth_method":"none","redirect_uris":["` + redirect + `"]}`))
				case "/token":
					tokenCalls.Add(1)
					if err := r.ParseForm(); err != nil {
						t.Error("could not read token form")
					}
					if r.Form.Get("grant_type") == "refresh_token" {
						if !originalRejected.Load() || r.Form.Get("resource") != resource || r.Form.Get("refresh_token") != refresh {
							t.Error("invalid refresh after MCP rejected a token without an expiry")
						}
						_, _ = w.Write([]byte(`{"access_token":"` + renewed + `","token_type":"Bearer"}`))
						return
					}
					if r.Form.Get("code") != "fixture-code" || r.Form.Get("resource") != resource || r.Form.Get("redirect_uri") != redirect {
						t.Error("token exchange was not bound to the original approval")
					}
					verifier := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
					if challenge.Load() != base64.RawURLEncoding.EncodeToString(verifier[:]) {
						t.Error("token exchange did not use the verifier for the approved PKCE challenge")
					}
					response := map[string]any{"access_token": secret, "token_type": "Bearer"}
					if scenario != "legacy registration" {
						response["refresh_token"] = refresh
					}
					_ = json.NewEncoder(w).Encode(response)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			local, _ := url.Parse(server.URL)
			network := oauthTestTransport(func(req *http.Request) (*http.Response, error) {
				copy := req.Clone(req.Context())
				copy.URL.Host = local.Host
				copy.Host = local.Host
				return server.Client().Transport.RoundTrip(copy)
			})
			cancelled := errors.New("fixture user cancelled")
			route := Route{Endpoint: resource, Tool: "image"}
			tools, grant, err := route.authorize(t.Context(), redirect, func(_ context.Context, approval *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
				approvals.Add(1)
				if scenario == "cancelled" {
					return nil, cancelled
				}
				address, _ := url.Parse(approval.URL)
				params := address.Query()
				scopes, prompt := strings.Fields(params.Get("scope")), "consent"
				slices.Sort(scopes)
				wantScopes := []string{"image:generate", "offline_access"}
				if scenario == "legacy registration" {
					wantScopes, prompt = []string{"image:generate"}, ""
				}
				if address.Host != "login.fixture.test" || params.Get("resource") != resource || !slices.Equal(scopes, wantScopes) || params.Get("prompt") != prompt || params.Get("code_challenge_method") != "S256" || params.Get("state") == "" {
					t.Error("unexpected browser address or scope")
					return nil, ErrBrowserUnsupported
				}
				challenge.Store(params.Get("code_challenge"))
				return &auth.AuthorizationResult{Code: "fixture-code", State: params.Get("state"), Iss: issuer}, nil
			}, network, true)
			if _, err := os.Stat(filepath.Join(dir, "image-mcp-auth")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("browser approval wrote credentials before tool confirmation")
			}
			if scenario == "cancelled" || scenario == "different token origin" {
				want := ErrBrowserUnsupported
				if scenario == "cancelled" {
					want = cancelled
				}
				if !errors.Is(err, want) || grant != nil || tokenCalls.Load() != 0 {
					t.Fatalf("unexpected result for rejected approval: %v, exchange count %d", err, tokenCalls.Load())
				}
				stored, err := Load()
				if err != nil || stored.Route != previous[0] {
					t.Fatal("existing image settings changed on OAuth cancellation or failure")
				}
				return
			}
			if err != nil || grant == nil || len(tools) != 1 || tools[0].Name != route.Tool || tokenCalls.Load() != 1 {
				t.Fatalf("browser login failed to list only authenticated tools: %+v, %v, %d", tools, err, tokenCalls.Load())
			}
			if scenario == "legacy registration" {
				if grant.refresh != nil || registrations.Load() != 2 || approvals.Load() != 1 {
					t.Fatal("legacy registration should use a single browser approval and keep its access token")
				}
			} else if grant.refresh == nil || grant.refresh.Token != refresh || grant.refresh.URL != issuer+"/token" || grant.refresh.ClientID != "fixture-freeway" {
				t.Fatal("refresh credentials were not bound to the approved token endpoint and client")
			}
			expired := *grant
			expired.expires = time.Now().Add(-time.Second)
			if err := SaveOAuthRoute(previous, []Route{route}, 0, &expired); !errors.Is(err, ErrBrowserLoginRequired) {
				t.Fatalf("accepted approval that expired before tool confirmation: %v", err)
			}
			if err := SaveOAuthRoute(previous, []Route{route}, 0, grant); err != nil {
				t.Fatal(err)
			}
			stored, err := Load()
			if err != nil || stored.OAuthID == "" || stored.TokenEnv != "" {
				t.Fatalf("invalid saved credential reference: %+v, %v", stored, err)
			}
			if token, err := stored.oauthToken(t.Context(), network, ""); err != nil || token != secret {
				t.Fatal("could not reload the encrypted access token")
			}
			secretFile := filepath.Join(dir, "image-mcp-auth", stored.OAuthID+".enc")
			for _, path := range []string{filepath.Join(dir, "image-mcp.json"), secretFile} {
				data, err := os.ReadFile(path)
				if err != nil || bytes.Contains(data, []byte(secret)) || bytes.Contains(data, []byte(refresh)) {
					t.Fatal("browser secrets leaked to a local settings file")
				}
			}
			wrong := stored.Route
			wrong.Endpoint = strings.Replace(resource, "/mcp", "/other", 1)
			if _, err := wrong.oauthToken(t.Context(), network, ""); !errors.Is(err, ErrBrowserLoginRequired) {
				t.Fatal("saved access token was valid for a different MCP endpoint")
			}
			originalRejected.Store(true)
			session, _, tools, err := stored.prepareTools(t.Context(), maxListBytes, "", false, network)
			if scenario == "legacy registration" {
				if !errors.Is(err, ErrBrowserLoginRequired) || tokenCalls.Load() != 1 {
					t.Fatal("missing refresh token must request browser login after rejection")
				}
			} else if err != nil || len(tools) != 2 || tokenCalls.Load() != 2 || approvals.Load() != 1 {
				t.Fatalf("MCP rejection should renew a token without an expiry before listing image and general tools: %v, exchange count %d", err, tokenCalls.Load())
			}
			if session != nil {
				_ = session.Close()
			}
			if err := SaveRoutes(stored.Routes(), nil); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(secretFile); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("disconnect did not remove the local credential")
			}
		})
	}
}
