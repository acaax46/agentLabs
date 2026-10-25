// End-to-end smoke test of the real server binary and the hosted Agent / Tools API.
//
// It builds the server, boots it on a fresh database with the Docker sandbox, puts a scripted
// OpenAI-compatible stand-in behind the platform model connection, and then walks the developer's
// flow through the PUBLIC API-key listener only: upload, run, live event stream, files, signed
// download, usage, ledger and a standalone Tools API call. It also checks that neither the provider
// key nor the API key ever reaches the model.
//
// Needs MongoDB (pnpm db:up), Docker and the sandbox image (pnpm build:sandbox). It never uses a
// real model provider and creates no state outside a throwaway database and .cache/smoke.
import { spawn, spawnSync } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { closeSync, mkdirSync, openSync, readFileSync, rmSync } from 'node:fs';
import { createServer } from 'node:http';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const work = join(root, '.cache', 'smoke');
const bin = join(work, 'bin');
const exe = (name) => join(bin, process.platform === 'win32' ? `${name}.exe` : name);
const ports = { model: 19001, local: 18787, platform: 18788 };
const database = `agentlab_smoke_${process.pid}`;
const providerKey = 'sk-smoke-provider-secret';
const providerModels = [
  'minimax/minimax-m3.1-flash',
  'deepseek/deepseek-v4.1-flash-1017',
  'z-ai/glm-5.3-flash',
];
const image = process.env.AGENTLAB_SANDBOX_IMAGE || 'agentlab/sandbox-python:20261024';
const mongo =
  process.env.AGENTLAB_MONGO_URI ||
  'mongodb://127.0.0.1:27018/?replicaSet=rs0&directConnection=true';

const failures = [];
function check(condition, message) {
  console.log(`  ${condition ? 'ok  ' : 'FAIL'} ${message}`);
  if (!condition) failures.push(message);
  return condition;
}

// ---------------------------------------------------------------- the model stand-in
const modelRequests = [];
const analysis = [
  'import pandas as pd',
  'import matplotlib.pyplot as plt',
  'df = pd.read_csv("/work/input/orders.csv")',
  'summary = df.groupby("region", as_index=False)["amount"].sum()',
  'summary.to_csv("/work/output/summary.csv", index=False)',
  'summary.plot.bar(x="region", y="amount", legend=False)',
  'plt.tight_layout()',
  'plt.savefig("/work/output/chart.png")',
  'print(summary.to_string(index=False))',
  '',
].join('\n');
const answer = {
  summary: '按地区汇总完成',
  findings: ['east 合计 15.75'],
  outputs: [
    { name: 'summary.csv', description: '地区汇总' },
    { name: 'chart.png', description: '柱状图' },
  ],
  warnings: [],
};

const modelServer = createServer((request, response) => {
  const reply = (status, body) => {
    const data = JSON.stringify(body);
    response.writeHead(status, { 'Content-Type': 'application/json' });
    response.end(data);
  };
  const chunks = [];
  request.on('data', (chunk) => chunks.push(chunk));
  request.on('end', () => {
    if (request.headers.authorization !== `Bearer ${providerKey}`) {
      return reply(401, { error: { message: 'bad key' } });
    }
    if (request.method === 'GET' && request.url === '/v1/models') {
      return reply(200, {
        object: 'list',
        data: providerModels.map((id) => ({ id, object: 'model' })),
      });
    }
    if (request.method === 'POST' && request.url === '/v1/chat/completions') {
      const body = JSON.parse(Buffer.concat(chunks).toString('utf8'));
      modelRequests.push({ text: JSON.stringify(body) });
      const sawToolResult = (body.messages ?? []).some((message) => message.role === 'tool');
      const message = sawToolResult
        ? { role: 'assistant', content: JSON.stringify(answer) }
        : {
            role: 'assistant',
            content: null,
            tool_calls: [
              {
                id: 'call_1',
                type: 'function',
                function: { name: 'code_execute', arguments: JSON.stringify({ code: analysis }) },
              },
            ],
          };
      return reply(200, {
        id: 'chatcmpl-smoke',
        model: body.model,
        choices: [{ message, finish_reason: 'stop' }],
        usage: { prompt_tokens: 900, completion_tokens: 80, total_tokens: 980 },
      });
    }
    return reply(404, {});
  });
});

// ---------------------------------------------------------------- a small HTTP client
async function call(base, method, path, { body, headers = {}, bearer, cookie, raw } = {}) {
  const requestHeaders = { ...headers };
  if (body !== undefined) requestHeaders['Content-Type'] = 'application/json';
  if (bearer) requestHeaders.Authorization = `Bearer ${bearer}`;
  if (cookie) requestHeaders.Cookie = cookie;
  if (!['GET', 'PUT', 'HEAD', 'DELETE'].includes(method) && !requestHeaders['Idempotency-Key']) {
    requestHeaders['Idempotency-Key'] = `smoke-${Date.now()}-${randomBytes(4).toString('hex')}`;
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
  if (response.status >= 300) {
    throw new Error(
      `${method} ${path} -> ${response.status} ${response.bytes.toString('utf8').slice(0, 300)}`,
    );
  }
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

// ---------------------------------------------------------------- the scenario
async function scenario(server, log) {
  const platform = `http://127.0.0.1:${ports.platform}`;
  const local = `http://127.0.0.1:${ports.local}`;

  const readiness = await waitFor('the server to become ready', async () => {
    if (server.exitCode !== null)
      throw new Error(`the server exited with ${server.exitCode}; see ${log}`);
    try {
      const response = await call(platform, 'GET', '/readyz');
      return response.status === 200 ? response.json() : null;
    } catch {
      return null;
    }
  });
  check(readiness.ready === true, '/readyz reports ready');
  check(
    readiness.capabilities.codeExecution === true &&
      readiness.capabilities.isolation === 'docker/container',
    `code execution is available and labelled with its real isolation (${readiness.capabilities.isolation})`,
  );
  check((await call(platform, 'GET', '/healthz')).status === 200, '/healthz answers');

  // The workbench side: sign in, verify the model connection, create a project and a key.
  const login = await call(local, 'POST', '/api/v1/auth/login', {
    body: { username: 'admin', password: 'AgentLab-Admin-2026!' },
    headers: { 'X-AgentLab-Request': 'auth' },
  });
  check(login.status === 200, `the admin signs in (${login.status})`);
  const cookie = login.headers.getSetCookie()[0].split(';')[0];
  const connections = await ok(local, 'GET', '/api/v1/connections', { cookie });
  const list = Array.isArray(connections) ? connections : connections.items;
  const gateway = list.find((connection) => connection.name === '平台模型网关');
  const verified = await ok(local, 'POST', `/api/v1/connections/${gateway.id}/verify`, {
    cookie,
    body: { version: gateway.version },
    headers: { 'X-AgentLab-Request': 'connections' },
  });
  check(
    verified.status === 'active',
    `the platform model connection verifies against the stand-in (${verified.status})`,
  );

  const workbench = { cookie, headers: { 'X-AgentLab-Request': 'platform' } };
  const project = await ok(local, 'POST', '/api/v1/platform/projects', {
    ...workbench,
    body: {
      name: 'Smoke',
      description: '',
      environment: 'production',
      currency: 'CNY',
      budgetMicros: '20000000',
    },
  });
  const issued = await ok(local, 'POST', '/api/v1/platform/api-keys', {
    ...workbench,
    body: {
      projectId: project.id,
      name: 'smoke',
      scopes: [
        'runs:write',
        'runs:read',
        'files:write',
        'artifacts:read',
        'usage:read',
        'tools:call',
      ],
      expiresAt: null,
      monthlyLimitMicros: null,
      ratePerMinute: 600,
    },
  });
  const key = issued.secret;
  check(key.startsWith('al_live_'), 'an API key is issued');

  // From here on this is the developer's side, over the public listener only.
  const withKey = { bearer: key };
  check(
    (await call(platform, 'GET', '/v1/projects', { cookie })).status === 401,
    'the public listener ignores workbench cookies',
  );
  const wrong = await call(platform, 'GET', '/v1/projects', {
    bearer: `al_live_deadbeef_${'0'.repeat(40)}`,
  });
  check(wrong.status === 401 && wrong.json().code === 'INVALID_API_KEY', 'a wrong key is refused');
  check(
    (await call(platform, 'POST', '/v1/projects', { ...withKey, body: { name: 'x' } })).status ===
      403,
    'a key cannot create projects',
  );

  const csv = Buffer.from('order_id,region,amount\nA-1,east,10.50\nA-2,west,20\nA-3,east,5.25\n');
  const ticket = await ok(platform, 'POST', '/v1/files', {
    ...withKey,
    body: { name: 'orders.csv', size: csv.length },
  });
  check(
    ticket.uploadUrl === `/v1/files/${ticket.id}/content`,
    `the upload URL names the public mount (${ticket.uploadUrl})`,
  );
  const uploaded = await call(platform, 'PUT', ticket.uploadUrl, {
    ...withKey,
    raw: csv,
    headers: { 'Content-Type': 'application/octet-stream' },
  });
  check(uploaded.status === 200 && uploaded.json().status === 'ready', 'the file uploads');

  const modelOptions = (await ok(platform, 'GET', '/v1/models', withKey)).items.filter(
    (option) => option.usable,
  );
  check(
    modelOptions.length > 0,
    `a usable model is offered (${modelOptions.map((option) => option.model).join(', ')})`,
  );
  const agents = (await ok(platform, 'GET', '/v1/agents', withKey)).items;
  const runBody = {
    agentVersionId: agents[0].latestVersion.id,
    modelConnectionId: modelOptions[0].connectionId,
    model: modelOptions[0].model,
    input: { instruction: '按地区汇总订单金额并画图', artifactIds: [ticket.id] },
    limits: {
      maxCostMicros: '3000000',
      currency: 'CNY',
      maxExecutionSeconds: 120,
      maxToolCalls: 10,
    },
  };
  const started = Date.now();
  const headers = { 'Idempotency-Key': 'smoke-run-0001' };
  const accepted = await call(platform, 'POST', '/v1/runs', { ...withKey, body: runBody, headers });
  check(
    accepted.status === 202 && accepted.json().status === 'queued',
    `the run is accepted (${accepted.status})`,
  );
  const runId = accepted.json().id;
  const again = await call(platform, 'POST', '/v1/runs', { ...withKey, body: runBody, headers });
  check(again.json().id === runId, 'retrying the request returns the same run');

  // Follow the live stream until it ends.
  const stream = await fetch(`${platform}/v1/runs/${runId}/events`, {
    headers: { Authorization: `Bearer ${key}`, Accept: 'text/event-stream' },
    signal: AbortSignal.timeout(180_000),
  });
  const names = [];
  let buffer = '';
  const decoder = new TextDecoder();
  for await (const chunk of stream.body) {
    buffer += decoder.decode(chunk, { stream: true });
    const lines = buffer.split('\n');
    buffer = lines.pop();
    for (const line of lines) if (line.startsWith('event: ')) names.push(line.slice(7));
    if (names.includes('end')) break;
  }
  const seconds = ((Date.now() - started) / 1000).toFixed(1);
  check(
    names[0] === 'run.accepted' && names.at(-2) === 'run.succeeded' && names.at(-1) === 'end',
    `the stream ends with run.succeeded (${names.length} events, ${seconds}s from submit to finish)`,
  );

  const final = await ok(platform, 'GET', `/v1/runs/${runId}`, withKey);
  check(
    final.status === 'succeeded' && final.billingStatus === 'settled',
    `the run succeeded and is settled (cost ${final.costMicros})`,
  );
  check(
    final.outputArtifactIds.length === 2,
    `two output files were produced (${final.outputArtifactIds.length})`,
  );
  const outputs = Object.fromEntries(
    (await ok(platform, 'GET', `/v1/artifacts?kind=output&runId=${runId}`, withKey)).items.map(
      (item) => [item.name, item],
    ),
  );
  const table = await call(
    platform,
    'GET',
    `/v1/artifacts/${outputs['summary.csv'].id}/content`,
    withKey,
  );
  check(
    table.status === 200 &&
      table.bytes.toString('utf8').replaceAll('\r\n', '\n') ===
        'region,amount\neast,15.75\nwest,20.0\n',
    'the CSV is correct',
  );
  const link = await ok(
    platform,
    'POST',
    `/v1/artifacts/${outputs['chart.png'].id}/download-link`,
    withKey,
  );
  const chart = await call(platform, 'GET', link.url);
  check(
    chart.status === 200 &&
      chart.bytes.subarray(0, 4).toString('hex') === '89504e47' &&
      chart.bytes.length > 1000,
    `the chart downloads through a signed link without credentials (${chart.bytes.length} bytes)`,
  );
  check(link.url.startsWith('/v1/download/'), 'the link names the public mount');

  const summary = await ok(platform, 'GET', '/v1/usage/summary', withKey);
  check(
    summary.settledMicros === final.costMicros && summary.pendingMicros === '0',
    `usage agrees with the run (${summary.settledMicros})`,
  );
  const types = (await ok(platform, 'GET', `/v1/billing/ledger?runId=${runId}`, withKey)).items
    .map((entry) => entry.type)
    .sort();
  check(
    types.join() === 'release,reserve,settle',
    `the ledger reserved, settled and released (${types.join(', ')})`,
  );
  const view = await ok(local, 'GET', `/api/v1/platform/projects/${project.id}`, { cookie });
  check(
    view.budget.heldMicros === '0' && view.budget.spentMicros === final.costMicros,
    'the project budget shows nothing held and the cost spent',
  );

  const seen = modelRequests.map((request) => request.text).join('\n');
  check(modelRequests.length === 2, `the model was called twice (${modelRequests.length})`);
  check(
    !seen.includes(providerKey) && !seen.includes(key) && !seen.includes('al_live_'),
    'neither the provider key nor the API key ever reached the model',
  );
  check(
    modelRequests[1].text.includes('east') && modelRequests[1].text.includes('15.75'),
    "the model saw the program's real output",
  );

  // The standalone Tools API on the same listener.
  const callCreated = await ok(platform, 'POST', '/v1/tools/code.execute/calls', {
    ...withKey,
    body: { input: { code: 'print(sum(range(101)))' } },
  });
  const done = await waitFor(
    'the tool call',
    async () => {
      const current = await ok(platform, 'GET', `/v1/tool-calls/${callCreated.id}`, withKey);
      return ['succeeded', 'failed', 'unknown'].includes(current.status) ? current : null;
    },
    { seconds: 60 },
  );
  check(
    done.status === 'succeeded' && JSON.stringify(done.result).includes('5050'),
    `a standalone code.execute call runs in the sandbox (${done.status})`,
  );
}

async function main() {
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
    BASE_URL: `http://127.0.0.1:${ports.model}/v1`,
    API_KEY: providerKey,
    AGENTLAB_CONNECTION_ALLOWED_ORIGINS: `http://127.0.0.1:${ports.model}`,
    AGENTLAB_LISTEN_ADDR: `127.0.0.1:${ports.local}`,
    AGENTLAB_PLATFORM_LISTEN_ADDR: `127.0.0.1:${ports.platform}`,
    AGENTLAB_BLOB_DIR: join(work, 'blobs'),
    AGENTLAB_SANDBOX_IMAGE: image,
    AGENTLAB_WORKER_POLL_MS: '200',
    AGENTLAB_LEARNING_EXECUTOR_READY: '0',
  };
  const sandboxContainers = () =>
    spawnSync('docker', ['ps', '-aq', '--filter', 'label=org.agentlab.sandbox=1'], {
      encoding: 'utf8',
    })
      .stdout.split('\n')
      .filter(Boolean);
  // Containers that belong to someone else (another test, a dev server) are not ours to judge.
  const alreadyRunning = new Set(sandboxContainers());
  await new Promise((done) => modelServer.listen(ports.model, '127.0.0.1', done));
  console.log(`seeding a fresh database ${database}`);
  run(exe('seed-demo'), [], { env, stdio: ['ignore', 'ignore', 'inherit'] });
  run(exe('seed-platform'), [], { env, stdio: ['ignore', 'ignore', 'inherit'] });

  const log = join(work, 'server.log');
  const logFd = openSync(log, 'w');
  console.log('starting the server');
  const server = spawn(exe('server'), [], { cwd: root, env, stdio: ['ignore', logFd, logFd] });
  try {
    await scenario(server, log);
    console.log('stopping the server gracefully');
    if (process.platform === 'win32') {
      console.log('  skip graceful stop check: Windows has no SIGTERM for a child process');
      server.kill();
    } else {
      const exited = new Promise((done) => server.once('exit', (code) => done(code)));
      server.kill('SIGTERM');
      const code = await Promise.race([
        exited,
        new Promise((done) => setTimeout(() => done('timeout'), 60_000)),
      ]);
      check(code === 0, `the server drains and exits cleanly (exit ${code})`);
    }
    // A container someone else started meanwhile may still be finishing; give it a moment before judging.
    const clean = await waitFor(
      'the sandbox containers started by this run to be gone',
      async () => sandboxContainers().every((id) => alreadyRunning.has(id)),
      { seconds: 20, every: 1000 },
    ).catch(() => false);
    check(clean === true, 'no sandbox container started by this run is left behind');
  } catch (error) {
    check(false, error.message);
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
    modelServer.close();
    rmSync(join(work, 'blobs'), { recursive: true, force: true });
  }
  if (failures.length) {
    console.error(`\n${failures.length} check(s) failed\n--- server log tail ---`);
    console.error(readFileSync(log, 'utf8').slice(-3000));
    process.exit(1);
  }
  console.log('\nall checks passed');
  process.exit(0);
}

await main();
