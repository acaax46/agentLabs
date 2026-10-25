import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const manifest = JSON.parse(await readFile(resolve(root, 'runtime/assets.lock.json'), 'utf8'));
const asset = manifest.assets.find((a) => a.name === 'alpine-vm-kernel');
if (!asset) throw new Error('Pinned guest kernel is missing from assets.lock.json');
const kernel = resolve(root, asset.path);
if (!kernel.startsWith(root)) throw new Error('Invalid kernel path');
const sha = (b) => createHash('sha256').update(b).digest('hex');
let data;
try {
  data = await readFile(kernel);
} catch {}
if (!data || sha(data) !== asset.sha256) {
  const response = await fetch(asset.url, { signal: AbortSignal.timeout(120000) });
  if (!response.ok) throw new Error(`Kernel download failed: ${response.status}`);
  data = Buffer.from(await response.arrayBuffer());
  if (sha(data) !== asset.sha256)
    throw new Error('Kernel digest changed; update the reviewed asset lock before building');
  await mkdir(dirname(kernel), { recursive: true });
  await writeFile(kernel, data);
}
function run(command, args, env = process.env) {
  const result = spawnSync(command, args, { cwd: root, stdio: 'inherit', env, shell: false });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} exited ${result.status}`);
}
await mkdir(resolve(root, 'runtime/guest/vendor'), { recursive: true });
run(
  'go',
  [
    '-C',
    'backend',
    'build',
    '-trimpath',
    '-ldflags',
    '-s -w',
    '-o',
    '../runtime/guest/vendor/agentlab-guest',
    './cmd/guest',
  ],
  { ...process.env, GOOS: 'linux', GOARCH: 'amd64', CGO_ENABLED: '0' },
);
if (!process.argv.includes('--skip-base'))
  run('docker', ['build', '-t', 'agentlab/vm-smoke:alpine-3.22', 'runtime/vm']);
run('docker', ['build', '-t', 'agentlab/student-guest:20261024', 'runtime/guest']);
console.log('Built real Linux/OpenSandbox guest: agentlab/student-guest:20261024');
