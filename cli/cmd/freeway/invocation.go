package main

import (
	"fmt"
	"strconv"
	"strings"
)

type invocation struct {
	mode, query, url                          string
	all, json, preview, noSave, noColor, help bool
}

func parseInvocation(args []string) (invocation, error) {
	o := invocation{mode: "new"}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if o.mode == "resume" && o.query == "" && i > 0 && !strings.HasPrefix(arg, "-") {
			o.query = arg
			continue
		}
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", arg)
			}
			i++
			return args[i], nil
		}
		switch arg {
		case "resume":
			o.mode = "resume"
		case "continue", "-c", "--continue", "--last":
			o.mode = "continue"
		case "sessions":
			o.mode = "sessions"
		case "chat", "--new", "-n":
			o.mode = "new"
		case "--resume", "-r":
			o.mode = "resume"
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				o.query = args[i]
			}
		case "--session":
			text, err := value()
			if err != nil {
				return o, err
			}
			o.mode, o.query = "resume", text
		case "--url":
			text, err := value()
			if err != nil {
				return o, err
			}
			o.url = text
		case "--all":
			o.all = true
		case "--json":
			o.json = true
		case "--preview":
			o.preview = true
		case "--no-save":
			o.noSave = true
		case "--no-color":
			o.noColor = true
		case "--help", "-h", "help":
			o.help = true
		default:
			if strings.HasPrefix(arg, "--url=") {
				o.url = strings.TrimPrefix(arg, "--url=")
				continue
			}
			if strings.HasPrefix(arg, "--session=") {
				o.mode, o.query = "resume", strings.TrimPrefix(arg, "--session=")
				continue
			}
			if strings.HasPrefix(arg, "--resume=") {
				o.mode, o.query = "resume", strings.TrimPrefix(arg, "--resume=")
				continue
			}
			if o.mode == "resume" && o.query == "" && !strings.HasPrefix(arg, "-") {
				o.query = arg
				continue
			}
			return o, fmt.Errorf("unknown argument %s; run freeway --help", strconv.Quote(arg))
		}
	}
	if o.json && o.mode != "sessions" {
		return o, fmt.Errorf("--json is supported by freeway sessions")
	}
	return o, nil
}

const usage = `Freeway — a free AI workspace

  freeway                         Start a new conversation
  freeway continue                Continue the latest session in this project
  freeway -c                      Same as continue
  freeway resume                  Pick a saved session
  freeway resume ID_OR_TITLE      Resume by ID, unique ID prefix, or title
  freeway resume --last            Continue the latest session
  freeway resume --all             Pick from all projects
  freeway sessions [--all]         List saved sessions
  freeway sessions --json          Export session metadata as JSON

  --new / -n       Start fresh
  --resume / -r    Open the session picker, or pass an ID/title
  --continue / -c  Continue the latest session in this project
  --no-save       Keep this session in memory only
  --no-color      Disable terminal colors
  --preview       Offline UI preview

In the UI: /resume, /fork, /rename TITLE, /new, /queue, /help

Native tools:
  freeway providers COMMAND  Native provider authentication and diagnostics
  freeway --version          Print the native client version
  freeway install --archive PATH --sha256 SHA256 [--root PATH]
  freeway update check       Check the trusted native release manifest
  freeway update             Verify and activate a new native version
  freeway rollback           Restore the previous program version
  freeway uninstall          Remove the program while preserving user data
  --url URL       Local Go gateway address (or FREEWAY_URL)

Connected mode uses FREEWAY_API_KEY. No Node.js or POC runtime is required.
`
