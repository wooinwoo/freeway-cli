// SPDX-License-Identifier: MIT

package imagegen

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

var ErrBrowserUnsupported = errors.New("this image server could not complete browser sign-in")
var errRefreshRegistration = errors.New("the authorization server rejected refresh registration")

type oneOAuthApproval struct {
	*auth.AuthorizationCodeHandler
	started              atomic.Bool
	registrationRejected atomic.Bool
}

func (handler *oneOAuthApproval) Authorize(ctx context.Context, req *http.Request, resp *http.Response) error {
	if resp.StatusCode != http.StatusUnauthorized || !handler.started.CompareAndSwap(false, true) {
		_ = resp.Body.Close()
		return ErrBrowserUnsupported
	}
	err := handler.AuthorizationCodeHandler.Authorize(ctx, req, resp)
	var registration *oauthex.ClientRegistrationError
	if errors.As(err, &registration) && registration.ErrorCode == "invalid_client_metadata" {
		handler.registrationRejected.Store(true)
	}
	return err
}

type browserOAuthTransport struct {
	http.RoundTripper
	approvedOrigin atomic.Value
}

func (t *browserOAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !validOAuthURL(req.URL) {
		return nil, ErrBrowserUnsupported
	}
	if req.Method == http.MethodPost && strings.HasPrefix(req.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		if t.approvedOrigin.Load().(string) != oauthOrigin(req.URL) {
			return nil, ErrBrowserUnsupported
		}
	}
	response, err := t.RoundTripper.RoundTrip(req)
	if err == nil {
		response.Body = http.MaxBytesReader(nil, response.Body, maxListBytes)
	}
	return response, err
}

var reservedOAuthNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("168.63.129.16/32"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
}

func publicOAuthIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	for _, reserved := range reservedOAuthNetworks {
		if reserved.Contains(ip) {
			return false
		}
	}
	return true
}

func validOAuthURL(u *url.URL) bool {
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if u.Scheme != "https" || u.User != nil || u.Fragment != "" || host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	ip, err := netip.ParseAddr(host)
	return err != nil || publicOAuthIP(ip)
}

func oauthOrigin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

func oauthPublicTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: 7 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return ErrBrowserUnsupported
		}
		ip, err := netip.ParseAddr(host)
		if err != nil || !publicOAuthIP(ip) {
			return ErrBrowserUnsupported
		}
		return nil
	}}).DialContext
	return transport
}

func (r Route) Authorize(ctx context.Context, callback string, fetcher auth.AuthorizationCodeFetcher, imageOnly bool) ([]Tool, *OAuthGrant, error) {
	transport := oauthPublicTransport()
	defer transport.CloseIdleConnections()
	return r.authorize(ctx, callback, func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		address, _ := url.Parse(args.URL)
		lookup, cancel := context.WithTimeout(ctx, 5*time.Second)
		addresses, err := net.DefaultResolver.LookupNetIP(lookup, "ip", address.Hostname())
		cancel()
		if err != nil || len(addresses) == 0 {
			return nil, ErrBrowserUnsupported
		}
		for _, ip := range addresses {
			if !publicOAuthIP(ip) {
				return nil, ErrBrowserUnsupported
			}
		}
		return fetcher(ctx, args)
	}, transport, imageOnly)
}

func (r Route) authorize(ctx context.Context, callback string, fetcher auth.AuthorizationCodeFetcher, network http.RoundTripper, imageOnly bool) ([]Tool, *OAuthGrant, error) {
	tools, grant, err := r.authorizeWithRefresh(ctx, callback, fetcher, network, imageOnly, true)
	if errors.Is(err, errRefreshRegistration) {
		return r.authorizeWithRefresh(ctx, callback, fetcher, network, imageOnly, false)
	}
	return tools, grant, err
}

func (r Route) authorizeWithRefresh(ctx context.Context, callback string, fetcher auth.AuthorizationCodeFetcher, network http.RoundTripper, imageOnly, requestRefresh bool) ([]Tool, *OAuthGrant, error) {
	u, err := url.Parse(r.Endpoint)
	if err != nil || !validEndpoint(r.Endpoint) || !validOAuthURL(u) {
		return nil, nil, ErrBrowserUnsupported
	}
	transport := &browserOAuthTransport{RoundTripper: network}
	transport.approvedOrigin.Store("")
	client := &http.Client{
		Transport:     transport,
		Timeout:       12 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	var grant *OAuthGrant
	var approvalErr error
	metadata := &oauthex.ClientRegistrationMetadata{ClientName: "Freeway", RedirectURIs: []string{callback}, TokenEndpointAuthMethod: "none"}
	if requestRefresh {
		metadata.GrantTypes = []string{"authorization_code", "refresh_token"}
	}
	handler, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		Client: client, RedirectURL: callback, AcceptUnadvertisedIss: true, RequestRefreshToken: requestRefresh,
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{Metadata: metadata},
		AuthorizationCodeFetcher: func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			authorizeURL, err := url.Parse(args.URL)
			if err != nil || !validOAuthURL(authorizeURL) || authorizeURL.Query().Get("resource") != r.Endpoint {
				return nil, ErrBrowserUnsupported
			}
			query := authorizeURL.Query()
			for _, scope := range strings.Fields(query.Get("scope")) {
				if scope == "offline_access" {
					query.Set("prompt", "consent")
					authorizeURL.RawQuery = query.Encode()
					args.URL = authorizeURL.String()
					break
				}
			}
			result, err := fetcher(ctx, args)
			if err != nil {
				approvalErr = err
				return nil, err
			}
			if result == nil {
				return nil, ErrBrowserUnsupported
			}
			transport.approvedOrigin.Store(oauthOrigin(authorizeURL))
			return result, nil
		},
		NewTokenSource: func(_ context.Context, config *oauth2.Config, token *oauth2.Token) (oauth2.TokenSource, error) {
			if !validBearerToken(token.AccessToken) || !strings.EqualFold(token.Type(), "Bearer") || !token.Valid() {
				return nil, ErrBrowserUnsupported
			}
			grant = &OAuthGrant{endpoint: r.Endpoint, token: token.AccessToken, expires: token.Expiry}
			if validBearerToken(token.RefreshToken) && config.ClientSecret == "" && config.ClientID != "" && len(config.ClientID) <= 8192 {
				endpoint, err := url.Parse(config.Endpoint.TokenURL)
				if err == nil && validOAuthURL(endpoint) && oauthOrigin(endpoint) == transport.approvedOrigin.Load().(string) {
					grant.refresh = &oauthRefresh{Token: token.RefreshToken, URL: endpoint.String(), ClientID: config.ClientID}
				}
			}
			return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token.AccessToken, TokenType: "Bearer", Expiry: token.Expiry}), nil
		},
	})
	if err != nil {
		return nil, nil, ErrBrowserUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "Freeway", Version: "1"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}})
	approval := &oneOAuthApproval{AuthorizationCodeHandler: handler}
	session, err := mcpClient.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: r.Endpoint, HTTPClient: client, OAuthHandler: approval,
		MaxRetries: -1, DisableStandaloneSSE: true, MaxEventSize: maxListBytes,
	}, nil)
	if err != nil {
		if approvalErr != nil {
			return nil, nil, approvalErr
		}
		if requestRefresh && approval.registrationRejected.Load() {
			return nil, nil, errRefreshRegistration
		}
		return nil, nil, ErrBrowserUnsupported
	}
	defer session.Close()
	tools, err := listSessionTools(ctx, session, &boundedTransport{}, "", imageOnly)
	if approvalErr != nil {
		return nil, nil, approvalErr
	}
	if err != nil || grant == nil {
		return nil, nil, ErrBrowserUnsupported
	}
	return tools, grant, nil
}
