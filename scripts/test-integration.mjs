import { spawnSync } from 'node:child_process';
const result = spawnSync('go', ['-C', 'backend', 'test', './...', '-count=1'], {
  stdio: 'inherit',
  env: {
    ...process.env,
    AGENTLAB_MONGO_URI:
      process.env.AGENTLAB_MONGO_URI ||
      'mongodb://127.0.0.1:27018/?replicaSet=rs0&directConnection=true',
    AGENTLAB_REQUIRE_MONGO_TESTS: '1',
  },
});
if (result.error) throw result.error;
process.exit(result.status ?? 1);
