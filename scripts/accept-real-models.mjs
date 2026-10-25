// Real-model acceptance of the hosted Agent / Tools API (the backend half of P0-23).
//
// pnpm smoke:platform proves the plumbing with a scripted stand-in. This proves the product with a REAL
// model provider: the channel in .env.dev (BASE_URL / API_KEY), configured the way a deployment would,
// as the platform model connection. It boots the real server with the Docker sandbox on a throwaway
// database, then walks the developer's flow over the PUBLIC API-key listener only:
//
//   - for each of the three launch models: a CSV analysis run, then an Excel analysis run
//   - one run on a customer's own provider account (BYOK), billed for what the platform supplies only
//   - standalone code.execute and files.transform calls
//   - usage, ledger, budget and storage agree; no secret reaches a response or the server log
//
// Model output is not deterministic, so results are judged by what the files contain, not by how the
// model got there. A scenario gets two attempts; every attempt is recorded, and the report says when a
// model needed the second one.
//
// It SPENDS REAL CREDIT (a few cents) and prints the spend measured at the provider. It never prints the
// key. Needs MongoDB (pnpm db:up), Docker, the sandbox image (pnpm build:sandbox) and network access.
//
//   node scripts/accept-real-models.mjs            run and print a report
//   node scripts/accept-real-models.mjs --record   also write the sanitized record assets/p0-backend-acceptance.json
import { spawn, spawnSync } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import { closeSync, mkdirSync, openSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const work = join(root, '.cache', 'real-models');
const bin = join(work, 'bin');
const exe = (name) => join(bin, process.platform === 'win32' ? `${name}.exe` : name);
const ports = { local: 18797, platform: 18798 };
const database = `agentlab_smoke_real_${process.pid}`;
const image = process.env.AGENTLAB_SANDBOX_IMAGE || 'agentlab/sandbox-python:20261024';
const mongo =
  process.env.AGENTLAB_MONGO_URI ||
  'mongodb://127.0.0.1:27018/?replicaSet=rs0&directConnection=true';
const writeRecord = process.argv.includes('--record');
const models = [
  'minimax/minimax-m3.1-flash',
  'deepseek/deepseek-v4.1-flash-1017',
  'z-ai/glm-5.3-flash',
];
const byokModel = 'z-ai/glm-5.3-flash';
const attemptsPerScenario = 2;

const failures = [];
const evidence = [];
// Failures are kept on the block of lines they belong to, so scenarios running side by side cannot
// disturb each other's verdict.
function report(lines, condition, message) {
  lines.push(`  ${condition ? 'ok  ' : 'FAIL'} ${message}`);
  if (!condition) (lines.failed ??= []).push(message);
  return condition;
}
// Prints a block of lines and counts what failed in it.
function flush(lines) {
  console.log(lines.join('\n'));
  failures.push(...(lines.failed ?? []));
}

// The project's designated model channel. Values stay in memory; nothing below prints them.
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

// ---------------------------------------------------------------- a small HTTP client
async function call(base, method, path, { body, headers = {}, bearer, cookie, raw } = {}) {
  const requestHeaders = { ...headers };
  if (body !== undefined) requestHeaders['Content-Type'] = 'application/json';
  if (bearer) requestHeaders.Authorization = `Bearer ${bearer}`;
  if (cookie) requestHeaders.Cookie = cookie;
  if (!['GET', 'PUT', 'HEAD', 'DELETE'].includes(method) && !requestHeaders['Idempotency-Key']) {
    requestHeaders['Idempotency-Key'] = `real-${Date.now()}-${randomBytes(4).toString('hex')}`;
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

// ---------------------------------------------------------------- workbook fixtures, built in the sandbox image
// The workbook is made by the same image that will read it, so it is a genuine .xlsx.
function sandboxPython(code, input) {
  const result = spawnSync(
    'docker',
    ['run', '--rm', '-i', '--network', 'none', '--entrypoint', 'python3', image, '-I', '-c', code],
    { input, maxBuffer: 32 << 20 },
  );
  if (result.error || result.status !== 0)
    throw new Error(`sandbox python failed: ${result.stderr?.toString('utf8').slice(0, 300)}`);
  return result.stdout;
}

const ordersWorkbook = () =>
  sandboxPython(
    [
      'import io, sys, pandas as pd',
      'buffer = io.BytesIO()',
      'pd.DataFrame({"order_id": ["A-1", "A-2", "A-3"], "region": ["east", "west", "east"],',
      '              "amount": [10.5, 20.0, 5.25]}).to_excel(buffer, index=False, sheet_name="orders")',
      'sys.stdout.buffer.write(buffer.getvalue())',
    ].join('\n'),
  );

// Opens a workbook in the sandbox image and returns its first sheet as records.
const readWorkbook = (bytes) =>
  JSON.parse(
    sandboxPython(
      [
        'import io, sys, pandas as pd',
        'frame = pd.read_excel(io.BytesIO(sys.stdin.buffer.read()))',
        'sys.stdout.write(frame.to_json(orient="records"))',
      ].join('\n'),
      bytes,
    ).toString('utf8'),
  );

// ---------------------------------------------------------------- judging results by content
const expectedTotals = { east: 15.75, west: 20 };
const near = (a, b) => Math.abs(Number(a) - b) < 0.005;

function totalsFromRecords(records) {
  const totals = {};
  for (const row of records) {
    const entries = Object.entries(row).map(([name, value]) => [name.trim().toLowerCase(), value]);
    const region = entries.find(([name]) => name.includes('region'))?.[1];
    const amount = entries.find(([name]) => name.includes('amount') || name.includes('total'))?.[1];
    if (region !== undefined && amount !== undefined)
      totals[String(region).trim()] = Number(amount);
  }
  return totals;
}

function totalsFromCsv(text) {
  const [header, ...rows] = text
    .trim()
    .split(/\r?\n/)
    .map((line) => line.split(','));
  return totalsFromRecords(
    rows.map((cells) => Object.fromEntries(header.map((name, i) => [name, cells[i]]))),
  );
}

const matchesExpected = (totals) =>
  Object.keys(totals).length === 2 &&
  Object.entries(expectedTotals).every(([region, amount]) => near(totals[region], amount));

// ---------------------------------------------------------------- one run, from submit to verdict
const terminal = new Set(['succeeded', 'failed', 'cancelled', 'unknown']);

async function pages(base, path, options) {
  const items = [];
  let cursor = '';
  do {
    const joiner = path.includes('?') ? '&' : '?';
    const page = await ok(
      base,
      'GET',
      `${path}${joiner}limit=100${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`,
      options,
    );
    items.push(...page.items);
    cursor = page.nextCursor || '';
  } while (cursor);
  return items;
}

async function attempt(ctx, scenario) {
  const { platform, withKey } = ctx;
  const started = Date.now();
  const accepted = await ok(platform, 'POST', '/v1/runs', {
    ...withKey,
    body: scenario.body,
    headers: {
      // Only letters, digits and _ . : - are allowed in a key, and model names contain slashes.
      'Idempotency-Key': `real-${scenario.attempt}-${scenario.id.replace(/[^A-Za-z0-9_.:-]/g, '-')}-${randomBytes(3).toString('hex')}`,
    },
  });
  const final = await waitFor(
    `run ${accepted.id}`,
    async () => {
      const current = await ok(platform, 'GET', `/v1/runs/${accepted.id}`, withKey);
      return terminal.has(current.status) ? current : null;
    },
    { seconds: 300, every: 1000 },
  );
  const seconds = (Date.now() - started) / 1000;
  const calls = (await ok(platform, 'GET', `/v1/runs/${accepted.id}/tool-calls`, withKey)).items;
  const outputs = (
    await ok(platform, 'GET', `/v1/artifacts?kind=output&runId=${accepted.id}`, withKey)
  ).items;
  const usage = (await ok(platform, 'GET', `/v1/usage?runId=${accepted.id}`, withKey)).items;
  const ledger = (await ok(platform, 'GET', `/v1/billing/ledger?runId=${accepted.id}`, withKey))
    .items;
  return { accepted, final, seconds, calls, outputs, usage, ledger };
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

// What every successful run must satisfy whoever supplied the model.
function judgeCommon(lines, view, source) {
  const { final, calls, outputs, usage, ledger } = view;
  if (!report(lines, final.status === 'succeeded', `the run succeeded (${final.status})`)) {
    lines.push(`       ${final.error?.code ?? '-'}: ${final.error?.message ?? ''}`.slice(0, 240));
    return false;
  }
  report(
    lines,
    final.billingStatus === 'settled' && final.costMicros !== null,
    `it is settled, cost ${final.costMicros} micro-${final.currency}`,
  );
  const tokens = usage.find((line) => line.category === 'model');
  report(
    lines,
    tokens && tokens.basis === 'provider-reported' && Number(tokens.quantity) > 0,
    `the provider reported its token usage (${tokens?.quantity ?? 'none'} tokens, ${tokens?.basis ?? '-'})`,
  );
  if (source === 'platform') {
    report(
      lines,
      tokens?.status === 'settled' &&
        tokens.amountMicros !== null &&
        Number(tokens.amountMicros) > 0,
      `platform-supplied model usage is priced and settled (${tokens?.amountMicros} micros)`,
    );
  } else {
    report(
      lines,
      tokens?.status === 'not_applicable' && tokens.amountMicros === null,
      `own-account model usage carries no platform charge (${tokens?.status}, amount ${tokens?.amountMicros})`,
    );
  }
  const names = usage.map((line) => line.category).sort();
  report(
    lines,
    calls.some((call) => call.name === 'code.execute' && call.status === 'succeeded') ||
      calls.some((call) => call.name === 'files.transform' && call.status === 'succeeded'),
    `a tool ran (${calls.map((call) => `${call.name}:${call.status}`).join(', ')}); usage categories ${names.join('/')}`,
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
  const claimed = (final.result?.outputs ?? []).map((output) => output.name);
  report(
    lines,
    claimed.length > 0 && claimed.every((name) => outputs.some((output) => output.name === name)),
    `every file the answer lists exists (${claimed.join(', ')})`,
  );
  return true;
}

async function scenario(ctx, spec) {
  const lines = [`\n${spec.title}`];
  const entry = { scenario: spec.id, model: spec.model, source: spec.source, attempts: [] };
  let passed = false;
  for (let number = 1; number <= attemptsPerScenario && !passed; number += 1) {
    const attemptLines = [];
    try {
      const view = await attempt(ctx, { ...spec, attempt: number });
      const detail = {
        attempt: number,
        runId: view.accepted.id,
        status: view.final.status,
        billingStatus: view.final.billingStatus,
        costMicros: view.final.costMicros,
        currency: view.final.currency,
        durationMs: view.final.durationMs,
        modelSteps: view.final.usage?.modelSteps,
        inputTokens: view.final.usage?.inputTokens,
        outputTokens: view.final.usage?.outputTokens,
        // A failed call keeps the platform's own explanation (never file contents or secrets), so a
        // model that stumbles can be told apart from a tool that is too strict.
        toolCalls: view.calls.map((call) => ({
          name: call.name,
          status: call.status,
          ...(call.status === 'succeeded'
            ? {}
            : {
                errorCode: call.error?.code ?? null,
                message: (call.error?.message ?? '').slice(0, 240),
                input: (call.inputSummary ?? '').slice(0, 200),
              }),
        })),
        outputs: view.outputs.map((output) => ({
          name: output.name,
          sizeBytes: output.sizeBytes,
          sha256: output.sha256,
        })),
        error: view.final.error ? { code: view.final.error.code } : null,
      };
      entry.attempts.push(detail);
      lines.push(
        `  attempt ${number}: ${view.final.status} in ${view.seconds.toFixed(0)}s, run ${view.accepted.id}`,
      );
      if (judgeCommon(attemptLines, view, spec.source)) await spec.judge(ctx, view, attemptLines);
      detail.passed = !attemptLines.failed;
      passed = detail.passed;
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
    // A failed attempt stays on record; only the last attempt decides whether the scenario failed.
    lines.failed = attemptLines.failed;
    if (!passed && number < attemptsPerScenario)
      lines.push('  (recorded as a failed attempt; trying once more)');
  }
  entry.passed = passed;
  entry.attemptsUsed = entry.attempts.length;
  evidence.push(entry);
  if (passed && entry.attemptsUsed > 1) lines.push(`  note: needed ${entry.attemptsUsed} attempts`);
  flush(lines);
  return passed;
}

// ---------------------------------------------------------------- the scenarios
const csvInstruction =
  '读取 orders.csv，按 region 汇总 amount，把结果写成 summary.csv（列名 region,amount），' +
  '再画一张各地区金额的柱状图保存为 chart.png。完成后按发布的结果格式返回，并在 outputs 里列出这两个文件。';
const excelInstruction =
  '读取 orders.xlsx（工作表 orders），按 region 汇总 amount，把结果写成 summary.xlsx（列名 region,amount）。' +
  '完成后按发布的结果格式返回，并在 outputs 里列出这个文件。';

function runBody(ctx, option, input, instruction) {
  return {
    agentVersionId: ctx.agent.latestVersion.id,
    modelConnectionId: option.connectionId,
    model: option.model,
    input: { instruction, artifactIds: [input] },
    limits: {
      maxCostMicros: '3000000',
      currency: 'CNY',
      maxExecutionSeconds: 240,
      maxToolCalls: 12,
    },
  };
}

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

const csvScenario = (ctx, option, input, id, title, source) => ({
  id,
  title,
  model: option.model,
  source,
  body: runBody(ctx, option, input, csvInstruction),
  async judge(context, view, lines) {
    const table = view.outputs.find((output) => output.name === 'summary.csv');
    const chart = view.outputs.find((output) => output.name === 'chart.png');
    const totals = table ? totalsFromCsv((await download(context, table)).toString('utf8')) : {};
    const picture = chart ? await download(context, chart) : Buffer.alloc(0);
    return (
      report(
        lines,
        matchesExpected(totals),
        `summary.csv holds the right totals (${JSON.stringify(totals)})`,
      ) &&
      report(
        lines,
        picture.subarray(0, 4).toString('hex') === '89504e47' && picture.length > 1000,
        `chart.png is a real PNG (${picture.length} bytes)`,
      )
    );
  },
});

const excelScenario = (ctx, option, input) => ({
  id: `excel-${option.model}`,
  title: `Excel analysis on ${option.model} (platform-supplied)`,
  model: option.model,
  source: 'platform',
  body: runBody(ctx, option, input, excelInstruction),
  async judge(context, view, lines) {
    const book = view.outputs.find((output) => output.name.toLowerCase().endsWith('.xlsx'));
    if (!report(lines, Boolean(book), `an .xlsx result exists (${book?.name ?? 'none'})`))
      return false;
    const bytes = await download(context, book);
    const totals = totalsFromRecords(readWorkbook(bytes));
    return (
      report(
        lines,
        bytes.subarray(0, 2).toString() === 'PK',
        `${book.name} is a real workbook (${bytes.length} bytes)`,
      ) &&
      report(
        lines,
        matchesExpected(totals),
        `the workbook holds the right totals (${JSON.stringify(totals)})`,
      )
    );
  },
});

async function standaloneTools(ctx, csvId) {
  const lines = ['\nStandalone Tools API calls'];
  const { platform, withKey } = ctx;
  const finish = (id) =>
    waitFor(
      `tool call ${id}`,
      async () => {
        const current = await ok(platform, 'GET', `/v1/tool-calls/${id}`, withKey);
        return ['succeeded', 'failed', 'unknown'].includes(current.status) ? current : null;
      },
      { seconds: 90 },
    );
  const code = await ok(platform, 'POST', '/v1/tools/code.execute/calls', {
    ...withKey,
    body: { input: { code: 'print(sum(range(101)))' } },
  });
  const ran = await finish(code.id);
  report(
    lines,
    ran.status === 'succeeded' && JSON.stringify(ran.result).includes('5050'),
    `code.execute runs in the sandbox (${ran.status})`,
  );
  const transform = await ok(platform, 'POST', '/v1/tools/files.transform/calls', {
    ...withKey,
    body: {
      artifactIds: [csvId],
      input: { operation: 'convert', inputs: ['orders.csv'], output: 'orders.json' },
    },
  });
  const converted = await finish(transform.id);
  report(
    lines,
    converted.status === 'succeeded',
    `files.transform converts a CSV (${converted.status})`,
  );
  const outputs = (
    await ok(platform, 'GET', `/v1/artifacts?kind=output&runId=${transform.id}`, withKey)
  ).items;
  const json = outputs.find((output) => output.name === 'orders.json');
  const rows = json ? JSON.parse((await download(ctx, json)).toString('utf8')) : null;
  report(
    lines,
    Array.isArray(rows) && rows.length === 3,
    `orders.json holds the three rows (${Array.isArray(rows) ? rows.length : 'unreadable'})`,
  );
  evidence.push({
    scenario: 'standalone-tools',
    passed: ran.status === 'succeeded' && converted.status === 'succeeded',
    calls: [
      { name: 'code.execute', status: ran.status },
      { name: 'files.transform', status: converted.status },
    ],
  });
  flush(lines);
}

// ---------------------------------------------------------------- the whole acceptance
async function acceptance(server, log, channel) {
  const platform = `http://127.0.0.1:${ports.platform}`;
  const local = `http://127.0.0.1:${ports.local}`;
  const lines = ['Setup'];

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
  report(
    lines,
    readiness.ready === true && readiness.capabilities.isolation === 'docker/container',
    `the server is ready with ${readiness.capabilities.isolation} isolation`,
  );

  const login = await call(local, 'POST', '/api/v1/auth/login', {
    body: { username: 'admin', password: 'AgentLab-Admin-2026!' },
    headers: { 'X-AgentLab-Request': 'auth' },
  });
  const cookie = login.headers.getSetCookie()[0].split(';')[0];
  const workbench = { cookie, headers: { 'X-AgentLab-Request': 'platform' } };
  const connections = await ok(local, 'GET', '/api/v1/connections', { cookie });
  const list = Array.isArray(connections) ? connections : connections.items;
  const gateway = list.find((connection) => connection.name === '平台模型网关');
  const verified = await ok(local, 'POST', `/api/v1/connections/${gateway.id}/verify`, {
    cookie,
    body: { version: gateway.version },
    headers: { 'X-AgentLab-Request': 'connections' },
  });
  report(
    lines,
    verified.status === 'active',
    `the platform model connection verifies against the real provider (${verified.status})`,
  );

  const project = await ok(local, 'POST', '/api/v1/platform/projects', {
    ...workbench,
    body: {
      name: 'Real models',
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
      name: 'real-acceptance',
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
  const withKey = { bearer: issued.secret };
  const agent = (await ok(platform, 'GET', '/v1/agents', withKey)).items[0];
  const options = (await ok(platform, 'GET', '/v1/models', withKey)).items;
  const optionFor = (model) =>
    options.find((option) => option.model === model && option.source === 'platform');
  for (const model of models) {
    const option = optionFor(model);
    report(
      lines,
      option && option.usable && option.priced,
      `${model} is offered, usable and priced (${option ? `${option.source}` : 'missing'})`,
    );
  }
  flush(lines);
  if (failures.length) return;

  const ctx = { platform, local, cookie, workbench, withKey, project, agent };
  const csvId = await upload(
    ctx,
    'orders.csv',
    Buffer.from('order_id,region,amount\nA-1,east,10.50\nA-2,west,20\nA-3,east,5.25\n'),
  );
  const bookId = await upload(ctx, 'orders.xlsx', ordersWorkbook());

  // Phase 1: the three launch models on a CSV, together, the way customers will use them.
  await Promise.all(
    models.map((model) =>
      scenario(
        ctx,
        csvScenario(
          ctx,
          optionFor(model),
          csvId,
          `csv-${model}`,
          `CSV analysis on ${model} (platform-supplied)`,
          'platform',
        ),
      ),
    ),
  );

  // Phase 2: Excel on every model, a customer's own account, and the standalone tools, together.
  let byokConnection = await ok(local, 'POST', '/api/v1/connections', {
    cookie,
    headers: { 'X-AgentLab-Request': 'connections' },
    body: {
      name: 'Real BYOK',
      provider: gateway.provider,
      baseUrl: channel.baseUrl,
      scope: 'private',
      models: [byokModel],
      apiKey: channel.apiKey,
      expiresAt: null,
    },
  });
  if (!byokConnection.id) {
    // Some answers carry only the list; find the connection by its name.
    const again = await ok(local, 'GET', '/api/v1/connections', { cookie });
    byokConnection = (Array.isArray(again) ? again : again.items).find(
      (item) => item.name === 'Real BYOK',
    );
  }
  const byokVerified = await ok(local, 'POST', `/api/v1/connections/${byokConnection.id}/verify`, {
    cookie,
    body: { version: byokConnection.version },
    headers: { 'X-AgentLab-Request': 'connections' },
  });
  await ok(
    local,
    'PUT',
    `/api/v1/platform/projects/${project.id}/connections/${byokConnection.id}`,
    workbench,
  );
  const byokOption = (await ok(platform, 'GET', '/v1/models', withKey)).items.find(
    (option) => option.connectionId === byokConnection.id && option.model === byokModel,
  );
  const byokLines = ['\nOwn provider account'];
  report(
    byokLines,
    byokVerified.status === 'active' &&
      byokOption?.usable &&
      byokOption.source === 'byok' &&
      byokOption.priced === false,
    `a private connection verifies, is granted to the project and is offered as BYOK, unpriced (${byokVerified.status})`,
  );
  flush(byokLines);

  await Promise.all([
    ...models.map((model) => scenario(ctx, excelScenario(ctx, optionFor(model), bookId))),
    scenario(
      ctx,
      csvScenario(
        ctx,
        byokOption,
        csvId,
        'csv-byok',
        `CSV analysis on ${byokModel} (own provider account)`,
        'byok',
      ),
    ),
    standaloneTools(ctx, csvId),
  ]);

  // Phase 3: the books agree with each other, storage is disclosed, and nothing leaked.
  const closing = ['\nBooks, storage and secrets'];
  const summary = await ok(platform, 'GET', '/v1/usage/summary', withKey);
  const settled = (await pages(platform, '/v1/billing/ledger?type=settle', withKey)).reduce(
    (sum, entry) => sum + BigInt(entry.amountMicros),
    0n,
  );
  report(
    closing,
    summary.pendingMicros === '0' && BigInt(summary.settledMicros) === settled,
    `usage settled ${summary.settledMicros} equals the ledger's settlements ${settled}, nothing pending`,
  );
  const view = await ok(local, 'GET', `/api/v1/platform/projects/${project.id}`, { cookie });
  report(
    closing,
    view.budget.heldMicros === '0' && view.budget.spentMicros === summary.settledMicros,
    `the project budget holds nothing and shows ${view.budget.spentMicros} spent`,
  );
  const stored = await waitFor(
    'the storage sample',
    async () => {
      const page = await ok(platform, 'GET', '/v1/usage?category=storage', withKey);
      return page.items.length ? page : null;
    },
    { seconds: 30, every: 1000 },
  ).catch(() => ({ items: [] }));
  const sample = stored.items[0];
  report(
    closing,
    sample &&
      sample.unit === 'mib_day' &&
      sample.status === 'not_applicable' &&
      sample.amountMicros === null &&
      sample.runId === '',
    `storage is disclosed as ${sample ? `${sample.quantity} MiB-day, not charged` : 'MISSING'}`,
  );
  const after = await ok(platform, 'GET', '/v1/usage/summary', withKey);
  report(
    closing,
    after.groups.some((group) => group.category === 'storage' && group.note !== '') &&
      after.groups.some(
        (group) =>
          group.category === 'model' &&
          group.status === 'not_applicable' &&
          /客户/.test(group.note),
      ),
    'the summary says who bears the own-account model cost and that storage is included',
  );
  const serverLog = readFileSync(log, 'utf8');
  const transcript = JSON.stringify(evidence);
  report(
    closing,
    ![serverLog, transcript].some(
      (text) => text.includes(channel.apiKey) || text.includes(issued.secret),
    ),
    'neither the provider key nor the API key appears in the server log or the evidence',
  );
  flush(closing);
}

// ---------------------------------------------------------------- the provider's own meter
async function providerSpend(channel) {
  try {
    const origin = new URL(channel.baseUrl).origin;
    const response = await fetch(`${origin}/api/v1/key`, {
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
    AGENTLAB_SANDBOX_IMAGE: image,
    AGENTLAB_WORKER_POLL_MS: '200',
    AGENTLAB_STORAGE_SAMPLE_SECONDS: '2',
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
      'the sandbox containers started by this run to be gone',
      async () => sandboxContainers().every((id) => alreadyRunning.has(id)),
      { seconds: 30, every: 1000 },
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

  // The provider's meter lags by a moment; ask again once it has caught up.
  await new Promise((done) => setTimeout(done, 6000));
  const spendAfter = await providerSpend(channel);
  const spent =
    spendBefore !== null && spendAfter !== null
      ? Math.round((spendAfter - spendBefore) * 1e6) / 1e6
      : null;
  const finished = new Date().toISOString();
  const outcome = {
    verifiedOn: finished,
    note: 'Real model provider, real Docker sandbox, throwaway database. Sanitized: no keys, no file contents.',
    sandboxImage: image,
    models,
    byokModel,
    providerSpendUsd: spent,
    elapsedSeconds: Math.round((Date.now() - began) / 1000),
    passed: failures.length === 0,
    scenarios: evidence,
  };
  mkdirSync(work, { recursive: true });
  const text = `${JSON.stringify(outcome, null, 2)}\n`;
  writeFileSync(join(work, 'evidence.json'), text);
  if (writeRecord) writeFileSync(join(root, 'assets', 'p0-backend-acceptance.json'), text);
  const digest = createHash('sha256').update(text).digest('hex').slice(0, 12);
  console.log(
    `\nprovider spend measured at the provider: ${spent === null ? 'unavailable' : `${spent} USD`}; ` +
      `${outcome.elapsedSeconds}s; evidence .cache/real-models/evidence.json (${digest})${writeRecord ? ' and assets/p0-backend-acceptance.json' : ''}`,
  );
  if (failures.length) {
    console.error(`\n${failures.length} check(s) failed\n--- server log tail ---`);
    console.error(readFileSync(log, 'utf8').slice(-2500));
    process.exit(1);
  }
  console.log('\nall checks passed');
  process.exit(0);
}

await main();
