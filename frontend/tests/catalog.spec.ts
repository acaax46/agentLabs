import { expect, test } from './fixtures';

test('problem editor preserves samples, validates duplicate codes and safely deletes', async ({
  page,
}) => {
  const code = `P-${Date.now()}`;
  await page.goto('/#/problems');
  await page.getByRole('link', { name: '新建题目', exact: true }).first().click();
  await page.getByLabel('题目编号').fill(code);
  await page.getByLabel('题目名称').fill('公开样例 <b>按原文显示</b>');
  await page.getByLabel('题目难度').selectOption('medium');
  await page.getByLabel('题目描述').fill('保留原始题面，只保存资料，不执行代码。');
  await page.getByRole('button', { name: '添加公开样例' }).click();
  await page.getByLabel('样例 1 输入').fill(' 2 3\n');
  await page.getByLabel('样例 1 输出').fill('5\n');
  await page.getByRole('button', { name: '保存题目', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: '公开样例 <b>按原文显示</b>', exact: true }),
  ).toBeVisible();
  expect(await page.locator('.sample-grid pre').first().textContent()).toBe(' 2 3\n');
  await page.reload();
  await expect(
    page.getByRole('heading', { name: '公开样例 <b>按原文显示</b>', exact: true }),
  ).toBeVisible();
  await page.getByRole('link', { name: '编辑题目', exact: true }).click();
  await page.getByLabel('题目名称').fill('编辑后的公开样例');
  await page.getByRole('button', { name: '保存题目', exact: true }).click();
  await expect(page.getByRole('heading', { name: '编辑后的公开样例', exact: true })).toBeVisible();
  await page.getByRole('link', { name: '返回题目管理', exact: true }).click();
  await page.getByLabel('搜索题目').fill(code);
  await page.getByRole('button', { name: '搜索', exact: true }).click();
  await page.getByLabel('难度筛选').selectOption('medium');
  await expect(page.getByRole('link', { name: '编辑后的公开样例', exact: true })).toBeVisible();
  await page.getByRole('link', { name: '新建题目', exact: true }).first().click();
  await page.getByLabel('题目编号').fill(code);
  await page.getByLabel('题目名称').fill('重复编号');
  await page.getByRole('button', { name: '保存题目', exact: true }).click();
  await expect(page.getByText('该编号已存在，请使用其他编号。', { exact: true })).toBeVisible();
  await page.getByRole('link', { name: '取消', exact: true }).click();
  await page.getByLabel('搜索题目').fill(code);
  await page.getByRole('button', { name: '搜索', exact: true }).click();
  await page
    .getByRole('row')
    .filter({ hasText: code })
    .getByRole('button', { name: '删除', exact: true })
    .click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole('button', { name: '取消', exact: true })).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(dialog).not.toBeVisible();
  await page
    .getByRole('row')
    .filter({ hasText: code })
    .getByRole('button', { name: '删除', exact: true })
    .click();
  await dialog.getByRole('button', { name: '确认删除', exact: true }).click();
  await expect(dialog).not.toBeVisible();
  await expect(page.getByRole('row').filter({ hasText: code })).toHaveCount(0);
});

test('all seven modules support linked create, edit, detail and delete from the UI', async ({
  page,
}) => {
  test.setTimeout(120_000);
  const suffix = Date.now();
  const records: Record<string, { id: string; name: string; code: string }> = {};
  const modules = [
    ['classes', '班级'],
    ['students', '学员'],
    ['templates', '模板'],
    ['problems', '题目'],
    ['courses', '课程'],
    ['assignments', '任务'],
    ['contests', '训练赛'],
  ] as const;
  for (const [kind, label] of modules) {
    const code = `${kind.slice(0, 3).toUpperCase()}-${suffix}`;
    const name = `${label}端到端验证 ${suffix}`;
    await page.goto(`/#/${kind}/new`);
    await page.getByLabel(`${label}编号`).fill(code);
    await page.getByLabel(kind === 'students' ? '学员姓名' : `${label}名称`).fill(name);
    if (kind === 'classes') await page.getByLabel('负责教师').fill('课堂教师');
    if (kind === 'students')
      await page
        .getByRole('checkbox', {
          name: `${records.classes!.code} · ${records.classes!.name}`,
          exact: true,
        })
        .check();
    if (kind === 'courses') {
      await page.getByLabel('环境模板').selectOption(records.templates!.id);
      await page
        .getByRole('checkbox', {
          name: `${records.problems!.code} · ${records.problems!.name}`,
          exact: true,
        })
        .check();
    }
    if (kind === 'assignments') {
      await page.getByLabel('所属班级').selectOption(records.classes!.id);
      await page.getByLabel('关联课程').selectOption(records.courses!.id);
    }
    if (kind === 'contests') {
      await page
        .getByRole('checkbox', {
          name: `${records.problems!.code} · ${records.problems!.name}`,
          exact: true,
        })
        .check();
      await page
        .getByRole('checkbox', {
          name: `${records.classes!.code} · ${records.classes!.name}`,
          exact: true,
        })
        .check();
    }
    await page.getByRole('button', { name: `保存${label}`, exact: true }).click();
    await expect(page.getByRole('heading', { name, exact: true })).toBeVisible();
    const id = page.url().split('/').pop()!;
    await page.getByRole('link', { name: `编辑${label}`, exact: true }).click();
    const edited = `${name} 已编辑`;
    await page.getByLabel(kind === 'students' ? '学员姓名' : `${label}名称`).fill(edited);
    await page.getByRole('button', { name: `保存${label}`, exact: true }).click();
    await expect(page.getByRole('heading', { name: edited, exact: true })).toBeVisible();
    records[kind] = { id, name: edited, code };
    await page.setViewportSize({ width: 390, height: 844 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.setViewportSize({ width: 1440, height: 1000 });
  }
  // Referenced problems must be protected by the backend, including the UI path.
  await page.goto(`/#/problems/${records.problems!.id}`);
  await page.getByRole('button', { name: '删除', exact: true }).click();
  await page.getByRole('button', { name: '确认删除', exact: true }).click();
  await expect(page.getByRole('dialog').getByRole('alert')).toContainText('仍被');
  await page.getByRole('button', { name: '取消', exact: true }).click();
  for (const kind of [
    'contests',
    'assignments',
    'courses',
    'students',
    'classes',
    'templates',
    'problems',
  ]) {
    await page.goto(`/#/${kind}/${records[kind]!.id}`);
    await page.getByRole('button', { name: '删除', exact: true }).click();
    await page.getByRole('button', { name: '确认删除', exact: true }).click();
    await expect(page.getByRole('dialog')).not.toBeVisible();
    await expect(page).toHaveURL(new RegExp(`/#/${kind}$`));
  }
});
