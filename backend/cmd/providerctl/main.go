// providerctl exercises the native provider package without running a backend server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/wooinwoo/freeway-cli/backend/internal/providers"
)

const marker = "FREEWAY_PROVIDER_OK"

var version = "dev"

const usage = `Usage: freeway-providers COMMAND [PROVIDER|all] [options]

  install|update|check [--root PATH] [--api-only|--only opencode|cline]
  list
  auth [PROVIDER|all] [--stdin|--from-env|--login] [--confirm-free]
  status|models|quota [PROVIDER|all]
  select PROVIDER MODEL_ID
  probe PROVIDER [--model MODEL_ID] [--reasoning-effort VALUE]
  ask PROVIDER [--model MODEL_ID] [--reasoning-effort VALUE] -- QUESTION

All vendor connections run directly from Go. No Freeway gateway or Node runtime is started.
API keys are accepted through hidden terminal input, environment variables, or stdin only.
`

type options struct {
	command, id, root, only, model, effort  string
	apiOnly, stdin, fromEnv, login, confirm bool
	timeout, maxTokens                      int
	positionals                             []string
}

type cliModel struct {
	providers.Model
	Commands          map[string][]string `json:"commands"`
	ReasoningCommands map[string][]string `json:"reasoningCommands,omitempty"`
}

func modelCommands(id string, models []providers.Model) []cliModel {
	binary, err := os.Executable()
	if err != nil {
		binary = "freeway-providers"
	}
	result := make([]cliModel, 0, len(models))
	for _, model := range models {
		probe := []string{binary, "probe", id, "--model", model.ID}
		entry := cliModel{Model: model, Commands: map[string][]string{"select": {binary, "select", id, model.ID}, "probe": probe, "ask": {binary, "ask", id, "--model", model.ID, "--", "질문"}}}
		for _, effort := range model.Reasoning.Efforts {
			if entry.ReasoningCommands == nil {
				entry.ReasoningCommands = map[string][]string{}
			}
			entry.ReasoningCommands[effort] = append(append([]string(nil), probe...), "--reasoning-effort", effort)
		}
		result = append(result, entry)
	}
	return result
}

func parse(args []string) (options, error) {
	o := options{command: "help", id: "all", root: providers.ProviderHome(), timeout: 120, maxTokens: 256}
	if len(args) == 0 {
		return o, nil
	}
	o.command = args[0]
	args = args[1:]
	if o.command == "help" || o.command == "--help" || o.command == "-h" {
		o.command = "help"
		return o, nil
	}
	commands := map[string]bool{"install": true, "update": true, "check": true, "list": true, "auth": true, "status": true, "models": true, "quota": true, "select": true, "probe": true, "ask": true}
	if !commands[o.command] {
		return o, errors.New("unknown command; use --help")
	}
	setup := o.command == "install" || o.command == "update" || o.command == "check"
	if !setup && o.command != "list" && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		o.id = args[0]
		args = args[1:]
	}
	if o.command == "select" {
		if len(args) != 1 {
			return o, errors.New("select requires a provider and model ID")
		}
		o.positionals = args
		args = nil
	}
	f := flag.NewFlagSet(o.command, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	if setup {
		f.StringVar(&o.root, "root", o.root, "")
		f.StringVar(&o.only, "only", "", "")
		f.BoolVar(&o.apiOnly, "api-only", false, "")
	}
	if o.command == "auth" {
		f.BoolVar(&o.stdin, "stdin", false, "")
		f.BoolVar(&o.fromEnv, "from-env", false, "")
		f.BoolVar(&o.login, "login", false, "")
		f.BoolVar(&o.confirm, "confirm-free", false, "")
	}
	if o.command == "probe" || o.command == "ask" {
		f.StringVar(&o.model, "model", "", "")
		f.StringVar(&o.effort, "reasoning-effort", "", "")
		f.IntVar(&o.timeout, "timeout", 120, "")
		f.IntVar(&o.maxTokens, "max-tokens", 256, "")
	}
	if err := f.Parse(args); err != nil {
		return o, errors.New("invalid option; use --help")
	}
	if o.command != "select" {
		o.positionals = f.Args()
	}
	if o.command != "ask" && o.command != "select" && len(o.positionals) > 0 {
		return o, errors.New("unexpected arguments; keys are not accepted in command arguments")
	}
	if o.command == "ask" && len(o.positionals) == 0 {
		return o, errors.New("ask requires a question after --")
	}
	if o.apiOnly && o.only != "" {
		return o, errors.New("choose --api-only or --only")
	}
	if o.only != "" && o.only != "opencode" && o.only != "cline" {
		return o, errors.New("--only accepts opencode or cline")
	}
	if o.timeout < 1 || o.timeout > 300 || o.maxTokens < 1 || o.maxTokens > 32768 {
		return o, errors.New("timeout must be 1..300 seconds and max-tokens 1..32768")
	}
	if o.stdin && o.fromEnv || o.login && (o.stdin || o.fromEnv) {
		return o, errors.New("choose one authentication input method")
	}
	if o.id == "all" && (o.stdin || o.login || o.command == "select" || o.command == "probe" || o.command == "ask") {
		return o, errors.New("this operation requires one provider")
	}
	if !setup && o.command != "list" && o.id != "all" {
		found := false
		for _, d := range providers.Definitions() {
			if d.ID == o.id {
				found = true
			}
		}
		if !found {
			return o, errors.New("unknown provider; use list")
		}
	}
	return o, nil
}

func encode(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
func problem(err error) map[string]any {
	var typed *providers.Error
	if errors.As(err, &typed) {
		return map[string]any{"code": typed.Code, "httpStatus": typed.Status, "retryAfter": typed.RetryAfter}
	}
	if errors.Is(err, context.Canceled) {
		return map[string]any{"code": "cancelled"}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return map[string]any{"code": "request_timeout"}
	}
	return map[string]any{"code": "provider_failed"}
}
func main() {
	code, err := run(os.Args[1:])
	if err != nil {
		_ = encode(map[string]any{"ok": false, "error": problem(err)})
		fmt.Fprintln(os.Stderr, err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func run(args []string) (int, error) {
	if len(args) == 1 && (args[0] == "--version" || args[0] == "version") {
		fmt.Println(version)
		return 0, nil
	}
	o, err := parse(args)
	if err != nil {
		return 2, err
	}
	if o.command == "help" {
		fmt.Print(usage)
		return 0, nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if o.command == "list" {
		return 0, encode(map[string]any{"ok": true, "providers": providers.Definitions()})
	}
	if o.command == "install" || o.command == "update" || o.command == "check" {
		var tools []string
		if o.apiOnly {
			tools = []string{}
		} else if o.only != "" {
			tools = []string{o.only}
		}
		result, err := providers.Install(ctx, providers.InstallOptions{Action: o.command, Root: o.root, Tools: tools})
		if err != nil {
			return 1, err
		}
		code := 0
		if !result.OK {
			code = 1
		}
		return code, encode(result)
	}
	store, err := providers.NewFileCredentials(filepath.Join(providers.ProviderHome(), "auth"))
	if err != nil {
		return 1, err
	}
	client, err := providers.New(providers.Config{Credentials: store})
	if err != nil {
		return 1, err
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(o.timeout)*time.Second)
	defer cancel()
	results := []map[string]any{}
	ok := true
	for _, d := range providers.Definitions() {
		if o.id != "all" && d.ID != o.id {
			continue
		}
		result := map[string]any{"id": d.ID}
		var err error
		switch o.command {
		case "auth":
			var auth providers.AuthResult
			auth, err = authenticate(ctx, client, store, d, o)
			result["authentication"] = auth
			if !auth.Ready {
				ok = false
			}
		case "status", "quota":
			result["quota"], err = client.Quota(ctx, d.ID)
		case "models":
			var models []providers.Model
			models, err = client.Models(ctx, d.ID)
			if err == nil {
				result["models"] = modelCommands(d.ID, models)
			}
		case "select":
			var models []providers.Model
			models, err = client.Models(ctx, d.ID)
			if err == nil {
				selected := ""
				for _, m := range models {
					if m.ID == o.positionals[0] || m.UpstreamID == o.positionals[0] {
						selected = m.ID
					}
				}
				if selected == "" {
					err = &providers.Error{Code: "model_not_found", Status: 404}
				} else {
					err = saveSelection(d.ID, selected)
					result["selectedModel"] = selected
				}
			}
		case "probe", "ask":
			model := o.model
			if model == "" {
				model = selection(d.ID)
			}
			if model == "" {
				var models []providers.Model
				models, err = client.Models(ctx, d.ID)
				if err == nil {
					for _, m := range models {
						if m.Ready {
							model = m.ID
							break
						}
					}
				}
				if model == "" && err == nil {
					err = &providers.Error{Code: "no_free_models", Status: 503}
				}
			}
			if err == nil {
				prompt := marker
				if o.command == "probe" {
					prompt = "Reply with exactly " + marker + ". Do not use tools."
				} else {
					prompt = strings.Join(o.positionals, " ")
				}
				var completion providers.Completion
				completion, err = client.Complete(ctx, providers.Request{Provider: d.ID, Model: model, ReasoningEffort: o.effort, MaxTokens: o.maxTokens, Messages: []providers.Message{{Role: "user", Content: prompt}}})
				if err == nil {
					result["completion"] = completion
					result["inferenceVerified"] = true
					if o.command == "probe" && strings.TrimSpace(completion.Content) != marker {
						err = &providers.Error{Code: "unexpected_probe_response", Status: 502}
						result["inferenceVerified"] = false
					}
				}
			}
		}
		if err != nil {
			ok = false
			result["error"] = problem(err)
		}
		results = append(results, result)
	}
	code := 0
	if !ok {
		code = 1
	}
	return code, encode(map[string]any{"ok": ok, "results": results})
}

func authenticate(ctx context.Context, client *providers.Client, store providers.CredentialStore, d providers.Definition, o options) (providers.AuthResult, error) {
	if o.login {
		if !term.IsTerminal(os.Stdin.Fd()) {
			return providers.AuthResult{}, errors.New("official CLI login requires a terminal")
		}
		if err := client.Login(ctx, d.ID, os.Stdin, os.Stderr, os.Stderr); err != nil {
			return providers.AuthResult{}, err
		}
	}
	key := ""
	if !d.RequiresCLI {
		if o.stdin {
			data, err := readKey(ctx, false)
			if err != nil {
				return providers.AuthResult{}, err
			}
			key = strings.TrimSpace(string(data))
			clear(data)
			if key == "" || len(key) > 4096 {
				return providers.AuthResult{}, &providers.Error{Code: "invalid_key", Status: 400}
			}
		} else if o.fromEnv {
			key = os.Getenv(d.Env)
			if key == "" && d.ID != "kilo" {
				return providers.AuthResult{}, &providers.Error{Code: "needs_key", Status: 409}
			}
		} else {
			current, err := store.Load(ctx, d.ID)
			if err != nil {
				return providers.AuthResult{}, err
			}
			if current.Key == "" && os.Getenv(d.Env) == "" && d.ID != "kilo" && term.IsTerminal(os.Stdin.Fd()) {
				fmt.Fprintf(os.Stderr, "%s API key (Enter skips): ", d.Name)
				data, err := readKey(ctx, true)
				fmt.Fprintln(os.Stderr)
				if err != nil {
					return providers.AuthResult{}, err
				}
				key = string(data)
				clear(data)
			}
		}
	} else if o.stdin {
		return providers.AuthResult{}, &providers.Error{Code: "cli_login_required", Status: 409}
	}
	return client.Authenticate(ctx, d.ID, providers.AuthOptions{Key: key, ConfirmFree: o.confirm})
}

// Closing stdin interrupts pipe reads; terminal reads may still need the context select.
// Restore echo before closing the terminal descriptor on cancellation.
func readKey(ctx context.Context, hidden bool) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input := os.Stdin
	fd := input.Fd()
	var state *term.State
	if hidden {
		var err error
		state, err = term.GetState(fd)
		if err != nil {
			return nil, err
		}
		defer func() { _ = term.Restore(fd, state) }()
	}
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		if state != nil {
			_ = term.Restore(fd, state)
		}
		_ = input.Close()
		close(interrupted)
	})
	defer func() {
		if !stop() {
			<-interrupted
		}
	}()
	type result struct {
		data []byte
		err  error
	}
	read := make(chan result)
	go func() {
		var data []byte
		var err error
		if hidden {
			data, err = term.ReadPassword(fd)
		} else {
			data, err = io.ReadAll(io.LimitReader(input, 4099))
		}
		select {
		case read <- result{data, err}:
		case <-ctx.Done():
			clear(data)
		}
	}()
	select {
	case result := <-read:
		if ctx.Err() != nil {
			clear(result.data)
			return nil, ctx.Err()
		}
		if result.err != nil {
			clear(result.data)
			return nil, result.err
		}
		return result.data, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func selection(id string) string {
	data, _ := os.ReadFile(filepath.Join(providers.ProviderHome(), "selected-models", id+".txt"))
	return strings.TrimSpace(string(data))
}
func saveSelection(id, model string) error {
	dir := filepath.Join(providers.ProviderHome(), "selected-models")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".selection-*.tmp")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.WriteString(model + "\n")
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(dir, id+".txt"))
}
