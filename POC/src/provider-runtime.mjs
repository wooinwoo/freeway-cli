import { readFileSync, accessSync, constants } from 'node:fs';
import { homedir } from 'node:os';
import { isAbsolute, join } from 'node:path';

export function providerHome() {
  return process.env.FREEWAY_PROVIDER_HOME || join(homedir(), '.freeway', 'providers');
}

export function managedExecutable(name) {
  try {
    const manifest = JSON.parse(readFileSync(join(providerHome(), 'install.json'), 'utf8'));
    if (manifest.schemaVersion !== 1) return null;
    const path = name === 'node' ? manifest.node?.path : manifest.tools?.[name]?.path;
    if (typeof path !== 'string' || !isAbsolute(path)) return null;
    accessSync(path, process.platform === 'win32' ? constants.F_OK : constants.X_OK);
    return path;
  } catch {
    return null;
  }
}
