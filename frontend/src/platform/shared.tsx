import { useQuery } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { APIError } from '../api/client';
import { get } from './api';
import type { Run, Page } from './api';
import { formatDuration, timeAgo, triggerLabels } from './model';
import { EmptyState, ErrorState, runBadge, billingBadge } from './ui';
import { usePlatform } from './state';
import { Money } from './controls';
import type { RunColumn } from './preferences';

export function useList<T>(
  path: string,
  extra: Record<string, string | undefined> = {},
  enabled = true,
) {
  const { projectId } = usePlatform();
  return useQuery({
    queryKey: ['platform', path, projectId, extra],
    queryFn: async ({ signal }) =>
      (await get<Page<T>>(path, { projectId, ...extra, limit: 100 }, signal)).items,
    enabled: !!projectId && enabled,
    retry: false,
  });
}
export function usePage<T>(
  path: string,
  extra: Record<string, string | undefined> = {},
  enabled = true,
  refresh = false,
) {
  const { projectId } = usePlatform();
  const [params, setParams] = useSearchParams();
  const cursor = params.get('cursor') ?? '';
  const history = params.getAll('previous');
  const query = useQuery({
    queryKey: ['platform', path, projectId, extra, cursor],
    queryFn: ({ signal }) => get<Page<T>>(path, { projectId, ...extra, cursor, limit: 25 }, signal),
    enabled: (!!projectId || path === '/projects') && enabled,
    retry: false,
    refetchInterval: refresh ? 5000 : false,
  });
  return {
    ...query,
    page: history.length + 1,
    next: () => {
      if (!query.data?.nextCursor) return;
      const next = new URLSearchParams(params);
      next.append('previous', cursor);
      next.set('cursor', query.data.nextCursor);
      setParams(next);
    },
    previous: () => {
      const next = new URLSearchParams(params);
      const cursors = [...history];
      const last = cursors.pop();
      next.delete('previous');
      cursors.forEach((item) => next.append('previous', item));
      if (last) next.set('cursor', last);
      else next.delete('cursor');
      setParams(next);
    },
  };
}
export function QueryState({
  pending,
  error,
  retry,
  children,
}: {
  pending: boolean;
  error: Error | null;
  retry: () => unknown;
  children: ReactNode;
}) {
  if (pending)
    return (
      <div className="wb-loading-block" role="status">
        <span className="wb-spinner" />
        正在加载…
      </div>
    );
  if (error)
    return (
      <ErrorState
        title={
          error instanceof APIError && ['NOT_FOUND', 'FORBIDDEN'].includes(error.code)
            ? '未找到或无权访问'
            : '无法读取平台数据'
        }
        description={
          <>
            {error.message}
            {error instanceof APIError && error.requestId && (
              <span className="wb-request-id">请求 ID：{error.requestId}</span>
            )}
          </>
        }
        action={
          <button className="wb-button wb-button--secondary" onClick={() => retry()}>
            重新加载
          </button>
        }
      />
    );
  return children;
}
export function MutationError({ error }: { error: Error | null }) {
  return error ? (
    <div className="wb-form-error" role="alert">
      {error.message}
      {error instanceof APIError && error.requestId && (
        <p className="wb-mono">请求 ID：{error.requestId}</p>
      )}
      {error instanceof APIError &&
        Object.entries(error.fields).map(([field, message]) => <p key={field}>{message}</p>)}
    </div>
  ) : null;
}
export function RequireProject({ children }: { children: ReactNode }) {
  const platform = usePlatform();
  return (
    <QueryState pending={platform.pending} error={platform.error} retry={platform.refresh}>
      {platform.project ? (
        children
      ) : (
        <EmptyState
          icon="projects"
          title="请先创建项目"
          description="项目绑定预算、连接、运行和产物。"
          action={
            <Link className="wb-button wb-button--primary" to="/projects?new=1">
              创建项目
            </Link>
          }
        />
      )}
    </QueryState>
  );
}
export function RunTable({
  runs,
  columns = ['agent', 'source', 'duration', 'cost', 'created'],
}: {
  runs: Run[];
  columns?: RunColumn[];
}) {
  const show = (key: RunColumn) => columns.includes(key);
  if (!runs.length)
    return (
      <EmptyState
        icon="runs"
        title="暂无运行"
        description="选择已发布 Agent，在 Playground 提交任务。"
      />
    );
  return (
    <div className="wb-table-wrap">
      <table className="wb-table">
        <thead>
          <tr>
            <th>Run / 输入</th>
            {show('agent') && <th>Agent / 版本</th>}
            <th>状态</th>
            {show('source') && <th>来源</th>}
            {show('duration') && <th>耗时</th>}
            {show('cost') && <th>费用</th>}
            {show('created') && <th>创建时间</th>}
          </tr>
        </thead>
        <tbody>
          {runs.map((run) => (
            <tr key={run.id}>
              <td>
                <span className="wb-cell-stack">
                  <Link className="wb-table-link wb-mono" to={`/runs/${run.id}`}>
                    {run.id}
                  </Link>
                  <small>{run.inputSummary}</small>
                </span>
              </td>
              {show('agent') && (
                <td>
                  <span className="wb-cell-stack">
                    <strong>{run.agent.name}</strong>
                    <small>{run.agent.version}</small>
                  </span>
                </td>
              )}
              <td>
                {runBadge(run.status)}
                {run.blockedReason && <small>{run.blockedReason}</small>}
              </td>
              {show('source') && (
                <td>
                  {run.trigger === 'api'
                    ? `API · ${run.actor?.name || '密钥名称未知'}`
                    : triggerLabels[run.trigger]}
                </td>
              )}
              {show('duration') && <td>{formatDuration(run.durationMs)}</td>}
              {show('cost') && (
                <td>
                  <span className="wb-cell-stack">
                    <span className="wb-mono">
                      <Money value={run.costMicros} currency={run.currency} />
                    </span>
                    {billingBadge(run.billingStatus)}
                  </span>
                </td>
              )}
              {show('created') && <td>{timeAgo(run.createdAt)}</td>}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
