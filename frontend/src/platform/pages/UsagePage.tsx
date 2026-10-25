import { useQuery } from '@tanstack/react-query';
import { Link, useSearchParams } from 'react-router-dom';
import { get } from '../api';
import type { Key, Ledger, UsageLine, UsageSummary } from '../api';
import { usePlatform } from '../state';
import {
  formatDateTime,
  formatMoney,
  formatQuantity,
  ledgerTypeLabels,
  usageCategoryLabels,
} from '../model';
import { QueryState, RequireProject, usePage, useList } from '../shared';
import { Money, Pagination } from '../controls';
import { EmptyState, PageHeader, Tabs, billingBadge } from '../ui';
import { PricingPanel } from '../pricing';
import { csv, saveText } from '../files';
import { CurrencySummary } from '../CurrencySummary';

export function UsagePage() {
  const { projectId } = usePlatform();
  const [params, setParams] = useSearchParams();
  const runId = params.get('run') ?? undefined;
  const view = params.get('view') === 'ledger' ? 'ledger' : 'usage';
  const category = params.get('category') ?? '';
  const from = params.get('from') ?? undefined;
  const to = params.get('to') ?? undefined;
  const keyId = params.get('keyId') ?? undefined;
  const meter = params.get('meter') ?? '';
  const ledgerType = params.get('ledgerType') ?? undefined;
  const keys = useList<Key>('/api-keys');
  const summary = useQuery({
    queryKey: ['platform', 'usage-summary', projectId, runId, keyId, from, to],
    queryFn: ({ signal }) =>
      get<UsageSummary>('/usage/summary', { projectId, runId, keyId, from, to }, signal),
    enabled: !!projectId,
    retry: false,
  });
  const usagePage = usePage<UsageLine>('/usage', { runId, keyId, category, from, to });
  const ledgerPage = usePage<Ledger>('/billing/ledger', { runId, type: ledgerType, from, to });
  const usage = {
    ...usagePage,
    data: usagePage.data?.items.filter(
      (line) => !meter || line.meter.toLowerCase().includes(meter.toLowerCase()),
    ),
  };
  const usageCategoryOptions = Object.entries(usageCategoryLabels).filter(([key]) =>
    ['model', 'tool', 'execution', 'storage', 'adjustment'].includes(key),
  );
  const ledger = { ...ledgerPage, data: ledgerPage.data?.items };
  const activePage = view === 'usage' ? usagePage : ledgerPage;
  const update = (values: Record<string, string>) => {
    const next = new URLSearchParams(params);
    next.delete('cursor');
    next.delete('previous');
    Object.entries(values).forEach(([key, value]) =>
      value ? next.set(key, value) : next.delete(key),
    );
    setParams(next);
  };
  return (
    <>
      <PageHeader
        title="用量与账单"
        description="核对平台计量与只追加账本。预算不是充值余额；BYOK 模型费用由客户自行承担。"
      />
      <RequireProject>
        <CurrencySummary from={from} to={to} />
        <PricingPanel />
        <form
          className="wb-toolbar wb-toolbar--wrap"
          onSubmit={(event) => {
            event.preventDefault();
            const data = new FormData(event.currentTarget);
            const start = String(data.get('from'));
            const end = String(data.get('to'));
            update({
              from: start ? new Date(start).toISOString() : '',
              to: end ? new Date(end).toISOString() : '',
            });
          }}
        >
          <label className="wb-field">
            <span>开始时间</span>
            <input type="datetime-local" name="from" />
          </label>
          <label className="wb-field">
            <span>结束时间（不含）</span>
            <input type="datetime-local" name="to" />
          </label>
          <button className="wb-button wb-button--secondary">应用时间范围</button>
          <button
            type="button"
            className="wb-button wb-button--ghost"
            onClick={() => update({ from: '', to: '' })}
          >
            清除时间范围
          </button>
        </form>
        {(from || to) && (
          <p className="wb-muted">
            范围：{from ? formatDateTime(from) : '不限'} 至 {to ? formatDateTime(to) : '不限'}
          </p>
        )}
        {runId && (
          <p className="wb-muted">
            当前运行：
            <Link className="wb-text-link wb-mono" to={`/runs/${runId}`}>
              {runId}
            </Link>{' '}
            <Link className="wb-text-link" to="/usage">
              清除筛选
            </Link>
          </p>
        )}
        <QueryState pending={summary.isPending} error={summary.error} retry={summary.refetch}>
          {summary.data && (
            <section className="wb-stat-grid wb-stat-grid--billing">
              <article className="wb-stat">
                <div className="wb-stat-head">已结算（{summary.data.currency}）</div>
                <strong className="wb-mono">
                  {formatMoney(summary.data.settledMicros, summary.data.currency)}
                </strong>
                <p>平台已结算金额，不代表供应商最终账单。</p>
              </article>
              <article className="wb-stat">
                <div className="wb-stat-head">待核对（{summary.data.currency}）</div>
                <strong className="wb-mono">
                  {formatMoney(
                    summary.data.groups.some(
                      (group) => group.status === 'pending' && group.amountKnown === false,
                    )
                      ? null
                      : summary.data.pendingMicros,
                    summary.data.currency,
                  )}
                </strong>
                <p>未知结果保留预占，不能自动退款。</p>
              </article>
              <article className="wb-stat">
                <div className="wb-stat-head">BYOK 模型费用</div>
                <strong>客户承担</strong>
                <p>不计入平台应收。</p>
              </article>
            </section>
          )}
        </QueryState>
        <section className="wb-panel">
          <h2>分项汇总</h2>
          <div className="wb-table-wrap">
            <table className="wb-table">
              <thead>
                <tr>
                  <th>类别</th>
                  <th>数量 / 单位</th>
                  <th>金额</th>
                  <th>结算状态</th>
                </tr>
              </thead>
              <tbody>
                {summary.data?.groups.map((group, index) => (
                  <tr key={index}>
                    <td>
                      {group.category === 'model' && group.status === 'not_applicable'
                        ? 'BYOK 模型'
                        : (usageCategoryLabels[
                            group.category as keyof typeof usageCategoryLabels
                          ] ?? group.category)}
                    </td>
                    <td>
                      {group.quantityKnown === false ? '未知' : formatQuantity(group.quantity)}{' '}
                      {group.unit}
                    </td>
                    <td>
                      {group.status === 'not_applicable' ? (
                        group.category === 'storage' ? (
                          '含在基础额度内'
                        ) : (
                          '由客户承担'
                        )
                      ) : (
                        <Money
                          value={group.amountKnown === false ? null : group.amountMicros}
                          currency={summary.data!.currency}
                        />
                      )}
                    </td>
                    <td>
                      {billingBadge(group.status)}
                      {group.note && <small className="wb-cell-stack">{group.note}</small>}
                    </td>
                  </tr>
                ))}
                {!summary.data?.groups.length && (
                  <tr>
                    <td colSpan={4}>所选范围内没有计量记录。</td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </section>
        <section className="wb-panel">
          <div className="wb-toolbar">
            <Tabs
              label="账单视图"
              value={view}
              onChange={(next) => update({ view: next === 'ledger' ? 'ledger' : '' })}
              items={[
                { value: 'usage', label: '用量明细' },
                { value: 'ledger', label: '只追加账本' },
              ]}
            />
            {view === 'usage' && (
              <label className="wb-select">
                <span id="usage-category-label">计量类别</span>
                <select
                  id="usage-category"
                  aria-labelledby="usage-category-label"
                  value={category}
                  onChange={(event) => update({ category: event.target.value })}
                >
                  <option value="">全部类别</option>
                  {usageCategoryOptions.map(([key, label]) => (
                    <option key={key} value={key}>
                      {label}
                    </option>
                  ))}
                </select>
              </label>
            )}
          </div>
          <div className="wb-toolbar wb-toolbar--wrap">
            {view === 'usage' && (
              <>
                <label className="wb-select">
                  <span>API Key 用量</span>
                  <select
                    value={keyId ?? ''}
                    onChange={(event) => update({ keyId: event.target.value })}
                  >
                    <option value="">全部调用身份</option>
                    {keys.data?.map((key) => (
                      <option key={key.id} value={key.id}>
                        {key.name}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="wb-field">
                  <span>工具或计量项（本页）</span>
                  <input
                    value={meter}
                    onChange={(event) => update({ meter: event.target.value })}
                    placeholder="如 files.transform 或 tokens"
                  />
                </label>
              </>
            )}
            {view === 'ledger' && (
              <label className="wb-select">
                <span>账本类型</span>
                <select
                  value={ledgerType ?? ''}
                  onChange={(event) => update({ ledgerType: event.target.value })}
                >
                  <option value="">全部类型</option>
                  {Object.entries(ledgerTypeLabels).map(([value, label]) => (
                    <option key={value} value={value}>
                      {label}
                    </option>
                  ))}
                </select>
              </label>
            )}
            <button
              className="wb-button wb-button--secondary"
              disabled={view === 'usage' ? !usage.data?.length : !ledger.data?.length}
              onClick={() =>
                view === 'usage'
                  ? saveText(
                      'usage-current-page.csv',
                      csv(
                        [
                          'id',
                          'runId',
                          'keyId',
                          'category',
                          'meter',
                          'quantity',
                          'unit',
                          'amountMicros',
                          'currency',
                          'status',
                          'basis',
                          'priceId',
                          'at',
                        ],
                        (usage.data ?? []).map((line) => [
                          line.id,
                          line.runId,
                          line.keyId,
                          line.category,
                          line.meter,
                          line.basis === 'unknown' ? null : line.quantity,
                          line.unit,
                          line.basis === 'unknown' && line.status === 'pending'
                            ? null
                            : line.amountMicros,
                          line.currency,
                          line.status,
                          line.basis,
                          line.priceId,
                          line.at,
                        ]),
                      ),
                      'text/csv;charset=utf-8',
                    )
                  : saveText(
                      'ledger-current-page.csv',
                      csv(
                        [
                          'id',
                          'runId',
                          'accountId',
                          'scope',
                          'type',
                          'amountMicros',
                          'currency',
                          'at',
                          'note',
                        ],
                        (ledger.data ?? []).map((line) => [
                          line.id,
                          line.runId,
                          line.accountId,
                          line.scope,
                          line.type,
                          line.amountMicros,
                          line.currency,
                          line.at,
                          line.note,
                        ]),
                      ),
                      'text/csv;charset=utf-8',
                    )
              }
            >
              导出当前页 CSV
            </button>
          </div>
          <p className="wb-field-help">
            工具/计量项只过滤当前页，CSV 导出当前页可见明细。账本按项目、Run、类型、时间查询；不将
            API Key 用量筛选冒充账本筛选。
          </p>
          {view === 'usage' ? (
            <QueryState pending={usage.isPending} error={usage.error} retry={usage.refetch}>
              {!usage.data?.length ? (
                <EmptyState title="暂无用量" description="运行产生计量证据后在此显示。" />
              ) : (
                <div className="wb-table-wrap">
                  <table className="wb-table">
                    <thead>
                      <tr>
                        <th>计量项</th>
                        <th>来源</th>
                        <th>数量</th>
                        <th>金额</th>
                        <th>结算状态</th>
                        <th>Run</th>
                        <th>时间 / 说明</th>
                      </tr>
                    </thead>
                    <tbody>
                      {usage.data.map((line) => (
                        <tr key={line.id}>
                          <td>
                            {line.meter}
                            <details>
                              <summary>计量证据</summary>
                              <p className="wb-mono">记录 {line.id}</p>
                              <p className="wb-mono">价格 {line.priceId ?? '未提供'}</p>
                              <p className="wb-mono">API Key {line.keyId ?? '未提供'}</p>
                              <p>依据 {line.basis}</p>
                            </details>
                          </td>
                          <td>{line.source}</td>
                          <td className="wb-mono">
                            {line.basis === 'unknown'
                              ? '未知'
                              : `${formatQuantity(line.quantity)} ${line.unit}`}
                          </td>
                          <td className="wb-mono">
                            {line.status === 'not_applicable' && line.category === 'storage'
                              ? '含在基础额度内'
                              : line.status === 'not_applicable' && line.category === 'model'
                                ? '由客户承担'
                                : `${formatMoney(line.basis === 'unknown' && line.status === 'pending' ? null : line.amountMicros, line.currency)} ${line.currency}`}
                          </td>
                          <td>{billingBadge(line.status)}</td>
                          <td>
                            {line.runId ? (
                              <Link className="wb-table-link wb-mono" to={`/runs/${line.runId}`}>
                                {line.runId}
                              </Link>
                            ) : (
                              '—'
                            )}
                          </td>
                          <td>
                            <span className="wb-cell-stack">
                              <span>{formatDateTime(line.at)}</span>
                              <small>
                                {line.note} · {line.basis}
                              </small>
                            </span>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </QueryState>
          ) : (
            <QueryState pending={ledger.isPending} error={ledger.error} retry={ledger.refetch}>
              {!ledger.data?.length ? (
                <EmptyState
                  title="暂无账本记录"
                  description="预占、结算、释放和调整由后端业务事务追加。"
                />
              ) : (
                <div className="wb-table-wrap">
                  <table className="wb-table">
                    <thead>
                      <tr>
                        <th>类型</th>
                        <th>金额</th>
                        <th>账户范围</th>
                        <th>Run</th>
                        <th>时间</th>
                        <th>说明</th>
                      </tr>
                    </thead>
                    <tbody>
                      {ledger.data.map((entry) => (
                        <tr key={entry.id}>
                          <td>{ledgerTypeLabels[entry.type]}</td>
                          <td className="wb-mono">
                            {BigInt(entry.amountMicros) > 0n ? '+' : ''}
                            {formatMoney(entry.amountMicros, entry.currency)} {entry.currency}
                          </td>
                          <td>{entry.scope}</td>
                          <td>
                            {entry.runId ? (
                              <Link className="wb-table-link wb-mono" to={`/runs/${entry.runId}`}>
                                {entry.runId}
                              </Link>
                            ) : (
                              '—'
                            )}
                          </td>
                          <td>{formatDateTime(entry.at)}</td>
                          <td>
                            {entry.note}
                            <details>
                              <summary>账本证据</summary>
                              <p className="wb-mono">记录 {entry.id}</p>
                              <p className="wb-mono">账户 {entry.accountId ?? '未提供'}</p>
                            </details>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </QueryState>
          )}
          <Pagination
            nextCursor={activePage.data?.nextCursor}
            page={activePage.page}
            pending={activePage.isFetching}
            onNext={activePage.next}
            onPrevious={activePage.previous}
          />
        </section>
      </RequireProject>
    </>
  );
}
