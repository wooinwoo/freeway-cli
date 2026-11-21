# Codex · Claude Code · Freeway usability audit

2026-11-17, Windows. Installed versions: Codex CLI **0.169.0**, Claude Code **2.1.317**. The comparison uses the installed CLIs in an isolated QA folder, with their normal authentication and permission settings.

## Evidence

The applications were launched interactively through a real PTY. Keyboard input was sent to their running processes. Their ANSI screen output was retained and replayed in xterm at **80 × 24** cells to produce PNGs. This captures terminal contents; it does not capture Windows window chrome or the desktop. No reference screenshots were generated from Freeway fixtures.

Open the [capture gallery](../../build/reference-qa/index.html) locally. Each screen has a PNG and terminal text. The capture directory is an ignored local build artifact.

- [Codex keyboard settings](../../build/reference-qa/codex-keymap.png)
- [Codex prompt search](../../build/reference-qa/codex-history.png)
- [Codex session picker](../../build/reference-qa/codex-resume.png)
- [Claude shortcut help](../../build/reference-qa/claude-help.png)
- [Claude prompt search](../../build/reference-qa/claude-history.png)
- [Claude session picker](../../build/reference-qa/claude-resume.png)
- [Freeway restored draft, attachment and paused queue](../../build/reference-qa/freeway-resume.png)
- [Freeway prompt search](../../build/reference-qa/freeway-history.png)
- [Freeway continue after closing the process](../../build/reference-qa/freeway-continued.png)
- [Freeway project file picker](../../build/reference-qa/freeway-files.png)

## Directly exercised reference workflows

| Workflow | Codex | Claude Code | Freeway result |
| --- | --- | --- | --- |
| Discover shortcuts | `?`, searchable `/keymap` with 154 actions | `?` shows contextual shortcuts | F1 and `/help`; updated descriptions match bindings |
| Discover slash commands | `/keymap`, `/resume` completion | `/resume`, `/exit` completion | Filtered suggestions; Up/Down selection and Tab completion |
| Send and receive | Sent a tools-disabled QA prompt; received `CODEX_UX_OK` | Sent a tools-disabled QA prompt; received `CLAUDE_UX_OK` | Real free-model reply `FREEWAY_UX_OK`; saved before reopening |
| Recall previous input | Up restored the QA prompt | Up restored the QA prompt with a history position indicator | Up/Down and Ctrl+P/N; multiline edge handling; draft and attachment restoration |
| Search prompts | Ctrl+R opened reverse search and matched the QA prompt | Ctrl+R opened searchable prompt history with preview | Ctrl+R opens type-to-filter saved prompt history; Enter restores editable input |
| Browse sessions | `/resume` displayed the saved QA session; cwd/all and status controls visible | `/resume` displayed project scope/search; the active session was excluded | `freeway resume`, ID/prefix/title lookup; Ctrl+A switches project scope |
| Reopen after exit | Exited, then reopened the QA session by its printed session ID; prior response restored | Exited, then reopened the QA session by its printed session ID; prior response restored | Exited, then `freeway continue` restored the real response and unsent Unicode draft |
| Mention files | `@` is listed in shortcut help; selection was not exercised | Typed `@he`, observed file suggestions | `@` opens actual workspace text files; selected content is included in model context |

The installed Codex build shows **Tab** for send/queue and **Enter** among newline bindings. Claude uses Enter to send. Freeway retains Enter to send, Ctrl+J/Shift+Enter/Alt+Enter for newline, and Tab/Enter to queue while generating. The two reference applications do not have identical bindings.

## Additional Freeway improvements and verification

- Ctrl+U/K/W delete input; Ctrl+Y yanks deleted text, Alt+Y cycles it. Ctrl+Z and Ctrl+Shift+Z undo/redo Unicode edits.
- Ctrl+S stashes/restores text and attachments. Ctrl+G opens VISUAL/EDITOR or the default editor. Editor results arriving after a session change are kept in the stash.
- Esc closes an open menu before interrupting generation. Ctrl+C interrupts a response, clears recoverable input, and requires a second press on empty input to quit.
- Esc twice clears a draft, or edits the last prompt in a new fork when input is empty. `/fork`, `/rename`, `/continue`, `/resume` and `/new` manage sessions.
- Follow-ups remain FIFO; Up takes back the latest queued item. `/queue` supports review, pause, resume and clear. Restored queues start paused.
- Session metadata includes workspace, draft and queued-item indicators. Reading saved sessions precedes model discovery, so model availability does not gate the session picker.
- Active-response menus preserve the draft. Streaming updates are coalesced and prior message rendering is cached; existing long-history and burst tests remain passing.

Go tests cover editing, Unicode history, menu behavior during generation, FIFO queueing, project scope, file boundaries, editor/session races, explicit empty-array updates, and file contents reaching the model. All Go packages pass. All **14 POC tests** pass.

A live installed-gateway check wrote and reopened only its own QA session. A separate check through the actual Go bridge verified that clearing queue/attachments persists as empty arrays, the real model reply remains saved, and an unsent Unicode draft survives reopening. This caught and fixed an omission bug that could resurrect cleared context on restart.

## Remaining differences

This audit covers the common chat workflow. Freeway has transcript search, text attachments, and separate OpenCode and MCP integrations. The reference applications also include session archiving and deletion, customizable keymaps, image input, permissions, tool execution, file edits and diffs, and agent controls.

External-editor launch was inspected in the implementation and its asynchronous result behavior was tested; an interactive edit in an external GUI editor was not exercised in this comparison. The captures also do not establish native-window frame timing or IME behavior.

## Follow-up regression queue

The Windows regression checks for the Freeway `0.6.0-rc.7` development build use synthetic prompts and temporary installs.

| Observed failure | Regression check |
| --- | --- |
| Installing the same version could leave a damaged installation unusable. | A checked release and a verified local archive each repair a damaged executable. A healthy installation skips an equal-version download. |
| Opening another conversation after a recovery save cleared the current draft if the target failed to load. | Failed navigation retains the draft, attachments, and recovery path. Edits made during loading stay in the source conversation. |
| A route marked ready rejected a default reply under its local token allowance. | F2 and F4 disclose the local allowance. The gateway reports a fitting reply length before sending upstream. F5 uses that length for one retry. |
| A failed conversation read discarded prompt matches from healthy conversations. | The current gateway returns prompt history with an incomplete flag in a single response. Older gateways retain successful results on partial failure. |
| Recovery files required copying text by hand. | F7 and `/restore` return a draft and attachments to the composer. Undo preserves the previous draft. |
| Follow-up requests could silently include the entire earlier transcript and resend old attachments. | F8 lists the included questions and file names, offers turn and attachment controls, and keeps them for the conversation. An 80×24 terminal scenario checks that the current attachment reaches both the request and its retry, while later requests exclude it after the user changes the setting. |
| A completed attachment could insert a file reference into a newly edited draft. | The attachment list updates independently of the prompt. Inline completion replaces the original token when the draft still matches. |
| Startup and history browsing fetched every conversation summary. | `freeway continue` asks for one usable conversation. A fresh session skips history reads. The picker loads 50 summaries at a time and searches across the current scope, including records outside the loaded pages. Tests cover equal timestamps, cursor validation, older records, and late responses after a scope change. |
| Prompt search opened with prompts from other projects and read saved conversations during startup. | Ctrl+R loads recent saved prompts on demand for the current workspace. Ctrl+A can switch scope from an empty project. A late project-scoped response cannot replace results for all projects. |
| Excluding one earlier attachment required dropping every earlier file. | F8 excludes earlier copies of a stored path independently. The terminal scenario checks that the other file reaches the next request and the exclusion survives reopening. Unit tests check the same path on the current question and a retry. |

On Windows, a synthetic library of 20,000 indexed conversations took 27.46 ms per operation to copy and JSON-encode the complete list (5,427,790 bytes). A 50-record project page took 0.083 ms (13,736 bytes), and a search that scanned the project for a single old record took 8.27 ms (275 bytes). This benchmark starts with a warm in-memory summary index and measures gateway list construction and JSON encoding.

### Open items

1. Add model-specific input limits to the context inspector and reply estimate.
2. Exercise Korean IME composition, multiline paste, and focus changes on Windows Terminal and other supported terminals. The 80×24 native test uses synthetic keyboard input.
3. Measure keyboard-to-render latency during history search and cold gateway startup with large encrypted libraries. The gateway still loads encrypted records to build the initial index.
4. Make shortcut discovery searchable by action and evaluate remapping conflict handling. Compare against the same tasks used in the reference captures.
