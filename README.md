# Freeway CLI

Free hosted AI in a full-screen terminal chat UI, with a JSON subagent mode and an MCP stdio server. No browser required. Windows x64 installer includes Node.js; you do not need Node or npm beforehand.

## Install (PowerShell)

```powershell
irm https://raw.githubusercontent.com/wooinwoo/freeway-cli/main/install.ps1 | iex
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
