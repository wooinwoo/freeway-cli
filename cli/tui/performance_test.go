package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

func longConversation() Model {
	m := update(New(), tea.WindowSizeMsg{Width: 120, Height: 36})
	for i := range 120 {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		m.messages = append(m.messages, newMessage(role, strings.Repeat(fmt.Sprintf("긴 대화 %d · 한국어와 Unicode 🌏를 포함한 문장입니다.\n", i), 8)))
	}
	m.renderMessages()
	return m
}

// This is the original renderer, retained only as a performance baseline.
func originalRender(m *Model) {
	colors := grooveColors()
	marker := lipgloss.NewStyle().Foreground(colors.accent).Render("› ")
	text := lipgloss.NewStyle().Width(m.chat.Width() - 2)
	rows := make([]string, 0, len(m.messages))
	for _, message := range m.messages {
		content := ansi.Strip(message.Content)
		if message.Role == "assistant" {
			rows = append(rows, lipgloss.NewStyle().Width(m.chat.Width()).Render(content))
		} else {
			rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, marker, text.Render(content)))
		}
	}
	m.chat.SetContent(strings.Join(rows, "\n\n"))
}

func BenchmarkLongConversationFrame(b *testing.B) {
	for _, cached := range []bool{false, true} {
		name := "original"
		if cached {
			name = "cached"
		}
		b.Run(name, func(b *testing.B) {
			m := longConversation()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.messages[len(m.messages)-1].Content = fmt.Sprintf("새 토큰 %d", i)
				if cached {
					m.renderMessages()
				} else {
					originalRender(&m)
				}
				_ = m.View()
			}
		})
	}
}

func BenchmarkTypingWithLongConversation(b *testing.B) {
	m := longConversation()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.prompt.Reset()
		m = update(m, tea.KeyPressMsg{Code: '가', Text: "가"})
		_ = m.View()
	}
}

func TestRenderPreviewArtifacts(t *testing.T) {
	dir := os.Getenv("FREEWAY_UI_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set FREEWAY_UI_SNAPSHOT_DIR to render QA fixtures")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, size := range [][2]int{{120, 36}, {80, 24}, {44, 12}} {
		m := update(New(), tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.selected = "free/auto"
		m.availableModels = []client.Model{{ID: "free/auto", Name: "Auto", Detail: "Finds the best available free connection"}, {ID: "sample/deepseek", Name: "DeepSeek Flash", Detail: "Kilo · OpenCode"}, {ID: "sample/minimax", Name: "MiniMax Flash", Detail: "Kilo · OpenCode"}, {ID: "sample/glm", Name: "GLM Flash", Detail: "OpenCode"}, {ID: "sample/nemotron", Name: "Nemotron Ultra", Detail: "Kilo"}}
		m.messages = []client.Message{newMessage("user", "Clean up this function and explain the changes."), newMessage("assistant", "## A small, predictable function\n\n**Keep the greeting explicit** and let callers choose the name.\n\n```go\nfunc greet(name string) string {\n    return \"Hello, \" + name\n}\n```\n\nUse /code to copy just the code, or Ctrl+Y for the complete response.")}
		m.prompt.SetValue("Can you add a test for the empty name case?")
		m.layout()
		m.renderMessages()
		m.chat.GotoBottom()
		path := filepath.Join(dir, fmt.Sprintf("chat-%dx%d.txt", size[0], size[1]))
		if err := os.WriteFile(path, []byte(ansi.Strip(m.View().Content)), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(strings.TrimSuffix(path, ".txt")+".ansi", []byte(m.View().Content), 0600); err != nil {
			t.Fatal(err)
		}
		welcome := update(New(), tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		welcome.selected = "free/auto"
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("welcome-%dx%d.ansi", size[0], size[1])), []byte(welcome.View().Content), 0600); err != nil {
			t.Fatal(err)
		}
		next, _ := m.changeFocus(true)
		menu := next.(Model)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("models-%dx%d.ansi", size[0], size[1])), []byte(menu.View().Content), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
