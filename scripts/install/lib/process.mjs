import { spawn } from 'node:child_process';
import { join } from 'node:path';

export function runProcess(
  binary,
  args,
  { capture = false, timeout = 30000, signal, onSpawn } = {}
) {
  return new Promise((resolvePromise, reject) => {
    if (signal?.aborted) return reject(signal.reason);
    const child = spawn(binary, args, {
      windowsHide: true,
      detached: process.platform !== 'win32',
      stdio: ['ignore', capture ? 'pipe' : 'inherit', 'pipe']
    });
    let stdout = '',
      stderr = '';
    let failure, force;
    const kill = (signal) => {
      if (!child.pid) return;
      if (process.platform === 'win32') {
        const killer = spawn(
          join(process.env.SystemRoot || 'C:\\Windows', 'System32', 'taskkill.exe'),
          ['/PID', String(child.pid), '/T', '/F'],
          { stdio: 'ignore', windowsHide: true }
        );
        killer.on('error', () => child.kill());
      } else {
        try {
          process.kill(-child.pid, signal);
        } catch (error) {
          if (error.code !== 'ESRCH') child.kill(signal);
        }
      }
    };
    const stop = (error) => {
      if (failure) return;
      failure = error;
      kill('SIGTERM');
      force = setTimeout(() => kill('SIGKILL'), 1500);
    };
    const abort = () => stop(signal.reason);
    const cleanup = () => {
      clearTimeout(timer);
      clearTimeout(force);
      signal?.removeEventListener('abort', abort);
    };
    child.stdout?.on('data', (data) => {
      stdout = (stdout + data).slice(-65536);
    });
    child.stderr.on('data', (data) => {
      stderr = (stderr + data).slice(-8192);
    });
    const timer = setTimeout(() => stop(new Error('installer_process_timeout')), timeout);
    signal?.addEventListener('abort', abort, { once: true });
    child.once('error', (error) => {
      cleanup();
      reject(error);
    });
    child.once('close', (code) => {
      if (failure && process.platform !== 'win32') kill('SIGKILL');
      cleanup();
      if (failure) reject(failure);
      else if (code !== 0)
        reject(new Error('installer_command_failed: ' + (stderr.trim().slice(-1000) || code)));
      else resolvePromise(stdout.trim());
    });
    try {
      if (child.pid) onSpawn?.(child.pid);
    } catch (error) {
      stop(error);
    }
  });
}

export async function versionOf(path, run, signal) {
  try {
    return (
      (await run(path, ['--version'], { capture: true, signal })).match(
        /(?:^|\s)v?(\d+\.\d+\.\d+)(?:\s|$)/
      )?.[1] || null
    );
  } catch {
    if (signal?.aborted) throw signal.reason;
    return null;
  }
}
