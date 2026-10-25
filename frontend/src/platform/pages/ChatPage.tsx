import { useEffect, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { all, get, requestKey, uploadFile, write } from '../api';
import type {
  Agent,
  Artifact,
  Conversation,
  ConversationDetail,
  ModelOption,
  RateSheet,
  Run,
} from '../api';
import { usePlatform } from '../state';
import { decimalToMicros, formatBytes, formatMoney, isTerminal, modelSourceLabels } from '../model';
import { MutationError, QueryState, RequireProject } from '../shared';
import { Icon, Modal } from '../ui';
import { ArtifactPreview } from './ArtifactsPage';
import { useConversationEvents } from '../conversationEvents';
import { stripTerminalControls } from '../logs';

type Result = { summary?: string; findings?: string[]; warnings?: string[] };

function ChatReply({ text }: { text: string }) {
  return (
    <div className="wb-chat-markdown">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        skipHtml
        components={{
          img: ({ alt }) => <span>[图片：{alt}，请在产物中预览]</span>,
          a: ({ children }) => <span>{children}</span>,
        }}
      >
        {stripTerminalControls(text)}
      </ReactMarkdown>
    </div>
  );
}

function ChatTurn({ run, onPreview }: { run: Run; onPreview: (artifact: Artifact) => void }) {
  const active = !isTerminal(run.status);
  const stream = useConversationEvents(run.id, active);
  const cache = useQueryClient();
  const [confirmStop, setConfirmStop] = useState(false);
  const stop = useMutation({
    mutationFn: () => write<Run>(`/runs/${run.id}/cancel`, {}),
    retry: false,
    onSuccess: () => {
      setConfirmStop(false);
      void cache.invalidateQueries({ queryKey: ['platform', 'conversation'] });
    },
  });
  const files = useQuery({
    queryKey: ['platform', 'chat-files', run.id],
    queryFn: ({ signal }) =>
      all<Artifact>(
        '/artifacts',
        { projectId: run.projectId, runId: run.id, kind: 'output' },
        signal,
      ),
    retry: false,
    refetchInterval: active ? 2500 : false,
  });
  const inputFiles = useQuery({
    queryKey: ['platform', 'chat-inputs', run.id],
    queryFn: ({ signal }) =>
      Promise.all(run.inputArtifactIds.map((id) => get<Artifact>(`/artifacts/${id}`, {}, signal))),
    enabled: !!run.inputArtifactIds.length,
    retry: false,
  });
  const result = (run.result ?? {}) as Result;
  const replies = new Map<number, string>();
  stream.events.forEach((event) => {
    const step = event.data?.step ?? 0;
    if (event.type === 'reply.delta') replies.set(step, (replies.get(step) ?? '') + event.message);
    if (event.type === 'reply.updated') replies.set(step, event.message);
  });
  const liveReply = Array.from(replies.values()).at(-1) ?? '';
  const progress = stream.events.filter(
    (event) => !event.type.startsWith('reply.') && !event.type.startsWith('harness.message'),
  );
  const latest = progress.at(-1);
  const status = active
    ? run.cancelRequested
      ? '正在停止'
      : (latest?.message ?? '正在准备沙箱…')
    : run.status === 'succeeded'
      ? '执行完成'
      : run.status === 'unknown'
        ? '结果待核对'
        : run.status === 'cancelled'
          ? '已停止'
          : '本轮未完成';
  return (
    <article className="wb-chat-turn" aria-label={`第 ${run.conversationTurn ?? 1} 轮对话`}>
      <div className="wb-chat-user">
        <span className="wb-chat-author">你</span>
        <p>{run.instruction}</p>
        {!!inputFiles.data?.length && (
          <div className="wb-chat-attachments">
            {inputFiles.data.map((file) => (
              <button key={file.id} className="wb-chat-file" onClick={() => onPreview(file)}>
                <Icon name="file" size={16} />
                {file.name}
              </button>
            ))}
          </div>
        )}
      </div>
      <div className="wb-chat-assistant">
        <span className="wb-chat-author">
          <Icon name="agents" size={17} />
          {run.agent.name}
        </span>
        <details className="wb-chat-progress">
          <summary>
            {active ? (
              <span className="wb-spinner" />
            ) : (
              <Icon name={run.status === 'succeeded' ? 'check' : 'warning'} size={16} />
            )}
            <span>{status}</span>
            <span className="wb-chat-step-count">
              {progress.length ? `${progress.length} 个事件` : ''}
              <Icon name="chevronDown" size={14} />
            </span>
          </summary>
          <ol>
            {progress.map((event) => (
              <li key={event.id}>
                <span>
                  {event.source === 'harness'
                    ? '引擎报告'
                    : event.source === 'gateway'
                      ? '模型'
                      : event.source === 'sandbox'
                        ? '沙箱'
                        : '执行'}
                </span>
                <p>{stripTerminalControls(event.message)}</p>
              </li>
            ))}
          </ol>
          {!progress.length && <p className="wb-muted">等待执行事件。</p>}
          <Link className="wb-text-link" to={`/runs/${run.id}`}>
            查看完整运行记录
          </Link>
        </details>
        {stream.reconnecting && (
          <p className="wb-chat-stream-status" role="status">
            连接恢复中，已收到的回复会保留…
          </p>
        )}
        {result.summary || liveReply ? (
          <ChatReply text={result.summary ?? liveReply} />
        ) : active ? (
          <p className="wb-chat-pending" role="status">
            正在处理你的消息<span className="wb-chat-dot">…</span>
          </p>
        ) : null}
        {!!result.findings?.length && (
          <ul className="wb-chat-findings">
            {result.findings.map((finding, index) => (
              <li key={index}>{finding}</li>
            ))}
          </ul>
        )}
        {!!result.warnings?.length && (
          <p className="wb-chat-stream-status">{result.warnings.join('；')}</p>
        )}
        {run.error && (
          <div className="wb-callout wb-callout--warning" role="alert">
            <div>
              <strong>{run.error.message}</strong>
              <p>{run.error.nextStep}</p>
            </div>
          </div>
        )}
        {run.status === 'unknown' && (
          <p className="wb-field-error">结果与费用待核对，预占保持冻结。请勿重复发送。</p>
        )}
        {!!files.data?.length && (
          <div className="wb-chat-artifact-grid">
            {files.data.map((file) => (
              <button key={file.id} className="wb-chat-artifact" onClick={() => onPreview(file)}>
                <span className="wb-chat-artifact-symbol">
                  <Icon name="file" size={22} />
                </span>
                <span>
                  <strong>{file.name}</strong>
                  <small>
                    {file.type.toUpperCase()} · {formatBytes(file.sizeBytes)} · 点击预览
                  </small>
                </span>
                <Icon name="chevronRight" size={16} />
              </button>
            ))}
          </div>
        )}
        {files.error && (
          <p className="wb-field-error">
            产物加载失败。
            <button className="wb-text-link" onClick={() => void files.refetch()}>
              重新加载
            </button>
          </p>
        )}
        <footer className="wb-chat-turn-footer">
          <span>
            {formatMoney(run.costMicros, run.currency)} {run.currency} ·{' '}
            {active ? '当前估算' : run.billingStatus === 'settled' ? '已结算' : '待核对'}
          </span>
          {active && (
            <button
              className="wb-text-link"
              disabled={run.cancelRequested || stop.isPending}
              onClick={() => setConfirmStop(true)}
            >
              <Icon name="stop" size={13} />
              {run.cancelRequested ? '正在停止…' : '停止生成'}
            </button>
          )}
        </footer>
      </div>
      <Modal
        open={confirmStop}
        onClose={() => setConfirmStop(false)}
        title="停止本轮生成？"
        description="停止后保留已发生的调用、费用和产物，已派发的外部操作无法撤回。"
      >
        <MutationError error={stop.error} />
        <div className="wb-form-actions">
          <button
            className="wb-button wb-button--danger"
            disabled={stop.isPending}
            onClick={() => stop.mutate()}
          >
            确认停止
          </button>
          <button className="wb-button wb-button--secondary" onClick={() => setConfirmStop(false)}>
            继续生成
          </button>
        </div>
      </Modal>
    </article>
  );
}

function SandboxStart({ onCreated }: { onCreated: (c: Conversation) => void }) {
  const { projectId, project, readiness } = usePlatform();
  const [params] = useSearchParams();
  const [agentId, setAgentId] = useState(params.get('agent') ?? '');
  const [resourceId, setResourceId] = useState('');
  const [modelId, setModelId] = useState('');
  const [harnessId, setHarnessId] = useState('');
  const [cap, setCap] = useState('2');
  const intent = useRef(requestKey());
  const agents = useQuery({
    queryKey: ['platform', 'agents'],
    queryFn: ({ signal }) => get<{ items: Agent[] }>('/agents', {}, signal),
    retry: false,
  });
  const models = useQuery({
    queryKey: ['platform', 'chat-models', projectId],
    queryFn: ({ signal }) => get<{ items: ModelOption[] }>('/models', { projectId }, signal),
    retry: false,
  });
  const rates = useQuery({
    queryKey: ['platform', 'chat-rates', projectId],
    queryFn: ({ signal }) => get<RateSheet>('/billing/rates', { projectId }, signal),
    retry: false,
  });
  const list =
    agents.data?.items.filter((agent) => !agent.projectId || agent.projectId === projectId) ?? [];
  const agent =
    list.find((agent) => agent.id === agentId) ??
    list.find((agent) => agent.id === 'agt_file_analysis') ??
    list[0];
  const version = agent?.latestVersion;
  const resources = (rates.data?.resources ?? []).filter((resource) =>
    version?.resourceProfiles?.length
      ? version.resourceProfiles.includes(resource.id)
      : !resource.gpu,
  );
  const selected =
    resources.find((resource) => resource.id === resourceId) ??
    resources.find((resource) => resource.available);
  const options = models.data?.items ?? [];
  const model =
    options.find((item) => `${item.connectionId}|${item.model}` === modelId) ??
    options.find((item) => item.usable);
  const harness =
    version?.harnesses?.find((id) => id === harnessId) ??
    version?.harnesses?.find((id) =>
      readiness?.capabilities.harnesses?.some((item) => item.id === id && item.available),
    ) ??
    version?.harnesses?.[0] ??
    'builtin';
  const engine = readiness?.capabilities.harnesses?.find((item) => item.id === harness);
  const needsResource = rates.data?.execution?.sandboxBilling === 'separate';
  const unavailable =
    !readiness?.capabilities.runs ||
    !readiness.capabilities.codeExecution ||
    !version ||
    !model?.usable ||
    !project?.budget.configured ||
    project.status !== 'active' ||
    (rates.data?.execution?.sandboxBilling === 'separate' && !selected?.available) ||
    (harness !== 'builtin' && !engine?.available);
  const create = useMutation({
    mutationFn: async () => {
      const body = {
        projectId,
        title: agent!.name.replace(/ Agent$/, '') + '对话',
        agentVersionId: version!.id,
        modelConnectionId: model!.connectionId,
        model: model!.model,
        resourceProfile: needsResource ? (selected?.id ?? '') : '',
        harness,
        limits: {
          maxCostMicros: decimalToMicros(cap),
          currency: project!.currency,
          maxExecutionSeconds: version!.limits.defaultExecutionSeconds,
          maxToolCalls: version!.limits.defaultToolCalls,
        },
      };
      return write<Conversation>('/conversations', body, intent.current(body));
    },
    onSuccess: onCreated,
    retry: false,
  });
  return (
    <div className="wb-chat-start">
      <span className="wb-chat-eyebrow">
        <Icon name="agents" size={18} /> AGENTLAB CONVERSATION
      </span>
      <h1>先选好沙箱，再聊你的想法。</h1>
      <p className="wb-chat-start-description">
        上传文件，描述目标。Agent 会在隔离环境里执行，你可以继续追问、修改结果，也可以随时打开产物。
      </p>
      <QueryState
        pending={agents.isPending || models.isPending || rates.isPending}
        error={agents.error ?? models.error ?? rates.error}
        retry={() => {
          void agents.refetch();
          void models.refetch();
          void rates.refetch();
        }}
      >
        <div className="wb-chat-config-row">
          <label className="wb-field">
            <span>选择 Agent</span>
            <select
              aria-label="对话 Agent"
              value={agent?.id ?? ''}
              onChange={(event) => {
                setAgentId(event.target.value);
                setResourceId('');
                setHarnessId('');
              }}
            >
              {list.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name}
                </option>
              ))}
            </select>
          </label>
          <label className="wb-field">
            <span>模型</span>
            <select
              aria-label="对话模型"
              value={model ? `${model.connectionId}|${model.model}` : ''}
              onChange={(event) => setModelId(event.target.value)}
            >
              <option value="" disabled>
                请选择可用模型
              </option>
              {options.map((item) => (
                <option
                  key={`${item.connectionId}|${item.model}`}
                  value={`${item.connectionId}|${item.model}`}
                  disabled={!item.usable}
                >
                  {item.model} · {item.source === 'byok' ? 'BYOK' : '平台供模'}
                  {!item.usable ? `（${item.reason ?? '不可用'}）` : ''}
                </option>
              ))}
            </select>
          </label>
        </div>
        <fieldset className="wb-chat-sandbox-options">
          <legend>沙箱规格</legend>
          {resources.length ? (
            resources.map((resource) => (
              <label
                key={resource.id}
                className={`wb-chat-sandbox ${selected?.id === resource.id ? 'is-selected' : ''} ${resource.available ? '' : 'is-disabled'}`}
              >
                <input
                  type="radio"
                  name="chat-sandbox"
                  value={resource.id}
                  checked={selected?.id === resource.id}
                  disabled={!resource.available}
                  onChange={() => setResourceId(resource.id)}
                />
                <span className="wb-chat-sandbox-icon">
                  <Icon name="terminal" size={22} />
                </span>
                <strong>{resource.name}</strong>
                <span>
                  {resource.cpu} vCPU · {resource.memoryMB / 1024} GiB
                  {resource.gpu ? ` · ${resource.gpuType}` : ''}
                </span>
                <small>
                  {resource.available
                    ? resource.price
                      ? `${formatMoney(resource.price.perSecondMicros, resource.price.currency)} ${resource.price.currency}/沙箱秒`
                      : '按当前托管规则计量'
                    : resource.reason}
                </small>
              </label>
            ))
          ) : (
            <p className="wb-muted">当前没有可用的沙箱规格，请在设置中检查执行能力与价格。</p>
          )}
        </fieldset>
        <details className="wb-chat-advanced">
          <summary>
            运行偏好与费用上限 <Icon name="chevronDown" size={14} />
          </summary>
          <div className="wb-chat-config-row">
            <label className="wb-field">
              <span>每轮费用上限（{project?.currency}）</span>
              <input
                aria-label="每轮费用上限"
                value={cap}
                inputMode="decimal"
                onChange={(event) => setCap(event.target.value)}
              />
            </label>
            <label className="wb-field">
              <span>执行引擎</span>
              <select
                aria-label="对话执行引擎"
                value={harness}
                disabled={!version?.harnesses?.length}
                onChange={(event) => setHarnessId(event.target.value)}
              >
                {(version?.harnesses?.length ? version.harnesses : ['builtin']).map((id) => {
                  const item = readiness?.capabilities.harnesses?.find(
                    (engine) => engine.id === id,
                  );
                  return (
                    <option key={id} value={id} disabled={id !== 'builtin' && !item?.available}>
                      {item?.name ?? (id === 'builtin' ? 'AgentLab 内置循环' : id)}
                      {item?.reason ? `（${item.reason}）` : ''}
                    </option>
                  );
                })}
              </select>
            </label>
          </div>
        </details>
        <div className="wb-chat-start-bottom">
          <p>
            {model ? `${modelSourceLabels[model.source]} · ` : ''}
            进入对话不收费，发送消息后按每轮上限预占预算。
            {model?.source === 'byok' && '模型费用由你的供应商账户承担。'}
          </p>
          <button
            className="wb-button wb-button--primary"
            disabled={unavailable || create.isPending}
            onClick={() => create.mutate()}
          >
            {create.isPending ? '正在准备…' : '进入对话'}
            <Icon name="chevronRight" size={16} />
          </button>
        </div>
        {unavailable && (
          <p className="wb-field-error">
            {!readiness?.capabilities.codeExecution
              ? (readiness?.capabilities.codeExecutionReason ?? '正在检查沙箱能力。')
              : !model
                ? '请先配置可用的模型连接。'
                : '当前 Agent 的沙箱或执行引擎不可用。'}{' '}
            <Link className="wb-text-link" to="/connections">
              管理连接
            </Link>
          </p>
        )}
        <MutationError error={create.error} />
      </QueryState>
    </div>
  );
}

function ConversationView({ id }: { id: string }) {
  const platform = usePlatform();
  const { projectId, selectProject } = platform;
  const cache = useQueryClient();
  const navigate = useNavigate();
  const [content, setContent] = useState('');
  const [uploads, setUploads] = useState<{ name: string; file?: Artifact; error?: string }[]>([]);
  const [preview, setPreview] = useState<Artifact | null>(null);
  const input = useRef<HTMLInputElement>(null);
  const textarea = useRef<HTMLTextAreaElement>(null);
  const end = useRef<HTMLDivElement>(null);
  const intent = useRef(requestKey());
  const query = useQuery({
    queryKey: ['platform', 'conversation', id],
    queryFn: ({ signal }) => get<ConversationDetail>(`/conversations/${id}`, {}, signal),
    retry: false,
    refetchInterval: (query) => {
      const last = query.state.data?.runs.at(-1);
      return last && !isTerminal(last.status) ? 2000 : false;
    },
  });
  const chat = query.data;
  const last = chat?.runs.at(-1);
  const busy = !!last && (!isTerminal(last.status) || last.status === 'unknown');
  const uploading = uploads.some((item) => !item.file && !item.error);
  const previewFocus = useRef<HTMLElement | null>(null);
  const previewPanel = useRef<HTMLElement>(null);
  const openPreview = (artifact: Artifact) => {
    previewFocus.current = document.activeElement as HTMLElement;
    setPreview(artifact);
  };
  const closePreview = () => {
    setPreview(null);
    requestAnimationFrame(() => previewFocus.current?.focus());
  };
  useEffect(() => {
    if (!preview) return;
    previewPanel.current?.querySelector<HTMLButtonElement>('button')?.focus();
    const keydown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setPreview(null);
        requestAnimationFrame(() => previewFocus.current?.focus());
      }
      if (event.key === 'Tab' && innerWidth < 768) {
        const targets = previewPanel.current?.querySelectorAll<HTMLElement>(
          'button:not(:disabled),a[href],summary,input,select,textarea',
        );
        if (!targets?.length) return;
        const first = targets[0],
          last = targets[targets.length - 1];
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault();
          last?.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          first?.focus();
        }
      }
    };
    document.addEventListener('keydown', keydown);
    return () => document.removeEventListener('keydown', keydown);
  }, [preview]);
  useEffect(() => {
    if (chat?.projectId && projectId !== chat.projectId) selectProject(chat.projectId);
  }, [chat?.projectId, projectId, selectProject]);
  useEffect(() => {
    end.current?.scrollIntoView({ block: 'end', behavior: 'instant' });
  }, [chat?.runs.length]);
  const send = useMutation({
    mutationFn: () => {
      const body = {
        content: content.trim(),
        artifactIds: uploads.flatMap((item) => (item.file ? [item.file.id] : [])),
      };
      return write<Run>(`/conversations/${id}/messages`, body, intent.current(body));
    },
    retry: false,
    onSuccess: (run) => {
      setContent('');
      setUploads([]);
      intent.current = requestKey();
      cache.setQueryData<ConversationDetail>(['platform', 'conversation', id], (value) =>
        value
          ? {
              ...value,
              latestRunId: run.id,
              runs: value.runs.some((item) => item.id === run.id)
                ? value.runs
                : [...value.runs, run],
            }
          : value,
      );
      void cache.invalidateQueries({ queryKey: ['platform', 'conversation'] });
      void cache.invalidateQueries({ queryKey: ['platform', 'conversations'] });
      textarea.current?.focus();
    },
  });
  const upload = async (files: FileList | null) => {
    if (!files || !chat || busy || send.isPending) return;
    for (const file of Array.from(files)) {
      const item = { name: file.name };
      setUploads((current) => [...current, item]);
      try {
        const uploaded = await uploadFile(chat.projectId, file, requestKey());
        setUploads((current) =>
          current.map((entry) => (entry === item ? { name: file.name, file: uploaded } : entry)),
        );
      } catch (error) {
        setUploads((current) =>
          current.map((entry) =>
            entry === item ? { name: file.name, error: (error as Error).message } : entry,
          ),
        );
      }
    }
    if (input.current) input.current.value = '';
  };
  return (
    <QueryState pending={query.isPending} error={query.error} retry={query.refetch}>
      {chat && (
        <div className={`wb-chat-room ${preview ? 'has-preview' : ''}`}>
          <div className="wb-chat-room-main">
            <header className="wb-chat-room-header">
              <div>
                <span className="wb-chat-eyebrow">CONVERSATION</span>
                <h1>{chat.title}</h1>
                <p>
                  <Icon name="terminal" size={14} />
                  {chat.resourceProfile || '平台沙箱'} · {chat.harness.name} · {chat.model}
                </p>
              </div>
              <button className="wb-button wb-button--secondary" onClick={() => navigate('/chat')}>
                <Icon name="plus" size={16} />
                新对话
              </button>
            </header>
            <div className="wb-chat-messages" role="log" aria-label="对话消息" aria-live="off">
              {!chat.runs.length && (
                <div className="wb-chat-welcome">
                  <span className="wb-chat-welcome-icon">
                    <Icon name="agents" size={28} />
                  </span>
                  <h2>沙箱配置已就绪</h2>
                  <p>告诉我你想完成什么。可以先上传文件，也可以从一个问题开始。</p>
                  <div className="wb-chat-prompts">
                    {[
                      '分析上传的数据，提炼值得关注的发现',
                      '帮我整理需求，然后一起逐步完善',
                      '生成一个可运行的小项目，并提供文件',
                    ].map((text) => (
                      <button
                        key={text}
                        onClick={() => {
                          setContent(text);
                          textarea.current?.focus();
                        }}
                      >
                        {text}
                        <Icon name="chevronRight" size={14} />
                      </button>
                    ))}
                  </div>
                </div>
              )}
              {chat.runs.map((run) => (
                <ChatTurn key={run.id} run={run} onPreview={openPreview} />
              ))}
              <div ref={end} />
            </div>
            <form
              className="wb-chat-composer-wrap"
              onSubmit={(event) => {
                event.preventDefault();
                if (
                  content.trim() &&
                  !busy &&
                  !uploading &&
                  !uploads.some((item) => item.error) &&
                  !send.isPending
                )
                  send.mutate();
              }}
            >
              <MutationError error={send.error} />
              <div className="wb-chat-composer">
                {!!uploads.length && (
                  <div className="wb-chat-attachments">
                    {uploads.map((item, index) => (
                      <span key={index} className={`wb-chat-file ${item.error ? 'has-error' : ''}`}>
                        <Icon name="file" size={15} />
                        <span>
                          {item.name}
                          <small>{item.error ?? (item.file ? '已上传' : '正在上传…')}</small>
                        </span>
                        <button
                          type="button"
                          aria-label={`移除附件 ${item.name}`}
                          disabled={send.isPending || (!item.file && !item.error)}
                          onClick={() =>
                            setUploads((current) =>
                              current.filter((_, itemIndex) => itemIndex !== index),
                            )
                          }
                        >
                          <Icon name="close" size={14} />
                        </button>
                      </span>
                    ))}
                  </div>
                )}
                <textarea
                  ref={textarea}
                  aria-label="输入消息"
                  placeholder={
                    busy
                      ? last?.status === 'unknown'
                        ? '上一轮结果待核对，请等待处理'
                        : 'Agent 正在处理，完成后可以继续对话'
                      : '描述你的目标，或继续追问…'
                  }
                  value={content}
                  onChange={(event) => setContent(event.target.value)}
                  maxLength={8000}
                  rows={3}
                  disabled={busy || send.isPending}
                  onKeyDown={(event) => {
                    if (
                      event.key === 'Enter' &&
                      !event.shiftKey &&
                      !event.nativeEvent.isComposing
                    ) {
                      event.preventDefault();
                      event.currentTarget.form?.requestSubmit();
                    }
                  }}
                />
                <div className="wb-chat-composer-tools">
                  <div>
                    <input
                      ref={input}
                      type="file"
                      multiple
                      hidden
                      aria-label="上传对话文件"
                      onChange={(event) => void upload(event.target.files)}
                    />
                    <button
                      type="button"
                      className="wb-icon-button"
                      aria-label="添加文件"
                      title="添加文件"
                      disabled={busy || send.isPending}
                      onClick={() => input.current?.click()}
                    >
                      <Icon name="plus" size={20} />
                    </button>
                    <span>Enter 发送 · Shift + Enter 换行</span>
                  </div>
                  <button
                    className="wb-chat-send"
                    aria-label="发送消息"
                    disabled={
                      !content.trim() ||
                      busy ||
                      uploading ||
                      uploads.some((item) => item.error) ||
                      send.isPending
                    }
                  >
                    <Icon name="chevronRight" size={20} />
                  </button>
                </div>
              </div>
              <p className="wb-chat-composer-note">
                每轮上限 {formatMoney(chat.limits.maxCostMicros, chat.limits.currency)}{' '}
                {chat.limits.currency}，发送即预占。仅运行时启动沙箱；聊天历史与工作文件会继续保留。
              </p>
            </form>
          </div>
          {preview && (
            <aside ref={previewPanel} className="wb-chat-preview" aria-label="产物预览">
              <header>
                <div>
                  <small>ARTIFACT</small>
                  <h2>{preview.name}</h2>
                </div>
                <button className="wb-icon-button" aria-label="关闭产物预览" onClick={closePreview}>
                  <Icon name="close" size={18} />
                </button>
              </header>
              <div className="wb-chat-preview-body">
                <ArtifactPreview key={preview.id} artifact={preview} />
              </div>
            </aside>
          )}
        </div>
      )}
    </QueryState>
  );
}

export function ChatPage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const { projectId, readiness } = usePlatform();
  const history = useQuery({
    queryKey: ['platform', 'conversations', projectId],
    queryFn: ({ signal }) => all<Conversation>('/conversations', { projectId }, signal),
    enabled: !!projectId,
    retry: false,
  });
  if (readiness?.capabilities.modules?.chat === false) return <p>当前部署尚未开放对话。</p>;
  return (
    <RequireProject>
      <div className="wb-chat-page">
        <nav className="wb-chat-history" aria-label="对话历史">
          <button className="wb-chat-new" onClick={() => navigate('/chat')}>
            <Icon name="plus" size={16} />
            新建对话
          </button>
          {history.data?.map((chat) => (
            <Link
              key={chat.id}
              to={`/chat/${chat.id}`}
              className={id === chat.id ? 'is-active' : ''}
              title={chat.title}
            >
              {chat.title}
            </Link>
          ))}
          {history.error && (
            <button className="wb-text-link" onClick={() => void history.refetch()}>
              重新加载历史
            </button>
          )}
        </nav>
        <div className="wb-chat-canvas">
          {id ? (
            <ConversationView key={id} id={id} />
          ) : (
            <SandboxStart
              key={projectId}
              onCreated={(chat) => {
                void history.refetch();
                navigate(`/chat/${chat.id}`);
              }}
            />
          )}
        </div>
      </div>
    </RequireProject>
  );
}
