#!/usr/bin/env bash
# Run this checkout's installer, authentication, discovery, and a real free-model probe.
set -euo pipefail

repo="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
provider="${FREEWAY_PROVIDER:-kilo}"
model="${FREEWAY_MODEL:-}"
if [[ -z "$model" ]]; then
  [[ "$provider" == kilo ]] || { echo 'Set FREEWAY_MODEL to a free model from the chosen provider.' >&2; exit 2; }
  model=reflection/beam-free
fi

/bin/bash "$repo/scripts/install/providers.sh" install
provider_home="${FREEWAY_PROVIDER_HOME:-$HOME/.freeway/providers}"
provider_cli="$provider_home/bin/freeway-providers"

"$provider_cli" auth "$provider"
"$provider_cli" models "$provider"
"$provider_cli" quota "$provider"

reasoning=()
if [[ -n ${FREEWAY_REASONING_EFFORT:-} ]]; then
  reasoning=(--reasoning-effort "$FREEWAY_REASONING_EFFORT")
fi
# A wrong response, authentication failure, or exhausted quota must fail this example.
"$provider_cli" probe "$provider" --model "$model" --timeout 120 "${reasoning[@]}"
