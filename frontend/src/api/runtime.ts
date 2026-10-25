export type GPUResource = {
  index: number;
  name: string;
  totalMemoryMB: number;
  freeMemoryMB: number;
};
export type RuntimeStep = {
  id: string;
  kind: string;
  label: string;
  status: string;
  provider?: string;
  model?: string;
  inputTokens?: number;
  outputTokens?: number;
  durationMS: number;
  error?: string;
};
export type RuntimeRun = {
  id: string;
  kind: string;
  mode?: string;
  status: string;
  agent?: string;
  provider?: string;
  model?: string;
  resources: { cpus: number; memoryMB: number; gpuCount: number };
  allocatedGpu?: GPUResource[];
  steps: RuntimeStep[];
  result?: Record<string, unknown>;
  error?: string;
};
export type EvaluationRun = {
  mode: 'oi' | 'model';
  problemId: string;
  code: string;
  language?: string;
  input: string;
  expected: string;
  modelRef?: string;
  weightsRef?: string;
  resources: { cpus: number; memoryMB: number; gpuCount: number };
};
export type AgentRun = {
  connectionId?: string;
  agent: string;
  provider: string;
  model: string;
  prompt: string;
  mode: 'fixture' | 'real';
  resources: { cpus: number; memoryMB: number; gpuCount: number };
};
async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      Accept: 'application/json',
      ...(init.headers || {}),
    },
  });
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    throw new Error(body.message || `请求失败 (${response.status})`);
  }
  return response.json() as Promise<T>;
}
export async function getResources(): Promise<{ gpus: GPUResource[] }> {
  return request('/api/v1/runtime/resources');
}
export async function startEvaluation(value: EvaluationRun): Promise<RuntimeRun> {
  return request('/api/v1/runtime/evaluations', { method: 'POST', body: JSON.stringify(value) });
}
export async function startAgent(value: AgentRun): Promise<RuntimeRun> {
  return request('/api/v1/runtime/agent-runs', { method: 'POST', body: JSON.stringify(value) });
}
export async function getRun(id: string): Promise<RuntimeRun> {
  return request(`/api/v1/runtime/runs/${encodeURIComponent(id)}`);
}
