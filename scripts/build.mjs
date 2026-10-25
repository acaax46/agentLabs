import { cpSync, mkdirSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { join } from 'node:path';

const root = fileURLToPath(new URL('..', import.meta.url));
const target = process.argv[2];
if (!['api', 'desktop'].includes(target)) throw new Error('Expected api or desktop');
const output = join(root, 'build');
mkdirSync(output, { recursive: true });
if (target === 'desktop') {
  // Copy only the frontend build we own; no recursive deletion of user paths.
  cpSync(join(root, 'frontend/dist'), join(root, 'backend/cmd/desktop/webdist'), {
    recursive: true,
  });
}
const extension = process.platform === 'win32' ? '.exe' : '';
const args = ['build'];
if (target === 'desktop') args.push('-tags', 'desktop,production');
if (target === 'desktop' && process.platform === 'win32') args.push('-ldflags', '-H windowsgui');
args.push(
  '-trimpath',
  '-o',
  join(output, `agentlab-${target}${extension}`),
  `./cmd/${target === 'api' ? 'server' : 'desktop'}`,
);
const result = spawnSync('go', args, { cwd: join(root, 'backend'), stdio: 'inherit' });
if (result.error) throw result.error;
process.exit(result.status ?? 1);
