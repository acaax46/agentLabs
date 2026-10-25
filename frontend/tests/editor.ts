import { expect } from '@playwright/test';
import type { Page } from '@playwright/test';
export async function setEditorText(page: Page, label: string, value: string) {
  const input = page.getByRole('textbox', { name: label, exact: true });
  await expect(input).toBeVisible({ timeout: 30000 });
  await input.press('ControlOrMeta+A');
  await page.keyboard.insertText(value);
}
export async function expectEditorText(page: Page, label: string, value: string) {
  const input = page.getByRole('textbox', { name: label, exact: true });
  await expect(input).toBeVisible({ timeout: 30000 });
  const wrapper = page.locator('.code-editor').filter({ has: input });
  const last = value.trim().split('\n').pop() || '';
  await expect(wrapper).toContainText(last);
}
