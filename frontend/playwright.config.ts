import { defineConfig } from '@playwright/test';

const apiPort = Number(process.env.AGENTLAB_TEST_API_PORT || 18787);
const webPort = Number(process.env.AGENTLAB_TEST_WEB_PORT || 15173);
const apiOrigin = `http://127.0.0.1:${apiPort}`;
const webOrigin = `http://127.0.0.1:${webPort}`;

export default defineConfig({
  testDir: './tests',
  // The retained learning/catalog/record suites target the archived interface. Keep their
  // source, but make the default product gate exercise current authentication and connections.
  testMatch: ['**/auth.spec.ts', '**/connections.spec.ts', '**/native-zoom.spec.ts'],
  fullyParallel: false,
  workers: 1,
  use: {
    baseURL: webOrigin,
    headless: true,
    launchOptions: process.env.AGENTLAB_BROWSER_PATH
      ? { executablePath: process.env.AGENTLAB_BROWSER_PATH }
      : {},
  },
  webServer: [
    {
      command: 'go -C ../backend run ./cmd/seed-demo && go -C ../backend run ./cmd/server',
      env: {
        AGENTLAB_MONGO_DATABASE: process.env.AGENTLAB_E2E_DATABASE || `agentlab_e2e_${process.pid}`,
        AGENTLAB_LISTEN_ADDR: `127.0.0.1:${apiPort}`,
        AGENTLAB_DEV_ORIGIN: webOrigin,
        AGENTLAB_LEARNING_EXECUTOR_READY: '0',
        AGENTLAB_CREDENTIAL_KEY: 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=',
        AGENTLAB_CONNECTION_ALLOWED_ORIGINS: 'http://127.0.0.1:18990',
      },
      url: `${apiOrigin}/health/live`,
      reuseExistingServer: false,
      timeout: 120_000,
    },
    {
      command: 'pnpm dev',
      env: { AGENTLAB_API_PORT: String(apiPort), AGENTLAB_WEB_PORT: String(webPort) },
      url: webOrigin,
      reuseExistingServer: false,
      timeout: 60_000,
    },
  ],
});
