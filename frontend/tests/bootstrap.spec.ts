import { expect, test } from './fixtures';

test('real API state stays visible and execution remains unavailable', async ({
  page,
  request,
}) => {
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto('/');
  await expect(page.getByRole('heading', { name: '让代码，在安全沙箱里运行。' })).toBeVisible();
  await expect(page.getByRole('heading', { name: '你的学习内容' })).toBeVisible();
  await page.getByRole('link', { name: '查看环境状态 →' }).click();
  await expect(page.getByRole('heading', { name: '尚未连接代码沙箱' })).toBeVisible();
  await page.getByRole('button', { name: '重新检查' }).click();
  await expect(page.getByText('已连接 · v0.1.0')).toBeVisible();
  const response = await request.post('/api/v1/sessions');
  expect(response.status()).toBe(503);
  expect((await response.json()).code).toBe('RUNTIME_UNAVAILABLE');
  await page.getByRole('link', { name: '课程管理', exact: true }).click();
  await expect(page.getByRole('heading', { name: '课程管理', exact: true })).toBeVisible();
  await page.getByRole('link', { name: '控制台概览', exact: true }).click();
  await page.screenshot({ path: '../.cache/agentlab-desktop.png', fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: '../.cache/agentlab-mobile.png', fullPage: true });
  expect(errors).toEqual([]);
});

test('a lost backend shows an error instead of sample student data', async ({ page }) => {
  await page.route('**/api/v1/bootstrap', (route) => route.abort());
  await page.goto('/');
  await expect(page.getByRole('heading', { name: '暂时无法完成操作' })).toBeVisible();
  await expect(page.getByRole('button', { name: '重新加载' })).toBeVisible();
});
