// Runs the tests that need a real container: the isolation and resource-limit checks against the
// sandbox image, and one agent run that executes real pandas/matplotlib code in it.
// Build the image first (pnpm build:sandbox) and start MongoDB (pnpm db:up).
// Set AGENTLAB_TEST_SANDBOX_RUNTIME (for example runsc) to run the same checks under gVisor or Kata.
import { spawnSync } from 'node:child_process';

const result = spawnSync(
  'go',
  [
    '-C',
    'backend',
    'test',
    './internal/platform',
    './internal/platform/sandbox',
    '-run',
    'TestRealContainer|TestAgentRunWithTheRealSandbox|TestAgentRunWithAnExcelWorkbook',
    '-count=1',
    '-v',
    '-timeout',
    '10m',
  ],
  {
    stdio: 'inherit',
    env: {
      ...process.env,
      AGENTLAB_TEST_SANDBOX_IMAGE:
        process.env.AGENTLAB_TEST_SANDBOX_IMAGE || 'agentlab/sandbox-python:20261024',
      AGENTLAB_MONGO_URI:
        process.env.AGENTLAB_MONGO_URI ||
        'mongodb://127.0.0.1:27018/?replicaSet=rs0&directConnection=true',
      AGENTLAB_REQUIRE_MONGO_TESTS: '1',
    },
  },
);
if (result.error) throw result.error;
process.exit(result.status ?? 1);
