import { test, expect } from '@playwright/test';
import type { APIRequestContext } from '@playwright/test';
import { readFile, writeFile } from 'node:fs/promises';
import type { Agent, ModelOption, Run, Artifact, Project } from '../../src/platform/api';

const root = 'http://127.0.0.1:18879';
const stamp = Date.now();
const cases = [
  { name: 'desktop', width: 1440, height: 1000 },
  { name: 'tablet', width: 1024, height: 900 },
  { name: 'mobile', width: 390, height: 844 },
  { name: 'zoom-200', width: 720, height: 500 },
];
async function json<T>(client: APIRequestContext, path: string): Promise<T> {
  const response = await client.get(path);
  if (!response.ok()) throw new Error(`验收读取失败：${response.status()} ${path}`);
  return response.json() as Promise<T>;
}
for (const size of cases) {
  test(`real platform flow ${size.name}`, async ({ page, playwright }, testInfo) => {
    await page.setViewportSize(size);
    if (size.name === 'zoom-200') {
      const cdp = await page.context().newCDPSession(page);
      await cdp.send('Emulation.setDeviceMetricsOverride', {
        width: 720,
        height: 500,
        deviceScaleFactor: 2,
        mobile: false,
      });
    }
    await page.goto('/#/login');
    await page.getByLabel('用户名', { exact: true }).fill('admin');
    await page.getByLabel('密码', { exact: true }).fill('AgentLab-Admin-2026!');
    await page.getByRole('button', { name: '登录工作台', exact: true }).click();
    await expect(page).toHaveURL(/overview/);
    const capability = await json<{
      ready: boolean;
      capabilities: { codeExecution: boolean; isolation: string };
    }>(page.request, '/api/v1/platform/capabilities');
    expect(capability.ready && capability.capabilities.codeExecution).toBe(true);
    expect(capability.capabilities.isolation).toBe('docker/container');
    await page.goto('/#/projects?new=1');
    await page
      .getByRole('dialog')
      .getByLabel(/项目名称/)
      .fill(`P0 ${size.name} ${stamp}`);
    await page
      .getByRole('dialog')
      .getByLabel('用途说明')
      .fill('真实模型、真实 Docker 沙箱的独立验收项目');
    await page.getByRole('dialog').getByLabel('项目累计预算', { exact: true }).fill('10');
    const createdProject = page.waitForResponse(
      (response) =>
        response.url().endsWith('/api/v1/platform/projects') &&
        response.request().method() === 'POST',
    );
    await page.getByRole('dialog').getByRole('button', { name: '创建项目', exact: true }).click();
    const project = (await (await createdProject).json()) as Project;
    await expect(page.getByRole('heading', { name: project.name, exact: true })).toBeVisible();
    const models = await json<{ items: ModelOption[] }>(
      page.request,
      `/api/v1/platform/models?projectId=${project.id}`,
    );
    const model = models.items.find((item) => item.source === 'platform' && item.usable);
    if (!model) throw new Error('验收环境没有已验证且已定价的平台模型，不能用模拟响应代替。');
    const agents = await json<{ items: Agent[] }>(page.request, '/api/v1/platform/agents');
    const agent = agents.items[0];
    if (!agent?.latestVersion) throw new Error('未发布文件分析 Agent。');
    await page.goto(`/#/agents/${agent.id}`);
    await page.getByLabel('上传输入文件').setInputFiles({
      name: 'orders.csv',
      mimeType: 'text/csv',
      buffer: Buffer.from('region,amount\neast,10\neast,5\nwest,20\n'),
    });
    await expect(page.getByText('服务端摘要校验通过', { exact: false })).toBeVisible();
    await page
      .getByLabel(/任务指令/)
      .fill(
        '请调用 code_execute，在真实沙箱用 pandas 读取 /work/input/orders.csv，按 region 汇总 amount，输出 /work/output/summary.csv，并用 matplotlib 输出 /work/output/summary.png。只需一次代码执行。最后严格按发布版本 schema 返回 summary、findings、outputs、warnings，outputs 列出上述两个文件。',
      );
    await page.getByLabel(/费用上限/).fill('2');
    await page.getByLabel(/工具调用次数/).fill('3');
    await page.getByRole('checkbox', { name: /我确认预占预算/ }).check();
    const submitted = page.waitForResponse(
      (response) =>
        response.url().endsWith('/api/v1/platform/runs') && response.request().method() === 'POST',
    );
    await page.getByRole('button', { name: '提交试运行', exact: true }).click();
    const created = (await (await submitted).json()) as Run;
    if (!created.id) throw new Error('真实运行提交失败，未返回 Run ID。');
    await expect
      .poll(
        async () => (await json<Run>(page.request, `/api/v1/platform/runs/${created.id}`)).status,
        { timeout: 240_000, intervals: [2000, 5000] },
      )
      .toBe('succeeded');
    await expect(page.getByText('已完成', { exact: true }).first()).toBeVisible();
    await page.getByRole('tab', { name: '工具调用树', exact: true }).click();
    await expect(page.getByText('code.execute@', { exact: false })).toBeVisible();
    const files = await json<{ items: Artifact[] }>(
      page.request,
      `/api/v1/platform/artifacts?projectId=${project.id}&runId=${created.id}`,
    );
    expect(files.items.some((file) => file.type === 'csv')).toBe(true);
    expect(files.items.some((file) => file.type === 'png')).toBe(true);
    const png = files.items.find((file) => file.type === 'png')!;
    await page.goto('/#/artifacts');
    const row = page.getByRole('row').filter({ hasText: png.name });
    await row.getByRole('button', { name: '查看产物', exact: true }).click();
    const frame = page.frameLocator('iframe[title$="图片预览"]');
    await expect(frame.getByRole('img')).toBeVisible();
    const download = page.waitForEvent('download');
    await page.getByRole('button', { name: '下载产物', exact: true }).click();
    expect((await download).suggestedFilename()).toBe(png.name);
    await page.getByRole('button', { name: '关闭对话框', exact: true }).click();
    await page.goto('/#/usage');
    await expect(page.getByRole('heading', { name: '分项汇总', exact: true })).toBeVisible();
    await page.getByRole('tab', { name: '只追加账本', exact: true }).click();
    await expect(page.getByRole('cell', { name: '结算', exact: true }).first()).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath(`${size.name}-ledger.png`), fullPage: true });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );

    if (size.name === 'desktop') {
      // Read the existing local provider configuration in memory; never emit it in reports.
      const env: Record<string, string> = {};
      for (const raw of (await readFile('../.env.dev', 'utf8')).split(/\r?\n/)) {
        const line = raw.trim();
        const at = line.indexOf('=');
        if (at > 0 && !line.startsWith('#'))
          env[line.slice(0, at).trim()] = line
            .slice(at + 1)
            .trim()
            .replace(/^['"]|['"]$/g, '');
      }
      await page.goto('/#/connections');
      await page.getByRole('button', { name: '连接模型账户', exact: true }).click();
      await page
        .getByRole('dialog')
        .getByLabel('连接名称', { exact: true })
        .fill(`P0 BYOK ${stamp}`);
      await page.getByRole('dialog').getByLabel('模型服务地址').fill(env.BASE_URL!);
      await page
        .getByRole('dialog')
        .getByLabel(/^API Key/)
        .fill(env.API_KEY!);
      await page.getByRole('dialog').getByLabel('允许使用的模型').fill(model.model);
      await page.getByRole('dialog').getByRole('checkbox').check();
      await page
        .getByRole('dialog')
        .getByRole('button', { name: '保存并验证', exact: true })
        .click();
      await expect(page.getByRole('dialog')).toHaveCount(0);
      const conns = await json<{ id: string; name: string; version: number }[]>(
        page.request,
        '/api/v1/connections',
      );
      const byok = conns.find((connection) => connection.name === `P0 BYOK ${stamp}`)!;
      await page.goto(`/#/projects/${project.id}`);
      await page.getByLabel('添加连接授权').selectOption(byok.id);
      await page.getByRole('button', { name: '授权连接', exact: true }).click();
      await expect(page.getByText(`P0 BYOK ${stamp}`, { exact: true }).first()).toBeVisible();
      await page.goto('/#/keys');
      await page.getByRole('button', { name: '创建 API Key', exact: true }).click();
      await page.getByLabel(/密钥名称/).fill('P0 external caller');
      await page.getByRole('button', { name: '创建密钥', exact: true }).click();
      const secret = await page.locator('.wb-secret-row code').textContent();
      if (!secret) throw new Error('服务没有返回一次性 API Key。');
      await page.locator('.wb-secret-row code').evaluate((element) => {
        element.textContent = '[已安全读取，仅保留内存引用]';
      });
      await page
        .getByRole('checkbox', { name: '我确认已将完整密钥保存到安全位置', exact: true })
        .check();
      await page.getByRole('button', { name: '我已保存', exact: true }).click();
      const external = await playwright.request.newContext({
        baseURL: root,
        extraHTTPHeaders: { Authorization: `Bearer ${secret}` },
      });
      const response = await external.post('/v1/runs', {
        headers: { 'Idempotency-Key': `p0-external-${stamp}` },
        data: {
          projectId: project.id,
          agentVersionId: agent.latestVersionId,
          modelConnectionId: byok.id,
          model: model.model,
          input: {
            instruction:
              '读取 orders.csv，调用 files_transform 输出按 region 汇总的文件 summary-api.csv，最后返回符合发布 schema 的结果。',
            artifactIds: created.inputArtifactIds,
          },
          limits: {
            maxCostMicros: '2000000',
            currency: 'CNY',
            maxExecutionSeconds: 120,
            maxToolCalls: 3,
          },
        },
      });
      if (!response.ok()) throw new Error(`外部 API 提交失败：${response.status()}`);
      const apiRun = (await response.json()) as Run;
      await expect
        .poll(async () => (await json<Run>(external, `/v1/runs/${apiRun.id}`)).status, {
          timeout: 240_000,
          intervals: [2000, 5000],
        })
        .toBe('succeeded');
      await page.goto('/#/runs?trigger=api');
      await expect(page.getByRole('link', { name: apiRun.id, exact: true })).toBeVisible();
      await expect(
        page.getByRole('cell', { name: 'API · P0 external caller', exact: true }),
      ).toBeVisible();
      const externalUsage = await json<{
        items: { category: string; status: string; amountMicros: string | null }[];
      }>(external, `/v1/usage?projectId=${project.id}&runId=${apiRun.id}`);
      expect(
        externalUsage.items.some(
          (line) =>
            line.category === 'model' &&
            line.status === 'not_applicable' &&
            line.amountMicros === null,
        ),
      ).toBe(true);
      const storedSecret = await page.evaluate(
        (value) => JSON.stringify({ ...localStorage, ...sessionStorage }).includes(value),
        secret,
      );
      expect(storedSecret).toBe(false);
      await external.dispose();
    }
    await writeFile(
      testInfo.outputPath(`${size.name}-evidence.json`),
      JSON.stringify(
        {
          projectId: project.id,
          runId: created.id,
          source: model.source,
          sandbox: capability.capabilities.isolation,
          artifacts: files.items.map((file) => ({
            id: file.id,
            name: file.name,
            sha256: file.sha256,
          })),
          viewport: size,
          note:
            size.name === 'zoom-200'
              ? '1440px viewport at 200% browser-equivalent CSS viewport; native browser zoom verified separately.'
              : undefined,
        },
        null,
        2,
      ),
    );
  });
}
