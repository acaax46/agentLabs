import { test, expect } from '@playwright/test';
import type { Agent, Artifact, Key, ModelOption, Page, Project, Run } from '../../src/platform/api';

test('real ownership, revocation and cancellation preserve backend facts', async ({
  page,
  playwright,
}, testInfo) => {
  const login = await page.request.post('/api/v1/auth/login', {
    headers: { 'X-AgentLab-Request': 'auth' },
    data: { username: 'admin', password: 'AgentLab-Admin-2026!' },
  });
  expect(login.ok()).toBe(true);
  const headers = { 'X-AgentLab-Request': 'platform' };
  const projects = (await (
    await page.request.get('/api/v1/platform/projects?all=true&limit=100')
  ).json()) as Page<Project>;
  let project: Project | undefined;
  let successful: Run | undefined;
  for (const candidate of projects.items) {
    const runs = (await (
      await page.request.get(`/api/v1/platform/runs?projectId=${candidate.id}`)
    ).json()) as Page<Run>;
    successful = runs.items.find(
      (run) => run.status === 'succeeded' && run.model.source === 'platform',
    );
    if (successful) {
      project = candidate;
      break;
    }
  }
  if (!project || !successful)
    throw new Error('请先完成真实主链路验收，权限检查不会用虚构资源代替。');
  const foreign = await page.request.post('/api/v1/platform/projects', {
    headers: { ...headers, 'Idempotency-Key': crypto.randomUUID() },
    data: {
      name: 'P0 权限隔离验收',
      environment: 'staging',
      currency: project.currency,
      budgetMicros: '1000000',
    },
  });
  const foreignProject = (await foreign.json()) as Project;
  const issued = await page.request.post('/api/v1/platform/api-keys', {
    headers: { ...headers, 'Idempotency-Key': crypto.randomUUID() },
    data: {
      projectId: foreignProject.id,
      name: 'P0 permission check',
      scopes: ['runs:read', 'artifacts:read'],
    },
  });
  if (!issued.ok()) throw new Error(`验收密钥创建失败：${issued.status()}`);
  const key = (await issued.json()) as Key & { secret: string };
  const external = await playwright.request.newContext({
    baseURL: 'http://127.0.0.1:18879',
    extraHTTPHeaders: { Authorization: `Bearer ${key.secret}` },
  });
  const files = (await (
    await page.request.get(
      `/api/v1/platform/artifacts?projectId=${project.id}&runId=${successful.id}`,
    )
  ).json()) as Page<Artifact>;
  const deniedRun = await external.get(`/v1/runs/${successful.id}`);
  const deniedFile = await external.get(`/v1/artifacts/${files.items[0]!.id}`);
  const missing = await external.get('/v1/runs/run_p0_missing');
  expect(deniedRun.status()).toBe(404);
  expect(deniedFile.status()).toBe(404);
  expect(missing.status()).toBe(404);
  expect((await deniedRun.json()).message).toBe((await missing.json()).message);
  const revoked = await page.request.post(`/api/v1/platform/api-keys/${key.id}/revoke`, {
    headers,
    data: {},
  });
  expect(revoked.ok()).toBe(true);
  expect((await external.get('/v1/me')).status()).toBe(401);
  await external.dispose();
  const models = (await (
    await page.request.get(`/api/v1/platform/models?projectId=${project.id}`)
  ).json()) as { items: ModelOption[] };
  const model = models.items.find((item) => item.source === 'platform' && item.usable)!;
  const agents = (await (await page.request.get('/api/v1/platform/agents')).json()) as {
    items: Agent[];
  };
  const response = await page.request.post('/api/v1/platform/runs', {
    headers: { ...headers, 'Idempotency-Key': crypto.randomUUID() },
    data: {
      projectId: project.id,
      agentVersionId: agents.items.find((item) => item.id === 'agt_file_analysis')!.latestVersionId,
      modelConnectionId: model.connectionId,
      model: model.model,
      input: {
        instruction: '请只返回符合发布 schema 的简短结果，不调用工具。',
        artifactIds: successful.inputArtifactIds,
      },
      limits: {
        currency: project.currency,
        maxCostMicros: '100000',
        maxExecutionSeconds: 60,
        maxToolCalls: 1,
      },
    },
  });
  if (!response.ok()) throw new Error(`取消验收 Run 创建失败：${response.status()}`);
  const run = (await response.json()) as Run;
  const cancel = await page.request.post(`/api/v1/platform/runs/${run.id}/cancel`, {
    headers,
    data: {},
  });
  expect(cancel.ok()).toBe(true);
  await expect
    .poll(
      async () =>
        ((await (await page.request.get(`/api/v1/platform/runs/${run.id}`)).json()) as Run).status,
      { timeout: 90_000, intervals: [1000, 3000] },
    )
    .toBe('cancelled');
  await page.goto(`/#/runs/${run.id}`);
  await expect(page.getByRole('status').filter({ hasText: '运行状态：已停止' })).toHaveCount(1);
  await expect(
    page.getByRole('heading', { name: '费用拆项与预算变动', exact: true }),
  ).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('cancelled-run.png'), fullPage: true });
});
