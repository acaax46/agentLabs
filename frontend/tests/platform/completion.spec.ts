import { test, expect, project } from './fixtures';
import type { Locator, Page } from '@playwright/test';

async function reach(page: Page, target: Locator) {
  for (let n = 0; n < 160; n++) {
    if (await target.evaluate((node) => node === document.activeElement).catch(() => false)) return;
    await page.keyboard.press('Tab');
  }
  throw new Error('目标控件无法用 Tab 到达');
}

test('keyboard chooses a file, submits a run and downloads its output through navigation links', async ({
  page,
  platform,
}) => {
  test.setTimeout(90_000);
  await page.goto('/#/agents/agent_server');
  const instruction = page.getByLabel('任务指令', { exact: true });
  await expect(instruction).toBeVisible();
  await reach(page, instruction);
  await page.keyboard.insertText('汇总输入订单');
  await reach(page, page.getByLabel('上传输入文件'));
  const chooser = page.waitForEvent('filechooser');
  await page.keyboard.press('Enter');
  // The keyboard opens the browser's native picker. Automation supplies its selection, without
  // bypassing the input's change event or simulating product business data in browser storage.
  await (
    await chooser
  ).setFiles({
    name: 'keyboard.csv',
    mimeType: 'text/csv',
    buffer: Buffer.from('name,amount\na,10\n'),
  });
  await expect(page.getByText('服务端摘要校验通过', { exact: false })).toBeVisible();
  await reach(page, page.getByRole('checkbox', { name: /我确认预占预算/ }));
  await page.keyboard.press('Space');
  await reach(page, page.getByRole('button', { name: '提交试运行', exact: true }));
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/runs\/run_created/);
  expect(
    platform.requests.find((request) => request.path === '/runs' && request.method === 'POST')?.body
      .input,
  ).toMatchObject({ artifactIds: ['file_uploaded'] });
  platform.files.push({
    id: 'keyboard_result',
    projectId: project.id,
    kind: 'output',
    name: 'summary.csv',
    type: 'csv',
    mediaType: 'text/csv',
    sizeBytes: 12,
    sha256: 'a'.repeat(64),
    status: 'ready',
    runId: 'run_created',
    summary: '汇总',
    retentionDays: 30,
    createdAt: '2026-10-24T00:00:00Z',
    expiresAt: '2099-01-01T00:00:00Z',
  });
  if (page.viewportSize()!.width < 768) {
    await reach(page, page.getByRole('button', { name: '打开导航菜单' }));
    await page.keyboard.press('Enter');
  }
  await reach(page, page.getByRole('link', { name: '产物', exact: true }));
  await page.keyboard.press('Enter');
  const row = page.getByRole('row').filter({ hasText: 'summary.csv' });
  await expect(row).toBeVisible();
  await reach(page, row.getByRole('button', { name: '查看产物' }));
  await page.keyboard.press('Enter');
  await reach(page, page.getByRole('button', { name: '下载产物' }));
  const download = page.waitForEvent('download');
  await page.keyboard.press('Enter');
  expect((await download).suggestedFilename()).toBe('summary.csv');
  await page.keyboard.press('Escape');
  await expect(row.getByRole('button', { name: '查看产物' })).toBeFocused();
});

test('tabs support arrows, Home and End with one tab stop', async ({ page }) => {
  await page.goto('/#/runs/run_server');
  const events = page.getByRole('tab', { name: '执行时间线' });
  const tools = page.getByRole('tab', { name: '工具调用树' });
  await expect(events).toBeVisible();
  await reach(page, events);
  await page.keyboard.press('ArrowRight');
  await expect(tools).toBeFocused();
  await expect(tools).toHaveAttribute('aria-selected', 'true');
  await expect(events).toHaveAttribute('tabindex', '-1');
  await page.keyboard.press('Home');
  await expect(events).toBeFocused();
  await page.keyboard.press('End');
  await expect(tools).toBeFocused();
});
