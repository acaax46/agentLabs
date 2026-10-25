import { useRef, useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { connections } from '../../connections/api';
import type { Connection } from '../../connections/api';
import { useAuth } from '../../auth/context';
import { get, write, requestKey, platformPath, platformRequest } from '../api';
import type { Project, Grant, ModelOption } from '../api';
import { usePlatform } from '../state';
import { decimalToMicros, formatMoney, formatDateTime } from '../model';
import { MutationError, QueryState, usePage } from '../shared';
import { BudgetBar, Field, Pagination } from '../controls';
import { useFeedback } from '../feedback';
import { EmptyState, Modal, PageHeader, SectionHeader, StatusBadge, KeyValue } from '../ui';

export function ProjectsPage() {
  const platform = usePlatform();
  const auth = useAuth();
  const listing = usePage<Project>('/projects', {
    all: auth.user?.role === 'admin' ? 'true' : undefined,
  });
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const [creating, setCreating] = useState(params.get('new') === '1');
  const [search, setSearch] = useState('');
  const key = useRef(requestKey());
  const create = useMutation({
    mutationFn: async (form: FormData) => {
      const amount = String(form.get('budget')).trim();
      const input = {
        name: String(form.get('name')).trim(),
        description: String(form.get('description')).trim(),
        environment: String(form.get('environment')),
        currency: String(form.get('currency')),
        budgetMicros: amount ? decimalToMicros(amount) : null,
      };
      const project = await write<Project>('/projects', input, key.current(input));
      platform.selectProject(project.id);
      platform.refresh();
      setCreating(false);
      navigate(`/projects/${project.id}`);
    },
    retry: false,
  });
  const projects = (listing.data?.items ?? []).filter((project) =>
    `${project.name} ${project.slug} ${project.description}`
      .toLowerCase()
      .includes(search.toLowerCase()),
  );
  const close = () => {
    if (create.isPending) return;
    setCreating(false);
    create.reset();
    params.delete('new');
    setParams(params, { replace: true });
  };
  return (
    <>
      <PageHeader
        title="项目"
        description="管理项目边界、预算和可用模型连接。"
        actions={
          <button className="wb-button wb-button--primary" onClick={() => setCreating(true)}>
            创建项目
          </button>
        }
      />
      <QueryState pending={listing.isPending} error={listing.error} retry={listing.refetch}>
        <section className="wb-panel">
          <div className="wb-toolbar">
            <label className="wb-search">
              <input
                aria-label="搜索项目"
                placeholder="搜索本页名称、slug 或用途"
                value={search}
                onChange={(event) => setSearch(event.target.value)}
              />
            </label>
          </div>
          {!projects.length ? (
            <EmptyState title="暂无符合条件的项目" description="创建项目后配置预算与模型连接。" />
          ) : (
            <div className="wb-table-wrap">
              <table className="wb-table">
                <thead>
                  <tr>
                    <th>项目</th>
                    <th>环境</th>
                    <th>状态</th>
                    <th>已结算 / 预算</th>
                    <th>预占</th>
                    <th>权限</th>
                  </tr>
                </thead>
                <tbody>
                  {projects.map((project) => (
                    <tr key={project.id}>
                      <td>
                        <span className="wb-cell-stack">
                          <Link className="wb-table-link" to={`/projects/${project.id}`}>
                            {project.name}
                          </Link>
                          <small>{project.slug}</small>
                        </span>
                      </td>
                      <td>{project.environment === 'production' ? '生产' : '预发布'}</td>
                      <td>
                        <StatusBadge tone={project.status === 'active' ? 'success' : 'warning'}>
                          {project.status === 'active' ? '正常' : '已暂停'}
                        </StatusBadge>
                      </td>
                      <td className="wb-mono">
                        {project.budget.configured
                          ? `${formatMoney(project.budget.spentMicros, project.currency)} / ${formatMoney(project.budget.limitMicros, project.currency)} ${project.currency}`
                          : '预算未配置'}
                      </td>
                      <td className="wb-mono">
                        {formatMoney(
                          project.budget.configured ? project.budget.heldMicros : null,
                          project.currency,
                        )}{' '}
                        {project.currency}
                      </td>
                      <td>{project.role === 'owner' ? '所有者' : '成员'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <Pagination
            nextCursor={listing.data?.nextCursor}
            page={listing.page}
            pending={listing.isFetching}
            onNext={listing.next}
            onPrevious={listing.previous}
          />
        </section>
      </QueryState>
      <Modal
        open={creating}
        onClose={close}
        title="创建项目"
        description="预算是执行额度，不是充值余额。未配置预算时不能提交运行。"
      >
        <form
          className="wb-form"
          onSubmit={(event) => {
            event.preventDefault();
            create.mutate(new FormData(event.currentTarget));
          }}
        >
          <Field label="项目名称" name="name" required error={create.error}>
            <input name="name" required maxLength={80} data-initial-focus />
          </Field>
          <Field label="用途说明" name="description" error={create.error}>
            <textarea name="description" rows={3} maxLength={500} />
          </Field>
          <div className="wb-form-grid">
            <label className="wb-field">
              <span>环境</span>
              <select name="environment">
                <option value="production">生产</option>
                <option value="staging">预发布</option>
              </select>
            </label>
            <label className="wb-field">
              <span>币种</span>
              <select name="currency">
                <option>CNY</option>
                <option>USD</option>
              </select>
            </label>
            <label className="wb-field">
              <span>项目累计预算</span>
              <input
                name="budget"
                inputMode="decimal"
                pattern="[0-9]+(\.[0-9]{1,6})?"
                defaultValue="500"
                placeholder="留空表示未配置"
              />
            </label>
          </div>
          <MutationError error={create.error} />
          <div className="wb-form-actions">
            <button className="wb-button wb-button--primary" disabled={create.isPending}>
              {create.isPending ? '正在创建…' : '创建项目'}
            </button>
            <button
              type="button"
              className="wb-button wb-button--secondary"
              disabled={create.isPending}
              onClick={close}
            >
              取消
            </button>
          </div>
        </form>
      </Modal>
    </>
  );
}

export function ProjectDetailPage() {
  const { id = '' } = useParams();
  const platform = usePlatform();
  const auth = useAuth();
  const notify = useFeedback();
  const query = useQuery({
    queryKey: ['platform', 'project', id],
    queryFn: ({ signal }) => get<Project>(`/projects/${encodeURIComponent(id)}`, {}, signal),
    retry: false,
  });
  const project = query.data;
  const models = useQuery({
    queryKey: ['platform', 'models', id],
    queryFn: ({ signal }) => get<{ items: ModelOption[] }>('/models', { projectId: id }, signal),
    enabled: !!project,
    retry: false,
  });
  const owner = project?.role === 'owner' || auth.user?.role === 'admin';
  const grants = useQuery({
    queryKey: ['platform', 'grants', id],
    queryFn: ({ signal }) =>
      get<{ items: Grant[] }>(`/projects/${encodeURIComponent(id)}/connections`, {}, signal),
    enabled: !!project,
    retry: false,
  });
  const available = useQuery({
    queryKey: ['connections'],
    queryFn: () => connections<Connection[]>('list'),
    enabled: !!owner,
    retry: false,
  });
  const [connectionId, setConnectionId] = useState('');
  const update = useMutation({
    mutationFn: (status: string) =>
      write<Project>(`/projects/${id}`, { status, version: project!.version }, undefined, 'PATCH'),
    onSuccess: () => {
      platform.refresh();
    },
    retry: false,
  });
  const budget = useMutation({
    mutationFn: (form: FormData) =>
      write(
        `/projects/${id}/budget`,
        {
          limitMicros: decimalToMicros(String(form.get('limit'))),
          maxConcurrentRuns: Number(form.get('concurrency')),
          version: project!.budget.version,
        },
        undefined,
        'PUT',
      ),
    onSuccess: () => {
      platform.refresh();
      notify('项目预算已保存');
    },
    retry: false,
  });
  const grant = useMutation({
    mutationFn: ({ connectionId, revoke }: { connectionId: string; revoke: boolean }) =>
      platformRequest(
        platformPath(
          `/projects/${encodeURIComponent(id)}/connections/${encodeURIComponent(connectionId)}`,
        ),
        { method: revoke ? 'DELETE' : 'PUT' },
      ),
    onSuccess: platform.refresh,
    retry: false,
  });
  return (
    <QueryState pending={query.isPending} error={query.error} retry={query.refetch}>
      {project && (
        <>
          <PageHeader
            title={project.name}
            description={project.description}
            meta={
              <StatusBadge tone={project.status === 'active' ? 'success' : 'warning'}>
                {project.status === 'active' ? '正常' : '已暂停'}
              </StatusBadge>
            }
            actions={
              <>
                <button
                  className="wb-button wb-button--primary"
                  onClick={() => {
                    platform.selectProject(project.id);
                  }}
                >
                  设为当前项目
                </button>
                {owner && (
                  <button
                    className="wb-button wb-button--secondary"
                    disabled={update.isPending}
                    onClick={() => update.mutate(project.status === 'active' ? 'paused' : 'active')}
                  >
                    {project.status === 'active' ? '暂停新运行' : '恢复项目'}
                  </button>
                )}
              </>
            }
          />
          <MutationError error={update.error} />
          <div className="wb-detail-grid">
            <div className="wb-detail-main">
              <section className="wb-panel">
                <SectionHeader
                  title="项目预算"
                  description="项目预算按累计消耗控制，不按月重置。额度须覆盖已结算与预占；API Key 的月度额度另行设置。"
                />
                <BudgetBar budget={project.budget} />
                <KeyValue
                  items={[
                    {
                      label: '已结算',
                      value: `${formatMoney(project.budget.configured ? project.budget.spentMicros : null, project.currency)} ${project.currency}`,
                    },
                    {
                      label: '预占',
                      value: `${formatMoney(project.budget.configured ? project.budget.heldMicros : null, project.currency)} ${project.currency}`,
                    },
                    {
                      label: '可用预算',
                      value: `${formatMoney(project.budget.configured ? project.budget.availableMicros : null, project.currency)} ${project.currency}`,
                    },
                    {
                      label: '并发运行',
                      value: project.budget.configured
                        ? `${project.budget.activeRuns} / ${project.budget.maxConcurrentRuns}`
                        : '未配置',
                    },
                  ]}
                />
                {owner && (
                  <form
                    className="wb-form"
                    key={project.budget.version}
                    onSubmit={(event) => {
                      event.preventDefault();
                      budget.mutate(new FormData(event.currentTarget));
                    }}
                  >
                    <label className="wb-field">
                      <span>项目累计预算（{project.currency}）</span>
                      <input
                        name="limit"
                        required
                        inputMode="decimal"
                        pattern="[0-9]+(\.[0-9]{1,6})?"
                        placeholder="输入预算额度"
                      />
                    </label>
                    <label className="wb-field">
                      <span>最大并发</span>
                      <input
                        name="concurrency"
                        type="number"
                        min="1"
                        max="100"
                        required
                        defaultValue={project.budget.maxConcurrentRuns || 2}
                      />
                    </label>
                    <MutationError error={budget.error} />
                    <button className="wb-button wb-button--primary" disabled={budget.isPending}>
                      保存预算
                    </button>
                  </form>
                )}
              </section>
              <section className="wb-panel">
                <SectionHeader title="Agent 与 API 密钥" />
                <p>已发布 Agent：{project.counts?.agents ?? '数量未知'}</p>
                <p className="wb-field-help">Agent 属于工作区目录，数量由服务端返回。</p>
                <p>API 密钥：{project.counts?.apiKeys ?? '数量未知'}</p>
                <Link className="wb-text-link" to={`/keys?project=${id}`}>
                  管理项目密钥
                </Link>
              </section>
              <section className="wb-panel">
                <SectionHeader
                  title="可用连接"
                  description="共享连接自动对项目可用；个人连接需授予项目。撤销仅阻止后续授权，已派发请求仍需核对。"
                />
                <QueryState pending={models.isPending} error={models.error} retry={models.refetch}>
                  <ul className="wb-compact-list">
                    {Array.from(
                      new Map(
                        models.data?.items.map((model) => [model.connectionId, model]),
                      ).values(),
                    ).map((model) => (
                      <li key={model.connectionId}>
                        <strong>{model.connectionName}</strong>
                        <span>
                          {model.source === 'platform' ? '平台供模' : 'BYOK'} · {model.status} ·{' '}
                          {model.reason || '已授权'}
                        </span>
                      </li>
                    ))}
                  </ul>
                </QueryState>
                <QueryState pending={grants.isPending} error={grants.error} retry={grants.refetch}>
                  <ul className="wb-compact-list">
                    {grants.data?.items.map((item) => (
                      <li key={item.connectionId}>
                        <div>
                          <strong>{item.connectionName}</strong>
                          <small>
                            {item.models.join(' · ')} · {item.status}
                          </small>
                        </div>
                        {owner && (
                          <button
                            className="wb-button wb-button--danger"
                            disabled={grant.isPending}
                            onClick={() =>
                              grant.mutate({ connectionId: item.connectionId, revoke: true })
                            }
                          >
                            撤销项目授权
                          </button>
                        )}
                      </li>
                    ))}
                  </ul>
                  {!grants.data?.items.length && <p className="wb-muted">尚未授权连接。</p>}
                </QueryState>
                {owner && (
                  <>
                    <QueryState
                      pending={available.isPending}
                      error={available.error}
                      retry={available.refetch}
                    >
                      <label className="wb-field">
                        <span>添加连接授权</span>
                        <select
                          value={connectionId}
                          onChange={(event) => setConnectionId(event.target.value)}
                        >
                          <option value="">选择账户连接</option>
                          {available.data
                            ?.filter(
                              (item) =>
                                item.scope === 'private' &&
                                item.ownerId === auth.user?.id &&
                                item.status !== 'revoked' &&
                                !grants.data?.items.some((value) => value.connectionId === item.id),
                            )
                            .map((item) => (
                              <option key={item.id} value={item.id}>
                                {item.name} · {item.status}
                              </option>
                            ))}
                        </select>
                      </label>
                      <button
                        className="wb-button wb-button--primary"
                        disabled={!connectionId || grant.isPending}
                        onClick={() => grant.mutate({ connectionId, revoke: false })}
                      >
                        授权连接
                      </button>
                    </QueryState>
                    <MutationError error={grant.error} />
                  </>
                )}
                <Link className="wb-text-link" to="/connections">
                  管理账户连接
                </Link>
              </section>
            </div>
            <aside className="wb-detail-side">
              <section className="wb-panel">
                <SectionHeader title="项目边界" />
                <KeyValue
                  items={[
                    { label: '项目 ID', value: project.id },
                    { label: 'Slug', value: project.slug },
                    { label: '保留期', value: `${project.retentionDays} 天` },
                    { label: '创建时间', value: formatDateTime(project.createdAt) },
                  ]}
                />
                <Link className="wb-text-link" to={`/keys?project=${id}`}>
                  管理 API 密钥
                </Link>
              </section>
            </aside>
          </div>
        </>
      )}
    </QueryState>
  );
}
