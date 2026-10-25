import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { connections, canUseConnection } from '../connections/api';
import type { Connection } from '../connections/api';
import { PageHeading, Notice } from '../catalog/shared';
import { LazyCodeEditor } from '../editor/LazyCodeEditor';
import { startAgent, startEvaluation, getRun, getResources } from '../api/runtime';
import type { EvaluationRun, GPUResource, RuntimeRun } from '../api/runtime';

function Chain({ run }: { run: RuntimeRun }) {
  return (
    <section className="runtime-chain">
      <div className="runtime-run-head">
        <div>
          <span className="eyebrow">{run.kind === 'agent' ? 'AGENT 调度链' : '沙箱评测链'}</span>
          <h2>{run.id}</h2>
        </div>
        <strong className={`run-status ${run.status}`}>{run.status}</strong>
      </div>
      <p>
        资源：{run.resources.cpus} CPU · {run.resources.memoryMB}MB · GPU{' '}
        {run.resources.gpuCount || 0}
        {run.allocatedGpu?.length ? `（${run.allocatedGpu.map((g) => g.name).join('、')}）` : ''}
      </p>
      <ol className="runtime-steps">
        {run.steps.map((step) => (
          <li key={step.id} className={step.status}>
            <span className="step-dot" />
            <div>
              <strong>{step.label}</strong>
              <small>
                {step.provider || 'sandbox'} · {step.model || step.kind} · {step.durationMS}ms
              </small>
              {step.inputTokens !== undefined && (
                <small>
                  输入 {step.inputTokens} / 输出 {step.outputTokens ?? '未知'} Token
                </small>
              )}
              {step.error && <em>{step.error}</em>}
            </div>
          </li>
        ))}
      </ol>
      {run.result && <pre className="runtime-result">{JSON.stringify(run.result, null, 2)}</pre>}
      {run.error && <Notice tone="danger">{run.error}</Notice>}
    </section>
  );
}

export function RuntimePage() {
  const accounts = useQuery({
    queryKey: ['connections'],
    queryFn: () => connections<Connection[]>('list'),
    refetchInterval: 15000,
  });
  const [tab, setTab] = useState<'judge' | 'agent'>('judge');
  const [resources, setResources] = useState<GPUResource[]>([]);
  const [run, setRun] = useState<RuntimeRun | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState('');
  const [judge, setJudge] = useState<
    Pick<EvaluationRun, 'mode' | 'code' | 'input' | 'expected' | 'problemId'> & { gpuCount: number }
  >({
    mode: 'oi',
    code: 'print(sum(map(int, input().split())))',
    input: '2 3\n',
    expected: '5\n',
    problemId: 'P-DEMO',
    gpuCount: 0,
  });
  const [agent, setAgent] = useState({
    connectionId: '',
    agent: 'codex',
    provider: 'openai-compatible',
    model: 'minimax/minimax-m3.1-flash',
    mode: 'real' as 'real' | 'fixture',
    prompt: '请把这段代码改得更易读，并说明修改原因。',
    gpuCount: 0,
  });
  useEffect(() => {
    getResources()
      .then((r) => setResources(r.gpus))
      .catch(() => setResources([]));
  }, []);
  useEffect(() => {
    if (!run || run.status === 'succeeded' || run.status === 'failed' || run.status === 'unknown')
      return;
    const timer = window.setInterval(
      () =>
        getRun(run.id)
          .then(setRun)
          .catch(() => undefined),
      700,
    );
    return () => window.clearInterval(timer);
  }, [run]);
  async function submit() {
    setBusy(true);
    setMessage('');
    try {
      const next =
        tab === 'judge'
          ? await startEvaluation({
              ...judge,
              resources: { cpus: 1, memoryMB: 512, gpuCount: Number(judge.gpuCount) },
            })
          : await startAgent({
              ...agent,
              resources: { cpus: 1, memoryMB: 512, gpuCount: Number(agent.gpuCount) },
              mode: agent.mode,
            });
      setRun(next);
    } catch (e) {
      setMessage(e instanceof Error ? e.message : '提交失败');
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <PageHeading
        eyebrow="运行中心 / 沙箱与 Agent"
        title="让代码在沙箱中运行，结果可追踪"
        description="OI 题、模型/权重评测和 Agent 调度共用隔离资源租约与运行记录。"
      />
      <div className="runtime-tabs">
        <button className={tab === 'judge' ? 'active' : ''} onClick={() => setTab('judge')}>
          沙箱评测
        </button>
        <button className={tab === 'agent' ? 'active' : ''} onClick={() => setTab('agent')}>
          Agent 调度
        </button>
      </div>
      <section className="runtime-form">
        <h2>{tab === 'judge' ? '提交一次评测' : '选择模型并调度 Agent'}</h2>
        {tab === 'judge' ? (
          <>
            <label>
              评测类型
              <select
                value={judge.mode}
                onChange={(e) =>
                  setJudge({ ...judge, mode: e.target.value === 'model' ? 'model' : 'oi' })
                }
              >
                <option value="oi">OI / stdin stdout</option>
                <option value="model">模型/权重评测</option>
              </select>
            </label>
            <label>
              题目或评测集
              <input
                value={judge.problemId}
                onChange={(e) => setJudge({ ...judge, problemId: e.target.value })}
              />
            </label>
            <div className="field">
              <span className="field-label">模型代码</span>
              <LazyCodeEditor
                value={judge.code}
                onChange={(code) => setJudge({ ...judge, code })}
                path="main.py"
                ariaLabel="模型代码"
                height={320}
              />
            </div>
            <div className="runtime-inline">
              <label>
                输入
                <textarea
                  value={judge.input}
                  onChange={(e) => setJudge({ ...judge, input: e.target.value })}
                />
              </label>
              <label>
                期望输出
                <textarea
                  value={judge.expected}
                  onChange={(e) => setJudge({ ...judge, expected: e.target.value })}
                />
              </label>
            </div>
          </>
        ) : (
          <>
            <label>
              调用模式
              <select
                value={agent.mode}
                onChange={(e) =>
                  setAgent({ ...agent, mode: e.target.value === 'fixture' ? 'fixture' : 'real' })
                }
              >
                <option value="real">真实模型网关</option>
                <option value="fixture">本地 fixture（不计费）</option>
              </select>
            </label>
            <label>
              模型账户
              <select
                value={agent.connectionId}
                onChange={(e) => {
                  const account = accounts.data?.find((c) => c.id === e.target.value);
                  setAgent({
                    ...agent,
                    connectionId: e.target.value,
                    provider: account?.provider || 'openai-compatible',
                    model: account?.models[0] || '',
                  });
                }}
              >
                <option value="">请选择已验证的账户</option>
                {accounts.data?.filter(canUseConnection).map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Agent
              <select
                value={agent.agent}
                onChange={(e) => setAgent({ ...agent, agent: e.target.value })}
              >
                <option>codex</option>
                <option>claude</option>
                <option>opencode</option>
                <option>pi</option>
              </select>
            </label>
            <label>
              模型
              <select
                value={agent.model}
                onChange={(e) => setAgent({ ...agent, model: e.target.value })}
              >
                {(
                  accounts.data?.find((c) => c.id === agent.connectionId)?.models || [
                    'minimax/minimax-m3.1-flash',
                    'deepseek/deepseek-v4.1-flash-1017',
                    'z-ai/glm-5.3-flash',
                  ]
                ).map((model) => (
                  <option key={model}>{model}</option>
                ))}
              </select>
            </label>
            <label>
              任务提示
              <textarea
                value={agent.prompt}
                onChange={(e) => setAgent({ ...agent, prompt: e.target.value })}
              />
            </label>
          </>
        )}
        <label>
          GPU 数量
          <select
            value={tab === 'judge' ? judge.gpuCount : agent.gpuCount}
            onChange={(e) =>
              tab === 'judge'
                ? setJudge({ ...judge, gpuCount: Number(e.target.value) })
                : setAgent({ ...agent, gpuCount: Number(e.target.value) })
            }
          >
            <option value={0}>CPU 模式</option>
            {resources.map((gpu) => (
              <option value={1} key={gpu.index}>
                1 × {gpu.name} · {gpu.freeMemoryMB}MB 可用
              </option>
            ))}
          </select>
        </label>
        <p className="field-help">
          GPU 调度按型号、显存和租约预留；本机检测到 {resources.length} 张 NVIDIA GPU。当前 Agent
          真实模型模式使用宿主网关并记录 Token/耗时；fixture 模式只用于不计费回归测试。
        </p>
        <button
          className="button"
          disabled={
            busy ||
            (tab === 'agent' &&
              agent.mode === 'real' &&
              !accounts.data?.some((c) => c.id === agent.connectionId && canUseConnection(c)))
          }
          onClick={() => void submit()}
        >
          {busy ? '正在提交…' : tab === 'judge' ? '开始沙箱评测' : '开始 Agent 调度'}
        </button>
        {message && <Notice tone="danger">{message}</Notice>}
      </section>
      {run && <Chain run={run} />}
    </>
  );
}
