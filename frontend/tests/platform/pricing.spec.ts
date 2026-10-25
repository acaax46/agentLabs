import { test, expect } from './fixtures';
import type { Price } from '../../src/platform/api';

const price: Price = {
  id: 'prc_model',
  kind: 'model',
  name: 'flash-test',
  currency: 'USD',
  provider: 'openai-compatible',
  model: 'minimax/minimax-m3.1-flash',
  inputMicros: '150000',
  cachedInputMicros: '15000',
  outputMicros: '600000',
  perSecondMicros: '0',
  perUnitMicros: '0',
  effectiveFrom: '2026-10-24T00:00:00Z',
  note: 'OpenRouter 基础价快照，固定平台价。',
  referenceUrl: 'https://openrouter.ai/minimax/minimax-m3.1-flash',
  referenceAt: '2026-10-24T00:00:00Z',
};
const hosted: Price = {
  ...price,
  id: 'prc_hosted',
  kind: 'execution',
  name: 'hosted',
  perSecondMicros: '10',
  sandboxBilling: 'separate',
};
const sandboxPrice: Price = {
  ...price,
  id: 'prc_sandbox',
  kind: 'sandbox',
  name: 'sandbox',
  perSecondMicros: '60',
  resourceProfile: 'cpu-2-4',
};

test('模型价格、沙箱规格与 API 示例使用同一计费配置', async ({ page, platform }) => {
  await page.route('**/api/v1/platform/billing/rates?*', (route) =>
    route.fulfill({
      json: {
        execution: hosted,
        resources: [
          {
            id: 'cpu-2-4',
            name: '标准',
            cpu: 2,
            memoryMB: 4096,
            gpu: 0,
            available: true,
            price: sandboxPrice,
          },
          {
            id: 'gpu-l4-8-32',
            name: 'GPU（预设，未开放）',
            cpu: 8,
            memoryMB: 32768,
            gpu: 1,
            gpuType: 'L4',
            available: false,
            reason: 'GPU 执行未开放',
            price: { ...sandboxPrice, perSecondMicros: '720' },
          },
        ],
      },
    }),
  );
  await page.route('**/api/v1/platform/models?*', (route) =>
    route.fulfill({
      json: {
        items: [
          {
            connectionId: 'conn_server',
            connectionName: '管理员渠道',
            provider: 'openai-compatible',
            model: price.model,
            source: 'platform',
            status: 'active',
            usable: true,
            priced: true,
            verifiedAt: null,
            toolCalling: 'unverified',
            price,
          },
        ],
      },
    }),
  );
  await page.goto('/#/agents/agent_server');
  await expect(page.getByText(/每百万 tokens/)).toContainText('$0.15');
  await expect(page.getByRole('link', { name: '价格参考' })).toHaveAttribute(
    'href',
    price.referenceUrl!,
  );
  const allocation = page.getByLabel('沙箱资源规格');
  await expect(allocation).toHaveValue('cpu-2-4');
  await expect(allocation.locator('option[value="gpu-l4-8-32"]')).toHaveAttribute('disabled', '');
  await page.getByLabel('任务指令', { exact: true }).fill('分析订单');
  await page.getByLabel('上传输入文件').setInputFiles({
    name: 'orders.csv',
    mimeType: 'text/csv',
    buffer: Buffer.from('name,amount\na,10\n'),
  });
  await page.getByRole('checkbox', { name: /我确认预占预算/ }).check();
  await page.getByRole('button', { name: '提交试运行', exact: true }).click();
  await expect(page).toHaveURL(/runs\/run_created/);
  expect(
    platform.requests.find((request) => request.path === '/runs' && request.method === 'POST')?.body
      .resourceProfile,
  ).toBe('cpu-2-4');
});

test('管理员可初始化预设和发布精确金额的新模型价格', async ({ page }, testInfo) => {
  const writes: Record<string, unknown>[] = [];
  await page.route('**/api/v1/platform/admin/prices?*', (route) =>
    route.fulfill({ json: { items: [price] } }),
  );
  await page.route('**/api/v1/platform/admin/prices', async (route) => {
    writes.push(route.request().postDataJSON());
    await route.fulfill({ status: 201, json: price });
  });
  await page.route('**/api/v1/platform/admin/prices/presets', (route) =>
    route.fulfill({ json: { items: [price] } }),
  );
  await page.goto('/#/usage');
  await page.getByRole('button', { name: '发布缺失的预设价格' }).click();
  await expect(page.getByRole('status').filter({ hasText: '已发布 1 项' })).toBeVisible();
  await page.getByText('模型与其他价格版本', { exact: true }).click();
  await page.getByRole('button', { name: '发布新版本', exact: true }).click();
  await page.getByLabel('输入（USD）', { exact: true }).fill('0.123456');
  await page.getByLabel('输出（USD）', { exact: true }).fill('0.654321');
  await page.getByRole('button', { name: '确认发布价格版本' }).click();
  await expect(page.getByRole('status').filter({ hasText: '新价格版本已发布' })).toBeVisible();
  expect(writes[0]).toMatchObject({
    inputMicros: '123456',
    outputMicros: '654321',
    kind: 'model',
    model: price.model,
  });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await page.evaluate(() => {
    window.scrollTo(0, 0);
    document.querySelectorAll('.wb-table-wrap').forEach((element) => {
      element.scrollLeft = 0;
    });
  });
  await page.screenshot({ path: testInfo.outputPath('pricing.png'), fullPage: true });
});
