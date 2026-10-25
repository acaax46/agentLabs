import { test, expect, agent } from './fixtures';
import type { Agent, HarnessStatus, Price } from '../../src/platform/api';

const now = '2026-10-25T00:00:00Z';
const base: Price = {
  id: 'prc_hosted',
  kind: 'execution',
  name: 'hosted',
  currency: 'USD',
  inputMicros: '0',
  cachedInputMicros: '0',
  outputMicros: '0',
  perSecondMicros: '10',
  perUnitMicros: '0',
  sandboxBilling: 'separate',
  note: '平台预设托管服务价。',
  effectiveFrom: now,
};
const sandbox = (id: string, perSecondMicros: string): Price => ({
  ...base,
  id: `prc_${id}`,
  kind: 'sandbox',
  name: id,
  resourceProfile: id,
  perSecondMicros,
  sandboxBilling: undefined,
});
const rates = {
  execution: base,
  resources: [
    {
      id: 'cpu-1-1',
      name: '基础',
      cpu: 1,
      memoryMB: 1024,
      gpu: 0,
      available: true,
      price: sandbox('cpu-1-1', '25'),
    },
    {
      id: 'cpu-2-4',
      name: '标准',
      cpu: 2,
      memoryMB: 4096,
      gpu: 0,
      available: true,
      price: sandbox('cpu-2-4', '60'),
    },
    {
      id: 'gpu-rtx3070-laptop-4-8',
      name: 'GPU · RTX 3070 Laptop（本机开发卡）',
      cpu: 4,
      memoryMB: 8192,
      gpu: 1,
      gpuType: 'NVIDIA GeForce RTX 3070 Laptop GPU 8 GiB',
      gpuModel: 'RTX 3070 Laptop',
      available: true,
      price: sandbox('gpu-rtx3070-laptop-4-8', '240'),
    },
    {
      id: 'gpu-l4-8-32',
      name: 'GPU · L4',
      cpu: 8,
      memoryMB: 32768,
      gpu: 1,
      gpuType: 'NVIDIA L4 24 GiB',
      gpuModel: 'L4',
      available: false,
      reason: '当前执行节点没有通过检查的 NVIDIA L4 24 GiB。',
      price: sandbox('gpu-l4-8-32', '720'),
    },
  ],
};
const engines: HarnessStatus[] = [
  {
    id: 'builtin',
    name: 'AgentLab 内置循环',
    version: '1',
    protocol: 'chat',
    description: '平台在执行域外驱动模型。',
    available: true,
  },
  {
    id: 'pi',
    name: 'pi',
    version: '1.4.0',
    protocol: 'chat',
    description: 'pi coding agent。',
    available: true,
  },
  {
    id: 'opencode',
    name: 'OpenCode',
    version: '1.19.4',
    protocol: 'chat',
    description: 'OpenCode。',
    available: true,
  },
  {
    id: 'codex',
    name: 'Codex CLI',
    version: '0.164.0',
    protocol: 'responses',
    description: 'Codex CLI。',
    available: true,
  },
  {
    id: 'claude-code',
    name: 'Claude Code',
    version: '2.1.309',
    protocol: 'messages',
    description: 'Claude Code。',
    available: false,
    reason: '此部署没有可用的代码 Agent 沙箱镜像。',
  },
];
const version = agent.latestVersion!;
const codeAgent: Agent = {
  ...agent,
  id: 'agt_code_agent',
  name: '代码 Agent（可选引擎）',
  latestVersionId: 'agv_code',
  latestVersion: {
    ...version,
    id: 'agv_code',
    agentId: 'agt_code_agent',
    tools: [],
    inputFormats: ['csv'],
    sandbox: 'agents-v1',
    harnesses: ['pi', 'opencode', 'codex', 'claude-code'],
    resourceProfiles: ['cpu-2-4', 'cpu-1-1'],
  },
};
const gpuAgent: Agent = {
  ...agent,
  id: 'agt_gpu_compute',
  name: 'GPU 计算 Agent',
  latestVersionId: 'agv_gpu',
  latestVersion: {
    ...version,
    id: 'agv_gpu',
    agentId: 'agt_gpu_compute',
    tools: [{ name: 'code.execute', version: '1.0.0' }],
    sandbox: 'python-cuda-v1',
    resourceProfiles: ['gpu-rtx3070-laptop-4-8', 'gpu-l4-8-32'],
  },
};

test.beforeEach(async ({ page, platform }) => {
  platform.readiness.capabilities.codeExecution = true;
  delete platform.readiness.capabilities.codeExecutionReason;
  platform.readiness.capabilities.harnesses = engines;
  for (const item of [codeAgent, gpuAgent]) {
    await page.route(`**/api/v1/platform/agents/${item.id}`, (route) =>
      route.fulfill({ json: { agent: item, versions: [item.latestVersion] } }),
    );
  }
  await page.route('**/api/v1/platform/billing/rates?*', (route) => route.fulfill({ json: rates }));
});

test('代码 Agent 可选执行引擎，不可用的引擎写明原因，提交冻结所选引擎', async ({
  page,
  platform,
}) => {
  await page.goto('/#/agents/agt_code_agent');
  const engine = page.getByLabel('执行引擎');
  await expect(engine).toHaveValue('pi');
  await expect(engine.locator('option')).toHaveCount(4);
  const claude = engine.locator('option[value="claude-code"]');
  await expect(claude).toHaveAttribute('disabled', '');
  await expect(claude).toContainText('此部署没有可用的代码 Agent 沙箱镜像');
  await expect(engine.locator('option[value="codex"]')).toContainText('OpenAI Responses');
  await expect(page.getByText(/模型请求只经平台计量通道/)).toBeVisible();
  await engine.selectOption('codex');
  // Only the version's own profiles, in its order: no GPU for a CPU code agent.
  const allocation = page.getByLabel('沙箱资源规格');
  await expect(allocation.locator('option')).toHaveCount(2);
  await expect(allocation).toHaveValue('cpu-2-4');
  await expect(page.getByText(/会话期间容器一直占用所选规格/)).toBeVisible();
  await page.getByLabel('任务指令', { exact: true }).fill('汇总订单');
  await page.getByLabel('上传输入文件').setInputFiles({
    name: 'orders.csv',
    mimeType: 'text/csv',
    buffer: Buffer.from('region,amount\neast,10\n'),
  });
  await expect(page.getByText('服务端摘要校验通过', { exact: false })).toBeVisible();
  await page.getByRole('checkbox', { name: /我确认预占预算/ }).check();
  await page.getByRole('button', { name: '提交试运行', exact: true }).click();
  await expect(page).toHaveURL(/runs\/run_created/);
  const submitted = platform.requests.find(
    (request) => request.path === '/runs' && request.method === 'POST',
  );
  expect(submitted?.body).toMatchObject({
    harness: 'codex',
    resourceProfile: 'cpu-2-4',
    agentVersionId: 'agv_code',
  });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
});

test('GPU Agent 只提供 GPU 规格，内置引擎写明固定', async ({ page }) => {
  await page.goto('/#/agents/agt_gpu_compute');
  const allocation = page.getByLabel('沙箱资源规格');
  await expect(allocation.locator('option')).toHaveCount(2);
  await expect(allocation).toHaveValue('gpu-rtx3070-laptop-4-8');
  const l4 = allocation.locator('option[value="gpu-l4-8-32"]');
  await expect(l4).toHaveAttribute('disabled', '');
  await expect(l4).toContainText('没有通过检查的 NVIDIA L4');
  await expect(page.getByText(/此版本只能使用 GPU 规格/)).toBeVisible();
  const engine = page.getByLabel('执行引擎');
  await expect(engine).toBeDisabled();
  await expect(engine).toContainText('此版本固定');
  await expect(page.getByRole('link', { name: '代码 Agent（可选引擎）' })).toHaveAttribute(
    'href',
    '#/agents/agt_code_agent',
  );
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
});

test('CPU 版本不提供 GPU 规格', async ({ page }) => {
  await page.goto('/#/agents/agent_server');
  const allocation = page.getByLabel('沙箱资源规格');
  await expect(allocation.locator('option')).toHaveCount(2);
  await expect(allocation.locator('option[value="gpu-rtx3070-laptop-4-8"]')).toHaveCount(0);
  await expect(page.getByText('此版本只使用不含 GPU 的规格', { exact: false })).toBeVisible();
});
