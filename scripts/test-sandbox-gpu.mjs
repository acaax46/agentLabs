// Runs the tests that need a real NVIDIA card: the CUDA check and an isolated PyTorch execution in
// the GPU sandbox, then a GPU agent run end to end (scripted model, real card, real billing).
// Needs Docker with the NVIDIA runtime (Docker Desktop + WSL2 provides it), both sandbox images
// (pnpm build:sandbox and pnpm build:sandbox:cuda) and MongoDB (pnpm db:up). The agent test expects
// the RTX 3070 Laptop preset; set AGENTLAB_TEST_SANDBOX_GPU_INDEX to use a card other than 0.
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
    'TestRealContainerGPU|TestGPUAgentRunOnARealCard',
    '-count=1',
    '-v',
    '-timeout',
    '15m',
  ],
  {
    stdio: 'inherit',
    env: {
      ...process.env,
      AGENTLAB_TEST_SANDBOX_IMAGE:
        process.env.AGENTLAB_TEST_SANDBOX_IMAGE || 'agentlab/sandbox-python:20261024',
      AGENTLAB_TEST_SANDBOX_GPU_IMAGE:
        process.env.AGENTLAB_TEST_SANDBOX_GPU_IMAGE || 'agentlab/sandbox-python-cuda:20261025',
      AGENTLAB_TEST_SANDBOX_GPU_MODEL:
        process.env.AGENTLAB_TEST_SANDBOX_GPU_MODEL || 'RTX 3070 Laptop',
      AGENTLAB_MONGO_URI:
        process.env.AGENTLAB_MONGO_URI ||
        'mongodb://127.0.0.1:27018/?replicaSet=rs0&directConnection=true',
      AGENTLAB_REQUIRE_MONGO_TESTS: '1',
    },
  },
);
if (result.error) throw result.error;
process.exit(result.status ?? 1);
