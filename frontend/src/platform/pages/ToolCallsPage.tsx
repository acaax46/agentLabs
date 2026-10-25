import { useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { get, requestKey, uploadFile, write } from '../api';
import type { Artifact, StandaloneCall, Tool } from '../api';
import { usePlatform } from '../state';
import { useList, usePage, MutationError, QueryState, RequireProject } from '../shared';
import { Field, Money, Pagination } from '../controls';
import {
  CodeBlock,
  EmptyState,
  Modal,
  PageHeader,
  SectionHeader,
  ToolCallBadge,
  billingBadge,
} from '../ui';
import { formatDateTime, formatUnitPrice } from '../model';
import { EventTimeline } from '../observability';
import { ArtifactList } from './ArtifactsPage';

const live = (call: StandaloneCall) =>
  ['planned', 'dispatched', 'waiting_approval'].includes(call.status);
export function ToolPlayground({ tool }: { tool: Tool }) {
  const { projectId, project, readiness } = usePlatform();
  const cache = useQueryClient();
  const navigate = useNavigate();
  const sample =
    tool.sample && typeof tool.sample === 'object' && 'input' in tool.sample
      ? tool.sample.input
      : tool.sample;
  const [input, setInput] = useState(JSON.stringify(sample ?? {}, null, 2));
  const [selected, setSelected] = useState<string[]>([]);
  const [confirmed, setConfirmed] = useState(false);
  const files = useList<Artifact>('/artifacts', { kind: 'input' });
  const key = useRef(requestKey());
  const uploadKey = useRef(requestKey());
  const available =
    files.data?.filter(
      (file) => file.status === 'ready' && new Date(file.expiresAt).getTime() > Date.now(),
    ) ?? [];
  const upload = useMutation({
    mutationFn: async (file: File) => {
      if (!readiness?.ready) throw new Error('服务能力尚未就绪，请稍后重试上传。');
      if (file.size > Math.min(readiness.capabilities.maxUploadBytes, 25 * 1024 * 1024))
        throw new Error('文件超过当前上传限制。');
      const result = await uploadFile(projectId, file, uploadKey.current);
      setSelected((current) => [...new Set([...current, result.id])]);
      await cache.invalidateQueries({ queryKey: ['platform', '/artifacts'] });
    },
    retry: false,
  });
  const reason = !project
    ? '请先选择项目。'
    : !project.budget.configured
      ? '请先配置项目预算。'
      : project.status !== 'active'
        ? '项目已暂停。'
        : !readiness?.ready
          ? '平台服务未就绪。'
          : tool.requiresApproval
            ? '此工具需要审批，当前工作区尚未开放审批操作。'
            : tool.name === 'code.execute' && !readiness.capabilities.codeExecution
              ? (readiness.capabilities.codeExecutionReason ?? '代码执行未开放。')
              : !tool.prices.some(
                    (price) =>
                      price.currency === project.currency &&
                      price.unit === (tool.name === 'code.execute' ? 'second' : 'file'),
                  )
                ? '当前项目币种没有可用工具价格。'
                : '';
  const submit = useMutation({
    mutationFn: async () => {
      if (reason || !confirmed) throw new Error(reason || '请确认费用与副作用。');
      let value: unknown;
      try {
        value = JSON.parse(input);
      } catch {
        throw new Error('工具输入必须是有效 JSON。');
      }
      if (!value || typeof value !== 'object' || Array.isArray(value))
        throw new Error('工具输入必须是 JSON 对象。');
      if (selected.length > 10) throw new Error('最多引用 10 个文件。');
      const body = {
        projectId,
        version: tool.version,
        artifactIds: selected.filter((id) => available.some((file) => file.id === id)),
        input: value,
      };
      return write<StandaloneCall>(
        `/tools/${encodeURIComponent(tool.name)}/calls`,
        body,
        key.current(body),
      );
    },
    retry: false,
    onSuccess: (call) => {
      void cache.invalidateQueries({ queryKey: ['platform'] });
      navigate(`/tool-calls/${call.id}`);
    },
  });
  return (
    <section className="wb-panel">
      <SectionHeader
        title="独立调用调试"
        description={`固定工具版本 ${tool.version}。会预占预算并按工具计量收费，成功响应可能仍在排队。`}
      />
      <form
        className="wb-form"
        onSubmit={(event) => {
          event.preventDefault();
          submit.mutate();
        }}
      >
        <p className="wb-callout">
          当前项目计价：
          {tool.prices
            .filter(
              (price) =>
                price.currency === project?.currency &&
                price.unit === (tool.name === 'code.execute' ? 'second' : 'file'),
            )
            .map(
              (price) =>
                `${formatUnitPrice(price.perUnitMicros, price.currency)} ${price.currency} / ${price.unit}（价格 ${price.id}）`,
            )
            .join('；') || '暂无可用价格'}
          。
          {tool.name === 'code.execute'
            ? '预占按输入超时秒数加 31 秒计算，未填写超时默认 60 秒，允许 1–120 秒；按实际沙箱秒数结算。'
            : '按 input.inputs 中的输入文件数量预占和计量；一次调用最多 16 个输入文件。'}
          最终预占、价格快照与结算以服务端记录为准。
        </p>
        <fieldset className="wb-fieldset" disabled={submit.isPending || upload.isPending}>
          <Field label="工具输入 JSON" name="input" required error={submit.error}>
            <textarea
              rows={9}
              value={input}
              maxLength={300000}
              onChange={(event) => setInput(event.target.value)}
              className="wb-mono"
            />
          </Field>
          <label className="wb-field">
            <span>上传工具输入文件</span>
            <input
              type="file"
              disabled={!projectId || !readiness?.ready}
              accept=".csv,.tsv,.json,.txt,.md,.xlsx"
              onChange={(event) => {
                const file = event.target.files?.[0];
                if (file) upload.mutate(file);
                event.target.value = '';
              }}
            />
          </label>
          <MutationError error={upload.error} />
          <QueryState
            pending={files.isPending && !!projectId}
            error={files.error}
            retry={files.refetch}
          >
            <fieldset className="wb-fieldset">
              <legend>允许工具读取的文件（最近 100 个可用输入）</legend>
              {available.map((file) => (
                <label className="wb-check" key={file.id}>
                  <input
                    type="checkbox"
                    checked={selected.includes(file.id)}
                    onChange={(event) =>
                      setSelected((current) =>
                        event.target.checked
                          ? [...current, file.id]
                          : current.filter((id) => id !== file.id),
                      )
                    }
                  />
                  <span>{file.name}</span>
                </label>
              ))}
              {!available.length && (
                <p className="wb-muted">尚无输入文件；JSON 内按文件名引用已选择的材料。</p>
              )}
            </fieldset>
          </QueryState>
          <label className="wb-check">
            <input
              required
              type="checkbox"
              checked={confirmed}
              onChange={(event) => setConfirmed(event.target.checked)}
            />
            <span>
              我确认按已展示的工具价格与资源上限预占和计量。{tool.sideEffects}{' '}
              停止不能撤回已经发生的副作用。
            </span>
          </label>
        </fieldset>
        <MutationError error={submit.error} />
        {reason && <p className="wb-field-error">{reason}</p>}
        <button
          className="wb-button wb-button--primary"
          disabled={!!reason || !confirmed || submit.isPending || upload.isPending}
        >
          {submit.isPending ? '正在提交…' : '提交工具调用'}
        </button>
      </form>
    </section>
  );
}
export function ToolCallsPage() {
  const query = usePage<StandaloneCall>('/tool-calls', {}, true, true);
  return (
    <>
      <PageHeader
        title="工具调用"
        description="当前项目的独立工具调用历史；执行状态与结算状态分开展示。"
        actions={
          <Link className="wb-button wb-button--primary" to="/tools">
            调试工具
          </Link>
        }
      />
      <RequireProject>
        <section className="wb-panel">
          <QueryState pending={query.isPending} error={query.error} retry={query.refetch}>
            {query.data?.items.length ? (
              <div className="wb-table-wrap">
                <table className="wb-table">
                  <thead>
                    <tr>
                      <th>ToolCall</th>
                      <th>工具 / 版本</th>
                      <th>状态</th>
                      <th>费用</th>
                      <th>时间</th>
                    </tr>
                  </thead>
                  <tbody>
                    {query.data.items.map((call) => (
                      <tr key={call.id}>
                        <td>
                          <Link className="wb-table-link wb-mono" to={`/tool-calls/${call.id}`}>
                            {call.id}
                          </Link>
                        </td>
                        <td>
                          {call.name}@{call.version}
                        </td>
                        <td>
                          <ToolCallBadge status={call.status} />
                        </td>
                        <td>
                          <Money value={call.costMicros} currency={call.currency ?? 'CNY'} />
                          {billingBadge(call.billingStatus)}
                        </td>
                        <td>{formatDateTime(call.createdAt)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <EmptyState title="暂无独立工具调用" description="从工具目录选择固定版本进行调试。" />
            )}
          </QueryState>
          <Pagination
            nextCursor={query.data?.nextCursor}
            page={query.page}
            pending={query.isFetching}
            onNext={query.next}
            onPrevious={query.previous}
          />
        </section>
      </RequireProject>
    </>
  );
}
export function ToolCallDetailPage() {
  const { id = '' } = useParams();
  const cache = useQueryClient();
  const [confirm, setConfirm] = useState(false);
  const [stopRequested, setStopRequested] = useState(false);
  const query = useQuery({
    queryKey: ['platform', 'standalone-call', id],
    queryFn: ({ signal }) =>
      get<StandaloneCall>(`/tool-calls/${encodeURIComponent(id)}`, {}, signal),
    retry: false,
    refetchInterval: (query) => (query.state.data && live(query.state.data) ? 2000 : false),
  });
  const cancel = useMutation({
    mutationFn: () => write<StandaloneCall>(`/tool-calls/${encodeURIComponent(id)}/cancel`, {}),
    retry: false,
    onSuccess: (call) => {
      cache.setQueryData(['platform', 'standalone-call', id], call);
      setConfirm(false);
      setStopRequested(true);
    },
  });
  const call = query.data;
  return (
    <QueryState pending={query.isPending} error={query.error} retry={query.refetch}>
      {call && (
        <>
          <PageHeader
            title={`${call.name}@${call.version}`}
            description={call.inputSummary}
            meta={
              <>
                <span className="wb-mono">{call.id}</span>
                <ToolCallBadge status={call.status} />
                {billingBadge(call.billingStatus)}
              </>
            }
            actions={
              live(call) && (
                <button
                  className="wb-button wb-button--danger"
                  disabled={cancel.isPending || stopRequested}
                  onClick={() => setConfirm(true)}
                >
                  {stopRequested ? '正在停止工具调用…' : '停止工具调用'}
                </button>
              )
            }
          />
          <p aria-live="polite" role="status">
            工具状态：{call.status}
          </p>
          {call.error && (
            <div className="wb-callout wb-callout--warning" role="alert">
              <div>
                <strong>{call.error.code}</strong>
                <p>{call.error.message}</p>
                <p>{call.error.nextStep}</p>
              </div>
            </div>
          )}
          {call.status === 'unknown' && (
            <p className="wb-field-error">结果待核对，不能按普通失败重试或承诺退款。</p>
          )}
          <div className="wb-detail-grid">
            <div className="wb-detail-main">
              <EventTimeline resource="tool-calls" id={id} active={live(call)} />
              <section className="wb-panel">
                <SectionHeader title="工具结果" />
                <CodeBlock copy>
                  {JSON.stringify(call.result, null, 2) ?? call.outputSummary}
                </CodeBlock>
              </section>
              <section className="wb-panel">
                <SectionHeader title="工具产物" />
                <ArtifactList
                  projectId={call.projectId}
                  runId={call.runId || call.id}
                  active={live(call)}
                />
              </section>
            </div>
            <aside className="wb-detail-side">
              <section className="wb-panel">
                <SectionHeader title="调用与计量" />
                <p>
                  {call.unit} · {call.quantity}
                </p>
                <Money value={call.costMicros} currency={call.currency ?? 'CNY'} />
                <p>{call.sandbox || '未使用代码沙箱'}</p>
                <p>{formatDateTime(call.createdAt)}</p>
              </section>
            </aside>
          </div>
          <Modal
            open={confirm}
            onClose={() => {
              if (!cancel.isPending) setConfirm(false);
            }}
            title={`停止工具调用 ${id}？`}
            description="仅停止后续执行。已派发动作、实际用量与未知结果仍需核对。"
          >
            <MutationError error={cancel.error} />
            <div className="wb-form-actions">
              <button
                className="wb-button wb-button--danger"
                disabled={cancel.isPending}
                onClick={() => cancel.mutate()}
              >
                确认停止工具调用
              </button>
              <button className="wb-button wb-button--secondary" onClick={() => setConfirm(false)}>
                继续执行
              </button>
            </div>
          </Modal>
        </>
      )}
    </QueryState>
  );
}
