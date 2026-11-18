# Native client delivery

The authorized scope is the Go TUI, native provider integrations, the public documentation site, and product installation and updates. The common Go backend is implemented last. The final product must not require Node.js or the historical POC. Node.js may be used to build the statically exported documentation site; the published files require no Node server.

## Acceptance conditions

- TUI: an authenticated loopback HTTP client replaces the Node bridge. Model discovery, text streaming, explicit cancellation, settings, attachments, conversation revisions, history, project scope, stashed drafts, and exports work against a deterministic Go fixture gateway. Streaming must not block independent saves. Session changes reject late saves and responses. No provider selection policy or user database is implemented inside the TUI.
- Providers: the existing native HTTP and official CLI adapters retain their boundaries. Credential changes invalidate free-account confirmations. Concurrent confirmation and key replacement cannot restore an old credential. Tests use isolated credentials and local protocol fixtures. Actual vendor access is reported separately from fixture coverage.
- Documentation: a responsive Astro + Starlight site follows the OpenRouter documentation structure, with quickstart, installation, provider connection, CLI usage, sessions, updates, troubleshooting, and privacy/usage conditions. Search, code copying, page navigation, light/dark themes, and language navigation work in a static export. Examples match the actual native commands. Backend-dependent operations are labeled accurately.
- Updates: versioned native artifacts contain the CLI, provider utility, licenses, and a package manifest. Installation, checksum verification, safe extraction, activation, rollback, and removal preserve user data. No Node or POC files enter the product archive. A failed update leaves the previous installation active. Release publication is outside this authorization.

## Backend handoff

`api/openapi.yaml` and `cli/client` define the versioned authenticated local HTTP boundary. The backend owns routing, quota accounting, persistent conversations and secrets, single-writer locking, schema changes, and crash recovery. The fixture gateway is test support, not a substitute implementation shipped as the backend.

Use an isolated backend data directory and a deterministic provider adapter for integration. Verify these outcomes before enabling connected chat in the release Quickstart:

| Boundary | Acceptance evidence |
| --- | --- |
| Loopback and Bearer key | The server listens on loopback. Missing or wrong keys receive 401 before model, stream, or conversation operations. Responses and logs omit the key. |
| Catalog and routing | The catalog reports explicit `free` and `ready` flags. `free/auto` exists only with an eligible route. When eligibility or allowance is missing, the gateway rejects the request with a structured error. |
| Streaming and cancellation | The installed TUI sees partial text before `[DONE]`, then records the actual model. A disconnect cancels the provider request. Saving a conversation remains possible during the stream. Once text has been sent, the gateway keeps the same route. |
| Conversation revisions | Creating returns 201, an ID, and revision 1. Two writes from the same revision produce one success and one 409 `chat_conflict`, including writes from different client processes. After a failed persistence attempt the old data and revision still load. |
| Resume and crash recovery | After reopening the TUI, the completed reply, stopped partial reply, model, and draft or stash load intact. After restarting the gateway, the same records remain available. Persisted interrupted replies reopen as stopped and their partial text stays out of the next model request. |
| Quota and errors | Run the quota cases with a deterministic upstream that reports success, quota exhaustion, and failure. Verify reserve, settle, and release once per request, including cancellation. Failure responses use the OpenAPI error envelope. |

`tests/e2e/native_smoke.py` exercises the client side in a real pseudo-terminal using `tests/e2e/gateway_scenario.py`. It proves model selection, partial text before completion, saving a draft during streaming, Escape disconnect, revision-checked save, and resume across two TUI processes. The test gateway keeps state in memory to verify the HTTP exchange. Restarting real gateway storage, enforcing server-side authentication and quotas, and reaching an actual upstream are integration gates for the backend phase.

Run `python3 tests/install/release_smoke.py dist/native --go go` for the package lifecycle. It checks six release archives and their manifests, installs a previous fixture version, checks a loopback release, rejects a damaged download, upgrades, corrupts the active utility, rolls back, and verifies that uninstall preserves account data. The [preflight workflow](../../.github/workflows/preflight.yml) builds the shared candidate, runs native checks on three OS runners, and builds both documentation locales. A version tag must match `VERSION`; the workflow uploads an Actions artifact.

## Verification

Run native CLI and provider tests with real vendor probes disabled, then run product archive/install failure tests and the documentation build. Inspect documentation at desktop and mobile widths, including search and keyboard navigation. Cross compilation is not evidence of execution on another OS. Preserve outstanding real-OS and real-account checks in the handoff rather than reporting them as passed.

## Verified delivery — 2026-11-18

- CLI: `FREEWAY_LIVE=0 go -C cli test -race ./...` passed, including authenticated HTTP/SSE, stream cancellation, independent saves, session lifecycle, attachment loading, revision conflicts, and bounded prompt history.
- Providers: `FREEWAY_LIVE=0 go -C backend test -race ./...` passed. A stale free-account confirmation cannot overwrite a concurrently replaced key in the native stores. Existing custom Load/Save stores remain compatible; their concurrency responsibility is documented in the provider API.
- Packages: `go -C cli run ./cmd/package --root "$PWD"` built Linux, macOS, and Windows for amd64 and arm64. All six archives passed manifest, file-list, file-hash, archive-hash, and no-Node/POC checks. Each contains 27 files, including 22 license notices.
- Installation: `python3 tests/e2e/native_smoke.py dist/native/freeway_0.6.0-dev_linux_amd64.tar.gz` passed. This exercises a real packaged Linux binary with no external PATH commands: offline TUI startup/exit, provider utility, authenticated scoped/all session listing, failed-install isolation, active-file corruption, removal, and user-data preservation. Installer unit tests cover staged failures, immutable versions, safe extraction, remote fixtures, locks, cancellation, updates and rollback.
- Documentation: Astro + Starlight replaced the earlier Fumadocs implementation at the user's preference. `npm --prefix docs/site run typecheck` and `npm --prefix docs/site run build` passed. Browser checks covered 18 English/Korean pages, 26 local links, English/Korean search, clipboard copying, language switching, light/dark themes, mobile navigation at 390px, and no browser exceptions. Public third-party/font licenses are included.
- Independent reviews reproduced and verified fixes for stale title saves, file selection during session loading, bounded history order, rollback from a damaged active version, Windows removal retry paths, and explicit backend availability in both Quickstarts. No substantiated review finding remains open.

## Remaining backend and release boundaries

The original client handoff verified connected conversations against deterministic fixtures. The current package bundles the implemented Go gateway. The Windows amd64 package test now starts that gateway and checks authentication and encrypted draft recovery across a restart. See the [current architecture](architecture.md) for gateway responsibilities. Actual API access for every vendor was not re-probed. macOS execution, Windows interactive TUI behavior, Windows removal after prolonged competing executable locks, Windows arm64 execution, and signing remain separate verification. Release publication, public hosting, and the production documentation domain were not performed. The static site's default sitemap integration skips generation until a public `site` URL is configured. Default Starlight translation/404 fallbacks are used.

## Additional direct evidence — 2026-11-18

- Linux: the packaged executable passed the native smoke test again, including pseudo-terminal preview startup and clean exit, scoped session fixtures, installation failure isolation, removal and user-data preservation.
- Kilo: `FREEWAY_LIVE=1 FREEWAY_LIVE_PROVIDER=kilo FREEWAY_LIVE_MODEL=reflection/beam-free go -C backend test -v -count=1 -timeout 4m -run '^TestNativeLive$' ./internal/providers` passed against the real anonymous service. The catalog returned 13 current free models; streaming `kilo/reflection/beam-free` passed at both low and high reasoning settings. This is actual upstream evidence, distinct from fixture coverage. Other vendors were not inferred to pass from this result.
- Windows amd64: the actual Windows executable ran on Windows NT 10.0.26200.0. In an isolated Windows temporary directory, archive installation, checksum/native-version probes, installed launcher execution, listing 11 provider definitions, detached removal, and preserving a sibling user-data marker all passed. The test did not execute a model request or render an interactive Windows TUI. Its temporary installation was removed.
- Documentation: the user rejected the initial stock Starlight presentation. Custom header, sidebar, page-title/copy and theme components now follow the actual OpenRouter reference with matching Plus Jakarta Sans/Geist Mono, two-row navigation, narrow reading column and dark default. Independent screenshot comparison returned SHIP with no material findings. Updated browser checks passed 18 pages, 24 local links, EN/KO search, page/code copying, language navigation, explicit and system themes, and 390px mobile menu without browser exceptions.
- The existing system Cline command is a Node.js script, so it was not used as evidence for the native-only product. A native Cline installation and authenticated live request remain unverified in this follow-up. The docs preview stays available at `http://localhost:42137/ko/docs/` while its local server process is running.

## Design finish verdict: SHIP

| Reviewed item | Verdict |
| --- | --- |
| Replacing the stock Starlight presentation | resolved |
| Matching reference header, columns and typography | resolved |
| Mobile composition and Freeway identity | resolved |
| Explicit unfinished gateway boundary | resolved |
| Required material fixes | none |

The final user-requested text-only wordmark and prominent Linux/macOS/Windows installation examples were also verified in the browser. The first section is installation, its Windows command copies correctly, and the wordmark has no adjacent image or SVG. Functional checks still passed all 18 pages and 24 local links without browser exceptions.

The user selected emerald green as the final accent. It is now the default in both themes; temporary blue/green URL and session-storage overrides were removed. Browser verification confirmed green with no query, old comparison URLs, and stale blue session data, with no mobile overflow or browser exceptions. Documentation build and type checks passed for the finalized source.

## Initial installation bootstrap

Root `install.sh` and `install.ps1` now prepare the first native executable, verify the archive SHA256, and invoke the existing Go installer. They support an official release directory, a local archive plus trusted hash, and explicit source builds. They leave account/conversation data outside the application directory and print the PATH setup rather than editing shell profiles or machine settings.

`tests/install/bootstrap_smoke.py` passed Unix local/download/source installation without Node, relative paths and spaces, invalid checksum, missing/ambiguous release, corrupted download, and preserved existing installation state. Its actual Windows PowerShell companion passed relative archive/root paths, native utility execution, signed-query download redirects, failure preservation, detached removal, and a surviving sibling data marker. Independent verification reproduced and closed the signed-query, relative-PATH, and isolated-source-test findings. Public new native release assets remain unpublished; current Quickstart uses source installation with Go 1.26+. macOS/ARM64 execution and Windows source compilation remain unverified.
