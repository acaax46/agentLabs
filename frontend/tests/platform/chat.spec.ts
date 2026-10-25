import { test, expect, project, run } from './fixtures';
import type { Artifact, ConversationDetail, Run, RunEvent } from '../../src/platform/api';

const now = '2026-10-25T12:00:00Z';
const output: Artifact = {
  id: 'chat_output',
  projectId: project.id,
  kind: 'output',
  name: 'summary.csv',
  type: 'csv',
  mediaType: 'text/csv',
  sizeBytes: 20,
  sha256: 'a'.repeat(64),
  status: 'ready',
  runId: 'chat_run_1',
  summary: '按地区汇总',
  retentionDays: 30,
  createdAt: now,
  expiresAt: '2099-12-01T00:00:00Z',
};

test('对话选沙箱、上传、流式回复、刷新续聊和产物预览', async ({ page, platform }, testInfo) => {
  platform.readiness.capabilities.codeExecution = true;
  const captures: { path: string; body: Record<string, unknown>; key?: string }[] = [];
  let phase = 0;
  const chat: ConversationDetail = {
    id: 'chat_server',
    projectId: project.id,
    title: '文件分析对话',
    agentVersionId: 'av_server_1',
    modelConnectionId: 'conn_server',
    model: 'test-model',
    resourceProfile: 'cpu-2-4',
    sandboxImage: 'sha256:test',
    harness: { id: 'builtin', name: 'AgentLab 内置循环', version: '1' },
    limits: {
      maxCostMicros: '2000000',
      currency: 'USD',
      maxExecutionSeconds: 120,
      maxToolCalls: 5,
    },
    latestRunId: '',
    turnCount: 0,
    version: 1,
    createdAt: now,
    updatedAt: now,
    runs: [],
  };
  const event = (seq: number, type: string, message: string): RunEvent => ({
    id: `evt_${seq}`,
    seq,
    at: now,
    level: 'info',
    source: type.startsWith('reply') ? 'assistant' : 'worker',
    type,
    message,
    data: { step: 1 },
  });
  const frames = [
    event(1, 'model.requested', '正在分析上传文件'),
    event(2, 'reply.delta', '我已读取订单，'),
    event(3, 'reply.delta', '正在按地区汇总。'),
  ];
  await page.route('**/api/v1/platform/**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname.replace('/api/v1/platform', '');
    const json = (body: unknown, status = 200) =>
      route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
    if (path === '/billing/rates')
      return json({
        execution: { sandboxBilling: 'separate' },
        resources: [
          {
            id: 'cpu-1-1',
            name: '基础',
            cpu: 1,
            memoryMB: 1024,
            gpu: 0,
            available: true,
            price: null,
          },
          {
            id: 'cpu-2-4',
            name: '标准',
            cpu: 2,
            memoryMB: 4096,
            gpu: 0,
            available: true,
            price: null,
          },
        ],
      });
    if (path === '/conversations' && request.method() === 'POST') {
      captures.push({
        path,
        body: request.postDataJSON() as Record<string, unknown>,
        key: request.headers()['idempotency-key'],
      });
      return json(chat, 201);
    }
    if (path === '/conversations') return json({ items: captures.length ? [chat] : [] });
    if (path === `/conversations/${chat.id}`) return json(chat);
    if (path === `/conversations/${chat.id}/messages`) {
      const body = request.postDataJSON() as { content: string; artifactIds: string[] };
      captures.push({ path, body, key: request.headers()['idempotency-key'] });
      const current: Run = {
        ...structuredClone(run),
        id: `chat_run_${chat.runs.length + 1}`,
        conversationId: chat.id,
        conversationTurn: chat.runs.length + 1,
        instruction: body.content,
        inputArtifactIds: body.artifactIds,
        status: 'running',
        result: null,
      };
      chat.runs.push(current);
      chat.latestRunId = current.id;
      chat.turnCount++;
      return json(current, 202);
    }
    if (path === '/runs/chat_run_1/events') {
      const after = Number(url.searchParams.get('after') ?? 0);
      if (phase === 0) {
        const body = frames
          .filter((item) => item.seq > after)
          .map((item) => `id: ${item.seq}\nevent: ${item.type}\ndata: ${JSON.stringify(item)}\n\n`)
          .join('');
        return route.fulfill({ contentType: 'text/event-stream', body });
      }
      return route.fulfill({
        contentType: 'text/event-stream',
        body:
          frames
            .map(
              (item) => `id: ${item.seq}\nevent: ${item.type}\ndata: ${JSON.stringify(item)}\n\n`,
            )
            .join('') + 'event: end\ndata: {"status":"succeeded"}\n\n',
      });
    }
    if (path === '/runs/chat_run_2/events')
      return route.fulfill({ contentType: 'text/event-stream', body: ': keep-alive\n\n' });
    if (path === '/artifacts' && url.searchParams.get('runId') === 'chat_run_1')
      return json({ items: phase ? [output] : [] });
    if (path === '/artifacts/chat_output/preview')
      return json({
        type: 'csv',
        name: output.name,
        columns: ['region', 'amount'],
        rows: [['east', '15.75']],
        truncated: false,
      });
    if (path === '/artifacts/file_uploaded') return json(platform.files[0]);
    return route.fallback();
  });
  await page.goto('/#/chat');
  await expect(page.getByRole('heading', { name: '先选好沙箱，再聊你的想法。' })).toBeVisible();
  await page.locator('input[value="cpu-2-4"]').check();
  await page.getByRole('button', { name: '进入对话', exact: true }).click();
  await expect(page.getByRole('heading', { name: '沙箱配置已就绪', exact: true })).toBeVisible();
  expect(captures[0]!.body.resourceProfile).toBe('cpu-2-4');
  expect(captures[0]!.key).toBeTruthy();
  await page.getByLabel('上传对话文件').setInputFiles({
    name: 'orders.csv',
    mimeType: 'text/csv',
    buffer: Buffer.from('region,amount\neast,15.75\n'),
  });
  await expect(page.getByText('已上传', { exact: true })).toBeVisible();
  await page.getByLabel('输入消息', { exact: true }).fill('请按地区汇总订单');
  await page.getByLabel('发送消息', { exact: true }).click();
  await expect(page.getByText('我已读取订单，正在按地区汇总。', { exact: true })).toBeVisible();
  await expect(page.getByLabel('发送消息')).toBeDisabled();
  await expect(page.locator('.wb-chat-progress')).not.toHaveAttribute('open', '');
  expect(captures[1]!.body.artifactIds).toEqual(['file_uploaded']);
  expect(await page.locator('body').textContent()).not.toContain('reasoning_content');
  phase = 1;
  chat.runs[0] = {
    ...chat.runs[0]!,
    status: 'succeeded',
    billingStatus: 'settled',
    costMicros: '1500',
    result: { summary: '已完成地区汇总，可以继续调整。', findings: [], warnings: [] },
  };
  await expect(page.getByText('已完成地区汇总，可以继续调整。', { exact: true })).toBeVisible({
    timeout: 12000,
  });
  await page.reload();
  await expect(page.getByText('请按地区汇总订单', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: /summary.csv.*点击预览/ }).click();
  await expect(page.getByRole('complementary', { name: '产物预览', exact: true })).toBeVisible();
  await expect(page.getByRole('cell', { name: '15.75', exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('conversation-preview.png'), fullPage: true });
  await page.keyboard.press('Escape');
  await expect(page.getByRole('complementary', { name: '产物预览', exact: true })).toHaveCount(0);
  await page.getByLabel('输入消息', { exact: true }).fill('保留上轮结果，把金额改为美元');
  await page.getByLabel('输入消息', { exact: true }).press('Enter');
  await expect(page.getByText('保留上轮结果，把金额改为美元', { exact: true })).toBeVisible();
  expect(captures.at(-1)?.body.artifactIds).toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath('conversation.png'), fullPage: true });
});

test('对话服务失败保持错误状态，不生成本地会话', async ({ page, platform }) => {
  platform.readiness.capabilities.codeExecution = true;
  await page.route('**/api/v1/platform/conversations/missing', (route) =>
    route.fulfill({
      status: 404,
      contentType: 'application/json',
      body: JSON.stringify({ code: 'NOT_FOUND', message: '未找到或无权访问' }),
    }),
  );
  await page.goto('/#/chat/missing');
  await expect(page.getByRole('heading', { name: '未找到或无权访问', exact: true })).toBeVisible();
  await expect(page.getByLabel('输入消息', { exact: true })).toHaveCount(0);
});

test('HTML 产物预览阻止脚本、远程资源和父页面访问', async ({ page, platform }) => {
  platform.files.push({
    ...output,
    id: 'chat_html',
    name: 'dashboard.html',
    type: 'html',
    mediaType: 'text/html',
  });
  const remote: string[] = [];
  page.on('request', (request) => {
    if (request.url().includes('chat-preview-remote.example')) remote.push(request.url());
  });
  await page.route('**/artifacts/chat_html/preview', (route) =>
    route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        type: 'text',
        name: 'dashboard.html',
        text: '<style>body{background:#10151e;color:white}</style><h1>订单看板</h1><script>parent.chatEscape=true</script><img src="https://chat-preview-remote.example/pixel"><a href="https://chat-preview-remote.example" onclick="parent.chatEscape=true">外链</a>',
        truncated: false,
      }),
    }),
  );
  await page.goto('/#/artifacts');
  await page.getByRole('button', { name: '查看产物', exact: true }).click();
  const frame = page.getByTitle('dashboard.html HTML 预览');
  await expect(frame).toHaveAttribute('sandbox', '');
  await expect(
    page.frameLocator('iframe').getByRole('heading', { name: '订单看板', exact: true }),
  ).toBeVisible();
  expect(remote).toEqual([]);
  expect(await page.evaluate(() => 'chatEscape' in window)).toBe(false);
  expect(await page.frameLocator('iframe').locator('script,[onclick],a[href]').count()).toBe(0);
});
