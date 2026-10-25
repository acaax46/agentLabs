import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { allRecords } from '../api/client';
import { ErrorPanel, Loading, Notice, PageHeading } from '../catalog/shared';
import { learning } from './api';
import type { Budget, Price, Ledger } from './api';
function micros(text: string) {
  if (!/^\d+(\.\d{1,6})?$/.test(text)) throw new Error('金额需要非负数，最多六位小数。');
  const [n, d = ''] = text.split('.');
  return (BigInt(n!) * 1000000n + BigInt(d.padEnd(6, '0'))).toString();
}
function amount(text: string) {
  const n = BigInt(text);
  return `${n / 1000000n}.${(n % 1000000n).toString().padStart(6, '0')}`;
}
export function BudgetsPage() {
  const cache = useQueryClient();
  const accounts = useQuery({
    queryKey: ['learning', 'budgets'],
    queryFn: () => learning<Budget[]>('budgets'),
  });
  const prices = useQuery({
    queryKey: ['learning', 'prices'],
    queryFn: () => learning<Price[]>('prices'),
  });
  const ledger = useQuery({
    queryKey: ['learning', 'ledger'],
    queryFn: () => learning<Ledger[]>('ledger'),
  });
  const classes = useQuery({
    queryKey: ['catalog', 'options', 'classes'],
    queryFn: () => allRecords('classes'),
  });
  const students = useQuery({
    queryKey: ['catalog', 'options', 'students'],
    queryFn: () => allRecords('students'),
  });
  const [scope, setScope] = useState('tenant'),
    [target, setTarget] = useState(''),
    [currency, setCurrency] = useState('USD'),
    [limit, setLimit] = useState(''),
    [notice, setNotice] = useState('');
  const [price, setPrice] = useState({
    id: '',
    provider: '',
    model: '',
    currency: 'USD',
    input: '',
    output: '',
    cachedRead: '',
    cacheWrite: '',
    maximum: '',
  });
  const budget = useMutation({
    mutationFn: () => {
      const current = accounts.data?.find(
        (a) =>
          a.scope === scope &&
          (scope === 'tenant' || a.targetId === target) &&
          a.currency === currency,
      );
      return learning<Budget>('budget', '', {
        scope,
        targetId: target,
        currency,
        limitMicros: micros(limit),
        version: current?.version || 0,
      });
    },
    onSuccess: () => {
      setNotice('预算已保存。');
      void cache.invalidateQueries({ queryKey: ['learning'] });
    },
  });
  const addPrice = useMutation({
    mutationFn: () =>
      learning<Price>('price', '', {
        id: price.id,
        provider: price.provider,
        model: price.model,
        currency: price.currency,
        inputMicros: micros(price.input),
        outputMicros: micros(price.output),
        cachedReadMicros: micros(price.cachedRead),
        cacheWriteMicros: micros(price.cacheWrite),
        maximumMicros: micros(price.maximum),
      }),
    onSuccess: () => {
      setNotice('价格版本已创建；后续修改需使用新版本号。');
      void cache.invalidateQueries({ queryKey: ['learning'] });
    },
  });
  return (
    <>
      <PageHeading
        eyebrow="机构 / 用量管理"
        title="预算与用量账本"
        description="按机构、班级、学生与代码空间限制预算。已发送但结果未知的请求继续保留预占。"
      />
      {notice && <p role="status">{notice}</p>}
      <Notice>
        这里配置的是模型使用上限，不代表充值余额。价格版本需按实际供应商合同填写；没有最终用量证据时不结算。每个代码空间首次调用时继承学生预算上限，所有上级限制仍同时生效。
      </Notice>
      <section className="learning-panel">
        <h2>配置预算上限</h2>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            budget.mutate();
          }}
          className="form-grid"
        >
          <label className="field">
            预算范围
            <select
              value={scope}
              onChange={(e) => {
                setScope(e.target.value);
                setTarget('');
              }}
            >
              <option value="tenant">当前机构</option>
              <option value="class">班级</option>
              <option value="student">学生</option>
              <option value="window">代码空间</option>
            </select>
          </label>
          {['class', 'student'].includes(scope) ? (
            <label className="field">
              关联资料
              <select required value={target} onChange={(e) => setTarget(e.target.value)}>
                <option value="">请选择</option>
                {(scope === 'class' ? classes.data : students.data)?.map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.code} · {r.name}
                  </option>
                ))}
              </select>
            </label>
          ) : scope === 'window' ? (
            <label className="field">
              代码空间编号
              <input required value={target} onChange={(e) => setTarget(e.target.value)} />
            </label>
          ) : null}
          <label className="field">
            币种
            <select value={currency} onChange={(e) => setCurrency(e.target.value)}>
              <option>USD</option>
              <option>CNY</option>
            </select>
          </label>
          <label className="field">
            累计预算上限
            <input
              required
              inputMode="decimal"
              value={limit}
              onChange={(e) => setLimit(e.target.value)}
            />
          </label>
          <div className="full-width">
            <button className="button" disabled={budget.isPending}>
              保存预算
            </button>
          </div>
        </form>
        {budget.error && <ErrorPanel error={budget.error} />}
      </section>
      <section className="learning-panel">
        <h2>当前预算</h2>
        {accounts.isPending ? (
          <Loading />
        ) : accounts.isError ? (
          <ErrorPanel error={accounts.error} />
        ) : (
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>范围 / 对象</th>
                  <th>币种</th>
                  <th>上限</th>
                  <th>预占</th>
                  <th>已用</th>
                </tr>
              </thead>
              <tbody>
                {accounts.data.map((a) => (
                  <tr key={a.id}>
                    <td>
                      {
                        (
                          {
                            tenant: '机构',
                            class: '班级',
                            student: '学生',
                            window: '代码空间',
                          } as Record<string, string>
                        )[a.scope]
                      }{' '}
                      · {a.targetId}
                    </td>
                    <td>{a.currency}</td>
                    <td>{amount(a.limitMicros)}</td>
                    <td>{amount(a.heldMicros)}</td>
                    <td>{amount(a.spentMicros)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            {!accounts.data.length && <p>尚未配置预算。</p>}
          </div>
        )}
      </section>
      <section className="learning-panel">
        <h2>创建不可变价格版本</h2>
        <form
          className="form-grid"
          onSubmit={(e) => {
            e.preventDefault();
            addPrice.mutate();
          }}
        >
          {(
            [
              { key: 'id', label: '价格版本号' },
              { key: 'provider', label: '供应商标识' },
              { key: 'model', label: '模型标识' },
              { key: 'input', label: '每百万输入 Token 价格' },
              { key: 'output', label: '每百万输出 Token 价格' },
              { key: 'cachedRead', label: '每百万缓存读取 Token 价格' },
              { key: 'cacheWrite', label: '每百万缓存写入 Token 价格' },
              { key: 'maximum', label: '单次调用预占金额' },
            ] as const
          ).map((f) => (
            <label className="field" key={f.key}>
              {f.label}
              <input
                required
                value={price[f.key]}
                onChange={(e) => setPrice({ ...price, [f.key]: e.target.value })}
              />
            </label>
          ))}
          <label className="field">
            价格币种
            <select
              value={price.currency}
              onChange={(e) => setPrice({ ...price, currency: e.target.value })}
            >
              <option>USD</option>
              <option>CNY</option>
            </select>
          </label>
          <div className="full-width">
            <button className="button" disabled={addPrice.isPending}>
              创建价格版本
            </button>
          </div>
        </form>
        {addPrice.error && <ErrorPanel error={addPrice.error} />}
        <ul className="release-history">
          {prices.data?.map((p) => (
            <li key={p.id}>
              <strong>{p.id}</strong>
              <span>
                {p.provider} / {p.model} · 单次预占 {amount(p.maximumMicros)} {p.currency}
              </span>
            </li>
          ))}
        </ul>
        {prices.isError && <ErrorPanel error={prices.error} />}
      </section>
      <section className="learning-panel">
        <h2>最近账本事件</h2>
        {ledger.isError ? (
          <ErrorPanel error={ledger.error} />
        ) : (
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>操作</th>
                  <th>任务</th>
                  <th>预算对象</th>
                  <th>金额</th>
                </tr>
              </thead>
              <tbody>
                {ledger.data?.map((l) => (
                  <tr key={l.id}>
                    <td>
                      {(
                        { reserve: '预占', settle: '结算', release: '释放' } as Record<
                          string,
                          string
                        >
                      )[l.type] || l.type}
                    </td>
                    <td>{l.jobId}</td>
                    <td>{l.accountId}</td>
                    <td>
                      {amount(l.amountMicros)} {l.currency}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {ledger.data?.length === 0 && <p>暂无账本事件。系统不会自动插入模拟费用。</p>}
          </div>
        )}
      </section>
    </>
  );
}
