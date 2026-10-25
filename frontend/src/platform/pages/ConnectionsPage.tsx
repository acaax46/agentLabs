import { useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useAuth } from '../../auth/context';
import { connections, connectionLabels } from '../../connections/api';
import type { Connection, ConnectionInput } from '../../connections/api';
import { get } from '../api';
import type { Grant } from '../api';
import { Field, SecretInput } from '../controls';
import { usePlatform } from '../state';
import { Link } from 'react-router-dom';
import { formatDateTime, timeAgo } from '../model';
import {
  EmptyState,
  ErrorState,
  Icon,
  Modal,
  PageHeader,
  StatusBadge,
  type BadgeTone,
} from '../ui';

const defaultModels = [
  'minimax/minimax-m3.1-flash',
  'deepseek/deepseek-v4.1-flash-1017',
  'z-ai/glm-5.3-flash',
];

function ConnectionProjects({
  connection,
  projects,
}: {
  connection: Connection;
  projects: { id: string; name: string }[];
}) {
  const query = useQuery({
    queryKey: ['platform', 'connection-projects', connection.id],
    queryFn: ({ signal }) =>
      get<{ items: Grant[] }>(
        `/connections/${encodeURIComponent(connection.id)}/projects`,
        {},
        signal,
      ),
    enabled: connection.scope !== 'tenant',
    retry: false,
  });
  const items =
    connection.scope === 'tenant'
      ? projects.map((project) => ({ projectId: project.id, projectName: project.name }))
      : (query.data?.items ?? []);
  return (
    <span className="wb-chip-row">
      {items.map((item) => (
        <Link key={item.projectId} className="wb-text-link" to={`/projects/${item.projectId}`}>
          {item.projectName}
        </Link>
      ))}
      {!items.length && (
        <small>
          {query.isError ? '项目授权读取失败' : query.isFetching ? '读取项目授权…' : '暂无项目授权'}
        </small>
      )}
    </span>
  );
}

function statusTone(status: Connection['status']): BadgeTone {
  if (status === 'active') return 'success';
  if (status === 'pending') return 'warning';
  if (status === 'error') return 'danger';
  if (status === 'expired') return 'warning';
  return 'neutral';
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : '账户连接操作未完成。';
}

function NewConnection({
  onDone,
  refresh,
  onBusyChange,
}: {
  onDone: () => void;
  refresh: () => void;
  onBusyChange: (busy: boolean) => void;
}) {
  const { state } = useAuth();
  const form = useRef<HTMLFormElement>(null);
  const idempotency = useRef(crypto.randomUUID());
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
          .map((value) => value.trim())
          .filter(Boolean),
        apiKey: String(data.get('apiKey')),
        expiresAt: data.get('expiresAt')
          ? new Date(String(data.get('expiresAt'))).toISOString()
          : null,
      };
      element.querySelector<HTMLInputElement>('[name="apiKey"]')!.value = '';
      data.delete('apiKey');
      let created: Connection;
      try {
        created = await connections<Connection>('create', '', input, idempotency.current);
      } finally {
        input.apiKey = '';
      }
      refresh();
      return connections<Connection>('verify', created.id, { version: created.version });
    },
    retry: false,
    onMutate: () => onBusyChange(true),
    onSuccess: onDone,
    onSettled: () => {
      onBusyChange(false);
      refresh();
    },
  });

  return (
    <form
      ref={form}
      className="wb-form"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
    >
      <div className="wb-form-grid">
        <Field label="连接名称" name="name" required error={save.error}>
          <input
            data-initial-focus
            name="name"
            required
            maxLength={80}
            placeholder="例如：团队模型账户"
          />
        </Field>
        <Field
          label="供应商标识"
          name="provider"
          required
          error={save.error}
          help="需与模型价格版本中的供应商标识一致。"
        >
          <input
            name="provider"
            defaultValue="openai-compatible"
            required
            maxLength={80}
            aria-describedby="connection-provider-help"
          />
        </Field>
      </div>

      <Field
        label="模型服务地址"
        name="baseUrl"
        required
        error={save.error}
        help="公网目标要求 HTTPS；不会把自定义地址变成任意网络代理。"
      >
        <input name="baseUrl" type="url" required placeholder="https://模型服务域名/v1" />
      </Field>

      <Field
        label="API Key"
        name="apiKey"
        required
        error={save.error}
        help="密钥由执行域外加密保管，提交后清空，不进入浏览器存储。"
      >
        <SecretInput name="apiKey" required />
      </Field>

      <div className="wb-form-grid">
        <label className="wb-field">
          <span>使用范围</span>
          <select name="scope" defaultValue="private">
            <option value="private">仅我使用</option>
            {state.permissions.manageUsers && <option value="tenant">当前工作区共享</option>}
          </select>
          <small>
            管理员发布的工作区共享渠道按平台供模价格计费；私人渠道按 BYOK 由连接方承担模型费用。
          </small>
        </label>
        <label className="wb-field">
          <span>有效期（可选）</span>
          <input name="expiresAt" type="datetime-local" />
          <small>到期后必须重新验证或轮换。</small>
        </label>
      </div>

      <Field
        label="允许使用的模型"
        name="models"
        required
        error={save.error}
        help="每行一个模型标识。验证核对授权与列表，不产生推理请求。"
      >
        <textarea
          name="models"
          required
          defaultValue={defaultModels.join('\n')}
          rows={4}
          aria-describedby="connection-model-help"
        />
      </Field>

      <label className="wb-consent">
        <input type="checkbox" required />
        <span>
          我确认向所填模型服务发送任务对话与项目文件；私人 BYOK
          模型费用由我方承担，管理员共享渠道按已发布模型价格计费，平台执行预算限制。
        </span>
      </label>

      {save.error && (
        <p className="wb-form-error" role="alert">
          {errorMessage(save.error)}
          如果连接已创建，请关闭窗口后刷新列表核对状态，避免重复创建。
        </p>
      )}

      <div className="wb-form-actions">
        <button className="wb-button wb-button--primary" disabled={save.isPending}>
          {save.isPending ? '正在保存并验证…' : '保存并验证'}
        </button>
        <button
          className="wb-button wb-button--secondary"
          type="button"
          disabled={save.isPending}
          onClick={onDone}
        >
          取消
        </button>
      </div>
    </form>
  );
}

export function ConnectionsPage() {
  const { user, state } = useAuth();
  const { projects } = usePlatform();
  const cache = useQueryClient();
  const query = useQuery({
    queryKey: ['connections'],
    queryFn: () => connections<Connection[]>('list'),
    refetchInterval: 15_000,
  });
  const [adding, setAdding] = useState(false);
  const [creating, setCreating] = useState(false);
  const [change, setChange] = useState<{
    connection: Connection;
    action: 'rotate' | 'revoke';
  } | null>(null);
  const secret = useRef<HTMLInputElement>(null);

  const refresh = () => {
    void cache.invalidateQueries({ queryKey: ['connections'] });
    void cache.invalidateQueries({ queryKey: ['platform'] });
  };

  const verify = useMutation({
    mutationFn: (connection: Connection) =>
      connections<Connection>('verify', connection.id, { version: connection.version }),
    onSettled: refresh,
    retry: false,
  });

  const manage = useMutation({
    mutationFn: async () => {
      const target = change!;
      const input = {
        version: target.connection.version,
        ...(target.action === 'rotate' ? { apiKey: secret.current!.value } : {}),
      };
      if (secret.current) secret.current.value = '';
      return connections<Connection>(target.action, target.connection.id, input);
    },
    onSuccess: () => setChange(null),
    onSettled: refresh,
    retry: false,
  });

  const busy = verify.isPending || manage.isPending;

  return (
    <>
      <PageHeader
        eyebrow="ACCESS / CONNECTIONS"
        title="连接"
        description="连接模型或工具账户，密钥由执行域外保管；项目级授权、轮换与撤销需要独立权限校验。"
        actions={
          <button className="wb-button wb-button--primary" onClick={() => setAdding(true)}>
            <Icon name="plus" size={16} />
            连接模型账户
          </button>
        }
      />

      <div className="wb-callout wb-callout--info">
        <Icon name="shield" size={18} />
        <div>
          <strong>此页使用现有连接服务</strong>
          <p>
            连接列表、验证与项目授权来自后端。工作区共享连接自动可用于可访问项目；个人连接需单独授予项目。秘密不回显。
          </p>
        </div>
      </div>

      {verify.error && (
        <p className="wb-form-error" role="alert">
          {errorMessage(verify.error)}
        </p>
      )}

      <section className="wb-panel">
        {query.isPending ? (
          <div className="wb-loading-block" role="status">
            <span className="wb-spinner" />
            正在读取账户连接…
          </div>
        ) : query.isError ? (
          <ErrorState
            title="无法读取账户连接"
            description={errorMessage(query.error)}
            action={
              <button
                className="wb-button wb-button--secondary wb-button--sm"
                onClick={() => void query.refetch()}
              >
                重新加载
              </button>
            }
          />
        ) : query.data.length === 0 ? (
          <EmptyState
            icon="connections"
            title="还没有模型账户连接"
            description="连接并验证账户后，才能在 Agent 中选择模型来源。"
            action={
              <button className="wb-button wb-button--primary" onClick={() => setAdding(true)}>
                连接模型账户
              </button>
            }
          />
        ) : (
          <div className="wb-table-wrap">
            <table className="wb-table">
              <thead>
                <tr>
                  <th scope="col">连接</th>
                  <th scope="col">范围</th>
                  <th scope="col">模型来源 / 可用项目</th>
                  <th scope="col">允许模型</th>
                  <th scope="col">状态</th>
                  <th scope="col">最近验证</th>
                  <th scope="col">有效期</th>
                  <th scope="col" className="wb-right">
                    操作
                  </th>
                </tr>
              </thead>
              <tbody>
                {query.data.map((connection) => {
                  const editable =
                    connection.scope === 'tenant'
                      ? state.permissions.manageUsers
                      : connection.ownerId === user!.id;
                  const closed = connection.status === 'revoked';
                  return (
                    <tr key={connection.id}>
                      <td>
                        <span className="wb-cell-stack">
                          <strong>{connection.name}</strong>
                          <small className="wb-mono">{connection.baseUrl}</small>
                          <small>{connection.provider}</small>
                        </span>
                      </td>
                      <td>
                        <StatusBadge
                          tone={connection.scope === 'tenant' ? 'accent' : 'neutral'}
                          dot={false}
                        >
                          {connection.scope === 'tenant' ? '工作区共享' : '仅我使用'}
                        </StatusBadge>
                      </td>
                      <td>
                        <span className="wb-cell-stack">
                          <strong>
                            {connection.ownerId === 'system' || connection.scope === 'tenant'
                              ? '平台供模'
                              : 'BYOK'}
                          </strong>
                          <ConnectionProjects connection={connection} projects={projects} />
                        </span>
                      </td>
                      <td>
                        <span className="wb-chip-row">
                          {connection.models.slice(0, 3).map((model) => (
                            <span className="wb-chip wb-mono" key={model}>
                              {model}
                            </span>
                          ))}
                          {connection.models.length > 3 && (
                            <span className="wb-chip">+{connection.models.length - 3}</span>
                          )}
                        </span>
                      </td>
                      <td>
                        <span className="wb-status-stack">
                          <StatusBadge tone={statusTone(connection.status)}>
                            {connectionLabels[connection.status]}
                          </StatusBadge>
                          <small className="wb-muted">第 {connection.generation} 代</small>
                        </span>
                      </td>
                      <td>
                        <time
                          dateTime={connection.verifiedAt ?? undefined}
                          title={formatDateTime(connection.verifiedAt)}
                        >
                          {timeAgo(connection.verifiedAt)}
                        </time>
                      </td>
                      <td>
                        {connection.expiresAt ? formatDateTime(connection.expiresAt) : '未设置'}
                      </td>
                      <td className="wb-right">
                        {editable && !closed ? (
                          <div className="wb-row-actions">
                            <button
                              className="wb-button wb-button--ghost wb-button--sm"
                              disabled={busy}
                              onClick={() => verify.mutate(connection)}
                            >
                              {verify.isPending && verify.variables?.id === connection.id
                                ? '验证中…'
                                : '验证'}
                            </button>
                            <button
                              className="wb-button wb-button--ghost wb-button--sm"
                              disabled={busy}
                              onClick={() => {
                                manage.reset();
                                setChange({ connection, action: 'rotate' });
                              }}
                            >
                              轮换
                            </button>
                            <button
                              className="wb-button wb-button--ghost wb-button--sm wb-button--danger-text"
                              disabled={busy}
                              onClick={() => {
                                manage.reset();
                                setChange({ connection, action: 'revoke' });
                              }}
                            >
                              撤销
                            </button>
                          </div>
                        ) : (
                          <span className="wb-muted">{closed ? '已关闭' : '由管理员管理'}</span>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section className="wb-panel wb-connection-facts">
        <div>
          <h2>连接验证到什么程度</h2>
          <p>保存配置不等于验证成功；模型列表通过也不等于所有模型完成推理测试。</p>
        </div>
        <ul className="wb-check-lines">
          <li>已连接只代表声明的验证通过，不代表上游无限可用。</li>
          <li>轮换后旧密钥立即停止使用，新密钥验证后才能发送新任务。</li>
          <li>撤销优先阻止尚未派发的操作；已发送请求仍可能产生费用。</li>
          <li>云端 KMS、OAuth 与跨租户托管尚未实现。</li>
        </ul>
      </section>

      <Modal
        open={adding}
        onClose={() => {
          if (!creating) setAdding(false);
        }}
        title="连接模型账户"
        description="使用现有连接服务保存并验证账户。提交后页面不会再次显示密钥。"
        size="lg"
      >
        {adding && (
          <NewConnection
            onDone={() => setAdding(false)}
            refresh={refresh}
            onBusyChange={setCreating}
          />
        )}
      </Modal>

      <Modal
        open={Boolean(change)}
        onClose={() => {
          if (!manage.isPending) setChange(null);
        }}
        title={change?.action === 'rotate' ? '轮换账户密钥' : '撤销账户连接'}
        description={
          change ? `${change.connection.name} · ${change.connection.baseUrl}` : undefined
        }
        size="sm"
      >
        {change && (
          <form
            className="wb-form"
            onSubmit={(event) => {
              event.preventDefault();
              manage.mutate();
            }}
          >
            <div
              className={`wb-callout ${change.action === 'revoke' ? 'wb-callout--danger' : 'wb-callout--warning'}`}
            >
              <Icon name="warning" size={18} />
              <div>
                <strong>
                  {change.action === 'rotate'
                    ? '旧密钥将立即停止使用'
                    : '撤销不会自动归零已发生费用'}
                </strong>
                <p>
                  {change.action === 'rotate'
                    ? '新密钥通过验证后才能发送新任务；排队中的任务需要重新核对。'
                    : '删除保管的密钥并阻止新派发。已发送请求仍可能产生供应商费用，未知用量保留待核对。'}
                </p>
              </div>
            </div>
            {change.action === 'rotate' && (
              <Field label="新的 API Key" name="apiKey" required error={manage.error}>
                <SecretInput name="apiKey" required inputRef={secret} />
              </Field>
            )}
            {manage.error && (
              <p className="wb-form-error" role="alert">
                {errorMessage(manage.error)}
              </p>
            )}
            <div className="wb-form-actions">
              <button
                className={`wb-button ${change.action === 'revoke' ? 'wb-button--danger' : 'wb-button--primary'}`}
                disabled={manage.isPending}
              >
                {manage.isPending
                  ? '正在处理…'
                  : change.action === 'rotate'
                    ? '轮换密钥'
                    : '确认撤销'}
              </button>
              <button
                className="wb-button wb-button--secondary"
                type="button"
                disabled={manage.isPending}
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
