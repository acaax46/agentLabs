import { useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useAuth } from '../auth/context';
import {
  EmptyState,
  ErrorPanel,
  FormError,
  Icon,
  Loading,
  Modal,
  Notice,
  PageHeading,
} from '../catalog/shared';
import { connections, connectionLabels } from './api';
import type { Connection, ConnectionInput } from './api';

const models = [
  'minimax/minimax-m3.1-flash',
  'deepseek/deepseek-v4.1-flash-1017',
  'z-ai/glm-5.3-flash',
];
function NewConnection({
  done,
  refresh,
  lock,
}: {
  done: () => void;
  refresh: () => void;
  lock: (value: boolean) => void;
}) {
  const { state } = useAuth();
  const form = useRef<HTMLFormElement>(null);
  const key = useRef(crypto.randomUUID());
  const save = useMutation({
    mutationFn: async () => {
      const element = form.current!;
      const data = new FormData(element);
      const input: ConnectionInput = {
        name: String(data.get('name')),
        provider: String(data.get('provider')),
        baseUrl: String(data.get('baseUrl')),
        scope: data.get('scope') === 'tenant' ? 'tenant' : 'private',
        models: String(data.get('models'))
          .split(/[,\n]/)
          .map((s) => s.trim())
          .filter(Boolean),
        apiKey: String(data.get('apiKey')),
        expiresAt: data.get('expiresAt')
          ? new Date(String(data.get('expiresAt'))).toISOString()
          : null,
      };
      // Secrets never enter React state, query data or browser storage.
      element.querySelector<HTMLInputElement>('[name="apiKey"]')!.value = '';
      data.delete('apiKey');
      let created: Connection;
      try {
        created = await connections<Connection>('create', '', input, key.current);
      } finally {
        input.apiKey = '';
      }
      refresh();
      return connections<Connection>('verify', created.id, { version: created.version });
    },
    retry: false,
    onMutate: () => lock(true),
    onSuccess: done,
    onSettled: () => {
      lock(false);
      refresh();
    },
  });
  return (
    <form
      ref={form}
      className="connection-form"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <label className="field">
        连接名称
        <input
          data-initial-focus
          name="name"
          required
          maxLength={80}
          placeholder="例如：课堂模型账户"
        />
      </label>
      <label className="field">
        供应商标识
        <input
          name="provider"
          defaultValue="openai-compatible"
          required
          maxLength={80}
          aria-describedby="connection-provider-help"
        />
      </label>
      <p id="connection-provider-help" className="field-help">
        与管理员在模型价格版本中配置的供应商标识一致。
      </p>
      <label className="field">
        模型服务地址
        <input name="baseUrl" type="url" required placeholder="https://模型服务域名/v1" />
      </label>
      <label className="field">
        API Key
        <input
          name="apiKey"
          type="password"
          required
          minLength={4}
          maxLength={8192}
          autoComplete="off"
          spellCheck={false}
          aria-describedby="connection-secret-help"
        />
      </label>
      <p id="connection-secret-help" className="field-help">
        提交后不再显示密钥。密钥由应用服务加密保管，不进入学生代码沙箱。
      </p>
      <label className="field">
        使用范围
        <select name="scope" defaultValue="private" aria-label="使用范围">
          <option value="private">仅我使用</option>
          {state.permissions.manageUsers && <option value="tenant">机构共享</option>}
        </select>
      </label>
      <label className="field">
        允许使用的模型
        <textarea
          aria-label="允许使用的模型"
          name="models"
          required
          defaultValue={models.join('\n')}
          rows={4}
          aria-describedby="connection-model-help"
        />
      </label>
      <p id="connection-model-help" className="field-help">
        每行一个模型标识。验证会核对授权和模型列表，不产生推理请求。
      </p>
      <label className="field">
        有效期（可选）
        <input name="expiresAt" type="datetime-local" />
      </label>
      <label className="connection-consent">
        <input type="checkbox" required />
        我同意向所填模型服务发送任务对话与项目代码；供应商收取模型费用，平台继续执行预算限制。
      </label>
      <FormError error={save.error} />
      <div className="button-row">
        <button className="button" disabled={save.isPending}>
          {save.isPending ? '正在保存并验证…' : '保存并验证'}
        </button>
        <button className="button secondary" type="button" disabled={save.isPending} onClick={done}>
          取消
        </button>
      </div>
    </form>
  );
}
export function ConnectionsPage() {
  const { user, state } = useAuth();
  const cache = useQueryClient();
  const query = useQuery({
    queryKey: ['connections'],
    queryFn: () => connections<Connection[]>('list'),
    refetchInterval: 15000,
  });
  const [adding, setAdding] = useState(false);
  const [creating, setCreating] = useState(false);
  const [change, setChange] = useState<{
    connection: Connection;
    action: 'rotate' | 'revoke';
  } | null>(null);
  const secretInput = useRef<HTMLInputElement>(null);
  const refresh = () => {
    void cache.invalidateQueries({ queryKey: ['connections'] });
    void cache.invalidateQueries({ queryKey: ['bootstrap'] });
    void cache.invalidateQueries({ queryKey: ['learning', 'capabilities'] });
  };
  const verify = useMutation({
    mutationFn: (c: Connection) => connections<Connection>('verify', c.id, { version: c.version }),
    onSettled: refresh,
    retry: false,
  });
  const mutation = useMutation({
    mutationFn: async () => {
      const target = change!;
      const input = {
        version: target.connection.version,
        ...(target.action === 'rotate' ? { apiKey: secretInput.current!.value } : {}),
      };
      if (secretInput.current) secretInput.current.value = '';
      return connections<Connection>(target.action, target.connection.id, input);
    },
    onSuccess: () => setChange(null),
    onSettled: refresh,
    retry: false,
  });
  const busy = verify.isPending || mutation.isPending;
  return (
    <>
      <PageHeading
        eyebrow="账户与工具 / 模型账户"
        title="账户连接"
        description="连接自己的模型账户，或使用机构授权的共享账户。"
        action={
          <button className="button" onClick={() => setAdding(true)}>
            连接模型账户
          </button>
        }
      />
      <Notice>
        <strong>凭据由应用服务保管</strong>
        <p>
          密钥加密保存，页面只展示连接信息。编程助手会向所选模型服务发送对话与当前项目代码；调用仍需通过预算检查。
        </p>
      </Notice>
      <FormError error={verify.error} />
      {query.isPending ? (
        <Loading text="正在读取账户连接…" />
      ) : query.isError ? (
        <ErrorPanel error={query.error} retry={() => void query.refetch()} />
      ) : query.data.length === 0 ? (
        <EmptyState
          title="还没有模型账户连接"
          description="连接并验证账户后，就可以在代码空间选择它来使用编程助手。"
          icon="vault"
        />
      ) : (
        <section className="connection-grid" aria-label="模型账户连接">
          {query.data.map((c) => {
            const manage =
              c.scope === 'tenant' ? state.permissions.manageUsers : c.ownerId === user!.id;
            const closed = c.status === 'revoked';
            return (
              <article className="connection-card" key={c.id}>
                <div className="connection-card-heading">
                  <Icon name="vault" />
                  <h2>{c.name}</h2>
                  <span className={`status-badge ${c.status === 'active' ? 'active' : 'draft'}`}>
                    {connectionLabels[c.status]}
                  </span>
                </div>
                <p className="field-help">
                  {c.scope === 'tenant' ? '机构共享' : '仅我使用'} · {c.provider}
                </p>
                <dl>
                  <div>
                    <dt>模型服务</dt>
                    <dd>{c.baseUrl}</dd>
                  </div>
                  <div>
                    <dt>允许模型</dt>
                    <dd>{c.models.join('、')}</dd>
                  </div>
                  <div>
                    <dt>有效期</dt>
                    <dd>
                      {c.expiresAt
                        ? new Date(c.expiresAt).toLocaleString('zh-CN')
                        : '未设置到期时间'}
                    </dd>
                  </div>
                  <div>
                    <dt>最近验证</dt>
                    <dd>
                      {c.verifiedAt
                        ? new Date(c.verifiedAt).toLocaleString('zh-CN')
                        : '尚未通过验证'}
                    </dd>
                  </div>
                </dl>
                <p role="status">{c.message}</p>
                {manage && !closed && (
                  <div className="button-row">
                    <button
                      className="button secondary"
                      disabled={busy}
                      onClick={() => verify.mutate(c)}
                    >
                      {verify.isPending && verify.variables?.id === c.id ? '正在验证…' : '验证连接'}
                    </button>
                    <button
                      className="button secondary"
                      disabled={busy}
                      onClick={() => {
                        mutation.reset();
                        setChange({ connection: c, action: 'rotate' });
                      }}
                    >
                      更换密钥
                    </button>
                    <button
                      className="text-button danger-text"
                      disabled={busy}
                      onClick={() => {
                        mutation.reset();
                        setChange({ connection: c, action: 'revoke' });
                      }}
                    >
                      撤销连接
                    </button>
                  </div>
                )}
                {!manage && <p className="field-help">此连接由机构管理员管理。</p>}
              </article>
            );
          })}
        </section>
      )}
      <Modal open={adding} title="连接模型账户" busy={creating} onClose={() => setAdding(false)}>
        {adding && (
          <NewConnection done={() => setAdding(false)} refresh={refresh} lock={setCreating} />
        )}
      </Modal>
      <Modal
        open={!!change}
        title={change?.action === 'rotate' ? '更换账户密钥' : '撤销账户连接'}
        busy={mutation.isPending}
        onClose={() => setChange(null)}
      >
        {change && (
          <form
            className="connection-form"
            onSubmit={(e) => {
              e.preventDefault();
              mutation.mutate();
            }}
          >
            <p>
              <strong>{change.connection.name}</strong>
              <br />
              {change.connection.baseUrl}
            </p>
            <Notice tone="warning">
              {change.action === 'rotate'
                ? '旧密钥立即停止使用，新密钥验证后才能发送新任务。'
                : '撤销后删除保管的密钥，相关排队任务将取消，正在发送的请求会中止。'}{' '}
              已发送请求仍可能产生供应商费用；未知用量保留待核对。
            </Notice>
            {change.action === 'rotate' && (
              <label className="field">
                新的 API Key
                <input
                  ref={secretInput}
                  data-initial-focus
                  type="password"
                  required
                  minLength={4}
                  maxLength={8192}
                  autoComplete="off"
                  spellCheck={false}
                />
              </label>
            )}
            <FormError error={mutation.error} />
            <div className="button-row">
              <button
                className={`button ${change.action === 'revoke' ? 'danger' : ''}`}
                disabled={mutation.isPending}
              >
                {mutation.isPending
                  ? '正在处理…'
                  : change.action === 'rotate'
                    ? '更换密钥'
                    : '确认撤销'}
              </button>
              <button
                className="button secondary"
                type="button"
                disabled={mutation.isPending}
                onClick={() => setChange(null)}
              >
                取消
              </button>
            </div>
          </form>
        )}
      </Modal>
    </>
  );
}
