import { createServer } from 'node:http';
import type { Server } from 'node:http';
import { mkdir } from 'node:fs/promises';
import { expect, test } from '@playwright/test';

const gateway = 'http://127.0.0.1:18990';
let server: Server;
let acceptedKey = 'account-canary-original';
test.beforeEach(async ({ page }) => {
  const response = await page.request.post('/api/v1/auth/login', {
    headers: { 'X-AgentLab-Request': 'auth' },
    data: { username: 'admin', password: 'AgentLab-Admin-2026!' },
  });
  expect(response.status()).toBe(200);
});
test.beforeAll(async () => {
  server = createServer((req, res) => {
    if (req.url !== '/v1/models' || req.headers.authorization !== `Bearer ${acceptedKey}`) {
      res.writeHead(401).end();
      return;
    }
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ data: [{ id: 'test-model' }] }));
  });
  await new Promise<void>((resolve) => server.listen(18990, '127.0.0.1', resolve));
  await mkdir('../.cache', { recursive: true });
});
test.afterAll(async () => {
  await new Promise<void>((resolve, reject) =>
    server.close((err) => (err ? reject(err) : resolve())),
  );
});

test('model accounts persist, verify, rotate and revoke without exposing credentials', async ({
  page,
}) => {
  await page.goto('/#/connections');
  await page.getByRole('button', { name: '连接模型账户', exact: true }).click();
  let dialog = page.getByRole('dialog');
  await dialog.getByLabel('连接名称', { exact: true }).fill('浏览器验收模型账户');
  await dialog.getByLabel('模型服务地址', { exact: true }).fill(gateway);
  await dialog.getByLabel('API Key', { exact: true }).fill(acceptedKey);
  await dialog.getByLabel('允许使用的模型', { exact: true }).fill('test-model');
  await dialog.getByRole('checkbox').check();
  await dialog.getByRole('button', { name: '保存并验证', exact: true }).click();
  await expect(dialog).not.toBeVisible();
  const card = page.getByRole('row').filter({ hasText: '浏览器验收模型账户' });
  await expect(card).toContainText('已连接');
  const result = await page.request.get('/api/v1/connections');
  const text = await result.text();
  expect(text).not.toContain(acceptedKey);
  expect(text).not.toContain('ciphertext');
  await page.reload();
  await expect(card).toContainText('已连接');
  expect(
    await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage })),
  ).not.toContain(acceptedKey);
  await card.getByRole('button', { name: '轮换', exact: true }).click();
  dialog = page.getByRole('dialog');
  acceptedKey = 'account-canary-rotated';
  await dialog.getByLabel('新的 API Key', { exact: true }).fill(acceptedKey);
  await dialog.getByRole('button', { name: '轮换密钥', exact: true }).click();
  await expect(dialog).not.toBeVisible();
  await expect(card).toContainText('待验证');
  await card.getByRole('button', { name: '验证', exact: true }).click();
  await expect(card).toContainText('已连接');
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: '../.cache/account-connections-desktop.png', fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.evaluate(() => window.scrollTo(0, 0));
  await expect(card.getByRole('button', { name: '验证', exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: '../.cache/account-connections-mobile.png', fullPage: true });
  await card.getByRole('button', { name: '撤销', exact: true }).click();
  await page.getByRole('dialog').getByRole('button', { name: '确认撤销', exact: true }).click();
  await expect(page.getByRole('dialog')).not.toBeVisible();
  await expect(card).toContainText('已撤销');
  await expect(card.getByRole('button', { name: '验证', exact: true })).toHaveCount(0);
});

test('users see shared metadata, cannot manage it or read another user private connection', async ({
  page,
  browser,
}) => {
  const headers = { 'X-AgentLab-Request': 'connections', 'Idempotency-Key': crypto.randomUUID() };
  const shared = await page.request.post('/api/v1/connections', {
    headers,
    data: {
      name: '机构共享验收',
      provider: 'openai-compatible',
      baseUrl: gateway,
      scope: 'tenant',
      models: ['test-model'],
      apiKey: acceptedKey,
    },
  });
  expect(shared.status()).toBe(200);
  const connection = await shared.json();
  const context = await browser.newContext({ baseURL: test.info().project.use.baseURL });
  try {
    expect(
      (
        await context.request.post('/api/v1/auth/login', {
          headers: { 'X-AgentLab-Request': 'auth' },
          data: { username: 'student', password: 'AgentLab-Student-2026!' },
        })
      ).status(),
    ).toBe(200);
    const student = await context.newPage();
    await student.goto('/#/connections');
    const card = student.getByRole('row').filter({ hasText: '机构共享验收' });
    await expect(card).toContainText('由管理员管理');
    await expect(card.getByRole('button')).toHaveCount(0);
    await expect(student.getByText('浏览器验收模型账户', { exact: true })).toHaveCount(0);
    const denied = await context.request.post(`/api/v1/connections/${connection.id}/revoke`, {
      headers: { 'X-AgentLab-Request': 'connections' },
      data: { version: connection.version },
    });
    expect(denied.status()).toBe(403);
    await student.getByRole('button', { name: '连接模型账户', exact: true }).click();
    await expect(student.getByRole('dialog').locator('select[name="scope"] option')).toHaveText([
      '仅我使用',
    ]);
  } finally {
    await context.close();
    await page.request.post(`/api/v1/connections/${connection.id}/revoke`, {
      headers: { 'X-AgentLab-Request': 'connections' },
      data: { version: connection.version },
    });
  }
});
