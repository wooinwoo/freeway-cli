package tui

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type SetupOption struct {
	ID, Label, Detail string
	Check             func(context.Context) (status, detail string)
}

type SetupDialog struct {
	Title, Detail, Notice string
	Options               []SetupOption
	Secret                bool
	Input, Value          string
	Wait                  func() string
	Check                 func(context.Context) (status, detail string)
	Selected              string
	// EscapeID is returned for Esc. Empty closes the dialog without a choice.
	EscapeID string
	// Help replaces the default key hint for dialogs whose Esc does something else.
	Help         string
	previewTheme bool
	cancel       bool
}

func (d SetupDialog) Run(prefs Preferences, options ...tea.ProgramOption) (string, error) {
	applyTheme(prefs.Theme)
	if d.previewTheme {
		defer applyTheme(prefs.Theme)
	}
	m := setupModel{SetupDialog: d, prefs: prefs, width: 80, height: 24}
	for i, option := range d.Options {
		if option.ID == d.Selected {
			m.index = i
		}
		if option.Check != nil && m.checks == nil {
			m.checks = make([]setupCheckResult, len(d.Options))
		}
	}
	if d.Check != nil || m.checks != nil {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		m.checkContext = ctx
	}
	if d.hasInput() {
		m.input = textinput.New()
		m.input.CharLimit, m.input.Placeholder = 4096, d.Input
		if d.Secret {
			m.input.EchoMode = textinput.EchoPassword
			if d.Input == "" {
				m.input.Placeholder = prefs.Text("Paste API key", "API 키 붙여넣기")
			}
		}
		m.input.SetValue(d.Value)
		m.input.CursorEnd()
		m.input.SetWidth(46)
		m.input.Focus()
	}
	finished, err := tea.NewProgram(m, options...).Run()
	if err != nil {
		return "", err
	}
	return finished.(setupModel).result, nil
}

func (d SetupDialog) hasInput() bool { return d.Secret || d.Input != "" }

type setupModel struct {
	SetupDialog
	prefs                Preferences
	width, height, index int
	result               string
	input                textinput.Model
	checkContext         context.Context
	check                setupCheckResult
	checks               []setupCheckResult
}

type setupFinished string

type setupCheckResult struct {
	index          int
	status, detail string
}

func (m setupModel) Init() tea.Cmd {
	commands := []tea.Cmd{tea.RequestBackgroundColor}
	if m.Wait != nil {
		commands = append(commands, func() tea.Msg { return setupFinished(m.Wait()) })
	}
	if m.Check != nil {
		commands = append(commands, func() tea.Msg {
			status, detail := m.Check(m.checkContext)
			return setupCheckResult{index: -1, status: status, detail: detail}
		})
	}
	for i, option := range m.Options {
		if option.Check != nil {
			commands = append(commands, func() tea.Msg {
				status, detail := option.Check(m.checkContext)
				return setupCheckResult{index: i, status: status, detail: detail}
			})
		}
	}
	return tea.Batch(commands...)
}

func (m setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case setupFinished:
		m.result = string(msg)
		return m, tea.Quit
	case setupCheckResult:
		if msg.index < 0 {
			m.check = msg
		} else {
			m.checks[msg.index] = msg
		}
	case tea.BackgroundColorMsg:
		lightBackground.Store(!msg.IsDark())
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.hasInput() {
			m.input.SetWidth(max(1, min(46, m.width-8)))
		}
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc", "ctrl+c":
			if msg.String() == "esc" {
				m.result = m.EscapeID
			}
			m.input.Reset()
			return m, tea.Quit
		case "enter":
			if m.hasInput() {
				m.result = strings.TrimSpace(m.input.Value())
				m.input.Reset()
				if m.result == "" {
					m.Notice = m.prefs.Text("Enter a value to continue.", "계속하려면 값을 입력하세요.")
					if m.Secret && m.Input == "" {
						m.Notice = m.prefs.Text("Enter a key to continue.", "계속하려면 키를 입력하세요.")
					}
					return m, nil
				}
			} else if len(m.Options) > 0 {
				m.result = m.Options[m.index].ID
			}
			return m, tea.Quit
		}
		if !m.hasInput() && len(m.Options) > 0 {
			switch msg.String() {
			case "up", "shift+tab", "k":
				m.index = (m.index + len(m.Options) - 1) % len(m.Options)
			case "down", "tab", "j":
				m.index = (m.index + 1) % len(m.Options)
			}
		}
	}
	if m.hasInput() {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m setupModel) View() tea.View {
	if m.previewTheme {
		applyTheme(m.Options[m.index].ID)
	}
	width := max(1, min(64, m.width-4))
	colors := grooveColors()
	accent := lipgloss.NewStyle().Foreground(colors.accent)
	muted := lipgloss.NewStyle().Foreground(colors.muted)
	text := lipgloss.NewStyle().Foreground(colors.text)
	label := func(s string) string { return ansi.Truncate(terminalSafe(s), width, "…") }
	checking := m.prefs.Text("Checking…", "확인 중…")
	title := m.Title
	if m.Check != nil {
		status := m.check.status
		if status == "" {
			status = checking
		}
		title += " · " + status
	}
	rows := []string{accent.Bold(true).Render(label("freeway")), "", text.Bold(true).Render(label(title))}
	if m.Detail != "" {
		detail := strings.Split(ansi.Wrap(terminalSafe(m.Detail), width, ""), "\n")
		rows = append(rows, muted.Render(strings.Join(detail[:min(len(detail), max(1, m.height-9))], "\n")))
	}
	rows = append(rows, "")
	help := m.prefs.Text("↑↓ move  ·  Enter select  ·  Esc back", "↑↓ 이동  ·  Enter 선택  ·  Esc 뒤로")
	if m.previewTheme {
		help = m.prefs.Text("↑↓ preview · Enter select · Esc back", "↑↓ 미리보기 · Enter 선택 · Esc 뒤로")
	}
	if m.cancel {
		help = m.prefs.Text("↑↓ move  ·  Enter select  ·  Esc cancel", "↑↓ 이동  ·  Enter 선택  ·  Esc 취소")
	}
	if m.Help != "" {
		help = m.Help
	}
	if m.hasInput() {
		rows = append(rows, m.input.View())
		help = m.prefs.Text("Enter connect  ·  Esc cancel", "Enter 연결  ·  Esc 취소")
	} else if len(m.Options) > 0 {
		detail := label(m.Options[m.index].Detail)
		statusDetail := m.check.detail
		if m.checks != nil && m.checks[m.index].detail != "" {
			statusDetail = m.checks[m.index].detail
		}
		if statusDetail != "" {
			lines := strings.Split(ansi.Wrap(terminalSafe(statusDetail), width, ""), "\n")
			detail = strings.Join(lines[:min(2, len(lines))], "\n")
		}
		visible := min(len(m.Options), max(1, m.height-lipgloss.Height(strings.Join(rows, "\n"))-4-lipgloss.Height(detail)))
		first := min(max(0, m.index-visible/2), len(m.Options)-visible)
		for i := first; i < first+visible; i++ {
			option := m.Options[i]
			name := option.Label
			if option.Check != nil {
				status := m.checks[i].status
				if status == "" {
					status = checking
				}
				name = status + " · " + name
			}
			if i == m.index {
				rows = append(rows, accent.Bold(true).Background(colors.selected).Render(padCells("›  "+terminalSafe(name), width)))
			} else {
				rows = append(rows, text.Render(label("   "+name)))
			}
		}
		if detail != "" {
			rows = append(rows, muted.Render(detail))
		}
	}
	if m.Notice != "" {
		notice := strings.Split(ansi.Wrap(terminalSafe(m.Notice), width, ""), "\n")
		rows = append(rows, accent.Render(strings.Join(notice[:min(2, len(notice))], "\n")))
	}
	rows = append(rows, "", muted.Render(label(help)))
	view := tea.NewView(lipgloss.NewStyle().MaxWidth(max(1, m.width)).MaxHeight(max(1, m.height)).PaddingLeft(min(2, max(0, m.width-1))).Render(strings.Join(rows, "\n")))
	view.AltScreen = true
	return themedView(view, m.prefs.NoColor)
}
