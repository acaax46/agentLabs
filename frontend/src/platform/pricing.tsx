import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useAuth } from '../auth/context';
import { get, write } from './api';
import type { Page, Price, RateSheet } from './api';
import { decimalToMicros, formatDateTime, formatUnitPrice } from './model';
import { usePlatform } from './state';
import { MutationError, QueryState } from './shared';
import { SectionHeader } from './ui';

export function ModelPrice({ price }: { price?: Price | null }) {
  if (!price) return <p className="wb-muted">模型价格未配置；请管理员发布价格版本。</p>;
  return (
    <div className="wb-muted">
      <p>
        每百万 tokens：输入 {formatUnitPrice(price.inputMicros, price.currency)} / 缓存命中{' '}
        {formatUnitPrice(
          price.cachedInputMicros === '0' ? price.inputMicros : price.cachedInputMicros,
          price.currency,
        )}{' '}
        / 输出 {formatUnitPrice(price.outputMicros, price.currency)} {price.currency}。
      </p>
      <p>
        {price.note}{' '}
        {price.referenceUrl && (
          <a className="wb-text-link" href={price.referenceUrl} target="_blank" rel="noreferrer">
            价格参考
          </a>
        )}
      </p>
      <small>
        价格版本 {price.name} · 生效 {formatDateTime(price.effectiveFrom)}
        {price.referenceAt && ` · 核对 ${formatDateTime(price.referenceAt)}`}
      </small>
    </div>
  );
}

export function PricingPanel() {
  const { projectId, project } = usePlatform();
  const { user } = useAuth();
  const cache = useQueryClient();
  const [provider, setProvider] = useState('openai-compatible');
  const [selected, setSelected] = useState<Price | null>(null);
  const [message, setMessage] = useState('');
  const rates = useQuery({
    queryKey: ['platform', 'rates', projectId],
    queryFn: ({ signal }) => get<RateSheet>('/billing/rates', { projectId }, signal),
    enabled: !!projectId,
    retry: false,
  });
  const prices = useQuery({
    queryKey: ['platform', 'prices'],
    queryFn: ({ signal }) => get<Page<Price>>('/admin/prices', { limit: 100 }, signal),
    retry: false,
  });
  const refresh = () => void cache.invalidateQueries({ queryKey: ['platform'] });
  const presets = useMutation({
    mutationFn: () => write<Page<Price>>('/admin/prices/presets', { provider: provider.trim() }),
    onSuccess: (data) => {
      setMessage(`已发布 ${data.items.length} 项缺失价格；已有管理员价格保留。`);
      refresh();
    },
  });
  const publish = useMutation({
    mutationFn: async (form: HTMLFormElement) => {
      if (!selected) return;
      const data = new FormData(form);
      const input: Record<string, unknown> = {
        kind: selected.kind,
        name: String(data.get('name')),
        currency: selected.currency,
        provider: selected.provider,
        model: selected.model,
        resourceProfile: selected.resourceProfile,
        sandboxBilling:
          selected.kind === 'execution' ? String(data.get('sandboxBilling')) : undefined,
        tool: selected.tool,
        unit: selected.unit,
        note: String(data.get('note')),
        referenceUrl: selected.referenceUrl,
        referenceAt: selected.referenceAt,
      };
      for (const field of selected.kind === 'model'
        ? ['inputMicros', 'cachedInputMicros', 'outputMicros']
        : [selected.kind === 'tool' ? 'perUnitMicros' : 'perSecondMicros']) {
        input[field] = decimalToMicros(String(data.get(field)));
      }
      await write<Price>('/admin/prices', input);
    },
    onSuccess: () => {
      setSelected(null);
      setMessage('新价格版本已发布，仅影响后续运行。');
      refresh();
    },
  });
  const currency = project?.currency;
  const displayed = prices.data?.items.filter((price) => price.currency === currency) ?? [];
  const decimal = (value: string) =>
    `${BigInt(value) / 1_000_000n}.${(BigInt(value) % 1_000_000n).toString().padStart(6, '0')}`;
  return (
    <section className="wb-panel">
      <SectionHeader
        title="价格与资源规格"
        description="Agent API 与工作台使用同一计费路径：平台模型 + 托管服务秒 + 沙箱规格秒 + 工具；BYOK 模型由客户承担。提交时固定价格，旧运行不随调价变化。"
      />
      <QueryState pending={rates.isPending} error={rates.error} retry={rates.refetch}>
        <p className="wb-muted">
          托管服务：
          {rates.data?.execution
            ? `${formatUnitPrice(rates.data.execution.perSecondMicros, rates.data.execution.currency)} ${rates.data.execution.currency} / 运行秒`
            : '价格未配置'}
          。
          {rates.data?.execution
            ? rates.data.execution.sandboxBilling === 'separate'
              ? '托管单价仅含服务费，沙箱规格另计。'
              : '当前托管单价已含算力，沙箱价格不叠加。'
            : '管理员发布价格后才能计费运行。'}
          运行秒含模型等待，不含排队。沙箱仅在隔离执行期间计费，每次不足一秒向上取整；含启动与回收。GPU
          规格为预设，尚未开放。
        </p>
        <div className="wb-table-wrap">
          <table className="wb-table">
            <thead>
              <tr>
                <th>规格</th>
                <th>CPU / 内存 / GPU</th>
                <th>沙箱秒单价</th>
                <th>可用状态</th>
              </tr>
            </thead>
            <tbody>
              {rates.data?.resources.map((resource) => (
                <tr key={resource.id}>
                  <td>
                    {resource.name}
                    <small className="wb-mono"> {resource.id}</small>
                  </td>
                  <td>
                    {resource.cpu} vCPU / {resource.memoryMB / 1024} GiB /{' '}
                    {resource.gpu ? resource.gpuType : '无 GPU'}
                  </td>
                  <td>
                    {resource.price
                      ? `${formatUnitPrice(resource.price.perSecondMicros, resource.price.currency)} ${resource.price.currency} / 秒`
                      : '未配置'}
                  </td>
                  <td>{resource.available ? '可用' : resource.reason}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </QueryState>
      <details>
        <summary>模型与其他价格版本</summary>
        <QueryState pending={prices.isPending} error={prices.error} retry={prices.refetch}>
          <p className="wb-muted">
            列出最近 100 个价格版本，含历史和待生效版本；实际可用模型与当前价格以 Playground 为准。
          </p>
          <div className="wb-table-wrap">
            <table className="wb-table">
              <thead>
                <tr>
                  <th>计费项 / 版本</th>
                  <th>价格（{currency}）</th>
                  <th>生效 / 来源</th>
                  {user?.role === 'admin' && <th>操作</th>}
                </tr>
              </thead>
              <tbody>
                {displayed.map((price) => (
                  <tr key={price.id}>
                    <td>
                      {price.model || price.resourceProfile || price.tool || '托管服务'}
                      <small className="wb-mono"> {price.name}</small>
                      {price.provider && <p>{price.provider}</p>}
                    </td>
                    <td>
                      {price.kind === 'model' ? (
                        <ModelPrice price={price} />
                      ) : (
                        `${formatUnitPrice(price.kind === 'tool' ? price.perUnitMicros : price.perSecondMicros, price.currency)} ${price.currency} / ${price.unit || 'second'}`
                      )}
                    </td>
                    <td>
                      {formatDateTime(price.effectiveFrom)}
                      <p>{price.note}</p>
                    </td>
                    {user?.role === 'admin' && (
                      <td>
                        <button
                          className="wb-button wb-button--secondary"
                          onClick={() => {
                            setSelected(price);
                            publish.reset();
                          }}
                        >
                          发布新版本
                        </button>
                      </td>
                    )}
                  </tr>
                ))}
                {!displayed.length && (
                  <tr>
                    <td colSpan={4}>尚无价格版本。</td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </QueryState>
      </details>
      {user?.role === 'admin' && (
        <>
          <form
            className="wb-form"
            onSubmit={(event) => {
              event.preventDefault();
              presets.mutate();
            }}
          >
            <p>
              初始化三款 Flash 模型、托管服务、沙箱与工具价格。模型参考 OpenRouter 2026-10-24
              接口基础价；CNY 使用固定汇率 7.20，无加价。资源价格为平台预设。
            </p>
            <label className="wb-field">
              <span>管理员模型渠道的供应商标识</span>
              <input
                required
                maxLength={80}
                value={provider}
                onChange={(event) => setProvider(event.target.value)}
              />
              <small>必须与模型连接的供应商标识相同，例如 openai-compatible。</small>
            </label>
            <button className="wb-button wb-button--secondary" disabled={presets.isPending}>
              {presets.isPending ? '正在发布…' : '发布缺失的预设价格'}
            </button>
            <MutationError error={presets.error} />
          </form>
          {selected && (
            <form
              key={selected.id}
              className="wb-form"
              onSubmit={(event) => {
                event.preventDefault();
                publish.mutate(event.currentTarget);
              }}
            >
              <h3>
                发布新价格：
                {selected.model || selected.resourceProfile || selected.tool || '托管服务'}
              </h3>
              <p>
                币种 {selected.currency}；模型按每百万
                tokens，资源与托管按秒。提交后立即生效，仅影响新 Run。
              </p>
              {selected.kind === 'execution' && (
                <label className="wb-field">
                  <span>沙箱算力计价方式</span>
                  <select name="sandboxBilling" defaultValue={selected.sandboxBilling || ''}>
                    <option value="">已包含在托管价格中</option>
                    <option value="separate">按沙箱规格另列</option>
                  </select>
                  <small>切换为另列前请确认新托管单价只包含服务费，以免重复计收算力。</small>
                </label>
              )}
              <label className="wb-field">
                <span>价格版本名称</span>
                <input
                  required
                  name="name"
                  maxLength={80}
                  defaultValue={`${selected.name.slice(0, 55)}-revision`}
                />
              </label>
              <div className="wb-form-grid">
                {(selected.kind === 'model'
                  ? [
                      ['inputMicros', '输入'],
                      ['cachedInputMicros', '缓存命中（0 表示同输入价）'],
                      ['outputMicros', '输出'],
                    ]
                  : [[selected.kind === 'tool' ? 'perUnitMicros' : 'perSecondMicros', '单价']]
                ).map(([field, label]) => (
                  <label className="wb-field" key={field}>
                    <span>
                      {label}（{selected.currency}）
                    </span>
                    <input
                      required
                      name={field}
                      inputMode="decimal"
                      pattern="[0-9]+(\.[0-9]{1,6})?"
                      defaultValue={decimal(selected[field as keyof Price] as string)}
                    />
                  </label>
                ))}
              </div>
              <label className="wb-field">
                <span>计费说明</span>
                <textarea name="note" maxLength={300} defaultValue={selected.note} />
              </label>
              <div className="wb-form-actions">
                <button className="wb-button wb-button--primary" disabled={publish.isPending}>
                  {publish.isPending ? '正在发布…' : '确认发布价格版本'}
                </button>
                <button
                  type="button"
                  className="wb-button wb-button--ghost"
                  onClick={() => setSelected(null)}
                >
                  取消
                </button>
              </div>
              <MutationError error={publish.error} />
            </form>
          )}
        </>
      )}
      <p role="status">{message}</p>
    </section>
  );
}
