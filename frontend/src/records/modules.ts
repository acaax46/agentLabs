import type { CatalogKind, RuntimeRecordKind } from '../api/client';

export type Filter = {
  key: string;
  label: string;
  options?: [string, string][];
  source?: CatalogKind;
};
export type RecordModule = {
  kind: RuntimeRecordKind;
  title: string;
  icon: string;
  description: string;
  empty: string;
  filters: Filter[];
  advanced: Filter[];
};
export const labels: Record<string, string> = {
  queued: '排队中',
  running: '评测中',
  completed: '已完成',
  rejected: '已拒绝',
  cancelled: '已取消',
  system_failed: '系统异常',
  accepted: '通过',
  wrong_answer: '答案错误',
  compile_error: '编译错误',
  runtime_error: '运行错误',
  time_limit: '超时',
  memory_limit: '超出内存',
  output_limit: '输出超限',
  succeeded: '成功',
  failed: '失败',
  unknown: '未知',
  'provider-reported': '供应商报告',
  estimated: '估算',
  pending: '待结算',
  settled: '已结算',
  'not-applicable': '不适用',
  operation: '操作日志',
  system: '系统日志',
  security: '安全日志',
  info: '信息',
  warning: '警示',
  error: '错误',
  success: '成功',
  denied: '已拒绝',
  failure: '失败',
  practice: '练习反馈',
  institution: '机构评测',
  model: '模型调用',
  mcp: '工具调用',
  judge: '判题资源',
  sandbox: '沙箱资源',
  'catalog.create': '新建资料',
  'catalog.update': '编辑资料',
  'catalog.delete': '删除资料',
};
const options = (...values: string[]): [string, string][] =>
  values.map((value) => [value, labels[value] || value]);
export const recordModules: RecordModule[] = [
  {
    kind: 'evaluations',
    title: '评测记录',
    icon: 'judge',
    description: '按提交和判题批次回看评测结果。重新评测保留独立记录，原始成绩不能手工覆盖。',
    empty: '还没有评测记录。真实判题接入后，提交与结果会显示在这里。',
    filters: [
      {
        key: 'status',
        label: '评测状态',
        options: options(
          'queued',
          'running',
          'completed',
          'system_failed',
          'rejected',
          'cancelled',
        ),
      },
      {
        key: 'verdict',
        label: '评测结果',
        options: options(
          'accepted',
          'wrong_answer',
          'compile_error',
          'runtime_error',
          'time_limit',
          'memory_limit',
          'output_limit',
        ),
      },
    ],
    advanced: [
      { key: 'studentId', label: '学员', source: 'students' },
      { key: 'problemId', label: '题目', source: 'problems' },
      { key: 'assignmentId', label: '学习任务', source: 'assignments' },
      { key: 'contestId', label: '训练赛', source: 'contests' },
    ],
  },
  {
    kind: 'usage-records',
    title: '用量记录',
    icon: 'usage',
    description:
      '按操作与尝试查看模型、工具、Token、耗时和费用。未知用量不按零计，估算值不当作已结算金额。',
    empty: '还没有用量记录。模型、工具或运行资源接入后，会记录实际可确认的数据。',
    filters: [
      {
        key: 'status',
        label: '调用状态',
        options: options('succeeded', 'failed', 'cancelled', 'unknown'),
      },
      {
        key: 'source',
        label: '用量来源',
        options: options('provider-reported', 'estimated', 'unknown'),
      },
      {
        key: 'settlement',
        label: '结算状态',
        options: options('pending', 'estimated', 'settled', 'not-applicable'),
      },
    ],
    advanced: [
      { key: 'studentId', label: '学员', source: 'students' },
      { key: 'provider', label: '供应商' },
      { key: 'model', label: '模型名称' },
      { key: 'operationId', label: '操作编号' },
    ],
  },
  {
    kind: 'audit-logs',
    title: '日志',
    icon: 'audit',
    description: '查看资料变更和后续系统、安全事件。日志仅追加，保留操作对象、结果和请求编号。',
    empty: '还没有日志。之后的资料新增、编辑和删除会自动记录；不会补造历史事件。',
    filters: [
      { key: 'category', label: '日志类别', options: options('operation', 'system', 'security') },
      { key: 'level', label: '日志级别', options: options('info', 'warning', 'error') },
      {
        key: 'result',
        label: '操作结果',
        options: options('success', 'denied', 'failure', 'unknown'),
      },
    ],
    advanced: [
      {
        key: 'resourceType',
        label: '对象类型',
        options: [
          ['problems', '题目'],
          ['classes', '班级'],
          ['students', '学员'],
          ['courses', '课程'],
          ['templates', '环境模板'],
          ['assignments', '任务'],
          ['contests', '训练赛'],
        ],
      },
      { key: 'resourceId', label: '对象 ID' },
      { key: 'requestId', label: '请求编号' },
    ],
  },
];
export const recordModuleMap = Object.fromEntries(
  recordModules.map((module) => [module.kind, module]),
) as Record<RuntimeRecordKind, RecordModule>;
export function known(value: number | null | undefined, suffix = '') {
  return value === null || value === undefined
    ? '未知'
    : `${value.toLocaleString('zh-CN')}${suffix}`;
}
export function money(
  value: string | null | undefined,
  currency: string,
  estimated = false,
  missing = '待结算',
) {
  if (value === null || value === undefined) return missing;
  if (!/^\d+$/.test(value)) return '未知金额';
  const amount = BigInt(value);
  const whole = (amount / 1000000n).toString();
  const fraction = (amount % 1000000n)
    .toString()
    .padStart(6, '0')
    .replace(/0+$/, '')
    .padEnd(2, '0');
  return `${estimated ? '约 ' : ''}${currency} ${whole}.${fraction}`;
}
