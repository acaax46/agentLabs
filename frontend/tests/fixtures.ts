import { test as base, expect } from '@playwright/test';
export const test = base.extend<{ signedIn: void }>({
  signedIn: [
    async ({ page, request }, use) => {
      const body = { username: 'admin', password: 'AgentLab-Admin-2026!' };
      for (const client of [page.request, request]) {
        const response = await client.post('/api/v1/auth/login', {
          headers: { 'X-AgentLab-Request': 'auth' },
          data: body,
        });
        expect(response.status()).toBe(200);
      }
      await use();
    },
    { auto: true },
  ],
});
export { expect };
