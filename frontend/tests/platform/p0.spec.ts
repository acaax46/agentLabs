import { test, expect, project } from './fixtures';
import type { Locator, Page } from '@playwright/test';

async function tabTo(page: Page, target: Locator) {
  for (let step = 0; step < 160; step++) {
    if (await target.evaluate((element) => element === document.activeElement).catch(() => false))
      return;
    await page.keyboard.press('Tab');
  }
  throw new Error('键盘 Tab 无法到达目标控件。');
}

test('能力控制导航与直接访问，减少动效关闭加载旋转', async ({ page, platform }) => {
  platform.readiness.capabilities.modules = { tools: false };
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto('/#/tools');
  await expect(page.getByRole('heading', { name: '未开放', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: '工具', exact: true })).toHaveCount(0);
  const motion = await page.evaluate(() => {
    const node = document.createElement('span');
    node.className = 'wb-spinner';
    document.body.append(node);
    const value = getComputedStyle(node).animationName;
    node.remove();
    return value;
  });
  expect(motion).toBe('none');
});

test('字段错误关联输入并显示请求编号', async ({ page }) => {
  await page.route('**/api/v1/platform/projects', (route) =>
    route.request().method() === 'POST'
      ? route.fulfill({
          status: 422,
          contentType: 'application/json',
          body: JSON.stringify({
            code: 'INVALID_FIELDS',
            message: '请检查项目名称',
            fields: { name: '名称已存在' },
            requestId: 'p0-request-id',
          }),
        })
      : route.fallback(),
  );
  await page.goto('/#/projects?new=1');
  await page.getByRole('dialog').getByLabel('项目名称', { exact: true }).fill('重复项目');
  await page.getByRole('dialog').getByRole('button', { name: '创建项目', exact: true }).click();
  const input = page.getByRole('dialog').getByLabel('项目名称', { exact: true });
  await expect(input).toHaveAttribute('aria-invalid', 'true');
  await expect(page.getByText('请求 ID：p0-request-id')).toBeVisible();
  const errorId = await input.getAttribute('aria-describedby');
  expect(await page.locator(`[id="${errorId}"]`).textContent()).toBe('名称已存在');
});

test('预算达到80%警示且小额可展开精确值', async ({ page, platform }) => {
  platform.projects[0]!.budget.limitMicros = '1000000';
  platform.projects[0]!.budget.spentMicros = '800000';
  platform.projects[0]!.budget.heldMicros = '1';
  platform.projects[0]!.budget.availableMicros = '199999';
  await page.goto(`/#/projects/${project.id}`);
  await expect(
    page.getByText('预算已使用或预占达到 80%，请核对剩余额度。', { exact: true }),
  ).toBeVisible();
  const amount = page.locator('.wb-money').filter({ hasText: '$< 0.01 USD' }).first();
  await amount.locator('summary').click();
  await expect(amount.getByText('$0.000001 USD', { exact: true })).toBeVisible();
});

test('事件中断保留已读日志、自动重连并剥离控制序列', async ({ page }) => {
  let disconnected = false;
  await page.route('**/api/v1/platform/runs/run_server/events*', (route) => {
    const after = Number(new URL(route.request().url()).searchParams.get('after') ?? 0);
    if (after === 1 && !disconnected) {
      disconnected = true;
      return route.abort('failed');
    }
    return route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        items:
          after === 0
            ? [
                {
                  id: 'event-1',
                  seq: 1,
                  at: '2026-10-24T00:00:00Z',
                  level: 'info',
                  source: 'worker',
                  type: 'run.started',
                  message: '\u001b[31m真实事件\u001b[0m',
                },
              ]
            : after === 1
              ? [
                  {
                    id: 'event-2',
                    seq: 2,
                    at: '2026-10-24T00:00:01Z',
                    level: 'info',
                    source: 'worker',
                    type: 'run.progress',
                    message: '恢复后的事件',
                  },
                ]
              : [],
        cursor: after ? 2 : 1,
        terminal: false,
        status: 'running',
      }),
    });
  });
  await page.goto('/#/runs/run_server');
  await expect(page.locator('.wb-event-log pre').filter({ hasText: '真实事件' })).toBeVisible();
  await expect(page.getByText('重新连接中，恢复后将从最后游标补齐。', { exact: true })).toBeVisible(
    { timeout: 10_000 },
  );
  await expect(page.getByText('恢复后的事件', { exact: true })).toBeVisible({ timeout: 10_000 });
  expect(await page.locator('.wb-event-log').textContent()).not.toContain('\u001b');
  await expect(page.locator('.wb-event-log pre').filter({ hasText: '真实事件' })).toHaveCount(1);
});

test('上传格式错误逐文件提示，工具次数使用键盘设置并发送', async ({ page, platform }) => {
  await page.goto('/#/agents/agent_server');
  await page.getByLabel('上传输入文件').setInputFiles({
    name: 'unsafe.exe',
    mimeType: 'application/octet-stream',
    buffer: Buffer.from('invalid'),
  });
  await expect(
    page.getByText('文件格式不在当前发布版本允许范围内。', { exact: true }).first(),
  ).toBeVisible();
  await page.getByLabel('任务指令', { exact: true }).focus();
  await page.keyboard.insertText('分析输入');
  await page.getByLabel('工具调用次数', { exact: true }).focus();
  await page.keyboard.press('ControlOrMeta+A');
  await page.keyboard.insertText('2');
  await page.getByRole('checkbox', { name: /我确认预占预算/ }).focus();
  await page.keyboard.press('Space');
  await page.getByRole('button', { name: '提交试运行', exact: true }).focus();
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/runs\/run_created/);
  expect(
    platform.requests.find((request) => request.path === '/runs' && request.method === 'POST')?.body
      .limits,
  ).toMatchObject({ maxToolCalls: 2 });
});

test('Markdown远程资源与脚本在无权限预览中被阻止', async ({ page, platform }) => {
  platform.files.push({
    id: 'md_p0',
    projectId: project.id,
    name: 'report.md',
    type: 'md',
    mediaType: 'text/markdown',
    kind: 'output',
    status: 'ready',
    runId: 'run_server',
    sizeBytes: 200,
    sha256: 'a'.repeat(64),
    summary: 'Markdown',
    retentionDays: 30,
    createdAt: '2026-10-24T00:00:00Z',
    expiresAt: '2099-01-01T00:00:00Z',
  });
  const remote: string[] = [];
  page.on('request', (request) => {
    if (request.url().includes('blocked.example')) remote.push(request.url());
  });
  await page.route('**/artifacts/md_p0/preview', (route) =>
    route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        type: 'md',
        name: 'report.md',
        text: '# 安全预览\n![外部图片](https://blocked.example/image.png)\n<script>window.parent.p0Attack=true</script>',
        truncated: false,
      }),
    }),
  );
  await page.goto('/#/artifacts');
  await page.getByRole('button', { name: '查看产物', exact: true }).click();
  const iframe = page.locator('iframe');
  await expect(iframe).toHaveAttribute('sandbox', '');
  await expect(
    page.frameLocator('iframe').getByRole('heading', { name: '安全预览', exact: true }),
  ).toBeVisible();
  expect(remote).toEqual([]);
  expect(await page.evaluate(() => 'p0Attack' in window)).toBe(false);
});

test('仅键盘完成创建、提交、下载与密钥保存确认', async ({ page, platform }) => {
  test.setTimeout(90_000);
  await page.goto('/#/projects?new=1');
  const name = page.getByRole('dialog').getByLabel('项目名称', { exact: true });
  await expect(name).toBeVisible();
  await tabTo(page, name);
  await page.keyboard.insertText('键盘项目');
  const create = page.getByRole('dialog').getByRole('button', { name: '创建项目', exact: true });
  await tabTo(page, create);
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/projects\/proj_created/);
  await page.goto('/#/agents/agent_server');
  const instruction = page.getByLabel('任务指令', { exact: true });
  await expect(instruction).toBeVisible();
  await tabTo(page, instruction);
  await page.keyboard.insertText('键盘发起分析');
  const consent = page.getByRole('checkbox', { name: /我确认预占预算/ });
  await tabTo(page, consent);
  await page.keyboard.press('Space');
  const submit = page.getByRole('button', { name: '提交试运行', exact: true });
  await tabTo(page, submit);
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/runs\/run_created/);
  platform.files.push({
    id: 'keyboard_output',
    projectId: 'proj_created',
    kind: 'output',
    name: 'keyboard.csv',
    type: 'csv',
    mediaType: 'text/csv',
    sizeBytes: 12,
    sha256: 'a'.repeat(64),
    status: 'ready',
    runId: 'run_created',
    summary: '键盘领取产物',
    retentionDays: 30,
    createdAt: '2026-10-24T00:00:00Z',
    expiresAt: '2099-01-01T00:00:00Z',
  });
  await page.goto('/#/artifacts');
  const view = page.getByRole('button', { name: '查看产物', exact: true });
  await expect(view).toBeVisible();
  await tabTo(page, view);
  await page.keyboard.press('Enter');
  const downloadButton = page.getByRole('button', { name: '下载产物', exact: true });
  await tabTo(page, downloadButton);
  const download = page.waitForEvent('download');
  await page.keyboard.press('Enter');
  expect((await download).suggestedFilename()).toBe('keyboard.csv');
  await page.keyboard.press('Escape');
  await page.goto('/#/keys');
  const newKey = page.getByRole('button', { name: '创建 API Key', exact: true });
  await expect(newKey).toBeVisible();
  await tabTo(page, newKey);
  await page.keyboard.press('Enter');
  const keyName = page.getByLabel('密钥名称', { exact: true });
  await tabTo(page, keyName);
  await page.keyboard.insertText('键盘密钥');
  const issue = page.getByRole('button', { name: '创建密钥', exact: true });
  await tabTo(page, issue);
  await page.keyboard.press('Enter');
  const saved = page.getByRole('checkbox', {
    name: '我确认已将完整密钥保存到安全位置',
    exact: true,
  });
  await expect(saved).toBeVisible();
  await tabTo(page, saved);
  await page.keyboard.press('Space');
  const acknowledge = page.getByRole('button', { name: '我已保存', exact: true });
  await tabTo(page, acknowledge);
  await page.keyboard.press('Enter');
  await expect(page.getByRole('dialog')).toHaveCount(0);
});
