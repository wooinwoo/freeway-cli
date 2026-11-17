import test from 'node:test';
import assert from 'node:assert/strict';
import {
  mkdtempSync,
  mkdirSync,
  writeFileSync,
  readFileSync,
  existsSync,
  rmSync,
  copyFileSync,
  linkSync
} from 'node:fs';
import { join, sep } from 'node:path';
import { tmpdir } from 'node:os';
import {
  parseArgs,
  install,
  check,
  versions,
  binaryCandidates,
  runProcess
} from '../../scripts/install/providers.mjs';
import { setTimeout as delay } from 'node:timers/promises';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { managedExecutable } from '../../POC/src/provider-runtime.mjs';
import { findExecutable } from '../../POC/src/executables.mjs';
import { run as manage } from '../../backend/internal/providers/manage.mjs';
import { writeLaunchers } from '../../scripts/install/lib/launchers.mjs';

function setup(t) {
  const root = mkdtempSync(join(tmpdir(), 'freeway-install-test-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const calls = [];
  const versionsByPath = new Map([[process.execPath, 'v' + process.versions.node]]);
  const run = async (binary, args) => {
    calls.push({ binary, args });
    if (args[0] === '/fixture/npm-cli.js') {
      const id = args.at(-1).startsWith('opencode-ai@') ? 'opencode' : 'cline';
      const prefix = args[args.indexOf('--prefix') + 1];
      const target = binaryCandidates(id, prefix)[0];
      mkdirSync(join(target, '..'), { recursive: true });
      writeFileSync(target, 'fixture executable', { mode: 0o755 });
      versionsByPath.set(target, versions[id]);
      return '';
    }
    if (!versionsByPath.has(binary)) throw new Error('unknown fixture binary');
    return versionsByPath.get(binary);
  };
  return { root, calls, run, versionsByPath, deps: { run, npm: () => '/fixture/npm-cli.js' } };
}

test('help explains installation and updates without creating an install directory', async (t) => {
  const { root } = setup(t);
  const target = join(root, 'not-created');
  const script = fileURLToPath(new URL('../../scripts/install/providers.mjs', import.meta.url));
  const text = await runProcess(process.execPath, [script, '--help', '--root', target], {
    capture: true
  });
  assert(text.includes('install|update|check'));
  assert.equal(existsSync(target), false);
  if (process.platform !== 'win32') {
    const shell = fileURLToPath(new URL('../../scripts/install/providers.sh', import.meta.url));
    const shellText = await runProcess('/bin/bash', [shell, '--help', '--root', target], {
      capture: true
    });
    assert(shellText.includes('tested versions'));
    assert.equal(existsSync(target), false);
  }
});

test('provider metadata distinguishes connection protocol from the OpenAI company', async () => {
  const providers = (await manage(['list'])).providers;
  assert.equal(
    providers.filter(
      (p) => p.transport === 'http' && p.protocol === 'openai_compatible' && !p.requiresCLI
    ).length,
    9
  );
  assert.equal(providers.find((p) => p.id === 'opencode').protocol, 'opencode_session');
  assert.equal(providers.find((p) => p.id === 'cline').protocol, 'acp');
  assert.equal(providers.filter((p) => p.requiresCLI).length, 2);
});

test('check performs no install and reports absent components', async (t) => {
  const { root, calls, run } = setup(t);
  const result = await check({ root, tools: ['opencode', 'cline'] }, run);
  assert.equal(result.ok, false);
  assert(result.components.every((c) => c.status === 'not_installed'));
  assert.equal(calls.length, 0);
  assert.equal(existsSync(join(root, 'install.json')), false);
});

test('installation pins both CLIs, disables lifecycle hooks, and is idempotent', async (t) => {
  const { root, calls, deps } = setup(t);
  const options = { root, tools: ['opencode', 'cline'], action: 'install' };
  const installed = await install(options, deps);
  assert.equal(installed.ok, true);
  assert.equal(installed.restartRequired, true);
  const commands = calls.filter((c) => c.args[0] === '/fixture/npm-cli.js');
  assert.equal(commands.length, 2);
  assert(
    commands.every(
      (c) =>
        c.args.includes('--ignore-scripts') &&
        c.args.includes('--global=false') &&
        c.args.includes('--include=optional')
    )
  );
  assert.deepEqual(
    commands.map((c) => c.args.at(-1)),
    [`opencode-ai@${versions.opencode}`, `cline@${versions.cline}`]
  );
  assert(commands.every((c) => c.args[c.args.indexOf('--prefix') + 1].startsWith(root + sep)));
  const manifest = JSON.parse(readFileSync(join(root, 'install.json'), 'utf8'));
  assert.equal(manifest.schemaVersion, 1);
  assert.equal(manifest.tools.opencode.version, versions.opencode);
  assert.equal(existsSync(join(root, '.install-lock')), false);
  const repeated = await install(options, deps);
  assert.equal(repeated.restartRequired, false);
  assert.equal(calls.filter((c) => c.args[0] === '/fixture/npm-cli.js').length, 2);
});

test('API-only installation installs no vendor packages', async (t) => {
  const { root, calls, deps } = setup(t);
  const result = await install({ root, tools: [], action: 'install' }, deps);
  assert.equal(result.ok, true);
  assert.equal(calls.filter((c) => c.args[0] === '/fixture/npm-cli.js').length, 0);
  assert.deepEqual(JSON.parse(readFileSync(join(root, 'install.json'), 'utf8')).tools, {});
  assert.deepEqual(parseArgs(['update', '--root', root]).tools, []);
  assert.deepEqual(parseArgs(['check', '--root', root]).tools, []);
});

test('an interrupted package install with a binary but no completion marker is retried', async (t) => {
  const { root, calls, versionsByPath, deps } = setup(t);
  const prefix = join(root, 'cli', `opencode-${versions.opencode}`);
  const binary = binaryCandidates('opencode', prefix)[0];
  mkdirSync(join(binary, '..'), { recursive: true });
  writeFileSync(binary, 'partial package', { mode: 0o755 });
  versionsByPath.set(binary, versions.opencode);
  await install({ root, tools: ['opencode'], action: 'install' }, deps);
  assert.equal(calls.filter((c) => c.args[0] === '/fixture/npm-cli.js').length, 1);
  assert(existsSync(join(prefix, '.freeway-install-complete.json')));
});

test('update switches the manifest after verification and retains the previous version files', async (t) => {
  const { root, deps } = setup(t);
  const old = join(root, 'old-opencode');
  writeFileSync(old, 'old version', { mode: 0o755 });
  writeFileSync(
    join(root, 'install.json'),
    JSON.stringify({ schemaVersion: 1, tools: { opencode: { path: old, version: '1.0.0' } } })
  );
  const result = await install({ root, tools: ['opencode'], action: 'update' }, deps);
  assert.equal(result.ok, true);
  const manifest = JSON.parse(readFileSync(join(root, 'install.json'), 'utf8'));
  assert.notEqual(manifest.tools.opencode.path, old);
  assert.equal(readFileSync(old, 'utf8'), 'old version');
});

test('failed update leaves the active manifest intact and releases the lock', async (t) => {
  const { root } = setup(t);
  const original = JSON.stringify({
    schemaVersion: 1,
    tools: { opencode: { path: '/old', version: '1.0.0' } }
  });
  writeFileSync(join(root, 'install.json'), original);
  await assert.rejects(
    install(
      { root, tools: ['opencode'], action: 'update' },
      {
        npm: () => '/fixture/npm-cli.js',
        run: async () => {
          throw new Error('offline');
        }
      }
    ),
    /offline/
  );
  assert.equal(readFileSync(join(root, 'install.json'), 'utf8'), original);
  assert.equal(existsSync(join(root, '.install-lock')), false);
});

test('final verification failure does not publish the candidate manifest', async (t) => {
  const { root } = setup(t);
  const original = JSON.stringify({
    schemaVersion: 1,
    tools: {},
    node: { path: '/old-node', version: '22.0.0' }
  });
  writeFileSync(join(root, 'install.json'), original);
  await assert.rejects(
    install(
      { root, tools: [], action: 'update' },
      {
        run: async () => {
          throw new Error('version failed');
        }
      }
    ),
    /installation_verification_failed/
  );
  assert.equal(readFileSync(join(root, 'install.json'), 'utf8'), original);
  assert.equal(existsSync(join(root, '.install-lock')), false);
});

test('dead-owner locks recover without touching a live installer lock', async (t) => {
  const { root, deps } = setup(t);
  const lock = join(root, '.install-lock');
  mkdirSync(lock);
  writeFileSync(join(lock, 'owner.json'), JSON.stringify({ pid: 99999999, phase: 'idle' }));
  await install({ root, tools: [], action: 'install' }, deps);
  assert.equal(existsSync(lock), false);
  mkdirSync(lock);
  writeFileSync(join(lock, 'owner.json'), JSON.stringify({ pid: process.pid }));
  await assert.rejects(
    install({ root, tools: [], action: 'install' }, deps),
    /installation_in_progress/
  );
  assert.equal(JSON.parse(readFileSync(join(lock, 'owner.json'), 'utf8')).pid, process.pid);
});

test('a dead installer with a live owned child is not reclaimed', async (t) => {
  const { root, deps } = setup(t);
  const lock = join(root, '.install-lock');
  mkdirSync(lock);
  const owner = JSON.stringify({ pid: 99999999, phase: 'running', jobPID: process.pid });
  writeFileSync(join(lock, 'owner.json'), owner);
  await assert.rejects(install({ root, tools: [], action: 'update' }, deps), /child_still_running/);
  assert.equal(readFileSync(join(lock, 'owner.json'), 'utf8'), owner);
});

test('cancellation waits for the installer child to exit and then releases its lock', async (t) => {
  const { root } = setup(t);
  const controller = new AbortController();
  const pidPath = join(root, 'child.pid');
  const pending = install(
    { root, tools: ['opencode'], action: 'install', signal: controller.signal },
    {
      npm: () => '/fixture/npm-cli.js',
      run: (_binary, _args, options) =>
        runProcess(
          process.execPath,
          [
            '-e',
            `require('node:fs').writeFileSync(process.argv[1],String(process.pid));setInterval(()=>{},1000)`,
            pidPath
          ],
          options
        )
    }
  );
  const expected = assert.rejects(pending, /cancelled/);
  for (let i = 0; !existsSync(pidPath) && i < 200; i++) await delay(10);
  assert(existsSync(pidPath));
  const pid = Number(readFileSync(pidPath, 'utf8'));
  controller.abort(new Error('cancelled'));
  await expected;
  assert.equal(existsSync(join(root, '.install-lock')), false);
  assert.throws(() => process.kill(pid, 0), { code: 'ESRCH' });
});

test('SIGINT of the actual installer CLI cleans its child and lock', async (t) => {
  if (process.platform === 'win32')
    return t.skip('Console SIGINT requires a real Windows terminal');
  const { root } = setup(t);
  const childPID = join(root, 'version-child.pid');
  const preload = join(root, 'preload.mjs');
  const code = `require('node:fs').writeFileSync(process.argv[1],String(process.pid));setInterval(()=>{},1000)`;
  writeFileSync(
    preload,
    `import cp from 'node:child_process';import{syncBuiltinESMExports}from'node:module';const real=cp.spawn;cp.spawn=(binary,args,options)=>real(process.execPath,['-e',${JSON.stringify(code)},${JSON.stringify(childPID)}],options);syncBuiltinESMExports();`
  );
  const script = fileURLToPath(new URL('../../scripts/install/providers.mjs', import.meta.url));
  const child = spawn(
    process.execPath,
    ['--import', preload, script, 'install', '--api-only', '--root', root],
    { stdio: 'ignore' }
  );
  const closed = new Promise((resolve, reject) => {
    child.once('close', resolve);
    child.once('error', reject);
  });
  t.after(() => {
    child.kill('SIGKILL');
    if (existsSync(childPID)) {
      try {
        process.kill(Number(readFileSync(childPID, 'utf8')), 'SIGKILL');
      } catch {}
    }
  });
  for (let i = 0; !existsSync(childPID) && i < 300; i++) await delay(10);
  assert(existsSync(childPID));
  child.kill('SIGINT');
  const exit = await Promise.race([
    closed,
    delay(5000, undefined, { ref: false }).then(() => {
      throw new Error('installer did not cancel');
    })
  ]);
  assert.equal(exit, 130);
  assert.equal(existsSync(join(root, '.install-lock')), false);
  assert.equal(existsSync(join(root, 'install.json')), false);
  assert.throws(() => process.kill(Number(readFileSync(childPID, 'utf8')), 0), { code: 'ESRCH' });
});

test('managed native executables are discovered and explicit overrides take precedence', (t) => {
  const { root } = setup(t);
  const previous = process.env.FREEWAY_PROVIDER_HOME;
  process.env.FREEWAY_PROVIDER_HOME = root;
  t.after(() => {
    if (previous === undefined) delete process.env.FREEWAY_PROVIDER_HOME;
    else process.env.FREEWAY_PROVIDER_HOME = previous;
  });
  const binary = join(root, 'native-opencode');
  const override = join(root, 'explicit-opencode');
  for (const path of [binary, override]) writeFileSync(path, 'fixture', { mode: 0o755 });
  writeFileSync(
    join(root, 'install.json'),
    JSON.stringify({
      schemaVersion: 1,
      node: { path: process.execPath },
      tools: { opencode: { path: binary } }
    })
  );
  assert.equal(managedExecutable('node'), process.execPath);
  assert.equal(findExecutable('opencode'), binary);
  assert.equal(findExecutable('opencode', override), override);
});

test('platform resolution uses native Windows binaries and supported pinned targets', () => {
  assert(
    binaryCandidates('opencode', '/prefix', 'win32', 'x64')[0].endsWith(
      join('opencode-windows-x64-baseline', 'bin', 'opencode.exe')
    )
  );
  assert(
    binaryCandidates('cline', '/prefix', 'win32', 'arm64')[0].endsWith(
      join('@cline', 'cli-windows-arm64', 'bin', 'cline.exe')
    )
  );
  assert(
    binaryCandidates('opencode', '/prefix', 'linux', 'x64', true)[0].includes('baseline-musl')
  );
  assert.throws(() => parseArgs(['install', '--only', 'unknown']), /invalid/);
  assert.throws(() => parseArgs(['install', '--root', '/']), /dedicated/);
  assert.throws(() => parseArgs(['install', '--only', 'cline', '--api-only']), /invalid/);
});

test(
  'PowerShell redirected progress preserves native success and failure exit codes',
  {
    skip: process.platform !== 'win32'
  },
  async (t) => {
    const { root } = setup(t);
    const source = join(root, 'source');
    const directory = join(source, 'scripts', 'install');
    const providerDir = join(source, 'backend', 'internal', 'providers');
    const installRoot = join(root, 'installation');
    const runtime = join(installRoot, 'runtime', `node-v${versions.node}-win-${process.arch}`);
    for (const path of [directory, providerDir, runtime]) mkdirSync(path, { recursive: true });
    copyFileSync(
      fileURLToPath(new URL('../../scripts/install/providers.ps1', import.meta.url)),
      join(directory, 'providers.ps1')
    );
    writeFileSync(join(directory, 'versions.json'), JSON.stringify(versions));
    try {
      linkSync(process.execPath, join(runtime, 'node.exe'));
    } catch {
      copyFileSync(process.execPath, join(runtime, 'node.exe'));
    }
    const fixture = `console.error('ordinary progress');console.log(JSON.stringify({args:process.argv.slice(2)}));process.exitCode=Number(process.env.FREEWAY_TEST_NATIVE_EXIT);`;
    writeFileSync(join(directory, 'providers.mjs'), fixture);
    writeFileSync(join(providerDir, 'manage.mjs'), fixture);
    const bin = writeLaunchers({
      root: installRoot,
      nodePath: process.execPath,
      repo: source,
      directory
    });
    const quote = (value) => "'" + value.replaceAll("'", "''") + "'";
    const verifier = join(root, 'verify.ps1');
    writeFileSync(
      verifier,
      `
$ErrorActionPreference = 'Stop'
$commands = @(
  @{ Path=${quote(join(directory, 'providers.ps1'))}; Arguments=@{Action='check';Root=${quote(installRoot)}} },
  @{ Path=${quote(join(bin, 'freeway-providers.ps1'))}; Arguments=@{ForwardArgs=@('models','kilo')} }
)
foreach ($code in @(0,7)) {
  $env:FREEWAY_TEST_NATIVE_EXIT = [string]$code
  foreach ($command in $commands) {
    $parameters = $command.Arguments
    & $command.Path @parameters *> ${quote(join(root, 'native.log'))}
    if ($LASTEXITCODE -ne $code) { throw 'Native exit code was lost' }
    $text = Get-Content -LiteralPath ${quote(join(root, 'native.log'))} -Raw
    if (-not $text.Contains('ordinary progress')) { throw 'Progress log was lost' }
    if (-not $text.Contains('"args"')) { throw 'Native command did not complete' }
  }
}
Write-Output 'Redirected native stderr regression OK'
`
    );
    const result = await runProcess(
      join(process.env.SystemRoot, 'System32', 'WindowsPowerShell', 'v1.0', 'powershell.exe'),
      ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', verifier],
      { capture: true }
    );
    assert(result.includes('regression OK'));
  }
);
