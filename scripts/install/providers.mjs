import {
  readFileSync,
  writeFileSync,
  mkdirSync,
  existsSync,
  realpathSync,
  chmodSync,
  renameSync
} from 'node:fs';
import { homedir } from 'node:os';
import { join, dirname, resolve, parse, delimiter } from 'node:path';
import { fileURLToPath } from 'node:url';
import { runProcess, versionOf } from './lib/process.mjs';
import { acquireInstallLock } from './lib/lock.mjs';
import { writeLaunchers } from './lib/launchers.mjs';

export { runProcess };

const directory = dirname(fileURLToPath(import.meta.url));
const repo = resolve(directory, '../..');
export const versions = JSON.parse(readFileSync(join(directory, 'versions.json'), 'utf8'));
for (const value of Object.values(versions))
  if (!/^\d+\.\d+\.\d+$/.test(value)) throw new Error('invalid_version_pin');

export function parseArgs(args) {
  let action = 'install',
    root = process.env.FREEWAY_PROVIDER_HOME || join(homedir(), '.freeway', 'providers');
  let tools = ['opencode', 'cline'],
    selected = false;
  if (args[0] && !args[0].startsWith('-')) action = args.shift();
  if (!['install', 'update', 'check'].includes(action))
    throw new Error('supported_commands: install, update, check');
  for (let i = 0; i < args.length; i++) {
    if (args[i] === '--root' && args[i + 1]) root = args[++i];
    else if (args[i] === '--api-only' && !selected) {
      tools = [];
      selected = true;
    } else if (args[i] === '--only' && !selected && ['opencode', 'cline'].includes(args[i + 1])) {
      tools = [args[++i]];
      selected = true;
    } else throw new Error('invalid_installer_argument');
  }
  root = resolve(root);
  if ([parse(root).root, resolve(homedir()), repo].includes(root))
    throw new Error('choose_a_dedicated_install_directory');
  const profile = readManifest(root).selectedTools;
  if (
    !selected &&
    action !== 'install' &&
    Array.isArray(profile) &&
    profile.every((id) => ['opencode', 'cline'].includes(id))
  )
    tools = profile;
  return { action, root, tools };
}

export function binaryCandidates(
  id,
  prefix,
  platform = process.platform,
  arch = process.arch,
  musl = !process.report?.getReport().header.glibcVersionRuntime
) {
  const system = platform === 'win32' ? 'windows' : platform;
  const executable = platform === 'win32' ? id + '.exe' : id;
  if (id === 'cline')
    return [join(prefix, 'node_modules', '@cline', `cli-${system}-${arch}`, 'bin', executable)];
  const base = `opencode-${system}-${arch}`;
  const packages =
    platform === 'linux' && musl
      ? arch === 'x64'
        ? [base + '-baseline-musl', base + '-musl']
        : [base + '-musl']
      : arch === 'x64'
        ? [base + '-baseline', base]
        : [base];
  return packages.map((name) => join(prefix, 'node_modules', name, 'bin', executable));
}

function npmCLI() {
  const bin = dirname(process.execPath);
  const candidates = [
    join(bin, 'node_modules/npm/bin/npm-cli.js'),
    join(bin, '../lib/node_modules/npm/bin/npm-cli.js')
  ];
  for (const directory of (process.env.PATH || '').split(delimiter).filter(Boolean)) {
    candidates.push(join(directory, 'node_modules/npm/bin/npm-cli.js'));
    try {
      const path = realpathSync(join(directory, 'npm'));
      if (path.endsWith('npm-cli.js')) candidates.push(path);
    } catch {}
  }
  const found = candidates.find(existsSync);
  if (!found) throw new Error('npm_not_found_use_bootstrap_installer');
  return found;
}

function readManifest(root) {
  try {
    const value = JSON.parse(readFileSync(join(root, 'install.json'), 'utf8'));
    return value.schemaVersion === 1 ? value : {};
  } catch {
    return {};
  }
}

export async function check(options, run = runProcess, manifest = readManifest(options.root)) {
  const names = ['node', ...options.tools];
  const components = [];
  for (const id of names) {
    const path = id === 'node' ? manifest.node?.path : manifest.tools?.[id]?.path;
    const installed = path && existsSync(path) ? await versionOf(path, run, options.signal) : null;
    components.push({
      id,
      path: path || null,
      installedVersion: installed,
      recommendedVersion: versions[id],
      updateAvailable: Boolean(installed && installed !== versions[id]),
      status: !installed
        ? 'not_installed'
        : installed === versions[id]
          ? 'current'
          : id === 'node' && Number(installed.split('.')[0]) >= 22
            ? 'compatible'
            : 'update_available'
    });
  }
  return {
    ok: components.every((c) => ['current', 'compatible'].includes(c.status)),
    command: 'check',
    root: options.root,
    components,
    apiProviders: {
      requiresProviderSoftware: false,
      authentication: 'provider_api_key_or_kilo_anonymous'
    }
  };
}

export async function install(options, { run = runProcess, npm = npmCLI } = {}) {
  if (
    !['linux', 'darwin', 'win32'].includes(process.platform) ||
    !['x64', 'arm64'].includes(process.arch)
  )
    throw new Error('unsupported_platform');
  if (Number(process.versions.node.split('.')[0]) < 22)
    throw new Error('node_22_or_newer_required');
  mkdirSync(options.root, { recursive: true });
  options.signal?.throwIfAborted();
  const lease = acquireInstallLock(options.root, run);
  try {
    const previous = readManifest(options.root);
    const manifest = {
      ...previous,
      schemaVersion: 1,
      repo,
      node: { path: process.execPath, version: process.versions.node },
      tools: { ...previous.tools },
      selectedTools: options.tools,
      updatedAt: new Date().toISOString()
    };
    let packagesChanged = false;
    for (const id of options.tools) {
      const prefix = join(options.root, 'cli', `${id}-${versions[id]}`);
      const complete = join(prefix, '.freeway-install-complete.json');
      let verified = false;
      try {
        const marker = JSON.parse(readFileSync(complete, 'utf8'));
        verified = marker.id === id && marker.version === versions[id];
      } catch {}
      let binary = binaryCandidates(id, prefix).find(existsSync);
      if (
        !verified ||
        !binary ||
        (await versionOf(binary, lease.run, options.signal)) !== versions[id]
      ) {
        packagesChanged = true;
        mkdirSync(prefix, { recursive: true });
        console.error(`Installing ${id} ${versions[id]}...`);
        await lease.run(
          process.execPath,
          [
            npm(),
            'install',
            '--global=false',
            '--prefix',
            prefix,
            '--ignore-scripts',
            '--include=optional',
            '--save-exact',
            '--no-audit',
            '--no-fund',
            '--registry=https://registry.npmjs.org/',
            `${id === 'opencode' ? 'opencode-ai' : 'cline'}@${versions[id]}`
          ],
          { capture: true, timeout: 600000, signal: options.signal }
        );
        binary = binaryCandidates(id, prefix).find(existsSync);
      }
      if (!binary) throw new Error('native_binary_missing: ' + id);
      if (process.platform !== 'win32') chmodSync(binary, 0o755);
      if ((await versionOf(binary, lease.run, options.signal)) !== versions[id])
        throw new Error('version_verification_failed: ' + id);
      writeFileSync(complete, JSON.stringify({ id, version: versions[id] }));
      manifest.tools[id] = { path: binary, version: versions[id] };
    }
    const result = await check(options, lease.run, manifest);
    if (!result.ok) throw new Error('installation_verification_failed');
    options.signal?.throwIfAborted();
    const bin = writeLaunchers({ root: options.root, nodePath: process.execPath, repo, directory });
    const temporary = join(options.root, `install.json.${process.pid}.tmp`);
    writeFileSync(temporary, JSON.stringify(manifest, null, 2) + '\n', { mode: 0o600 });
    renameSync(temporary, join(options.root, 'install.json'));
    const restartRequired =
      packagesChanged ||
      previous.node?.path !== manifest.node.path ||
      options.tools.some((id) => previous.tools?.[id]?.path !== manifest.tools[id].path);
    return {
      ...result,
      command: options.action,
      launchers: bin,
      authenticationConfigured: false,
      restartRequired
    };
  } finally {
    lease.release();
  }
}

const isMain = process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url);
if (isMain && process.argv.slice(2).some((arg) => ['--help', '-h'].includes(arg))) {
  console.log(
    'Usage: providers.mjs [install|update|check] [--root PATH] [--api-only|--only opencode|cline]'
  );
  console.log(
    'Updates apply the tested versions bundled with this checkout. Check never installs or authenticates.'
  );
  console.log('Without Node, start with providers.sh or providers.ps1.');
} else if (isMain) {
  const controller = new AbortController();
  process.once('SIGINT', () => controller.abort(new Error('installation_cancelled')));
  process.once('SIGTERM', () => controller.abort(new Error('installation_cancelled')));
  try {
    const options = parseArgs(process.argv.slice(2));
    options.signal = controller.signal;
    const result = options.action === 'check' ? await check(options) : await install(options);
    console.log(JSON.stringify(result, null, 2));
    if (!result.ok) process.exitCode = 1;
  } catch (error) {
    console.error(error.message);
    process.exitCode = controller.signal.aborted ? 130 : 1;
  }
}
