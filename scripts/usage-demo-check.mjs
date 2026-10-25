import { createRequire } from 'node:module';
import { mkdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
const require = createRequire(new URL('../frontend/package.json', import.meta.url));
const { chromium } = require('@playwright/test');
const root = fileURLToPath(new URL('../.cache/', import.meta.url));
mkdirSync(root, { recursive: true });
const browser = await chromium.launch({
  headless: true,
  ...(process.env.AGENTLAB_BROWSER_PATH
    ? { executablePath: process.env.AGENTLAB_BROWSER_PATH }
    : {}),
});
try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto('http://127.0.0.1:18789');
  await page.getByRole('button', { name: '模拟发送前失败' }).click();
  await page.waitForFunction(() => !document.getElementById('retry').disabled);
  const before = await page.locator('#window').textContent();
  await page.getByRole('button', { name: '在本窗口重试' }).click();
  await page.waitForFunction(() => document.getElementById('status').textContent === '已完成');
  if (before !== (await page.locator('#window').textContent()))
    throw new Error('Retry changed window or operation');
  if ((await page.locator('article').count()) !== 2) throw new Error('Attempt history lost');
  if (!(await page.locator('#ledger').textContent()).includes('$0.001900'))
    throw new Error('Incorrect fixture settlement');
  await page.reload();
  await page.waitForFunction(() => document.querySelectorAll('article').length === 2);
  await page.screenshot({ path: `${root}/usage-demo-desktop.png`, fullPage: true });
  await page.getByRole('button', { name: '模拟流中断' }).click();
  await page.waitForFunction(() => document.getElementById('status').textContent === '结果未知');
  if (!(await page.getByRole('button', { name: '在本窗口重试' }).isDisabled()))
    throw new Error('Unknown result can retry');
  const state = await (await page.request.get('http://127.0.0.1:18789/api/state')).json();
  const denied = await page.request.post('http://127.0.0.1:18789/api/retry', {
    headers: { 'X-AgentLab-Demo': 'fixture' },
    data: { operationId: state.operation.operationId, attemptId: state.attempts.at(-1).attemptId },
  });
  if (denied.status() !== 409 || state.balance.Held < 5000)
    throw new Error('Unknown reservation was released or replayed');
  await page.setViewportSize({ width: 390, height: 844 });
  if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth))
    throw new Error('Mobile overflow');
  await page.screenshot({ path: `${root}/usage-demo-mobile.png`, fullPage: true });
  if (errors.length) throw new Error(errors.join('\n'));
  console.log(
    'Passed: same-window retry, append-only attempts, fixture settlement, refresh recovery, unknown retry denied, budget held, mobile layout.',
  );
} finally {
  await browser.close();
}
