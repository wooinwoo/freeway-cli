---
title: Local gateway
description: Connect the terminal workspace to a local API v1 server.
---

The shared Go gateway is pending. The current checkout supports [offline preview and direct provider requests](../) as independent commands. Use the steps below once you have a compatible local API v1 server.

## Connect

Run the server on a loopback address and obtain a client key from it. For Bash or Zsh, export the address and key in the terminal where you will run Freeway.

```sh
export FREEWAY_URL=http://127.0.0.1:4141
read -rs FREEWAY_API_KEY
export FREEWAY_API_KEY
freeway
```

For PowerShell, set `$env:FREEWAY_URL` and `$env:FREEWAY_API_KEY` in the current process. The client key authenticates Freeway to the local gateway. Configure provider keys separately through the gateway's account setup.

Open `/model`, select an available model, and send a prompt. Press Esc to cancel a stream. Use `freeway sessions` to list project sessions and `freeway resume SESSION_ID` to reopen one.

## Integration contract

The [OpenAPI v1 specification](https://github.com/wooinwoo/freeway-cli/blob/main/api/openapi.yaml) defines bearer authentication, ready/free model discovery, SSE completions, and revision-checked conversation storage. The [backend handoff](https://github.com/wooinwoo/freeway-cli/blob/main/docs/development/client-delivery.md#backend-handoff) records the integration acceptance checks.
