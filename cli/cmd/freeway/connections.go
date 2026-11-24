package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/client"
	"github.com/wooinwoo/freeway-cli/cli/imagegen"
	"github.com/wooinwoo/freeway-cli/cli/tui"
)

type providerConnection struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	AuthURL           string `json:"authUrl"`
	RequiresCLI       bool   `json:"requiresCLI"`
	NeedsConfirmation bool   `json:"needsConfirmation"`
	Catalog           bool   `json:"catalog"`
}

func setupConnections(ctx context.Context, gateway *client.HTTP, prefs *tui.Preferences, options []tea.ProgramOption, startMode, providerID string) (string, []client.ProviderStatus, error) {
	t := prefs.Text
	startup := startMode != ""
	if startup {
		fmt.Fprintln(os.Stderr, t("Checking provider connections…", "공급처 연결 확인 중…"))
	}
	var selected, notice string
	var statuses []client.ProviderStatus
	var err error
	if providerID == "" {
		statuses, err = gateway.ProviderStatuses(ctx)
	} else {
		fmt.Fprintln(os.Stderr, t("Checking the selected provider…", "선택한 공급처를 확인 중입니다…"))
		status, checkErr := gateway.ProviderStatus(ctx, providerID)
		err = checkErr
		if err == nil {
			statuses = []client.ProviderStatus{status}
		} else if providerGatewayError(err) == "unknown_provider" {
			return "", nil, errors.New(t("This provider is disabled in the local gateway.", "로컬 게이트웨이에서 이 공급처를 사용할 수 없습니다."))
		}
	}
	if err != nil {
		if !startup {
			return "", nil, errors.New(t("Could not check provider connections. Start Freeway again to retry.", "공급처 연결 상태를 확인하지 못했습니다. Freeway를 다시 실행해 보세요."))
		}
		if models, err := gateway.Models(ctx); err == nil && len(models) > 0 {
			return startMode, nil, nil
		}
		notice = t("Could not check provider connections.", "공급처 연결 상태를 확인하지 못했습니다.")
	}
	ready, keyReady := connectionReadiness(statuses)
	if startup && ready && (prefs.SetupDone || keyReady) {
		return startMode, statuses, nil
	}
	if startup && startMode == "new" && !ready {
		if _, err := imagegen.Load(); err == nil {
			return startMode, statuses, nil
		}
	}
	binary, err := nativeBinary("freeway-providers")
	if err != nil {
		return "", nil, err
	}
	cmd := exec.CommandContext(ctx, binary, "list")
	cmd.Env = imagegen.WithoutTokenEnv()
	data, err := cmd.Output()
	if err != nil {
		return "", nil, err
	}
	var inventory struct {
		Providers  []providerConnection `json:"providers"`
		CLIProfile string               `json:"cliProfile"`
	}
	if err := json.Unmarshal(data, &inventory); err != nil {
		return "", nil, err
	}
	providers := make(map[string]providerConnection, len(inventory.Providers))
	for _, provider := range inventory.Providers {
		providers[provider.ID] = provider
	}
	if providerID != "" {
		for _, status := range statuses {
			if status.ID == providerID {
				return "", nil, configureProvider(ctx, *prefs, options, binary, gateway, providers[providerID], status, inventory.CLIProfile)
			}
		}
		return "", nil, errors.New(t("This provider is disabled in the local gateway.", "로컬 게이트웨이에서 이 공급처를 사용할 수 없습니다."))
	}
	history := tui.SetupOption{ID: "history", Label: t("Open saved conversations", "저장한 대화 열기"), Detail: t("Read or export your saved work while offline.", "연결 없이 저장된 대화를 읽거나 내보낼 수 있습니다.")}
	for {
		dialog := tui.SetupDialog{
			Title:    t("Connect a model provider", "모델 공급처 연결"),
			Detail:   t("Choose a provider to sign in or add an API key.", "공급처를 선택해 로그인하거나 API 키를 등록하세요."),
			Selected: selected,
			Notice:   notice,
		}
		ready, _ = connectionReadiness(statuses)
		if startup {
			// Esc skips the guide instead of quitting. Ctrl+C still quits.
			dialog.EscapeID = "skip"
			dialog.Help = t("↑↓ move  ·  Enter select  ·  Esc chat  ·  Ctrl+C quit", "↑↓ 이동  ·  Enter 선택  ·  Esc 채팅으로  ·  Ctrl+C 종료")
			if ready {
				dialog.EscapeID = "continue"
			}
		}
		if startup && ready {
			dialog.Options = append(dialog.Options, tui.SetupOption{ID: "continue", Label: t("Start chat", "채팅 시작"), Detail: t("Use the free models already available.", "지금 사용할 수 있는 무료 모델로 시작합니다.")})
		} else if startup {
			dialog.Options = append(dialog.Options, history, tui.SetupOption{
				ID: "image-mcp", Label: t("Connect an image tool", "이미지 도구 연결"),
				Detail: t("Use a free image generation tool over MCP.", "MCP로 무료 이미지 생성 도구를 연결합니다."),
			})
			if startMode == "new" && dialog.Selected == "" && len(statuses) > 0 {
				dialog.Selected = statuses[0].ID
			}
		}
		for _, status := range statuses {
			label := status.Name + " · " + tui.ProviderConnectionText(*prefs, status)
			dialog.Options = append(dialog.Options, tui.SetupOption{ID: status.ID, Label: label})
		}
		dialog.Options = append(dialog.Options, tui.SetupOption{ID: "refresh", Label: t("Check connections again", "연결 상태 새로고침")})
		choice, err := dialog.Run(*prefs, options...)
		if err != nil || choice == "" {
			return "", nil, err
		}
		if choice == "continue" {
			prefs.SetupDone = true
			if err := prefs.Save(); err != nil {
				fmt.Fprintln(os.Stderr, t("Could not save setup preferences.", "연결 안내 설정을 저장하지 못했습니다."))
			}
			return startMode, statuses, nil
		}
		if choice == "skip" {
			return startMode, statuses, nil
		}
		if choice == "history" {
			if startMode == "new" {
				startMode = "resume"
			}
			return startMode, statuses, nil
		}
		if choice == "image-mcp" {
			connected, err := configureImageTool(ctx, *prefs, options)
			if err != nil {
				return "", nil, err
			}
			if connected {
				return "new", statuses, nil
			}
			selected = choice
			continue
		}
		for _, status := range statuses {
			if status.ID == choice {
				if err := configureProvider(ctx, *prefs, options, binary, gateway, providers[choice], status, inventory.CLIProfile); err != nil {
					return "", nil, err
				}
				break
			}
		}
		refreshed, err := gateway.ProviderStatuses(ctx)
		selected, notice = choice, ""
		if err == nil {
			statuses = refreshed
		} else {
			notice = t("Could not refresh the connection status.", "연결 상태를 새로고침하지 못했습니다.")
		}
	}
}

func connectionReadiness(statuses []client.ProviderStatus) (ready, keyReady bool) {
	for _, status := range statuses {
		if status.State == "ready" && status.FreeModels > 0 {
			ready = true
			keyReady = keyReady || status.KeyConfigured
		}
	}
	return ready, keyReady
}

func configureProvider(ctx context.Context, prefs tui.Preferences, options []tea.ProgramOption, binary string, gateway *client.HTTP, provider providerConnection, status client.ProviderStatus, cliProfile string) error {
	t := prefs.Text
	if provider.ID == "" {
		return errors.New(t("Connection setup is unavailable for this provider.", "이 공급처는 연결 안내를 지원하지 않습니다."))
	}
	dialog := tui.SetupDialog{Title: provider.Name, Notice: tui.ProviderConnectionText(prefs, status)}
	if !status.Management {
		dialog.Detail = t("Connect to an automatically started gateway from the current Freeway version to make changes.", "연결을 변경하려면 현재 Freeway 버전의 자동 시작 게이트웨이에 연결하세요.")
		dialog.Options = []tui.SetupOption{{ID: "back", Label: t("Back", "뒤로")}}
		_, err := dialog.Run(prefs, options...)
		return err
	}
	switch {
	case provider.RequiresCLI && (cliProfile == "" || cliProfile != status.CLIProfile):
		dialog.Detail = t("Use the same CLI profile as the running gateway to install or sign in.", "CLI를 설치하거나 로그인하려면 현재 게이트웨이와 같은 셸 환경에서 실행하세요.")
	case provider.RequiresCLI:
		dialog.Detail = t("Connect using the provider's official CLI.", "공식 CLI로 계정을 연결합니다.")
		if status.State == "opencode_not_installed" {
			dialog.Options = append(dialog.Options, tui.SetupOption{ID: "install", Label: t("Install official CLI", "공식 CLI 설치")})
		}
		label := t("Sign in with the official CLI", "공식 CLI로 로그인")
		if status.State == "ready" || status.State == "connection_rejected" {
			label = t("Sign in again with the official CLI", "공식 CLI로 다시 로그인")
		}
		dialog.Options = append(dialog.Options, tui.SetupOption{ID: "login", Label: label})
	default:
		dialog.Detail = t("Open the provider's site to create a key, then paste it here.", "공급처 사이트에서 키를 발급받아 여기에 붙여넣으세요.")
		if provider.ID == "openrouter" {
			dialog.Detail = t("Approve Freeway in OpenRouter and your key is checked automatically.", "OpenRouter에서 승인하면 Freeway가 키를 받아 자동으로 확인합니다.")
			label := t("Sign in in your browser", "브라우저로 로그인")
			if status.KeyConfigured {
				label = t("Sign in again in your browser", "브라우저로 다시 로그인")
			}
			dialog.Options = append(dialog.Options,
				tui.SetupOption{ID: "oauth", Label: label},
				tui.SetupOption{ID: "oauth-code", Label: t("Sign in with an authorization code", "인증 코드로 로그인")})
		}
		if provider.AuthURL != "" {
			dialog.Options = append(dialog.Options, tui.SetupOption{ID: "browser", Label: t("Open key page in browser", "브라우저에서 키 발급 페이지 열기"), Detail: provider.AuthURL})
		}
		label := t("Enter an API key", "API 키 입력")
		if status.KeyConfigured {
			label = t("Replace API key", "API 키 교체")
		}
		dialog.Options = append(dialog.Options, tui.SetupOption{ID: "key", Label: label})
		if status.State == "confirm_free_account" && status.KeyConfigured {
			dialog.Options = append(dialog.Options, tui.SetupOption{ID: "confirm", Label: t("Confirm the saved key's free plan", "등록된 키의 무료 플랜 확인")})
		}
	}
	dialog.Detail += "\n" + tui.ProviderUsageText(prefs, status)
	if status.State == "disconnected" {
		dialog.Options = append(dialog.Options, tui.SetupOption{ID: "enable", Label: t("Enable in Freeway", "Freeway에서 다시 사용")})
	} else if !tui.ProviderUnconnected(status) {
		if status.AutoSupported {
			option := tui.SetupOption{ID: "exclude-auto", Label: t("Exclude from Auto", "Auto에서 제외")}
			if status.AutoExcluded {
				option = tui.SetupOption{ID: "include-auto", Label: t("Include in Auto", "Auto에 포함")}
			}
			dialog.Options = append(dialog.Options, option)
		}
		dialog.Options = append(dialog.Options, tui.SetupOption{ID: "disconnect", Label: t("Disconnect from Freeway", "Freeway에서 연결 해제")})
	}
	dialog.Options = append(dialog.Options, tui.SetupOption{ID: "back", Label: t("Back", "뒤로")})
	for {
		choice, err := dialog.Run(prefs, options...)
		if err != nil || choice == "" || choice == "back" {
			return err
		}
		dialog.Selected = choice
		switch choice {
		case "disconnect", "enable", "include-auto", "exclude-auto":
			if choice == "disconnect" {
				detail := t("Freeway removes the locally saved key and stops using this provider.", "Freeway에 저장한 키를 삭제하고 이 공급처 사용을 중지합니다.")
				if provider.RequiresCLI {
					detail = t("Freeway stops using this provider. Your official CLI session stays signed in.", "Freeway에서만 사용을 중지합니다. 공식 CLI 로그인은 계속 사용할 수 있습니다.")
				}
				confirmed, err := confirmAction(prefs, options, t("Disconnect from Freeway?", "Freeway에서 연결을 해제할까요?"), detail, t("Disconnect", "연결 해제"))
				if err != nil {
					return err
				}
				if !confirmed {
					continue
				}
			}
			if failed := providerGatewayError(gateway.ChangeProvider(ctx, provider.ID, choice, "", false)); failed != "" {
				dialog.Notice = providerActionFailure(prefs, failed)
				continue
			}
			return nil
		}
		if choice == "browser" {
			dialog.Selected = "key"
			dialog.Notice = t("Opened the key page. Return here with your key.", "키 발급 페이지를 열었습니다. 키를 가져와 입력하세요.")
			if err := openProviderURL(ctx, provider.AuthURL); err != nil {
				dialog.Notice = t("Open this address manually: ", "이 주소를 직접 여세요: ") + provider.AuthURL
			}
			continue
		}
		if choice == "oauth" || choice == "oauth-code" {
			key, err := loginOpenRouter(ctx, prefs, options, choice == "oauth-code")
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				dialog.Notice = t("OpenRouter approval did not finish. Try signing in again or use an authorization code.", "OpenRouter 승인을 마치지 못했습니다. 다시 로그인하거나 인증 코드로 연결하세요.")
				continue
			}
			if key == "" {
				dialog.Notice = t("OpenRouter sign-in was cancelled.", "OpenRouter 로그인을 취소했습니다.")
				continue
			}
			fmt.Fprintln(os.Stderr, t("Checking your OpenRouter key…", "OpenRouter 키를 확인 중입니다…"))
			if failed := providerGatewayError(gateway.ChangeProvider(ctx, provider.ID, "authenticate", key, false)); failed != "" {
				dialog.Notice = providerActionFailure(prefs, failed)
				dialog.Detail = t("If an unused Freeway key appears at openrouter.ai/settings/keys, you can revoke it there.", "openrouter.ai/settings/keys에서 연결에 사용하지 못한 새 Freeway 키를 삭제할 수 있습니다.")
				continue
			}
			fmt.Fprintln(os.Stderr, t("OpenRouter connected.", "OpenRouter를 연결했습니다."))
			return nil
		}
		if choice == "install" {
			if err := runProviderTerminal(ctx, binary, "install", "--only", provider.ID); err != nil {
				dialog.Notice = t("CLI installation failed. Try again.", "CLI 설치에 실패했습니다. 다시 시도하세요.")
				continue
			}
			return nil
		}
		if choice == "login" {
			if err := runProviderTerminal(ctx, binary, "auth", provider.ID, "--login"); err != nil {
				dialog.Notice = t("Login could not be verified. Try again.", "로그인을 확인하지 못했습니다. 다시 시도하세요.")
				continue
			}
			if status.State == "disconnected" {
				if failed := providerGatewayError(gateway.ChangeProvider(ctx, provider.ID, "enable", "", false)); failed != "" {
					dialog.Notice = providerActionFailure(prefs, failed)
					continue
				}
			}
			return nil
		}
		if choice == "key" && status.KeyConfigured && !provider.Catalog {
			confirmed, err := confirmAction(prefs, options, t("Replace the saved key?", "등록된 키를 교체할까요?"),
				t("This provider checks the key on the next model request.", "이 공급처의 새 키는 다음 모델 요청에서 확인됩니다."),
				t("Continue with replacement", "키 교체 계속"))
			if err != nil {
				return err
			}
			if !confirmed {
				continue
			}
		}
		confirmFree := false
		if provider.NeedsConfirmation {
			confirmed, err := confirmAction(prefs, options, t("Confirm the free plan", "무료 플랜 확인"),
				t("Check your account plan on the provider's site before enabling this connection.", "연결하기 전에 공급처 사이트에서 계정이 무료 플랜인지 확인하세요."),
				t("I verified the free plan", "무료 플랜을 확인했습니다"))
			if err != nil {
				return err
			}
			if !confirmed {
				continue
			}
			confirmFree = true
		}
		var key string
		if choice == "key" {
			prompt := tui.SetupDialog{Title: t("Enter API key", "API 키 입력"), Detail: provider.Name, Secret: true}
			key, err = prompt.Run(prefs, options...)
			if err != nil {
				return err
			}
			if key == "" {
				continue
			}
		}
		fmt.Fprintln(os.Stderr, t("Checking the connection…", "연결 확인 중…"))
		failed := providerGatewayError(gateway.ChangeProvider(ctx, provider.ID, "authenticate", key, confirmFree))
		if failed == "" {
			return nil
		}
		dialog.Notice = providerActionFailure(prefs, failed)
	}
}

func providerGatewayError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "request_timeout"
	}
	var gateway *client.GatewayError
	if errors.As(err, &gateway) {
		switch gateway.Status {
		case 401, 403:
			if gateway.Code != "invalid_api_key" && gateway.Code != "local_client_required" {
				return "key_rejected"
			}
		case 429:
			return "rate_limited"
		}
		if gateway.Code != "" {
			return gateway.Code
		}
	}
	return "provider_failed"
}

func confirmAction(prefs tui.Preferences, options []tea.ProgramOption, title, detail, label string) (bool, error) {
	dialog := tui.SetupDialog{Title: title, Detail: detail, Options: []tui.SetupOption{
		{ID: "back", Label: prefs.Text("Back", "뒤로")},
		{ID: "confirm", Label: label},
	}}
	answer, err := dialog.Run(prefs, options...)
	return answer == "confirm", err
}

func providerActionFailure(prefs tui.Preferences, code string) string {
	switch code {
	case "key_rejected", "invalid_key":
		return prefs.Text("The new key was rejected. Previous settings are unchanged.", "새 키가 거부됐습니다. 기존 설정을 유지했습니다.")
	case "inference_key_required":
		return prefs.Text("Sign in again or use a model API key.", "다시 로그인하거나 모델 호출용 API 키를 입력하세요.")
	case "credential_conflict", "credential_store_busy":
		return prefs.Text("Settings changed in another window. Reopen provider management to try again.", "다른 창에서 설정이 변경됐습니다. 공급처 관리를 다시 열어 주세요.")
	case "no_free_models":
		return prefs.Text("No eligible free models were found. Previous settings are unchanged.", "쓸 수 있는 무료 모델이 없습니다. 기존 설정을 유지했습니다.")
	case "request_timeout":
		return prefs.Text("The connection check timed out. Try again.", "연결 확인 시간이 초과됐습니다. 다시 시도하세요.")
	case "rate_limited":
		return prefs.Text("The provider rate limited the check. Try again later.", "공급처가 확인 요청을 제한했습니다. 잠시 후 다시 시도하세요.")
	default:
		return prefs.Text("Could not update this connection. Check the account or try again.", "연결 설정을 변경하지 못했습니다. 계정을 확인하거나 다시 시도하세요.")
	}
}

func runProviderTerminal(ctx context.Context, executable string, args ...string) error {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = imagegen.WithoutTokenEnv()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func openProviderURL(ctx context.Context, address string) error {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return errors.New("invalid provider URL")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	open := func(name string, args ...string) error {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Env = imagegen.WithoutTokenEnv()
		return cmd.Run()
	}
	switch runtime.GOOS {
	case "darwin":
		return open("open", address)
	case "windows":
		return open("rundll32.exe", "url.dll,FileProtocolHandler", address)
	default:
		if bin, err := exec.LookPath("wslview"); err == nil && os.Getenv("WSL_DISTRO_NAME") != "" {
			return open(bin, address)
		}
		return open("xdg-open", address)
	}
}
