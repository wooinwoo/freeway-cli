// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/imagegen"
	"github.com/wooinwoo/freeway-cli/cli/tui"
)

func manageMCPServers(ctx context.Context, prefs tui.Preferences, options []tea.ProgramOption, directAdd bool) error {
	t := prefs.Text
	for {
		servers, err := imagegen.LoadMCPServers()
		if err != nil {
			return err
		}
		if directAdd {
			if len(servers) == imagegen.MaxMCPServers {
				return errors.New(t("Manage saved servers to make room for a new connection.", "새 MCP 서버를 추가하려면 저장된 연결을 관리하세요."))
			}
			return editMCPServer(ctx, prefs, options, servers, len(servers), "")
		}
		dialog := tui.SetupDialog{Title: t("MCP servers", "MCP 서버")}
		for index, server := range servers {
			dialog.Options = append(dialog.Options, tui.SetupOption{
				ID: strconv.Itoa(index), Label: server.Endpoint, Check: mcpStatusCheck(prefs, server),
			})
		}
		if len(servers) < imagegen.MaxMCPServers {
			dialog.Options = append(dialog.Options, tui.SetupOption{ID: "add", Label: t("Add MCP server", "MCP 서버 추가")})
		}
		dialog.Options = append(dialog.Options, tui.SetupOption{ID: "image", Label: t("Manage image tools · F9", "이미지 도구 관리 · F9")})
		choice, err := dialog.Run(prefs, options...)
		if err != nil || choice == "" {
			return err
		}
		switch choice {
		case "add":
			err = editMCPServer(ctx, prefs, options, servers, len(servers), "")
		case "image":
			_, err = configureImageTool(ctx, prefs, options)
		default:
			index, _ := strconv.Atoi(choice)
			err = manageMCPServer(ctx, prefs, options, servers, index)
		}
		if err != nil {
			return err
		}
	}
}

func mcpStatusCheck(prefs tui.Preferences, server imagegen.Route) func(context.Context) (string, string) {
	t := prefs.Text
	return func(ctx context.Context) (string, string) {
		tools, err := server.MCPTools(ctx)
		switch {
		case err == nil:
			return t("Connected", "연결됨"), fmt.Sprintf(t("Advertised tools · %d", "도구 %d개"), len(tools))
		case errors.Is(err, imagegen.ErrBrowserLoginRequired):
			return t("Sign-in required", "로그인 필요"), t("Select the server and sign in again.", "서버를 선택해 다시 로그인하세요.")
		case errors.Is(err, imagegen.ErrBrowserRefreshUnavailable):
			return t("Renewal failed", "갱신 실패"), t("Check the connection again in a moment.", "잠시 뒤 연결 상태를 다시 확인하세요.")
		case errors.Is(err, imagegen.ErrTokenUnavailable):
			return t("Token required", "토큰 필요"), server.TokenEnv
		case errors.Is(err, imagegen.ErrAuthRequired):
			return t("Authorization required", "인증 필요"), t("Check the token and tool permissions.", "토큰과 도구 접근 권한을 확인하세요.")
		default:
			return t("Connection failed", "연결 실패"), t("Check the server URL and try again.", "서버 URL을 확인하고 다시 시도하세요.")
		}
	}
}

func manageMCPServer(ctx context.Context, prefs tui.Preferences, options []tea.ProgramOption, servers []imagegen.Route, index int) error {
	t := prefs.Text
	server := servers[index]
	dialog := tui.SetupDialog{
		Title: t("MCP server", "MCP 서버"), Detail: server.Endpoint, Check: mcpStatusCheck(prefs, server),
		Options: []tui.SetupOption{
			{ID: "check", Label: t("Check connection again", "연결 상태 다시 확인")},
			{ID: "url", Label: t("Change server URL", "서버 URL 변경")},
			{ID: "auth", Label: t("Set Bearer token", "Bearer 토큰 설정")},
			{ID: "remove", Label: t("Remove server", "서버 삭제")},
		},
	}
	if strings.HasPrefix(server.Endpoint, "https://") {
		dialog.Options = append(dialog.Options, tui.SetupOption{ID: "browser", Label: t("Browser sign-in", "브라우저로 로그인")})
	}
	for {
		choice, err := dialog.Run(prefs, options...)
		if err != nil || choice == "" {
			return err
		}
		dialog.Selected = choice
		switch choice {
		case "check":
			continue
		case "remove":
			ok, err := confirmAction(prefs, options, t("Remove this MCP server?", "이 MCP 서버를 삭제할까요?"), server.Endpoint, t("Remove server", "서버 삭제"))
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			return saveMCPServerChanges(prefs, servers, slices.Delete(slices.Clone(servers), index, index+1), 0, nil)
		case "url":
			return editMCPServer(ctx, prefs, options, servers, index, "")
		default:
			return editMCPServer(ctx, prefs, options, servers, index, choice)
		}
	}
}

func editMCPServer(ctx context.Context, prefs tui.Preferences, options []tea.ProgramOption, servers []imagegen.Route, index int, authMode string) error {
	t := prefs.Text
	current := imagegen.Route{}
	if index < len(servers) {
		current = servers[index]
	}
	address := tui.SetupDialog{
		Title:  t("Connect an MCP server", "MCP 서버 연결"),
		Detail: t("Enter a trusted Streamable HTTP endpoint.", "신뢰할 수 있는 Streamable HTTP 서버 URL을 입력하세요."),
		Input:  "https://example.com/mcp", Value: current.Endpoint,
	}
	for {
		candidate := current
		if authMode == "" {
			endpoint, err := address.Run(prefs, options...)
			if err != nil || endpoint == "" {
				return err
			}
			address.Value = endpoint
			if endpoint != current.Endpoint {
				candidate = reusableMCPConnection(endpoint)
			}
		}
		if slices.ContainsFunc(servers, func(route imagegen.Route) bool {
			return route.Endpoint == candidate.Endpoint && route.Endpoint != current.Endpoint
		}) {
			address.Notice = t("This MCP server is already saved.", "이미 저장된 MCP 서버입니다.")
			continue
		}
		_, grant, err := discoverMCPTools(ctx, prefs, options, &candidate, authMode, false)
		if errors.Is(err, errMCPAuthCanceled) {
			return nil
		}
		if err != nil {
			if authMode != "" {
				return err
			}
			address.Notice = t("Could not list tools. Check the URL and server.", "도구 목록을 읽지 못했습니다. URL과 서버를 확인하세요.")
			continue
		}
		next := slices.Clone(servers)
		if index == len(next) {
			next = append(next, candidate)
		} else {
			next[index] = candidate
		}
		if err := saveMCPServerChanges(prefs, servers, next, index, grant); err != nil {
			return err
		}
		fmt.Println(t("MCP server saved.", "MCP 서버를 저장했습니다."))
		return nil
	}
}

func reusableMCPConnection(endpoint string) imagegen.Route {
	guest := imagegen.Route{Endpoint: endpoint}
	routes, _ := imagegen.LoadMCPServers()
	if image, err := imagegen.Load(); err == nil {
		routes = append(routes, image.Routes()...)
	}
	var connection imagegen.Route
	for _, route := range routes {
		if route.Endpoint != endpoint {
			continue
		}
		route.Tool = ""
		if connection.Endpoint != "" && connection != route {
			return guest
		}
		connection = route
	}
	if connection.Endpoint == "" {
		return guest
	}
	return connection
}

func saveMCPServerChanges(prefs tui.Preferences, previous, next []imagegen.Route, index int, grant *imagegen.OAuthGrant) error {
	err := imagegen.SaveMCPServers(previous, next, index, grant)
	if errors.Is(err, imagegen.ErrConfigChanged) {
		return errors.New(prefs.Text("MCP settings changed in another window. Open MCP management again.", "다른 창에서 MCP 설정이 바뀌었습니다. MCP 관리를 다시 열어 주세요."))
	}
	return err
}
