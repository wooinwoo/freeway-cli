---
title: Local gateway
description: Connect the terminal workspace to a local API v1 server.
---

## Connect

Run `freeway` to start or reuse your local gateway automatically. The automatic listener uses a free loopback port. Your conversations and usage live in `~/.freeway/gateway`. Set `FREEWAY_GATEWAY_HOME` to use a separate local profile.

## Fixed address for API clients

Run the bundled gateway in one terminal. It listens on `127.0.0.1:4141` and stores conversations and usage in `~/.freeway/gateway`.

```sh
freeway gateway serve
```

In another terminal, set the client key and start Freeway.

```sh
export FREEWAY_API_KEY="$(freeway gateway key)"
export FREEWAY_URL=http://127.0.0.1:4141
freeway
```

```powershell
$env:FREEWAY_API_KEY = (freeway gateway key)
$env:FREEWAY_URL = 'http://127.0.0.1:4141'
freeway
```

If port 4141 is in use, pass `--listen 127.0.0.1:4142` to `freeway gateway serve` and set `FREEWAY_URL` to `http://127.0.0.1:4142`. For custom storage, pass the same `--data-dir` to both `serve` and `key`.

Configure model-provider credentials with `freeway providers auth PROVIDER`. The automatically started gateway includes the installed OpenCode CLI among the available routes for an explicitly selected model. OpenCode, Cline and NVIDIA are also available through the direct provider tool. Auto selects from eligible HTTP providers.

Open `/model`, select an available model, and send a prompt. Press Esc to cancel a stream. Use `freeway sessions` to list project sessions and `freeway resume SESSION_ID` to reopen one.

Auto uses the model quality order for its first attempt unless you set an explicit preference. F2 shows the currently eligible providers for a model. Each completed reply shows which provider and model answered.

## Image generation

Press F9 or run `/image-setup` to enter a trusted Streamable HTTP MCP endpoint. Choose an image tool and confirm that it cannot bill you before enabling it for Auto. The tool must accept a string `prompt` and return a PNG or JPEG as MCP image content or an embedded resource.

Ask Auto to “generate a cat logo on a black background,” or use `/image cat logo` with any selected model. Freeway saves the output under `freeway/images` in your user configuration directory. Use `/image-open` to open the latest image or `/image-path` to copy its path.

Press F9 to connect up to three image tools and choose which one Auto tries first. If a server is unreachable or its selected tool is unavailable before generation starts, Auto checks the next tool. If the result is uncertain after a generation call has started, Freeway does not resend the prompt to another tool. The saved result identifies the server and tool used.

Use `freeway image status` to check each tool without starting generation. Remove individual connections in F9 or clear them all with `freeway image disconnect`. Saved images remain available.

Choose **Browser sign-in** in F9 for public HTTPS MCP servers that support OAuth Dynamic Client Registration. The authorization and token endpoints must use the same HTTPS origin. Review the sign-in site and requested scopes before approving, then select the image tool. Freeway encrypts the credentials and binds them to that MCP URL. Freeway can renew expired access for public OAuth clients when the server issues a refresh token. Sign in again in F9 if the server revokes or did not issue a refresh token for your connection. You can also connect using a Bearer token environment variable. [MCP authorization](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization)

## MCP tool commands

Open `/mcp` in chat or run `freeway mcp manage` to save a server, check its connection and change its authentication. Use browser sign-in or a Bearer token environment variable. When one saved credential matches the exact URL, general MCP and F9 connections can share it.

`/mcp-ask QUESTION` sends the question and summaries of saved MCP tools and their inputs to the selected model. Review the server, tool, and JSON arguments. Press F8 to approve or Esc to cancel. Scroll to the end of long arguments before approving. Freeway calls the approved tool once and passes its text or structured result to the selected model for an answer. This request accepts no file attachments.

`freeway mcp tools` lists tools and input schemas across saved servers and F9 connections. Copy the `server` ID into `freeway mcp call --server ID --args-file arguments.json TOOL` to make one call. The ID stays stable when you reorder your connections. Set `--args-file -` to read a JSON object from standard input. For a one-time Streamable HTTP connection, pass `--url URL` and, if required, `--token-env FREEWAY_MCP_TOKEN`. The commands return JSON.

## Integration contract

The [OpenAPI v1 specification](https://github.com/wooinwoo/freeway-cli/blob/main/api/openapi.yaml) defines bearer authentication, ready/free model discovery, SSE completions, and revision-checked conversation storage. The [backend handoff](https://github.com/wooinwoo/freeway-cli/blob/main/docs/development/client-delivery.md#backend-handoff) records the integration acceptance checks.
