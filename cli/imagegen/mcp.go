// SPDX-License-Identifier: MIT

package imagegen

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wooinwoo/freeway-cli/cli/internal/filelock"
)

const (
	maxImageBytes     = 8 << 20
	maxMCPBytes       = 12 << 20
	maxListBytes      = 1 << 20
	legacyConfigLimit = 8192
	readinessTimeout  = 10 * time.Second
	MaxPrompt         = 4000
	MaxRoutes         = 3
	tokenEnvPrefix    = "FREEWAY_MCP_"
	mcpConfigLock     = ".image-mcp.lock"
)

var ErrNotConnected = errors.New("no image MCP tool is configured")
var ErrAuthRequired = errors.New("the MCP server requires authorization or rejected the token")
var ErrTokenUnavailable = errors.New("the MCP token environment variable is empty or invalid")
var ErrToolUnavailable = errors.New("the configured image tool is no longer available")
var ErrNoReadyRoute = errors.New("no configured MCP image tool is ready")
var ErrDuplicateRoute = errors.New("this MCP image tool is already configured")
var ErrConfigChanged = errors.New("image MCP settings changed in another Freeway window")

type Route struct {
	Endpoint string `json:"endpoint"`
	Tool     string `json:"tool,omitempty"`
	TokenEnv string `json:"token_env,omitempty"`
	OAuthID  string `json:"oauth_id,omitempty"`
}

type Config struct {
	Route
	Alternates []Route `json:"alternates,omitempty"`
}

type Result struct {
	Path       string
	Route      Route
	Position   int
	Dispatched bool
}

func (c Config) Routes() []Route { return append([]Route{c.Route}, c.Alternates...) }

func (r Route) Host() string {
	endpoint, err := url.Parse(r.Endpoint)
	if err != nil {
		return ""
	}
	return endpoint.Host
}

type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"inputSchema,omitempty"`
}

func configDir() (string, error) {
	dir, err := os.UserConfigDir()
	return filepath.Join(dir, "freeway"), err
}

func Load() (Config, error) {
	dir, err := configDir()
	if err != nil {
		return Config{}, err
	}
	file, err := os.Open(filepath.Join(dir, "image-mcp.json"))
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, ErrNotConnected
	}
	if err != nil {
		return Config{}, err
	}
	defer file.Close()
	var config Config
	if err := json.NewDecoder(io.LimitReader(file, 64<<10)).Decode(&config); err != nil {
		return Config{}, errors.New("invalid image MCP configuration")
	}
	if err := validateRoutes(config.Routes()); err != nil {
		return Config{}, err
	}
	return config, nil
}

func validateRoutes(routes []Route) error {
	if len(routes) > MaxRoutes {
		return errors.New("at most three image MCP tools may be connected")
	}
	seen := make(map[[2]string]bool, len(routes))
	for _, route := range routes {
		if !validMCPServer(route) || !validToolName(route.Tool) {
			return errors.New("invalid image MCP endpoint or tool")
		}
		key := [2]string{route.Endpoint, route.Tool}
		if seen[key] {
			return ErrDuplicateRoute
		}
		seen[key] = true
	}
	return nil
}

func SaveRoutes(previous, routes []Route) error {
	return saveRoutes(previous, routes, 0, nil)
}

func SaveOAuthRoute(previous, routes []Route, index int, grant *OAuthGrant) error {
	routes, err := bindOAuthGrant(routes, index, grant)
	if err != nil {
		return err
	}
	return saveRoutes(previous, routes, index, grant)
}

func bindOAuthGrant(routes []Route, index int, grant *OAuthGrant) ([]Route, error) {
	if grant == nil || index < 0 || index >= len(routes) || routes[index].Endpoint != grant.endpoint || !validBearerToken(grant.token) {
		return nil, errors.New("invalid MCP browser approval")
	}
	if !grant.expires.IsZero() && !time.Now().Before(grant.expires) {
		return nil, ErrBrowserLoginRequired
	}
	routes = slices.Clone(routes)
	routes[index].TokenEnv = ""
	routes[index].OAuthID = rand.Text()
	return routes, nil
}

func saveRoutes(previous, routes []Route, grantIndex int, grant *OAuthGrant) error {
	if err := validateRoutes(routes); err != nil {
		return err
	}
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	release, err := filelock.Try(filepath.Join(dir, mcpConfigLock))
	if errors.Is(err, filelock.ErrLocked) {
		return ErrConfigChanged
	}
	if err != nil {
		return err
	}
	defer release()
	current, err := Load()
	var currentRoutes []Route
	if err == nil {
		currentRoutes = current.Routes()
	} else if !errors.Is(err, ErrNotConnected) {
		return err
	}
	if !slices.Equal(previous, currentRoutes) {
		return ErrConfigChanged
	}
	known := slices.Clone(currentRoutes)
	if servers, err := LoadMCPServers(); err == nil {
		known = append(known, servers...)
	}
	if err := validateOAuthReferences(routes, known, grantIndex, grant); err != nil {
		return err
	}
	if len(routes) == 0 {
		if err := removeConfig(dir); err != nil {
			return err
		}
		pruneImageOAuthGrants(dir, previous, nil)
		return nil
	}
	config := Config{Route: routes[0], Alternates: routes[1:]}
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(config); err != nil {
		return err
	}
	if data.Len() > legacyConfigLimit {
		return errors.New("the image MCP configuration is too large; use shorter endpoint URLs")
	}
	if grant != nil {
		if err := saveOAuthGrant(dir, routes[grantIndex], grant); err != nil {
			return err
		}
	}
	published := false
	defer func() {
		if grant != nil && !published {
			removeOAuthGrant(dir, routes[grantIndex].OAuthID)
		}
	}()
	if err := writeMCPConfig(dir, "image-mcp.json", data.Bytes()); err != nil {
		return err
	}
	published = true
	pruneImageOAuthGrants(dir, previous, routes)
	return nil
}

func writeMCPConfig(dir, name string, data []byte) error {
	file, err := os.CreateTemp(dir, ".mcp-settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	if err := file.Close(); writeErr == nil {
		writeErr = err
	}
	if writeErr != nil {
		return writeErr
	}
	return os.Rename(file.Name(), filepath.Join(dir, name))
}

func pruneImageOAuthGrants(dir string, previous, current []Route) {
	servers, err := LoadMCPServers()
	if err == nil {
		pruneOAuthGrants(dir, previous, append(slices.Clone(current), servers...))
	}
}

func pruneOAuthGrants(dir string, previous, current []Route) {
	for _, route := range previous {
		if route.OAuthID != "" && !slices.ContainsFunc(current, func(next Route) bool { return next.OAuthID == route.OAuthID }) {
			removeOAuthGrant(dir, route.OAuthID)
		}
	}
}

func Disconnect() error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	release, err := filelock.Try(filepath.Join(dir, mcpConfigLock))
	if errors.Is(err, filelock.ErrLocked) {
		return ErrConfigChanged
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer release()
	current, loadErr := Load()
	if err := removeConfig(dir); err != nil {
		return err
	}
	if loadErr == nil {
		pruneImageOAuthGrants(dir, current.Routes(), nil)
	}
	return nil
}

func removeConfig(dir string) error {
	err := os.Remove(filepath.Join(dir, "image-mcp.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func validEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || len(endpoint) > 2048 {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "https" || u.Scheme == "http" && ip != nil && ip.IsLoopback()
}

func validToolName(name string) bool {
	return len(name) > 0 && len(name) <= 128 && strings.IndexFunc(name, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.", r))
	}) < 0
}

type boundedTransport struct {
	http.RoundTripper
	maxBytes      atomic.Int64
	token         string
	authDenied    atomic.Bool
	tokenRejected atomic.Bool
}

func (t *boundedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.token != "" {
		req = req.Clone(req.Context())
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	resp, err := t.RoundTripper.RoundTrip(req)
	if err == nil {
		if resp.StatusCode == http.StatusUnauthorized {
			t.tokenRejected.Store(true)
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			t.authDenied.Store(true)
		}
		resp.Body = http.MaxBytesReader(nil, resp.Body, t.maxBytes.Load())
	}
	return resp, err
}

func validTokenEnv(name string) bool {
	suffix, ok := strings.CutPrefix(name, tokenEnvPrefix)
	return name == "" || ok && suffix != "" && len(name) <= 100 && strings.IndexFunc(suffix, func(r rune) bool {
		return !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_')
	}) < 0
}

func validBearerToken(token string) bool {
	return token != "" && len(token) <= 8192 && strings.IndexFunc(token, func(r rune) bool { return r < '!' || r > '~' }) < 0
}

// WithoutTokenEnv keeps image MCP credentials out of other processes.
func WithoutTokenEnv() []string {
	return slices.DeleteFunc(os.Environ(), func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return strings.HasPrefix(strings.ToUpper(name), tokenEnvPrefix)
	})
}

func (c Route) checkTools(ctx context.Context, maxBytes int64, selected string, imageOnly bool, transport http.RoundTripper, rejected string) (*mcp.ClientSession, *boundedTransport, []Tool, error) {
	var token string
	if c.OAuthID != "" {
		var err error
		token, err = c.oauthToken(ctx, transport, rejected)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if c.TokenEnv != "" {
		if !validTokenEnv(c.TokenEnv) {
			return nil, nil, nil, ErrTokenUnavailable
		}
		token = os.Getenv(c.TokenEnv)
		if !validBearerToken(token) {
			return nil, nil, nil, ErrTokenUnavailable
		}
	}
	bounded := &boundedTransport{RoundTripper: transport, token: token}
	bounded.maxBytes.Store(maxListBytes)
	client := mcp.NewClient(&mcp.Implementation{Name: "Freeway", Version: "1"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}})
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: c.Endpoint, MaxRetries: -1, DisableStandaloneSSE: true, MaxEventSize: int(maxBytes),
		HTTPClient: &http.Client{
			Timeout: 2 * time.Minute, Transport: bounded,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil)
	if err != nil {
		return nil, bounded, nil, bounded.failure("could not connect to the MCP server")
	}
	tools, err := listSessionTools(ctx, session, bounded, selected, imageOnly)
	if err != nil {
		_ = session.Close()
		return nil, bounded, nil, err
	}
	return session, bounded, tools, nil
}

func (c Route) transport() *http.Transport {
	var transport *http.Transport
	if c.OAuthID != "" {
		transport = oauthPublicTransport()
	} else {
		transport = http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
	}
	transport.DisableKeepAlives = true
	return transport
}

func (c Route) prepareTools(ctx context.Context, maxBytes int64, selected string, imageOnly bool, network http.RoundTripper) (*mcp.ClientSession, *boundedTransport, []Tool, error) {
	if !validEndpoint(c.Endpoint) {
		return nil, nil, nil, errors.New("enter an HTTPS MCP URL or an HTTP loopback IP")
	}
	if network == nil {
		network = c.transport()
	}
	ctx, cancel := context.WithTimeout(ctx, readinessTimeout)
	defer cancel()
	session, bounded, tools, err := c.checkTools(ctx, maxBytes, selected, imageOnly, network, "")
	if c.OAuthID != "" && errors.Is(err, ErrAuthRequired) && bounded.tokenRejected.Load() {
		// A server can expire a token before the recorded expiry, or omit an expiry.
		session, bounded, tools, err = c.checkTools(ctx, maxBytes, selected, imageOnly, network, bounded.token)
		if errors.Is(err, ErrAuthRequired) && bounded.tokenRejected.Load() {
			err = ErrBrowserLoginRequired
		}
	}
	return session, bounded, tools, err
}

func (t *boundedTransport) failure(message string) error {
	if t.authDenied.Load() {
		return ErrAuthRequired
	}
	return errors.New(message)
}

type checkTransport struct {
	http.RoundTripper
	ctx context.Context
}

func (t checkTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodDelete {
		// The SDK detaches cleanup from the probe and waits up to five seconds.
		ctx, cancel := context.WithTimeout(t.ctx, 500*time.Millisecond)
		defer cancel()
		return t.RoundTripper.RoundTrip(req.Clone(ctx))
	}
	return t.RoundTripper.RoundTrip(req)
}

func (c Route) Check(ctx context.Context) error {
	session, _, _, err := c.prepareTools(ctx, maxListBytes, c.Tool, true, checkTransport{RoundTripper: c.transport(), ctx: ctx})
	if err == nil {
		_ = session.Close()
	}
	return err
}

func (c Route) Tools(ctx context.Context) ([]Tool, error) {
	return c.tools(ctx, true)
}

func (c Route) tools(ctx context.Context, imageOnly bool) ([]Tool, error) {
	session, _, tools, err := c.prepareTools(ctx, maxListBytes, "", imageOnly, checkTransport{RoundTripper: c.transport(), ctx: ctx})
	if err == nil {
		_ = session.Close()
	}
	return tools, err
}

func listSessionTools(ctx context.Context, session *mcp.ClientSession, transport *boundedTransport, selected string, imageOnly bool) ([]Tool, error) {
	tools := []Tool{}
	seen := make(map[string]bool)
	pages := 16
	if imageOnly {
		pages = 4
	}
	params := &mcp.ListToolsParams{}
	for range pages {
		page, err := session.ListTools(ctx, params)
		if err != nil {
			return nil, transport.failure("could not list MCP tools")
		}
		for _, tool := range page.Tools {
			if tool == nil || !validToolName(tool.Name) || seen[tool.Name] || imageOnly && !acceptsPrompt(tool.InputSchema) {
				continue
			}
			if selected == tool.Name {
				return nil, nil
			}
			if selected != "" {
				continue
			}
			entry := Tool{Name: tool.Name, Description: tool.Description}
			if imageOnly {
				description := []rune(tool.Description)
				entry.Description = string(description[:min(len(description), 240)])
			} else {
				entry.InputSchema = tool.InputSchema
			}
			tools = append(tools, entry)
			seen[tool.Name] = true
			if imageOnly && len(tools) == 32 {
				return tools, nil
			}
			if len(tools) > 256 {
				return nil, errors.New("the MCP tool list exceeds 256 tools. You can call a known tool by name")
			}
		}
		if page.NextCursor == "" {
			if selected != "" {
				if !imageOnly {
					return nil, errors.New("the named MCP tool is not advertised by this server")
				}
				return nil, ErrToolUnavailable
			}
			return tools, nil
		}
		if page.NextCursor == params.Cursor {
			if !imageOnly {
				return nil, errors.New("the MCP server repeated a tool list cursor")
			}
			break
		}
		params.Cursor = page.NextCursor
	}
	if !imageOnly {
		return nil, errors.New("the MCP tool list exceeds 16 pages")
	}
	if selected != "" {
		return nil, ErrToolUnavailable
	}
	return tools, nil
}

func acceptsPrompt(schema any) bool {
	data, err := json.Marshal(schema)
	if err != nil {
		return false
	}
	var shape struct {
		Properties map[string]struct{ Type string } `json:"properties"`
		Required   []string                         `json:"required"`
	}
	if json.Unmarshal(data, &shape) != nil || shape.Properties["prompt"].Type != "string" {
		return false
	}
	for _, name := range shape.Required {
		if name != "prompt" {
			return false
		}
	}
	return true
}

func (c Config) Generate(ctx context.Context, id, prompt string) (Result, error) {
	if prompt == "" || !utf8.ValidString(prompt) || utf8.RuneCountInString(prompt) > MaxPrompt {
		return Result{}, errors.New("invalid image prompt")
	}
	if _, err := imagePath(id, ".png"); err != nil {
		return Result{}, err
	}
	routes := c.Routes()
	if err := validateRoutes(routes); err != nil {
		return Result{}, err
	}
	for i, route := range routes {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		result := Result{Route: route, Position: i + 1}
		session, transport, err := route.prepareImage(ctx)
		if err != nil {
			if len(routes) == 1 {
				return result, err
			}
			continue
		}
		defer session.Close()
		// The request may be processed even when its response is lost. Never
		// forward the prompt to another tool after the call starts.
		result.Dispatched = true
		result.Path, err = route.callImage(ctx, id, prompt, session, transport)
		return result, err
	}
	return Result{}, ErrNoReadyRoute
}

func (c Route) prepareImage(ctx context.Context) (*mcp.ClientSession, *boundedTransport, error) {
	session, transport, _, err := c.prepareTools(ctx, maxMCPBytes, c.Tool, true, nil)
	return session, transport, err
}

func (c Route) callImage(ctx context.Context, id, prompt string, session *mcp.ClientSession, transport *boundedTransport) (string, error) {
	transport.maxBytes.Store(maxMCPBytes)
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: c.Tool, Arguments: map[string]string{"prompt": prompt}})
	if err != nil || result.IsError {
		return "", transport.failure("the MCP image tool could not finish this request")
	}
	for _, content := range result.Content {
		var output *mcp.ImageContent
		switch item := content.(type) {
		case *mcp.ImageContent:
			output = item
		case *mcp.EmbeddedResource:
			if item.Resource != nil && strings.HasPrefix(item.Resource.MIMEType, "image/") {
				output = &mcp.ImageContent{Data: item.Resource.Blob, MIMEType: item.Resource.MIMEType}
			}
		}
		if output == nil {
			continue
		}
		ext, err := imageExtension(output)
		if err != nil {
			return "", err
		}
		return saveImage(ctx, id, ext, output.Data)
	}
	return "", errors.New("the MCP tool did not return an image attachment")
}

func imageExtension(item *mcp.ImageContent) (string, error) {
	if len(item.Data) <= maxImageBytes {
		header, format, err := image.DecodeConfig(bytes.NewReader(item.Data))
		if err == nil && header.Width > 0 && header.Height > 0 && header.Width <= 4096 && header.Height <= 4096 &&
			(format == "png" || format == "jpeg") && item.MIMEType == "image/"+format {
			if _, _, err := image.Decode(bytes.NewReader(item.Data)); err == nil {
				return "." + format, nil
			}
		}
	}
	return "", errors.New("the MCP tool must return a valid PNG or JPEG up to 8 MiB and 4096 × 4096 pixels")
}
