// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/wooinwoo/freeway-cli/cli/imagegen"
	"github.com/wooinwoo/freeway-cli/cli/internal/terminaljson"
	"github.com/wooinwoo/freeway-cli/cli/tui"
)

const mcpUsage = `  freeway mcp connect  Save a Streamable HTTP server
  freeway mcp manage   Check and manage saved servers
  freeway mcp tools [--server ID_OR_URL | --url URL [--token-env NAME]]
  freeway mcp call (--server ID_OR_URL | --url URL [--token-env NAME]) [--args-file PATH|-] TOOL
`

func runMCPCommand(ctx context.Context, args []string, prefs tui.Preferences) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(mcpUsage)
		return nil
	}
	command := args[0]
	if (command == "connect" || command == "manage") && len(args) == 1 {
		if !interactiveTerminal() {
			return errors.New("MCP connection management requires an interactive terminal")
		}
		return manageMCPServers(ctx, prefs, terminalOptions(ctx, prefs.NoColor), command == "connect")
	}
	if command != "tools" && command != "call" {
		return errors.New("use freeway mcp --help to see the available commands")
	}
	flags := flag.NewFlagSet("freeway mcp "+command, flag.ContinueOnError)
	flags.Usage = func() { fmt.Fprint(flags.Output(), mcpUsage) }
	server := flags.String("server", "", "saved server ID or exact URL shown by freeway mcp tools")
	endpoint := flags.String("url", "", "Streamable HTTP MCP URL")
	tokenEnv := flags.String("token-env", "", "name of the FREEWAY_MCP_ variable containing the Bearer token")
	argsFile := ""
	if command == "call" {
		flags.StringVar(&argsFile, "args-file", "", "JSON object file, or - to read standard input")
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	wantArgs := 0
	if command == "call" {
		wantArgs = 1
	}
	if flags.NArg() != wantArgs || *endpoint != "" && *server != "" || *endpoint == "" && *tokenEnv != "" {
		return errors.New("invalid arguments for freeway mcp (see --help)")
	}
	if command == "call" && *server == "" && *endpoint == "" {
		return errors.New("choose a destination with --server or --url. Run freeway mcp tools to list saved servers")
	}
	routes, err := mcpRoutes(*server, *endpoint, *tokenEnv)
	if err != nil {
		return err
	}
	if command == "tools" {
		return listMCPTools(ctx, routes)
	}
	if len(routes) != 1 {
		return errors.New("multiple connections share this URL. Use the server ID shown by freeway mcp tools")
	}
	arguments, err := readMCPArguments(ctx, argsFile, os.Stdin)
	if err != nil {
		return err
	}
	if *server != "" {
		routes, err = mcpRoutes(routes[0].ID, "", "")
		if err != nil || len(routes) != 1 {
			return errors.New("the saved MCP connection changed while arguments were being read")
		}
	}
	result, err := routes[0].CallMCPTool(ctx, flags.Arg(0), arguments)
	if err != nil {
		return err
	}
	if err := writeMCPJSON(os.Stdout, result); err != nil {
		return err
	}
	if result.IsError {
		return errors.New("the MCP tool returned an error")
	}
	return nil
}

type mcpDestination struct {
	imagegen.Route
	ID       string
	Position int
}

func mcpRoutes(server, endpoint, tokenEnv string) ([]mcpDestination, error) {
	if endpoint != "" {
		return []mcpDestination{{Route: imagegen.Route{Endpoint: endpoint, TokenEnv: tokenEnv}}}, nil
	}
	config, err := imagegen.Load()
	var imageRoutes []imagegen.Route
	if err == nil {
		imageRoutes = config.Routes()
	} else if !errors.Is(err, imagegen.ErrNotConnected) {
		return nil, err
	}
	servers, err := imagegen.LoadMCPServers()
	if err != nil {
		return nil, err
	}
	if len(servers)+len(imageRoutes) == 0 {
		return nil, errors.New("save a server with freeway mcp connect or specify --url")
	}
	var destinations []mcpDestination
	seen := make(map[[3]string]bool)
	for index, route := range append(slices.Clone(imageRoutes), servers...) {
		identity := [3]string{route.Endpoint, route.TokenEnv, route.OAuthID}
		if seen[identity] {
			continue
		}
		seen[identity] = true
		digest := sha256.Sum256([]byte(strings.Join(identity[:], "\x00")))
		id := fmt.Sprintf("%x", digest[:8])
		if server == "" || server == id || server == route.Endpoint {
			position := 0
			if index < len(imageRoutes) {
				position = index + 1
			}
			destinations = append(destinations, mcpDestination{Route: route, ID: id, Position: position})
		}
	}
	if len(destinations) == 0 {
		return nil, errors.New("no saved MCP connection matches --server. Run freeway mcp tools to list saved servers")
	}
	return destinations, nil
}

func listMCPTools(ctx context.Context, routes []mcpDestination) error {
	type listing struct {
		Server   string          `json:"server,omitempty"`
		Position int             `json:"position,omitempty"`
		Endpoint string          `json:"endpoint"`
		Tools    []imagegen.Tool `json:"tools"`
		Error    string          `json:"error,omitempty"`
	}
	results := make([]listing, len(routes))
	var checks sync.WaitGroup
	for index, route := range routes {
		checks.Go(func() {
			tools, err := route.MCPTools(ctx)
			results[index] = listing{Server: route.ID, Position: route.Position, Endpoint: route.Endpoint, Tools: tools}
			if err != nil {
				results[index].Error = err.Error()
			}
		})
	}
	checks.Wait()
	if err := writeMCPJSON(os.Stdout, results); err != nil {
		return err
	}
	for _, result := range results {
		if result.Error == "" {
			return nil
		}
	}
	return errors.New("could not list tools on any selected MCP server")
}

func readMCPArguments(ctx context.Context, path string, stdin *os.File) ([]byte, error) {
	if path == "" {
		return []byte("{}"), nil
	}
	type inputResult struct {
		data []byte
		err  error
	}
	result := make(chan inputResult, 1)
	go func() {
		data, err := readMCPArgumentFile(path, stdin)
		result <- inputResult{data: data, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case input := <-result:
		return input.data, input.err
	}
}

func readMCPArgumentFile(path string, stdin *os.File) ([]byte, error) {
	input := stdin
	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		input = file
	}
	return io.ReadAll(io.LimitReader(input, imagegen.MaxToolArguments+1))
}

func writeMCPJSON(output io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = io.WriteString(output, terminaljson.Escape(data)+"\n")
	return err
}
