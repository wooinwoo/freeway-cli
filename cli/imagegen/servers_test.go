// SPDX-License-Identifier: MIT

package imagegen

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPServerKeepsSharedLoginUntilBothConnectionsAreRemoved(t *testing.T) {
	dir := isolatedImageConfig(t)
	const endpoint = "https://mcp.fixture.test/mcp"
	grant := &OAuthGrant{endpoint: endpoint, token: "fixture-access", refresh: &oauthRefresh{
		URL: "https://login.fixture.test/token", ClientID: "fixture-client", Token: "fixture-refresh",
	}}
	if err := SaveOAuthRoute(nil, []Route{{Endpoint: endpoint, Tool: "image"}}, 0, grant); err != nil {
		t.Fatal(err)
	}
	image, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	server := image.Route
	server.Tool = ""
	if err := SaveMCPServers(nil, []Route{server}, 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := Disconnect(); err != nil {
		t.Fatal(err)
	}
	stored, err := LoadMCPServers()
	if err != nil || len(stored) != 1 || stored[0] != server {
		t.Fatalf("image disconnect changed the general MCP server: %+v, %v", stored, err)
	}
	network := oauthTestTransport(func(req *http.Request) (*http.Response, error) {
		defer req.Body.Close()
		if req.URL.String() != grant.refresh.URL || req.ParseForm() != nil || req.Form.Get("resource") != endpoint {
			t.Error("general MCP refresh did not use the approved issuer and resource")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"access_token":"updated","token_type":"Bearer"}`))}, nil
	})
	if token, err := stored[0].oauthToken(t.Context(), network, "fixture-access"); err != nil || token != "updated" {
		t.Fatalf("general MCP reference failed to refresh: %v", err)
	}
	if err := SaveMCPServers(nil, nil, 0, nil); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("stale settings write was accepted: %v", err)
	}
	if err := SaveMCPServers(stored, nil, 0, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "image-mcp-auth", server.OAuthID+".enc")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("removing the last reference left a browser credential")
	}
	if err := SaveMCPServers(nil, []Route{server}, 0, nil); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("removed login was accepted as a new shared connection: %v", err)
	}
}

func TestImageRouteCanUseGeneralServerLogin(t *testing.T) {
	isolatedImageConfig(t)
	const endpoint = "https://mcp.fixture.test/mcp"
	grant := &OAuthGrant{endpoint: endpoint, token: "fixture-access"}
	if err := SaveMCPServers(nil, []Route{{Endpoint: endpoint}}, 0, grant); err != nil {
		t.Fatal(err)
	}
	servers, err := LoadMCPServers()
	if err != nil || len(servers) != 1 || servers[0].OAuthID == "" {
		t.Fatalf("general OAuth approval was not saved: %+v, %v", servers, err)
	}
	route := servers[0]
	route.Tool = "image"
	if err := SaveRoutes(nil, []Route{route}); err != nil {
		t.Fatal(err)
	}
	if err := SaveMCPServers(servers, nil, 0, nil); err != nil {
		t.Fatal(err)
	}
	if token, err := route.oauthToken(t.Context(), nil, ""); err != nil || token != "fixture-access" {
		t.Fatalf("removing the general server revoked the F9 login: %v", err)
	}
	if err := Disconnect(); err != nil {
		t.Fatal(err)
	}
	if err := SaveRoutes(nil, []Route{route}); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("image setup accepted a removed credential reference: %v", err)
	}
}
