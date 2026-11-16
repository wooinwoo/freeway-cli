import { OpenCodeBridge } from './opencode.mjs';
import { ClineBridge } from './cline.mjs';
import { join } from 'node:path';

export class ProviderBridges {
  constructor(dir) {
    this.providers = { opencode: new OpenCodeBridge(join(dir, 'opencode-workspace')),
      cline: new ClineBridge(join(dir, 'cline-workspace')) };
  }
  forProvider(id) { return this.providers[id]; }
  get binary() { return this.providers.opencode.binary; }
  state() { return this.providers.opencode.state(); }
  states() { return Object.fromEntries(Object.entries(this.providers).map(([id, bridge]) => [id, bridge.state()])); }
  start(id = 'opencode') { return this.providers[id].start(); }
  stop() { for (const bridge of Object.values(this.providers)) bridge.stop(); }
}
