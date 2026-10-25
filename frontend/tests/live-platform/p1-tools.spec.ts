import { test, expect } from '@playwright/test';
import { readFile, writeFile } from 'node:fs/promises';
import type { Artifact, Project, StandaloneCall } from '../../src/platform/api';

for (const width of [1440, 390]) {
  test(`real standalone files tool ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 });
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.goto('/#/login');
    await page.getByLabel('用户名', { exact: true }).fill('admin');
    await page.getByLabel('密码', { exact: true }).fill('AgentLab-Admin-2026!');
    await page.getByRole('button', { name: '登录工作台', exact: true }).click();
    await expect(page).toHaveURL(/overview/);
    await page.goto('/#/projects?new=1');
    const dialog = page.getByRole('dialog');
    await dialog.getByLabel(/项目名称/).fill(`P1 tools ${width} ${Date.now()}`);
    await dialog.getByLabel('用途说明').fill('独立文件工具真实前端验收，无模型推理');
    await dialog.getByLabel('项目累计预算', { exact: true }).fill('10');
    const creating = page.waitForResponse(
      (response) =>
        response.url().endsWith('/api/v1/platform/projects') &&
        response.request().method() === 'POST',
    );
    await dialog.getByRole('button', { name: '创建项目', exact: true }).click();
    const project = (await (await creating).json()) as Project;
    await expect(page.getByRole('heading', { name: project.name, exact: true })).toBeVisible();
    await page.goto('/#/tools?debug=files.transform');
    await expect(page.getByLabel('上传工具输入文件', { exact: true })).toBeEnabled();
    await page.getByLabel('上传工具输入文件', { exact: true }).setInputFiles({
      name: 'orders.csv',
      mimeType: 'text/csv',
      buffer: Buffer.from('region,amount\neast,10\nwest,20\n'),
    });
    await expect(page.getByRole('checkbox', { name: 'orders.csv', exact: true })).toBeChecked();
    await page
      .getByLabel('工具输入 JSON', { exact: true })
      .fill(JSON.stringify({ operation: 'merge', inputs: ['orders.csv'], output: 'merged.csv' }));
    await page.getByRole('checkbox', { name: /我确认按已展示的工具价格/ }).check();
    await page.screenshot({ path: testInfo.outputPath('tool-playground.png') });
    const posting = page.waitForResponse(
      (response) =>
        response.url().endsWith('/tools/files.transform/calls') &&
        response.request().method() === 'POST',
    );
    await page.getByRole('button', { name: '提交工具调用', exact: true }).click();
    const response = await posting;
    expect(response.status()).toBe(202);
    const created = (await response.json()) as StandaloneCall;
    expect(created.id).toMatch(/^tc_/);
    await expect(page).toHaveURL(new RegExp(`tool-calls/${created.id}`));
    let final: StandaloneCall | undefined;
    await expect
      .poll(
        async () => {
          const reading = await page.request.get(`/api/v1/platform/tool-calls/${created.id}`);
          expect(reading.ok()).toBe(true);
          final = (await reading.json()) as StandaloneCall;
          return final.status;
        },
        { timeout: 90_000, intervals: [1000, 2000] },
      )
      .toBe('succeeded');
    expect(final!.billingStatus).toBe('settled');
    expect(final!.costMicros).not.toBeNull();
    expect(final!.artifactIds.length).toBeGreaterThan(0);
    await expect(page.getByRole('button', { name: '停止工具调用', exact: true })).toHaveCount(0);
    await expect(page.locator('.wb-event-log pre').first()).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(
      true,
    );
    await page.screenshot({ path: testInfo.outputPath('tool-result.png') });
    const filesResponse = await page.request.get(
      `/api/v1/platform/artifacts?projectId=${project.id}&runId=${created.id}`,
    );
    expect(filesResponse.ok()).toBe(true);
    const files = ((await filesResponse.json()) as { items: Artifact[] }).items;
    const output = files.find((file) => file.name === 'merged.csv');
    expect(output).toBeDefined();
    await page.goto(`/#/artifacts?project=${project.id}`);
    await page.getByRole('checkbox', { name: '选择产物 merged.csv', exact: true }).check();
    const downloading = page.waitForEvent('download');
    await page.getByRole('button', { name: '批量下载 ZIP（1）', exact: true }).click();
    const download = await downloading;
    await download.saveAs(testInfo.outputPath('real-artifacts.zip'));
    const bytes = await readFile((await download.path())!);
    expect(bytes.includes(Buffer.from('east,10'))).toBe(true);
    await page.goto('/#/tool-calls');
    await expect(page.getByRole('link', { name: created.id, exact: true })).toBeVisible();
    expect(errors).toEqual([]);
    await writeFile(
      testInfo.outputPath('evidence.json'),
      JSON.stringify(
        {
          projectId: project.id,
          toolCallId: created.id,
          status: final!.status,
          billingStatus: final!.billingStatus,
          costMicros: final!.costMicros,
          currency: final!.currency,
          output: { id: output!.id, name: output!.name, sha256: output!.sha256 },
          viewportWidth: width,
          modelInference: false,
        },
        null,
        2,
      ),
    );
  });
}
