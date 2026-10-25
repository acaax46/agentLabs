import { expect, test } from '@playwright/test';

test('student trains public problems and keeps private course progress', async ({
  page,
  browser,
}) => {
  test.setTimeout(90000);
  await page.goto('/#/login');
  const context = await browser.newContext({ baseURL: new URL(page.url()).origin });
  const learner = await context.newPage();
  try {
    const login = await context.request.post('/api/v1/auth/login', {
      headers: { 'X-AgentLab-Request': 'auth' },
      data: { username: 'student', password: 'AgentLab-Student-2026!' },
    });
    expect(login.status()).toBe(200);
    expect((await context.request.get('/api/v1/assignments')).status()).toBe(403);
    expect((await context.request.get('/api/v1/classes')).status()).toBe(403);
    await learner.goto('/#/student');
    await expect(learner.locator('.workspace-nav')).toContainText('训练题目');
    await expect(learner.locator('.workspace-nav')).toContainText('课程资源');
    await expect(learner.locator('.workspace-nav')).not.toContainText('我的任务');
    await expect(learner.locator('.workspace-nav')).not.toContainText('我的班级');
    await learner
      .locator('.workspace-nav')
      .getByRole('link', { name: '训练题目', exact: true })
      .click();
    await expect(learner.getByRole('heading', { name: '训练题目', exact: true })).toBeVisible();
    await expect(learner.locator('.training-table')).toBeVisible();
    await learner.screenshot({
      path: '../.cache/agentlab-student-training-list.png',
      fullPage: true,
    });
    await learner.getByRole('link', { name: /输出第一行代码/ }).click();
    await expect(
      learner.getByRole('heading', { name: 'T-PRINT-01 · 输出第一行代码', exact: true }),
    ).toBeVisible();
    await expect(learner.getByText('Hello, AgentLab!', { exact: true }).first()).toBeVisible();
    await learner.getByRole('button', { name: '开始训练', exact: true }).click();
    await expect(learner).toHaveURL(/#\/workspace\//);
    await expect(learner.locator('.code-editor .monaco-editor')).toBeVisible({ timeout: 30000 });
    await expect(learner.locator('.workspace-problem .section-heading h2')).toContainText(
      '输出第一行代码',
    );
    await expect(learner.locator('.workspace-files')).toContainText('main.py');
    await expect(learner.getByRole('button', { name: '打开编程助手', exact: true })).toBeVisible();
    await learner.setViewportSize({ width: 390, height: 844 });
    expect(await learner.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await learner.setViewportSize({ width: 1440, height: 900 });
    await learner.screenshot({
      path: '../.cache/agentlab-student-workspace.png',
      fullPage: true,
    });
    await learner.goto('/#/student/resources');
    await expect(learner.getByRole('heading', { name: '课程资源', exact: true })).toBeVisible();
    await learner.getByRole('link', { name: /Python 编程基础/ }).click();
    await expect(
      learner.getByRole('heading', { name: 'Python 编程基础', exact: true }),
    ).toBeVisible();
    await expect(learner.getByText('输出、变量与算术', { exact: true }).first()).toBeVisible();
    await learner.getByRole('button', { name: '标记本节已完成', exact: true }).click();
    await expect(learner.getByRole('status').filter({ hasText: '本节已标记完成' })).toBeVisible();
    await learner.reload();
    await expect(learner.getByText('已完成 1/6 节', { exact: true })).toBeVisible();
    const course = await (
      await context.request.get('/api/v1/training/courses/course-python-basics')
    ).json();
    expect(course.progress.completedSectionIds).toContain('print-variables');
    await learner.setViewportSize({ width: 390, height: 844 });
    expect(await learner.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await learner.setViewportSize({ width: 1440, height: 900 });
    await learner.screenshot({
      path: '../.cache/agentlab-student-course.png',
      fullPage: true,
    });
  } finally {
    await context.close();
  }
});

test('student previews HTML problems in a sandboxed iframe', async ({ page, browser }) => {
  test.setTimeout(90000);
  await page.goto('/#/login');
  const context = await browser.newContext({ baseURL: new URL(page.url()).origin });
  const learner = await context.newPage();
  try {
    const login = await context.request.post('/api/v1/auth/login', {
      headers: { 'X-AgentLab-Request': 'auth' },
      data: { username: 'student', password: 'AgentLab-Student-2026!' },
    });
    expect(login.status()).toBe(200);
    await learner.goto('/#/student');
    await learner
      .locator('.workspace-nav')
      .getByRole('link', { name: '训练题目', exact: true })
      .click();
    await expect(learner.getByRole('heading', { name: '训练题目', exact: true })).toBeVisible();
    await learner.getByRole('link', { name: /个人名片页/ }).click();
    await expect(
      learner.getByRole('heading', { name: 'T-HTML-13 · 个人名片页', exact: true }),
    ).toBeVisible();
    await learner.getByRole('button', { name: '开始训练', exact: true }).click();
    await expect(learner).toHaveURL(/#\/workspace\//);
    await expect(learner.locator('.code-editor .monaco-editor')).toBeVisible({ timeout: 30000 });
    await expect(learner.locator('.workspace-files')).toContainText('index.html');
    await expect(learner.locator('.workspace-files')).toContainText('styles.css');
    await learner.getByRole('button', { name: '预览页面', exact: true }).click();
    const cardFrame = learner.frameLocator('iframe[title="HTML 预览"]');
    await expect(cardFrame.getByRole('heading', { name: '你的名字', exact: true })).toBeVisible();
    await expect(cardFrame.getByText('编程学习者', { exact: true })).toBeVisible();
    await learner.screenshot({
      path: '../.cache/agentlab-student-html-preview.png',
      fullPage: true,
    });
    await learner.goto('/#/student');
    await learner
      .locator('.workspace-nav')
      .getByRole('link', { name: '训练题目', exact: true })
      .click();
    await learner.getByRole('link', { name: /待办清单/ }).click();
    await learner.getByRole('button', { name: '开始训练', exact: true }).click();
    await expect(learner).toHaveURL(/#\/workspace\//);
    await expect(learner.locator('.code-editor .monaco-editor')).toBeVisible({ timeout: 30000 });
    await expect(learner.locator('.workspace-files')).toContainText('script.js');
    await learner.getByRole('button', { name: '预览页面', exact: true }).click();
    const todoFrame = learner.frameLocator('iframe[title="HTML 预览"]');
    await todoFrame.getByPlaceholder('写下一件要做的事').fill('完成 HTML 练习');
    await todoFrame.getByRole('button', { name: '添加', exact: true }).click();
    await expect(todoFrame.getByText('完成 HTML 练习', { exact: true })).toBeVisible();
  } finally {
    await context.close();
  }
});
