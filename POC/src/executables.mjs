import { accessSync, constants } from 'node:fs';
import { delimiter, join } from 'node:path';

export function findExecutable(name, override) {
  const windows = process.platform === 'win32';
  const candidates = override ? [override] : (process.env.PATH || '').split(delimiter).filter(Boolean).flatMap(dir => {
    if (!windows) return [join(dir, name)];
    const packageRoot = join(dir, 'node_modules');
    return [join(dir, name + '.exe'),
      ...(name === 'opencode' ? [join(packageRoot, 'opencode-ai', 'bin', 'opencode.exe')] : []),
      ...(name === 'cline' ? [join(packageRoot, 'cline', 'bin', '.cline'),
        join(packageRoot, '@cline', 'cli-windows-' + process.arch, 'bin', 'cline.exe'),
        join(packageRoot, 'cline', 'node_modules', '@cline', 'cli-windows-' + process.arch, 'bin', 'cline.exe')] : [])];
  });
  return candidates.find(path => {
    // npm .cmd/.bat shims cannot be spawned without a shell. Resolve native binaries instead.
    if (windows && /\.(cmd|bat)$/i.test(path)) return false;
    try { accessSync(path, process.platform === 'win32' ? constants.F_OK : constants.X_OK); return true; }
    catch { return false; }
  }) || null;
}
