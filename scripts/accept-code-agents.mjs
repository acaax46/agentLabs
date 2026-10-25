// Real acceptance of the code-agent engines and the GPU agent.
//
// It boots the real server on a throwaway database with the Docker sandbox, the code-agent image and
// the local NVIDIA card, configures the .env.dev channel (BASE_URL / API_KEY) as the platform model
// connection, and drives the PUBLIC API-key listener only:
//
//   - the code agent once per engine (pi, OpenCode, Codex CLI, Claude Code) on one platform model:
//     a CSV summary judged by the file's content, plus the sandbox's environment written to a file,
//     which must hold no credential or provider address
//   - the GPU agent fitting a regression with PyTorch on the card, judged by the file it writes
//   - every run settles, its usage is provider-reported, its sandbox seconds are billed at the
//     frozen allocation's rate, and no secret appears in the server log, the events or any output
//
// Model output is not deterministic: a scenario gets two attempts and every attempt is recorded.
// It SPENDS REAL CREDIT (cents) and prints the spend measured at the provider; it never prints the key.
// Needs MongoDB (pnpm db:up), Docker with the NVIDIA runtime and the images (pnpm build:sandbox,
// pnpm build:sandbox:cuda, pnpm build:sandbox:agents).
//
//   node scripts/accept-code-agents.mjs            run and print a report
//   node scripts/accept-code-agents.mjs --record   also write assets/code-agent-acceptance.json
//   AGENTLAB_ACCEPT_ENGINES=pi,codex node scripts/accept-code-agents.mjs   only some engines
import { spawn, spawnSync } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import { closeSync, mkdirSync, openSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const work = join(root, '.cache', 'code-agents');
const bin = join(work, 'bin');
const exe = (name) => join(bin, process.platform === 'win32' ? `${name}.exe` : name);
const ports = { local: 18807, platform: 18808 };
const database = `agentlab_smoke_agents_${process.pid}`;
const images = {
  cpu: process.env.AGENTLAB_SANDBOX_IMAGE || 'agentlab/sandbox-python:20261024',
  gpu: process.env.AGENTLAB_SANDBOX_GPU_IMAGE || 'agentlab/sandbox-python-cuda:20261025',
  agents: process.env.AGENTLAB_SANDBOX_AGENT_IMAGE || 'agentlab/sandbox-agents:20261025',
};
const mongo =
  process.env.AGENTLAB_MONGO_URI ||
  'mongodb://127.0.0.1:27018/?replicaSet=rs0&directConnection=true';
const writeRecord = process.argv.includes('--record');
const model = process.env.AGENTLAB_ACCEPT_MODEL || 'deepseek/deepseek-v4.1-flash-1017';
const engines = (process.env.AGENTLAB_ACCEPT_ENGINES || 'pi,opencode,codex,claude-code').split(',');
const attemptsPerScenario = 2;

const failures = [];
const evidence = [];
function report(lines, condition, message) {
  lines.push(`  ${condition ? 'ok  ' : 'FAIL'} ${message}`);
  if (!condition) (lines.failed ??= []).push(message);
  return condition;
}
function flush(lines) {
  console.log(lines.join('\n'));
  failures.push(...(lines.failed ?? []));
}

function readChannel() {
  const values = {};
  for (const raw of readFileSync(join(root, '.env.dev'), 'utf8').split(/\r?\n/)) {
    const line = raw.trim();
    const at = line.indexOf('=');
    if (!line || line.startsWith('#') || at < 1) continue;
    let value = line.slice(at + 1).trim();
    if (/^(['"]).*\1$/.test(value)) value = value.slice(1, -1);
    values[line.slice(0, at).trim()] = value;
  }
  if (!values.BASE_URL || !values.API_KEY)
    throw new Error('real acceptance needs BASE_URL and API_KEY in .env.dev');
  return { baseUrl: values.BASE_URL, apiKey: values.API_KEY };
}

async function call(base, method, path, { body, headers = {}, bearer, cookie, raw } = {}) {
  const requestHeaders = { ...headers };
  if (body !== undefined) requestHeaders['Content-Type'] = 'application/json';
  if (bearer) requestHeaders.Authorization = `Bearer ${bearer}`;
  if (cookie) requestHeaders.Cookie = cookie;
  if (!['GET', 'PUT', 'HEAD', 'DELETE'].includes(method) && !requestHeaders['Idempotency-Key']) {
    requestHeaders['Idempotency-Key'] = `agents-${Date.now()}-${randomBytes(4).toString('hex')}`;
  }
  const response = await fetch(base + path, {
    method,
    headers: requestHeaders,
    body: raw ?? (body === undefined ? undefined : JSON.stringify(body)),
  });
  const bytes = Buffer.from(await response.arrayBuffer());
  return {
    status: response.status,
    headers: response.headers,
    bytes,
    json: () => JSON.parse(bytes.toString('utf8')),
  };
}

async function ok(base, method, path, options) {
  const response = await call(base, method, path, options);
  if (response.status >= 300)
    throw new Error(
      `${method} ${path} -> ${response.status} ${response.bytes.toString('utf8').slice(0, 300)}`,
    );
  return response.json();
}

async function waitFor(description, condition, { seconds = 120, every = 500 } = {}) {
  const deadline = Date.now() + seconds * 1000;
  while (Date.now() < deadline) {
    const value = await condition();
    if (value) return value;
    await new Promise((done) => setTimeout(done, every));
  }
  throw new Error(`timed out waiting for ${description}`);
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { stdio: 'inherit', cwd: root, ...options });
  if (result.error) throw result.error;
  if (result.status !== 0)
    throw new Error(`${command} ${args.join(' ')} exited with ${result.status}`);
}

const terminal = new Set(['succeeded', 'failed', 'cancelled', 'unknown']);
const near = (a, b, tolerance) => Math.abs(Number(a) - b) < tolerance;

function totalsFromCsv(text) {
  const [header, ...rows] = text
    .trim()
    .split(/\r?\n/)
    .map((line) => line.split(',').map((cell) => cell.trim().replace(/^"|"$/g, '')));
  const region = header.findIndex((name) => name.toLowerCase().includes('region'));
  const amount = header.findIndex((name) => /amount|total/i.test(name));
  const totals = {};
  for (const cells of rows)
    if (region >= 0 && amount >= 0) totals[cells[region]] = Number(cells[amount]);
  return totals;
}

async function download(ctx, artifact) {
  const response = await call(
    ctx.platform,
    'GET',
    `/v1/artifacts/${artifact.id}/content`,
    ctx.withKey,
  );
  if (response.status !== 200) throw new Error(`download ${artifact.name} -> ${response.status}`);
  return response.bytes;
}

async function attempt(ctx, spec, number) {
  const started = Date.now();
  const accepted = await ok(ctx.platform, 'POST', '/v1/runs', {
    ...ctx.withKey,
    body: spec.body,
    headers: { 'Idempotency-Key': `agents-${number}-${spec.id}-${randomBytes(3).toString('hex')}` },
  });
  const final = await waitFor(
    `run ${accepted.id}`,
    async () => {
      const current = await ok(ctx.platform, 'GET', `/v1/runs/${accepted.id}`, ctx.withKey);
      return terminal.has(current.status) ? current : null;
    },
    { seconds: 900, every: 2000 },
  );
  const outputs = (
    await ok(ctx.platform, 'GET', `/v1/artifacts?kind=output&runId=${accepted.id}`, ctx.withKey)
  ).items;
  const usage = (await ok(ctx.platform, 'GET', `/v1/usage?runId=${accepted.id}`, ctx.withKey))
    .items;
  const ledger = (
    await ok(ctx.platform, 'GET', `/v1/billing/ledger?runId=${accepted.id}`, ctx.withKey)
  ).items;
  const events = (
    await ok(ctx.platform, 'GET', `/v1/runs/${accepted.id}/events?limit=500`, ctx.withKey)
  ).items;
  return {
    accepted,
    final,
    outputs,
    usage,
    ledger,
    events,
    seconds: (Date.now() - started) / 1000,
  };
}

// What every run must satisfy: settled, provider-reported model usage, sandbox time at the frozen rate.
function judgeCommon(lines, view, ctx) {
  const { final, usage, ledger } = view;
  if (!report(lines, final.status === 'succeeded', `the run succeeded (${final.status})`)) {
    lines.push(`       ${final.error?.code ?? '-'}: ${final.error?.message ?? ''}`.slice(0, 300));
    return false;
  }
  report(
    lines,
    final.billingStatus === 'settled' && final.costMicros !== null,
    `settled at ${final.costMicros} micro-${final.currency}`,
  );
  const tokens = usage.find((line) => line.category === 'model');
  report(
    lines,
    tokens?.basis === 'provider-reported' &&
      tokens.status === 'settled' &&
      Number(tokens.amountMicros) > 0,
    `model usage is provider-reported and priced (${tokens?.quantity} tokens, ${tokens?.amountMicros} micros, ${final.usage?.modelSteps} requests)`,
  );
  const sandbox = usage.find((line) => line.meter === 'sandbox_seconds');
  const rate = ctx.rates.get(final.resources?.id);
  report(
    lines,
    sandbox && rate && BigInt(sandbox.amountMicros) === BigInt(sandbox.quantity) * BigInt(rate),
    `sandbox time is billed at the frozen ${final.resources?.id} rate (${sandbox?.quantity} s x ${rate})`,
  );
  const types = ledger
    .map((entry) => entry.type)
    .sort()
    .join();
  report(
    lines,
    types === 'release,reserve,settle',
    `the ledger reserved, settled and released (${types})`,
  );
  return true;
}

async function scenario(ctx, spec) {
  const lines = [`\n${spec.title}`];
  const entry = { scenario: spec.id, harness: spec.harness ?? 'builtin', model, attempts: [] };
  let passed = false;
  for (let number = 1; number <= attemptsPerScenario && !passed; number += 1) {
    const attemptLines = [];
    try {
      const view = await attempt(ctx, spec, number);
      const detail = {
        attempt: number,
        runId: view.accepted.id,
        status: view.final.status,
        harness: view.final.harness,
        resources: view.final.resources?.id ?? null,
        costMicros: view.final.costMicros,
        currency: view.final.currency,
        durationMs: view.final.durationMs,
        modelSteps: view.final.usage?.modelSteps,
        inputTokens: view.final.usage?.inputTokens,
        outputTokens: view.final.usage?.outputTokens,
        sandboxSeconds: Number(
          view.usage.find((line) => line.meter === 'sandbox_seconds')?.quantity ?? 0,
        ),
        reportedSteps: view.events.filter((event) => event.source === 'harness').length,
        outputs: view.outputs.map((output) => ({
          name: output.name,
          sizeBytes: output.sizeBytes,
          sha256: output.sha256,
        })),
        error: view.final.error
          ? { code: view.final.error.code, message: view.final.error.message.slice(0, 200) }
          : null,
      };
      entry.attempts.push(detail);
      lines.push(
        `  attempt ${number}: ${view.final.status} in ${view.seconds.toFixed(0)}s, run ${view.accepted.id}`,
      );
      if (judgeCommon(attemptLines, view, ctx)) await spec.judge(ctx, view, attemptLines);
      detail.passed = !attemptLines.failed;
      passed = detail.passed;
      ctx.texts.push(JSON.stringify(view.events));
    } catch (error) {
      attemptLines.push(`  FAIL ${error.message}`.slice(0, 300));
      (attemptLines.failed ??= []).push(`${spec.title}: ${error.message}`);
      entry.attempts.push({
        attempt: number,
        passed: false,
        error: { message: error.message.slice(0, 200) },
      });
    }
    lines.push(...attemptLines);
    lines.failed = attemptLines.failed;
    if (!passed && number < attemptsPerScenario)
      lines.push('  (recorded as a failed attempt; trying once more)');
  }
  entry.passed = passed;
  entry.attemptsUsed = entry.attempts.length;
  evidence.push(entry);
  if (passed && entry.attemptsUsed > 1) lines.push(`  note: needed ${entry.attemptsUsed} attempts`);
  flush(lines);
}

const engineInstruction =
  '读取 /work/input/orders.csv，按 region 汇总 amount，写成 summary.csv（列名 region,amount），' +
  '然后运行 printenv > env.txt 记录沙箱环境变量。完成后按约定的 JSON 返回，outputs 列出 summary.csv 和 env.txt。';
const gpuInstruction =
  '用 PyTorch 在 GPU 上对 points.csv（列 x,y）拟合线性回归 y = w*x + b，' +
  '把 {"device": torch.cuda.get_device_name(0), "weight": w, "bias": b} 写入 gpu_fit.json。' +
  '完成后按约定的 JSON 返回，outputs 列出 gpu_fit.json。';

const engineScenario = (ctx, harness) => ({
  id: `engine-${harness}`,
  harness,
  title: `Code agent on ${harness} (${model}, platform-supplied)`,
  body: {
    agentVersionId: ctx.codeAgent.latestVersion.id,
    modelConnectionId: ctx.option.connectionId,
    model,
    harness,
    resourceProfile: 'cpu-2-4',
    input: { instruction: engineInstruction, artifactIds: [ctx.ordersId] },
    limits: {
      maxCostMicros: '3000000',
      currency: 'CNY',
      maxExecutionSeconds: 600,
      maxToolCalls: 30,
    },
  },
  async judge(context, view, lines) {
    report(
      lines,
      view.final.harness?.id === harness,
      `the run was frozen with ${view.final.harness?.name} ${view.final.harness?.version}`,
    );
    const table = view.outputs.find((output) => output.name === 'summary.csv');
    const totals = table ? totalsFromCsv((await download(context, table)).toString('utf8')) : {};
    report(
      lines,
      near(totals.east, 15.75, 0.005) && near(totals.west, 20, 0.005),
      `summary.csv holds the right totals (${JSON.stringify(totals)})`,
    );
    const env = view.outputs.find((output) => output.name === 'env.txt');
    const text = env ? (await download(context, env)).toString('utf8') : '';
    context.texts.push(text);
    const host = new URL(context.channel.baseUrl).host;
    report(
      lines,
      text.length > 0 &&
        !text.includes(context.channel.apiKey) &&
        !text.includes(host) &&
        !text.includes(context.secret),
      `the sandbox environment holds no credential or provider address (${text.split('\n').length} lines checked)`,
    );
    report(
      lines,
      view.events.some((event) => event.source === 'harness' && event.data?.reported === true),
      "the CLI's own steps are recorded as reported, not as platform tool calls",
    );
    const note = view.usage.find((line) => line.meter === 'sandbox_seconds')?.note ?? '';
    report(
      lines,
      note.includes('含模型等待'),
      'the bill says a session holds its allocation while waiting for the model',
    );
  },
});

const gpuScenario = (ctx) => ({
  id: 'gpu-agent',
  title: `GPU agent on the local card (${model}, platform-supplied)`,
  body: {
    agentVersionId: ctx.gpuAgent.latestVersion.id,
    modelConnectionId: ctx.option.connectionId,
    model,
    input: { instruction: gpuInstruction, artifactIds: [ctx.pointsId] },
    limits: {
      maxCostMicros: '3000000',
      currency: 'CNY',
      maxExecutionSeconds: 600,
      maxToolCalls: 12,
    },
  },
  async judge(context, view, lines) {
    report(
      lines,
      view.final.resources?.id === 'gpu-rtx3070-laptop-4-8',
      `the run defaulted to the card's profile (${view.final.resources?.id})`,
    );
    const file = view.outputs.find((output) => output.name === 'gpu_fit.json');
    let fit = {};
    try {
      fit = file ? JSON.parse((await download(context, file)).toString('utf8')) : {};
    } catch {
      fit = {};
    }
    report(
      lines,
      /RTX 3070/.test(String(fit.device)),
      `the code ran on ${fit.device ?? 'no device'}`,
    );
    report(
      lines,
      near(fit.weight, 3, 0.05) && near(fit.bias, 1, 0.2),
      `the fit is right (weight ${fit.weight}, bias ${fit.bias})`,
    );
  },
});

async function upload(ctx, name, bytes) {
  const ticket = await ok(ctx.platform, 'POST', '/v1/files', {
    ...ctx.withKey,
    body: { name, size: bytes.length },
  });
  const response = await call(ctx.platform, 'PUT', ticket.uploadUrl, {
    ...ctx.withKey,
    raw: bytes,
    headers: { 'Content-Type': 'application/octet-stream' },
  });
  if (response.status !== 200 || response.json().status !== 'ready')
    throw new Error(`upload ${name} -> ${response.status}`);
  return ticket.id;
}

async function acceptance(server, log, channel) {
  const platform = `http://127.0.0.1:${ports.platform}`;
  const local = `http://127.0.0.1:${ports.local}`;
  const lines = ['Setup'];
  const readiness = await waitFor(
    'the server to become ready',
    async () => {
      if (server.exitCode !== null)
        throw new Error(`the server exited with ${server.exitCode}; see ${log}`);
      try {
        const response = await call(platform, 'GET', '/readyz');
        return response.status === 200 ? response.json() : null;
      } catch {
        return null;
      }
    },
    { seconds: 180 },
  );
  const available = Object.fromEntries(
    (readiness.capabilities.harnesses ?? []).map((item) => [item.id, item.available]),
  );
  report(
    lines,
    engines.every((id) => available[id]),
    `the server is ready and offers ${Object.keys(available)
      .filter((id) => available[id])
      .join(', ')}`,
  );
  const login = await call(local, 'POST', '/api/v1/auth/login', {
    body: { username: 'admin', password: 'AgentLab-Admin-2026!' },
    headers: { 'X-AgentLab-Request': 'auth' },
  });
  const cookie = login.headers.getSetCookie()[0].split(';')[0];
  const workbench = { cookie, headers: { 'X-AgentLab-Request': 'platform' } };
  const connections = await ok(local, 'GET', '/api/v1/connections', { cookie });
  const gateway = (Array.isArray(connections) ? connections : connections.items).find(
    (item) => item.name === '平台模型网关',
  );
  const verified = await ok(local, 'POST', `/api/v1/connections/${gateway.id}/verify`, {
    cookie,
    body: { version: gateway.version },
    headers: { 'X-AgentLab-Request': 'connections' },
  });
  report(
    lines,
    verified.status === 'active',
    `the platform model connection verifies (${verified.status})`,
  );
  const project = await ok(local, 'POST', '/api/v1/platform/projects', {
    ...workbench,
    body: {
      name: 'Code agents and GPU',
      description: '',
      environment: 'production',
      currency: 'CNY',
      budgetMicros: '30000000',
    },
  });
  const issued = await ok(local, 'POST', '/api/v1/platform/api-keys', {
    ...workbench,
    body: {
      projectId: project.id,
      name: 'agents-acceptance',
      scopes: ['runs:write', 'runs:read', 'files:write', 'artifacts:read', 'usage:read'],
      expiresAt: null,
      monthlyLimitMicros: null,
      ratePerMinute: 600,
    },
  });
  const withKey = { bearer: issued.secret };
  const agents = (await ok(platform, 'GET', '/v1/agents', withKey)).items;
  const codeAgent = agents.find((item) => item.id === 'agt_code_agent');
  const gpuAgent = agents.find((item) => item.id === 'agt_gpu_compute');
  report(
    lines,
    codeAgent?.latestVersion?.harnesses?.length === 4 &&
      gpuAgent?.latestVersion?.resourceProfiles?.length > 0,
    'the code agent and the GPU agent are published',
  );
  const option = (await ok(platform, 'GET', '/v1/models', withKey)).items.find(
    (item) => item.model === model && item.source === 'platform',
  );
  report(lines, option?.usable && option.priced, `${model} is offered, usable and priced`);
  // The CUDA check runs in the background after start-up; until it passes the card is not offered.
  const firstSheet = await ok(platform, 'GET', '/v1/billing/rates', withKey);
  const pending = firstSheet.resources.find((row) => row.id === 'gpu-rtx3070-laptop-4-8');
  lines.push(
    `       before the check: available=${pending?.available} (${pending?.reason ?? '-'})`,
  );
  const sheet = await waitFor(
    'the CUDA check to pass',
    async () => {
      const current = await ok(platform, 'GET', '/v1/billing/rates', withKey);
      return current.resources.find((row) => row.id === 'gpu-rtx3070-laptop-4-8')?.available
        ? current
        : null;
    },
    { seconds: 240, every: 3000 },
  ).catch(() => firstSheet);
  const rates = new Map(
    sheet.resources.filter((row) => row.price).map((row) => [row.id, row.price.perSecondMicros]),
  );
  const card = sheet.resources.find((row) => row.id === 'gpu-rtx3070-laptop-4-8');
  report(
    lines,
    card?.available === true,
    `the RTX 3070 Laptop profile is available (${card?.reason ?? 'checked by a real CUDA run'})`,
  );
  flush(lines);
  if (failures.length) return;

  const ctx = {
    platform,
    local,
    withKey,
    channel,
    secret: issued.secret,
    codeAgent,
    gpuAgent,
    option,
    rates,
    texts: [],
  };
  ctx.ordersId = await upload(
    ctx,
    'orders.csv',
    Buffer.from('order_id,region,amount\nA-1,east,10.50\nA-2,west,20\nA-3,east,5.25\n'),
  );
  let points = 'x,y\n';
  for (let i = 0; i < 100; i += 1) points += `${i / 10},${(3 * i) / 10 + 1}\n`;
  ctx.pointsId = await upload(ctx, 'points.csv', Buffer.from(points));

  await Promise.all([
    ...engines.map((harness) => scenario(ctx, engineScenario(ctx, harness))),
    scenario(ctx, gpuScenario(ctx)),
  ]);

  const closing = ['\nBooks and secrets'];
  const summary = await ok(platform, 'GET', '/v1/usage/summary', withKey);
  const view = await ok(local, 'GET', `/api/v1/platform/projects/${project.id}`, { cookie });
  report(
    closing,
    summary.pendingMicros === '0' && view.budget.heldMicros === '0',
    `nothing is pending or held (settled ${summary.settledMicros})`,
  );
  const serverLog = readFileSync(log, 'utf8');
  report(
    closing,
    ![serverLog, JSON.stringify(evidence), ...ctx.texts].some(
      (text) => text.includes(channel.apiKey) || text.includes(issued.secret),
    ),
    'neither the provider key nor the API key appears in the server log, the events, the outputs or the evidence',
  );
  flush(closing);
}

async function providerSpend(channel) {
  try {
    const response = await fetch(`${new URL(channel.baseUrl).origin}/api/v1/key`, {
      headers: { Authorization: `Bearer ${channel.apiKey}` },
      signal: AbortSignal.timeout(15_000),
    });
    if (!response.ok) return null;
    const { data } = await response.json();
    return typeof data?.usage === 'number' ? data.usage : null;
  } catch {
    return null;
  }
}

async function main() {
  const channel = readChannel();
  mkdirSync(bin, { recursive: true });
  console.log('building binaries');
  for (const [name, pkg] of [
    ['server', './cmd/server'],
    ['seed-demo', './cmd/seed-demo'],
    ['seed-platform', './cmd/seed-platform'],
  ]) {
    run('go', ['build', '-o', exe(name), pkg], { cwd: join(root, 'backend') });
  }
  const env = {
    ...process.env,
    AGENTLAB_MONGO_URI: mongo,
    AGENTLAB_MONGO_DATABASE: database,
    AGENTLAB_CREDENTIAL_KEY: randomBytes(32).toString('base64'),
    BASE_URL: channel.baseUrl,
    API_KEY: channel.apiKey,
    AGENTLAB_CONNECTION_ALLOWED_ORIGINS: new URL(channel.baseUrl).origin,
    AGENTLAB_LISTEN_ADDR: `127.0.0.1:${ports.local}`,
    AGENTLAB_PLATFORM_LISTEN_ADDR: `127.0.0.1:${ports.platform}`,
    AGENTLAB_BLOB_DIR: join(work, 'blobs'),
    AGENTLAB_SANDBOX_IMAGE: images.cpu,
    AGENTLAB_SANDBOX_GPU_IMAGE: images.gpu,
    AGENTLAB_SANDBOX_GPUS: process.env.AGENTLAB_SANDBOX_GPUS || '0',
    AGENTLAB_SANDBOX_AGENT_IMAGE: images.agents,
    AGENTLAB_SANDBOX_CPUS: '4',
    AGENTLAB_SANDBOX_MEMORY_MB: '8192',
    AGENTLAB_SANDBOX_SLOTS: '6',
    AGENTLAB_WORKER_CONCURRENCY: '6',
    AGENTLAB_WORKER_POLL_MS: '200',
    AGENTLAB_LEARNING_EXECUTOR_READY: '0',
  };
  const sandboxContainers = () =>
    spawnSync('docker', ['ps', '-aq', '--filter', 'label=org.agentlab.sandbox=1'], {
      encoding: 'utf8',
    })
      .stdout.split('\n')
      .filter(Boolean);
  const alreadyRunning = new Set(sandboxContainers());
  const spendBefore = await providerSpend(channel);
  const began = Date.now();
  console.log(`seeding a fresh database ${database}`);
  run(exe('seed-demo'), [], { env, stdio: ['ignore', 'ignore', 'inherit'] });
  run(exe('seed-platform'), [], { env, stdio: ['ignore', 'ignore', 'inherit'] });
  const log = join(work, 'server.log');
  const logFd = openSync(log, 'w');
  console.log('starting the server\n');
  const server = spawn(exe('server'), [], { cwd: root, env, stdio: ['ignore', logFd, logFd] });
  try {
    await acceptance(server, log, channel);
    server.kill();
    const closing = ['\nCleanup'];
    const clean = await waitFor(
      'sandbox containers to be gone',
      async () => sandboxContainers().every((id) => alreadyRunning.has(id)),
      { seconds: 60, every: 1000 },
    ).catch(() => false);
    report(closing, clean === true, 'no sandbox container started by this run is left behind');
    flush(closing);
  } catch (error) {
    console.log(`  FAIL ${error.message}`);
    failures.push(error.message);
  } finally {
    if (server.exitCode === null) server.kill('SIGKILL');
    closeSync(logFd);
    spawnSync(
      'docker',
      [
        'compose',
        '-f',
        'compose.test.yaml',
        'exec',
        '-T',
        'mongo',
        'mongosh',
        '--quiet',
        '--eval',
        `db.getSiblingDB("${database}").dropDatabase()`,
      ],
      { cwd: root, stdio: 'ignore' },
    );
    rmSync(join(work, 'blobs'), { recursive: true, force: true });
  }
  await new Promise((done) => setTimeout(done, 6000));
  const spendAfter = await providerSpend(channel);
  const spent =
    spendBefore !== null && spendAfter !== null
      ? Math.round((spendAfter - spendBefore) * 1e6) / 1e6
      : null;
  const outcome = {
    verifiedOn: new Date().toISOString(),
    note: 'Real model provider, real Docker sessions and a real NVIDIA card, throwaway database, public API key only. Sanitized: no keys, no file contents.',
    images,
    model,
    engines,
    providerSpendUsd: spent,
    elapsedSeconds: Math.round((Date.now() - began) / 1000),
    passed: failures.length === 0,
    scenarios: evidence,
  };
  const text = `${JSON.stringify(outcome, null, 2)}\n`;
  writeFileSync(join(work, 'evidence.json'), text);
  if (writeRecord) writeFileSync(join(root, 'assets', 'code-agent-acceptance.json'), text);
  const digest = createHash('sha256').update(text).digest('hex').slice(0, 12);
  console.log(
    `\nprovider spend measured at the provider: ${spent === null ? 'unavailable' : `${spent} USD`}; ${outcome.elapsedSeconds}s; evidence .cache/code-agents/evidence.json (${digest})`,
  );
  if (failures.length) {
    console.error(`\n${failures.length} check(s) failed\n--- server log tail ---`);
    console.error(readFileSync(log, 'utf8').split(channel.apiKey).join('<REDACTED>').slice(-3000));
    process.exit(1);
  }
  console.log('\nall checks passed');
  process.exit(0);
}

await main();
