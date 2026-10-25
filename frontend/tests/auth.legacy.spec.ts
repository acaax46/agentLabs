import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';

async function signIn(page: Page, username: string, password: string) {
  await page.goto('/#/login');
  await page.getByLabel('用户名', { exact: true }).fill(username);
  await page.getByLabel('密码', { exact: true }).fill(password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
}

test('login gates management and user/admin enter their own workspaces', async ({ page }) => {
  test.setTimeout(120000);
  await page.goto('/#/users');
  await expect(page).toHaveURL(/#\/login$/);
  expect((await page.request.get('/api/v1/bootstrap')).status()).toBe(401);
  await page.screenshot({ path: '../.cache/agentlab-login.png', fullPage: true });
  const accounts = [
    ['admin', 'Admin', '/admin', '管理员'],
    ['student', 'Student', '/student', '用户'],
  ];
  for (const [username, label, home, role] of accounts) {
    await signIn(page, username!, `AgentLab-${label}-2026!`);
    await expect(page).toHaveURL(new RegExp(`#${home}$`));
    await expect(page.locator('.account-chip')).toContainText(role!);
    await expect(page.getByRole('button', { name: '退出登录', exact: true })).toBeVisible();
    if (username === 'student') {
      await expect(page.locator('.workspace-nav')).not.toContainText('账号与权限');
      await page.goto('/#/users');
      await expect(
        page.getByRole('heading', { name: '无权访问此页面', exact: true }),
      ).toBeVisible();
      expect((await page.request.get('/api/v1/users')).status()).toBe(403);
      await page.goto('/#/student');
      await page.screenshot({ path: '../.cache/agentlab-student.png', fullPage: true });
    }
    await page.getByRole('button', { name: '退出登录', exact: true }).click();
    await expect(page).toHaveURL(/#\/login$/);
  }
});

test('user registration cannot select an elevated role, and password changes revoke login', async ({
  page,
  request,
}) => {
  const username = `learner_${Date.now().toString(36)}`;
  const elevated = await request.post('/api/v1/auth/register', {
    headers: { 'X-AgentLab-Request': 'auth' },
    data: {
      username: username + 'x',
      displayName: '不能自选权限',
      password: 'SelfRegister-2026!',
      role: 'owner',
    },
  });
  expect(elevated.status()).toBe(400);
  await page.goto('/#/register');
  await page.getByLabel('用户名', { exact: true }).fill(username);
  await page.getByLabel('姓名或昵称', { exact: true }).fill('注册验收用户');
  await page.getByLabel('密码', { exact: true }).fill('SelfRegister-2026!');
  await page.getByRole('button', { name: '注册并进入学习空间', exact: true }).click();
  await expect(page).toHaveURL(/#\/student$/);
  const state = await (await page.request.get('/api/v1/auth/me')).json();
  expect(state.user.role).toBe('user');
  expect(state.permissions.catalogWrite).toBe(false);
  expect(state.permissions.manageUsers).toBe(false);
  const profiles = await (await page.request.get('/api/v1/students')).json();
  expect(profiles.total).toBe(1);
  expect(profiles.items[0].id).toBe(state.user.studentId);
  const cookies = await page.context().cookies();
  expect(cookies.find((cookie) => cookie.name === 'agentlab_session')?.httpOnly).toBe(true);
  expect(await page.evaluate(() => document.cookie)).not.toContain('agentlab_session');
  await page.goto('/#/account');
  await page.getByLabel('当前密码', { exact: true }).fill('SelfRegister-2026!');
  await page.getByLabel('新密码', { exact: true }).fill('ChangedStudent-2026!');
  await page.getByLabel('确认新密码', { exact: true }).fill('ChangedStudent-2026!');
  await page.getByRole('button', { name: '更新密码并重新登录', exact: true }).click();
  await expect(page).toHaveURL(/#\/login$/);
  await signIn(page, username, 'SelfRegister-2026!');
  await expect(page.getByRole('alert')).toContainText('用户名或密码不正确');
  await page.getByLabel('密码', { exact: true }).fill('ChangedStudent-2026!');
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/#\/student$/);
});

test('navigation has no visible left scrollbar and mobile drawer remains keyboard accessible', async ({
  page,
}) => {
  await signIn(page, 'admin', 'AgentLab-Admin-2026!');
  await expect(page).toHaveURL(/#\/admin$/);
  expect(
    await page.locator('.sidebar').evaluate((element) => getComputedStyle(element).overflowY),
  ).toBe('hidden');
  expect(
    await page
      .locator('.workspace-nav')
      .evaluate((element) => getComputedStyle(element).scrollbarWidth),
  ).toBe('none');
  await page.getByRole('button', { name: '运行记录', exact: true }).click();
  await expect(page.getByRole('link', { name: '日志', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: '题目管理', exact: true })).not.toBeVisible();
  await page.screenshot({ path: '../.cache/agentlab-auth-admin.png', fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole('navigation', { name: '主导航' })).not.toBeVisible();
  const open = page.getByRole('button', { name: '打开导航菜单', exact: true });
  await open.click();
  await expect(page.getByRole('dialog', { name: '导航菜单', exact: true })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog', { name: '导航菜单', exact: true })).not.toBeVisible();
  await expect(open).toBeFocused();
  await open.click();
  await page.getByRole('link', { name: '用量记录', exact: true }).click();
  await expect(page.getByRole('heading', { name: '用量记录', exact: true })).toBeVisible();
  await expect(page.getByRole('dialog', { name: '导航菜单', exact: true })).not.toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: '../.cache/agentlab-auth-mobile.png', fullPage: true });
});

test('admin creates a user account and disabling it invalidates its active session', async ({
  page,
  browser,
}) => {
  test.setTimeout(60000);
  await signIn(page, 'admin', 'AgentLab-Admin-2026!');
  await expect(page).toHaveURL(/#\/admin$/);
  await page.goto('/#/users');
  const username = `user_${Date.now().toString(36)}`;
  await page.getByRole('button', { name: '创建账号', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('用户名', { exact: true }).fill(username);
  await dialog.getByLabel('姓名或昵称', { exact: true }).fill('权限验收用户');
  await dialog.getByLabel('账号角色', { exact: true }).selectOption('user');
  await dialog.getByLabel('初始密码', { exact: true }).fill('UserVerify-2026!');
  await dialog.getByRole('button', { name: '保存账号', exact: true }).click();
  await expect(dialog).not.toBeVisible();
  const context = await browser.newContext({ baseURL: new URL(page.url()).origin });
  const other = await context.newPage();
  try {
    await signIn(other, username, 'UserVerify-2026!');
    await expect(other).toHaveURL(/#\/student$/);
    await page.getByLabel('搜索账号', { exact: true }).fill(username);
    await page.getByRole('button', { name: '搜索', exact: true }).click();
    const row = page.getByRole('row').filter({ hasText: username });
    await row.getByRole('button', { name: '编辑权限', exact: true }).click();
    await dialog.getByLabel('账号状态', { exact: true }).selectOption('disabled');
    await dialog.getByRole('button', { name: '保存账号', exact: true }).click();
    await expect(dialog).not.toBeVisible();
    await expect(row).toContainText('已停用');
    await other.reload();
    await expect(other).toHaveURL(/#\/login$/);
  } finally {
    await context.close();
  }
});
