import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests/live-platform',
  outputDir: '../.cache/p0-acceptance-results',
  workers: 1,
  timeout: 360_000,
  expect: { timeout: 10_000 },
  use: {
    baseURL: 'http://127.0.0.1:15175',
    headless: true,
    actionTimeout: 15_000,
    trace: 'off',
    screenshot: 'off',
    launchOptions: process.env.AGENTLAB_BROWSER_PATH
      ? { executablePath: process.env.AGENTLAB_BROWSER_PATH }
      : {},
  },
  webServer: {
    command: 'pnpm preview --port 15175 --strictPort',
    env: { AGENTLAB_WEB_PORT: '15175', AGENTLAB_API_PORT: '18879' },
    url: 'http://127.0.0.1:15175',
    reuseExistingServer: false,
    timeout: 60_000,
  },
});
