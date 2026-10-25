import { test as base, expect } from '@playwright/test';
import type { Route } from '@playwright/test';
import type {
  Agent,
  Artifact,
  Key,
  Project,
  Readiness,
  Run,
  UsageLine,
} from '../../src/platform/api';

const now = '2026-10-24T00:00:00Z';
export const project: Project = {
  id: 'proj_server',
  name: '服务端文件项目',
  slug: 'server-files',
  description: '来自后端的项目',
  environment: 'production',
  status: 'active',
  currency: 'USD',
  retentionDays: 30,
  createdAt: now,
  updatedAt: now,
  version: 1,
  role: 'owner',
  budget: {
    configured: true,
    currency: 'USD',
    limitMicros: '500000000',
    heldMicros: '2000000',
    spentMicros: '1',
    availableMicros: '497999999',
    activeRuns: 1,
    maxConcurrentRuns: 2,
    version: 1,
  },
  counts: { apiKeys: 0, connections: 1, agents: 1 },
};
export const agent: Agent = {
  id: 'agent_server',
  name: '文件分析 Agent',
  description: '发布版本来自服务端',
  status: 'published',
  latestVersionId: 'av_server_1',
  createdAt: now,
  latestVersion: {
    id: 'av_server_1',
    agentId: 'agent_server',
    version: '1.0.0',
    status: 'published',
    digest: 'digest',
    promptDigest: 'prompt-digest',
    tools: [{ name: 'files.transform', version: '1.0.0' }],
    outputSchema: { type: 'object' },
    inputFormats: ['csv', 'xlsx'],
    sandbox: 'none',
    createdAt: now,
    limits: {
      defaultExecutionSeconds: 120,
      maxExecutionSeconds: 300,
      defaultToolCalls: 5,
      maxToolCalls: 10,
      maxInputFiles: 4,
      maxInputBytes: 1048576,
      maxTokensPerStep: 4000,
    },
  },
};
export const run: Run = {
  id: 'run_server',
  projectId: project.id,
  status: 'running',
  blockedReason: null,
  cancelRequested: false,
  agent: { id: agent.id, name: agent.name, versionId: agent.latestVersionId, version: '1.0.0' },
  trigger: 'workbench',
  inputSummary: '分析订单',
  inputArtifactIds: [],
  model: {
    connectionId: 'conn_server',
    connectionName: '服务端模型账户',
    model: 'test-model',
    source: 'byok',
  },
  limits: { maxExecutionSeconds: 120, maxToolCalls: 5, maxCostMicros: '2000000', currency: 'USD' },
  createdAt: now,
  startedAt: now,
  finishedAt: null,
  durationMs: 1250,
  costMicros: null,
  currency: 'USD',
  billingStatus: 'pending',
  reservedMicros: '2000000',
  usage: {
    modelSteps: 1,
    toolCalls: 0,
    inputTokens: null,
    outputTokens: null,
    executionSeconds: 2,
  },
  result: null,
  outputArtifactIds: [],
  error: null,
};
export type Capture = {
  path: string;
  url: string;
  method: string;
  body: Record<string, unknown>;
  key: string;
  product: string;
  raw: Buffer | null;
};
export type MockPlatform = {
  projects: Project[];
  files: Artifact[];
  keys: Key[];
  runs: Run[];
  requests: Capture[];
  readiness: Readiness;
  projectFailure: number;
  submitFailures: number;
  usage: UsageLine[];
};
export const test = base.extend<{ platform: MockPlatform }>({
  platform: [
    async ({ page }, use) => {
      const state: MockPlatform = {
        projects: [
          structuredClone(project),
          {
            ...structuredClone(project),
            id: 'proj_other',
            name: '另一个服务端项目',
            slug: 'other',
          },
        ],
        files: [],
        keys: [],
        runs: [structuredClone(run)],
        requests: [],
        projectFailure: 0,
        submitFailures: 0,
        readiness: {
          ready: true,
          checks: [
            { name: 'database', ok: true },
            { name: 'storage', ok: true },
          ],
          capabilities: {
            runs: true,
            codeExecution: false,
            codeExecutionReason: '此部署没有配置代码执行沙箱。',
            maxUploadBytes: 1048576,
            maxRunInputBytes: 1048576,
            workerConcurrency: 2,
          },
        },
        usage: [
          {
            id: 'usage_byok',
            projectId: project.id,
            runId: run.id,
            category: 'model',
            meter: 'tokens',
            source: 'BYOK · test-model',
            quantity: '0',
            unit: 'tokens',
            amountMicros: null,
            currency: 'USD',
            status: 'not_applicable',
            basis: 'unknown',
            note: '供应商未返回用量',
            at: now,
          },
        ],
      };
      const json = (route: Route, body: unknown, status = 200) =>
        route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
      await page.route('**/api/v1/auth/me', (route) =>
        json(route, {
          user: {
            id: 'user_server',
            tenantId: 'local',
            username: 'developer',
            displayName: '开发者',
            role: 'admin',
            status: 'active',
            version: 1,
            canManage: true,
            email: '',
            studentId: '',
            createdAt: now,
          },
          permissions: {
            catalogRead: [],
            recordsRead: [],
            catalogWrite: true,
            manageUsers: true,
            environment: true,
          },
        }),
      );
      await page.route('**/api/v1/connections', (route) =>
        json(route, [
          {
            id: 'conn_server',
            ownerId: 'user_server',
            scope: 'private',
            name: '服务端模型账户',
            provider: 'openai-compatible',
            models: ['test-model'],
            status: 'active',
            generation: 1,
            version: 1,
            expiresAt: null,
            verifiedAt: now,
            createdAt: now,
            updatedAt: now,
          },
        ]),
      );
      await page.route('**/api/v1/platform/**', async (route) => {
        const request = route.request();
        const url = new URL(request.url());
        const path = url.pathname.replace('/api/v1/platform', '');
        const method = request.method();
        const body = request.headers()['content-type']?.includes('application/json')
          ? ((request.postDataJSON() as Record<string, unknown>) ?? {})
          : {};
        state.requests.push({
          path,
          url: request.url(),
          method,
          body,
          key: request.headers()['idempotency-key'] ?? '',
          product: request.headers()['x-agentlab-request'] ?? '',
          raw: request.postDataBuffer(),
        });
        const current =
          state.projects.find((item) => item.id === url.searchParams.get('projectId')) ??
          state.projects[0] ??
          project;
        if (path === '/capabilities') return json(route, state.readiness);
        if (path === '/me')
          return json(route, {
            kind: 'user',
            userId: 'user_server',
            username: 'developer',
            displayName: '开发者',
            admin: true,
            workspace: { id: 'local', name: '文件分析工作区' },
          });
        if (path === '/billing/rates') return json(route, { execution: null, resources: [] });
        if (path === '/admin/prices') return json(route, { items: [] });
        if (path.startsWith('/connections/') && path.endsWith('/projects'))
          return json(route, { items: [] });
        if (path === '/projects' && method === 'GET') {
          if (state.projectFailure)
            return json(
              route,
              {
                code: state.projectFailure === 401 ? 'UNAUTHENTICATED' : 'INTERNAL_ERROR',
                message: '平台暂时不可用',
              },
              state.projectFailure,
            );
          if (url.searchParams.get('limit') === '100')
            return json(route, { items: state.projects });
          if (url.searchParams.has('cursor') && url.searchParams.get('cursor'))
            return json(route, { items: state.projects.slice(1) });
          return json(route, {
            items: state.projects.slice(0, 1),
            ...(state.projects.length > 1 ? { nextCursor: 'projects-page-2' } : {}),
          });
        }
        if (path === '/projects' && method === 'POST') {
          const created = {
            ...structuredClone(project),
            id: 'proj_created',
            name: String(body.name),
            description: String(body.description),
            currency: body.currency as Project['currency'],
            budget: {
              ...project.budget,
              currency: body.currency as Project['currency'],
              limitMicros: String(body.budgetMicros),
            },
          };
          state.projects.push(created);
          return json(route, created, 201);
        }
        if (/^\/projects\/[^/]+$/.test(path)) {
          const selected = state.projects.find((item) => item.id === path.split('/')[2]);
          if (!selected)
            return json(route, { code: 'NOT_FOUND', message: '未找到或无权访问' }, 404);
          if (method === 'PATCH') {
            selected.status = body.status as Project['status'];
            selected.version++;
          }
          return json(route, selected);
        }
        if (path.endsWith('/budget')) return json(route, { ...current.budget, ...body });
        if (/^\/projects\/[^/]+\/connections\/[^/]+$/.test(path))
          return json(route, { projectId: path.split('/')[2], connectionId: path.split('/')[4] });
        if (path.endsWith('/connections')) return json(route, { items: [] });
        if (path === '/agents') return json(route, { items: [agent] });
        if (path.startsWith('/agents/'))
          return json(route, { agent, versions: [agent.latestVersion] });
        if (path === '/models')
          return json(route, {
            items: [
              {
                connectionId: 'conn_server',
                connectionName: '服务端模型账户',
                provider: 'openai-compatible',
                model: 'test-model',
                source: 'byok',
                status: 'active',
                usable: true,
                priced: false,
                toolCalling: 'unverified',
                verifiedAt: now,
              },
            ],
          });
        if (path === '/files' && method === 'POST') {
          const artifact: Artifact = {
            id: 'file_uploaded',
            projectId: String(body.projectId),
            name: String(body.name),
            type: 'csv',
            kind: 'input',
            mediaType: 'text/csv',
            sizeBytes: Number(body.size),
            sha256: String(body.sha256),
            status: 'pending',
            summary: '上传文件',
            retentionDays: 30,
            createdAt: now,
            expiresAt: '2099-11-01T00:00:00Z',
          };
          state.files.push(artifact);
          return json(
            route,
            { ...artifact, uploadUrl: '/api/v1/platform/files/file_uploaded/content' },
            201,
          );
        }
        if (path === '/files/file_uploaded/content') {
          state.files[0]!.status = 'ready';
          return json(route, state.files[0]);
        }
        if (path === '/artifacts')
          return json(route, {
            items: state.files.filter(
              (item) =>
                item.projectId === current.id &&
                (!url.searchParams.get('runId') || item.runId === url.searchParams.get('runId')),
            ),
          });
        if (path.endsWith('/preview'))
          return json(route, {
            type: 'csv',
            name: 'orders.csv',
            columns: ['name', 'amount'],
            rows: [['<script>alert(1)</script>', '10']],
            truncated: true,
          });
        if (path.endsWith('/download-link'))
          return json(route, {
            url: '/api/v1/platform/download/test-token',
            expiresAt: '2099-11-01T00:00:00Z',
          });
        if (path === '/download/test-token')
          return route.fulfill({
            contentType: 'text/csv',
            headers: { 'Content-Disposition': 'attachment; filename="orders.csv"' },
            body: 'name,amount\na,10\n',
          });
        if (path === '/runs' && method === 'POST') {
          if (state.submitFailures > 0) {
            state.submitFailures--;
            return json(route, { code: 'SERVICE_UNAVAILABLE', message: '请使用同一请求重试' }, 503);
          }
          const created = {
            ...structuredClone(run),
            id: 'run_created',
            projectId: String(body.projectId),
            status: 'queued' as const,
          };
          state.runs.push(created);
          return json(route, created, 202);
        }
        if (path === '/runs')
          return json(route, { items: state.runs.filter((item) => item.projectId === current.id) });
        if (path.endsWith('/events')) {
          const after = Number(url.searchParams.get('after') || 0);
          return json(route, {
            items: after
              ? []
              : [
                  {
                    id: 'event_server',
                    seq: 1,
                    at: now,
                    level: 'info',
                    source: 'worker',
                    type: 'run.started',
                    message: '服务端持久事件',
                  },
                ],
            cursor: Math.max(1, after),
            terminal: false,
            status: 'running',
          });
        }
        if (path.endsWith('/tool-calls')) return json(route, { items: [] });
        if (path.startsWith('/runs/')) {
          const item = state.runs.find((item) => item.id === path.split('/')[2]);
          if (!item) return json(route, { code: 'NOT_FOUND', message: '未找到或无权访问' }, 404);
          if (path.endsWith('/cancel')) {
            item.status = 'cancelling';
            item.cancelRequested = true;
          }
          return json(route, item);
        }
        if (path === '/api-keys' && method === 'POST') {
          const item: Key = {
            id: 'key_server',
            projectId: current.id,
            name: String(body.name),
            prefix: 'al_live_test',
            scopes: body.scopes as string[],
            status: 'active',
            state: 'active',
            expiresAt: body.expiresAt as string | null,
            lastUsedAt: null,
            monthlyLimitMicros: body.monthlyLimitMicros as string | null,
            currency: current.currency,
            ratePerMinute: Number(body.ratePerMinute),
            createdAt: now,
          };
          state.keys.push(item);
          return json(route, { ...item, secret: 'al_live_test_once_only_secret' }, 201);
        }
        if (path === '/api-keys') return json(route, { items: state.keys });
        if (path.endsWith('/revoke')) {
          state.keys[0]!.status = 'revoked';
          state.keys[0]!.state = 'revoked';
          return json(route, state.keys[0]);
        }
        if (path === '/usage')
          return json(route, {
            items:
              url.searchParams.get('category') === 'storage'
                ? [
                    {
                      id: 'usage_storage',
                      projectId: current.id,
                      category: 'storage',
                      meter: 'storage_mib_days',
                      source: '平台存储 · 每日采样',
                      quantity: '1',
                      unit: 'mib_day',
                      amountMicros: null,
                      currency: current.currency,
                      status: 'not_applicable',
                      basis: 'sampled',
                      note: '含在基础额度内，不单独计费。',
                      at: now,
                    },
                  ]
                : state.usage,
          });
        if (path === '/usage/summary')
          return json(route, {
            currency: current.currency,
            settledMicros: '1',
            pendingMicros: '0',
            groups: [
              {
                category: 'model',
                unit: 'tokens',
                status: 'not_applicable',
                quantity: '0',
                quantityKnown: true,
                amountMicros: null,
                amountKnown: true,
                lines: 1,
                note: '模型费用由客户在供应商处承担。',
              },
              {
                category: 'storage',
                unit: 'mib_day',
                status: 'not_applicable',
                quantity: '1',
                quantityKnown: true,
                amountMicros: null,
                amountKnown: true,
                lines: 1,
                note: '含在基础额度内，不单独计费。',
              },
            ],
          });
        if (path === '/billing/ledger')
          return json(route, {
            items: [
              {
                id: 'ledger_server',
                projectId: current.id,
                runId: run.id,
                scope: 'project',
                type: 'reserve',
                amountMicros: '2000000',
                currency: current.currency,
                note: '服务端预算预占',
                at: now,
              },
            ],
          });
        if (path === '/overview')
          return json(route, {
            project: current,
            counts: { running: 1 },
            active: state.runs,
            attention: [],
            recent: state.runs,
            since: now,
          });
        if (path === '/tools') return json(route, { items: [] });
        return json(route, { code: 'UNMOCKED_REQUEST', message: `${method} ${path}` }, 500);
      });
      await use(state);
    },
    { auto: true },
  ],
});
export { expect };
