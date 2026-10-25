import { test, expect, project } from './fixtures';

test('项目分页、预算精度与项目切换只读取服务端数据', async ({ page, platform }) => {
  await page.addInitScript(() =>
    localStorage.setItem(
      'agentlab.mvp.state.v1',
      JSON.stringify({ projects: [{ name: '不应显示的本地数据' }] }),
    ),
  );
  await page.goto('/#/projects');
  await expect(page.getByText('文件分析工作区', { exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: project.name, exact: true })).toBeVisible();
  await page.getByRole('button', { name: '下一页', exact: true }).click();
  await expect(page.getByRole('link', { name: '另一个服务端项目', exact: true })).toBeVisible();
  await page.getByRole('button', { name: '上一页', exact: true }).click();
  await expect(page.getByText('不应显示的本地数据')).toHaveCount(0);
  await expect(
    page.getByRole('cell', { name: '$< 0.01 / $500.00 USD', exact: true }).first(),
  ).toBeVisible();
  await page.getByLabel('当前项目', { exact: true }).selectOption('proj_other');
  await page.goto('/#/runs');
  await expect
    .poll(() =>
      platform.requests.some(
        (request) =>
          request.path === '/runs' &&
          new URL(request.url).searchParams.get('projectId') === 'proj_other',
      ),
    )
    .toBe(true);
  await expect(page.getByRole('heading', { name: '暂无运行', exact: true })).toBeVisible();
});

test('创建项目与配置预算、授权使用正确接口和微单位字符串', async ({ page, platform }) => {
  await page.goto('/#/projects?new=1');
  await page.getByLabel('项目名称', { exact: true }).fill('新接口项目');
  await page.getByLabel('用途说明').fill('文件分析');
  await page.getByLabel('项目累计预算', { exact: true }).fill('12.345678');
  await page.getByRole('dialog').getByRole('button', { name: '创建项目', exact: true }).click();
  await expect(page.getByRole('heading', { name: '新接口项目', exact: true })).toBeVisible();
  const creation = platform.requests.find(
    (request) => request.path === '/projects' && request.method === 'POST',
  )!;
  expect(creation.body.budgetMicros).toBe('12345678');
  expect(creation.key).not.toBe('');
  expect(creation.product).toBe('platform');
  await page.getByLabel('项目累计预算（CNY）', { exact: true }).fill('9.000001');
  await page.getByRole('button', { name: '保存预算', exact: true }).click();
  await expect
    .poll(
      () =>
        platform.requests.find((request) => request.path === '/projects/proj_created/budget')?.body
          .limitMicros,
    )
    .toBe('9000001');
  await page.getByLabel('添加连接授权').selectOption('conn_server');
  await page.getByRole('button', { name: '授权连接', exact: true }).click();
  await expect
    .poll(() =>
      platform.requests.some(
        (request) =>
          request.path === '/projects/proj_created/connections/conn_server' &&
          request.method === 'PUT',
      ),
    )
    .toBe(true);
});

test('上传、运行提交、同键重试、事件游标与真实取消状态', async ({ page, platform }) => {
  platform.submitFailures = 1;
  await page.goto('/#/agents/agent_server');
  await page.getByLabel('任务指令', { exact: true }).fill('合并订单文件');
  await page.getByLabel('上传输入文件').setInputFiles({
    name: 'orders.csv',
    mimeType: 'text/csv',
    buffer: Buffer.from('name,amount\na,10\n'),
  });
  await expect(page.getByText('orders.csv ·', { exact: false })).toBeVisible();
  await page.getByLabel('费用上限（USD）', { exact: true }).fill('1.000001');
  await page.getByRole('checkbox', { name: /我确认预占预算/ }).check();
  await page.getByRole('button', { name: '提交试运行', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('请使用同一请求重试');
  expect(
    platform.requests.filter((request) => request.path === '/runs' && request.method === 'POST'),
  ).toHaveLength(1);
  await page.getByRole('button', { name: '提交试运行', exact: true }).click();
  await expect(page).toHaveURL(/runs\/run_created/);
  const submissions = platform.requests.filter(
    (request) => request.path === '/runs' && request.method === 'POST',
  );
  expect(submissions).toHaveLength(2);
  expect(submissions[0]!.key).toBe(submissions[1]!.key);
  expect(submissions[0]!.body).toMatchObject({
    projectId: project.id,
    agentVersionId: 'av_server_1',
    modelConnectionId: 'conn_server',
    model: 'test-model',
    input: { instruction: '合并订单文件', artifactIds: ['file_uploaded'] },
    limits: { maxCostMicros: '1000001', currency: 'USD' },
  });
  expect(platform.requests.find((request) => request.path === '/files')!.body.sha256).toMatch(
    /^[0-9a-f]{64}$/,
  );
  expect(
    platform.requests
      .find((request) => request.path === '/files/file_uploaded/content')!
      .raw?.toString(),
  ).toBe('name,amount\na,10\n');
  await expect(page.getByText('服务端持久事件', { exact: false })).toBeVisible();
  await page.getByRole('button', { name: '停止运行', exact: true }).click();
  await page.getByRole('button', { name: '确认停止', exact: true }).click();
  await expect(page.getByRole('button', { name: '正在停止…', exact: true })).toBeDisabled();
  expect(platform.runs.find((item) => item.id === 'run_created')!.status).toBe('cancelling');
});

test('签发只显示一次的服务端密钥并撤销', async ({ page, platform }) => {
  await page.goto('/#/keys');
  await page.getByRole('button', { name: '创建 API Key', exact: true }).click();
  await page.getByLabel('密钥名称').fill('CI 调用');
  await page.getByLabel('月度费用上限（USD）', { exact: true }).fill('0.000001');
  await page.getByRole('button', { name: '创建密钥', exact: true }).click();
  await expect(page.getByText('al_live_test_once_only_secret', { exact: true })).toBeVisible();
  const creation = platform.requests.find(
    (request) => request.path === '/api-keys' && request.method === 'POST',
  )!;
  expect(creation.body.monthlyLimitMicros).toBe('1');
  expect(creation.body.scopes).toEqual(expect.arrayContaining(['files:write', 'usage:read']));
  expect(creation.key).not.toBe('');
  await expect(page.getByRole('button', { name: '我已保存', exact: true })).toBeDisabled();
  await page.keyboard.press('Escape');
  await expect(page.getByText('al_live_test_once_only_secret', { exact: true })).toBeVisible();
  await page
    .getByRole('checkbox', { name: '我确认已将完整密钥保存到安全位置', exact: true })
    .check();
  await page.getByRole('button', { name: '我已保存', exact: true }).click();
  await expect(page.getByText('al_live_test_once_only_secret', { exact: true })).toHaveCount(0);
  expect(
    await page.evaluate(() =>
      JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }),
    ),
  ).not.toContain('once_only_secret');
  await page.getByRole('button', { name: '撤销', exact: true }).click();
  await page.getByRole('button', { name: '确认撤销', exact: true }).click();
  await expect(page.getByText('已撤销', { exact: true })).toBeVisible();
});

test('产物预览为文本、授权下载、BYOK 与未知用量', async ({ page, platform }) => {
  platform.files.push({
    id: 'artifact_server',
    projectId: project.id,
    kind: 'output',
    name: 'orders.csv',
    type: 'csv',
    mediaType: 'text/csv',
    sizeBytes: 12,
    sha256: 'a'.repeat(64),
    status: 'ready',
    runId: 'run_server',
    summary: '服务端输出',
    retentionDays: 30,
    createdAt: '2026-10-24T00:00:00Z',
    expiresAt: '2099-11-01T00:00:00Z',
  });
  await page.goto('/#/artifacts');
  await page.getByRole('button', { name: '查看产物', exact: true }).click();
  await expect(
    page.getByRole('cell', { name: '<script>alert(1)</script>', exact: true }),
  ).toBeVisible();
  const downloaded = page.waitForEvent('download');
  await page.getByRole('button', { name: '下载产物', exact: true }).click();
  expect((await downloaded).suggestedFilename()).toBe('orders.csv');
  expect(
    platform.requests.find((request) => request.path.endsWith('/download-link'))!.product,
  ).toBe('platform');
  await page.goto('/#/usage');
  await expect(page.getByRole('cell', { name: '由客户承担', exact: true }).first()).toBeVisible();
  await expect(page.getByRole('cell', { name: '含在基础额度内', exact: true })).toBeVisible();
  await page.getByLabel('计量类别', { exact: true }).selectOption('storage');
  await expect
    .poll(() =>
      platform.requests.some(
        (request) =>
          request.path === '/usage' &&
          new URL(request.url).searchParams.get('category') === 'storage',
      ),
    )
    .toBe(true);
  await page.getByRole('tab', { name: '只追加账本', exact: true }).click();
  await expect(page.getByRole('cell', { name: '服务端预算预占', exact: true })).toBeVisible();
});

test('接口失败显示错误、可重试，不回退演示数据', async ({ page, platform }) => {
  platform.projectFailure = 503;
  await page.goto('/#/projects');
  await expect(page.getByRole('alert')).toContainText('平台暂时不可用');
  await expect(page.getByText(project.name, { exact: true })).toHaveCount(0);
  platform.projectFailure = 0;
  await page.getByRole('button', { name: '重新加载', exact: true }).click();
  await expect(page.getByRole('link', { name: project.name, exact: true })).toBeVisible();
});

test('空项目和未知执行能力保留明确引导', async ({ page, platform }) => {
  platform.projects = [];
  await page.goto('/#/runs');
  await expect(page.getByRole('heading', { name: '请先创建项目', exact: true })).toBeVisible();
});

test('桌面与手机页面内容不撑宽、导航可用', async ({ page }, testInfo) => {
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  for (const path of [
    'overview',
    'projects',
    'agents',
    'agents/agent_server',
    'runs',
    'runs/run_server',
    'keys',
    'usage',
    'artifacts',
    'tools',
  ]) {
    await page.goto(`/#/${path}`);
    await expect(page.locator('h1')).toBeVisible();
    await expect
      .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth))
      .toBe(true);
    if (['overview', 'agents/agent_server', 'runs/run_server'].includes(path)) {
      await expect(page.getByText('正在加载…', { exact: true })).toHaveCount(0);
      await page.screenshot({
        path: testInfo.outputPath(`${path.replace('/', '-')}.png`),
        fullPage: true,
      });
    }
  }
  if (page.viewportSize()!.width === 390) {
    await page.getByRole('button', { name: '打开导航菜单', exact: true }).click();
    await expect(page.getByRole('dialog', { name: '工作台导航', exact: true })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('button', { name: '打开导航菜单', exact: true })).toBeFocused();
  }
  expect(errors).toEqual([]);
});

test('执行能力未就绪时禁止提交真实运行', async ({ page, platform }) => {
  platform.readiness.ready = false;
  platform.readiness.capabilities.runs = false;
  await page.goto('/#/agents/agent_server');
  await expect(page.getByText('运行服务尚未就绪或能力未知。', { exact: true })).toBeVisible();
  await page.getByLabel('任务指令', { exact: true }).fill('分析文件');
  await page.getByRole('checkbox', { name: /我确认预占预算/ }).check();
  await expect(page.getByRole('button', { name: '提交试运行', exact: true })).toBeDisabled();
  expect(
    platform.requests.filter((request) => request.path === '/runs' && request.method === 'POST'),
  ).toHaveLength(0);
});

test('资源不存在与会话过期不会继续显示业务数据', async ({ page, platform }) => {
  await page.goto('/#/runs/missing');
  await expect(page.getByRole('heading', { name: '未找到或无权访问', exact: true })).toBeVisible();
  platform.projectFailure = 401;
  await page.reload();
  await expect(page).toHaveURL(/#\/login$/);
  await expect(page.getByRole('heading', { name: '继续你的工作台', exact: true })).toBeVisible();
  await expect(page.getByLabel('当前项目', { exact: true })).toHaveCount(0);
});
