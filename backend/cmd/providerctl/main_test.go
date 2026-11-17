package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wooinwoo/freeway-cli/backend/internal/providers"
)

func TestMain(m *testing.M) {
	if os.Getenv("FREEWAY_PROVIDERCTL_TEST_HELPER") == "1" {
		if len(os.Args) == 2 && os.Args[1] == "--version" && os.Getenv("FREEWAY_PROVIDERCTL_VERSION_PID") != "" {
			_ = os.WriteFile(os.Getenv("FREEWAY_PROVIDERCTL_VERSION_PID"), []byte(strconv.Itoa(os.Getpid())), 0600)
			for {
				time.Sleep(time.Hour)
			}
		}
		code, err := run(os.Args[1:])
		if err != nil {
			_ = encode(map[string]any{"ok": false, "error": problem(err)})
			if code == 0 {
				code = 1
			}
		}
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func TestProviderCLIArguments(t *testing.T) {
	for _, args := range [][]string{{"auth", "all", "--from-env"}, {"auth", "cline", "--login"}, {"install", "--api-only"}, {"update", "--only", "opencode"}, {"ask", "kilo", "--model", "reflection/beam-free", "--reasoning-effort", "high", "--", "--model이라는 문자열을 설명해 주세요"}} {
		if _, err := parse(args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	for _, args := range [][]string{{"auth", "all", "--stdin"}, {"auth", "cline", "--login", "--from-env"}, {"auth", "zai", "--api-key", "never-print-this-key"}, {"install", "--api-only", "--only", "cline"}, {"models", "unknown"}, {"probe", "all"}, {"select", "kilo"}} {
		if _, err := parse(args); err == nil {
			t.Fatalf("accepted %v", args)
		} else if strings.Contains(err.Error(), "never-print-this-key") {
			t.Fatal("argument error exposed a secret")
		}
	}
	o, err := parse([]string{"ask", "kilo", "--", "--model is literal prompt text"})
	if err != nil || strings.Join(o.positionals, " ") != "--model is literal prompt text" {
		t.Fatalf("prompt boundary: %+v %v", o, err)
	}
}

func executeCLI(t *testing.T, root, input string, args ...string) (map[string]any, int, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = append(os.Environ(), "FREEWAY_PROVIDERCTL_TEST_HELPER=1", "FREEWAY_PROVIDER_HOME="+root)
	cmd.Stdin = strings.NewReader(input)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	var result map[string]any
	if json.Unmarshal(data, &result) != nil {
		t.Fatalf("invalid command output: %s", data)
	}
	return result, code, string(data) + stderr.String()
}

func TestProviderCLIStdinAuthenticationPersistsEncryptedWithoutInference(t *testing.T) {
	root := t.TempDir()
	key := "standalone-fixture-key"
	result, code, output := executeCLI(t, root, key+"\n", "auth", "zai", "--stdin")
	if code != 0 || result["ok"] != true {
		t.Fatalf("authentication: %d %s", code, output)
	}
	if strings.Contains(output, key) {
		t.Fatal("authentication output exposed its key")
	}
	auth := result["results"].([]any)[0].(map[string]any)["authentication"].(map[string]any)
	if auth["inferenceVerified"] != false || auth["ready"] != true {
		t.Fatalf("authentication claims: %v", auth)
	}
	store, err := providers.NewFileCredentials(filepath.Join(root, "auth"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Load(context.Background(), "zai")
	if err != nil || value.Key != key {
		t.Fatal("credential was not persisted")
	}
	file, err := os.Open(filepath.Join(root, "auth", "provider-auth.enc"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	ciphertext, _ := io.ReadAll(file)
	if strings.Contains(string(ciphertext), key) {
		t.Fatal("credential was saved as plaintext")
	}
	models, code, _ := executeCLI(t, root, "", "models", "zai")
	if code != 0 {
		t.Fatal("saved credentials not reusable")
	}
	entries := models["results"].([]any)[0].(map[string]any)["models"].([]any)
	if len(entries) != 2 {
		t.Fatal("seed model list missing")
	}
	commands := entries[0].(map[string]any)["commands"].(map[string]any)
	if commands["probe"].([]any)[1] != "probe" {
		t.Fatal("model executable argv missing")
	}
	_, code, _ = executeCLI(t, root, "", "select", "zai", "glm-4.7-flash")
	if code != 0 {
		t.Fatal("model selection failed")
	}
	if selectionData, err := os.ReadFile(filepath.Join(root, "selected-models", "zai.txt")); err != nil || strings.TrimSpace(string(selectionData)) != "zai/glm-4.7-flash" {
		t.Fatal("selection not persisted")
	}
	failed, code, _ := executeCLI(t, root, "", "probe", "zai", "--model", "not-a-free-model")
	if code != 1 || failed["ok"] != false {
		t.Fatal("unknown model was accepted")
	}
	if failed["results"].([]any)[0].(map[string]any)["error"].(map[string]any)["code"] != "model_not_found" {
		t.Fatal("unknown model error was lost")
	}
}

func TestProviderCLIKeyReplacementDoesNotKeepFreeConfirmation(t *testing.T) {
	root := t.TempDir()
	_, code, _ := executeCLI(t, root, "first-fixture-key\n", "auth", "groq", "--stdin", "--confirm-free")
	if code != 0 {
		t.Fatal("confirmed setup failed")
	}
	result, code, output := executeCLI(t, root, "second-fixture-key\n", "auth", "groq", "--stdin")
	if code != 1 || result["ok"] != false || strings.Contains(output, "second-fixture-key") {
		t.Fatal("replacement result invalid")
	}
	store, err := providers.NewFileCredentials(filepath.Join(root, "auth"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Load(context.Background(), "groq")
	if err != nil || value.Key != "second-fixture-key" || value.FreeConfirmed {
		t.Fatal("free-account confirmation survived key replacement")
	}
}

func TestProviderCLIQuotaDoesNotCreateAnAuthenticationStore(t *testing.T) {
	root := t.TempDir()
	result, code, _ := executeCLI(t, root, "", "quota", "kilo")
	if code != 0 {
		t.Fatal("quota inspection failed")
	}
	q := result["results"].([]any)[0].(map[string]any)["quota"].(map[string]any)
	if q["authoritative"] != false || q["providerRemaining"] != nil || q["usage"] != nil {
		t.Fatal("unknown quota was fabricated")
	}
	if _, err := os.Stat(filepath.Join(root, "auth")); !os.IsNotExist(err) {
		t.Fatal("quota inspection mutated the credential directory")
	}
}

func waitCLIFile(t *testing.T, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(name); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("CLI did not reach readiness: %s", name)
}

func assertCLICancelled(t *testing.T, cmd *exec.Cmd, finished <-chan error, output *strings.Builder) {
	t.Helper()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(output.String(), "cancelled") {
			t.Fatalf("SIGTERM was not handled as cancellation: %v / %s", err, output.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SIGTERM left the CLI blocked")
	}
}

func TestProviderCLISIGTERMCancelsOwnedVersionProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix SIGTERM")
	}
	root := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "opencode")
	if err := os.Link(exe, binary); err != nil {
		data, err := os.ReadFile(exe)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(binary, data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	manifest, _ := json.Marshal(map[string]any{"schemaVersion": 1, "tools": map[string]any{"opencode": map[string]string{"path": binary, "version": "1.18.35"}}, "selectedTools": []string{"opencode"}})
	if err := os.WriteFile(filepath.Join(root, "install.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(root, "version.pid")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "check", "--root", root)
	cmd.Env = append(os.Environ(), "FREEWAY_PROVIDERCTL_TEST_HELPER=1", "FREEWAY_PROVIDER_HOME="+root, "FREEWAY_PROVIDERCTL_VERSION_PID="+pidFile)
	var output strings.Builder
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	waitCLIFile(t, pidFile)
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Kill(); _ = process.Release() })
	assertCLICancelled(t, cmd, finished, &output)
	if err := process.Signal(syscall.Signal(0)); err == nil {
		t.Fatal("CLI left its owned version process alive")
	}
}

func TestProviderCLISIGTERMCancelsBlockedStdinAuthentication(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix SIGTERM")
	}
	root := t.TempDir()
	workspaces := filepath.Join(root, "temporary")
	if err := os.Mkdir(workspaces, 0700); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "auth", "zai", "--stdin")
	cmd.Env = append(os.Environ(), "FREEWAY_PROVIDERCTL_TEST_HELPER=1", "FREEWAY_PROVIDER_HOME="+root, "TMPDIR="+workspaces)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	var output strings.Builder
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		entries, err := os.ReadDir(workspaces)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("CLI did not initialize before reading stdin")
		}
		time.Sleep(10 * time.Millisecond)
	}
	const key = "never-store-or-print-this-key"
	if _, err := io.WriteString(input, key); err != nil {
		t.Fatal(err)
	}
	assertCLICancelled(t, cmd, finished, &output)
	if strings.Contains(output.String(), key) {
		t.Fatal("cancellation exposed its partial key")
	}
	if _, err := os.Stat(filepath.Join(root, "auth")); !os.IsNotExist(err) {
		t.Fatal("cancelled authentication created a credential directory")
	}
}
