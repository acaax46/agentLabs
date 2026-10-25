import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { APIError, deleteRecord, getRecord, listRecords, saveRecord } from '../api/client';
import type { CatalogInput, CatalogKind, CatalogRecord, ListQuery } from '../api/client';
import { brief, formatDate, moduleMap, statuses, statusLabels } from './modules';
import type { Module } from './modules';
import { EditorField, ReadonlyField } from './fields';
import { PublishingPanel } from '../learning/publishing';
import { Forbidden } from '../auth/pages';
import {
  EmptyState,
  ErrorPanel,
  FormError,
  Icon,
  Loading,
  Modal,
  Notice,
  PageHeading,
  Status,
} from './shared';

function DeleteDialog({
  record,
  module,
  onClose,
  onDeleted,
}: {
  record: CatalogRecord | null;
  module: Module;
  onClose: () => void;
  onDeleted?: () => void;
}) {
  const cache = useQueryClient();
  const key = useRef(crypto.randomUUID());
  useEffect(() => {
    key.current = crypto.randomUUID();
  }, [record?.id]);
  const mutation = useMutation({
    mutationFn: () => deleteRecord(record!, key.current),
    onSuccess: async () => {
      await cache.invalidateQueries({ queryKey: ['catalog'] });
      onClose();
      onDeleted?.();
    },
    onError: () => {
      void cache.invalidateQueries({ queryKey: ['catalog'] });
    },
  });
  useEffect(() => {
    if (!record) mutation.reset();
  }, [record]);
  return (
    <Modal
      open={!!record}
      title={`删除${module.singular}`}
      onClose={onClose}
      busy={mutation.isPending}
    >
      <p className="dialog-description">
        确定删除「{record?.name}」吗？此操作不能撤销。如果它仍被其他资料引用，将保留记录并提示原因。
      </p>
      <div className="delete-record-code">{record?.code}</div>
      <FormError error={mutation.error} />
      <div className="dialog-actions">
        <button
          className="button secondary"
          data-initial-focus
          onClick={onClose}
          disabled={mutation.isPending}
        >
          取消
        </button>
        <button
          className="button danger"
          disabled={mutation.isPending}
          onClick={() => mutation.mutate()}
        >
          {mutation.isPending ? '正在删除…' : '确认删除'}
        </button>
      </div>
    </Modal>
  );
}

export function ListPage({ kind }: { kind: CatalogKind }) {
  const module = moduleMap[kind];
  const [params, setParams] = useSearchParams();
  const [search, setSearch] = useState(params.get('q') || '');
  const [deleting, setDeleting] = useState<CatalogRecord | null>(null);
  useEffect(() => {
    setSearch(params.get('q') || '');
  }, [params]);
  const query: ListQuery = {
    q: params.get('q') || '',
    status: params.get('status') || '',
    difficulty: params.get('difficulty') || '',
    page: Number(params.get('page') || 1),
    pageSize: 20,
  };
  const records = useQuery({
    queryKey: ['catalog', 'list', kind, query],
    queryFn: () => listRecords(kind, query),
  });
  const change = (values: Record<string, string>) => {
    const next = new URLSearchParams(params);
    Object.entries(values).forEach(([key, value]) => {
      if (value) next.set(key, value);
      else next.delete(key);
    });
    setParams(next);
  };
  const submit = (event: FormEvent) => {
    event.preventDefault();
    change({ q: search.trim(), page: '1' });
  };
  const filtered = !!query.q || !!query.status || !!query.difficulty;
  return (
    <>
      <PageHeading
        eyebrow="学习内容 / 管理"
        title={module.title}
        description={module.description}
        action={
          <Link className="button" to={`/${kind}/new`}>
            <Icon name="start" />
            新建{module.singular}
          </Link>
        }
      />
      <section className="list-panel">
        <form className="list-toolbar" onSubmit={submit}>
          <div className="search-box">
            <label className="sr-only" htmlFor="record-search">
              搜索{module.singular}
            </label>
            <input
              id="record-search"
              type="search"
              placeholder="搜索名称、编号或标签"
              value={search}
              onChange={(event) => setSearch(event.target.value)}
            />
            <button className="button secondary" type="submit">
              搜索
            </button>
          </div>
          <div className="list-filters">
            <label>
              <span className="sr-only">状态筛选</span>
              <select
                aria-label="状态筛选"
                value={query.status}
                onChange={(event) => change({ status: event.target.value, page: '1' })}
              >
                <option value="">全部状态</option>
                {statuses(module).map((value) => (
                  <option key={value} value={value}>
                    {statusLabels[value]}
                  </option>
                ))}
              </select>
            </label>
            {kind === 'problems' && (
              <select
                aria-label="难度筛选"
                value={query.difficulty}
                onChange={(event) => change({ difficulty: event.target.value, page: '1' })}
              >
                <option value="">全部难度</option>
                <option value="easy">入门</option>
                <option value="medium">进阶</option>
                <option value="hard">挑战</option>
              </select>
            )}
            {filtered && (
              <button type="button" className="text-button" onClick={() => setParams({})}>
                清除筛选
              </button>
            )}
          </div>
        </form>
        {records.isPending ? (
          <Loading />
        ) : records.isError ? (
          <ErrorPanel error={records.error} retry={() => void records.refetch()} />
        ) : records.data.total === 0 ? (
          <EmptyState
            title={filtered ? '没有匹配的资料' : `还没有${module.singular}`}
            description={
              filtered
                ? '试试其他关键词或清除筛选条件。'
                : `从第一份${module.singular}开始，逐步整理你的学习内容。`
            }
            href={filtered ? undefined : `/${kind}/new`}
            action={`新建${module.singular}`}
            icon={module.icon}
          />
        ) : (
          <>
            <div className="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th scope="col">编号 / 名称</th>
                    <th scope="col">{kind === 'problems' ? '难度' : '概况'}</th>
                    <th scope="col">状态</th>
                    <th scope="col">更新时间</th>
                    <th scope="col" className="actions-column">
                      操作
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {records.data.items.map((record) => (
                    <tr key={record.id}>
                      <td>
                        <Link className="record-title" to={`/${kind}/${record.id}`}>
                          {record.name}
                        </Link>
                        <div className="record-code">{record.code}</div>
                      </td>
                      <td>{brief(record)}</td>
                      <td>
                        <Status value={record.status} />
                      </td>
                      <td className="date-cell">{formatDate(record.updatedAt)}</td>
                      <td>
                        <div className="row-actions">
                          <Link to={`/${kind}/${record.id}`}>详情</Link>
                          {record.canEdit && (
                            <>
                              <Link to={`/${kind}/${record.id}/edit`}>编辑</Link>
                              <button
                                type="button"
                                className="text-button danger-text"
                                onClick={() => setDeleting(record)}
                              >
                                删除
                              </button>
                            </>
                          )}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="pagination">
              <span>
                共 {records.data.total} 条 · 第 {records.data.page} /{' '}
                {Math.max(1, Math.ceil(records.data.total / records.data.pageSize))} 页
              </span>
              <div className="button-row">
                <button
                  className="button secondary"
                  disabled={(query.page || 1) <= 1}
                  onClick={() => change({ page: String((query.page || 1) - 1) })}
                >
                  上一页
                </button>
                <button
                  className="button secondary"
                  disabled={(query.page || 1) * 20 >= records.data.total}
                  onClick={() => change({ page: String((query.page || 1) + 1) })}
                >
                  下一页
                </button>
              </div>
            </div>
            {records.data.items.length === 0 && <Notice>当前页已无记录，请返回上一页查看。</Notice>}
          </>
        )}
      </section>
      <DeleteDialog record={deleting} module={module} onClose={() => setDeleting(null)} />
    </>
  );
}

export function DetailPage({ kind }: { kind: CatalogKind }) {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const module = moduleMap[kind];
  const query = useQuery({
    queryKey: ['catalog', 'record', kind, id],
    queryFn: () => getRecord(kind, id),
  });
  const [deleting, setDeleting] = useState<CatalogRecord | null>(null);
  if (query.isPending) return <Loading />;
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
  const fields = record.fields as Record<string, unknown>;
  return (
    <>
      <Link className="back-link" to={`/${kind}`}>
        返回{module.title}
      </Link>
      <PageHeading
        eyebrow={`${module.singular}详情 / ${record.code}`}
        title={record.name}
        action={
          record.canEdit ? (
            <div className="button-row">
              <Link className="button" to={`/${kind}/${record.id}/edit`}>
                编辑{module.singular}
              </Link>
              <button className="button secondary danger-text" onClick={() => setDeleting(record)}>
                删除
              </button>
            </div>
          ) : undefined
        }
      />
      <div className="record-meta">
        <Status value={record.status} />
        <span>编号 {record.code}</span>
        <span>版本 {record.version}</span>
        <span>更新于 {formatDate(record.updatedAt)}</span>
      </div>
      <section className="detail-panel">
        <div className="detail-field full-width">
          <h2>简介与备注</h2>
          <p className="reading-text">{record.description || '暂无简介。'}</p>
        </div>
        {module.fields.map((field) => (
          <ReadonlyField key={field.key} field={field} value={fields[field.key]} />
        ))}
      </section>
      {['assignments', 'contests', 'templates'].includes(kind) && (
        <Notice>
          {kind === 'assignments'
            ? '资料编辑与任务发布分开管理，用户获得的是已发布版本。'
            : '当前保存的是资料草稿，不会启动沙箱、开放比赛或执行判题。'}
        </Notice>
      )}
      {kind === 'assignments' && (
        <PublishingPanel
          id={id}
          version={record.version}
          canEdit={record.canEdit}
          courseId={String(fields.courseId || '')}
        />
      )}
      <DeleteDialog
        record={deleting}
        module={module}
        onClose={() => setDeleting(null)}
        onDeleted={() => navigate(`/${kind}`)}
      />
    </>
  );
}

function localDate(value: unknown) {
  if (!value) return '';
  const date = new Date(String(value));
  return new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
}
function RecordForm({ module, record }: { module: Module; record?: CatalogRecord }) {
  const navigate = useNavigate();
  const cache = useQueryClient();
  const key = useRef(crypto.randomUUID());
  const [code, setCode] = useState(record?.code || '');
  const [name, setName] = useState(record?.name || '');
  const [description, setDescription] = useState(record?.description || '');
  const [status, setStatus] = useState(record?.status || module.defaultStatus);
  const [fields, setFields] = useState<Record<string, unknown>>(() => {
    const initial: Record<string, unknown> = { ...module.defaults, ...record?.fields };
    module.fields.forEach((field) => {
      if (field.type === 'datetime') initial[field.key] = localDate(initial[field.key]);
    });
    return initial;
  });
  const mutation = useMutation({
    mutationFn: (input: CatalogInput) => saveRecord(module.kind, input, key.current, record),
    onSuccess: async (value) => {
      await cache.invalidateQueries({ queryKey: ['catalog'] });
      navigate(`/${module.kind}/${value.id}`, { replace: true });
    },
  });
  const errors = mutation.error instanceof APIError ? mutation.error.fields : {};
  const submit = (event: FormEvent) => {
    event.preventDefault();
    const converted = { ...fields };
    module.fields.forEach((field) => {
      const value = converted[field.key];
      if (field.type === 'number') converted[field.key] = Number(value);
      if (field.type === 'tags')
        converted[field.key] = Array.isArray(value)
          ? value
          : String(value || '')
              .split(/[,，\n]/)
              .map((item) => item.trim())
              .filter(Boolean);
      if (field.type === 'datetime')
        converted[field.key] = value ? new Date(String(value)).toISOString() : '';
    });
    mutation.mutate({
      code,
      name,
      description,
      status: status as CatalogInput['status'],
      fields: converted as CatalogInput['fields'],
    });
  };
  const setField = (key: string, value: unknown) =>
    setFields((previous) => ({ ...previous, [key]: value }));
  return (
    <>
      <Link className="back-link" to={record ? `/${module.kind}/${record.id}` : `/${module.kind}`}>
        返回{record ? '详情' : module.title}
      </Link>
      <PageHeading
        eyebrow={`${module.singular} / ${record ? '编辑' : '新建'}`}
        title={`${record ? '编辑' : '新建'}${module.singular}`}
        description={module.description}
      />
      <form onSubmit={submit} className="editor-form">
        <FormError error={mutation.error} />
        <section className="form-section">
          <h2>基本信息</h2>
          <div className="form-grid">
            <div className="field">
              <label htmlFor="record-code">
                {module.singular}编号
                <span className="required" aria-hidden="true">
                  {' '}
                  *
                </span>
              </label>
              <input
                id="record-code"
                required
                maxLength={32}
                pattern="[A-Za-z0-9][A-Za-z0-9_-]{0,31}"
                value={code}
                aria-invalid={!!errors.code}
                aria-describedby="code-help code-error"
                onChange={(event) => setCode(event.target.value)}
              />
              <span className="field-help" id="code-help">
                字母、数字、下划线或连字符；同类编号不能重复。
              </span>
              <span className="field-error" id="code-error">
                {errors.code}
              </span>
            </div>
            <div className="field">
              <label htmlFor="record-name">
                {module.kind === 'students' ? '学员姓名' : `${module.singular}名称`}
                <span className="required" aria-hidden="true">
                  {' '}
                  *
                </span>
              </label>
              <input
                id="record-name"
                required
                maxLength={120}
                value={name}
                aria-invalid={!!errors.name}
                aria-describedby="name-error"
                onChange={(event) => setName(event.target.value)}
              />
              <span className="field-error" id="name-error">
                {errors.name}
              </span>
            </div>
            <div className="field">
              <label htmlFor="record-status">资料状态</label>
              <select
                id="record-status"
                value={status}
                onChange={(event) => setStatus(event.target.value)}
              >
                {statuses(module).map((value) => (
                  <option key={value} value={value}>
                    {statusLabels[value]}
                  </option>
                ))}
              </select>
            </div>
            <div className="field full-width">
              <label htmlFor="record-description">简介与备注</label>
              <textarea
                id="record-description"
                rows={3}
                maxLength={4000}
                value={description}
                aria-invalid={!!errors.description}
                aria-describedby="description-error"
                onChange={(event) => setDescription(event.target.value)}
              />
              <span className="field-error" id="description-error">
                {errors.description}
              </span>
            </div>
          </div>
        </section>
        <section className="form-section">
          <h2>{module.kind === 'problems' ? '题面与公开样例' : '详细信息'}</h2>
          {errors.fields && (
            <Notice tone="danger" role="alert">
              {errors.fields}
            </Notice>
          )}
          <div className="form-grid">
            {module.fields.map((field) => (
              <EditorField
                key={field.key}
                field={field}
                value={fields[field.key]}
                onChange={(value) => setField(field.key, value)}
                error={errors[field.key]}
              />
            ))}
          </div>
        </section>
        <div className="editor-actions">
          <p className="field-help">
            保存后可继续编辑资料。
            {record
              ? `当前版本 ${record.version}，并发修改会提示冲突。`
              : '不会发布任务或启动沙箱。'}
          </p>
          <div className="button-row">
            <Link
              className={`button secondary ${mutation.isPending ? 'inert-link' : ''}`}
              aria-disabled={mutation.isPending}
              to={record ? `/${module.kind}/${record.id}` : `/${module.kind}`}
            >
              取消
            </Link>
            <button className="button" type="submit" disabled={mutation.isPending}>
              {mutation.isPending ? '正在保存…' : `保存${module.singular}`}
            </button>
          </div>
        </div>
      </form>
    </>
  );
}
export function EditorPage({ kind }: { kind: CatalogKind }) {
  const { id } = useParams();
  const module = moduleMap[kind];
  const query = useQuery({
    queryKey: ['catalog', 'record', kind, id],
    queryFn: () => getRecord(kind, id!),
    enabled: !!id,
    refetchOnWindowFocus: false,
    staleTime: Infinity,
  });
  if (id && query.isPending) return <Loading />;
  if (id && query.isError)
    return <ErrorPanel error={query.error} retry={() => void query.refetch()} />;
  if (id && query.data && !query.data.canEdit) return <Forbidden />;
  return <RecordForm key={id || 'new'} module={module} record={id ? query.data : undefined} />;
}
