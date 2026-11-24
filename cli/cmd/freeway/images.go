// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/imagegen"
	"github.com/wooinwoo/freeway-cli/cli/tui"
)

func runImageCommand(ctx context.Context, args []string, prefs tui.Preferences) error {
	if len(args) != 1 {
		return errors.New("use freeway image connect, status or disconnect")
	}
	switch args[0] {
	case "connect":
		if !interactiveTerminal() {
			return errors.New("freeway image connect requires an interactive terminal")
		}
		_, err := configureImageTool(ctx, prefs, terminalOptions(ctx, prefs.NoColor))
		return err
	case "status":
		config, err := imagegen.Load()
		if errors.Is(err, imagegen.ErrNotConnected) {
			fmt.Println(prefs.Text("Image MCP is disconnected.", "이미지 MCP가 연결되지 않았습니다."))
			return nil
		}
		if err != nil {
			return err
		}
		routes := config.Routes()
		summaries := make([]string, len(routes))
		ready := make([]bool, len(routes))
		var checks sync.WaitGroup
		for i, route := range routes {
			checks.Go(func() {
				summary, err := imageConnectionStatus(ctx, prefs, route)
				summaries[i], ready[i] = summary, err == nil
			})
		}
		checks.Wait()
		if !slices.Contains(ready, true) {
			return errors.New(strings.Join(summaries, "\n"))
		}
		for i, summary := range summaries {
			if len(summaries) == 1 {
				fmt.Println(summary)
			} else {
				fmt.Printf("%d. %s\n", i+1, summary)
			}
		}
		return nil
	case "disconnect":
		if err := imagegen.Disconnect(); err != nil {
			return err
		}
		fmt.Println(prefs.Text("Image MCP disconnected.", "이미지 MCP 연결을 해제했습니다."))
		return nil
	default:
		return errors.New("use freeway image connect, status or disconnect")
	}
}

func imageConnectionStatus(ctx context.Context, prefs tui.Preferences, config imagegen.Route) (string, error) {
	err := config.Check(ctx)
	_, status := imageStatusText(prefs, config, err)
	return status + " · " + config.Tool + " · " + config.Endpoint, err
}

func imageStatusText(prefs tui.Preferences, config imagegen.Route, err error) (status, detail string) {
	t := prefs.Text
	switch {
	case err == nil:
		return t("Connected", "연결됨"), t("Image MCP connected", "이미지 MCP 연결됨")
	case errors.Is(err, imagegen.ErrAuthRequired):
		return t("Needs authorization", "인증 필요"), t("Image MCP requires authorization. Check the token and permissions.", "이미지 MCP 인증이 필요합니다. 토큰과 권한을 확인하세요.")
	case errors.Is(err, imagegen.ErrTokenUnavailable):
		return t("Needs token", "토큰 필요"), t("Set the token environment variable", "토큰 환경변수를 설정하세요") + " " + config.TokenEnv
	case errors.Is(err, imagegen.ErrBrowserLoginRequired):
		return t("Sign-in required", "로그인 필요"), t("Image MCP sign-in expired or is missing. Sign in again in F9.", "이미지 MCP 로그인 만료 또는 미등록 · F9에서 다시 로그인하세요.")
	case errors.Is(err, imagegen.ErrBrowserRefreshUnavailable):
		return t("Renewal failed", "갱신 실패"), t("Could not renew the image MCP sign-in. Try checking again shortly.", "이미지 MCP 로그인을 갱신하지 못했습니다. 잠시 후 다시 확인하세요.")
	case errors.Is(err, imagegen.ErrToolUnavailable):
		return t("Tool unavailable", "도구 없음"), t("The saved image tool is unavailable. Select another tool.", "저장된 이미지 도구가 없습니다. 도구를 다시 선택하세요.")
	default:
		return t("Unreachable", "연결 실패"), t("Could not reach the image MCP tool.", "이미지 MCP 도구에 연결하지 못했습니다.")
	}
}

func configureImageTool(ctx context.Context, prefs tui.Preferences, options []tea.ProgramOption) (bool, error) {
	t := prefs.Text
	config, err := imagegen.Load()
	if err != nil && !errors.Is(err, imagegen.ErrNotConnected) {
		return false, err
	}
	var routes []imagegen.Route
	if err == nil {
		routes = config.Routes()
	}
	previous := slices.Clone(routes)
	check := func(route imagegen.Route) func(context.Context) (string, string) {
		return func(ctx context.Context) (string, string) {
			err := route.Check(ctx)
			status, detail := imageStatusText(prefs, route, err)
			if err == nil {
				detail = ""
			}
			return status, detail
		}
	}
	save := func(next []imagegen.Route, index int, grant *imagegen.OAuthGrant) error {
		var err error
		if grant == nil {
			err = imagegen.SaveRoutes(previous, next)
		} else {
			err = imagegen.SaveOAuthRoute(previous, next, index, grant)
		}
		switch {
		case errors.Is(err, imagegen.ErrConfigChanged):
			return errors.New(t("Image settings changed in another window. Reopen F9 to try again.", "다른 창에서 이미지 설정이 변경됐습니다. F9을 다시 열어 주세요."))
		case errors.Is(err, imagegen.ErrDuplicateRoute):
			return errors.New(t("This image MCP tool is already connected.", "이미 연결한 이미지 MCP 도구입니다."))
		case errors.Is(err, imagegen.ErrBrowserLoginRequired):
			return errors.New(t("The approval expired. Sign in again in F9.", "승인이 만료됐습니다. F9에서 다시 로그인하세요."))
		default:
			return err
		}
	}
	index := 0
	if len(routes) > 1 {
		list := tui.SetupDialog{Title: t("Image MCP · Auto order", "이미지 MCP · Auto 연결 순서")}
		for i, route := range routes {
			label := fmt.Sprintf("%d. %s · %s", i+1, route.Tool, route.Host())
			list.Options = append(list.Options, tui.SetupOption{ID: strconv.Itoa(i), Label: label, Detail: route.Endpoint, Check: check(route)})
		}
		if len(routes) < imagegen.MaxRoutes {
			list.Options = append(list.Options, tui.SetupOption{ID: "add", Label: t("Add image tool", "이미지 도구 추가")})
		}
		choice, err := list.Run(prefs, options...)
		if err != nil || choice == "" {
			return false, err
		}
		if choice == "add" {
			index = len(routes)
		} else {
			index, _ = strconv.Atoi(choice)
		}
	}
	var current imagegen.Route
	authMode := ""
	if index < len(routes) {
		current = routes[index]
		manage := tui.SetupDialog{
			Title:  t("Image MCP", "이미지 MCP"),
			Detail: current.Tool + " · " + current.Endpoint,
			Check:  check(current),
			Options: []tui.SetupOption{
				{ID: "change", Label: t("Change server or tool", "서버 또는 도구 변경")},
				{ID: "disconnect", Label: t("Disconnect", "연결 해제")},
				{ID: "check", Label: t("Check connection again", "연결 상태 다시 확인")},
				{ID: "auth", Label: t("Set Bearer token", "Bearer 토큰 설정")},
			},
		}
		if index > 0 {
			manage.Options = append(manage.Options, tui.SetupOption{ID: "first", Label: t("Try this tool first", "이 도구를 가장 먼저 사용")})
		}
		if len(routes) == 1 {
			manage.Options = append(manage.Options, tui.SetupOption{ID: "add", Label: t("Add image tool", "이미지 도구 추가")})
		}
		if strings.HasPrefix(current.Endpoint, "https://") {
			manage.Options = append(manage.Options, tui.SetupOption{ID: "browser", Label: t("Browser sign-in", "브라우저로 로그인")})
		}
		for {
			choice, err := manage.Run(prefs, options...)
			if err != nil || choice == "" {
				return false, err
			}
			if choice == "check" {
				manage.Selected = "check"
				continue
			}
			if choice == "disconnect" {
				manage.Selected = "disconnect"
				confirmation := current.Tool + " · " + current.Host()
				if current.OAuthID != "" {
					confirmation += "\n" + t("Freeway removes the saved browser login for this tool.", "이 도구의 브라우저 로그인을 Freeway에서 삭제합니다.")
				}
				confirmed, err := confirmAction(prefs, options, t("Disconnect this image tool?", "이 이미지 도구의 연결을 해제할까요?"), confirmation, t("Disconnect", "연결 해제"))
				if err != nil {
					return false, err
				}
				if !confirmed {
					continue
				}
				if err := save(slices.Delete(routes, index, index+1), 0, nil); err != nil {
					return false, err
				}
				fmt.Println(t("Image MCP disconnected.", "이미지 MCP 연결을 해제했습니다."))
				return false, nil
			}
			if choice == "first" {
				if err := save(slices.Insert(slices.Delete(routes, index, index+1), 0, current), 0, nil); err != nil {
					return false, err
				}
				fmt.Println(t("Selected image tool will be tried first.", "선택한 이미지 도구를 가장 먼저 사용합니다."))
				return true, nil
			}
			if choice == "add" {
				index, current = len(routes), imagegen.Route{}
			}
			if choice == "auth" || choice == "browser" {
				authMode = choice
			}
			break
		}
	}
	chosen, grant, err := selectImageTool(ctx, prefs, options, current, authMode)
	if err != nil || chosen.Tool == "" {
		return false, err
	}
	if index == len(routes) {
		routes = append(routes, chosen)
	} else {
		routes[index] = chosen
	}
	if err := save(routes, index, grant); err != nil {
		return false, err
	}
	fmt.Println(t("Image MCP connected.", "이미지 MCP를 연결했습니다."))
	return true, nil
}

func selectImageTool(ctx context.Context, prefs tui.Preferences, options []tea.ProgramOption, config imagegen.Route, authMode string) (imagegen.Route, *imagegen.OAuthGrant, error) {
	t := prefs.Text
	url := tui.SetupDialog{
		Title:  t("Connect an image tool", "이미지 도구 연결"),
		Detail: t("Use a trusted Streamable HTTP MCP server. It receives the image prompt.", "신뢰할 수 있는 Streamable HTTP MCP 서버를 입력하세요. 이미지 요청이 이 서버에 전송됩니다."),
		Input:  "https://example.com/mcp", Value: config.Endpoint,
	}
	for {
		candidate := config
		if authMode == "" {
			endpoint, err := url.Run(prefs, options...)
			if err != nil || endpoint == "" {
				return imagegen.Route{}, nil, err
			}
			url.Value, candidate.Endpoint = endpoint, endpoint
			if endpoint != config.Endpoint {
				candidate = reusableMCPConnection(endpoint)
			}
		}
		tools, grant, err := discoverMCPTools(ctx, prefs, options, &candidate, authMode, true)
		if errors.Is(err, errMCPAuthCanceled) {
			if authMode != "" {
				return imagegen.Route{}, nil, nil
			}
			continue
		}
		if authMode != "" && err != nil {
			return imagegen.Route{}, nil, err
		}
		authMode = ""
		if err != nil || len(tools) == 0 {
			url.Notice = t("Could not find a compatible tool. Check the URL and server.", "호환 도구를 찾지 못했습니다. URL과 서버를 확인하세요.")
			continue
		}
		pick := tui.SetupDialog{
			Title:    t("Choose an image generation tool", "이미지 생성 도구 선택"),
			Detail:   t("Select a tool that returns PNG or JPEG attachments.", "PNG 또는 JPEG를 첨부로 돌려주는 도구를 선택하세요."),
			Selected: config.Tool,
		}
		for _, tool := range tools {
			pick.Options = append(pick.Options, tui.SetupOption{ID: tool.Name, Label: tool.Name, Detail: tool.Description})
		}
		tool, err := pick.Run(prefs, options...)
		if err != nil {
			return imagegen.Route{}, nil, err
		}
		if tool == "" {
			continue
		}
		confirm := tui.SetupDialog{
			Title:  t("Confirm this tool's cost and access", "도구의 요금과 접근 확인"),
			Detail: t("Auto can send image prompts to this server when it reaches this tool in the connection order. Enable it only if the tool cannot bill you.", "이 도구의 순서가 되면 Auto가 이미지 요청을 이 서버로 보낼 수 있습니다. 청구가 발생하지 않는 도구만 연결하세요."),
			Options: []tui.SetupOption{
				{ID: "back", Label: t("Back", "뒤로")},
				{ID: "confirm", Label: t("Enable for Auto", "확인했고 Auto에서 사용")},
			},
		}
		choice, err := confirm.Run(prefs, options...)
		if err != nil {
			return imagegen.Route{}, nil, err
		}
		if choice != "confirm" {
			continue
		}
		candidate.Tool = tool
		return candidate, grant, nil
	}
}

var errMCPAuthCanceled = errors.New("MCP sign-in canceled")

func discoverMCPTools(ctx context.Context, prefs tui.Preferences, options []tea.ProgramOption, config *imagegen.Route, mode string, imageOnly bool) ([]imagegen.Tool, *imagegen.OAuthGrant, error) {
	t := prefs.Text
	auth := tui.SetupDialog{
		Title:  t("MCP Bearer token", "MCP Bearer 토큰"),
		Detail: config.Endpoint + "\n" + t("Enter the name of a FREEWAY_MCP_ environment variable already set in this terminal.", "현재 터미널에 설정한 FREEWAY_MCP_ 환경변수 이름을 입력하세요."),
		Input:  "FREEWAY_MCP_IMAGE_TOKEN", Value: config.TokenEnv, Secret: true,
	}
	authChoice := tui.SetupDialog{
		Title:  t("Connect to this MCP server", "MCP 인증 방식"),
		Detail: config.Host(),
		Options: []tui.SetupOption{
			{ID: "browser", Label: t("Browser sign-in", "브라우저로 로그인")},
			{ID: "auth", Label: t("Bearer token", "Bearer 토큰")},
		},
	}
	canUseBrowser := strings.HasPrefix(config.Endpoint, "https://")
	for {
		if mode == "browser" {
			tools, grant, err := authorizeMCPInBrowser(ctx, prefs, options, *config, imageOnly)
			if errors.Is(err, imagegen.ErrBrowserUnsupported) {
				authChoice.Notice = t("Browser sign-in requires a compatible OAuth server. You can connect with a token.", "브라우저 로그인에 호환되는 OAuth 서버가 필요합니다. 토큰으로 연결할 수 있습니다.")
				mode = "choose"
				continue
			}
			return tools, grant, err
		}
		if mode == "choose" {
			choice, err := authChoice.Run(prefs, options...)
			if err != nil {
				return nil, nil, err
			}
			if choice == "" {
				return nil, nil, errMCPAuthCanceled
			}
			mode = choice
			continue
		}
		if mode == "auth" {
			name, err := auth.Run(prefs, options...)
			if err != nil {
				return nil, nil, err
			}
			if name == "" {
				return nil, nil, errMCPAuthCanceled
			}
			config.TokenEnv, config.OAuthID = name, ""
		}
		fmt.Fprintln(os.Stderr, t("Checking the MCP server…", "MCP 서버를 확인 중입니다…"))
		loadTools := config.MCPTools
		if imageOnly {
			loadTools = config.Tools
		}
		tools, err := loadTools(ctx)
		switch {
		case errors.Is(err, imagegen.ErrAuthRequired), errors.Is(err, imagegen.ErrBrowserLoginRequired):
			auth.Notice = t("Check the token and server permissions.", "서버가 인증을 요구합니다. 토큰과 권한을 확인하세요.")
		case errors.Is(err, imagegen.ErrTokenUnavailable):
			auth.Notice = t("Enter a FREEWAY_MCP_ variable with a valid, nonempty token.", "토큰이 들어 있는 FREEWAY_MCP_ 환경변수 이름을 입력하세요.")
		default:
			return tools, nil, err
		}
		if mode == "" && canUseBrowser {
			mode = "choose"
		} else {
			mode = "auth"
		}
	}
}
