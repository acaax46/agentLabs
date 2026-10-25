import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';

async function signIn(page: Page, username: string, password: string) {
  await page.goto('/#/login');
  await page.getByLabel('用户名', { exact: true }).fill(username);
  await page.getByLabel('密码', { exact: true }).fill(password);
  await page.getByRole('button', { name: '登录工作台', exact: true }).click();
}

test('current workbench authenticates both roles and clears the session on logout', async ({
  page,
}) => {
  await page.goto('/#/projects');
  await expect(page).toHaveURL(/#\/login$/);
  expect((await page.request.get('/api/v1/platform/projects')).status()).toBe(401);
  for (const [username, password, role] of [
    ['admin', 'AgentLab-Admin-2026!', '管理员'],
    ['student', 'AgentLab-Student-2026!', '成员'],
  ]) {
    await signIn(page, username!, password!);
    await expect(page).toHaveURL(username === 'admin' ? /#\/overview$/ : /#\/chat$/);
    await page.goto('/#/settings');
    await expect(page.getByRole('definition').filter({ hasText: role! }).first()).toBeVisible();
    const state = await (await page.request.get('/api/v1/auth/me')).json();
    expect(state.user.role).toBe(username === 'admin' ? 'admin' : 'user');
    if (username === 'student')
      expect((await page.request.get('/api/v1/users')).status()).toBe(403);
    await page.getByRole('button', { name: '退出登录', exact: true }).click();
    await expect(page).toHaveURL(/#\/login$/);
    expect((await page.request.get('/api/v1/platform/projects')).status()).toBe(401);
  }
});

test('registration rejects elevated roles and changing password revokes existing sessions', async ({
  page,
  request,
}) => {
  const username = `dev_${Date.now().toString(36)}`;
  const elevated = await request.post('/api/v1/auth/register', {
    headers: { 'X-AgentLab-Request': 'auth' },
    data: {
      username: `${username}x`,
      displayName: '越权注册',
      password: 'Register-2026!',
      role: 'admin',
    },
  });
  expect(elevated.status()).toBe(400);
  await page.goto('/#/register');
  await page.getByLabel('用户名', { exact: true }).fill(username);
  await page.getByLabel('姓名或昵称').fill('注册验收用户');
  await page.getByLabel('密码', { exact: true }).fill('Register-2026!');
  await page.getByRole('button', { name: '注册并进入工作台', exact: true }).click();
  await expect(page).toHaveURL(/#\/chat$/);
  const state = await (await page.request.get('/api/v1/auth/me')).json();
  expect(state.user.role).toBe('user');
  expect(state.permissions.manageUsers).toBe(false);
  await request.post('/api/v1/auth/login', {
    headers: { 'X-AgentLab-Request': 'auth' },
    data: { username, password: 'Register-2026!' },
  });
  expect(
    (await page.context().cookies()).find((cookie) => cookie.name === 'agentlab_session')?.httpOnly,
  ).toBe(true);
  expect(await page.evaluate(() => document.cookie)).not.toContain('agentlab_session');
  await page.goto('/#/settings');
  await page.getByLabel('当前密码', { exact: true }).fill('Register-2026!');
  await page.getByLabel('新密码', { exact: true }).fill('Changed-2026!');
  await page.getByLabel('确认新密码', { exact: true }).fill('Changed-2026!');
  await page.getByRole('button', { name: '更新密码并重新登录' }).click();
  await expect(page).toHaveURL(/#\/login$/);
  expect((await (await request.get('/api/v1/auth/me')).json()).user).toBeNull();
  await signIn(page, username, 'Register-2026!');
  await expect(page.getByRole('alert')).toContainText('用户名或密码不正确');
  await page.getByLabel('密码', { exact: true }).fill('Changed-2026!');
  await page.getByRole('button', { name: '登录工作台' }).click();
  await expect(page).toHaveURL(/#\/chat$/);
});

test('mobile drawer traps keyboard focus, closes with Escape and has no visible scrollbar', async ({
  page,
}) => {
  await signIn(page, 'admin', 'AgentLab-Admin-2026!');
  await expect(page).toHaveURL(/#\/overview$/);
  await expect(page.locator('h1')).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  const open = page.getByRole('button', { name: '打开导航菜单' });
  await open.click();
  const drawer = page.getByRole('dialog', { name: '工作台导航' });
  await expect(drawer).toBeVisible();
  expect(
    await page.locator('main').evaluate((node) => node instanceof HTMLElement && node.inert),
  ).toBe(true);
  await page.keyboard.press('Shift+Tab');
  expect(await drawer.evaluate((node) => node.contains(document.activeElement))).toBe(true);
  await page.keyboard.press('Escape');
  await expect(open).toBeFocused();
  await expect(
    page.getByRole('dialog', { name: '工作台导航', includeHidden: true }),
  ).toHaveAttribute('aria-hidden', 'true');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test('login network failures remain actionable without an authenticated fallback', async ({
  page,
}) => {
  await page.route('**/api/v1/auth/login', (route) => route.abort('failed'));
  await signIn(page, 'admin', 'AgentLab-Admin-2026!');
  await expect(page.getByRole('alert')).toBeVisible();
  await expect(page).toHaveURL(/#\/login$/);
  await expect(page.getByRole('button', { name: '登录工作台' })).toBeEnabled();
});
