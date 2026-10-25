import { useEffect, useState } from 'react';
import type { FormEvent, ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { RejudgeAction } from '../learning/rejudge';
import { Link, useParams, useSearchParams } from 'react-router-dom';
import { allRecords, getRuntimeRecord, listRuntimeRecords } from '../api/client';
import type { RuntimeRecord, RuntimeRecordKind, RuntimeQuery } from '../api/client';
import { formatDate, moduleMap } from '../catalog/modules';
import { EmptyState, ErrorPanel, Loading, Notice, PageHeading } from '../catalog/shared';
import { known, labels, money, recordModuleMap } from './modules';
import type { Filter } from './modules';

function RecordBadge({ value }: { value: string }) {
  const tone = ['success', 'succeeded', 'completed', 'accepted', 'settled'].includes(value)
    ? 'active'
    : [
          'failure',
          'failed',
          'error',
          'system_failed',
          'denied',
          'wrong_answer',
          'compile_error',
          'runtime_error',
          'time_limit',
          'memory_limit',
          'output_limit',
        ].includes(value)
      ? 'record-error'
      : ['queued', 'running', 'pending', 'unknown', 'estimated', 'warning'].includes(value)
        ? 'draft'
        : 'archived';
  return <span className={`status-badge ${tone}`}>{labels[value] || value || '—'}</span>;
}
function FilterField({
  field,
  value,
  onChange,
}: {
  field: Filter;
  value: string;
  onChange: (value: string) => void;
}) {
  const query = useQuery({
    queryKey: ['catalog', 'options', field.source],
    queryFn: () => allRecords(field.source!),
    enabled: !!field.source,
  });
  const options = field.source
    ? (query.data || []).map((record) => [record.id, `${record.code} · ${record.name}`])
    : field.options;
  return (
    <label className="record-filter">
      <span>{field.label}</span>
      {options ? (
        <select
          aria-label={field.label}
          value={value}
          onChange={(event) => onChange(event.target.value)}
        >
          <option value="">全部</option>
          {options.map(([id, name]) => (
            <option value={id} key={id}>
              {name}
            </option>
          ))}
        </select>
      ) : (
        <input
          aria-label={field.label}
          value={value}
          maxLength={160}
          onChange={(event) => onChange(event.target.value)}
        />
      )}
      {query.isError && <span className="field-error">关联选项读取失败</span>}
    </label>
  );
}
function day(value: string, end = false) {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  if (end) date.setMilliseconds(date.getMilliseconds() - 1);
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
}
function dateBoundary(value: string, nextDay = false) {
  const date = new Date(`${value}T00:00:00`);
  if (nextDay) date.setDate(date.getDate() + 1);
  return date.toISOString();
}
function valuesFrom(params: URLSearchParams) {
  return {
    ...Object.fromEntries(params.entries()),
    fromDate: day(params.get('from') || ''),
    toDate: day(params.get('to') || '', true),
  };
}
function RowCells({ record }: { record: RuntimeRecord }) {
  if (record.evaluation) {
    const data = record.evaluation;
    return (
      <>
        <td>
          <strong className="record-title">
            {data.problemCode} {data.problemTitle || '未命名题目'}
          </strong>
          <span className="record-code">{data.submissionId}</span>
        </td>
        <td>{data.studentCode || data.studentId}</td>
        <td>
          <RecordBadge value={data.verdict || data.status} />
        </td>
        <td className="number">
          {data.score === null ? '—' : `${data.score} / ${data.maxScore ?? '—'}`}
        </td>
        <td>{known(data.durationMs, ' ms')}</td>
        <td>
          <RecordBadge value={data.authority} />
        </td>
      </>
    );
  }
  if (record.usage) {
    const data = record.usage;
    return (
      <>
        <td>
          <strong className="record-title">{data.model || data.tool || labels[data.kind]}</strong>
          <span className="record-code">
            {data.provider || '未标记供应商'} · {data.attemptId}
          </span>
        </td>
        <td>
          <RecordBadge value={data.status} />
        </td>
        <td className="number">{known(data.totalTokens)}</td>
        <td>{known(data.durationMs, ' ms')}</td>
        <td className="number record-money">
          {money(
            data.costMicros,
            data.currency,
            data.settlement === 'estimated',
            data.settlement === 'not-applicable' ? '不适用' : '待结算',
          )}
          <div className="record-code">{labels[data.settlement]}</div>
        </td>
        <td>
          <RecordBadge value={data.source} />
        </td>
      </>
    );
  }
  if (record.audit) {
    const data = record.audit;
    return (
      <>
        <td>
          <strong className="record-title">
            {data.message || labels[data.action] || data.action}
          </strong>
          <span className="record-code">{data.action}</span>
        </td>
        <td>{data.resourceCode || data.resourceType || '—'}</td>
        <td>
          <RecordBadge value={data.level} />
        </td>
        <td>
          <RecordBadge value={data.result} />
        </td>
        <td>
          <span className="record-code record-request">{data.requestId || '—'}</span>
        </td>
        <td>{data.actorId === 'local' ? '本地操作者' : data.actorId}</td>
      </>
    );
  }
  return <td colSpan={6}>记录内容不可用</td>;
}

export function RuntimeListPage({ kind }: { kind: RuntimeRecordKind }) {
  const module = recordModuleMap[kind];
  const [params, setParams] = useSearchParams();
  const [filters, setFilters] = useState<Record<string, string>>(() => valuesFrom(params));
  const [filterError, setFilterError] = useState('');
  useEffect(() => {
    setFilters(valuesFrom(params));
  }, [params]);
  const query: RuntimeQuery = {
    ...Object.fromEntries(params.entries()),
    page: Number(params.get('page') || 1),
    pageSize: 20,
  };
  const records = useQuery({
    queryKey: ['runtime-records', kind, query],
    queryFn: () => listRuntimeRecords(kind, query),
  });
  const set = (key: string, value: string) =>
    setFilters((previous) => ({ ...previous, [key]: value }));
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (filters.fromDate && filters.toDate && filters.fromDate > filters.toDate) {
      setFilterError('开始日期不能晚于结束日期。');
      return;
    }
    setFilterError('');
    const next = new URLSearchParams();
    for (const key of [
      'q',
      ...module.filters.map((field) => field.key),
      ...module.advanced.map((field) => field.key),
    ]) {
      if (filters[key]?.trim()) next.set(key, filters[key]!.trim());
    }
    if (filters.fromDate) next.set('from', dateBoundary(filters.fromDate));
    if (filters.toDate) next.set('to', dateBoundary(filters.toDate, true));
    setParams(next);
  };
  const move = (direction: number) => {
    const next = new URLSearchParams(params);
    next.set('page', String(Number(query.page) + direction));
    setParams(next);
  };
  const headers =
    kind === 'evaluations'
      ? ['题目 / 提交编号', '学员', '评测结果', '分数', '耗时', '记录来源']
      : kind === 'usage-records'
        ? ['模型 / 工具', '状态', '总 Token', '耗时', '费用', '用量来源']
        : ['事件', '操作对象', '级别', '结果', '请求编号', '操作者'];
  return (
    <>
      <PageHeading
        eyebrow="运行记录 / 查询"
        title={module.title}
        description={module.description}
        action={
          <button
            className="button secondary"
            disabled={records.isFetching}
            onClick={() => void records.refetch()}
          >
            {records.isFetching ? '正在读取…' : '刷新记录'}
          </button>
        }
      />
      <section className="list-panel">
        <form className="record-filters" onSubmit={submit}>
          <div className="record-filter-grid">
            <label className="record-filter record-search">
              <span>搜索{module.title}</span>
              <input
                type="search"
                aria-label={`搜索${module.title}`}
                maxLength={150}
                placeholder={
                  kind === 'audit-logs'
                    ? '搜索事件、对象编号或请求编号'
                    : '搜索关联编号、模型或名称'
                }
                value={filters.q || ''}
                onChange={(event) => set('q', event.target.value)}
              />
            </label>
            {module.filters.map((field) => (
              <FilterField
                key={field.key}
                field={field}
                value={filters[field.key] || ''}
                onChange={(value) => set(field.key, value)}
              />
            ))}
          </div>
          <div className="record-range">
            <label className="record-filter">
              <span>开始日期</span>
              <input
                type="date"
                value={filters.fromDate || ''}
                onChange={(event) => set('fromDate', event.target.value)}
              />
            </label>
            <label className="record-filter">
              <span>结束日期</span>
              <input
                type="date"
                value={filters.toDate || ''}
                onChange={(event) => set('toDate', event.target.value)}
              />
            </label>
            <span className="field-help">按本地日期筛选，包含结束当天。</span>
          </div>
          <details className="record-advanced">
            <summary>更多筛选</summary>
            <div className="record-filter-grid">
              {module.advanced.map((field) => (
                <FilterField
                  key={field.key}
                  field={field}
                  value={filters[field.key] || ''}
                  onChange={(value) => set(field.key, value)}
                />
              ))}
            </div>
          </details>
          {filterError && (
            <Notice tone="danger" role="alert">
              {filterError}
            </Notice>
          )}
          <div className="button-row">
            <button className="button" type="submit">
              查询记录
            </button>
            <button
              type="button"
              className="button secondary"
              onClick={() => {
                setFilterError('');
                setFilters({});
                setParams({});
              }}
            >
              重置筛选
            </button>
            <span className="field-help">只读记录 · 不提供手工修改或删除</span>
          </div>
        </form>
        {records.isPending ? (
          <Loading text="正在读取记录…" />
        ) : records.isError ? (
          <ErrorPanel error={records.error} retry={() => void records.refetch()} />
        ) : records.data.total === 0 ? (
          <EmptyState
            title={`暂无${module.title}`}
            description={params.size ? '没有符合条件的记录，可调整或重置筛选。' : module.empty}
            icon={module.icon}
          />
        ) : (
          <>
            <div className="table-scroll">
              <table className="runtime-table">
                <thead>
                  <tr>
                    {headers.map((header) => (
                      <th key={header} scope="col">
                        {header}
                      </th>
                    ))}
                    <th scope="col">记录时间</th>
                    <th scope="col">详情</th>
                  </tr>
                </thead>
                <tbody>
                  {records.data.items.map((record) => (
                    <tr key={record.id}>
                      <RowCells record={record} />
                      <td className="date-cell">{formatDate(record.occurredAt)}</td>
                      <td>
                        <Link to={`/${kind}/${record.id}`}>查看</Link>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="pagination">
              <span>
                共 {records.data.total} 条 · 第 {records.data.page} /{' '}
                {Math.max(1, Math.ceil(records.data.total / 20))} 页
              </span>
              <div className="button-row">
                <button
                  className="button secondary"
                  disabled={Number(query.page) <= 1}
                  onClick={() => move(-1)}
                >
                  上一页
                </button>
                <button
                  className="button secondary"
                  disabled={Number(query.page) * 20 >= records.data.total}
                  onClick={() => move(1)}
                >
                  下一页
                </button>
              </div>
            </div>
          </>
        )}
      </section>
      <Notice>
        {kind === 'evaluations'
          ? '评测来源与结果按记录展示；当前尚未接入真实判题。'
          : kind === 'usage-records'
            ? '记录页不发起调用、不预占或扣款。费用以记录的币种和结算状态区分，未知指标保持未知。'
            : '资料变更的成功日志与变更在同一事务保存。当前不采集完整对话、源码、请求体或终端全文。'}
      </Notice>
    </>
  );
}

type Item = [string, ReactNode];
function evaluationItems(record: RuntimeRecord): Item[] {
  const d = record.evaluation!;
  return [
    ['源码摘要', d.sourceDigest || '未记录'],
    ['私有测试包摘要', d.testDigest || '未记录'],
    ['执行镜像版本', d.runtimeImageId || '未记录'],
    ['提交编号', d.submissionId],
    ['判题批次', d.judgeRunId],
    ['结果版本', d.resultVersion],
    [
      '题目',
      <Link to={`/problems/${encodeURIComponent(d.problemId)}`}>
        {d.problemCode} {d.problemTitle || d.problemId}
      </Link>,
    ],
    ['学员', d.studentCode || d.studentId],
    ['学习任务', d.assignmentId || '未关联'],
    ['训练赛', d.contestId || '未关联'],
    ['评测状态', <RecordBadge value={d.status} />],
    ['评测结果', d.verdict ? <RecordBadge value={d.verdict} /> : '未产生结果'],
    ['分数', d.score === null ? '未产生分数' : `${d.score} / ${d.maxScore ?? '—'}`],
    ['耗时', known(d.durationMs, ' ms')],
    ['峰值内存', known(d.memoryPeakBytes, ' bytes')],
    ['记录来源', labels[d.authority]],
    ['错误码', d.errorCode || '无'],
  ];
}
function usageItems(record: RuntimeRecord): Item[] {
  const d = record.usage!;
  return [
    ['操作编号', d.operationId],
    ['尝试编号', d.attemptId],
    ['窗口编号', d.windowId || '未标记'],
    ['会话编号', d.sessionId || '未标记'],
    ['学员 ID', d.studentId || '未标记'],
    ['调用类型', labels[d.kind]],
    ['供应商', d.provider || '未标记'],
    ['模型', d.model || '未标记'],
    ['工具', d.tool || '未使用'],
    ['调用状态', <RecordBadge value={d.status} />],
    ['用量来源', <RecordBadge value={d.source} />],
    ['输入 Token', known(d.inputTokens)],
    ['缓存输入 Token', known(d.cachedInputTokens)],
    ['输出 Token', known(d.outputTokens)],
    ['推理 Token', known(d.reasoningTokens)],
    ['总 Token', known(d.totalTokens)],
    ['耗时', known(d.durationMs, ' ms')],
    [
      '费用',
      money(
        d.costMicros,
        d.currency,
        d.settlement === 'estimated',
        d.settlement === 'not-applicable' ? '不适用' : '待结算',
      ),
    ],
    ['结算状态', <RecordBadge value={d.settlement} />],
    ['价格版本', d.priceVersion || '未标记'],
    ['上游请求编号', d.providerRequestId || '未返回'],
    ['错误码', d.errorCode || '无'],
  ];
}
function auditItems(record: RuntimeRecord): Item[] {
  const d = record.audit!;
  return [
    ['日志类别', labels[d.category]],
    ['日志级别', <RecordBadge value={d.level} />],
    ['操作者', d.actorId === 'local' ? '本地操作者' : d.actorId],
    ['事件', labels[d.action] || d.action],
    ['事件标识', d.action],
    [
      '对象类型',
      moduleMap[d.resourceType as keyof typeof moduleMap]?.title || d.resourceType || '未关联',
    ],
    ['对象 ID', d.resourceId || '未关联'],
    ['对象编号', d.resourceCode || '未标记'],
    ['对象版本', d.resourceVersion || '未标记'],
    ['操作结果', <RecordBadge value={d.result} />],
    ['请求编号', d.requestId || '未标记'],
    ['日志摘要', d.message],
  ];
}
export function RuntimeDetailPage({ kind }: { kind: RuntimeRecordKind }) {
  const { id = '' } = useParams();
  const module = recordModuleMap[kind];
  const query = useQuery({
    queryKey: ['runtime-records', kind, id],
    queryFn: () => getRuntimeRecord(kind, id),
  });
  if (query.isPending) return <Loading text="正在读取记录详情…" />;
  if (query.isError)
    return (
      <>
        <Link className="back-link" to={`/${kind}`}>
          返回{module.title}
        </Link>
        <ErrorPanel error={query.error} retry={() => void query.refetch()} />
      </>
    );
  const record = query.data;
  const items = record.evaluation
    ? evaluationItems(record)
    : record.usage
      ? usageItems(record)
      : record.audit
        ? auditItems(record)
        : [];
  return (
    <>
      <Link className="back-link" to={`/${kind}`}>
        返回{module.title}
      </Link>
      <PageHeading
        eyebrow="运行记录 / 详情"
        title={`${module.title}详情`}
        description="原始运行记录仅供查询，不能通过此页修改或删除。"
      />
      <div className="record-meta">
        <span>记录 ID {record.id}</span>
        <span>{formatDate(record.occurredAt)}</span>
      </div>
      <dl className="runtime-details">
        {items.map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
      {record.evaluation?.authority === 'institution' && (
        <RejudgeAction jobId={record.evaluation.judgeRunId} />
      )}
      <Notice>
        {kind === 'evaluations'
          ? '隐藏测试输入、标准答案和私有原始日志不会展示在这里。'
          : kind === 'usage-records'
            ? '未知用量不会补成 0；本页不计算供应商账单或修改余额。'
            : '日志保留脱敏摘要与请求编号，不包含资料正文和秘密。'}
      </Notice>
    </>
  );
}
