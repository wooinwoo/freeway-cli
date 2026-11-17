# Freeway CLI POC

This directory contains the existing proof of concept and temporary CLI provider adapters used by the Go TUI. It is not the planned Go backend. Run source commands from `POC/`. Planning and architecture documents live in the repository root under `docs/`.

The source checkout now defaults to `POC/.data/`. To reuse data from the former repository-root `.data/`, set `FREEWAY_DATA_DIR` to that directory's absolute path for both the CLI and server. Existing data is not moved or deleted automatically.

Free hosted AI in a full-screen terminal chat UI, with a JSON subagent mode and an MCP stdio server. No browser required. Windows x64 installer includes Node.js; you do not need Node or npm beforehand.

## Install (PowerShell)

```powershell
irm https://raw.githubusercontent.com/wooinwoo/freeway-cli/main/POC/install.ps1 | iex
```

Then run:

```powershell
freeway
freeway "Explain this code"
freeway models
```

`freeway` opens the terminal UI: a conversation view with formatted code, a bordered multi-line composer, live generation status, searchable model and conversation pickers, and keyboard controls. `freeway --plain` retains the simple line interface for terminals that need it. Headless and MCP output remain machine-readable.

| Key | Action |
| --- | --- |
| Enter | Send |
| Ctrl+O (also Ctrl+J) | Insert a new line |
| Ctrl+P | Select a model; recommended models are sorted first |
| Ctrl+L | Search saved conversations |
| PgUp / PgDn | Scroll conversation |
| Esc | Stop generation or close a menu |
| Ctrl+R | Regenerate the last answer |
| Ctrl+N | New conversation |

Type `/` for commands. `/settings` changes output length, `/copy` copies the latest answer on Windows, `/export` saves Markdown, and `/exit` closes the UI.

The installer downloads a checksum-verified release into your per-user application directory and registers the command on your user PATH. No administrator privileges are required. Reopen your terminal if it does not see the new command. An internet connection is needed for AI inference.

Default model: **NVIDIA Nemotron 3 Ultra**, through Kilo's zero-priced route. Default output budget: 4,096 tokens. Provider free quotas and availability still apply; no provider credentials or conversation history ship with the package. This is a personal client, not a source of unlimited API access.

## CLI providers

The temporary gateway can also call the unmodified official OpenCode and Cline CLIs installed on the user's PC. It does not proxy their private inference endpoints or switch to paid models when free usage runs out.

```sh
node cli.mjs connect opencode
cline auth cline
node cli.mjs connect cline
```

Executables are discovered on `PATH`. `OPENCODE_BIN` and `CLINE_BIN` can specify an absolute path. The Cline adapter uses the default signed-in CLI account at `~/.cline/data/settings/providers.json`. It checks that credentials exist; the official CLI handles token refresh and requests. Keys and tokens are never returned by Freeway's state endpoint or committed to this repository.

- **OpenCode:** starts its authenticated loopback server, registers only its own zero-priced models, and uses its official session API. Tool permissions deny everything except an approval-gated read schema, which keeps the free route usable. Every read permission request is rejected and its session is aborted before file contents can be returned. Legacy `tools` flags are not sent because they can overwrite session permissions. Reasoning parts are excluded from the answer. Free-limit responses never trigger a paid subscription.
- **Cline:** registers only the official catalog's `free` array. Recommended and ClinePass lists are excluded. Each request starts the official CLI in ACP mode, creates a session, explicitly selects the free model with `session/set_model`, and only then sends the conversation. ACP ignores the normal CLI model flag, so the explicit model selection is required to avoid the default model. Agent reasoning is excluded from the text stream. Tool permission requests are denied, and client filesystem/terminal capabilities are not offered.
- **Cancellation:** aborting a request terminates its Cline process group and removes its transient workspace. Cline may retain its own session history under its normal data directory; this is separate from Freeway's saved chat API.

Both CLI providers are text-only and send conversation history as text. CLI-specific output limits are controlled by each official client; Freeway's `max_tokens` value is not an enforced output cap for these bridges. Their free quotas can expire independently of the model catalog, so a catalog entry does not guarantee that a request will succeed.

Gemini CLI's personal Google-login route is excluded: Google ended consumer-account support on June 18, 2026, and the official CLI rejects the account after authentication. See [Google's deprecation notice](https://developers.google.com/gemini-code-assist/docs/deprecations/code-assist-individuals). No credential or client-identity workaround is included.

Focused adapter tests use local fixtures and make no model requests:

```sh
npm test
```

The picker shows the full synchronized catalog, including DeepSeek and Muse when present in a connected provider's catalog. Each row states whether it is free, needs a connection/key, is temporarily unavailable, or is paid. Paid and unverified-price models remain visible for investigation but cannot be selected for free inference. To connect OpenCode explicitly, run `freeway connect opencode` or `/connect opencode` inside the TUI. On a fresh installation, an existing OpenCode executable is discovered automatically; an explicitly disabled provider remains disabled.

The same model from multiple providers appears once, with its routes nested underneath. Model IDs preserve versions, dated releases, Flash/Pro, and other variants. Grouping uses upstream IDs and known manufacturer namespaces; ambiguous aliases remain separate. `freeway models --json` exports groups with all route metadata. `/v1/models` and the MCP model tool also return distinct models. Existing provider-specific IDs remain accepted for an exact-route request.

For a model group, Freeway tries ready, confirmed-free routes in provider-priority order. If a route fails before output begins, it can try another route for the same model. Paid routes are excluded. Once streaming starts, it never switches routes. `free/auto` tries model groups in the configured preference order; the fallback limit counts models, not duplicate routes. This is a configurable recommendation order, not a measured performance leaderboard.

```powershell
freeway priority
freeway priority model/deepseek/deepseek-v4.1-flash model/nvidia/nemotron-3-ultra-550b-a55b
freeway --model free/auto
freeway priority reset
```

Inside the TUI, `/priority` promotes the selected model to first place and selects Auto for the current conversation. The priority command changes Auto's ordering; a directly selected model continues to use its own group. The initial recommendation order is DeepSeek V4.1 Flash, MiniMax M3.1 Flash, GLM-5.3-Flash, Nemotron 3 Ultra, then Nemotron 3.5 Super VL. Availability and free quotas can cause Auto to skip unavailable models.

## Use as a subagent

```powershell
freeway agent --json "Review this implementation and suggest improvements"
Get-Content task.txt -Raw | freeway agent --json
```

Successful stdout is one JSON object with `ok`, `model`, `provider`, `content`, `usage` when available, and `elapsedMs`. Failures emit an error object and a nonzero exit code. Headless tasks are not saved to conversation history unless `--save` is passed. `--stream-json` emits newline-delimited chunk events followed by a result.

```powershell
freeway --model kilo/nvidia/nemotron-3.5-super-vl:free "Your question"
freeway agent --json --system "You are a code reviewer" --max-tokens 4096 "Your task"
```

## MCP / OpenCode

Launch an existing OpenCode installation with the subagent tool connected:

```powershell
freeway opencode
```

This passes a local MCP server called `freeway` through OpenCode's per-process configuration. It preserves your global configuration files and supports V1/V2 configuration shapes. In OpenCode, ask the main agent to use `freeway_delegate` for a second opinion or a self-contained task. `freeway setup opencode` checks that OpenCode can be found. For a permanent manual entry, `freeway mcp-config` prints both OpenCode formats and the standard MCP format.

For other MCP clients:

```powershell
freeway mcp-config
```

Copy the `standard.mcpServers.freeway` entry into your client's MCP configuration. The command prints absolute paths to the installed bundled runtime. `freeway mcp` speaks newline-delimited JSON-RPC on stdio; all protocol output stays on stdout. Available tools:

- `freeway_delegate`: a task, optional system instructions/model/token budget; returns an AI answer.
- `freeway_models`: available free models and the configured default.

The subagent provides text analysis and suggestions. It does not execute shell commands, edit local files, or autonomously run tools.

## Interactive history

Interactive sessions save locally. Commands: `/new`, `/models`, `/model ID`, `/history`, `/open ID`, `/exit`. Resume a saved session with `freeway --session ID` or disable saving with `--no-save`. Saved conversations and provider keys are encrypted locally; browser/CLI access under the same OS account can access them. Provider retention policies are separate.

The CLI automatically starts a loopback gateway in the background when necessary. `freeway status` reports its status; `freeway serve` runs it in the foreground for troubleshooting. `FREEWAY_URL` can select another local gateway port. An optional browser client remains available at `http://127.0.0.1:4141`.

## Runtime

The Windows release includes Node.js and its full license notice under `runtime/LICENSE.txt`. Source runs on Node.js 22 or later with no third-party npm dependencies.

Official references: [Kilo free models](https://kilo.ai/docs/getting-started/using-kilo-for-free), [Nemotron 3 Ultra](https://build.nvidia.com/nvidia/nemotron-3-ultra-550b-a55b/modelcard), [MCP stdio](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports), [OpenCode MCP](https://opencode.ai/docs/mcp-servers/).
