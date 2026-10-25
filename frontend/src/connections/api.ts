import { APIError } from '../api/client';
import type { components } from '../api/generated';
export type Connection = components['schemas']['Connection'];
export type ConnectionInput = components['schemas']['ConnectionInput'];
export type ConnectionMutation = components['schemas']['ConnectionMutation'];
export async function connections<T>(
  action: 'list' | 'create' | 'verify' | 'rotate' | 'revoke',
  id = '',
  payload: ConnectionInput | ConnectionMutation | Record<string, never> = {},
  key = '',
): Promise<T> {
  const native = window.go?.app?.Desktop;
  if (native) {
    const response = await native.Connections({ action, id, key, payload });
    if (response.error) throw new APIError(response.error.message, response.error.code);
    return response.data as T;
  }
  let response: Response;
  try {
    response = await fetch(
      `/api/v1/connections${id ? `/${encodeURIComponent(id)}/${action}` : ''}`,
      {
        method: action === 'list' ? 'GET' : 'POST',
        credentials: 'same-origin',
        signal: AbortSignal.timeout(15000),
        headers: {
          Accept: 'application/json',
          'Content-Type': 'application/json',
          'X-AgentLab-Request': 'connections',
          ...(key ? { 'Idempotency-Key': key } : {}),
        },
        ...(action !== 'list' ? { body: JSON.stringify(payload) } : {}),
      },
    );
  } catch {
    throw new APIError('连接操作结果尚未确认，请刷新列表核对后再试。');
  }
  if (!response.ok) {
    const result = await response.json().catch(() => ({}));
    throw new APIError(
      result.message || '账户连接操作未完成。',
      result.code,
      result.fields,
      result.requestId || response.headers.get('X-Request-ID') || '',
    );
  }
  return response.json() as Promise<T>;
}
export const connectionLabels: Record<Connection['status'], string> = {
  pending: '待验证',
  active: '已连接',
  error: '验证失败',
  expired: '已到期',
  revoked: '已撤销',
};
export function canUseConnection(c: Connection) {
  return c.status === 'active' && (!c.expiresAt || new Date(c.expiresAt).getTime() > Date.now());
}
