import { useRef, useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { useSearchParams } from 'react-router-dom';
import { requestKey, write } from '../api';
import type { Key } from '../api';
import { usePlatform } from '../state';
import { useAuth } from '../../auth/context';
import { decimalToMicros, expiryLabel, formatMoney, maskKey, timeAgo } from '../model';
import { MutationError, QueryState, RequireProject, usePage } from '../shared';
import { Field, Pagination } from '../controls';
import { CopyButton, EmptyState, Modal, PageHeader, StatusBadge } from '../ui';

const scopes = [
  ['runs:write', '提交运行'],
  ['runs:read', '读取运行与事件'],
  ['files:write', '上传文件'],
  ['artifacts:read', '读取产物'],
  ['usage:read', '读取用量'],
  ['tools:call', '调用工具'],
] as const;
function CreateKey({ onClose }: { onClose: () => void }) {
  const { projectId, project, refresh } = usePlatform();
  const key = useRef(requestKey());
  const expiresAt = useRef<string | null>(null);
  const [created, setCreated] = useState<(Key & { secret: string }) | null>(null);
  const [saved, setSaved] = useState(false);
  const mutation = useMutation({
    mutationFn: async (form: FormData) => {
      const days = String(form.get('expiry'));
      // Hold the exact expiry for a manual retry of the same request.
      if (!expiresAt.current && days !== 'never')
        expiresAt.current = new Date(Date.now() + Number(days) * 86_400_000).toISOString();
      const amount = String(form.get('limit')).trim();
      const input = {
        projectId,
        name: String(form.get('name')).trim(),
        scopes: form.getAll('scopes').map(String),
        expiresAt: days === 'never' ? null : expiresAt.current,
        monthlyLimitMicros: amount ? decimalToMicros(amount) : null,
        ratePerMinute: Number(form.get('rate')),
      };
      if (!input.scopes.length) throw new Error('至少选择一个 scope。');
      const result = await write<Key & { secret: string }>('/api-keys', input, key.current(input));
      if (!result.secret)
        throw new Error('服务未再次返回完整密钥。请核对密钥列表；如未保存，撤销后重新签发。');
      setCreated(result);
      refresh();
      // Do not return the secret into React Query's mutation cache.
    },
    retry: false,
    gcTime: 0,
  });
  return (
    <Modal
      open
      onClose={() => {
        if (!mutation.isPending) {
          if (created && !saved) return;
          setCreated(null);
          onClose();
        }
      }}
      title={created ? 'API Key 已创建' : '创建 API Key'}
      description={
        created
          ? '完整密钥只显示一次，请立即保存。'
          : `所属项目：${project?.name}。密钥权限不能超过项目授权。`
      }
    >
      {created ? (
        <div className="wb-key-created">
          <div className="wb-secret-row">
            <code>{created.secret}</code>
            <CopyButton value={created.secret} label="复制密钥" />
          </div>
          <p>关闭后仅保留前缀。完整密钥不写入浏览器存储。</p>
          <label className="wb-check">
            <input
              type="checkbox"
              checked={saved}
              onChange={(event) => setSaved(event.target.checked)}
            />
            <span>我确认已将完整密钥保存到安全位置</span>
          </label>
          <button
            className="wb-button wb-button--primary"
            disabled={!saved}
            onClick={() => {
              setCreated(null);
              onClose();
            }}
          >
            我已保存
          </button>
        </div>
      ) : (
        <form
          className="wb-form"
          onSubmit={(event) => {
            event.preventDefault();
            mutation.mutate(new FormData(event.currentTarget));
          }}
        >
          <fieldset className="wb-fieldset" disabled={mutation.isPending}>
            <Field label="密钥名称" name="name" required error={mutation.error}>
              <input name="name" required maxLength={80} data-initial-focus />
            </Field>
            <fieldset className="wb-fieldset">
              <legend>Scopes</legend>
              <div className="wb-check-grid">
                {scopes.map(([value, label]) => (
                  <label className="wb-check" key={value}>
                    <input
                      type="checkbox"
                      name="scopes"
                      value={value}
                      defaultChecked={value !== 'tools:call'}
                    />
                    <span>
                      {label}
                      <small className="wb-mono">{value}</small>
                    </span>
                  </label>
                ))}
              </div>
            </fieldset>
            <div className="wb-form-grid">
              <label className="wb-field">
                <span>有效期</span>
                <select
                  name="expiry"
                  defaultValue="90"
                  onChange={() => {
                    expiresAt.current = null;
                  }}
                >
                  <option value="30">30 天</option>
                  <option value="90">90 天</option>
                  <option value="365">365 天</option>
                  <option value="never">不自动到期</option>
                </select>
              </label>
              <label className="wb-field">
                <span>月度费用上限（{project?.currency}）</span>
                <input
                  name="limit"
                  inputMode="decimal"
                  pattern="[0-9]+(\.[0-9]{1,6})?"
                  placeholder="留空表示受项目额度限制"
                />
              </label>
              <label className="wb-field">
                <span>每分钟请求数</span>
                <input name="rate" type="number" required min="1" max="10000" defaultValue="60" />
              </label>
            </div>
          </fieldset>
          <MutationError error={mutation.error} />
          <div className="wb-form-actions">
            <button className="wb-button wb-button--primary" disabled={mutation.isPending}>
              {mutation.isPending ? '正在创建…' : '创建密钥'}
            </button>
            <button
              className="wb-button wb-button--secondary"
              type="button"
              disabled={mutation.isPending}
              onClick={onClose}
            >
              取消
            </button>
          </div>
        </form>
      )}
    </Modal>
  );
}
export function KeysPage() {
  const { project, projectId, refresh } = usePlatform();
  const { user } = useAuth();
  const owner = project?.role === 'owner' || user?.role === 'admin';
  const keyPage = usePage<Key>('/api-keys', {}, owner);
  const query = { ...keyPage, data: keyPage.data?.items };
  const [params, setParams] = useSearchParams();
  const [creating, setCreating] = useState(params.get('new') === '1');
  const [revoking, setRevoking] = useState<Key | null>(null);
  const revoke = useMutation({
    mutationFn: () => write(`/api-keys/${encodeURIComponent(revoking!.id)}/revoke`, {}),
    onSuccess: () => {
      setRevoking(null);
      refresh();
    },
    retry: false,
  });
  return (
    <>
      <PageHeader
        title="API 密钥"
        description="签发绑定当前项目、scope、期限、速率和费用上限的机器身份。"
        actions={
          owner && (
            <button className="wb-button wb-button--primary" onClick={() => setCreating(true)}>
              创建 API Key
            </button>
          )
        }
      />
      <RequireProject>
        {!owner ? (
          <EmptyState title="需要项目所有者权限" description="请联系项目所有者管理机器身份。" />
        ) : (
          <section className="wb-panel">
            <QueryState pending={query.isPending} error={query.error} retry={query.refetch}>
              {!query.data?.length ? (
                <EmptyState title="暂无 API 密钥" description="创建密钥以调用同一发布版本。" />
              ) : (
                <div className="wb-table-wrap">
                  <table className="wb-table">
                    <thead>
                      <tr>
                        <th>名称 / 前缀</th>
                        <th>项目</th>
                        <th>Scopes</th>
                        <th>状态</th>
                        <th>有效期</th>
                        <th>最近使用</th>
                        <th>月度上限</th>
                        <th>操作</th>
                      </tr>
                    </thead>
                    <tbody>
                      {query.data.map((item) => (
                        <tr key={item.id}>
                          <td>
                            <span className="wb-cell-stack">
                              <strong>{item.name}</strong>
                              <small className="wb-mono">{maskKey(item.prefix)}</small>
                            </span>
                          </td>
                          <td>{project?.name ?? item.projectId}</td>
                          <td>{item.scopes.join(' · ')}</td>
                          <td>
                            <StatusBadge
                              tone={
                                item.state === 'active'
                                  ? 'success'
                                  : item.state === 'revoked'
                                    ? 'neutral'
                                    : 'warning'
                              }
                            >
                              {
                                {
                                  active: '启用',
                                  revoked: '已撤销',
                                  expired: '已到期',
                                  expiring: '即将到期',
                                }[item.state]
                              }
                            </StatusBadge>
                          </td>
                          <td>{expiryLabel(item.expiresAt)}</td>
                          <td>{item.lastUsedAt ? timeAgo(item.lastUsedAt) : '从未使用'}</td>
                          <td>
                            {item.monthlyLimitMicros === null
                              ? '受项目额度限制'
                              : `${formatMoney(item.monthlyLimitMicros, item.currency)} ${item.currency}`}
                          </td>
                          <td>
                            <button
                              className="wb-button wb-button--danger"
                              disabled={item.status === 'revoked'}
                              onClick={() => setRevoking(item)}
                            >
                              撤销
                            </button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
              <Pagination
                nextCursor={keyPage.data?.nextCursor}
                page={keyPage.page}
                pending={keyPage.isFetching}
                onNext={keyPage.next}
                onPrevious={keyPage.previous}
              />
            </QueryState>
          </section>
        )}
      </RequireProject>
      {creating && owner && (
        <CreateKey
          key={projectId}
          onClose={() => {
            setCreating(false);
            params.delete('new');
            setParams(params, { replace: true });
          }}
        />
      )}
      <Modal
        open={!!revoking}
        onClose={() => {
          if (!revoke.isPending) setRevoking(null);
        }}
        title="撤销 API Key？"
        description={`${revoking?.name ?? ''}。撤销后无法恢复，新调用将被拒绝；已派发请求的副作用与费用仍需核对。`}
      >
        <MutationError error={revoke.error} />
        <div className="wb-form-actions">
          <button
            className="wb-button wb-button--danger"
            disabled={revoke.isPending}
            onClick={() => revoke.mutate()}
          >
            {revoke.isPending ? '正在撤销…' : '确认撤销'}
          </button>
          <button
            className="wb-button wb-button--secondary"
            disabled={revoke.isPending}
            onClick={() => setRevoking(null)}
          >
            保留密钥
          </button>
        </div>
      </Modal>
    </>
  );
}
