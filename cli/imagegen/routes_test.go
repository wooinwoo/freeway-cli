// SPDX-License-Identifier: MIT

package imagegen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func isolatedImageConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"XDG_CONFIG_HOME", "HOME", "APPDATA"} {
		t.Setenv(name, dir)
	}
	// macOS ignores XDG_CONFIG_HOME, so resolve the folder the same way the code does.
	config, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(config, "freeway")
}

func TestImageRoutesPreserveLegacySettingsAndRejectStaleWrites(t *testing.T) {
	dir := isolatedImageConfig(t)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "image-mcp.json"), []byte(`{"endpoint":"https://example.org/mcp","tool":"first","token_env":"FREEWAY_MCP_TOKEN"}`), 0600); err != nil {
		t.Fatal(err)
	}
	legacy, err := Load()
	if err != nil || len(legacy.Routes()) != 1 || legacy.Route.Tool != "first" || legacy.TokenEnv != "FREEWAY_MCP_TOKEN" {
		t.Fatalf("legacy config: %+v, %v", legacy, err)
	}
	backup := Route{Endpoint: "https://other.example.org/mcp", Tool: "next"}
	if err := SaveRoutes(legacy.Routes(), []Route{legacy.Route, legacy.Route}); !errors.Is(err, ErrDuplicateRoute) {
		t.Fatalf("accepted a duplicate route: %v", err)
	}
	want := []Route{backup, legacy.Route}
	if err := SaveRoutes(legacy.Routes(), want); err != nil {
		t.Fatal(err)
	}
	if err := SaveRoutes(legacy.Routes(), nil); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("stale removal did not detect the other window's update: %v", err)
	}
	loaded, err := Load()
	if err != nil || !slices.Equal(loaded.Routes(), want) {
		t.Fatalf("reordered settings were not preserved: %+v, %v", loaded, err)
	}
	longRoutes := []Route{
		{Endpoint: "https://example.org/" + strings.Repeat("&", 1200), Tool: "first"},
		{Endpoint: "https://other.example.org/" + strings.Repeat("&", 1200), Tool: "second"},
	}
	if err := SaveRoutes(want, longRoutes); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join(dir, "image-mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var legacyPrimary Route
	if err := json.NewDecoder(io.LimitReader(file, 8192)).Decode(&legacyPrimary); err != nil || legacyPrimary != longRoutes[0] {
		t.Fatalf("an older release could not read the primary route: %+v, %v", legacyPrimary, err)
	}
}

func TestImageAutoOnlyFallsBackBeforeCallingATool(t *testing.T) {
	isolatedImageConfig(t)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	readyImage := []mcp.Content{&mcp.ImageContent{MIMEType: "image/png", Data: encoded.Bytes()}}
	newServer := func(name string, contents []mcp.Content, calls *atomic.Int32) *httptest.Server {
		server := mcp.NewServer(&mcp.Implementation{Name: "image-fixture", Version: "1"}, nil)
		type promptInput struct {
			Prompt string `json:"prompt"`
		}
		mcp.AddTool(server, &mcp.Tool{Name: name}, func(_ context.Context, _ *mcp.CallToolRequest, input promptInput) (*mcp.CallToolResult, any, error) {
			calls.Add(1)
			if input.Prompt != "test image" {
				t.Errorf("unexpected prompt: %q", input.Prompt)
			}
			return &mcp.CallToolResult{Content: contents}, nil, nil
		})
		return httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	}
	var primaryCalls, fallbackCalls atomic.Int32
	primary := newServer("primary", []mcp.Content{&mcp.TextContent{Text: "requested image was not returned"}}, &primaryCalls)
	defer primary.Close()
	backup := newServer("backup", readyImage, &fallbackCalls)
	defer backup.Close()
	config := Config{Route: Route{Endpoint: primary.URL, Tool: "removed_tool"}, Alternates: []Route{{Endpoint: backup.URL, Tool: "backup"}}}
	image, err := config.Generate(t.Context(), "fixture-backup", "test image")
	if err != nil || image.Path == "" || image.Route.Tool != "backup" || primaryCalls.Load() != 0 || fallbackCalls.Load() != 1 {
		t.Fatalf("backup was not used after the selected tool disappeared: %+v, %v", image, err)
	}
	config.Route.Tool = "primary"
	image, err = config.Generate(t.Context(), "fixture-primary-error", "test image")
	if err == nil || !image.Dispatched || image.Route.Tool != "primary" || primaryCalls.Load() != 1 || fallbackCalls.Load() != 1 {
		t.Fatalf("request was duplicated after tool dispatch: %+v, %v (%d, %d)", image, err, primaryCalls.Load(), fallbackCalls.Load())
	}
}
