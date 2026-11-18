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

Configure model-provider credentials with `freeway providers auth PROVIDER`. In a chat using the automatically started gateway, select **OpenCode…** in the model menu to use the installed OpenCode CLI. OpenCode, Cline and NVIDIA are also available through the direct provider tool. Auto selects from eligible HTTP providers.

Open `/model`, select an available model, and send a prompt. Press Esc to cancel a stream. Use `freeway sessions` to list project sessions and `freeway resume SESSION_ID` to reopen one.

## Integration contract

The [OpenAPI v1 specification](https://github.com/wooinwoo/freeway-cli/blob/main/api/openapi.yaml) defines bearer authentication, ready/free model discovery, SSE completions, and revision-checked conversation storage. The [backend handoff](https://github.com/wooinwoo/freeway-cli/blob/main/docs/development/client-delivery.md#backend-handoff) records the integration acceptance checks.
