package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/wooinwoo/freeway-cli/cli/tui"
	"github.com/wooinwoo/freeway-cli/cli/update"
)

func TestStartupOffersNewerReleaseBeforeDownloading(t *testing.T) {
	var downloaded atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/package" {
			downloaded.Store(true)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(update.Release{SchemaVersion: 1, Version: "0.7.0", Artifacts: []update.Artifact{
			{OS: runtime.GOOS, Arch: runtime.GOARCH, SHA256: strings.Repeat("0", 64), URL: "http://" + r.Host + "/package"},
		}})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var output bytes.Buffer
	options := []tea.ProgramOption{tea.WithContext(ctx), tea.WithWindowSize(80, 24), tea.WithInput(strings.NewReader("j\r")), tea.WithOutput(&output)}
	manager := update.Manager{Root: t.TempDir(), ReleaseURL: server.URL}
	prefs := tui.Preferences{Language: "en"}
	restarted, err := offerStartupUpdate(ctx, manager, "0.6.0", prefs, options)
	if err != nil || restarted || downloaded.Load() || !strings.Contains(output.String(), "Freeway update available") {
		t.Fatalf("new release was not offered without downloading: restarted=%t, downloaded=%t, err=%v, output=%q", restarted, downloaded.Load(), err, output.String())
	}
	output.Reset()
	restarted, err = offerStartupUpdate(ctx, manager, "0.7.0", prefs, options)
	if err != nil || restarted || output.Len() != 0 {
		t.Fatalf("the installed version triggered an update dialog: %v", err)
	}
}
