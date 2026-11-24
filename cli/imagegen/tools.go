// SPDX-License-Identifier: MIT

package imagegen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const MaxToolArguments = 64 << 10

var ErrMCPPrivateResult = errors.New("the MCP response contained a connection credential")

// MCPTools lists the tools advertised by this connection and includes their input schemas.
func (r Route) MCPTools(ctx context.Context) ([]Tool, error) {
	return r.tools(ctx, false)
}

// MCPToolsForModel omits catalog entries that reflect the credential used to list them.
func (r Route) MCPToolsForModel(ctx context.Context) ([]Tool, error) {
	session, transport, tools, err := r.prepareTools(ctx, maxListBytes, "", false, checkTransport{RoundTripper: r.transport(), ctx: ctx})
	if err != nil {
		return nil, err
	}
	defer session.Close()
	var safe []Tool
	for _, tool := range tools {
		if !containsMCPCredential(tool, transport.token) {
			safe = append(safe, tool)
		}
	}
	return safe, nil
}

// CallMCPTool checks the named tool before sending the provided JSON arguments once.
func (r Route) CallMCPTool(ctx context.Context, name string, args []byte) (*mcp.CallToolResult, error) {
	return r.callMCPTool(ctx, name, args, false)
}

// CallMCPToolForModel prevents a reflected authorization credential from reaching the model.
func (r Route) CallMCPToolForModel(ctx context.Context, name string, args []byte) (*mcp.CallToolResult, error) {
	return r.callMCPTool(ctx, name, args, true)
}

func (r Route) callMCPTool(ctx context.Context, name string, args []byte, shareWithModel bool) (*mcp.CallToolResult, error) {
	if !validToolName(name) {
		return nil, errors.New("enter a valid MCP tool name")
	}
	var arguments map[string]json.RawMessage
	if len(args) > MaxToolArguments || !utf8.Valid(args) || json.Unmarshal(args, &arguments) != nil || arguments == nil {
		return nil, errors.New("MCP arguments must be a UTF-8 JSON object up to 64 KiB")
	}
	session, transport, _, err := r.prepareTools(ctx, maxListBytes, name, false, checkTransport{RoundTripper: r.transport(), ctx: ctx})
	if err != nil {
		return nil, err
	}
	defer session.Close()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil || result == nil {
		return nil, errors.New("could not confirm the MCP result. Check the server before retrying")
	}
	if shareWithModel && containsMCPCredential(result, transport.token) {
		return nil, ErrMCPPrivateResult
	}
	return result, nil
}

func containsMCPCredential(value any, token string) bool {
	if token == "" {
		return false
	}
	data, err := json.Marshal(value)
	secret, _ := json.Marshal(token)
	return err != nil || bytes.Contains(data, secret[1:len(secret)-1])
}
