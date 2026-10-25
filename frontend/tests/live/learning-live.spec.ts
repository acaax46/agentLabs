import { expect, test } from '../fixtures';
import { setEditorText } from '../editor';
test('real user VM runs samples, grades private tests and retains rejudge history', async ({
  page,
  request,
  browser,
}) => {
  const suffix = Date.now().toString(36).toUpperCase();
  const create = async (kind: string, fields: unknown) => {
    const code = `LIVE-${kind.slice(0, 2)}-${suffix}`;
    const response = await request.post(`/api/v1/${kind}`, {
      headers: { 'X-AgentLab-Request': 'catalog', 'Idempotency-Key': crypto.randomUUID() },
      data: { code, name: code, status: 'draft', description: '真实执行浏览器验证', fields },
    });
    expect(response.status()).toBe(201);
    return response.json();
  };
  const classes = await (await request.get('/api/v1/classes')).json();
  const cl = classes.items.find((c: { code: string }) => c.code === 'DEMO-CLASS');
  expect(cl).toBeTruthy();
  const problem = await create('problems', {
    difficulty: 'easy',
    statement: '读取两个整数并输出其和。',
    timeLimitMs: 30000,
    memoryLimitMb: 256,
    tags: [],
    samples: [{ input: '2 3\n', output: '5\n', explanation: '' }],
  });
  const course = await create('courses', {
    mode: 'classroom',
    templateId: '',
    problemIds: [problem.id],
  });
  const assignment = await create('assignments', {
    classId: cl.id,
    courseId: course.id,
    deadline: '',
    submissionLimit: 3,
  });
  await page.goto(`/#/assignments/${assignment.id}`);
  await page.getByRole('button', { name: '发布任务版本', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await setEditorText(page, '初始 main.py', 'print(sum(map(int,input().split())))\n');
  await dialog.getByRole('button', { name: '新增私有测试包', exact: true }).click();
  await dialog.getByLabel('私有用例 JSON', { exact: true }).fill(
    JSON.stringify([
      { input: '7 11\n', expected: '18\n' },
      { input: '100 -20\n', expected: '80\n' },
    ]),
  );
  await dialog.getByRole('button', { name: '保存测试包', exact: true }).click();
  await expect(dialog.getByLabel('私有用例 JSON', { exact: true })).not.toBeVisible();
  await expect(
    dialog.getByRole('combobox', { name: `题目 ${problem.id} 的私有测试包`, exact: true }),
  ).not.toHaveValue('');
  await dialog.getByRole('button', { name: '确认发布', exact: true }).click();
  await expect(dialog).not.toBeVisible();
  const releases = await (
    await request.get(`/api/v1/learning/releases?assignmentId=${assignment.id}`)
  ).json();
  expect(releases[0].submissionProblemIds).toContain(problem.id);
  const context = await browser.newContext({ baseURL: new URL(page.url()).origin });
  const student = await context.newPage();
  student.on('dialog', (d) => void d.accept());
  try {
    const login = await context.request.post('/api/v1/auth/login', {
      headers: { 'X-AgentLab-Request': 'auth' },
      data: { username: 'student', password: 'AgentLab-Student-2026!' },
    });
    expect(login.status()).toBe(200);
    const opened = await context.request.post(
      `/api/v1/learning/releases/${releases[0].id}/workspace`,
      {
        headers: { 'X-AgentLab-Request': 'learning', 'Content-Type': 'application/json' },
        data: {},
      },
    );
    expect(opened.status()).toBe(200);
    const workspace = await opened.json();
    await student.goto(`/#/workspace/${workspace.id}`);
    await student.bringToFront();
    await expect(student).toHaveURL(/#\/workspace\//);
    const workspaceId = workspace.id;
    await expect(student.getByRole('button', { name: '运行公开测试', exact: true })).toBeEnabled({
      timeout: 180000,
    });
    await student.getByRole('button', { name: '运行公开测试', exact: true }).click();
    await expect
      .poll(
        async () => {
          const rows = await (
            await context.request.get(`/api/v1/learning/workspaces/${workspaceId}/jobs`)
          ).json();
          return rows.some(
            (job: { kind: string; status: string }) =>
              job.kind === 'public_test' && !['queued', 'running'].includes(job.status),
          );
        },
        { timeout: 150000 },
      )
      .toBe(true);
    const publicJobs = await (
      await context.request.get(`/api/v1/learning/workspaces/${workspaceId}/jobs`)
    ).json();
    const publicResult = publicJobs.find((job: { kind: string }) => job.kind === 'public_test');
    expect(
      publicResult?.output,
      JSON.stringify({
        status: publicResult?.status,
        error: publicResult?.error,
        output: publicResult?.output,
      }),
    ).toContain('通过 1 / 1 个公开样例。');
    await expect(student.getByText('通过 1 / 1 个公开样例。', { exact: false })).toBeVisible({
      timeout: 120000,
    });
    await expect(student.getByRole('button', { name: '提交正式评测', exact: true })).toBeEnabled();
    await student.bringToFront();
    const submitResponse = student.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        response.url().endsWith(`/workspaces/${workspaceId}/jobs`),
    );
    await student.getByRole('button', { name: '提交正式评测', exact: true }).click();
    const submissionResponse = await submitResponse;
    expect(submissionResponse.status(), await submissionResponse.text()).toBe(200);
    await expect
      .poll(
        async () => {
          const rows = await (
            await context.request.get(`/api/v1/learning/workspaces/${workspaceId}/jobs`)
          ).json();
          return rows.some(
            (job: { kind: string; status: string }) =>
              job.kind === 'submission' && !['queued', 'running'].includes(job.status),
          );
        },
        { timeout: 150000 },
      )
      .toBe(true);
    const completedJobs = await (
      await context.request.get(`/api/v1/learning/workspaces/${workspaceId}/jobs`)
    ).json();
    const completed = completedJobs.find((job: { kind: string }) => job.kind === 'submission');
    expect(
      completed?.evaluation?.verdict,
      JSON.stringify({
        status: completed?.status,
        error: completed?.error,
        output: completed?.output,
      }),
    ).toBe('accepted');
    await expect(
      student.getByText('正式评测：accepted，得分 100.00 / 100。', { exact: true }),
    ).toBeVisible({ timeout: 120000 });
    const jobs = await (
      await context.request.get(`/api/v1/learning/workspaces/${workspaceId}/jobs`)
    ).json();
    const official = jobs.find((j: { kind: string }) => j.kind === 'submission');
    expect(official.evaluation.studentId).toBeTruthy();
    expect(JSON.stringify(jobs)).not.toContain('7 11');
    expect(JSON.stringify(jobs)).not.toContain('100 -20');
    const records = await (
      await request.get(`/api/v1/evaluations?assignmentId=${assignment.id}`)
    ).json();
    expect(records.items).toHaveLength(1);
    await page.goto(`/#/evaluations/${records.items[0].id}`);
    await page.bringToFront();
    page.once('dialog', (d) => void d.accept());
    await page.getByRole('button', { name: '创建重判任务', exact: true }).click();
    await expect(page.getByRole('status')).toContainText('结果版本 2');
    await expect
      .poll(
        async () => {
          const r = await (
            await request.get(`/api/v1/evaluations?assignmentId=${assignment.id}`)
          ).json();
          return r.total;
        },
        { timeout: 120000 },
      )
      .toBe(2);
    await student.bringToFront();
    await expect(student.locator('.learning-job').filter({ hasText: '结果版本 2' })).toContainText(
      '评测结果：accepted',
      { timeout: 15000 },
    );
    await student.evaluate(() => scrollTo(0, 0));
    await student.screenshot({ path: '../.cache/agentlab-learning-live.png', fullPage: true });
  } finally {
    await context.close();
  }
});
