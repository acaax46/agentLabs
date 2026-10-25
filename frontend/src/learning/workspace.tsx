import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useParams } from 'react-router-dom';
import { connections, canUseConnection } from '../connections/api';
import type { Connection } from '../connections/api';
import { ErrorPanel, Icon, Loading, Notice, PageHeading } from '../catalog/shared';
import { LazyMarkdown } from '../components/LazyMarkdown';
import { LazyCodeEditor } from '../editor/LazyCodeEditor';
import { buildHtmlPreview } from './htmlPreview';
import type { HtmlPreview } from './htmlPreview';
import { learning, jobLabels } from './api';
import type { Workspace, Release, Job, File, Price, Capabilities, Event } from './api';
export function WorkspacePage() {
  const { id = '' } = useParams();
  const cache = useQueryClient();
  const query = useQuery({
    queryKey: ['learning', 'workspace', id],
    queryFn: () => learning<Workspace>('workspace', id),
    refetchInterval: 5000,
  });
  const release = useQuery({
    queryKey: ['learning', 'release', query.data?.releaseId],
    queryFn: () => learning<Release>('release', query.data!.releaseId),
    enabled: !!query.data,
  });
  const capabilities = useQuery({
    queryKey: ['learning', 'capabilities'],
    queryFn: () => learning<Capabilities>('capabilities'),
    refetchInterval: 15000,
  });
  const jobs = useQuery({
    queryKey: ['learning', 'jobs', id],
    queryFn: () => learning<Job[]>('jobs', id),
    refetchInterval: 2000,
  });
  const events = useQuery({
    queryKey: ['learning', 'events', id],
    queryFn: () => learning<Event[]>('events', id),
    refetchInterval: 2000,
  });
  const prices = useQuery({
    queryKey: ['learning', 'prices'],
    queryFn: () => learning<Price[]>('prices'),
  });
  const accounts = useQuery({
    queryKey: ['connections'],
    queryFn: () => connections<Connection[]>('list'),
    refetchInterval: 15000,
  });
  const [connectionId, setConnectionId] = useState('');
  const [assistantOpen, setAssistantOpen] = useState(false);
  const [previewOpen, setPreviewOpen] = useState(false);
  const [preview, setPreview] = useState<HtmlPreview | null>(null);
  const [files, setFiles] = useState<File[]>([]),
    [selected, setSelected] = useState(''),
    [dirty, setDirty] = useState(false),
    [version, setVersion] = useState(0),
    [prompt, setPrompt] = useState(''),
    [price, setPrice] = useState(''),
    [problem, setProblem] = useState(''),
    [name, setName] = useState(''),
    [saved, setSaved] = useState(false);
  const selectedPrice = prices.data?.find((p) => p.id === price);
  useEffect(() => {
    if (!query.data || dirty) return;
    setFiles((current) => {
      const next = query.data.files;
      if (
        current.length === next.length &&
        current.every(
          (file, index) => file.path === next[index]?.path && file.content === next[index]?.content,
        )
      )
        return current;
      return next;
    });
    setVersion(query.data.version);
    setSelected((current) => {
      if (current && query.data.files.some((file) => file.path === current)) return current;
      return query.data.files[0]?.path || '';
    });
  }, [query.data, dirty]);
  const refresh = () => {
    void cache.invalidateQueries({ queryKey: ['learning'] });
  };
  const save = useMutation({
    mutationFn: () =>
      learning<Workspace>('save', id, { version, generation: query.data!.generation, files }),
    onSuccess: (w) => {
      cache.setQueryData(['learning', 'workspace', id], w);
      setVersion(w.version);
      setDirty(false);
      setSaved(true);
    },
  });
  const [operationKey, setOperationKey] = useState(crypto.randomUUID());
  const run = useMutation({
    mutationFn: (kind: string) =>
      learning<Job>(
        'enqueue',
        id,
        {
          kind,
          version: query.data!.version,
          prompt,
          priceVersion: price,
          connectionId,
          problemId: problem || release.data?.snapshot.problems[0]?.id || '',
        },
        operationKey,
      ),
    onSuccess: () => {
      setOperationKey(crypto.randomUUID());
      setPrompt('');
      refresh();
    },
  });
  const cancel = useMutation({
    mutationFn: (jobId: string) => learning('cancel', jobId),
    onSuccess: refresh,
  });
  const reset = useMutation({
    mutationFn: () =>
      learning<Workspace>('reset', id, {
        version: query.data!.version,
        generation: query.data!.generation,
        files: [],
      }),
    onSuccess: (w) => {
      setDirty(false);
      setSelected('');
      setSaved(false);
      cache.setQueryData(['learning', 'workspace', id], w);
      refresh();
    },
  });
  useEffect(() => {
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => {
      e.preventDefault();
    };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [dirty]);
  const snapshotProblems = release.data?.snapshot.problems ?? [];
  const activeProblem = snapshotProblems.find((item) => item.id === problem) || snapshotProblems[0];
  const activeFields = activeProblem?.fields as
    | {
        statement?: string;
        inputDescription?: string;
        outputDescription?: string;
        language?: string;
        samples?: { input: string; output: string; explanation?: string }[];
      }
    | undefined;
  const isHtml = activeFields?.language === 'html';
  useEffect(() => {
    if (!previewOpen || !isHtml) {
      setPreview(null);
      return;
    }
    const next = buildHtmlPreview(files);
    setPreview(next);
    return () => next.revoke();
  }, [previewOpen, isHtml, files]);
  if (query.isPending) return <Loading />;
  if (query.isError) return <ErrorPanel error={query.error} retry={() => void query.refetch()} />;
  const w = query.data;
  const active = jobs.data?.find((j) => ['queued', 'running', 'unknown'].includes(j.status));
  const source = files.find((f) => f.path === selected);
  const blocked = dirty || !!active || release.data?.revoked;
  const edit = (next: File[]) => {
    setFiles(next);
    setDirty(true);
    setSaved(false);
    save.reset();
  };
  return (
    <>
      <PageHeading
        eyebrow={release.data?.public ? '训练题目 / 代码空间' : '代码空间 / 我的成果'}
        title={release.data?.snapshot.assignment.name || '代码空间'}
        description={
          release.data?.public
            ? `公开训练题 · 代码空间代次 ${w.generation}`
            : `发布版本 ${release.data?.revision || '…'} · 代码空间代次 ${w.generation}`
        }
        action={
          <button
            className="button"
            disabled={!dirty || save.isPending}
            onClick={() => save.mutate()}
          >
            {save.isPending ? '正在保存…' : '保存成果'}
          </button>
        }
      />
      <div className="workspace-status" role="status">
        {dirty ? '有未保存修改' : saved ? '成果已保存' : '成果已与服务端同步'} · 文件版本 {version}
      </div>
      {!capabilities.data?.agent && !capabilities.data?.publicTest && (
        <Notice>{capabilities.data?.reason || '正在确认执行能力…'}</Notice>
      )}
      {[
        save.error,
        run.error,
        cancel.error,
        reset.error,
        release.error,
        capabilities.error,
        prices.error,
      ]
        .filter(Boolean)
        .map((e, i) => (
          <ErrorPanel key={i} error={e} />
        ))}
      {save.error && (
        <p>
          本地编辑仍保留。请先将需要的内容另存，再{' '}
          <button
            className="text-button"
            onClick={() => {
              if (window.confirm('重新读取将放弃当前未保存编辑，确定继续？')) {
                setDirty(false);
                save.reset();
                void query.refetch();
              }
            }}
          >
            重新读取服务端版本
          </button>
          。
        </p>
      )}
      {activeProblem && (
        <section className="learning-panel workspace-problem">
          <div className="section-heading">
            <div>
              <h2>
                {activeProblem.code} · {activeProblem.name}
              </h2>
              <p>阅读题目说明和公开样例，然后在下方代码空间中完成练习。</p>
            </div>
          </div>
          <LazyMarkdown value={activeFields?.statement ?? ''} emptyText="尚未提供题面。" />
          {activeFields?.inputDescription && (
            <>
              <h3>输入说明</h3>
              <LazyMarkdown value={activeFields.inputDescription} />
            </>
          )}
          {activeFields?.outputDescription && (
            <>
              <h3>输出说明</h3>
              <LazyMarkdown value={activeFields.outputDescription} />
            </>
          )}
          {!!activeFields?.samples?.length && (
            <div className="workspace-samples">
              <h3>公开样例</h3>
              {activeFields.samples.map((sample, index) => (
                <div className="sample-grid" key={index}>
                  <div>
                    <h3>样例 {index + 1} 输入</h3>
                    <pre>{sample.input || '（空输入）'}</pre>
                  </div>
                  <div>
                    <h3>样例 {index + 1} 输出</h3>
                    <pre>{sample.output}</pre>
                  </div>
                </div>
              ))}
            </div>
          )}
        </section>
      )}
      <div className="learning-workspace">
        <aside className="workspace-files" aria-label="工作区文件">
          <div className="workspace-files-head">
            <strong>工作区文件</strong>
            <span>{files.length}/32</span>
          </div>
          <ul>
            {files.map((file) => (
              <li key={file.path}>
                <button
                  type="button"
                  className={file.path === selected ? 'active' : ''}
                  aria-current={file.path === selected ? 'true' : undefined}
                  onClick={() => setSelected(file.path)}
                >
                  <Icon name="files" />
                  <span>{file.path}</span>
                </button>
              </li>
            ))}
          </ul>
          <button
            className="text-button danger-text workspace-delete-file"
            type="button"
            disabled={files.length <= 1}
            onClick={() => {
              if (window.confirm(`删除 ${selected}？保存后生效。`)) {
                edit(files.filter((f) => f.path !== selected));
                setSelected(files.find((f) => f.path !== selected)?.path || '');
              }
            }}
          >
            删除当前文件
          </button>
          <form
            className="workspace-new-file"
            onSubmit={(e) => {
              e.preventDefault();
              if (!name || files.some((f) => f.path.toLowerCase() === name.toLowerCase())) return;
              edit([...files, { path: name, content: '' }]);
              setSelected(name);
              setName('');
            }}
          >
            <input
              aria-label="新文件路径"
              placeholder="例如 src/solution.py"
              maxLength={180}
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
            <button className="button secondary" disabled={files.length >= 32 || !name}>
              添加文件
            </button>
          </form>
          <p className="field-help">仅保存当前项目文本文件，文件路径与大小由服务端校验。</p>
        </aside>
        <section className="learning-editor" aria-label="代码编辑器">
          <div className="workspace-toolbar">
            <div className="workspace-current-file">
              <strong>{selected || '未选择文件'}</strong>
              <span>{isHtml ? 'HTML / CSS / JavaScript' : 'Python'}</span>
            </div>
            {isHtml && (
              <button
                className="button secondary"
                type="button"
                onClick={() => setPreviewOpen((value) => !value)}
              >
                {previewOpen ? '关闭预览' : '预览页面'}
              </button>
            )}
          </div>
          <LazyCodeEditor
            path={selected}
            ariaLabel={`编辑 ${selected}`}
            value={source?.content || ''}
            onChange={(content) =>
              edit(files.map((f) => (f.path === selected ? { ...f, content } : f)))
            }
          />
          {isHtml && previewOpen && (
            <div className="html-preview">
              <div className="html-preview-head">
                <div>
                  <strong>隔离预览</strong>
                  <span>sandbox iframe</span>
                </div>
                <button className="text-button" type="button" onClick={() => setPreviewOpen(false)}>
                  关闭
                </button>
              </div>
              <iframe
                title="HTML 预览"
                sandbox="allow-scripts allow-forms"
                src={preview?.url || 'about:blank'}
              />
            </div>
          )}
        </section>
      </div>
      {assistantOpen && (
        <aside id="programming-assistant" className="assistant-panel" aria-label="编程助手">
          <div className="assistant-head">
            <div>
              <strong>编程助手</strong>
              <span>基于当前代码空间</span>
            </div>
            <button className="text-button" type="button" onClick={() => setAssistantOpen(false)}>
              收起
            </button>
          </div>
          <div className="learning-messages">
            {w.messages.length ? (
              w.messages.map((m) => (
                <article key={m.id} className={`learning-message ${m.role}`}>
                  <strong>{m.role === 'user' ? '我' : '编程助手'}</strong>
                  {m.role === 'user' ? (
                    <p className="reading-text">{m.content}</p>
                  ) : (
                    <LazyMarkdown value={m.content} emptyText="编程助手尚未返回内容。" />
                  )}
                </article>
              ))
            ) : (
              <p className="field-help">接入真实执行器后，可以在此发送任务并保留对话。</p>
            )}
          </div>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              run.mutate('agent');
            }}
          >
            <label className="field">
              模型价格版本
              <select
                value={price}
                onChange={(e) => {
                  setPrice(e.target.value);
                  setOperationKey(crypto.randomUUID());
                }}
              >
                <option value="">请选择管理员配置的模型</option>
                {prices.data?.map((p) => (
                  <option value={p.id} key={p.id}>
                    {p.provider} / {p.model} · {p.id}
                  </option>
                ))}
              </select>
            </label>
            <label className="field">
              模型账户
              <select
                value={connectionId}
                onChange={(e) => {
                  setConnectionId(e.target.value);
                  setOperationKey(crypto.randomUUID());
                }}
              >
                <option value="">请选择已验证的模型账户</option>
                {accounts.data
                  ?.filter(
                    (c) =>
                      canUseConnection(c) &&
                      c.provider === selectedPrice?.provider &&
                      c.models.includes(selectedPrice.model),
                  )
                  .map((c) => (
                    <option value={c.id} key={c.id}>
                      {c.name} · {c.scope === 'tenant' ? '机构共享' : '个人账户'}
                    </option>
                  ))}
              </select>
            </label>
            {accounts.isError && <Notice tone="danger">{accounts.error.message}</Notice>}
            <p className="field-help">
              任务对话与当前项目代码将发送给所选模型服务，调用受预算限制。
              <Link to="/connections">管理账户连接</Link>
            </p>
            <label className="field">
              向编程助手提问
              <textarea
                rows={4}
                maxLength={16000}
                value={prompt}
                onChange={(e) => {
                  setPrompt(e.target.value);
                  setOperationKey(crypto.randomUUID());
                }}
              />
            </label>
            <button
              className="button"
              disabled={
                !capabilities.data?.agent ||
                !price ||
                !connectionId ||
                !accounts.data?.some(
                  (c) =>
                    c.id === connectionId &&
                    canUseConnection(c) &&
                    c.provider === selectedPrice?.provider &&
                    c.models.includes(selectedPrice.model),
                ) ||
                !prompt.trim() ||
                blocked ||
                run.isPending
              }
            >
              发送任务
            </button>
          </form>
        </aside>
      )}
      {!assistantOpen && (
        <button
          className="assistant-fab"
          type="button"
          aria-label="打开编程助手"
          title="编程助手"
          aria-expanded={assistantOpen}
          aria-controls="programming-assistant"
          onClick={() => setAssistantOpen(true)}
        >
          <Icon name="agent" />
        </button>
      )}
      <section className="learning-panel">
        <div className="section-heading">
          <div>
            <h2>{isHtml ? '运行输出与页面预览' : '运行输出与公开测试'}</h2>
            <p>
              {isHtml
                ? 'HTML 题目使用隔离预览，不进入 stdin/stdout 评测。'
                : '只运行已保存的文件；公开测试反馈不作为正式成绩。'}
            </p>
          </div>
          <div className="button-row">
            {isHtml ? (
              <button
                className="button secondary"
                type="button"
                onClick={() => setPreviewOpen(true)}
              >
                打开页面预览
              </button>
            ) : (
              <>
                <select
                  aria-label="公开测试题目"
                  value={problem || release.data?.snapshot.problems[0]?.id || ''}
                  onChange={(e) => {
                    setProblem(e.target.value);
                    setOperationKey(crypto.randomUUID());
                  }}
                >
                  {release.data?.snapshot.problems.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.code} · {p.name}
                    </option>
                  ))}
                </select>
                <button
                  className="button secondary"
                  disabled={!capabilities.data?.publicTest || blocked || run.isPending}
                  onClick={() => run.mutate('public_test')}
                >
                  运行公开测试
                </button>
                <button
                  className="button"
                  disabled={
                    !capabilities.data?.submission ||
                    blocked ||
                    run.isPending ||
                    !release.data?.submissionProblemIds?.includes(
                      problem || release.data?.snapshot.problems[0]?.id || '',
                    )
                  }
                  onClick={() => {
                    if (window.confirm('提交将冻结当前已保存文件，并计入任务提交次数。是否继续？'))
                      run.mutate('submission');
                  }}
                >
                  提交正式评测
                </button>
              </>
            )}
          </div>
        </div>
        {jobs.isError ? (
          <ErrorPanel error={jobs.error} />
        ) : (
          jobs.data?.map((j) => (
            <article className="learning-job" key={j.id}>
              <div>
                <strong>
                  {j.kind === 'agent'
                    ? '编程助手'
                    : j.kind === 'submission'
                      ? '正式评测'
                      : '公开测试'}{' '}
                  · {jobLabels[j.status] || j.status}
                </strong>
                <span>
                  第 {j.attempts} 次执行 · 代码空间代次 {j.generation}
                </span>
                {['queued', 'running'].includes(j.status) && (
                  <button
                    className="text-button danger-text"
                    disabled={cancel.isPending || j.cancelRequested}
                    onClick={() => cancel.mutate(j.id)}
                  >
                    {j.cancelRequested ? '已请求停止' : '停止任务'}
                  </button>
                )}
              </div>
              {j.error && <p role="status">{j.error}</p>}
              {j.kind === 'submission' && (
                <p className="field-help">
                  结果版本 {j.resultVersion} · 源码摘要 {j.sourceDigest?.slice(0, 16)}
                </p>
              )}
              {j.evaluation && (
                <p>
                  评测结果：{j.evaluation.verdict || j.evaluation.status} · 得分{' '}
                  {j.evaluation.score ?? '待确认'} / {j.evaluation.maxScore ?? '未知'}
                </p>
              )}
              {j.output && <pre className="workspace-output">{j.output}</pre>}
            </article>
          ))
        )}
        {events.isError && <ErrorPanel error={events.error} />}
        <details>
          <summary>运行进度</summary>
          <ul className="learning-events">
            {events.data?.map((event) => (
              <li key={event.id}>
                {event.type} · {event.text}
              </li>
            ))}
          </ul>
        </details>
      </section>
      <section className="learning-panel">
        <h2>成果管理</h2>
        <div className="button-row">
          <button
            className="button secondary"
            onClick={() => {
              const url = URL.createObjectURL(
                new Blob(
                  [
                    JSON.stringify(
                      { releaseId: w.releaseId, generation: w.generation, files },
                      null,
                      2,
                    ),
                  ],
                  { type: 'application/json' },
                ),
              );
              const a = document.createElement('a');
              a.href = url;
              a.download = 'agentlab-project.json';
              a.click();
              URL.revokeObjectURL(url);
            }}
          >
            导出当前项目
          </button>
          <button
            className="button secondary danger-text"
            disabled={!!active || reset.isPending}
            onClick={() => {
              if (
                window.confirm(
                  '恢复初始文件会归档当前已保存成果并建立新代次，未保存编辑会丢弃。是否继续？',
                )
              )
                reset.mutate();
            }}
          >
            恢复初始文件
          </button>
        </div>
      </section>
    </>
  );
}
