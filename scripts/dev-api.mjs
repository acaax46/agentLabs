import { readFile } from 'node:fs/promises';
import { spawn } from 'node:child_process';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const args = process.argv.slice(2);
const env = { ...process.env };
if (args.length) {
  if (args.length !== 2 || args[0] !== '--env-file') throw new Error('Use --env-file <local-file>');
  const content = await readFile(resolve(root, args[1]), 'utf8');
  for (const raw of content.split(/\r?\n/)) {
    const line = raw.replace(/^\uFEFF/, '').trim();
    if (!line || line.startsWith('#')) continue;
    const at = line.indexOf('=');
    if (at < 1) continue;
    const key = line.slice(0, at).trim();
    if (
      ![
        'BASE_URL',
        'API_KEY',
        'AGENTLAB_MODEL_PROVIDER',
        'AGENTLAB_GUEST_IMAGE',
        'AGENTLAB_CREDENTIAL_KEY',
        'AGENTLAB_CREDENTIAL_KEY_FILE',
        'AGENTLAB_CONNECTION_ALLOWED_ORIGINS',
        'AGENTLAB_ROLE',
        'AGENTLAB_PLATFORM_LISTEN_ADDR',
        'AGENTLAB_BLOB_BACKEND',
        'AGENTLAB_BLOB_DIR',
        'AGENTLAB_SANDBOX_BACKEND',
        'AGENTLAB_SANDBOX_IMAGE',
        'AGENTLAB_SANDBOX_GPUS',
        'AGENTLAB_SANDBOX_GPU_IMAGE',
        'AGENTLAB_SANDBOX_AGENT_IMAGE',
        'AGENTLAB_SANDBOX_RUNTIME',
        'AGENTLAB_SANDBOX_CPUS',
        'AGENTLAB_SANDBOX_MEMORY_MB',
        'AGENTLAB_SANDBOX_SLOTS',
        'AGENTLAB_WORKER_ID',
        'AGENTLAB_WORKER_CONCURRENCY',
        'AGENTLAB_WORKER_POLL_MS',
        'AGENTLAB_RUN_LEASE_SECONDS',
        'AGENTLAB_DRAIN_SECONDS',
        'AGENTLAB_RECOVER_SECONDS',
        'AGENTLAB_RUN_MAX_ATTEMPTS',
        'AGENTLAB_MODEL_TIMEOUT_SECONDS',
        'AGENTLAB_MODEL_SEND_RETRIES',
        'AGENTLAB_DEFAULT_CURRENCY',
        'AGENTLAB_PROJECT_MAX_CONCURRENT_RUNS',
        'AGENTLAB_ARTIFACT_RETENTION_DAYS',
        'AGENTLAB_DEFAULT_RATE_PER_MINUTE',
        'AGENTLAB_MAX_UPLOAD_MB',
        'AGENTLAB_MAX_RUN_INPUT_MB',
        'AGENTLAB_WORKSPACE_NAME',
        'AGENTLAB_STORAGE_SAMPLE_SECONDS',
      ].includes(key)
    )
      continue;
    let value = line.slice(at + 1).trim();
    if (
      (value.startsWith('"') && value.endsWith('"')) ||
      (value.startsWith("'") && value.endsWith("'"))
    )
      value = value.slice(1, -1);
    env[key] = value;
  }
}
// Secrets are passed to the host API child only; no argument or console output includes them.
const child = spawn('go', ['-C', 'backend', 'run', './cmd/server'], {
  cwd: root,
  env,
  stdio: 'inherit',
  shell: false,
});
child.on('exit', (code) => process.exit(code ?? 1));
child.on('error', () => {
  console.error('Cannot launch Go API');
  process.exit(1);
});
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => child.kill(signal));
