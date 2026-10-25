import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { get } from '../api';
import type { Overview } from '../api';
import { usePlatform } from '../state';
import { formatMoney, formatDateTime } from '../model';
import { PageHeader, SectionHeader, StatusBadge, KeyValue } from '../ui';
import { QueryState, RequireProject, RunTable } from '../shared';
import { BudgetBar } from '../controls';
import { OverviewTrends } from '../Trends';

export function OverviewPage() {
  const { projectId, readiness } = usePlatform();
  const query = useQuery({
    queryKey: ['platform', 'overview', projectId],
    queryFn: ({ signal }) => get<Overview>('/overview', { projectId }, signal),
    enabled: !!projectId,
    retry: false,
    refetchInterval: 10_000,
  });
  const view = query.data;
  return (
    <>
      <PageHeader
        eyebrow="AGENTLAB / OVERVIEW"
        title="工作台概览"
        description="查看当前项目的运行、预算和需要核对的事项。"
        actions={
          <Link className="wb-button wb-button--primary" to="/chat">
            开始对话
          </Link>
        }
      />
      <RequireProject>
        <OverviewTrends />
        <QueryState pending={query.isPending} error={query.error} retry={query.refetch}>
          {view && (
            <>
              <section className="wb-stat-grid">
                {(
                  [
                    ['进行中的 Run', view.active.length],
                    ['待处理事项', view.attention.length],
                    [
                      '近七天运行',
                      Object.values(view.counts).reduce((total, count) => total + count, 0),
                    ],
                    [
                      '已结算',
                      `${formatMoney(view.project.budget.configured ? view.project.budget.spentMicros : null, view.project.currency)} ${view.project.currency}`,
                    ],
                  ] as const
                ).map(([label, value]) => (
                  <article className="wb-stat" key={label}>
                    <div className="wb-stat-head">{label}</div>
                    <strong>{value}</strong>
                  </article>
                ))}
              </section>
              <div className="wb-detail-grid">
                <div className="wb-detail-main">
                  <section className="wb-panel">
                    <SectionHeader title="进行中" />
                    <RunTable runs={view.active} />
                  </section>
                  <section className="wb-panel">
                    <SectionHeader
                      title="需要处理"
                      description="未知结果保留预占，需要核对实际副作用与费用。"
                    />
                    <RunTable runs={view.attention} />
                  </section>
                  <section className="wb-panel">
                    <SectionHeader
                      title="最近运行"
                      description={`统计窗口开始于 ${formatDateTime(view.since)}`}
                    />
                    <RunTable runs={view.recent} />
                  </section>
                </div>
                <aside className="wb-detail-side">
                  <section className="wb-panel">
                    <SectionHeader title="项目预算" />
                    <BudgetBar budget={view.project.budget} />
                    <KeyValue
                      items={[
                        { label: '当前项目', value: view.project.name },
                        {
                          label: '预算上限',
                          value: `${formatMoney(view.project.budget.configured ? view.project.budget.limitMicros : null, view.project.currency)} ${view.project.currency}`,
                        },
                        {
                          label: '预占',
                          value: `${formatMoney(view.project.budget.configured ? view.project.budget.heldMicros : null, view.project.currency)} ${view.project.currency}`,
                        },
                        {
                          label: '可用预算',
                          value: `${formatMoney(view.project.budget.configured ? view.project.budget.availableMicros : null, view.project.currency)} ${view.project.currency}`,
                        },
                      ]}
                    />
                    <Link className="wb-text-link" to={`/projects/${projectId}`}>
                      管理预算与连接
                    </Link>
                  </section>
                  <section className="wb-panel">
                    <SectionHeader title="执行能力" />
                    <StatusBadge tone={readiness?.ready ? 'success' : 'warning'}>
                      {readiness ? (readiness.ready ? '平台服务就绪' : '服务未就绪') : '能力未知'}
                    </StatusBadge>
                    <p className="wb-muted">
                      {readiness?.capabilities.codeExecution
                        ? readiness.capabilities.isolation
                        : (readiness?.capabilities.codeExecutionReason ?? '正在读取后端能力。')}
                    </p>
                  </section>
                </aside>
              </div>
            </>
          )}
        </QueryState>
      </RequireProject>
    </>
  );
}
