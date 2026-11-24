// SPDX-License-Identifier: MIT

package imagegen

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wooinwoo/freeway-cli/cli/internal/filelock"
)

const MaxMCPServers = 8
const mcpServersFile = "mcp-servers.json"
const maxMCPServerConfig = 32 << 10

func validMCPServer(route Route) bool {
	return validEndpoint(route.Endpoint) && validTokenEnv(route.TokenEnv) &&
		(route.OAuthID == "" || route.TokenEnv == "" && validOAuthID(route.OAuthID) && strings.HasPrefix(route.Endpoint, "https://"))
}

func validateMCPServers(servers []Route) error {
	if len(servers) > MaxMCPServers {
		return errors.New("at most eight MCP servers may be saved")
	}
	seen := make(map[string]bool, len(servers))
	for _, server := range servers {
		if server.Tool != "" || !validMCPServer(server) {
			return errors.New("invalid saved MCP server")
		}
		if seen[server.Endpoint] {
			return errors.New("this MCP server is already saved")
		}
		seen[server.Endpoint] = true
	}
	return nil
}

func LoadMCPServers() ([]Route, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	file, err := os.Open(filepath.Join(dir, mcpServersFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxMCPServerConfig+1))
	if err != nil {
		return nil, err
	}
	var servers []Route
	if len(data) > maxMCPServerConfig || json.Unmarshal(data, &servers) != nil || servers == nil {
		return nil, errors.New("invalid MCP server settings")
	}
	if err := validateMCPServers(servers); err != nil {
		return nil, err
	}
	return servers, nil
}

// SaveMCPServers updates explicitly managed servers after verifying the caller's snapshot.
func SaveMCPServers(previous, servers []Route, index int, grant *OAuthGrant) error {
	if grant != nil {
		var err error
		servers, err = bindOAuthGrant(servers, index, grant)
		if err != nil {
			return err
		}
	}
	if err := validateMCPServers(servers); err != nil {
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
	current, err := LoadMCPServers()
	if err != nil {
		return err
	}
	if !slices.Equal(previous, current) {
		return ErrConfigChanged
	}
	image, imageErr := Load()
	known := slices.Clone(current)
	if imageErr == nil {
		known = append(known, image.Routes()...)
	}
	if err := validateOAuthReferences(servers, known, index, grant); err != nil {
		return err
	}
	data, err := json.Marshal(append([]Route{}, servers...))
	if err != nil {
		return err
	}
	if len(data) > maxMCPServerConfig {
		return errors.New("the MCP server settings exceed 32 KiB")
	}
	if grant != nil {
		if err := saveOAuthGrant(dir, servers[index], grant); err != nil {
			return err
		}
	}
	if err := writeMCPConfig(dir, mcpServersFile, data); err != nil {
		if grant != nil {
			removeOAuthGrant(dir, servers[index].OAuthID)
		}
		return err
	}
	if imageErr == nil {
		servers = append(slices.Clone(servers), image.Routes()...)
	}
	if imageErr == nil || errors.Is(imageErr, ErrNotConnected) {
		pruneOAuthGrants(dir, current, servers)
	}
	return nil
}

func validateOAuthReferences(routes, known []Route, index int, grant *OAuthGrant) error {
	for i, candidate := range routes {
		if candidate.OAuthID == "" || grant != nil && i == index {
			continue
		}
		if !slices.ContainsFunc(known, func(route Route) bool {
			return route.Endpoint == candidate.Endpoint && route.OAuthID == candidate.OAuthID
		}) {
			return ErrConfigChanged
		}
	}
	return nil
}
