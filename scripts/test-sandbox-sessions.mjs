// Runs each code-agent CLI (pi, OpenCode, Codex CLI, Claude Code) in a real session container
// against a scripted model: the CLI must reach the model only through the stdio relay, run its shell
// tool inside the sandbox without network and hand back its files and final answer. No model is paid.
// Needs Docker and the code-agent image (pnpm build:sandbox:agents). AGENTLAB_TEST_HARNESS=pi,codex
// limits the engines.
import { spawnSync } from 'node:child_process';

const result = spawnSync(
  'go',
  [
    '-C',
    'backend',
    'test',
    './internal/platform/sandbox',
    '-run',
    'TestRealContainerSessions',
    '-count=1',
    '-v',
    '-timeout',
    '20m',
  ],
  {
    stdio: 'inherit',
    env: {
      ...process.env,
      AGENTLAB_TEST_SANDBOX_AGENT_IMAGE:
        process.env.AGENTLAB_TEST_SANDBOX_AGENT_IMAGE || 'agentlab/sandbox-agents:20261025',
    },
  },
);
if (result.error) throw result.error;
process.exit(result.status ?? 1);
