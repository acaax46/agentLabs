import { test, expect } from '@playwright/test';
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { resolve } from 'node:path';

test('native Chromium tab zoom at 200% preserves current workbench controls', async ({
  playwright,
}, testInfo) => {
  test.setTimeout(90_000);
  const parent = resolve('../.cache/native-zoom');
  await mkdir(parent, { recursive: true });
  const work = await mkdtemp(`${parent}/run-`);
  if (!work.startsWith(`${parent}/`) && !work.startsWith(`${parent}\\`))
    throw new Error('临时浏览器路径超出本次验证目录');
  const extension = resolve(work, 'extension');
  await mkdir(extension);
  await writeFile(
    resolve(extension, 'manifest.json'),
    JSON.stringify({
      manifest_version: 3,
      name: 'AgentLab native zoom verification',
      version: '1.0',
      permissions: ['tabs'],
      background: { service_worker: 'background.js' },
    }),
  );
  await writeFile(
    resolve(extension, 'background.js'),
    `chrome.runtime.onInstalled.addListener(() => {});`,
  );
  const context = await playwright.chromium.launchPersistentContext(resolve(work, 'profile'), {
    executablePath: process.env.AGENTLAB_ZOOM_BROWSER_PATH || playwright.chromium.executablePath(),
    headless: true,
    viewport: { width: 1440, height: 1000 },
    args: [`--disable-extensions-except=${extension}`, `--load-extension=${extension}`],
  });
  try {
    const baseURL = String(testInfo.project.use.baseURL);
    const page = context.pages()[0] ?? (await context.newPage());
    await page.goto(`${baseURL}/#/login`);
    const worker = context.serviceWorkers()[0] ?? (await context.waitForEvent('serviceworker'));
    await expect
      .poll(() =>
        worker.evaluate(async () => {
          const api = (
            globalThis as unknown as {
              chrome: {
                tabs: {
                  query(options: object): Promise<{ id: number; url: string }[]>;
                  getZoom(id: number): Promise<number>;
                  setZoom(id: number, factor: number): Promise<void>;
                };
              };
            }
          ).chrome;
          const tab = (await api.tabs.query({})).find((item) =>
            item.url.startsWith('http://127.0.0.1:'),
          );
          if (!tab) return 0;
          await api.tabs.setZoom(tab.id, 2);
          return api.tabs.getZoom(tab.id);
        }),
      )
      .toBe(2);
    await page.getByLabel('用户名', { exact: true }).fill('admin');
    await page.getByLabel('密码', { exact: true }).fill('AgentLab-Admin-2026!');
    await expect(page.getByLabel('用户名', { exact: true })).toHaveValue('admin');
    await page.getByLabel('密码', { exact: true }).press('Enter');
    await expect(page).toHaveURL(/overview/);
    await expect(page.locator('h1')).toBeVisible();
    for (const route of [
      'projects?new=1',
      'agents',
      'runs',
      'usage',
      'keys',
      'artifacts',
      'settings',
    ]) {
      await page.goto(`${baseURL}/#/${route}`);
      await expect(page.locator('h1')).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      // Real browser zoom changes the CSS viewport, not just physical pixel density.
      expect(await page.evaluate(() => innerWidth)).toBeLessThanOrEqual(720);
      if (route === 'projects?new=1') {
        await page.getByLabel('项目名称', { exact: true }).fill('原生 200% 缩放');
        await expect(
          page.getByRole('dialog').getByRole('button', { name: '创建项目', exact: true }),
        ).toBeEnabled();
        await page.keyboard.press('Escape');
      }
    }
    await page.screenshot({ path: testInfo.outputPath('native-zoom-200.png'), fullPage: true });
  } finally {
    await context.close();
    await rm(work, { recursive: true, force: true });
  }
});
