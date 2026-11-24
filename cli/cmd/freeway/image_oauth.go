// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/url"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/wooinwoo/freeway-cli/cli/imagegen"
	"github.com/wooinwoo/freeway-cli/cli/tui"
)

func authorizeMCPInBrowser(ctx context.Context, prefs tui.Preferences, options []tea.ProgramOption, route imagegen.Route, imageOnly bool) ([]imagegen.Tool, *imagegen.OAuthGrant, error) {
	if !strings.HasPrefix(route.Endpoint, "https://") {
		return nil, nil, imagegen.ErrBrowserUnsupported
	}
	callback, err := startBrowserCallback("127.0.0.1", rand.Text(), "", false, prefs)
	if err != nil {
		return nil, nil, err
	}
	defer callback.close()
	t := prefs.Text
	fmt.Fprintln(os.Stderr, t("Checking browser sign-in support…", "브라우저 로그인 지원을 확인 중입니다…"))
	return route.Authorize(ctx, callback.url, func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		address, err := url.Parse(args.URL)
		if err != nil || address.Query().Get("state") == "" {
			return nil, imagegen.ErrBrowserUnsupported
		}
		detail := route.Host() + "\n" + t("Sign-in site", "로그인 사이트") + " · " + address.Host
		if scope := address.Query().Get("scope"); scope != "" {
			detail += "\n" + t("Requested scopes", "요청 권한") + " · " + scope
		}
		ok, err := confirmAction(prefs, options, t("Open MCP sign-in in your browser?", "MCP 로그인 사이트를 열까요?"), detail, t("Open browser", "브라우저 열기"))
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errMCPAuthCanceled
		}
		callback.state.Store(address.Query().Get("state"))
		if err := openProviderURL(ctx, args.URL); err != nil {
			fmt.Fprintln(os.Stderr, args.URL)
		}
		wait := tui.SetupDialog{
			Title:   t("Approve MCP access in your browser", "브라우저에서 MCP 접근 승인"),
			Detail:  detail,
			Options: []tui.SetupOption{{ID: "cancel", Label: t("Cancel sign-in", "로그인 취소")}},
			Wait: func() string {
				select {
				case <-ctx.Done():
					return "timeout"
				case <-callback.finished:
					return "finished"
				}
			},
		}
		choice, err := wait.Run(prefs, options...)
		if err != nil {
			return nil, err
		}
		if choice == "timeout" {
			return nil, context.DeadlineExceeded
		}
		if choice != "finished" || callback.denied {
			return nil, errMCPAuthCanceled
		}
		return &auth.AuthorizationResult{Code: callback.code, State: address.Query().Get("state"), Iss: callback.issuer}, nil
	}, imageOnly)
}
