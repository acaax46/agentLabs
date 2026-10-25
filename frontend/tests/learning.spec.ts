import { expect, test } from './fixtures';
import { setEditorText, expectEditorText } from './editor';

test('administrator publishes a frozen task and user saves a private workspace across reloads', async ({
  page,
  request,
  browser,
}) => {
  test.setTimeout(90000);
  const headers = { 'X-AgentLab-Request': 'catalog', 'Idempotency-Key': crypto.randomUUID() };
  const suffix = Date.now().toString(36).toUpperCase();
  const create = async (kind: string, code: string, name: string, fields: unknown) => {
    const response = await request.post(`/api/v1/${kind}`, {
      headers: { ...headers, 'Idempotency-Key': crypto.randomUUID() },
      data: { code, name, status: 'draft', description: '发布测试资料', fields },
    });
    expect(response.status()).toBe(201);
    return response.json();
  };
  const classes = await (await request.get('/api/v1/classes')).json();
  const cls = classes.items.find((c: { code: string }) => c.code === 'DEMO-CLASS');
  expect(cls).toBeTruthy();
  const problem = await create('problems', `PUB-P-${suffix}`, '发布快照题目', {
    difficulty: 'easy',
    statement:
      '## 发布前的固定题面\n\n| 输入 | 输出 |\n| --- | --- |\n| 1 | 1 |\n\n公式 $a^2+b^2=c^2$。',
    timeLimitMs: 1000,
    memoryLimitMb: 128,
    tags: [],
    samples: [{ input: '1\n', output: '1\n', explanation: '' }],
  });
  const course = await create('courses', `PUB-C-${suffix}`, '工作区课程', {
    mode: 'classroom',
    templateId: '',
    problemIds: [problem.id],
  });
  const assignment = await create('assignments', `PUB-A-${suffix}`, `成果保存任务 ${suffix}`, {
    classId: cls.id,
    courseId: course.id,
    deadline: '',
    submissionLimit: 3,
  });
  await page.goto(`/#/assignments/${assignment.id}`);
  await page.getByRole('button', { name: '发布任务版本', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await setEditorText(page, '初始 main.py', '# initial\nprint(1)\n');
  const publishRequest = page.waitForRequest((request) => request.url().includes('/publish'));
  await dialog.getByRole('button', { name: '确认发布', exact: true }).click();
  const published = (await publishRequest).postDataJSON() as {
    files: { path: string; content: string }[];
  };
  expect(published.files).toContainEqual({ path: 'main.py', content: '# initial\nprint(1)\n' });
  await expect(dialog).not.toBeVisible();
  await expect(page.getByRole('link', { name: '发布版本 1', exact: true })).toBeVisible();
  const revisions = await (
    await request.get(`/api/v1/learning/releases?assignmentId=${assignment.id}`)
  ).json();
  const release = revisions[0];
  const changed = await request.put(`/api/v1/problems/${problem.id}`, {
    headers: {
      ...headers,
      'If-Match': `"${problem.version}"`,
      'Idempotency-Key': crypto.randomUUID(),
    },
    data: {
      code: problem.code,
      name: problem.name,
      status: 'draft',
      description: 'changed',
      fields: { ...problem.fields, statement: '修改后的草稿题面' },
    },
  });
  expect(changed.status()).toBe(200);
  const student = await browser.newContext({ baseURL: new URL(page.url()).origin });
  const learner = await student.newPage();
  try {
    const login = await student.request.post('/api/v1/auth/login', {
      headers: { 'X-AgentLab-Request': 'auth' },
      data: { username: 'student', password: 'AgentLab-Student-2026!' },
    });
    expect(login.status()).toBe(200);
    await learner.goto('/#/student');
    await expect(learner.locator('.workspace-nav')).toContainText('训练题目');
    await expect(learner.locator('.workspace-nav')).toContainText('课程资源');
    await expect(learner.locator('.workspace-nav')).not.toContainText('我的任务');
    await expect(learner.locator('.workspace-nav')).not.toContainText('我的班级');
    const opened = await student.request.post(`/api/v1/learning/releases/${release.id}/workspace`, {
      headers: { 'X-AgentLab-Request': 'learning', 'Content-Type': 'application/json' },
      data: {},
    });
    expect(opened.status()).toBe(200);
    const workspace = await opened.json();
    await learner.goto(`/#/workspace/${workspace.id}`);
    await expect(learner).toHaveURL(/#\/workspace\//);
    await expect(
      learner.getByRole('heading', { name: '发布前的固定题面', exact: true }),
    ).toBeVisible();
    await expect(learner.getByRole('table')).toBeVisible();
    await expect(learner.locator('.katex').first()).toBeVisible();
    await expect(learner.getByText('修改后的草稿题面', { exact: true })).not.toBeVisible();
    await expectEditorText(learner, '编辑 main.py', '# initial\nprint(1)\n');
    expect(
      await learner
        .locator('.code-editor .monaco-editor-background')
        .first()
        .evaluate((element) => getComputedStyle(element).backgroundColor),
    ).toBe('rgb(30, 51, 54)');
    await setEditorText(learner, '编辑 main.py', '# my own work\nprint(42)\n');
    await learner.getByRole('button', { name: '保存成果', exact: true }).click();
    await expect(learner.getByRole('status').filter({ hasText: '成果已保存' })).toBeVisible();
    await learner.reload();
    await expectEditorText(learner, '编辑 main.py', '# my own work\nprint(42)\n');
    await learner.getByRole('textbox', { name: '新文件路径', exact: true }).fill('src/helper.py');
    await learner.getByRole('button', { name: '添加文件', exact: true }).click();
    await setEditorText(learner, '编辑 src/helper.py', 'def answer():\n    return 42\n');
    await learner.getByRole('button', { name: '保存成果', exact: true }).click();
    await expect(learner.getByRole('status').filter({ hasText: '成果已保存' })).toBeVisible();
    await expect(learner.getByRole('button', { name: '运行公开测试', exact: true })).toBeDisabled();
    await learner.getByRole('button', { name: '打开编程助手', exact: true }).click();
    await expect(learner.getByRole('button', { name: '发送任务', exact: true })).toBeDisabled();
    await learner.setViewportSize({ width: 1440, height: 900 });
    await learner.screenshot({ path: '../.cache/agentlab-learning-workspace.png', fullPage: true });
    await learner.setViewportSize({ width: 390, height: 844 });
    expect(await learner.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await learner.screenshot({
      path: '../.cache/agentlab-learning-workspace-mobile.png',
      fullPage: true,
    });
    const response = await student.request.get(`/api/v1/learning/releases/${release.id}`);
    expect((await response.json()).digest).toBe(release.digest);
  } finally {
    await student.close();
  }
});

test('administrator configures a real budget cap and immutable model price version', async ({
  page,
}) => {
  await page.goto('/#/budgets');
  await page.getByRole('combobox', { name: '预算范围', exact: true }).selectOption('tenant');
  await page.getByLabel('累计预算上限', { exact: true }).fill('12.345678');
  await page.getByRole('button', { name: '保存预算', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('预算已保存');
  await expect(page.getByRole('row').filter({ hasText: '机构 · local' })).toContainText(
    '12.345678',
  );
  const version = `test-browser-${Date.now().toString(36)}`;
  for (const [label, value] of [
    ['价格版本号', version],
    ['供应商标识', 'test-gateway'],
    ['模型标识', 'test-model'],
    ['每百万输入 Token 价格', '1'],
    ['每百万输出 Token 价格', '2'],
    ['每百万缓存读取 Token 价格', '0.5'],
    ['每百万缓存写入 Token 价格', '1'],
    ['单次调用预占金额', '0.01'],
  ])
    await page.getByLabel(label!, { exact: true }).fill(value!);
  await page.getByRole('button', { name: '创建价格版本', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('价格版本已创建');
  await expect(page.getByText(version, { exact: true })).toBeVisible();
});
