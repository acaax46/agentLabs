import { defineConfig } from '@playwright/test';

const port = Number(process.env.AGENTLAB_PLATFORM_TEST_WEB_PORT || 15174);
const baseURL = `http://127.0.0.1:${port}`;
export default defineConfig({
  testDir: './tests/platform',
  testMatch: [
    '**/p0.spec.ts',
    '**/platform.spec.ts',
    '**/pricing.spec.ts',
    '**/completion.spec.ts',
    '**/engines.spec.ts',
    '**/chat.spec.ts',
  ],
  outputDir: '../.cache/p0-ui-results',
  fullyParallel: true,
  workers: 2,
  use: {
    baseURL,
    headless: true,
    trace: 'retain-on-failure',
    launchOptions: process.env.AGENTLAB_BROWSER_PATH
      ? { executablePath: process.env.AGENTLAB_BROWSER_PATH }
      : {},
  },
  projects: [
    { name: 'desktop', use: { viewport: { width: 1440, height: 1000 } } },
    { name: 'tablet', use: { viewport: { width: 1024, height: 900 } } },
    { name: 'zoom-200', use: { viewport: { width: 720, height: 500 }, deviceScaleFactor: 2 } },
    {
      name: 'mobile',
      use: { viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true },
    },
  ],
  webServer: {
    command: 'pnpm dev',
    env: { AGENTLAB_WEB_PORT: String(port) },
    url: baseURL,
    reuseExistingServer: false,
    timeout: 60_000,
  },
});
