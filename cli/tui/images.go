// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/wooinwoo/freeway-cli/cli/client"
	"github.com/wooinwoo/freeway-cli/cli/imagegen"
)

func (m Model) validateImageRequest(prompt string, files []client.Attachment) (imagegen.Config, string) {
	switch {
	case m.gateway == nil:
		return imagegen.Config{}, m.tr("Preview mode does not generate images.", "미리보기에서는 이미지를 생성할 수 없습니다.")
	case len(files) > 0:
		return imagegen.Config{}, m.tr("This tool accepts text only. Use /detach to remove the attachments.", "이 도구는 텍스트만 받습니다. /detach로 첨부를 해제하세요.")
	case utf8.RuneCountInString(prompt) > imagegen.MaxPrompt:
		return imagegen.Config{}, m.tr("Shorten the image prompt to 4,000 characters or fewer.", "이미지 요청을 4,000자 이내로 줄여 주세요.")
	}
	config, err := imagegen.Load()
	if err != nil {
		return imagegen.Config{}, m.tr("F9 to connect an image tool. Your prompt is kept.", "F9으로 이미지 도구를 연결하세요. 입력을 보존했습니다.")
	}
	return config, ""
}

func (m Model) beginImage(prompt string, user client.Message) (tea.Model, tea.Cmd) {
	m.generation++
	generation := m.generation
	assetID := m.messages[len(m.messages)-1].ID
	ctx, cancel := context.WithTimeout(m.ctx, 125*time.Second)
	m.cancel = cancel
	m.status = "Generating image"
	m.lastResponseTokens = 0
	config, issue := m.validateImageRequest(prompt, user.Files)
	return m, func() tea.Msg {
		event := client.Event{Type: "error", Model: "mcp/"}
		if issue != "" {
			event.Error = issue
		} else {
			result, err := config.Generate(ctx, assetID, prompt)
			switch {
			case err != nil && result.Dispatched:
				event.Provider = result.Route.Host()
				event.Error = m.tr("Freeway could not confirm the image result. Check the tool before retrying.", "이미지 요청 결과를 확인하지 못했습니다. 다시 보내기 전에 도구 상태를 확인하세요.")
			case errors.Is(err, imagegen.ErrNoReadyRoute):
				event.Error = m.tr("No image tool is ready. Check the connections in F9.", "사용 가능한 이미지 도구가 없습니다. F9에서 연결을 확인하세요.")
			case errors.Is(err, imagegen.ErrTokenUnavailable):
				event.Error = m.tr("Set the MCP token environment variable", "MCP 토큰 환경변수를 설정하세요") + " " + result.Route.TokenEnv
			case errors.Is(err, imagegen.ErrBrowserLoginRequired):
				event.Error = m.tr("Image MCP sign-in expired or is missing. Sign in again in F9.", "이미지 MCP 로그인이 만료됐거나 없습니다. F9에서 다시 로그인하세요.")
			case errors.Is(err, imagegen.ErrBrowserRefreshUnavailable):
				event.Error = m.tr("Could not renew the image MCP sign-in. Try again or check F9.", "이미지 MCP 로그인을 갱신하지 못했습니다. 다시 시도하거나 F9에서 연결을 확인하세요.")
			case errors.Is(err, imagegen.ErrAuthRequired):
				event.Error = m.tr("MCP authorization failed. Check your token and permissions in F9.", "MCP 인증에 실패했습니다. F9에서 토큰과 접근 권한을 확인하세요.")
			case err != nil:
				event.Error = m.tr("Image generation failed. Check the MCP tool and try again.", "이미지 생성에 실패했습니다. MCP 도구를 확인하고 다시 시도하세요.")
			default:
				event.Type, event.Provider, event.Model = "done", fmt.Sprintf("MCP #%d · %s", result.Position, result.Route.Host()), "mcp/"+result.Route.Tool
				event.Text = m.tr("Image saved", "이미지 저장됨") + "\n" + result.Path +
					"\n\n" + m.tr("/image-open to open · /image-path to copy path", "/image-open 열기 · /image-path 경로 복사")
			}
		}
		return responseEvent{event: event, generation: generation}
	}
}

func (m Model) setupImage() (tea.Model, tea.Cmd) {
	return m.openMCPManager("image", "connect")
}

func (m Model) openMCPManager(commandName, action string) (tea.Model, tea.Cmd) {
	manual := fmt.Sprintf(m.tr("Run freeway %s %s in your terminal.", "터미널에서 freeway %s %s를 실행하세요."), commandName, action)
	if m.ctx == nil {
		m.notify(manual)
		return m, nil
	}
	if m.busy {
		m.notify(m.tr("Stop the current response before changing MCP connections.", "MCP 연결을 바꾸려면 진행 중인 응답을 먼저 중지하세요."))
		return m, nil
	}
	executable, err := os.Executable()
	if err != nil {
		m.notify(manual)
		return m, nil
	}
	command := exec.CommandContext(m.ctx, executable, commandName, action)
	if m.prefs.NoColor {
		command.Env = append(os.Environ(), "NO_COLOR=1")
	}
	return m, tea.ExecProcess(command, func(err error) tea.Msg {
		return actionResult{err: err}
	})
}

func (m Model) latestImage(copyPath bool) (tea.Model, tea.Cmd) {
	for i := len(m.messages) - 1; i >= 0; i-- {
		message := m.messages[i]
		if message.Role != "assistant" || message.Status != "done" || !strings.HasPrefix(message.Model, "mcp/") {
			continue
		}
		path := imagegen.Find(message.ID)
		if path == "" {
			continue
		}
		copyText := m.copyText
		if copyText == nil {
			copyText = clipboard.WriteAll
		}
		return m, func() tea.Msg {
			if copyPath {
				return actionResult{text: m.tr("Image path copied.", "이미지 경로를 복사했습니다."), err: copyText(path), clipboard: path}
			}
			ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
			defer cancel()
			if err := imagegen.Open(ctx, message.ID); err != nil {
				return actionResult{err: errors.New(m.tr("Could not open the image. Copy its path with /image-path.", "이미지를 열지 못했습니다. /image-path로 경로를 복사하세요."))}
			}
			return actionResult{text: m.tr("Opened the image.", "이미지를 열었습니다.")}
		}
	}
	m.notify(m.tr("No generated image file was found in this conversation.", "이 대화에서 생성한 이미지 파일을 찾을 수 없습니다."))
	return m, nil
}
