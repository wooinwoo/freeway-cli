package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type Preferences struct {
	Language             string `json:"language"`
	Theme                string `json:"theme"`
	SetupDone            bool   `json:"setupDone,omitempty"`
	NoColor              bool   `json:"-"`
	appearanceConfigured bool
}

func preferencesPath() (string, error) {
	dir, err := os.UserConfigDir()
	return filepath.Join(dir, "freeway", "preferences.json"), err
}

func LoadPreferences() Preferences {
	prefs := Preferences{Language: "en", Theme: "system", NoColor: os.Getenv("NO_COLOR") != ""}
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if locale := strings.ToLower(os.Getenv(name)); locale != "" {
			if locale == "ko" || strings.HasPrefix(locale, "ko_") || strings.HasPrefix(locale, "ko-") {
				prefs.Language = "ko"
			}
			break
		}
	}
	path, err := preferencesPath()
	if err != nil {
		return prefs
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return prefs
	}
	var saved Preferences
	if json.Unmarshal(data, &saved) == nil {
		validLanguage := saved.Language == "ko" || saved.Language == "en"
		if validLanguage {
			prefs.Language = saved.Language
		}
		switch saved.Theme {
		case "system", "dark", "light", "indigo", "amber":
			prefs.Theme = saved.Theme
			prefs.appearanceConfigured = validLanguage
		}
		prefs.SetupDone = saved.SetupDone
	}
	return prefs
}

func languageOptions() []SetupOption {
	return []SetupOption{{ID: "en", Label: "English"}, {ID: "ko", Label: "한국어"}}
}

func (p Preferences) themeOptions() []SetupOption {
	return []SetupOption{
		{ID: "system", Label: p.Text("Follow terminal", "터미널 설정 따르기")},
		{ID: "dark", Label: p.Text("Emerald dark", "에메랄드 다크")},
		{ID: "light", Label: p.Text("Emerald light", "에메랄드 라이트")},
		{ID: "indigo", Label: p.Text("Indigo", "인디고")},
		{ID: "amber", Label: p.Text("Amber", "앰버")},
	}
}

func (p *Preferences) ConfigureAppearance(options ...tea.ProgramOption) (bool, error) {
	if p.appearanceConfigured {
		return true, nil
	}
	chosen := *p
	for {
		language := SetupDialog{Title: "Language / 언어", Options: languageOptions(), Selected: chosen.Language, cancel: true}
		id, err := language.Run(chosen, options...)
		if err != nil || id == "" {
			return false, err
		}
		chosen.Language = id
		theme := SetupDialog{
			Title:        chosen.Text("Choose a color theme", "색상 테마 선택"),
			Options:      chosen.themeOptions(),
			Selected:     chosen.Theme,
			previewTheme: true,
			escapeID:     "back",
		}
		id, err = theme.Run(chosen, options...)
		if err != nil || id == "" {
			return false, err
		}
		if id == "back" {
			continue
		}
		chosen.Theme = id
		chosen.appearanceConfigured = true
		*p = chosen
		return true, chosen.Save()
	}
}

func (p Preferences) Save() error {
	path, err := preferencesPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".preferences-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	writeErr := json.NewEncoder(file).Encode(p)
	if err := file.Close(); writeErr == nil {
		writeErr = err
	}
	if writeErr != nil {
		return writeErr
	}
	return os.Rename(file.Name(), path)
}

func (p Preferences) Text(en, ko string) string {
	if p.Language == "ko" {
		return ko
	}
	return en
}

func (m Model) tr(en, ko string) string { return m.prefs.Text(en, ko) }

func (m Model) WithPreferences(prefs Preferences) Model {
	m.prefs = prefs
	m.applyPreferences()
	return m
}

func (m *Model) applyPreferences() {
	m.renderedTheme = m.activeTheme()
	applyTheme(m.renderedTheme)
	m.prompt.Placeholder = m.tr("Message…  / commands  ↑ history", "메시지…  / 명령  ↑ 기록")
	applyPromptTheme(&m.prompt)
	m.renderCache = nil
	m.renderMessages()
}

func (m Model) themePicker() bool { return m.modelsFocused && m.menu == "theme" }

func (m Model) activeTheme() string {
	if m.themePicker() {
		if item, ok := m.models.list.SelectedItem().(modelItem); ok {
			return item.ID
		}
	}
	return m.prefs.Theme
}
