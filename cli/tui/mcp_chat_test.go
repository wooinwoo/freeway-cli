// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wooinwoo/freeway-cli/cli/client"
	"github.com/wooinwoo/freeway-cli/cli/imagegen"
)

type mcpTestGateway struct {
	requests [][]client.Message
	proposal string
}

func (*mcpTestGateway) Models(context.Context) ([]client.Model, error) { return nil, nil }

func (g *mcpTestGateway) Stream(_ context.Context, model string, messages []client.Message, emit func(client.Event)) error {
	g.requests = append(g.requests, messages)
	text := "The fixture result was 42."
	if messages[0].Content == chooseMCPTool {
		text = g.proposal
	}
	emit(client.Event{Type: "done", Model: model, Provider: "fixture", Text: text})
	return nil
}

func TestMCPChatRequiresApprovalAndUsesOnlyApprovedTool(t *testing.T) {
	for _, scenario := range []string{"approve", "cancel", "removed", "unlisted", "reflected"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range []string{"XDG_CONFIG_HOME", "HOME", "APPDATA"} {
				t.Setenv(name, dir)
			}
			const credential = "fixture-bearer-token<&"
			t.Setenv("FREEWAY_MCP_CHAT_TEST", credential)
			var calls atomic.Int32
			server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
			server.AddTool(&mcp.Tool{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","default":"private-example","enum":["specific","alternate"]},"format":{"const":"v1"}},"required":["query"]}`)},
				func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					calls.Add(1)
					var args map[string]string
					if err := json.Unmarshal(req.Params.Arguments, &args); err != nil || args["query"] != "specific" {
						t.Error("tool did not receive the approved arguments")
					}
					result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "actual lookup data: 42"}}}
					if scenario == "reflected" {
						result.StructuredContent = map[string]string{"authorization": "Bearer " + credential}
					}
					return result, nil
				})
			server.AddTool(&mcp.Tool{Name: "reflect", Description: credential, InputSchema: json.RawMessage(`{"type":"object"}`)},
				func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					t.Error("a metadata entry with a reflected credential was sent to the model")
					return nil, nil
				})
			handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
			host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+credential {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				handler.ServeHTTP(w, r)
			}))
			defer host.Close()
			saved := []imagegen.Route{{Endpoint: host.URL, TokenEnv: "FREEWAY_MCP_CHAT_TEST"}}
			if err := imagegen.SaveMCPServers(nil, saved, 0, nil); err != nil {
				t.Fatal(err)
			}
			g := &mcpTestGateway{proposal: `{"tool":1,"arguments":{"query":"specific"}}`}
			if scenario == "unlisted" {
				g.proposal = `{"tool":2,"arguments":{"query":"specific"}}`
			}
			model := update(NewConnected(t.Context(), g), tea.WindowSizeMsg{Width: 80, Height: 24})
			model.ready = true
			model.selected = "fixture/free"
			model.messages = []client.Message{{Role: "user", Content: "Previous conversation data"}}
			model.prompt.SetValue("/mcp-ask Check the value.")
			next, cmd := model.submit()
			model = next.(Model)
			model = update(model, cmd())
			if calls.Load() != 0 || len(g.requests) != 1 {
				t.Fatal("planning called a tool or included unrelated history or endpoints")
			}
			for _, hidden := range []string{host.URL, "Previous conversation data", "private-example", "fixture-bearer-token", `"reflect"`} {
				if strings.Contains(g.requests[0][1].Content, hidden) {
					t.Errorf("model received data outside the question and tool summary: %q", hidden)
				}
			}
			if summary := g.requests[0][1].Content; !strings.Contains(summary, `"enum":["specific","alternate"]`) || !strings.Contains(summary, `"const":"v1"`) {
				t.Fatal("the tool summary lost its allowed argument values")
			}
			if scenario == "unlisted" {
				if model.mcpApproval != nil || model.busy {
					t.Fatal("an unlisted tool reached approval")
				}
				return
			}
			view := ansi.Strip(model.View().Content)
			for _, value := range []string{"Approve MCP tool call", "fixture/free", host.URL, `"lookup"`, `"specific"`} {
				if !strings.Contains(view, value) {
					t.Fatalf("approval hid %q:\n%s", value, view)
				}
			}
			if scenario == "cancel" {
				model = update(model, press(tea.KeyEsc))
				if model.mcpApproval != nil || model.busy || calls.Load() != 0 {
					t.Fatal("cancellation sent the tool request")
				}
				return
			}
			if scenario == "removed" {
				if err := imagegen.SaveMCPServers(saved, nil, 0, nil); err != nil {
					t.Fatal(err)
				}
			}
			model = update(model, press(tea.KeyEnter))
			model = update(model, press('y'))
			if model.mcpApproval == nil || calls.Load() != 0 {
				t.Fatal("Enter must not approve the tool by default")
			}
			next, cmd = model.Update(press(tea.KeyF8))
			model = next.(Model)
			for cmd != nil && model.busy {
				next, cmd = model.Update(cmd())
				model = next.(Model)
			}
			if scenario == "removed" {
				if calls.Load() != 0 || len(g.requests) != 1 {
					t.Fatal("a removed server was invoked")
				}
				return
			}
			if scenario == "reflected" {
				if calls.Load() != 1 || len(g.requests) != 1 || !strings.Contains(model.notice, "connection credential") {
					t.Fatal("a reflected credential did not stop sharing the tool result")
				}
				return
			}
			if calls.Load() != 1 || len(g.requests) != 2 || !strings.Contains(g.requests[1][1].Content, "actual lookup data: 42") ||
				!strings.Contains(model.messages[len(model.messages)-1].Content, "The fixture result was 42.") {
				t.Fatal("approval did not call the tool once and use its result in the final reply")
			}
		})
	}
}

func TestMCPApprovalRendersArgumentsAgainAfterResize(t *testing.T) {
	model := update(New(), tea.WindowSizeMsg{Width: 44, Height: 12})
	_, cancel := context.WithCancel(t.Context())
	defer cancel()
	model.busy, model.cancel = true, cancel
	args, err := json.Marshal(map[string]string{"query": strings.Repeat("0123456789", 180)})
	if err != nil {
		t.Fatal(err)
	}
	plan := &mcpPlan{mcpToolChoice: mcpToolChoice{route: imagegen.Route{Endpoint: "https://mcp.fixture.test"}, name: "lookup"}, arguments: args}
	model = update(model, mcpPlanLoaded{plan: plan})
	model.mcpApproval.view.GotoBottom()
	model = update(model, tea.WindowSizeMsg{Width: 100, Height: 16})
	if model.mcpApproval.view.AtBottom() || !strings.Contains(ansi.Strip(model.View().Content), plan.route.Endpoint) {
		t.Fatal("resizing must reveal the destination again before approving long arguments")
	}
	next, _ := model.Update(press(tea.KeyF8))
	if next.(Model).mcpApproval == nil {
		t.Fatal("F8 approved while the resized argument view was incomplete")
	}
}
