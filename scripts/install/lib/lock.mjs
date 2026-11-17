import {
  mkdirSync,
  readFileSync,
  writeFileSync,
  readdirSync,
  unlinkSync,
  rmdirSync
} from 'node:fs';
import { join } from 'node:path';

function isProcessAlive(pid) {
  try {
    process.kill(pid, 0);
    return true;
  } catch (error) {
    // Permission errors are not proof that the owner has exited.
    return error.code !== 'ESRCH';
  }
}

export function acquireInstallLock(root, runner) {
  const lock = join(root, '.install-lock');
  try {
    mkdirSync(lock);
  } catch {
    let owner;
    try {
      owner = JSON.parse(readFileSync(join(lock, 'owner.json'), 'utf8'));
    } catch {
      throw new Error('installation_in_progress_or_incomplete_lock');
    }
    if (!Number.isInteger(owner.pid) || owner.pid <= 0) throw new Error('installation_in_progress');
    const recovery = join(root, '.install-lock-recovery');
    try {
      mkdirSync(recovery);
    } catch {
      throw new Error('installation_lock_recovery_in_progress');
    }
    try {
      const current = JSON.parse(readFileSync(join(lock, 'owner.json'), 'utf8'));
      if (current.pid !== owner.pid) throw new Error('installation_in_progress');
      if (isProcessAlive(owner.pid)) throw new Error('installation_in_progress');
      if (current.phase === 'running') {
        if (!Number.isInteger(current.jobPID) || current.jobPID <= 0)
          throw new Error('installation_lock_state_unknown');
        if (isProcessAlive(current.jobPID)) throw new Error('installation_child_still_running');
        if (process.platform === 'win32')
          throw new Error('installation_lock_needs_manual_process_check');
        if (isProcessAlive(-current.jobPID))
          throw new Error('installation_child_group_still_running');
      } else if (current.phase !== 'idle') throw new Error('installation_lock_state_unknown');
      if (readdirSync(lock).some((name) => name !== 'owner.json'))
        throw new Error('installation_lock_contains_unknown_files');
      unlinkSync(join(lock, 'owner.json'));
      rmdirSync(lock);
      mkdirSync(lock);
    } finally {
      rmdirSync(recovery);
    }
  }
  writeFileSync(
    join(lock, 'owner.json'),
    JSON.stringify({ pid: process.pid, phase: 'idle', startedAt: new Date().toISOString() })
  );
  const run = async (binary, args, settings = {}) => {
    const writeOwner = (fields) =>
      writeFileSync(join(lock, 'owner.json'), JSON.stringify({ pid: process.pid, ...fields }));
    writeOwner({ phase: 'launching' });
    try {
      return await runner(binary, args, {
        ...settings,
        onSpawn: (jobPID) => writeOwner({ phase: 'running', jobPID })
      });
    } finally {
      writeOwner({ phase: 'idle' });
    }
  };
  return {
    run,
    release() {
      unlinkSync(join(lock, 'owner.json'));
      rmdirSync(lock);
    }
  };
}
