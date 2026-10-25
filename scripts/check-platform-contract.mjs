// Checks that contracts/platform.openapi.yaml, the backend and the workbench agree.
//
//   1. The contract is a valid OpenAPI document (Redocly lint) and a typed client can be generated
//      from it (openapi-typescript; written under .cache only, never over frontend/src/api).
//   2. Every response the real backend produces for the calls the workbench makes validates against
//      its schema, with undeclared and missing properties both rejected.
//   3. The same responses type-check against the types the workbench declares in
//      frontend/src/platform/api.ts, using the real TypeScript compiler.
//
// Step 2 and 3 run the backend's TestRecordSamples, so MongoDB must be up (pnpm db:up).
import { spawnSync } from 'node:child_process';
import { mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const specPath = join(root, 'contracts', 'platform.openapi.yaml');
const workDir = join(root, '.cache', 'platform-contract');
const samplesDir = join(workDir, 'samples');

// Reuse the tooling the frontend already installs; nothing new is added to the workspace.
const fromFrontend = createRequire(join(root, 'frontend', 'package.json'));
const openapiTypescriptPath = fromFrontend.resolve('openapi-typescript');
const fromTool = createRequire(openapiTypescriptPath);
const { lint, bundle, createConfig } = fromTool('@redocly/openapi-core');
const ajvModule = fromTool('@redocly/ajv/dist/2020');
const Ajv2020 = ajvModule.default ?? ajvModule;
const ts = fromFrontend('typescript');

let failures = 0;
const fail = (message) => {
  failures += 1;
  console.error(`  FAIL ${message}`);
};
const pass = (message) => console.log(`  ok   ${message}`);

// sample name -> [schema in the contract, type expression in frontend/src/platform/api.ts or null]
const page = (type) => `Page<${type}>`;
const list = (type) => `{ items: ${type}[] }`;
const expectations = {
  'project.created': ['Project', 'Project'],
  'project.created3': ['Project', 'Project'],
  'project.nobudget': ['Project', 'Project'],
  'project.get': ['Project', 'Project'],
  'project.get2': ['Project', 'Project'],
  'project.get3': ['Project', 'Project'],
  'project.patched': ['Project', 'Project'],
  'project.patched2': ['Project', 'Project'],
  'projects.list': ['ProjectPage', page('Project')],
  'projects.all': ['ProjectPage', page('Project')],
  'budget.put': ['Budget', null],
  'key.created': ['CreatedApiKey', 'Key & { secret: string }'],
  'key.created2': ['CreatedApiKey', 'Key & { secret: string }'],
  'keys.list': ['ApiKeyPage', page('Key')],
  'keys.empty': ['ApiKeyPage', page('Key')],
  'upload.ticket': ['UploadTicket', 'Artifact & { uploadUrl: string }'],
  'upload.ready': ['Artifact', 'Artifact'],
  'artifacts.inputs': ['ArtifactPage', page('Artifact')],
  'artifacts.run': ['ArtifactPage', page('Artifact')],
  'artifacts.empty': ['ArtifactPage', page('Artifact')],
  models: ['ModelOptionList', list('ModelOption')],
  grants: ['GrantList', list('Grant')],
  'grants.empty': ['GrantList', list('Grant')],
  'agents.list': ['AgentList', list('Agent')],
  'agent.get': ['AgentDetail', '{ agent: Agent; versions: AgentVersion[] }'],
  'agent.get.code': ['AgentDetail', '{ agent: Agent; versions: AgentVersion[] }'],
  'agent.get.gpu': ['AgentDetail', '{ agent: Agent; versions: AgentVersion[] }'],
  'tools.list': ['ToolList', list('Tool')],
  'tool.get': ['Tool', 'Tool'],
  capabilities: ['Readiness', 'Readiness'],
  'run.created': ['Run', 'Run'],
  'run.created.c': ['Run', 'Run'],
  'run.created.d': ['Run', 'Run'],
  'run.created.e': ['Run', 'Run'],
  'run.created.f': ['Run', 'Run'],
  'run.queued': ['Run', 'Run'],
  'run.succeeded': ['Run', 'Run'],
  'run.failed': ['Run', 'Run'],
  'run.cancel-response': ['Run', 'Run'],
  'run.cancelled': ['Run', 'Run'],
  'run.unknown': ['Run', 'Run'],
  'runs.list': ['RunPage', page('Run')],
  'runs.empty': ['RunPage', page('Run')],
  'events.page': ['EventPage', 'EventPage'],
  'toolcalls.list': ['ToolCallList', list('ToolCall')],
  'toolcall.created': ['ToolCall', 'ToolCall'],
  'toolcall.get': ['ToolCall', 'ToolCall'],
  'toolcalls.standalone': ['ToolCallPage', page('ToolCall')],
  'preview.csv': ['Preview', 'Preview'],
  'preview.png': ['Preview', 'Preview'],
  'preview.xlsx': ['Preview', 'Preview'],
  me: ['Me', null],
  'usage.list': ['UsageLinePage', page('UsageLine')],
  'usage.storage': ['UsageLinePage', page('UsageLine')],
  'usage.empty': ['UsageLinePage', page('UsageLine')],
  rates: ['RateSheet', 'RateSheet'],
  presets: ['PriceList', '{ items: Price[] }'],
  'usage.summary': ['UsageSummary', 'UsageSummary'],
  'usage.summary.all': ['UsageSummary', 'UsageSummary'],
  'usage.summary.empty': ['UsageSummary', 'UsageSummary'],
  'ledger.list': ['LedgerPage', page('Ledger')],
  'ledger.empty': ['LedgerPage', page('Ledger')],
  overview: ['Overview', 'Overview'],
  'overview.empty': ['Overview', 'Overview'],
};

// ---------------------------------------------------------------- 1. the document itself
console.log('1. the contract');
const config = await createConfig({
  extends: ['recommended'],
  rules: {
    'info-license': 'off',
    'no-server-example.com': 'off',
    'operation-4xx-response': 'off',
    'no-unused-components': 'warn',
  },
});
const problems = await lint({ ref: specPath, config });
const errors = problems.filter((problem) => problem.severity === 'error');
const warnings = problems.filter((problem) => problem.severity === 'warn');
for (const problem of [...errors, ...warnings].slice(0, 30)) {
  const where = problem.location?.[0]?.pointer ?? '';
  console.error(
    `  ${problem.severity === 'error' ? 'FAIL' : 'warn'} ${problem.ruleId}: ${problem.message} ${where}`,
  );
}
if (errors.length) fail(`${errors.length} lint error(s)`);
else pass(`valid OpenAPI document${warnings.length ? ` (${warnings.length} warning(s))` : ''}`);

const { bundle: bundled } = await bundle({ ref: specPath, config });
const document = bundled.parsed;
const operations = Object.values(document.paths).flatMap((item) =>
  Object.entries(item)
    .filter(([method]) => ['get', 'put', 'post', 'patch', 'delete'].includes(method))
    .map(([, operation]) => operation),
);
pass(`${operations.length} operations across ${Object.keys(document.paths).length} paths`);

mkdirSync(workDir, { recursive: true });
try {
  const openapiTypescript = fromFrontend(openapiTypescriptPath);
  const output = openapiTypescript.astToString(
    await openapiTypescript.default(pathToFileURL(specPath)),
  );
  writeFileSync(join(workDir, 'platform-api.d.ts'), output);
  pass(
    `a typed client can be generated (${output.split('\n').length} lines, written to .cache/platform-contract)`,
  );
} catch (error) {
  fail(`client generation failed: ${error.message}`);
}

// ---------------------------------------------------------------- 2. the real responses
console.log('2. responses from the real backend');
rmSync(samplesDir, { recursive: true, force: true });
mkdirSync(samplesDir, { recursive: true });
const recorded = spawnSync(
  'go',
  ['-C', 'backend', 'test', './internal/platform', '-run', '^TestRecordSamples$', '-count=1'],
  {
    cwd: root,
    stdio: 'inherit',
    env: {
      ...process.env,
      AGENTLAB_SAMPLES_DIR: samplesDir,
      AGENTLAB_MONGO_URI:
        process.env.AGENTLAB_MONGO_URI ||
        'mongodb://127.0.0.1:27018/?replicaSet=rs0&directConnection=true',
      AGENTLAB_REQUIRE_MONGO_TESTS: '1',
    },
  },
);
if (recorded.status !== 0) {
  fail('the backend could not record its responses (is MongoDB up? pnpm db:up)');
  process.exit(1);
}
const names = readdirSync(samplesDir)
  .filter((file) => file.endsWith('.json'))
  .map((file) => file.slice(0, -5))
  .sort();
const unmapped = names.filter((name) => !(name in expectations));
const missing = Object.keys(expectations).filter((name) => !names.includes(name));
if (unmapped.length)
  fail(
    `recorded responses without an expectation: ${unmapped.join(', ')} (add them to scripts/check-platform-contract.mjs)`,
  );
if (missing.length) fail(`expected responses that were not recorded: ${missing.join(', ')}`);

// Reject undeclared properties as well as missing ones. Strictness is applied to this copy only, not to the
// published document, where clients should tolerate additions. A schema composed of several objects (allOf)
// is merged into one first: otherwise each part would reject the properties of the others.
const declared = document.components.schemas;
const merge = (schema) => {
  if (!Array.isArray(schema.allOf)) return schema;
  const merged = { type: 'object', properties: {}, required: [] };
  for (const part of schema.allOf) {
    const resolved = merge(
      part.$ref ? declared[part.$ref.replace('#/components/schemas/', '')] : part,
    );
    Object.assign(merged.properties, resolved.properties ?? {});
    merged.required.push(...(resolved.required ?? []));
  }
  const { allOf, ...rest } = schema;
  return { ...rest, ...merged, required: [...new Set(merged.required)] };
};
const strict = (node) => {
  if (Array.isArray(node)) return node.map(strict);
  if (node === null || typeof node !== 'object') return node;
  const copy = Object.fromEntries(
    Object.entries(node).map(([key, value]) => [
      key,
      key === 'properties'
        ? Object.fromEntries(Object.entries(value).map(([name, schema]) => [name, strict(schema)]))
        : strict(value),
    ]),
  );
  if (copy.properties && copy.additionalProperties === undefined)
    copy.unevaluatedProperties = false;
  return copy;
};
const ajv = new Ajv2020({ strict: false, allErrors: true, validateFormats: false });
ajv.addSchema({
  $id: 'contract',
  components: {
    schemas: Object.fromEntries(
      Object.entries(declared).map(([name, schema]) => [name, strict(merge(schema))]),
    ),
  },
});
let validated = 0;
for (const name of names.filter((candidate) => candidate in expectations)) {
  const [schemaName] = expectations[name];
  const validate = ajv.getSchema(`contract#/components/schemas/${schemaName}`);
  if (!validate) {
    fail(`${name}: the contract has no schema ${schemaName}`);
    continue;
  }
  const sample = JSON.parse(readFileSync(join(samplesDir, `${name}.json`), 'utf8'));
  if (validate(sample)) {
    validated += 1;
  } else {
    fail(`${name} does not match ${schemaName}`);
    for (const error of validate.errors.slice(0, 6))
      console.error(
        `         ${error.instancePath || '/'} ${error.message}${error.params?.unevaluatedProperty ? ` (${error.params.unevaluatedProperty})` : ''}${error.params?.missingProperty ? ` (${error.params.missingProperty})` : ''}`,
      );
  }
}
if (validated)
  pass(`${validated} of ${names.length} recorded responses match the contract exactly`);

// ---------------------------------------------------------------- 3. the workbench's own types
console.log('3. the types the workbench declares');
const typed = names.filter((name) => expectations[name]?.[1]);
const identifiers = new Set();
for (const name of typed)
  for (const match of expectations[name][1].matchAll(/\b[A-Z][A-Za-z]+\b/g))
    if (match[0] !== 'Page') identifiers.add(match[0]);
const lines = [
  'import type { Page, ' +
    [...identifiers].sort().join(', ') +
    " } from '../../frontend/src/platform/api';",
  'type Mutable<T> = { -readonly [K in keyof T]: Mutable<T[K]> };',
  'function accepts<T>(value: T): T {',
  '  return value;',
  '}',
];
for (const name of typed) {
  const json = readFileSync(join(samplesDir, `${name}.json`), 'utf8').trim();
  lines.push(
    `// sample: ${name}`,
    '{',
    `  const sample = ${json} as const;`,
    `  accepts<${expectations[name][1]}>(sample as Mutable<typeof sample>);`,
    '}',
  );
}
const checkFile = join(workDir, 'workbench-types.ts');
writeFileSync(checkFile, lines.join('\n') + '\n');
const frontendDir = join(root, 'frontend');
const tsconfig = ts.readConfigFile(join(frontendDir, 'tsconfig.json'), ts.sys.readFile);
const parsed = ts.parseJsonConfigFileContent(tsconfig.config, ts.sys, frontendDir);
const program = ts.createProgram([checkFile], {
  ...parsed.options,
  noEmit: true,
  noUnusedLocals: false,
});
const sourceLines = lines;
const diagnostics = ts
  .getPreEmitDiagnostics(program)
  .filter(
    (diagnostic) => diagnostic.file?.fileName.replace(/\\/g, '/') === checkFile.replace(/\\/g, '/'),
  );
for (const diagnostic of diagnostics.slice(0, 20)) {
  const at = diagnostic.file.getLineAndCharacterOfPosition(diagnostic.start).line;
  let sample = '?';
  for (let index = at; index >= 0; index -= 1)
    if (sourceLines[index].startsWith('// sample: ')) {
      sample = sourceLines[index].slice(11);
      break;
    }
  fail(
    `${sample}: ${ts.flattenDiagnosticMessageText(diagnostic.messageText, '\n').split('\n').slice(0, 6).join(' | ').slice(0, 700)}`,
  );
}
if (!diagnostics.length) pass(`${typed.length} responses satisfy the declared TypeScript types`);

console.log(
  failures
    ? `\n${failures} problem(s) found`
    : '\nthe contract, the backend and the workbench agree',
);
process.exit(failures ? 1 : 0);
