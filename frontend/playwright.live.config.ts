import { defineConfig } from '@playwright/test';
// The live suite uses real QEMU/OpenSandbox and a separate MongoDB database. No model calls without an explicit model.
const apiPort = 18788,
  webPort = 15174;
export default defineConfig({
  outputDir: '../.cache/playwright-live',
  testDir: './tests/live',
  workers: 1,
  timeout: 420000,
  use: {
    baseURL: `http://127.0.0.1:${webPort}`,
    headless: true,
    launchOptions: process.env.AGENTLAB_BROWSER_PATH
      ? { executablePath: process.env.AGENTLAB_BROWSER_PATH }
      : {},
  },
  webServer: [
    {
      command: 'go -C ../backend run ./cmd/seed-demo && go -C ../backend run ./cmd/server',
      env: {
        AGENTLAB_MONGO_DATABASE:
          process.env.AGENTLAB_E2E_DATABASE || `agentlab_live_e2e_${process.pid}`,
        AGENTLAB_LISTEN_ADDR: `127.0.0.1:${apiPort}`,
        AGENTLAB_DEV_ORIGIN: `http://127.0.0.1:${webPort}`,
        AGENTLAB_LEARNING_EXECUTOR_READY: '1',
        AGENTLAB_CREDENTIAL_KEY: 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=',
      },
      url: `http://127.0.0.1:${apiPort}/health/live`,
      reuseExistingServer: false,
      timeout: 120000,
    },
    {
      command: 'pnpm dev',
      env: { AGENTLAB_API_PORT: String(apiPort), AGENTLAB_WEB_PORT: String(webPort) },
      url: `http://127.0.0.1:${webPort}`,
      reuseExistingServer: false,
      timeout: 60000,
    },
  ],
});
