import type { components } from './generated';

export type Bootstrap = components['schemas']['Bootstrap'];
export type Template = components['schemas']['Template'];
export type CatalogKind = components['schemas']['CatalogKind'];
export type CatalogInput = components['schemas']['CatalogInput'];
export type CatalogRecord = components['schemas']['CatalogRecord'];
export type CatalogPage = components['schemas']['CatalogPage'];
export type CatalogSummary = components['schemas']['CatalogSummary'];
export type Sample = components['schemas']['Sample'];
export type AuthUser = components['schemas']['AuthUser'];
export type AuthState = components['schemas']['AuthState'];
export type Role = components['schemas']['Role'];
export type RegisterInput = components['schemas']['RegisterInput'];
export type CreateUserInput = components['schemas']['CreateUserInput'];
export type UpdateUserInput = components['schemas']['UpdateUserInput'];
export type UserPage = components['schemas']['UserPage'];
type NativeAuthResponse = {
  state?: AuthState;
  user?: AuthUser;
  page?: UserPage;
  error?: { code: string; message: string; fields?: Record<string, string> };
};
export type RuntimeRecord = components['schemas']['RuntimeRecord'];
export type RuntimeRecordPage = components['schemas']['RuntimeRecordPage'];
export type RuntimeRecordKind = components['schemas']['RuntimeRecordKind'];
export type RuntimeQuery = Record<string, string | number | undefined>;
type NativeRuntimeResponse = {
  record?: RuntimeRecord;
  page?: RuntimeRecordPage;
  error?: { code: string; message: string };
};
export type ListQuery = {
  q?: string;
  status?: string;
  difficulty?: string;
  classId?: string;
  page?: number;
  pageSize?: number;
};
type NativeResponse = {
  record?: CatalogRecord;
  page?: CatalogPage;
  summary?: CatalogSummary;
  error?: { code: string; message: string; fields?: Record<string, string> };
};
interface NativeDesktop {
  Connections: (input: {
    action: string;
    id: string;
    key: string;
    payload: unknown;
  }) => Promise<{ data?: unknown; error?: { code: string; message: string } }>;
  Learning: (input: {
    action: string;
    id: string;
    key: string;
    payload: unknown;
  }) => Promise<{ data?: unknown; error?: { code: string; message: string } }>;
  Training: (input: {
    action: string;
    id: string;
    key: string;
    payload: unknown;
  }) => Promise<{ data?: unknown; error?: { code: string; message: string } }>;
  GetAuthState: () => Promise<NativeAuthResponse>;
  Login: (username: string, password: string) => Promise<NativeAuthResponse>;
  Register: (input: RegisterInput) => Promise<NativeAuthResponse>;
  Logout: () => Promise<NativeAuthResponse>;
  ChangePassword: (current: string, password: string) => Promise<NativeAuthResponse>;
  ListUsers: (q: string, page: number) => Promise<NativeAuthResponse>;
  CreateUser: (input: CreateUserInput) => Promise<NativeAuthResponse>;
  UpdateUser: (id: string, input: UpdateUserInput) => Promise<NativeAuthResponse>;
  ResetUserPassword: (id: string, password: string) => Promise<NativeAuthResponse>;
  ListRuntimeRecords: (
    kind: RuntimeRecordKind,
    query: RuntimeQuery,
  ) => Promise<NativeRuntimeResponse>;
  GetRuntimeRecord: (kind: RuntimeRecordKind, id: string) => Promise<NativeRuntimeResponse>;
  GetBootstrap: () => Promise<Bootstrap>;
  GetCatalogSummary: () => Promise<NativeResponse>;
  ListCatalogRecords: (kind: CatalogKind, query: ListQuery) => Promise<NativeResponse>;
  GetCatalogRecord: (kind: CatalogKind, id: string) => Promise<NativeResponse>;
  CreateCatalogRecord: (
    kind: CatalogKind,
    input: CatalogInput,
    key: string,
  ) => Promise<NativeResponse>;
  UpdateCatalogRecord: (
    kind: CatalogKind,
    id: string,
    version: number,
    input: CatalogInput,
    key: string,
  ) => Promise<NativeResponse>;
  DeleteCatalogRecord: (
    kind: CatalogKind,
    id: string,
    version: number,
    key: string,
  ) => Promise<NativeResponse>;
}
declare global {
  interface Window {
    go?: { app?: { Desktop?: NativeDesktop } };
  }
}

export class APIError extends Error {
  constructor(
    message: string,
    public code = 'NETWORK_ERROR',
    public fields: Record<string, string> = {},
    public requestId = '',
  ) {
    super(message);
    this.name = 'APIError';
    if (code === 'UNAUTHENTICATED') window.dispatchEvent(new Event('agentlab:unauthenticated'));
  }
}
function native<T>(response: NativeResponse, key: 'record' | 'page' | 'summary'): T {
  if (response.error)
    throw new APIError(response.error.message, response.error.code, response.error.fields);
  return response[key] as T;
}
async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  let response: Response;
  try {
    response = await fetch(path, {
      ...init,
      credentials: 'same-origin',
      signal: AbortSignal.timeout(15000),
      headers: { Accept: 'application/json', ...init.headers },
    });
  } catch {
    throw new APIError('暂时无法连接服务。保存结果可能尚未确认，请先核对列表后重试。');
  }
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    throw new APIError(
      body.message || '操作未完成，请稍后重试。',
      body.code,
      body.fields,
      body.requestId || response.headers.get('X-Request-ID') || '',
    );
  }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}
export async function getBootstrap(): Promise<Bootstrap> {
  const d = window.go?.app?.Desktop;
  return d ? d.GetBootstrap() : request('/api/v1/bootstrap');
}
export async function getSummary(): Promise<CatalogSummary> {
  const d = window.go?.app?.Desktop;
  return d ? native(await d.GetCatalogSummary(), 'summary') : request('/api/v1/catalog/summary');
}
export async function listRecords(kind: CatalogKind, query: ListQuery = {}): Promise<CatalogPage> {
  const d = window.go?.app?.Desktop;
  if (d) return native(await d.ListCatalogRecords(kind, query), 'page');
  const params = new URLSearchParams();
  Object.entries(query).forEach(([key, value]) => {
    if (value !== undefined && value !== '') params.set(key, String(value));
  });
  return request(`/api/v1/${kind}?${params}`);
}
export async function getRecord(kind: CatalogKind, id: string): Promise<CatalogRecord> {
  const d = window.go?.app?.Desktop;
  return d
    ? native(await d.GetCatalogRecord(kind, id), 'record')
    : request(`/api/v1/${kind}/${encodeURIComponent(id)}`);
}
export async function saveRecord(
  kind: CatalogKind,
  input: CatalogInput,
  key: string,
  record?: CatalogRecord,
): Promise<CatalogRecord> {
  const d = window.go?.app?.Desktop;
  if (d)
    return native(
      record
        ? await d.UpdateCatalogRecord(kind, record.id, record.version, input, key)
        : await d.CreateCatalogRecord(kind, input, key),
      'record',
    );
  return request(`/api/v1/${kind}${record ? `/${encodeURIComponent(record.id)}` : ''}`, {
    method: record ? 'PUT' : 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-AgentLab-Request': 'catalog',
      'Idempotency-Key': key,
      ...(record ? { 'If-Match': `"${record.version}"` } : {}),
    },
    body: JSON.stringify(input),
  });
}
export async function deleteRecord(record: CatalogRecord, key: string): Promise<void> {
  const d = window.go?.app?.Desktop;
  if (d) {
    const response = await d.DeleteCatalogRecord(record.kind, record.id, record.version, key);
    if (response.error)
      throw new APIError(response.error.message, response.error.code, response.error.fields);
    return;
  }
  return request(`/api/v1/${record.kind}/${encodeURIComponent(record.id)}`, {
    method: 'DELETE',
    headers: {
      'Content-Type': 'application/json',
      'X-AgentLab-Request': 'catalog',
      'Idempotency-Key': key,
      'If-Match': `"${record.version}"`,
    },
  });
}
export async function allRecords(kind: CatalogKind): Promise<CatalogRecord[]> {
  const first = await listRecords(kind, { page: 1, pageSize: 100 });
  const items = [...first.items];
  for (let page = 2; items.length < first.total; page++) {
    const next = await listRecords(kind, { page, pageSize: 100 });
    if (!next.items.length) break;
    items.push(...next.items);
  }
  return items;
}

export async function listRuntimeRecords(
  kind: RuntimeRecordKind,
  query: RuntimeQuery,
): Promise<RuntimeRecordPage> {
  const d = window.go?.app?.Desktop;
  if (d) {
    const value = await d.ListRuntimeRecords(kind, query);
    if (value.error) throw new APIError(value.error.message, value.error.code);
    return value.page!;
  }
  const params = new URLSearchParams();
  Object.entries(query).forEach(([key, value]) => {
    if (value !== undefined && value !== '') params.set(key, String(value));
  });
  return request(`/api/v1/${kind}?${params}`);
}
export async function getRuntimeRecord(
  kind: RuntimeRecordKind,
  id: string,
): Promise<RuntimeRecord> {
  const d = window.go?.app?.Desktop;
  if (d) {
    const value = await d.GetRuntimeRecord(kind, id);
    if (value.error) throw new APIError(value.error.message, value.error.code);
    return value.record!;
  }
  return request(`/api/v1/${kind}/${encodeURIComponent(id)}`);
}

function authNative<T>(value: NativeAuthResponse, key: 'state' | 'user' | 'page'): T {
  if (value.error) throw new APIError(value.error.message, value.error.code, value.error.fields);
  return value[key] as T;
}
async function authWrite<T>(path: string, body: unknown, method = 'POST'): Promise<T> {
  return request(path, {
    method,
    headers: { 'Content-Type': 'application/json', 'X-AgentLab-Request': 'auth' },
    body: JSON.stringify(body),
  });
}
export async function getAuthState(): Promise<AuthState> {
  const d = window.go?.app?.Desktop;
  return d ? authNative(await d.GetAuthState(), 'state') : request('/api/v1/auth/me');
}
export async function login(username: string, password: string): Promise<AuthState> {
  const d = window.go?.app?.Desktop;
  return d
    ? authNative(await d.Login(username, password), 'state')
    : authWrite('/api/v1/auth/login', { username, password });
}
export async function register(input: RegisterInput): Promise<AuthState> {
  const d = window.go?.app?.Desktop;
  return d
    ? authNative(await d.Register(input), 'state')
    : authWrite('/api/v1/auth/register', input);
}
export async function logout(): Promise<void> {
  const d = window.go?.app?.Desktop;
  if (d) {
    authNative(await d.Logout(), 'state');
    return;
  }
  return authWrite('/api/v1/auth/logout', {});
}
export async function changePassword(currentPassword: string, password: string): Promise<void> {
  const d = window.go?.app?.Desktop;
  if (d) {
    const response = await d.ChangePassword(currentPassword, password);
    if (response.error) throw new APIError(response.error.message, response.error.code);
    return;
  }
  return authWrite('/api/v1/auth/password', { currentPassword, password });
}
export async function listUsers(q = '', page = 1): Promise<UserPage> {
  const d = window.go?.app?.Desktop;
  return d
    ? authNative(await d.ListUsers(q, page), 'page')
    : request(`/api/v1/users?${new URLSearchParams({ q, page: String(page) })}`);
}
export async function createUser(input: CreateUserInput): Promise<AuthUser> {
  const d = window.go?.app?.Desktop;
  return d ? authNative(await d.CreateUser(input), 'user') : authWrite('/api/v1/users', input);
}
export async function updateUser(id: string, input: UpdateUserInput): Promise<AuthUser> {
  const d = window.go?.app?.Desktop;
  return d
    ? authNative(await d.UpdateUser(id, input), 'user')
    : authWrite(`/api/v1/users/${encodeURIComponent(id)}`, input, 'PUT');
}
export async function resetUserPassword(id: string, password: string): Promise<void> {
  const d = window.go?.app?.Desktop;
  if (d) {
    const value = await d.ResetUserPassword(id, password);
    if (value.error) throw new APIError(value.error.message, value.error.code);
    return;
  }
  return authWrite(`/api/v1/users/${encodeURIComponent(id)}/password`, { password });
}
