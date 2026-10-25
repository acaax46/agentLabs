import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useParams, useSearchParams } from 'react-router-dom';
import { get, write } from '../api';
import type { Run, ToolCall } from '../api';
import { usePlatform } from '../state';
import {
  formatDateTime,
  formatDuration,
  formatMoney,
  isTerminal,
  modelSourceLabels,
  runStatusLabels,
} from '../model';
import { QueryState, RequireProject, MutationError, RunTable, usePage } from '../shared';
import { Pagination, Money } from '../controls';
import { runColumnOptions, useRunPreferences } from '../preferences';
import { decimalToMicros } from '../model';
import { EventTimeline, RunCharges, ToolTree } from '../observability';
import { RunEvidence } from '../ArtifactEnhancements';
import { ArtifactList } from './ArtifactsPage';
import {
  CodeBlock,
  CopyButton,
  KeyValue,
  Modal,
  PageHeader,
  SectionHeader,
  Tabs,
  billingBadge,
  runBadge,
} from '../ui';

export function RunsPage() {
  const { project } = usePlatform();
  const [params, setParams] = useSearchParams();
  const status = params.get('status') ?? '';
  const trigger = params.get('trigger') ?? '';
  const actorId = params.get('actorId') ?? '';
  const query = usePage<Run>('/runs', { status, trigger, actorId }, true, true);
  const preferences = useRunPreferences();
  const [viewName, setViewName] = useState('');
  const [filterError, setFilterError] = useState('');
  const minimum = params.get('minCost') ?? '';
  const maximum = params.get('maxCost') ?? '';
  const from = params.get('fromRun') ?? '';
  const to = params.get('toRun') ?? '';
  const visibleRuns = (query.data?.items ?? []).filter((run) => {
    if (
      params.get('agent') &&
      !`${run.agent.name} ${run.agent.id}`
        .toLowerCase()
        .includes(params.get('agent')!.toLowerCase())
    )
      return false;
    if (
      params.get('version') &&
      !`${run.agent.version} ${run.agent.versionId}`.includes(params.get('version')!)
    )
      return false;
    if (params.get('source') && run.model.source !== params.get('source')) return false;
    if (from && new Date(run.createdAt) < new Date(from)) return false;
    if (to && new Date(run.createdAt) >= new Date(to)) return false;
    if (minimum || maximum) {
      if (run.costMicros === null) return false;
      try {
        if (minimum && BigInt(run.costMicros) < BigInt(decimalToMicros(minimum))) return false;
        if (maximum && BigInt(run.costMicros) > BigInt(decimalToMicros(maximum))) return false;
      } catch {
        return false;
      }
    }
    return true;
  });
  const filter = (name: string, value: string) => {
    const next = new URLSearchParams(params);
    next.delete('cursor');
    next.delete('previous');
    if (value) next.set(name, value);
    else next.delete(name);
    setParams(next);
  };
  return (
    <>
      <PageHeader
        title="运行"
        description="按当前项目和服务端游标分页查看运行。每 5 秒更新状态。"
        actions={
          <Link className="wb-button wb-button--primary" to="/agents">
            新建运行
          </Link>
        }
      />
      <RequireProject>
        <section className="wb-panel">
          <div className="wb-toolbar">
            <label className="wb-select">
              <span>运行状态</span>
              <select value={status} onChange={(event) => filter('status', event.target.value)}>
                <option value="">全部状态</option>
                <option value="queued,preparing,running,cancelling">进行中</option>
                <option value="succeeded">已完成</option>
                <option value="failed">失败</option>
                <option value="unknown">待核对</option>
                <option value="cancelled">已停止</option>
              </select>
            </label>
            <label className="wb-select">
              <span>调用来源</span>
              <select value={trigger} onChange={(event) => filter('trigger', event.target.value)}>
                <option value="">全部来源</option>
                <option value="workbench">工作台</option>
                <option value="api">API</option>
              </select>
            </label>
            <button className="wb-button wb-button--secondary" onClick={() => void query.refetch()}>
              刷新
            </button>
          </div>
          <details className="wb-p1-filters">
            <summary>高级筛选、保存视图与列设置</summary>
            <p className="wb-muted">
              状态、来源与调用身份由服务端筛选。其余条件仅筛选当前加载页，翻页可继续查看；不将本页结果当作全量检索。
            </p>
            <form
              className="wb-form"
              onSubmit={(event) => {
                event.preventDefault();
                const data = new FormData(event.currentTarget);
                try {
                  const min = String(data.get('minCost') ?? '');
                  const max = String(data.get('maxCost') ?? '');
                  if (min) decimalToMicros(min);
                  if (max) decimalToMicros(max);
                  if (min && max && BigInt(decimalToMicros(min)) > BigInt(decimalToMicros(max)))
                    throw new Error('最低金额不能高于最高金额。');
                  const start = String(data.get('fromRun') ?? '');
                  const end = String(data.get('toRun') ?? '');
                  if (start && end && new Date(start) >= new Date(end))
                    throw new Error('开始时间必须早于结束时间。');
                  const next = new URLSearchParams(params);
                  next.delete('cursor');
                  next.delete('previous');
                  for (const key of [
                    'actorId',
                    'agent',
                    'version',
                    'source',
                    'minCost',
                    'maxCost',
                    'fromRun',
                    'toRun',
                  ]) {
                    const value = String(data.get(key) ?? '').trim();
                    if (value) next.set(key, value);
                    else next.delete(key);
                  }
                  setParams(next);
                  setFilterError('');
                } catch (error) {
                  setFilterError(error instanceof Error ? error.message : '筛选条件无效。');
                }
              }}
            >
              <div className="wb-form-grid">
                <label className="wb-field">
                  <span>调用身份 ID（服务端）</span>
                  <input name="actorId" defaultValue={actorId} placeholder="API Key ID 或用户 ID" />
                </label>
                <label className="wb-field">
                  <span>Agent 名称或 ID（本页）</span>
                  <input name="agent" defaultValue={params.get('agent') ?? ''} />
                </label>
                <label className="wb-field">
                  <span>版本（本页）</span>
                  <input name="version" defaultValue={params.get('version') ?? ''} />
                </label>
                <label className="wb-field">
                  <span>模型来源（本页）</span>
                  <select name="source" defaultValue={params.get('source') ?? ''}>
                    <option value="">全部</option>
                    <option value="platform">平台供模</option>
                    <option value="byok">BYOK</option>
                  </select>
                </label>
                <label className="wb-field">
                  <span>创建时间从（本页）</span>
                  <input name="fromRun" type="datetime-local" defaultValue={from} />
                </label>
                <label className="wb-field">
                  <span>创建时间至，不含（本页）</span>
                  <input name="toRun" type="datetime-local" defaultValue={to} />
                </label>
                <label className="wb-field">
                  <span>最低费用（{project?.currency ?? '币种未知'}，本页）</span>
                  <input name="minCost" inputMode="decimal" defaultValue={minimum} />
                </label>
                <label className="wb-field">
                  <span>最高费用（{project?.currency ?? '币种未知'}，本页）</span>
                  <input name="maxCost" inputMode="decimal" defaultValue={maximum} />
                </label>
              </div>
              {filterError && (
                <p className="wb-form-error" role="alert">
                  {filterError}
                </p>
              )}
              <button className="wb-button wb-button--secondary">应用高级筛选</button>
            </form>
            <div className="wb-toolbar">
              <label className="wb-field">
                <span>视图名称</span>
                <input
                  value={viewName}
                  maxLength={40}
                  onChange={(event) => setViewName(event.target.value)}
                />
              </label>
              <button
                className="wb-button wb-button--secondary"
                disabled={!viewName.trim()}
                onClick={() => {
                  preferences.save(viewName, params.toString());
                  setViewName('');
                }}
              >
                保存当前筛选视图
              </button>
            </div>
            <ul className="wb-compact-list">
              {preferences.value.views.map((view) => (
                <li key={view.name}>
                  <button
                    className="wb-text-link"
                    onClick={() => setParams(new URLSearchParams(view.search))}
                  >
                    {view.name}
                  </button>
                  <button
                    className="wb-button wb-button--ghost wb-button--sm"
                    onClick={() => preferences.remove(view.name)}
                  >
                    删除视图 {view.name}
                  </button>
                </li>
              ))}
            </ul>
            <fieldset className="wb-fieldset">
              <legend>表格列（Run 与状态始终显示）</legend>
              <div className="wb-check-grid">
                {runColumnOptions.map(([key, label]) => (
                  <label className="wb-check" key={key}>
                    <input
                      type="checkbox"
                      checked={preferences.value.columns.includes(key)}
                      onChange={(event) =>
                        preferences.columns(
                          event.target.checked
                            ? [...preferences.value.columns, key]
                            : preferences.value.columns.filter((value) => value !== key),
                        )
                      }
                    />
                    {label}
                  </label>
                ))}
              </div>
            </fieldset>
            <p className="wb-field-help">
              只在当前账号浏览器保存筛选与列偏好，不保存 Run、结果或密钥。
            </p>
          </details>
          <p className="wb-muted">
            {query.isFetching
              ? '更新中…'
              : `最后更新：${query.dataUpdatedAt ? formatDateTime(new Date(query.dataUpdatedAt).toISOString()) : '尚未获取'}`}
          </p>
          <QueryState pending={query.isPending} error={query.error} retry={query.refetch}>
            <RunTable runs={visibleRuns} columns={preferences.value.columns} />
          </QueryState>
          <Pagination
            nextCursor={query.data?.nextCursor}
            page={query.page}
            pending={query.isFetching}
            onNext={query.next}
            onPrevious={query.previous}
          />
        </section>
      </RequireProject>
    </>
  );
}

export function RunDetailPage() {
  const { id = '' } = useParams();
  const platform = usePlatform();
  const cache = useQueryClient();
  const [stopping, setStopping] = useState(false);
  const [view, setView] = useState<'events' | 'tools'>('events');
  const query = useQuery({
    queryKey: ['platform', 'run', id],
    queryFn: ({ signal }) => get<Run>(`/runs/${encodeURIComponent(id)}`, {}, signal),
    retry: false,
    refetchInterval: (query) =>
      query.state.data && !isTerminal(query.state.data.status) ? 2000 : false,
  });
  const run = query.data;
  const runStatus = run?.status;
  const active = !!run && !isTerminal(run.status);
  const calls = useQuery({
    queryKey: ['platform', 'tool-calls', id],
    queryFn: ({ signal }) =>
      get<{ items: ToolCall[] }>(`/runs/${encodeURIComponent(id)}/tool-calls`, {}, signal),
    enabled: !!run,
    retry: false,
    refetchInterval: active ? 2000 : false,
  });
  useEffect(() => {
    if (runStatus && isTerminal(runStatus)) {
      void cache.invalidateQueries({ queryKey: ['platform', 'events', id] });
      void cache.invalidateQueries({ queryKey: ['platform', 'tool-calls', id] });
      void cache.invalidateQueries({ queryKey: ['platform', '/artifacts'] });
    }
  }, [runStatus, id, cache]);
  const stop = useMutation({
    mutationFn: () => write<Run>(`/runs/${encodeURIComponent(id)}/cancel`, {}),
    retry: false,
    onSuccess: (updated) => {
      cache.setQueryData(['platform', 'run', id], updated);
      platform.refresh();
      setStopping(false);
    },
  });
  return (
    <QueryState pending={query.isPending} error={query.error} retry={query.refetch}>
      {run && (
        <>
          <div className="wb-run-head">
            <PageHeader
              title={run.agent.name}
              description={run.inputSummary}
              meta={
                <>
                  {runBadge(run.status)}
                  {billingBadge(run.billingStatus)}
                  <span className="wb-mono">{run.id}</span>
                  <CopyButton value={run.id} label="复制 Run ID" />
                </>
              }
              actions={
                active && (
                  <button
                    className="wb-button wb-button--danger"
                    disabled={run.cancelRequested || stop.isPending}
                    onClick={() => setStopping(true)}
                  >
                    {run.cancelRequested ? '正在停止…' : '停止运行'}
                  </button>
                )
              }
            />
          </div>
          <p className="wb-sr-only" role="status" aria-live="polite">
            运行状态：{runStatusLabels[run.status]}
          </p>
          {['unknown', 'cancelled'].includes(run.status) && (
            <div className="wb-callout wb-callout--warning" role="status">
              {run.status === 'unknown'
                ? '结果待核对，保留预占；请核对外部调用和费用，不会自动重发或退款。'
                : '运行已停止，已发生的调用和用量仍按实际证据核对。'}{' '}
              当前费用：
              <Money value={run.costMicros} currency={run.currency} />
            </div>
          )}
          {run.error && (
            <div className="wb-callout wb-callout--danger" role="alert">
              <div>
                <strong>{run.error.code}</strong>
                <p>{run.error.message}</p>
                <p>{run.error.nextStep}</p>
              </div>
            </div>
          )}
          {run.blockedReason && (
            <div className="wb-callout wb-callout--warning">
              阻塞原因：{run.blockedReason}。当前服务未提供审批操作，请核对后端状态。
            </div>
          )}
          <RunEvidence run={run} />
          <div className="wb-detail-grid">
            <div className="wb-detail-main">
              <Tabs
                label="运行观测"
                value={view}
                onChange={setView}
                items={[
                  { value: 'events', label: '执行时间线' },
                  { value: 'tools', label: '工具调用树' },
                ]}
              />
              {view === 'events' && <EventTimeline id={id} active={active} />}
              {view === 'tools' && (
                <section className="wb-panel">
                  <SectionHeader title="工具调用" />
                  <QueryState pending={calls.isPending} error={calls.error} retry={calls.refetch}>
                    <ToolTree run={run} calls={calls.data?.items ?? []} />
                  </QueryState>
                </section>
              )}
              <section className="wb-panel">
                <SectionHeader title="运行结果" />
                <CodeBlock>{JSON.stringify(run.result, null, 2) ?? '暂无结果'}</CodeBlock>
              </section>
              <section className="wb-panel">
                <SectionHeader title="输出产物" />
                <ArtifactList projectId={run.projectId} runId={id} active={active} />
              </section>
            </div>
            <aside className="wb-detail-side">
              <RunCharges run={run} />
              <section className="wb-panel">
                <SectionHeader title="运行与费用" />
                <KeyValue
                  items={[
                    {
                      label: '项目',
                      value: <Link to={`/projects/${run.projectId}`}>{run.projectId}</Link>,
                    },
                    { label: '发布版本', value: `${run.agent.version} / ${run.agent.versionId}` },
                    { label: '模型', value: run.model.model },
                    { label: '来源', value: modelSourceLabels[run.model.source] },
                    { label: '连接', value: run.model.connectionName },
                    {
                      label: '费用',
                      value: `${formatMoney(run.costMicros, run.currency)} ${run.currency}`,
                    },
                    {
                      label: '预占',
                      value: `${formatMoney(run.reservedMicros, run.currency)} ${run.currency}`,
                    },
                    {
                      label: '费用上限',
                      value: `${formatMoney(run.limits.maxCostMicros, run.currency)} ${run.currency}`,
                    },
                    { label: '执行时限', value: `${run.limits.maxExecutionSeconds} 秒` },
                    { label: '冻结镜像', value: run.sandboxImage || '未使用或历史运行未记录' },
                    {
                      label: '沙箱资源',
                      value: run.resources
                        ? `${run.resources.cpu} vCPU / ${run.resources.memoryMB / 1024} GiB / ${run.resources.gpu ? run.resources.gpuType : '无 GPU'}`
                        : '旧版托管执行计价',
                    },
                    { label: '耗时', value: formatDuration(run.durationMs) },
                    { label: '创建', value: formatDateTime(run.createdAt) },
                    { label: '开始', value: formatDateTime(run.startedAt) },
                    { label: '结束', value: formatDateTime(run.finishedAt) },
                    { label: '输入 tokens', value: run.usage.inputTokens ?? '未知' },
                    { label: '输出 tokens', value: run.usage.outputTokens ?? '未知' },
                  ]}
                />
                {run.model.source === 'byok' && (
                  <p className="wb-muted">
                    BYOK 模型费用由客户承担；本页平台费用按工具与执行用量计算。
                  </p>
                )}
                <Link className="wb-text-link" to={`/usage?run=${id}&project=${run.projectId}`}>
                  核对本次用量与账本
                </Link>
              </section>
            </aside>
          </div>
          <Modal
            open={stopping}
            onClose={() => {
              if (!stop.isPending) setStopping(false);
            }}
            title={`停止运行 ${run.id}？`}
            description={`对象：${run.agent.name}。当前费用 ${formatMoney(run.costMicros, run.currency)} ${run.currency}，预占 ${formatMoney(run.reservedMicros, run.currency)} ${run.currency}。停止只阻止后续执行，不能撤回外部副作用。未知结果不自动退款。`}
          >
            <MutationError error={stop.error} />
            <div className="wb-form-actions">
              <button
                className="wb-button wb-button--danger"
                disabled={stop.isPending}
                onClick={() => stop.mutate()}
              >
                {stop.isPending ? '正在请求…' : '确认停止'}
              </button>
              <button
                className="wb-button wb-button--secondary"
                disabled={stop.isPending}
                onClick={() => setStopping(false)}
              >
                继续运行
              </button>
            </div>
          </Modal>
        </>
      )}
    </QueryState>
  );
}
