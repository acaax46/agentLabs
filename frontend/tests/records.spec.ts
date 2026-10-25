import { expect, test } from './fixtures';
import type { RuntimeRecord } from '../src/api/client';

test('runtime pages query MongoDB and retain real catalog operation logs', async ({
  page,
  request,
}) => {
  const code = `P-LOG-${Date.now()}`;
  const body = {
    code,
    name: '日志关联练习',
    description: 'PRIVATE-SHOULD-NOT-ENTER-LOG',
    status: 'draft',
    fields: { difficulty: 'easy', timeLimitMs: 1000, memoryLimitMb: 256, tags: [], samples: [] },
  };
  const headers = {
    'Content-Type': 'application/json',
    'X-AgentLab-Request': 'catalog',
    'Idempotency-Key': crypto.randomUUID(),
  };
  const created = await request.post('/api/v1/problems', { headers, data: body });
  expect(created.status()).toBe(201);
  const problem = await created.json();
  const requestID = created.headers()['x-request-id'];
  if (!requestID) throw new Error('Missing server request ID');
  await page.goto(`/#/audit-logs?q=${code}`);
  await expect(page.getByRole('heading', { name: '日志', exact: true })).toBeVisible();
  const row = page.getByRole('row').filter({ hasText: code });
  await expect(row).toContainText('新建题目');
  await row.getByRole('link', { name: '查看', exact: true }).click();
  await expect(page.getByRole('heading', { name: '日志详情', exact: true })).toBeVisible();
  await expect(page.getByText(requestID, { exact: true })).toBeVisible();
  await expect(page.locator('main')).not.toContainText('PRIVATE-SHOULD-NOT-ENTER-LOG');
  await expect(page.locator('main').getByRole('button', { name: /删除|编辑/ })).toHaveCount(0);
  await page.reload();
  await expect(page.getByText(requestID, { exact: true })).toBeVisible();
  const updated = await request.put(`/api/v1/problems/${problem.id}`, {
    headers: { ...headers, 'Idempotency-Key': crypto.randomUUID(), 'If-Match': '"1"' },
    data: { ...body, name: '已编辑的练习' },
  });
  expect(updated.status()).toBe(200);
  const deleted = await request.delete(`/api/v1/problems/${problem.id}`, {
    headers: { ...headers, 'Idempotency-Key': crypto.randomUUID(), 'If-Match': '"2"' },
  });
  expect(deleted.status()).toBe(204);
  await page.goto(`/#/audit-logs?q=${code}`);
  await expect(page.getByRole('row').filter({ hasText: code })).toHaveCount(3);
  await page.getByLabel('日志级别', { exact: true }).selectOption('info');
  await page.getByRole('button', { name: '查询记录', exact: true }).click();
  await expect(page.getByRole('row').filter({ hasText: code })).toHaveCount(3);
  await page.screenshot({ path: '../.cache/agentlab-audit-records.png', fullPage: true });
  for (const [kind, title] of [
    ['evaluations', '评测记录'],
    ['usage-records', '用量记录'],
  ] as const) {
    await page.goto(`/#/${kind}`);
    await expect(page.getByRole('heading', { name: `暂无${title}`, exact: true })).toBeVisible();
    await expect(page.locator('main').getByRole('link', { name: /新建/ })).toHaveCount(0);
    await page.setViewportSize({ width: 390, height: 844 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.setViewportSize({ width: 1440, height: 1000 });
  }
  const denied = await request.post('/api/v1/evaluations', { data: { score: 100 } });
  expect(denied.status()).toBe(405);
});

test('usage rendering preserves integer money and distinguishes unknown from zero (fixture)', async ({
  page,
}) => {
  // These are isolated UI fixtures, never inserted into the application database.
  const base = {
    operationId: 'fixture-operation',
    attemptId: 'fixture-attempt',
    windowId: '',
    sessionId: '',
    studentId: '',
    kind: 'model' as const,
    provider: 'fixture-provider',
    model: '已知用量夹具',
    tool: '',
    status: 'succeeded' as const,
    source: 'provider-reported' as const,
    inputTokens: 0,
    cachedInputTokens: 0,
    outputTokens: 0,
    reasoningTokens: 0,
    totalTokens: 0,
    durationMs: 1,
    costMicros: '9007199254740993',
    currency: 'USD',
    settlement: 'settled' as const,
    priceVersion: 'fixture-price',
    providerRequestId: 'fixture-request',
    errorCode: '',
  };
  const known: RuntimeRecord = {
    id: 'usage-fixture',
    kind: 'usage-records',
    occurredAt: '2026-10-23T00:00:00Z',
    usage: base,
  };
  const unknown: RuntimeRecord = {
    id: 'unknown-fixture',
    kind: 'usage-records',
    occurredAt: '2026-10-23T01:00:00Z',
    usage: {
      ...base,
      attemptId: 'unknown-attempt',
      model: '未知用量夹具',
      status: 'unknown',
      source: 'unknown',
      inputTokens: null,
      cachedInputTokens: null,
      outputTokens: null,
      reasoningTokens: null,
      totalTokens: null,
      durationMs: null,
      costMicros: null,
      settlement: 'pending',
    },
  };
  await page.route('**/api/v1/usage-records?*', (route) =>
    route.fulfill({ json: { items: [known, unknown], total: 2, page: 1, pageSize: 20 } }),
  );
  await page.route('**/api/v1/usage-records/usage-fixture', (route) =>
    route.fulfill({ json: known }),
  );
  await page.goto('/#/usage-records');
  const knownRow = page.getByRole('row').filter({ hasText: '已知用量夹具' });
  await expect(knownRow.locator('td').nth(2)).toHaveText('0');
  await expect(knownRow).toContainText('USD 9007199254.740993');
  const unknownRow = page.getByRole('row').filter({ hasText: '未知用量夹具' });
  await expect(unknownRow.locator('td').nth(2)).toHaveText('未知');
  await expect(unknownRow).toContainText('待结算');
  await knownRow.getByRole('link', { name: '查看', exact: true }).click();
  await expect(page.getByRole('heading', { name: '用量记录详情', exact: true })).toBeVisible();
  await expect(page.getByText('USD 9007199254.740993', { exact: true })).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test('runtime query failure stays visible instead of showing an empty dataset', async ({
  page,
}) => {
  await page.route('**/api/v1/usage-records?*', (route) =>
    route.fulfill({
      status: 503,
      json: {
        code: 'RECORDS_UNAVAILABLE',
        message: '记录数据库暂时不可用。',
        requestId: 'fixture-error',
      },
    }),
  );
  await page.goto('/#/usage-records');
  await expect(page.getByRole('alert')).toContainText('记录数据库暂时不可用。');
  await expect(page.getByRole('heading', { name: '暂无用量记录', exact: true })).toHaveCount(0);
});
