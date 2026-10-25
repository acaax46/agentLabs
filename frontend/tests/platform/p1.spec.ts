import { readFile } from 'node:fs/promises';
import { test, expect, project, run, agent } from './fixtures';
import type { Artifact, StandaloneCall, Tool } from '../../src/platform/api';
import { csv } from '../../src/platform/files';

const tool: Tool = {
  id: 'files.transform@1.0.0',
  name: 'files.transform',
  version: '1.0.0',
  category: 'files',
  summary: '结构化文件处理',
  description: '合并和转换 CSV',
  inputSchema: { type: 'object' },
  outputSchema: { type: 'object' },
  risk: 'low',
  requiresApproval: false,
  sideEffects: '生成项目内文件，不访问外部网络。',
  retry: '同键同参数返回原调用',
  meters: [{ unit: 'file', description: '按输入文件计量' }],
  status: 'available',
  sample: { input: { operation: 'merge', inputs: ['orders.csv'], output: 'merged.csv' } },
  prices: [
    {
      id: 'price_files',
      currency: 'USD',
      unit: 'file',
      perUnitMicros: '1000',
      effectiveFrom: '2026-10-24T00:00:00Z',
    },
  ],
};
const file = (id: string, name: string, type = 'csv'): Artifact => ({
  id,
  name,
  type,
  projectId: project.id,
  kind: 'input',
  mediaType: 'text/plain',
  sizeBytes: 12,
  sha256: 'a'.repeat(64),
  status: 'ready',
  summary: '文件材料',
  retentionDays: 30,
  createdAt: '2026-10-24T00:00:00Z',
  expiresAt: '2099-01-01T00:00:00Z',
});

test('命令面板支持快捷键、导航和有归属校验的完整 ID 查询', async ({ page, platform }) => {
  await page.goto('/#/overview');
  await expect(page.getByRole('button', { name: '打开命令面板', exact: true })).toBeVisible();
  await page.keyboard.press('ControlOrMeta+k');
  const dialog = page.getByRole('dialog', { name: '搜索与跳转', exact: true });
  await expect(dialog).toBeVisible();
  await dialog.getByLabel('搜索页面或资源 ID').fill('run_server');
  await dialog.getByRole('button', { name: '按完整 ID 查询', exact: true }).click();
  await expect(page).toHaveURL(/runs\/run_server/);
  expect(
    platform.requests.some(
      (request) => request.path === '/runs/run_server' && request.method === 'GET',
    ),
  ).toBe(true);
  await page.keyboard.press('ControlOrMeta+k');
  await page
    .getByRole('dialog', { name: '搜索与跳转', exact: true })
    .getByLabel('搜索页面或资源 ID')
    .fill('工具调用');
  await page
    .getByRole('dialog', { name: '搜索与跳转', exact: true })
    .getByRole('button', { name: '工具调用', exact: true })
    .click();
  await expect(page).toHaveURL(/tool-calls/);
  await page.keyboard.press('?');
  await expect(page.getByRole('dialog', { name: '快捷键说明', exact: true })).toBeVisible();
  await page.keyboard.press('Escape');
  await page.keyboard.press('ControlOrMeta+k');
  await dialog.getByLabel('搜索页面或资源 ID').fill('agent_server');
  await dialog.getByLabel('ID 查询类型').selectOption('agents');
  await dialog.getByRole('button', { name: '按完整 ID 查询', exact: true }).click();
  await expect(page).toHaveURL(/agents\/agent_server$/);
});

test('独立工具调用固定版本、费用确认、历史与未知状态', async ({ page, platform }) => {
  platform.files.push(file('art_input', 'orders.csv'));
  let call: StandaloneCall = {
    id: 'tc_p1',
    projectId: project.id,
    standalone: true,
    name: tool.name,
    version: tool.version,
    status: 'planned',
    inputSummary: 'merge orders.csv',
    outputSummary: '',
    startedAt: null,
    finishedAt: null,
    durationMs: null,
    costMicros: null,
    currency: 'USD',
    billingStatus: 'pending',
    createdAt: '2026-10-24T00:00:00Z',
    artifactIds: [],
    unit: 'file',
    quantity: '1',
    error: null,
  };
  let submitted: Record<string, unknown> | undefined;
  let idempotency = '';
  let writes = 0;
  await page.route('**/api/v1/platform/tools', (route) =>
    route.fulfill({ contentType: 'application/json', body: JSON.stringify({ items: [tool] }) }),
  );
  await page.route('**/api/v1/platform/tools/files.transform/calls', (route) => {
    writes++;
    submitted = route.request().postDataJSON() as Record<string, unknown>;
    idempotency = route.request().headers()['idempotency-key'] ?? '';
    return route.fulfill({
      status: 202,
      contentType: 'application/json',
      body: JSON.stringify(call),
    });
  });
  await page.route('**/api/v1/platform/tool-calls/tc_p1', (route) =>
    route.fulfill({ contentType: 'application/json', body: JSON.stringify(call) }),
  );
  await page.route('**/api/v1/platform/tool-calls?*', (route) =>
    route.fulfill({ contentType: 'application/json', body: JSON.stringify({ items: [call] }) }),
  );
  await page.goto('/#/tools');
  await page.getByRole('button', { name: '调试', exact: true }).click();
  await page.getByRole('checkbox', { name: 'orders.csv', exact: true }).check();
  await page.getByRole('checkbox', { name: /我确认按已展示的工具价格/ }).check();
  await page.getByRole('button', { name: '提交工具调用', exact: true }).click();
  await expect(page).toHaveURL(/tool-calls\/tc_p1/);
  expect(idempotency).not.toBe('');
  expect(submitted).toMatchObject({
    projectId: project.id,
    version: '1.0.0',
    artifactIds: ['art_input'],
    input: { operation: 'merge' },
  });
  call = {
    ...call,
    status: 'unknown',
    billingStatus: 'pending',
    error: { code: 'RESULT_UNKNOWN', message: '执行结果待核对', nextStep: '核对账本和产物' },
  };
  await expect(
    page.getByText('结果待核对，不能按普通失败重试或承诺退款。', { exact: true }),
  ).toBeVisible({ timeout: 10000 });
  await expect(page.getByRole('button', { name: '停止工具调用', exact: true })).toHaveCount(0);
  expect(writes).toBe(1);
  await page.goto('/#/tool-calls');
  await expect(page.getByRole('link', { name: 'tc_p1', exact: true })).toBeVisible();
});

test('多语言示例和后端版本差异不模拟发布', async ({ page, platform }) => {
  const second = structuredClone(agent.latestVersion!);
  second.id = 'av_p1_two';
  second.version = '2.0.0';
  second.tools = [{ name: 'code.execute', version: '2.0.0' }];
  second.limits.maxToolCalls = 20;
  await page.route('**/api/v1/platform/agents/agent_server', (route) =>
    route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({ agent, versions: [agent.latestVersion, second] }),
    }),
  );
  await page.goto('/#/agents/agent_server/versions');
  await page.getByLabel('右侧版本').selectOption('av_p1_two');
  await expect(page.getByText('已变化', { exact: true }).first()).toBeVisible();
  await expect(
    page.getByText('草稿编辑、发布新版本及 API 默认版本切换未开放。', { exact: false }),
  ).toBeVisible();
  expect(platform.requests.filter((request) => request.method !== 'GET')).toHaveLength(0);
  await page.goto('/#/agents/agent_server');
  for (const name of ['Python', 'JavaScript', 'Go']) {
    await page.getByRole('tab', { name, exact: true }).click();
    await expect(
      page.locator('.wb-code').filter({ hasText: 'AGENTLAB_API_KEY' }).first(),
    ).toBeVisible();
  }
});

test('筛选视图与列偏好可以恢复且不保存业务记录', async ({ page, platform }) => {
  platform.runs[0]!.model.source = 'platform';
  platform.runs.push({
    ...structuredClone(run),
    id: 'run_byok',
    model: { ...run.model, source: 'byok' },
    costMicros: '2000000',
  });
  await page.goto('/#/runs');
  await page.getByText('高级筛选、保存视图与列设置', { exact: true }).click();
  await page.getByLabel('模型来源（本页）').selectOption('byok');
  await page.getByRole('button', { name: '应用高级筛选', exact: true }).click();
  await expect(page.getByRole('link', { name: 'run_byok', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'run_server', exact: true })).toHaveCount(0);
  await page.getByLabel('视图名称').fill('BYOK 视图');
  await page.getByRole('button', { name: '保存当前筛选视图', exact: true }).click();
  await page.getByRole('checkbox', { name: '耗时', exact: true }).uncheck();
  await expect(page.getByRole('columnheader', { name: '耗时', exact: true })).toHaveCount(0);
  await page.reload();
  await expect(page.getByRole('columnheader', { name: '耗时', exact: true })).toHaveCount(0);
  const data = await page.evaluate(() => JSON.stringify({ ...localStorage }));
  expect(data).toContain('BYOK 视图');
  expect(data).not.toContain('inputSummary');
  expect(data).not.toContain('costMicros');
});

test('API Key 用量、账本视图恢复、CSV 安全导出与真实汇总趋势', async ({ page, platform }) => {
  platform.keys.push({
    id: 'key_p1',
    projectId: project.id,
    name: 'automation',
    prefix: 'al_live_public',
    scopes: ['usage:read'],
    status: 'active',
    state: 'active',
    expiresAt: null,
    lastUsedAt: null,
    monthlyLimitMicros: null,
    currency: 'USD',
    ratePerMinute: 60,
    createdAt: '2026-10-24T00:00:00Z',
  });
  platform.usage[0]!.meter = '=WEBSERVICE("https://blocked.example")';
  await page.goto('/#/usage');
  await page.getByLabel('API Key 用量').selectOption('key_p1');
  await expect
    .poll(() =>
      platform.requests.some(
        (request) =>
          request.path === '/usage' && new URL(request.url).searchParams.get('keyId') === 'key_p1',
      ),
    )
    .toBe(true);
  const download = page.waitForEvent('download');
  await page.getByRole('button', { name: '导出当前页 CSV', exact: true }).click();
  const path = await (await download).path();
  const text = await readFile(path!, 'utf8');
  expect(text).toContain("'=WEBSERVICE");
  expect(text).not.toContain('once_only_secret');
  expect(csv(['value'], [['+SUM(1,2)']])).toContain("'+SUM");
  await page.getByRole('tab', { name: '只追加账本', exact: true }).click();
  await expect(page).toHaveURL(/view=ledger/);
  await page.reload();
  await expect(page.getByRole('tab', { name: '只追加账本', exact: true })).toHaveAttribute(
    'aria-selected',
    'true',
  );
  await expect(page.getByRole('cell', { name: '服务端预算预占', exact: true })).toBeVisible();
  const ledgerDownload = page.waitForEvent('download');
  await page.getByRole('button', { name: '导出当前页 CSV', exact: true }).click();
  expect(await readFile((await (await ledgerDownload).path())!, 'utf8')).toContain('ledger_server');
  await page.goto('/#/overview');
  await expect(
    page.getByRole('img', { name: '近七日已结算费用，精确数值见下表', exact: true }),
  ).toBeVisible();
  expect(
    platform.requests.filter(
      (request) =>
        request.path === '/usage/summary' && new URL(request.url).searchParams.has('from'),
    ).length,
  ).toBeGreaterThanOrEqual(7);
});

test('Excel 安全预览、CSV 图表与批量授权 ZIP', async ({ page, platform }, testInfo) => {
  const bytes = await readFile(new URL('./data/orders.xlsx', import.meta.url));
  platform.files.push(
    { ...file('art_excel', 'orders.xlsx', 'xlsx'), sizeBytes: bytes.length },
    file('art_csv', 'orders.csv'),
  );
  await page.route('**/api/v1/platform/artifacts/art_excel/content', (route) =>
    route.fulfill({ contentType: 'application/octet-stream', body: bytes }),
  );
  await page.goto('/#/artifacts');
  await page
    .getByRole('row')
    .filter({ hasText: 'orders.xlsx' })
    .getByRole('button', { name: '查看产物', exact: true })
    .click();
  await expect(page.getByRole('cell', { name: 'east', exact: true })).toBeVisible();
  await page.getByText('用预览数据绘图', { exact: true }).click();
  await expect(page.getByRole('img', { name: /预览柱状图/ })).toBeVisible();
  await page.keyboard.press('Escape');
  await page.getByRole('checkbox', { name: '选择产物 orders.xlsx', exact: true }).check();
  await page.getByRole('checkbox', { name: '选择产物 orders.csv', exact: true }).check();
  const download = page.waitForEvent('download');
  await page.getByRole('button', { name: '批量下载 ZIP（2）', exact: true }).click();
  const archive = await download;
  await archive.saveAs(testInfo.outputPath('artifacts.zip'));
  const content = await readFile((await archive.path())!);
  expect(content.subarray(0, 4).toString('hex')).toBe('504b0304');
  expect(content.includes(Buffer.from('artifact-manifest.json'))).toBe(true);
  expect(
    platform.requests.filter((request) => request.path.endsWith('/download-link')).length,
  ).toBe(2);
});

test('未有服务接口的审批、Webhook和成员管理明确未开放', async ({ page, platform }) => {
  for (const path of [
    'approvals',
    'webhooks',
    'projects/proj_server/members',
    'agents/agent_server/draft',
    'connections/custom',
  ]) {
    await page.goto('/#/' + path);
    await expect(page.getByRole('heading', { name: /未开放$/ })).toBeVisible();
  }
  expect(platform.requests.filter((request) => request.method !== 'GET')).toHaveLength(0);
});

test('日志按级别搜索和换行，导出内容移除终端控制符', async ({ page, platform }) => {
  platform.runs[0]!.status = 'succeeded';
  await page.route('**/api/v1/platform/runs/run_server/events?*', (route) =>
    route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        items: [
          {
            id: 'ev_info',
            seq: 1,
            at: run.createdAt,
            level: 'info',
            source: 'worker',
            type: 'started',
            message: '普通日志',
          },
          {
            id: 'ev_error',
            seq: 2,
            at: run.createdAt,
            level: 'error',
            source: 'worker',
            type: 'failed',
            message: '\u001b[31m文件读取失败\u001b[0m',
          },
        ],
        cursor: 2,
        terminal: true,
        status: 'succeeded',
      }),
    }),
  );
  await page.goto('/#/runs/run_server');
  await page.getByRole('tab', { name: '执行时间线', exact: true }).click();
  await page.getByRole('combobox', { name: '日志级别', exact: true }).selectOption('error');
  await page.getByLabel('搜索日志', { exact: true }).fill('文件');
  await expect(page.locator('.wb-event-log pre')).toHaveText(['文件读取失败']);
  await page.getByRole('checkbox', { name: '日志换行', exact: true }).uncheck();
  await expect(page.locator('.wb-event-log')).toHaveAttribute('data-wrap', 'false');
  const downloading = page.waitForEvent('download');
  await page.getByRole('button', { name: '下载筛选日志', exact: true }).click();
  const text = await readFile((await (await downloading).path())!, 'utf8');
  expect(text).toContain('[error] worker 文件读取失败');
  expect(text).not.toContain('普通日志');
  expect(text).not.toContain('\u001b');
});

test('证据包保留后续页游标并明确快照范围', async ({ page, platform }) => {
  platform.runs[0]!.status = 'succeeded';
  await page.route('**/api/v1/platform/usage?*', (route) =>
    route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({ items: platform.usage, nextCursor: 'more_usage' }),
    }),
  );
  await page.goto('/#/runs/run_server');
  const downloading = page.waitForEvent('download');
  await page.getByRole('button', { name: '导出运行证据包 JSON', exact: true }).click();
  const data = JSON.parse(await readFile((await (await downloading).path())!, 'utf8')) as {
    partial: boolean;
    scope: string;
    usage: { nextCursor: string };
    run: { id: string };
  };
  expect(data.partial).toBe(true);
  expect(data.usage.nextCursor).toBe('more_usage');
  expect(data.run.id).toBe(run.id);
  expect(data.scope).toContain('快照');
  await expect(
    page.getByText('部分明细仍有后续页，证据包内已记录游标，不能视为完整历史。', { exact: true }),
  ).toBeVisible();
});

test('Excel 拒绝外部实体和超过预览上限的文件', async ({ page, platform }) => {
  const bytes = await readFile(new URL('./data/external-entity.xlsx', import.meta.url));
  platform.files.push(
    { ...file('art_unsafe', 'external.xlsx', 'xlsx'), sizeBytes: bytes.length },
    { ...file('art_large', 'large.xlsx', 'xlsx'), sizeBytes: 9 * 1024 * 1024 },
  );
  const outside: string[] = [];
  page.on('request', (request) => {
    if (request.url().includes('blocked.example')) outside.push(request.url());
  });
  await page.route('**/api/v1/platform/artifacts/art_unsafe/content', (route) =>
    route.fulfill({ body: bytes }),
  );
  await page.goto('/#/artifacts');
  await page
    .getByRole('row')
    .filter({ hasText: 'external.xlsx' })
    .getByRole('button', { name: '查看产物', exact: true })
    .click();
  await expect(
    page.getByRole('dialog').getByText('预览不允许 XML 外部实体。', { exact: true }),
  ).toBeVisible();
  expect(outside).toEqual([]);
  await page.keyboard.press('Escape');
  await page
    .getByRole('row')
    .filter({ hasText: 'large.xlsx' })
    .getByRole('button', { name: '查看产物', exact: true })
    .click();
  await expect(
    page.getByRole('dialog').getByText('Excel 预览上限 8 MB，请下载查看。', { exact: true }),
  ).toBeVisible();
  expect(platform.requests.some((request) => request.path === '/artifacts/art_large/content')).toBe(
    false,
  );
});

test('全局快捷键不能遮盖尚未保存的一次性密钥', async ({ page }) => {
  await page.goto('/#/keys');
  await page.getByRole('button', { name: '创建 API Key', exact: true }).click();
  await page.getByLabel('密钥名称', { exact: true }).fill('P1 shortcut');
  await page.getByRole('button', { name: '创建密钥', exact: true }).click();
  const confirm = page.getByRole('checkbox', {
    name: '我确认已将完整密钥保存到安全位置',
    exact: true,
  });
  await expect(confirm).toBeVisible();
  await page.keyboard.press('ControlOrMeta+k');
  await expect(page.getByRole('dialog', { name: '搜索与跳转', exact: true })).toHaveCount(0);
  await expect(confirm).toBeVisible();
});

test('新增页面在各视口内布局且没有脚本异常', async ({ page }, testInfo) => {
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  for (const path of [
    'overview',
    'runs',
    'tool-calls',
    'agents/agent_server/versions',
    'usage',
    'artifacts',
  ]) {
    await page.goto('/#/' + path);
    await expect(page.getByRole('button', { name: '打开命令面板', exact: true })).toBeVisible();
    await expect(page.locator('main h1')).toBeVisible();
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1),
    ).toBe(true);
    await page.screenshot({ path: testInfo.outputPath(path.replaceAll('/', '-') + '.png') });
  }
  expect(errors).toEqual([]);
});
