package tui

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/client"
)

type recoverySaved struct {
	chat  client.Conversation
	epoch uint64
	path  string
	err   error
}

type recoveryDraftLoaded struct {
	draft  draftState
	before draftState
	epoch  uint64
	err    error
}

func (m Model) recoverConversation() (tea.Model, tea.Cmd) {
	if m.saveError == "" {
		m.notify(m.tr("Recovery is available when a conversation cannot be saved.", "복구는 대화를 저장하지 못했을 때 사용할 수 있습니다."))
		return m, nil
	}
	if m.recovering || m.busy || m.saving || m.loadingSession || m.attachmentsPending > 0 {
		m.notify(m.tr("Wait for the current operation before saving a recovery copy.", "진행 중인 작업이 끝난 뒤 복구 사본을 저장하세요."))
		return m, nil
	}
	m.recovering = true
	chat := m.snapshot()
	epoch := m.sessionEpoch
	return m, func() tea.Msg {
		home, err := os.UserHomeDir()
		var path string
		if err == nil {
			path, err = writeRecovery(filepath.Join(home, ".freeway", "recovery"), chat)
		}
		return recoverySaved{chat: chat, epoch: epoch, path: path, err: err}
	}
}

func (m Model) receiveRecovery(saved recoverySaved) (tea.Model, tea.Cmd, bool) {
	m.recovering = false
	if saved.err != nil {
		m.notify(m.tr("Could not write recovery file: ", "복구 파일을 쓰지 못했습니다: ") + terminalSafe(saved.err.Error()))
		return m, nil, true
	}
	if saved.epoch != m.sessionEpoch || !reflect.DeepEqual(saved.chat, m.snapshot()) {
		m.notify(m.tr("Recovery file saved. Press F6 again to include newer edits.", "복구 파일을 저장했습니다. 이후 수정까지 담으려면 F6을 다시 누르세요."))
		return m, nil, true
	}
	action := m.recoveryAction
	if action == "" || action == "fork" || action == "fork-edit" {
		action = "new"
	}
	if !strings.HasPrefix(action, "open:") {
		m.clearSaveState()
		m.dirty = false
	}
	m.recoveryPath = saved.path
	next, cmd := m.performNavigation(action)
	model := next.(Model)
	model.notify(model.tr("Recovery saved to ", "복구 파일 저장 위치: ") + terminalSafe(saved.path))
	return model, cmd, true
}

func writeRecovery(directory string, chat client.Conversation) (string, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(directory, "conversation-*.json")
	if err != nil {
		return "", err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	err = encoder.Encode(chat)
	if err == nil {
		err = file.Sync()
	}
	if err = errors.Join(err, file.Close()); err != nil {
		_ = os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}

func (m Model) restoreRecovery(arg string) (tea.Model, tea.Cmd) {
	if m.recovering || m.loadingSession || m.loadingHistory || m.attachmentsPending > 0 || m.editorOpen || m.editingAt >= 0 {
		m.notify(m.tr("Finish the current edit or load before restoring a recovery draft.", "편집이나 불러오기를 마친 뒤 복구 초안을 복원하세요."))
		return m, nil
	}
	path := strings.Trim(arg, "\"")
	before, epoch := m.captureDraft(), m.sessionEpoch
	return m, func() tea.Msg {
		var draft draftState
		var err error
		if path == "" {
			path, err = latestRecovery()
		}
		if err == nil {
			draft, err = readRecoveryDraft(path)
		}
		return recoveryDraftLoaded{draft: draft, before: before, epoch: epoch, err: err}
	}
}

var errNoRecovery = errors.New("no recovery files found")

func latestRecovery() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	directory := filepath.Join(home, ".freeway", "recovery")
	files, err := os.ReadDir(directory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	var path string
	var latest time.Time
	for _, file := range files {
		if !strings.HasPrefix(file.Name(), "conversation-") || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		info, err := file.Info()
		if err == nil && info.Mode().IsRegular() && (path == "" || info.ModTime().After(latest)) {
			path, latest = filepath.Join(directory, file.Name()), info.ModTime()
		}
	}
	if path == "" {
		return "", errNoRecovery
	}
	return path, nil
}

func readRecoveryDraft(path string) (draftState, error) {
	const maxRecoveryBytes = 128 * 1024 * 1024
	file, err := os.Open(path)
	if err != nil {
		return draftState{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return draftState{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxRecoveryBytes {
		return draftState{}, errors.New("choose a recovery JSON file up to 128 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxRecoveryBytes+1))
	if err != nil {
		return draftState{}, err
	}
	if len(data) > maxRecoveryBytes {
		return draftState{}, errors.New("recovery JSON exceeds 128 MiB")
	}
	var chat client.Conversation
	if err := json.Unmarshal(data, &chat); err != nil {
		return draftState{}, err
	}
	draft := draftState{Text: chat.Draft, Files: chat.DraftFiles}
	if draft.Text == "" && len(draft.Files) == 0 && chat.Stash != nil {
		draft = draftState{Text: chat.Stash.Text, Files: chat.Stash.Files}
	}
	if draft.Text == "" && len(draft.Files) == 0 {
		for i := len(chat.Messages) - 1; i >= 0; i-- {
			message := chat.Messages[i]
			if message.Role == "user" {
				draft = draftState{Text: message.Content, Files: message.Files}
				break
			}
		}
	}
	if draft.Text == "" && len(draft.Files) == 0 {
		return draftState{}, errors.New("this file has no draft or user prompt")
	}
	if utf8.RuneCountInString(draft.Text) > 150000 || len(draft.Files) > 5 {
		return draftState{}, errors.New("recovered input exceeds the draft or attachment limit")
	}
	for _, file := range draft.Files {
		if utf8.RuneCountInString(file.Path) > 1000 || utf8.RuneCountInString(file.Content) > 200000 {
			return draftState{}, errors.New("a recovered attachment exceeds the save limit")
		}
	}
	return draft, nil
}
