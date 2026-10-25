export type Currency = 'CNY' | 'USD';
export type ProjectEnvironment = 'production' | 'staging';
export type ProjectStatus = 'active' | 'paused';
export type AgentStatus = 'published' | 'draft' | 'archived';
export type ToolStatus = 'available' | 'beta' | 'disabled';
export type ToolRisk = 'low' | 'medium' | 'high';
export type RunStatus =
  | 'queued'
  | 'preparing'
  | 'running'
  | 'cancelling'
  | 'succeeded'
  | 'failed'
  | 'cancelled'
  | 'unknown';
export type RunTrigger = 'workbench' | 'api';
export type ModelSource = 'platform' | 'byok' | 'custom';
export type BillingStatus = 'pending' | 'estimated' | 'settled' | 'not_applicable';
export type ToolCallStatus =
  | 'planned'
  | 'waiting_approval'
  | 'dispatched'
  | 'succeeded'
  | 'failed'
  | 'unknown';
export type RunLevel = 'info' | 'warning' | 'error' | 'success';
export type UsageCategory = 'model' | 'tool' | 'execution' | 'storage' | 'adjustment';
export type LedgerType = 'reserve' | 'settle' | 'release' | 'adjustment';

export const currencySymbol: Record<Currency, string> = { CNY: '¥', USD: '$' };

export const runStatusLabels: Record<RunStatus, string> = {
  queued: '排队中',
  preparing: '准备中',
  running: '运行中',
  cancelling: '正在停止',
  succeeded: '已完成',
  failed: '失败',
  cancelled: '已停止',
  unknown: '结果待核对',
};

export const billingStatusLabels: Record<BillingStatus, string> = {
  pending: '待结算',
  estimated: '估算',
  settled: '已结算',
  not_applicable: '不适用',
};

export const agentStatusLabels: Record<AgentStatus, string> = {
  published: '已发布',
  draft: '草稿',
  archived: '已归档',
};

export const toolStatusLabels: Record<ToolStatus, string> = {
  available: '可用',
  beta: '测试版',
  disabled: '未启用',
};

export const riskLabels: Record<ToolRisk, string> = {
  low: '低风险',
  medium: '需关注',
  high: '高影响',
};

export const toolCallStatusLabels: Record<ToolCallStatus, string> = {
  planned: '已计划',
  waiting_approval: '等待审批',
  dispatched: '已派发',
  succeeded: '成功',
  failed: '失败',
  unknown: '结果待核对',
};

export const ledgerTypeLabels: Record<LedgerType, string> = {
  reserve: '预占',
  settle: '结算',
  release: '释放',
  adjustment: '调整',
};

export const usageCategoryLabels: Record<UsageCategory, string> = {
  model: '模型',
  tool: '工具',
  execution: '托管执行',
  storage: '存储',
  adjustment: '调整',
};

export const modelSourceLabels: Record<ModelSource, string> = {
  platform: '平台供模',
  byok: 'BYOK',
  custom: '自建端点',
};

export const triggerLabels: Record<RunTrigger, string> = {
  workbench: '工作台',
  api: 'API',
};

export function isTerminal(status: RunStatus) {
  return ['succeeded', 'failed', 'cancelled', 'unknown'].includes(status);
}

export function formatMoney(micros: string | null, currency: Currency = 'CNY') {
  if (micros === null || !/^-?\d+$/.test(micros)) return '未知';
  const value = BigInt(micros);
  const absolute = value < 0n ? -value : value;
  const sign = value < 0n ? '-' : '';
  if (absolute > 0n && absolute < 10_000n) return `${sign}${currencySymbol[currency]}< 0.01`;
  const cents = (absolute + 5_000n) / 10_000n;
  return `${sign}${currencySymbol[currency]}${(cents / 100n).toLocaleString('zh-CN')}.${String(cents % 100n).padStart(2, '0')}`;
}

export function formatUnitPrice(micros: string | null, currency: Currency = 'CNY') {
  if (micros === null || !/^-?\d+$/.test(micros)) return '未定价';
  const value = BigInt(micros);
  const absolute = value < 0n ? -value : value;
  const fraction = String(absolute % 1_000_000n)
    .padStart(6, '0')
    .replace(/0+$/, '')
    .padEnd(2, '0');
  return `${value < 0n ? '-' : ''}${currencySymbol[currency]}${(absolute / 1_000_000n).toLocaleString('zh-CN')}.${fraction}`;
}

export function decimalToMicros(value: string) {
  const input = value.trim();
  if (!/^\d+(?:\.\d{1,6})?$/.test(input)) throw new Error('请输入非负金额，最多保留 6 位小数。');
  const [whole = '0', fraction = ''] = input.split('.');
  return (BigInt(whole) * 1_000_000n + BigInt(fraction.padEnd(6, '0'))).toString();
}

export function formatQuantity(value: string): string {
  if (/^-?\d+$/.test(value)) return BigInt(value).toLocaleString('zh-CN');
  if (value.includes('/')) {
    return value
      .split('/')
      .map((part) => formatQuantity(part.trim()))
      .join(' / ');
  }
  const number = Number(value);
  if (!Number.isFinite(number)) return value;
  return number.toLocaleString('zh-CN', { maximumFractionDigits: 2 });
}

export function formatDate(value: string | null) {
  if (!value) return '—';
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(new Date(value));
}

export function expiryLabel(value: string | null) {
  if (!value) return '不自动到期';
  const time = new Date(value).getTime();
  if (!Number.isFinite(time)) return '—';
  if (time < Date.now()) return '已到期';
  const days = Math.ceil((time - Date.now()) / 86_400_000);
  if (days <= 45) return `${days} 天后`;
  return formatDate(value);
}

export function formatDuration(ms: number | null) {
  if (ms === null) return '—';
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(ms < 10_000 ? 1 : 0)} 秒`;
  const minutes = Math.floor(ms / 60_000);
  const seconds = Math.round((ms % 60_000) / 1000);
  return `${minutes} 分 ${seconds} 秒`;
}

export function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

export function formatDateTime(value: string | null) {
  if (!value) return '—';
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    timeZoneName: 'short',
  }).format(new Date(value));
}

export function timeAgo(value: string | null) {
  if (!value) return '—';
  const diff = Date.now() - new Date(value).getTime();
  if (!Number.isFinite(diff)) return '—';
  if (diff < 60_000) return '刚刚';
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)} 分钟前`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)} 小时前`;
  return `${Math.floor(diff / 86_400_000)} 天前`;
}

export function shortId(id: string, length = 10) {
  return id.length <= length ? id : `${id.slice(0, length)}…`;
}

export function maskKey(prefix: string) {
  return `${prefix}${'•'.repeat(18)}`;
}
