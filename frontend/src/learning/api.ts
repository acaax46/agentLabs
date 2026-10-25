import { APIError } from '../api/client';
import type { components } from '../api/generated';
export type Release = components['schemas']['LearningRelease'];
export type Workspace = components['schemas']['LearningWorkspace'];
export type Job = components['schemas']['LearningJob'];
export type File = components['schemas']['LearningFile'];
export type Budget = components['schemas']['LearningAccount'];
export type Price = components['schemas']['LearningPrice'];
export type Ledger = components['schemas']['LearningLedger'];
export type Event = components['schemas']['LearningEvent'];
export type Capabilities = components['schemas']['LearningCapabilities'];
export async function learning<T>(
  action: string,
  id = '',
  payload?: unknown,
  key = '',
): Promise<T> {
  const native = window.go?.app?.Desktop;
  if (native) {
    const r = await native.Learning({ action, id, key, payload: payload || {} });
    if (r.error) throw new APIError(r.error.message, r.error.code);
    return r.data as T;
  }
  const routes: Record<string, [string, string]> = {
    capabilities: ['GET', 'capabilities'],
    releases: ['GET', `releases${id ? `?assignmentId=${encodeURIComponent(id)}` : ''}`],
    release: ['GET', `releases/${id}`],
    publish: ['POST', `assignments/${id}/publish`],
    revoke: ['POST', `releases/${id}/revoke`],
    open: ['POST', `releases/${id}/workspace`],
    workspaces: ['GET', 'workspaces'],
    workspace: ['GET', `workspaces/${id}`],
    save: ['PUT', `workspaces/${id}`],
    reset: ['POST', `workspaces/${id}/reset`],
    jobs: ['GET', `workspaces/${id}/jobs`],
    enqueue: ['POST', `workspaces/${id}/jobs`],
    events: ['GET', `workspaces/${id}/events`],
    job: ['GET', `jobs/${id}`],
    cancel: ['POST', `jobs/${id}/cancel`],
    budgets: ['GET', 'budgets'],
    budget: ['POST', 'budgets'],
    prices: ['GET', 'prices'],
    price: ['POST', 'prices'],
    ledger: ['GET', 'ledger'],
    bundles: ['GET', `problems/${id}/judge-bundles`],
    bundle: ['POST', 'judge-bundles'],
    rejudge: ['POST', `jobs/${id}/rejudge`],
  };
  const route = routes[action];
  if (!route) throw new Error('未知代码空间操作');
  let response: Response;
  try {
    response = await fetch(`/api/v1/learning/${route[1]}`, {
      method: route[0],
      credentials: 'same-origin',
      signal: AbortSignal.timeout(15000),
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-AgentLab-Request': 'learning',
        ...(key ? { 'Idempotency-Key': key } : {}),
      },
      ...(route[0] !== 'GET' ? { body: JSON.stringify(payload || {}) } : {}),
    });
  } catch {
    throw new APIError('暂时无法连接代码空间服务。保存结果未确认时，请先核对再重试。');
  }
  if (!response.ok) {
    const r = await response.json().catch(() => ({}));
    throw new APIError(r.message || '代码空间操作未完成。', r.code);
  }
  return response.json() as Promise<T>;
}
export const jobLabels: Record<string, string> = {
  queued: '排队中',
  running: '运行中',
  succeeded: '已完成',
  failed: '失败',
  unknown: '结果未知，待核对',
  cancelled: '已停止',
};
