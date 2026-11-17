import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

function shellQuote(value) {
  return "'" + value.replaceAll("'", "'\"'\"'") + "'";
}
function psQuote(value) {
  return "'" + value.replaceAll("'", "''") + "'";
}

export function writeLaunchers({ root, nodePath, repo, directory }) {
  const bin = join(root, 'bin');
  const manage = join(repo, 'backend/internal/providers/manage.mjs');
  mkdirSync(bin, { recursive: true });

  for (const name of ['freeway-providers', 'freeway-provider-setup']) {
    const setup = name === 'freeway-provider-setup';
    if (process.platform === 'win32') {
      const text = setup
        ? `param(
  [ValidateSet('install','update','check')][string]$Action = 'install',
  [switch]$ApiOnly,
  [ValidateSet('opencode','cline')][string]$Only,
  [switch]$Help
)
$parameters = @{Action=$Action; Root=${psQuote(root)}; ApiOnly=$ApiOnly; Help=$Help}
if ($Only) { $parameters.Only = $Only }
& ${psQuote(join(directory, 'providers.ps1'))} @parameters
exit $LASTEXITCODE
`
        : `param([Parameter(ValueFromRemainingArguments=$true)][string[]]$ForwardArgs)
$env:FREEWAY_PROVIDER_HOME = ${psQuote(root)}
& ${psQuote(nodePath)} ${psQuote(manage)} @ForwardArgs
exit $LASTEXITCODE
`;
      writeFileSync(join(bin, name + '.ps1'), text);
    } else {
      const command = setup
        ? `/bin/bash ${shellQuote(join(directory, 'providers.sh'))} "$@" --root ${shellQuote(root)}`
        : `${shellQuote(nodePath)} ${shellQuote(manage)} "$@"`;
      const text = `#!/bin/sh
export FREEWAY_PROVIDER_HOME=${shellQuote(root)}
exec ${command}
`;
      writeFileSync(join(bin, name), text, { mode: 0o755 });
    }
  }
  return bin;
}
