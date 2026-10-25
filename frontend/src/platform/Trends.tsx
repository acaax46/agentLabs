import { useQueries, useQuery } from '@tanstack/react-query';
import { get } from './api';
import type { Overview, UsageSummary } from './api';
import { usePlatform } from './state';
import { formatMoney, runStatusLabels } from './model';
import type { RunStatus } from './model';
import { QueryState } from './shared';
import { SectionHeader } from './ui';

export function OverviewTrends() {
  const { projectId, project } = usePlatform();
  const end = new Date();
  end.setUTCHours(0, 0, 0, 0);
  end.setUTCDate(end.getUTCDate() + 1);
  const windows = Array.from({ length: 7 }, (_, index) => {
    const to = new Date(end.getTime() - (6 - index) * 86400000);
    const from = new Date(to.getTime() - 86400000);
    return { from: from.toISOString(), to: to.toISOString(), day: from.toISOString().slice(0, 10) };
  });
  const queries = useQueries({
    queries: windows.map((window) => ({
      queryKey: ['platform', 'trend', projectId, window.day],
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        get<UsageSummary>(
          '/usage/summary',
          { projectId, from: window.from, to: window.to },
          signal,
        ),
      enabled: !!projectId,
      retry: false,
      staleTime: 60000,
    })),
  });
  const overview = useQuery({
    queryKey: ['platform', 'overview', projectId],
    queryFn: ({ signal }) => get<Overview>('/overview', { projectId }, signal),
    enabled: !!projectId,
    retry: false,
  });
  const error = queries.find((query) => query.error)?.error ?? null;
  const values = queries.map((query) =>
    query.data &&
    !query.data.groups.some((group) => group.status === 'settled' && group.amountKnown === false)
      ? BigInt(query.data.settledMicros)
      : null,
  );
  const max = values.reduce<bigint>(
    (value, next) => (next !== null && next > value ? next : value),
    0n,
  );
  const counts = Object.entries(overview.data?.counts ?? {});
  const total = counts.reduce((sum, [, count]) => sum + count, 0);
  return (
    <section className="wb-panel">
      <SectionHeader
        title="近七日用量趋势与运行分布"
        description={`UTC 自然日 · ${project?.currency ?? '币种未知'} · 图形只表示平台已结算费用；预占和 BYOK 直付不计入。`}
      />
      <QueryState
        pending={queries.some((query) => query.isPending)}
        error={error}
        retry={() => queries.forEach((query) => void query.refetch())}
      >
        <div className="wb-trend-chart" role="img" aria-label="近七日已结算费用，精确数值见下表">
          {windows.map((window, index) => (
            <div key={window.day}>
              <span
                className="wb-trend-bar"
                style={{
                  height:
                    values[index] !== null && max > 0n
                      ? `${Math.max(1, Number((values[index]! * 100n) / max))}%`
                      : '0%',
                }}
              />
              <small>{window.day.slice(5)}</small>
            </div>
          ))}
        </div>
        <div className="wb-table-wrap">
          <table className="wb-table">
            <thead>
              <tr>
                <th>日期（UTC）</th>
                <th>已结算</th>
                <th>待核对 / 未结算</th>
              </tr>
            </thead>
            <tbody>
              {windows.map((window, index) => (
                <tr key={window.day}>
                  <td>{window.day}</td>
                  <td>
                    {formatMoney(values[index]?.toString() ?? null, project?.currency)}{' '}
                    {project?.currency}
                  </td>
                  <td>
                    {queries[index]?.data
                      ? `${formatMoney(queries[index]!.data!.groups.some((group) => group.status === 'pending' && group.amountKnown === false) ? null : queries[index]!.data!.pendingMicros, project?.currency)} ${project?.currency}`
                      : '未知'}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </QueryState>
      <QueryState pending={overview.isPending} error={overview.error} retry={overview.refetch}>
        <p className="wb-muted">
          运行结果分布统计起点：{overview.data?.since ?? '未知'}；分母为此窗口全部状态的运行。
        </p>
        {total ? (
          <div className="wb-inline-stats">
            {counts.map(([status, count]) => (
              <span key={status}>
                {runStatusLabels[status as RunStatus] ?? status}：{count}（
                {((count / total) * 100).toFixed(1)}%）
              </span>
            ))}
          </div>
        ) : (
          <p className="wb-muted">此统计窗口没有运行，不绘制虚构的结果分布。</p>
        )}
      </QueryState>
    </section>
  );
}
