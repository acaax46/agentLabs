import { readFile, mkdir } from 'node:fs/promises';
import { createWriteStream } from 'node:fs';
import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const work = resolve(root, '.cache', `p0-live-${Date.now()}`);
await mkdir(work, { recursive: true });
const env = { ...process.env };
const allowed = new Set([
  'BASE_URL',
  'API_KEY',
  'AGENTLAB_MODEL_PROVIDER',
  'AGENTLAB_CONNECTION_ALLOWED_ORIGINS',
]);
for (const raw of (
  await readFile(resolve(root, process.env.AGENTLAB_P0_ENV_FILE || '.env.dev'), 'utf8')
).split(/\r?\n/)) {
  const line = raw.trim();
  const at = line.indexOf('=');
  const name = line.slice(0, at).trim();
  if (!line || line.startsWith('#') || at < 1 || !allowed.has(name)) continue;
  let value = line.slice(at + 1).trim();
  if (
    (value.startsWith('"') && value.endsWith('"')) ||
    (value.startsWith("'") && value.endsWith("'"))
  )
    value = value.slice(1, -1);
  env[name] = value;
}
if (!env.BASE_URL || !env.API_KEY)
  throw new Error('真实验收需要现有本地模型配置；不会启用模拟模型。');
Object.assign(env, {
  AGENTLAB_MONGO_DATABASE: `agentlab_p0_acceptance_${Date.now()}`,
  AGENTLAB_LISTEN_ADDR: '127.0.0.1:18879',
  AGENTLAB_DEV_ORIGIN: 'http://127.0.0.1:15175',
  AGENTLAB_CREDENTIAL_KEY: randomBytes(32).toString('base64'),
  AGENTLAB_BLOB_DIR: resolve(work, 'blobs'),
  AGENTLAB_LEARNING_EXECUTOR_READY: '0',
  AGENTLAB_SANDBOX_BACKEND: 'docker',
  AGENTLAB_SANDBOX_IMAGE: 'agentlab/sandbox-python:20261024',
});
const log = createWriteStream(resolve(work, 'server.log'));
const suffix = process.platform === 'win32' ? '.exe' : '';
const run = (command, args, childEnv) =>
  new Promise((done, fail) => {
    const child = spawn(command, args, {
      cwd: root,
      env: childEnv,
      shell: false,
      windowsHide: true,
    });
    child.stdout.pipe(log, { end: false });
    child.stderr.pipe(log, { end: false });
    child.on('error', fail);
    child.on('exit', (code) =>
      code === 0
        ? done()
        : fail(new Error('验收初始化失败，请检查本地 server.log；凭据不会作为命令参数输出。')),
    );
  });
for (const name of ['seed-demo', 'seed-platform', 'server']) {
  const binary = resolve(work, name + suffix);
  await run('go', ['-C', 'backend', 'build', '-o', binary, `./cmd/${name}`], process.env);
  if (name !== 'server') await run(binary, [], env);
}
const server = spawn(resolve(work, 'server' + suffix), [], {
  cwd: root,
  env,
  shell: false,
  windowsHide: true,
});
server.stdout.pipe(log, { end: false });
server.stderr.pipe(log, { end: false });
server.on('error', () => {
  console.error('验收 API 未能启动。');
  process.exit(1);
});
server.on('exit', (code) => process.exit(code ?? 1));
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => server.kill(signal));
let ready = false;
for (let attempt = 0; attempt < 30; attempt++) {
  try {
    ready = (await fetch('http://127.0.0.1:18879/health/live')).ok;
  } catch {
    /* wait for listener */
  }
  if (ready) break;
  await new Promise((done) => setTimeout(done, 1000));
}
if (!ready) throw new Error('验收 API 没有就绪。');
const login = await fetch('http://127.0.0.1:18879/api/v1/auth/login', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json', 'X-AgentLab-Request': 'auth' },
  body: JSON.stringify({ username: 'admin', password: 'AgentLab-Admin-2026!' }),
});
if (!login.ok) throw new Error('独立验收账号未能登录。');
const headers = {
  Cookie: login.headers.get('set-cookie').split(';')[0],
  'Content-Type': 'application/json',
  'X-AgentLab-Request': 'connections',
};
const connections = await (
  await fetch('http://127.0.0.1:18879/api/v1/connections', { headers })
).json();
for (const connection of connections) {
  const response = await fetch(
    `http://127.0.0.1:18879/api/v1/connections/${encodeURIComponent(connection.id)}/verify`,
    { method: 'POST', headers, body: JSON.stringify({ version: connection.version }) },
  );
  if (!response.ok || (await response.json()).status !== 'active')
    throw new Error('现有模型连接未通过真实授权验证，不能继续验收。');
}
console.log(
  '独立 P0 验收 API 已启动：http://127.0.0.1:18879。随后先构建前端，再执行 test:e2e:acceptance；测试会产生真实模型调用。',
);
