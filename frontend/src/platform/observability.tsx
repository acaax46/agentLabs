import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { get } from './api';
import type { EventPage, Ledger, Page, Run, ToolCall, UsageSummary } from './api';
import { formatDateTime, formatDuration, isTerminal, ledgerTypeLabels } from './model';
import { Money } from './controls';
import { QueryState } from './shared';
import { SectionHeader, ToolCallBadge, billingBadge } from './ui';
import { stripTerminalControls } from './logs';
import { saveText } from './files';

export function EventTimeline({
  id,
  active,
  resource = 'runs',
}: {
  id: string;
  active: boolean;
  resource?: 'runs' | 'tool-calls';
}) {
  const cache = useQueryClient();
  const [follow, setFollow] = useState(true);
  const [search, setSearch] = useState('');
  const [level, setLevel] = useState('');
  const [wrap, setWrap] = useState(true);
  const queryKey = ['platform', resource === 'runs' ? 'events' : 'tool-events', id];
  const box = useRef<HTMLDivElement>(null);
  const query = useQuery({
    queryKey,
    queryFn: async ({ signal }) => {
      const previous = cache.getQueryData<EventPage>(queryKey);
      const items = [...(previous?.items ?? [])];
      let cursor = previous?.cursor ?? 0;
      let page: EventPage;
      do {
        page = await get<EventPage>(
          `/${resource}/${encodeURIComponent(id)}/events`,
          { after: cursor, limit: 200 },
          signal,
        );
        const known = new Set(items.map((event) => event.id));
        items.push(...page.items.filter((event) => !known.has(event.id)));
        if (page.cursor <= cursor) break;
        cursor = page.cursor;
      } while (page.items.length === 200);
      return { ...page, items, cursor };
    },
    retry: false,
    refetchInterval: active ? 2000 : false,
  });
  const count = query.data?.items.length;
  const filtered =
    query.data?.items.filter(
      (event) =>
        (!level || event.level === level) &&
        (!search ||
          stripTerminalControls(`${event.message} ${event.source} ${event.type}`)
            .toLowerCase()
            .includes(search.toLowerCase())),
    ) ?? [];
  useEffect(() => {
    if (follow && box.current) box.current.scrollTop = box.current.scrollHeight;
  }, [count, follow]);
  return (
    <section className="wb-panel">
      <SectionHeader title="事件与日志" description="使用服务端游标恢复，已收到的事件不会重复。" />
      <div className="wb-toolbar">
        <label className="wb-search">
          <input
            aria-label="搜索日志"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="搜索已加载日志"
          />
        </label>
        <label className="wb-select">
          <span>日志级别</span>
          <select value={level} onChange={(event) => setLevel(event.target.value)}>
            <option value="">全部</option>
            {['info', 'warning', 'error', 'success'].map((value) => (
              <option key={value}>{value}</option>
            ))}
          </select>
        </label>
        <label className="wb-check">
          <input
            type="checkbox"
            checked={wrap}
            onChange={(event) => setWrap(event.target.checked)}
          />
          日志换行
        </label>
        <button
          className="wb-button wb-button--secondary"
          disabled={!filtered.length}
          onClick={() =>
            saveText(
              `${id}-logs.txt`,
              filtered
                .map(
                  (event) =>
                    `${event.at} [${event.level}] ${event.source} ${stripTerminalControls(event.message)}`,
                )
                .join('\n'),
            )
          }
        >
          下载筛选日志
        </button>
        <label className="wb-check">
          <input
            type="checkbox"
            checked={follow}
            onChange={(event) => setFollow(event.target.checked)}
          />
          自动跟随最新
        </label>
        {!follow && (
          <button className="wb-button wb-button--secondary" onClick={() => setFollow(true)}>
            跳到最新
          </button>
        )}
        <span className="wb-muted">
          最后更新：
          {query.dataUpdatedAt
            ? formatDateTime(new Date(query.dataUpdatedAt).toISOString())
            : '尚未获取'}
        </span>
      </div>
      {query.error && (
        <div className="wb-callout wb-callout--warning" role="status">
          <span>{active ? '重新连接中，恢复后将从最后游标补齐。' : '事件读取中断，请重试。'}</span>
          <button className="wb-button wb-button--secondary" onClick={() => void query.refetch()}>
            重新连接
          </button>
        </div>
      )}
      <QueryState
        pending={query.isPending}
        error={query.data ? null : query.error}
        retry={query.refetch}
      >
        <div
          className="wb-event-log wb-code"
          data-wrap={wrap}
          ref={box}
          onScroll={() => {
            const element = box.current;
            if (
              element &&
              element.scrollHeight - element.scrollTop - element.clientHeight > 32 &&
              follow
            )
              setFollow(false);
          }}
        >
          <ol>
            {filtered.map((event) => (
              <li key={event.id}>
                <time>{formatDateTime(event.at)}</time>
                <span className="wb-chip">
                  {event.level} · {event.source}
                </span>
                <pre>{stripTerminalControls(event.message)}</pre>
              </li>
            ))}
          </ol>
          {!count && <p className="wb-muted">暂无事件。</p>}
          {!!count && !filtered.length && <p className="wb-muted">已加载日志中没有匹配项。</p>}
        </div>
      </QueryState>
    </section>
  );
}
export function ToolTree({ run, calls }: { run: Run; calls: ToolCall[] }) {
  return (
    <ul className="wb-tool-tree">
      <li>
        <strong>
          {run.agent.name} · {run.agent.version}
        </strong>
        <ul>
          {calls.map((call) => (
            <li key={call.id}>
              <details>
                <summary>
                  <span className="wb-mono">
                    {call.name}@{call.version}
                  </span>{' '}
                  <ToolCallBadge status={call.status} />
                </summary>
                <dl className="wb-fact-list">
                  <div>
                    <dt>开始时间</dt>
                    <dd>{formatDateTime(call.startedAt)}</dd>
                  </div>
                  <div>
                    <dt>耗时</dt>
                    <dd>{formatDuration(call.durationMs)}</dd>
                  </div>
                  <div>
                    <dt>费用</dt>
                    <dd>
                      <Money value={call.costMicros} currency={call.currency ?? run.currency} />
                      {billingBadge(call.billingStatus)}
                    </dd>
                  </div>
                  <div>
                    <dt>隔离边界</dt>
                    <dd>{call.sandbox || '未使用代码沙箱'}</dd>
                  </div>
                </dl>
                <p>输入摘要：{call.inputSummary}</p>
                <p>输出摘要：{call.outputSummary}</p>
                {call.status === 'unknown' && (
                  <p className="wb-field-error">结果待核对，不自动重试或退款。</p>
                )}
              </details>
            </li>
          ))}
        </ul>
        {!calls.length && <p className="wb-muted">尚无工具调用。</p>}
      </li>
    </ul>
  );
}
export function RunCharges({ run }: { run: Run }) {
  const active = !isTerminal(run.status);
  const summary = useQuery({
    queryKey: ['platform', 'run-charges', run.id, run.status],
    queryFn: ({ signal }) =>
      get<UsageSummary>('/usage/summary', { projectId: run.projectId, runId: run.id }, signal),
    retry: false,
    refetchInterval: active ? 5000 : false,
  });
  const ledger = useQuery({
    queryKey: ['platform', 'run-ledger', run.id, run.status],
    queryFn: ({ signal }) =>
      get<Page<Ledger>>(
        '/billing/ledger',
        { projectId: run.projectId, runId: run.id, limit: 100 },
        signal,
      ),
    retry: false,
    refetchInterval: active ? 5000 : false,
  });
  return (
    <section className="wb-panel">
      <SectionHeader title="费用拆项与预算变动" />
      <QueryState pending={summary.isPending} error={summary.error} retry={summary.refetch}>
        <ul className="wb-compact-list">
          {summary.data?.groups.map((group, index) => (
            <li key={index}>
              <span>
                {group.category === 'model'
                  ? '模型'
                  : group.category === 'execution'
                    ? '托管执行'
                    : group.category === 'tool'
                      ? '工具'
                      : group.category}
              </span>
              <span>
                {group.status === 'not_applicable' ? (
                  '由客户承担'
                ) : (
                  <Money
                    value={group.amountKnown === false ? null : group.amountMicros}
                    currency={run.currency}
                  />
                )}
                {billingBadge(group.status)}
              </span>
            </li>
          ))}
        </ul>
        {!summary.data?.groups.length && (
          <p className="wb-muted">尚无已记录用量；当前预占不能视为已结算费用。</p>
        )}
      </QueryState>
      <QueryState pending={ledger.isPending} error={ledger.error} retry={ledger.refetch}>
        <ul className="wb-compact-list">
          {ledger.data?.items
            .filter((entry) => entry.scope === 'project')
            .map((entry) => (
              <li key={entry.id}>
                <span>{ledgerTypeLabels[entry.type]}</span>
                <span>
                  <Money value={entry.amountMicros} currency={entry.currency} />
                  {entry.note}
                </span>
              </li>
            ))}
        </ul>
      </QueryState>
    </section>
  );
}
