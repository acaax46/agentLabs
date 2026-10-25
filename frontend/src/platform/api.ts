import { APIError } from '../api/client';
import type { BillingStatus, Currency, RunStatus, ToolCallStatus } from './model';

// Views follow backend/internal/platform; the generated client describes the legacy API.
export type Page<T> = { items: T[]; nextCursor?: string };
export type Workspace = {
  id: string;
  name: string;
};
export type Me = {
  kind: 'user' | 'api_key';
  userId?: string;
  username?: string;
  displayName?: string;
  admin: boolean;
  keyId?: string;
  keyName?: string;
  projectId?: string;
  scopes?: string[];
  workspace: Workspace;
};
export type Budget = {
  configured: boolean;
  currency: Currency;
  limitMicros: string;
  heldMicros: string;
  spentMicros: string;
  availableMicros: string;
  activeRuns: number;
  maxConcurrentRuns: number;
  version: number;
};
export type Project = {
  id: string;
  name: string;
  slug: string;
  description: string;
  environment: 'production' | 'staging';
  status: 'active' | 'paused';
  currency: Currency;
  retentionDays: number;
  createdAt: string;
  updatedAt: string;
  version: number;
  role: 'owner' | 'member' | 'admin';
  budget: Budget;
  counts?: { apiKeys: number; connections: number; agents: number };
};
export type AgentVersion = {
  id: string;
  agentId: string;
  version: string;
  status: string;
  digest: string;
  promptDigest: string;
  tools: { name: string; version: string }[];
  outputSchema: unknown;
  inputFormats: string[];
  sandbox: string;
  /** Allocations this version may use, in order of preference; absent means CPU profiles only. */
  resourceProfiles?: string[];
  /** Code-agent CLIs a run may choose, the first being the default; absent means the builtin loop. */
  harnesses?: HarnessId[];
  createdAt: string;
  limits: {
    defaultExecutionSeconds: number;
    maxExecutionSeconds: number;
    defaultToolCalls: number;
    maxToolCalls: number;
    maxInputFiles: number;
    maxInputBytes: number;
    maxTokensPerStep: number;
  };
};
export type Agent = {
  id: string;
  projectId?: string;
  name: string;
  description: string;
  status: string;
  latestVersionId: string;
  latestVersion: AgentVersion | null;
  createdAt: string;
};
export type ModelOption = {
  connectionId: string;
  connectionName: string;
  provider: string;
  model: string;
  source: 'platform' | 'byok';
  status: string;
  usable: boolean;
  reason?: string;
  priced: boolean;
  price?: Price | null;
  verifiedAt: string | null;
  toolCalling: string;
};
export type Price = {
  id: string;
  kind: 'model' | 'execution' | 'sandbox' | 'tool';
  name: string;
  currency: Currency;
  provider?: string;
  model?: string;
  resourceProfile?: string;
  sandboxBilling?: string;
  inputMicros: string;
  cachedInputMicros: string;
  outputMicros: string;
  perSecondMicros: string;
  perUnitMicros: string;
  tool?: string;
  unit?: string;
  note: string;
  referenceUrl?: string;
  referenceAt?: string;
  effectiveFrom: string;
};
export type ResourceProfile = {
  id: string;
  name: string;
  cpu: number;
  memoryMB: number;
  gpu: number;
  gpuType?: string;
  gpuModel?: string;
};
export type RateSheet = {
  execution: Price | null;
  resources: (ResourceProfile & { available: boolean; reason?: string; price: Price | null })[];
};
export type Grant = {
  projectId: string;
  projectName: string;
  connectionId: string;
  connectionName: string;
  provider: string;
  models: string[];
  status: string;
  source: 'platform' | 'byok';
  createdAt: string;
};
export type Key = {
  id: string;
  projectId: string;
  name: string;
  prefix: string;
  scopes: string[];
  status: 'active' | 'revoked';
  state: 'active' | 'revoked' | 'expired' | 'expiring';
  expiresAt: string | null;
  lastUsedAt: string | null;
  monthlyLimitMicros: string | null;
  currency: Currency;
  ratePerMinute: number;
  createdAt: string;
};
export type HarnessId = 'builtin' | 'pi' | 'opencode' | 'codex' | 'claude-code';
export type HarnessRef = { id: HarnessId; name: string; version: string };
export type HarnessStatus = HarnessRef & {
  protocol: 'chat' | 'responses' | 'messages';
  description: string;
  available: boolean;
  reason?: string;
};
export type Run = {
  conversationId?: string;
  conversationTurn?: number;
  sandboxImage?: string;
  harness?: HarnessRef;
  resources?: ResourceProfile | null;
  prices?: { model?: string; execution: string; sandbox?: string; tools: Record<string, string> };
  id: string;
  projectId: string;
  status: RunStatus;
  blockedReason: string | null;
  cancelRequested: boolean;
  agent: { id: string; name: string; versionId: string; version: string };
  trigger: 'workbench' | 'api';
  actor?: { kind: string; id: string; name: string };
  inputSummary: string;
  instruction?: string;
  inputArtifactIds: string[];
  model: {
    connectionId: string;
    connectionName: string;
    model: string;
    source: 'platform' | 'byok';
  };
  limits: {
    maxExecutionSeconds: number;
    maxToolCalls: number;
    maxCostMicros: string;
    currency: Currency;
  };
  createdAt: string;
  startedAt: string | null;
  finishedAt: string | null;
  durationMs: number | null;
  costMicros: string | null;
  currency: Currency;
  billingStatus: BillingStatus;
  reservedMicros: string;
  usage: {
    modelSteps: number;
    toolCalls: number;
    inputTokens: number | null;
    outputTokens: number | null;
    executionSeconds: number | null;
  };
  result: unknown;
  outputArtifactIds: string[];
  error: { code: string; message: string; nextStep: string } | null;
};
export type RunEvent = {
  id: string;
  seq: number;
  at: string;
  level: string;
  source: string;
  type: string;
  message: string;
  data?: { step?: number; [key: string]: unknown };
};

export type Conversation = {
  id: string;
  projectId: string;
  title: string;
  agentVersionId: string;
  modelConnectionId: string;
  model: string;
  resourceProfile: string;
  sandboxImage: string;
  harness: HarnessRef;
  limits: {
    maxExecutionSeconds: number;
    maxToolCalls: number;
    maxCostMicros: string;
    currency: Currency;
  };
  latestRunId: string;
  turnCount: number;
  version: number;
  createdAt: string;
  updatedAt: string;
};
export type ConversationDetail = Conversation & { runs: Run[] };
export type EventPage = { items: RunEvent[]; cursor: number; terminal: boolean; status: RunStatus };
export type ToolCall = {
  id: string;
  name: string;
  version: string;
  status: ToolCallStatus;
  inputSummary: string;
  outputSummary: string;
  startedAt: string | null;
  durationMs: number | null;
  costMicros: string | null;
  currency?: Currency;
  billingStatus: BillingStatus;
  seq?: number;
  billedTo?: string;
  sandbox?: string;
};
export type StandaloneCall = ToolCall & {
  currency: Currency;
  projectId: string;
  runId?: string;
  standalone: boolean;
  createdAt: string;
  finishedAt: string | null;
  artifactIds: string[];
  result?: unknown;
  unit: string;
  quantity: string;
  error: Run['error'];
};
export type Artifact = {
  id: string;
  projectId: string;
  kind: 'input' | 'output';
  name: string;
  type: string;
  mediaType: string;
  sizeBytes: number;
  sha256: string;
  status: 'pending' | 'ready' | 'expired' | 'failed';
  runId?: string;
  toolCallId?: string;
  summary: string;
  retentionDays: number;
  createdAt: string;
  expiresAt: string;
};
export type Preview = {
  type: string;
  name: string;
  text?: string;
  columns?: string[];
  rows?: string[][];
  truncated: boolean;
};
export type UsageLine = {
  id: string;
  projectId: string;
  runId?: string;
  category: 'model' | 'tool' | 'execution' | 'adjustment' | 'storage';
  meter: string;
  source: string;
  quantity: string;
  unit: string;
  amountMicros: string | null;
  currency: Currency;
  status: BillingStatus;
  basis: string;
  note: string;
  at: string;
  keyId?: string;
  priceId?: string;
};
export type Ledger = {
  id: string;
  projectId: string;
  runId?: string;
  scope: string;
  accountId?: string;
  type: 'reserve' | 'settle' | 'release' | 'adjustment';
  amountMicros: string;
  currency: Currency;
  note: string;
  at: string;
};
export type UsageSummary = {
  currency: Currency;
  settledMicros: string;
  pendingMicros: string;
  groups: {
    category: string;
    unit: string;
    status: BillingStatus;
    quantity: string;
    amountMicros: string | null;
    quantityKnown?: boolean;
    amountKnown?: boolean;
    lines: number;
    note: string;
  }[];
};
export type Tool = {
  billingPolicy?: string;
  id: string;
  name: string;
  version: string;
  category: string;
  summary: string;
  description: string;
  inputSchema: unknown;
  outputSchema: unknown;
  risk: 'low' | 'medium' | 'high';
  requiresApproval: boolean;
  sideEffects: string;
  retry: string;
  meters: { unit: string; description: string }[];
  status: 'available' | 'beta' | 'disabled';
  sample: unknown;
  prices: {
    id: string;
    currency: Currency;
    unit: string;
    perUnitMicros: string;
    effectiveFrom: string;
  }[];
};
export type Readiness = {
  ready: boolean;
  checks: { name: string; ok: boolean; detail?: string }[];
  capabilities: {
    modules?: Record<string, boolean>;
    runs: boolean;
    codeExecution: boolean;
    codeExecutionReason?: string;
    isolation?: string;
    maxUploadBytes: number;
    maxRunInputBytes: number;
    workerConcurrency: number;
    harnesses?: HarnessStatus[];
  };
};
export type Overview = {
  project: Project;
  counts: Record<string, number>;
  active: Run[];
  attention: Run[];
  recent: Run[];
  since: string;
};

export const platformRoot = '/api/v1/platform';
type Query = Record<string, string | number | undefined>;
export function platformPath(path: string, query: Query = {}) {
  const params = new URLSearchParams();
  Object.entries(query).forEach(([key, value]) => {
    if (value !== undefined && value !== '') params.set(key, String(value));
  });
  return `${platformRoot}${path}${params.size ? `?${params}` : ''}`;
}
export async function platformRequest<T>(path: string, init: RequestInit = {}): Promise<T> {
  if (window.go?.app?.Desktop)
    throw new APIError(
      '当前桌面端尚未提供平台原生接口，请在浏览器工作台使用此功能。',
      'PLATFORM_UNAVAILABLE',
    );
  let response: Response;
  try {
    response = await fetch(path, {
      ...init,
      credentials: 'same-origin',
      cache: 'no-store',
      signal: init.signal
        ? AbortSignal.any([init.signal, AbortSignal.timeout(30_000)])
        : AbortSignal.timeout(init.body instanceof File ? 120_000 : 30_000),
      headers: { Accept: 'application/json', 'X-AgentLab-Request': 'platform', ...init.headers },
    });
  } catch (error) {
    if (init.signal?.aborted) throw error;
    throw new APIError('无法连接平台服务。写入结果可能尚未确认，请核对资源后使用同一请求重试。');
  }
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    throw new APIError(
      body.message || `平台请求失败（${response.status}）。`,
      body.code || `HTTP_${response.status}`,
      body.fields,
      body.requestId || response.headers.get('X-Request-ID') || '',
    );
  }
  if (response.status === 204) return undefined as T;
  if (!response.headers.get('Content-Type')?.includes('application/json'))
    throw new APIError('此服务未提供平台接口，请检查 API 地址和后端版本。', 'PLATFORM_UNAVAILABLE');
  return response.json() as Promise<T>;
}
export function get<T>(path: string, query: Query = {}, signal?: AbortSignal) {
  return platformRequest<T>(platformPath(path, query), { signal });
}
export function write<T>(path: string, body: unknown, key?: string, method = 'POST') {
  return platformRequest<T>(platformPath(path), {
    method,
    headers: { 'Content-Type': 'application/json', ...(key ? { 'Idempotency-Key': key } : {}) },
    body: JSON.stringify(body),
  });
}
export async function all<T>(path: string, query: Query = {}, signal?: AbortSignal): Promise<T[]> {
  const items: T[] = [];
  let cursor: string | undefined;
  const seen = new Set<string>();
  do {
    const page = await get<Page<T>>(path, { ...query, limit: 100, cursor }, signal);
    items.push(...(page.items ?? []));
    cursor = page.nextCursor;
    if (cursor && seen.has(cursor))
      throw new APIError('服务返回了重复分页游标，请刷新后重试。', 'INVALID_CURSOR');
    if (cursor) seen.add(cursor);
  } while (cursor);
  return items;
}
// Identical payloads keep their key after a timeout; writes are never retried automatically.
export function requestKey() {
  let previous = '';
  let key = '';
  return (input: unknown) => {
    const serialized = JSON.stringify(input);
    if (!key || serialized !== previous) {
      previous = serialized;
      key = crypto.randomUUID();
    }
    return key;
  };
}
export async function uploadFile(
  projectId: string,
  file: File,
  keyForInput: ReturnType<typeof requestKey>,
) {
  const hash = await crypto.subtle.digest('SHA-256', await file.arrayBuffer());
  const sha256 = Array.from(new Uint8Array(hash), (byte) =>
    byte.toString(16).padStart(2, '0'),
  ).join('');
  const input = { projectId, name: file.name, size: file.size, sha256 };
  const ticket = await write<Artifact & { uploadUrl: string }>('/files', input, keyForInput(input));
  if (!ticket.uploadUrl.startsWith(`${platformRoot}/files/`))
    throw new APIError('上传地址不属于平台文件接口。', 'INVALID_UPLOAD_URL');
  return platformRequest<Artifact>(ticket.uploadUrl, {
    method: 'PUT',
    body: file,
    headers: { 'Content-Type': 'application/octet-stream' },
  });
}
export async function fetchArtifactDownload(id: string) {
  const link = await write<{ url: string; expiresAt: string }>(
    `/artifacts/${encodeURIComponent(id)}/download-link`,
    {},
  );
  if (!link.url.startsWith(`${platformRoot}/download/`))
    throw new APIError('下载地址不属于平台下载接口。', 'INVALID_DOWNLOAD_URL');
  let response: Response;
  try {
    response = await fetch(link.url, {
      credentials: 'same-origin',
      cache: 'no-store',
      signal: AbortSignal.timeout(120_000),
    });
  } catch {
    throw new APIError('下载未完成，请重新获取下载授权后重试。');
  }
  if (!response.ok) {
    const error = await response.json().catch(() => ({}));
    throw new APIError(error.message || '文件下载失败，授权可能已到期。', error.code);
  }
  return response.blob();
}
export async function downloadArtifact(id: string, name: string) {
  const url = URL.createObjectURL(await fetchArtifactDownload(id));
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = name;
  anchor.rel = 'noreferrer';
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 30_000);
}

export async function artifactBlob(id: string, signal?: AbortSignal) {
  const response = await fetch(platformPath(`/artifacts/${encodeURIComponent(id)}/content`), {
    credentials: 'same-origin',
    cache: 'no-store',
    signal,
  });
  if (!response.ok) {
    const error = await response.json().catch(() => ({}));
    throw new APIError(
      error.message || '无法读取产物。',
      error.code,
      error.fields,
      error.requestId,
    );
  }
  return response.blob();
}
