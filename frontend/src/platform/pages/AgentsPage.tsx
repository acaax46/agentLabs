import { useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { get, requestKey, uploadFile, write } from '../api';
import type { Agent, AgentVersion, Artifact, ModelOption, Run, RateSheet } from '../api';
import { usePlatform } from '../state';
import { decimalToMicros, formatBytes, formatUnitPrice, modelSourceLabels } from '../model';
import { ModelPrice } from '../pricing';
import { ApiExamples } from '../ApiExamples';
import { MutationError, QueryState, RequireProject, useList } from '../shared';
import { CodeBlock, EmptyState, PageHeader, SectionHeader, StatusBadge } from '../ui';
import { Icon } from '../ui';
import { Field } from '../controls';

export function AgentsPage() {
  const { projectId } = usePlatform();
  const query = useQuery({
    queryKey: ['platform', 'agents'],
    queryFn: ({ signal }) => get<{ items: Agent[] }>('/agents', {}, signal),
    retry: false,
  });
  return (
    <>
      <PageHeader
        title="Agent"
        description="选择平台预设的不可变版本，配置模型和文件后提交运行。当前目录包含 8 个预设 Agent；用户自定义 Agent 的草稿与发布尚未开放。"
      />
      <QueryState pending={query.isPending} error={query.error} retry={query.refetch}>
        <div className="wb-callout wb-callout--info">
          <Icon name="layers" size={18} />
          <div>
            <strong>当前提供平台预设 Agent</strong>
            <p>
              每个版本都固定提示词摘要、工具版本、输入格式和运行限制。代码生成与工作台搭建 Agent
              会交付源码、说明和 ZIP；ZIP 需要用户自行部署。自定义 Agent
              需要草稿、发布、权限审批和版本回滚链路，当前尚未开放。
            </p>
          </div>
        </div>
        <section className="wb-panel">
          {!query.data?.items.length ? (
            <EmptyState title="暂无已发布 Agent" description="当前服务没有可运行的发布版本。" />
          ) : (
            <div className="wb-table-wrap">
              <table className="wb-table">
                <thead>
                  <tr>
                    <th>Agent</th>
                    <th>发布版本</th>
                    <th>工具</th>
                    <th>输入格式</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {query.data.items
                    .filter((agent) => !agent.projectId || agent.projectId === projectId)
                    .map((agent) => (
                      <tr key={agent.id}>
                        <td>
                          <span className="wb-cell-stack">
                            <strong>{agent.name}</strong>
                            <small>{agent.description}</small>
                          </span>
                        </td>
                        <td className="wb-mono">{agent.latestVersion?.version ?? '版本未知'}</td>
                        <td>
                          {agent.latestVersion?.tools
                            .map((tool) => `${tool.name}@${tool.version}`)
                            .join(' · ')}
                        </td>
                        <td>{agent.latestVersion?.inputFormats.join(' / ')}</td>
                        <td>
                          <Link
                            className="wb-button wb-button--secondary"
                            to={`/agents/${agent.id}`}
                          >
                            Playground
                          </Link>
                        </td>
                      </tr>
                    ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
      </QueryState>
    </>
  );
}

const protocolLabels: Record<string, string> = {
  chat: 'Chat Completions',
  responses: 'OpenAI Responses',
  messages: 'Anthropic Messages',
};

export function AgentPlaygroundPage() {
  const { id = '' } = useParams();
  const platform = usePlatform();
  const cache = useQueryClient();
  const navigate = useNavigate();
  const detail = useQuery({
    queryKey: ['platform', 'agent', id],
    queryFn: ({ signal }) =>
      get<{ agent: Agent; versions: AgentVersion[] }>(
        `/agents/${encodeURIComponent(id)}`,
        {},
        signal,
      ),
    retry: false,
  });
  const [versionId, setVersionId] = useState('');
  const versions = detail.data?.versions ?? [];
  const version =
    versions.find((item) => item.id === versionId) ??
    versions.find((item) => item.id === detail.data?.agent.latestVersionId) ??
    versions[0];
  const models = useQuery({
    queryKey: ['platform', 'models', platform.projectId],
    queryFn: ({ signal }) =>
      get<{ items: ModelOption[] }>('/models', { projectId: platform.projectId }, signal),
    enabled: !!platform.projectId,
    retry: false,
  });
  const artifacts = useList<Artifact>('/artifacts', { kind: 'input' });
  const rates = useQuery({
    queryKey: ['platform', 'rates', platform.projectId],
    queryFn: ({ signal }) =>
      get<RateSheet>('/billing/rates', { projectId: platform.projectId }, signal),
    enabled: !!platform.projectId,
    retry: false,
  });
  const [resourceId, setResourceId] = useState('');
  // A version that lists profiles may use only those, in its order; any other version runs on CPU only.
  const policy = version?.resourceProfiles ?? [];
  const gpuOnly = policy.length > 0;
  const allowedResources = (rates.data?.resources ?? [])
    .filter((item) => (gpuOnly ? policy.includes(item.id) : !item.gpu))
    .sort((a, b) => policy.indexOf(a.id) - policy.indexOf(b.id));
  const resource =
    allowedResources.find((item) => item.id === resourceId) ??
    allowedResources.find((item) => item.available);
  const needsResource = rates.data?.execution?.sandboxBilling === 'separate';
  const [modelId, setModelId] = useState('');
  const options = models.data?.items ?? [];
  const modelKey = (option: ModelOption) => `${option.connectionId}\n${option.model}`;
  const model =
    options.find((option) => modelKey(option) === modelId) ??
    options.find((option) => option.usable);
  const [instruction, setInstruction] = useState('');
  const [selected, setSelected] = useState<string[]>([]);
  const [amount, setAmount] = useState('2');
  const [seconds, setSeconds] = useState('');
  const [toolLimit, setToolLimit] = useState('');
  const [uploads, setUploads] = useState<{ name: string; status: string; error?: string }[]>([]);
  const [batchBusy, setBatchBusy] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const key = useRef(requestKey());
  const uploadKey = useRef(requestKey());
  const readyFiles =
    artifacts.data?.filter(
      (file) =>
        file.status === 'ready' &&
        version?.inputFormats.includes(file.type) &&
        new Date(file.expiresAt).getTime() > Date.now(),
    ) ?? [];
  const selectedFiles = readyFiles.filter((file) => selected.includes(file.id));
  const codeRequired = version?.tools.some((tool) => tool.name === 'code.execute');
  // A version that lists code-agent CLIs lets the run choose one; any other runs the builtin loop.
  const [harnessId, setHarnessId] = useState('');
  const engines = platform.readiness?.capabilities.harnesses ?? [];
  const versionEngines = version?.harnesses ?? [];
  const engineOf = (id: string) => engines.find((item) => item.id === id);
  const harness =
    versionEngines.find((id) => id === harnessId) ??
    versionEngines.find((id) => engineOf(id)?.available) ??
    versionEngines[0];
  const engine = harness ? engineOf(harness) : undefined;
  const builtinEngine = engineOf('builtin');
  const engineReason = !harness
    ? ''
    : !platform.readiness?.capabilities.codeExecution
      ? (platform.readiness?.capabilities.codeExecutionReason ?? '代码执行不可用。')
      : !engine?.available
        ? (engine?.reason ?? '所选执行引擎在此部署不可用。')
        : '';
  const disabledReason = !platform.project
    ? '请先创建项目。'
    : platform.project.status !== 'active'
      ? '项目已暂停。'
      : !platform.project.budget.configured
        ? '请先配置项目预算。'
        : !platform.readiness?.capabilities.runs
          ? '运行服务尚未就绪或能力未知。'
          : codeRequired && !platform.readiness.capabilities.codeExecution
            ? (platform.readiness.capabilities.codeExecutionReason ?? '代码执行不可用。')
            : !model?.usable
              ? '请先为项目授权可用的模型连接。'
              : !version
                ? '没有可运行的发布版本。'
                : rates.isPending || rates.isError
                  ? '资源价格尚未读取成功，请刷新重试。'
                  : gpuOnly && !needsResource
                    ? '此版本只能使用按资源计费的 GPU 规格；需管理员先发布资源另列的托管价格。'
                    : needsResource && !resource?.available
                      ? gpuOnly
                        ? (allowedResources.find((item) => item.reason)?.reason ??
                          '当前没有可用的 GPU 规格。')
                        : '当前没有可用的已定价沙箱规格。'
                      : engineReason;
  const upload = useMutation({
    mutationFn: async (file: File) => {
      setUploads((current) => [
        ...current.filter((item) => item.name !== file.name),
        { name: file.name, status: '校验并上传中' },
      ]);
      if (!version?.inputFormats.includes(file.name.split('.').pop()?.toLowerCase() ?? ''))
        throw new Error('文件格式不在当前发布版本允许范围内。');
      const max = Math.min(
        version.limits.maxInputBytes,
        platform.readiness?.capabilities.maxUploadBytes ?? 0,
      );
      if (file.size <= 0 || file.size > max)
        throw new Error(`文件大小须在 1 B 到 ${formatBytes(max)} 之间。`);
      const artifact = await uploadFile(platform.projectId, file, uploadKey.current);
      setUploads((current) =>
        current.map((item) =>
          item.name === file.name ? { ...item, status: '服务端摘要校验通过' } : item,
        ),
      );
      setSelected((current) =>
        current.includes(artifact.id) ? current : [...current, artifact.id],
      );
      await cache.invalidateQueries({ queryKey: ['platform', '/artifacts'] });
    },
    retry: false,
    onError: (error, file) =>
      setUploads((current) =>
        current.map((item) =>
          item.name === file.name ? { ...item, status: '失败', error: error.message } : item,
        ),
      ),
  });
  const uploadBatch = async (files: File[]) => {
    if (batchBusy || upload.isPending) return;
    setBatchBusy(true);
    try {
      for (const file of files) {
        try {
          await upload.mutateAsync(file);
        } catch {
          /* per-file error remains visible */
        }
      }
    } finally {
      setBatchBusy(false);
    }
  };
  const submit = useMutation({
    mutationFn: async () => {
      if (disabledReason) throw new Error(disabledReason);
      if (!instruction.trim() || !confirmed) throw new Error('请填写指令并确认费用影响。');
      const maxCostMicros = decimalToMicros(amount);
      if (BigInt(maxCostMicros) <= 0n) throw new Error('费用上限必须大于 0。');
      const maxSeconds = Number(seconds || version!.limits.defaultExecutionSeconds);
      const maxToolCalls = Number(toolLimit || version!.limits.defaultToolCalls);
      if (
        !Number.isInteger(maxToolCalls) ||
        maxToolCalls < 1 ||
        maxToolCalls > version!.limits.maxToolCalls
      )
        throw new Error('工具次数超出发布版本允许范围。');
      if (
        !Number.isInteger(maxSeconds) ||
        maxSeconds < 1 ||
        maxSeconds > version!.limits.maxExecutionSeconds
      )
        throw new Error('执行时限超出发布版本范围。');
      if (
        selectedFiles.length > version!.limits.maxInputFiles ||
        selectedFiles.reduce((sum, file) => sum + file.sizeBytes, 0) >
          Math.min(version!.limits.maxInputBytes, platform.readiness!.capabilities.maxRunInputBytes)
      )
        throw new Error('所选输入文件数量或总大小超出发布版本限制。');
      const input = {
        projectId: platform.projectId,
        agentVersionId: version!.id,
        modelConnectionId: model!.connectionId,
        model: model!.model,
        ...(needsResource && resource ? { resourceProfile: resource.id } : {}),
        ...(harness ? { harness } : {}),
        input: {
          instruction: instruction.trim(),
          artifactIds: selectedFiles.map((file) => file.id),
        },
        limits: {
          maxCostMicros,
          currency: platform.project!.currency,
          maxExecutionSeconds: maxSeconds,
          maxToolCalls,
        },
      };
      return write<Run>('/runs', input, key.current(input));
    },
    retry: false,
    onSuccess: (run) => {
      platform.refresh();
      navigate(`/runs/${run.id}`);
    },
  });
  const currency = platform.project?.currency ?? 'CNY';
  const sample = {
    projectId: platform.projectId,
    agentVersionId: version?.id,
    modelConnectionId: model?.connectionId,
    model: model?.model,
    ...(needsResource && resource ? { resourceProfile: resource.id } : {}),
    ...(harness ? { harness } : {}),
    input: {
      instruction: instruction || '分析输入文件并输出结果',
      artifactIds: selectedFiles.map((file) => file.id),
    },
    limits: {
      maxCostMicros: /^[0-9]+(?:\.[0-9]{1,6})?$/.test(amount) ? decimalToMicros(amount) : '2000000',
      currency,
      maxExecutionSeconds: Number(seconds || version?.limits.defaultExecutionSeconds || 120),
      maxToolCalls: Number(toolLimit || version?.limits.defaultToolCalls || 1),
    },
  };
  return (
    <RequireProject>
      <QueryState pending={detail.isPending} error={detail.error} retry={detail.refetch}>
        {detail.data && (
          <>
            <PageHeader
              title={detail.data.agent.name}
              description={detail.data.agent.description}
              meta={<StatusBadge tone="accent">已发布版本</StatusBadge>}
            />
            <div className="wb-playground">
              <section className="wb-panel wb-playground-main">
                <SectionHeader
                  title="试运行"
                  description="提交后会预占项目预算，运行产生实际模型、工具和执行用量。"
                />
                <form
                  className="wb-form"
                  onSubmit={(event) => {
                    event.preventDefault();
                    submit.mutate();
                  }}
                >
                  <fieldset
                    disabled={submit.isPending || upload.isPending || batchBusy}
                    className="wb-fieldset"
                  >
                    <label className="wb-field">
                      <span>发布版本</span>
                      <select
                        value={version?.id ?? ''}
                        onChange={(event) => {
                          setVersionId(event.target.value);
                          setSeconds('');
                          setSelected([]);
                          setResourceId('');
                          setHarnessId('');
                        }}
                      >
                        {versions.map((item) => (
                          <option key={item.id} value={item.id}>
                            {item.version}
                          </option>
                        ))}
                      </select>
                    </label>
                    <label className="wb-field">
                      <span>执行引擎</span>
                      {harness ? (
                        <select
                          value={harness}
                          aria-describedby="engine-help"
                          onChange={(event) => setHarnessId(event.target.value)}
                        >
                          {versionEngines.map((id) => {
                            const item = engineOf(id);
                            return (
                              <option key={id} value={id} disabled={!item?.available}>
                                {item ? `${item.name} ${item.version}` : id} ·{' '}
                                {protocolLabels[item?.protocol ?? ''] ?? '协议未知'}
                                {item && !item.available ? `（${item.reason}）` : ''}
                              </option>
                            );
                          })}
                        </select>
                      ) : (
                        <select value="builtin" disabled aria-describedby="engine-help">
                          <option value="builtin">
                            {builtinEngine?.name ?? 'AgentLab 内置循环'}（此版本固定）
                          </option>
                        </select>
                      )}
                      <small id="engine-help">
                        {harness ? (
                          <>
                            {engine?.description}
                            CLI
                            在无网络的沙箱内运行，模型请求只经平台计量通道：平台固定所选模型、限制输出并记录供应商用量，沙箱内没有真实密钥。引擎在沙箱内的工具步骤由
                            CLI 自报，显示在事件时间线。
                          </>
                        ) : (
                          <>
                            此版本固定由平台在沙箱外驱动模型，工具调用逐次记录。可选
                            pi、OpenCode、Codex CLI 或 Claude Code 的版本见{' '}
                            <Link className="wb-text-link" to="/agents/agt_code_agent">
                              代码 Agent（可选引擎）
                            </Link>
                            。
                          </>
                        )}
                      </small>
                    </label>
                    <QueryState
                      pending={models.isPending}
                      error={models.error}
                      retry={models.refetch}
                    >
                      <label className="wb-field">
                        <span>模型连接</span>
                        <select
                          value={model ? modelKey(model) : ''}
                          onChange={(event) => setModelId(event.target.value)}
                        >
                          <option value="">选择可用模型</option>
                          {options.map((option) => (
                            <option
                              key={modelKey(option)}
                              value={modelKey(option)}
                              disabled={!option.usable}
                            >
                              {option.connectionName} / {option.model} ·{' '}
                              {modelSourceLabels[option.source]}
                              {' · '}
                              {option.status}
                              {option.reason ? `（${option.reason}）` : ''}
                            </option>
                          ))}
                        </select>
                      </label>
                      <p className="wb-field-help">
                        工具调用能力：
                        {model?.toolCalling === 'unverified'
                          ? '尚未通过推理验证；模型列表验证仅确认授权。'
                          : (model?.toolCalling ?? '未知')}
                        。模型来源和连接范围由后端校验。
                      </p>
                      {!options.length && (
                        <Link className="wb-text-link" to={`/projects/${platform.projectId}`}>
                          为项目授权连接
                        </Link>
                      )}
                    </QueryState>
                    {model?.source === 'platform' ? (
                      <ModelPrice price={model.price} />
                    ) : (
                      <p className="wb-muted">BYOK 模型费用由客户在供应商账户承担，平台不代收。</p>
                    )}
                    <QueryState pending={rates.isPending} error={rates.error} retry={rates.refetch}>
                      {needsResource ? (
                        <label className="wb-field">
                          <span>沙箱资源规格</span>
                          <select
                            value={resource?.id ?? ''}
                            onChange={(event) => setResourceId(event.target.value)}
                          >
                            {allowedResources.map((item) => (
                              <option key={item.id} value={item.id} disabled={!item.available}>
                                {item.name} · {item.cpu} vCPU / {item.memoryMB / 1024} GiB /{' '}
                                {item.gpu ? item.gpuType : '无 GPU'} ·{' '}
                                {item.price
                                  ? `${formatUnitPrice(item.price.perSecondMicros, currency)} ${currency}/秒`
                                  : '未定价'}
                                {!item.available ? `（${item.reason}）` : ''}
                              </option>
                            ))}
                          </select>
                          <small>
                            {gpuOnly
                              ? '此版本只能使用 GPU 规格：代码在 CUDA 镜像中运行，每次代码执行独占整卡，GPU 按卡型计价。'
                              : '此版本只使用不含 GPU 的规格。'}
                            {harness
                              ? '代码 Agent 会话期间容器一直占用所选规格：按容器从启动到移除的时长向上取整秒计费，含模型等待，不含排队。'
                              : '按每次隔离执行时长向上取整秒计费，含启动与回收；模型等待与排队不计沙箱资源费。'}
                          </small>
                        </label>
                      ) : (
                        <p className="wb-muted">
                          当前托管价格已包含算力。管理员发布“资源另列”的托管价格后可选择沙箱规格。
                        </p>
                      )}
                      <p className="wb-muted">
                        托管服务：
                        {rates.data?.execution
                          ? `${formatUnitPrice(rates.data.execution.perSecondMicros, currency)} ${currency}/运行秒`
                          : '未配置'}
                        ；含模型等待，不含排队。
                        <Link className="wb-text-link" to="/usage">
                          查看完整计费项
                        </Link>
                      </p>
                    </QueryState>
                    <Field label="任务指令" name="input.instruction" required error={submit.error}>
                      <textarea
                        required
                        rows={6}
                        maxLength={4000}
                        value={instruction}
                        onChange={(event) => setInstruction(event.target.value)}
                      />
                    </Field>
                    <div
                      className="wb-upload-drop"
                      onDragOver={(event) => event.preventDefault()}
                      onDrop={(event) => {
                        event.preventDefault();
                        void uploadBatch(Array.from(event.dataTransfer.files));
                      }}
                    >
                      <Icon name="upload" />
                      <label className="wb-field">
                        <span>上传输入文件</span>
                        <input
                          type="file"
                          multiple
                          accept={version?.inputFormats.map((format) => `.${format}`).join(',')}
                          disabled={!platform.readiness?.ready}
                          onChange={(event) => {
                            const files = Array.from(event.target.files ?? []);
                            void uploadBatch(files);
                            event.target.value = '';
                          }}
                        />
                        <small>
                          拖放文件或使用选择按钮。允许格式：{version?.inputFormats.join(' / ')}。
                          最多 {version?.limits.maxInputFiles} 个文件，共{' '}
                          {formatBytes(version?.limits.maxInputBytes ?? 0)}。文件先校验摘要后上传。
                        </small>
                      </label>
                    </div>
                    <ul className="wb-upload-feedback" aria-live="polite">
                      {uploads.map((item) => (
                        <li key={item.name}>
                          <span>
                            {item.name} · {item.status}
                          </span>
                          {item.status === '校验并上传中' && (
                            <progress aria-label={`${item.name} 上传进度`} />
                          )}
                          {item.error && <p className="wb-field-error">{item.error}</p>}
                        </li>
                      ))}
                    </ul>
                    <MutationError error={upload.error} />
                    <QueryState
                      pending={artifacts.isPending}
                      error={artifacts.error}
                      retry={artifacts.refetch}
                    >
                      <div className="wb-check-list">
                        {readyFiles.map((file) => (
                          <label className="wb-check" key={file.id}>
                            <input
                              type="checkbox"
                              checked={selected.includes(file.id)}
                              onChange={(event) =>
                                setSelected((current) =>
                                  event.target.checked
                                    ? [...current, file.id]
                                    : current.filter((item) => item !== file.id),
                                )
                              }
                            />
                            <span>
                              {file.name} · {formatBytes(file.sizeBytes)}
                            </span>
                          </label>
                        ))}
                      </div>
                    </QueryState>
                    <div className="wb-form-grid">
                      <Field
                        label={`费用上限（${currency}）`}
                        name="limits.maxCostMicros"
                        required
                        error={submit.error}
                      >
                        <input
                          required
                          inputMode="decimal"
                          pattern="[0-9]+(\.[0-9]{1,6})?"
                          value={amount}
                          onChange={(event) => setAmount(event.target.value)}
                        />
                      </Field>
                      <Field
                        label="最长执行时间（秒）"
                        name="limits.maxExecutionSeconds"
                        required
                        error={submit.error}
                      >
                        <input
                          required
                          type="number"
                          min="1"
                          max={version?.limits.maxExecutionSeconds}
                          value={seconds || version?.limits.defaultExecutionSeconds || ''}
                          onChange={(event) => setSeconds(event.target.value)}
                        />
                      </Field>
                      <Field
                        label="工具调用次数"
                        name="limits.maxToolCalls"
                        required
                        error={submit.error}
                        help={`允许 1–${version?.limits.maxToolCalls} 次；执行时限允许 1–${version?.limits.maxExecutionSeconds} 秒。${harness ? '代码 Agent 引擎按模型请求次数限制，上限为该值 + 6。' : ''}`}
                      >
                        <input
                          type="number"
                          min="1"
                          max={version?.limits.maxToolCalls}
                          value={toolLimit || version?.limits.defaultToolCalls || ''}
                          onChange={(event) => setToolLimit(event.target.value)}
                        />
                      </Field>
                    </div>
                    <label className="wb-check">
                      <input
                        type="checkbox"
                        required
                        checked={confirmed}
                        onChange={(event) => setConfirmed(event.target.checked)}
                      />
                      <span>
                        我确认预占预算与费用影响。
                        {model?.source === 'byok'
                          ? 'BYOK 模型费用由客户承担，平台另计工具与执行用量。'
                          : '平台供模按冻结价格计量。'}
                        取消不能撤回已发生的外部副作用。
                      </span>
                    </label>
                  </fieldset>
                  {disabledReason && <p className="wb-field-error">{disabledReason}</p>}
                  <MutationError error={submit.error} />
                  <div className="wb-submit-bar">
                    <p className="wb-field-help">
                      {model?.source === 'byok'
                        ? '模型由客户付费，平台计工具与托管执行。'
                        : '平台按模型、工具与托管执行计量。'}
                      费用上限 {amount || '未填写'} {currency}。
                    </p>
                    <button
                      className="wb-button wb-button--primary"
                      disabled={
                        !!disabledReason ||
                        !confirmed ||
                        submit.isPending ||
                        upload.isPending ||
                        batchBusy ||
                        artifacts.isPending ||
                        artifacts.isError
                      }
                    >
                      {submit.isPending ? '正在提交…' : '提交试运行'}
                    </button>
                  </div>
                </form>
              </section>
              <aside className="wb-playground-side">
                <section className="wb-panel">
                  <SectionHeader title="固定发布清单" />
                  <Link className="wb-text-link" to={`/agents/${id}/versions`}>
                    查看版本与差异
                  </Link>
                  <CodeBlock label="AgentVersion">{JSON.stringify(version, null, 2)}</CodeBlock>
                </section>
                <section className="wb-panel">
                  <ApiExamples body={sample} />
                  <Link className="wb-text-link" to="/keys">
                    签发 API Key
                  </Link>
                </section>
              </aside>
            </div>
          </>
        )}
      </QueryState>
    </RequireProject>
  );
}
