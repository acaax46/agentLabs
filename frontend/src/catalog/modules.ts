import type { CatalogKind, CatalogRecord } from '../api/client';

export type Field = {
  key: string;
  label: string;
  type: 'text' | 'textarea' | 'number' | 'select' | 'multi' | 'tags' | 'samples' | 'datetime';
  required?: boolean;
  min?: number;
  max?: number;
  maxLength?: number;
  source?: CatalogKind;
  options?: { value: string; label: string }[];
  help?: string;
  markdown?: boolean;
};
export type Module = {
  kind: CatalogKind;
  title: string;
  singular: string;
  icon: string;
  description: string;
  defaultStatus: string;
  defaults: Record<string, unknown>;
  fields: Field[];
};
const modeOptions = [
  { value: 'classroom', label: '课程练习' },
  { value: 'competition', label: '比赛训练' },
  { value: 'family', label: '家庭练习' },
];
export const modules: Module[] = [
  {
    kind: 'problems',
    title: '题目管理',
    singular: '题目',
    icon: 'tests',
    description: '整理题面、难度、标签与公开样例，为课程和评测准备练习内容。',
    defaultStatus: 'draft',
    defaults: {
      difficulty: 'easy',
      language: 'python',
      statement: '',
      inputDescription: '',
      outputDescription: '',
      timeLimitMs: 1000,
      memoryLimitMb: 256,
      tags: [],
      samples: [],
    },
    fields: [
      {
        key: 'difficulty',
        label: '题目难度',
        type: 'select',
        required: true,
        options: [
          { value: 'easy', label: '入门' },
          { value: 'medium', label: '进阶' },
          { value: 'hard', label: '挑战' },
        ],
      },
      {
        key: 'language',
        label: '题目类型',
        type: 'select',
        required: true,
        options: [
          { value: 'python', label: 'Python / stdin stdout' },
          { value: 'html', label: 'HTML / CSS / JavaScript 预览' },
        ],
        help: 'HTML 题目在工作区提供隔离预览，不进入 stdin/stdout 评测。',
      },
      {
        key: 'timeLimitMs',
        label: '时间限制（毫秒）',
        type: 'number',
        required: true,
        min: 100,
        max: 60000,
      },
      {
        key: 'memoryLimitMb',
        label: '内存限制（MB）',
        type: 'number',
        required: true,
        min: 16,
        max: 2048,
      },
      {
        key: 'tags',
        label: '知识标签',
        type: 'tags',
        help: '最多 12 个，每个不超过 24 字，使用逗号分隔。',
      },
      {
        key: 'statement',
        label: '题目描述',
        type: 'textarea',
        maxLength: 30000,
        markdown: true,
        help: '支持 Markdown、表格、代码块与 LaTeX 公式；发布后按渲染结果展示。',
      },
      {
        key: 'inputDescription',
        label: '输入说明',
        type: 'textarea',
        maxLength: 10000,
        markdown: true,
        help: '支持 Markdown 与 LaTeX 公式。',
      },
      {
        key: 'outputDescription',
        label: '输出说明',
        type: 'textarea',
        maxLength: 10000,
        markdown: true,
        help: '支持 Markdown 与 LaTeX 公式。',
      },
      {
        key: 'samples',
        label: '公开样例',
        type: 'samples',
        help: '仅管理公开样例，隐藏测试与判题器不在此处配置。',
      },
    ],
  },
  {
    kind: 'classes',
    title: '班级管理',
    singular: '班级',
    icon: 'classroom',
    description: '维护班级、负责教师、学期与容量，关联学员和任务。',
    defaultStatus: 'active',
    defaults: { teacher: '', term: '', capacity: 30 },
    fields: [
      { key: 'teacher', label: '负责教师', type: 'text', required: true, maxLength: 80 },
      { key: 'term', label: '学期', type: 'text', maxLength: 80 },
      { key: 'capacity', label: '班级容量', type: 'number', required: true, min: 1, max: 1000 },
    ],
  },
  {
    kind: 'students',
    title: '学员管理',
    singular: '学员',
    icon: 'student',
    description: '维护学员档案与班级归属。档案不等于登录账号，也不会自动开通代码空间。',
    defaultStatus: 'active',
    defaults: { email: '', classIds: [] },
    fields: [
      { key: 'email', label: '联系邮箱', type: 'text', maxLength: 254 },
      {
        key: 'classIds',
        label: '所属班级',
        type: 'multi',
        source: 'classes',
        help: '可加入多个班级；有效学员受各班容量约束。',
      },
    ],
  },
  {
    kind: 'templates',
    title: '环境模板',
    singular: '模板',
    icon: 'templates',
    description: '保存 Agent、网络策略与沙箱环境说明的草稿，后续再接运行环境和版本发布。',
    defaultStatus: 'draft',
    defaults: {
      mode: 'classroom',
      agents: ['Claude Code', 'Codex CLI'],
      network: 'approved-only',
      environmentNote: '',
    },
    fields: [
      { key: 'mode', label: '使用场景', type: 'select', required: true, options: modeOptions },
      {
        key: 'network',
        label: '网络策略草稿',
        type: 'select',
        required: true,
        options: [
          { value: 'approved-only', label: '仅批准的模型与工具' },
          { value: 'offline', label: '不允许外网' },
        ],
      },
      {
        key: 'agents',
        label: '计划使用的 Agent',
        type: 'multi',
        options: [
          { value: 'Claude Code', label: 'Claude Code' },
          { value: 'Codex CLI', label: 'Codex CLI' },
        ],
      },
      {
        key: 'environmentNote',
        label: '环境说明',
        type: 'textarea',
        maxLength: 4000,
        help: '仅保存环境要求，不填写 API Key、密码或隐藏测试。',
      },
    ],
  },
  {
    kind: 'courses',
    title: '课程管理',
    singular: '课程',
    icon: 'courses',
    description: '组织课程介绍、练习题与沙箱环境模板，先准备内容，再安排学习任务。',
    defaultStatus: 'draft',
    defaults: { mode: 'classroom', templateId: '', problemIds: [] },
    fields: [
      { key: 'mode', label: '课程场景', type: 'select', required: true, options: modeOptions },
      { key: 'templateId', label: '环境模板', type: 'select', source: 'templates' },
      {
        key: 'problemIds',
        label: '课程题目',
        type: 'multi',
        source: 'problems',
        help: '选择的顺序会保存为课程题目顺序。',
      },
    ],
  },
  {
    kind: 'assignments',
    title: '学习任务',
    singular: '任务',
    icon: 'assignments',
    description: '为班级准备课程任务、截止时间与提交次数。当前只保存草稿，不发布或启动沙箱。',
    defaultStatus: 'draft',
    defaults: { classId: '', courseId: '', deadline: '', submissionLimit: 10 },
    fields: [
      { key: 'classId', label: '所属班级', type: 'select', required: true, source: 'classes' },
      { key: 'courseId', label: '关联课程', type: 'select', required: true, source: 'courses' },
      { key: 'deadline', label: '提交截止时间', type: 'datetime' },
      {
        key: 'submissionLimit',
        label: '最多提交次数',
        type: 'number',
        required: true,
        min: 1,
        max: 1000,
      },
    ],
  },
  {
    kind: 'contests',
    title: '训练赛',
    singular: '训练赛',
    icon: 'trophy',
    description: '整理训练赛说明、时间、参赛班级和题目。当前不开放比赛、排名或自动判题。',
    defaultStatus: 'draft',
    defaults: { startsAt: '', endsAt: '', problemIds: [], classIds: [] },
    fields: [
      { key: 'startsAt', label: '开始时间', type: 'datetime' },
      { key: 'endsAt', label: '结束时间', type: 'datetime' },
      { key: 'classIds', label: '参赛班级', type: 'multi', source: 'classes' },
      {
        key: 'problemIds',
        label: '训练题目',
        type: 'multi',
        source: 'problems',
        help: '选择顺序即题目顺序；时间可以同时留空。',
      },
    ],
  },
];
export const moduleMap = Object.fromEntries(
  modules.map((module) => [module.kind, module]),
) as Record<CatalogKind, Module>;
export const statusLabels: Record<string, string> = {
  draft: '草稿',
  active: '有效',
  archived: '已归档',
};
export function statuses(module: Module) {
  return module.kind === 'classes' || module.kind === 'students'
    ? ['active', 'draft', 'archived']
    : ['draft', 'archived'];
}
export function brief(record: CatalogRecord): string {
  const fields = record.fields as Record<string, unknown>;
  if (record.kind === 'problems')
    return (
      { easy: '入门', medium: '进阶', hard: '挑战' }[String(fields.difficulty)] || '未设置难度'
    );
  if (record.kind === 'classes')
    return `${String(fields.teacher)} · 容量 ${String(fields.capacity)}`;
  if (record.kind === 'students')
    return `${(fields.classIds as string[] | undefined)?.length || 0} 个班级`;
  if (record.kind === 'courses' || record.kind === 'contests')
    return `${(fields.problemIds as string[] | undefined)?.length || 0} 道练习题`;
  if (record.kind === 'assignments')
    return fields.deadline ? `截止 ${formatDate(String(fields.deadline))}` : '未设置截止时间';
  return String(fields.mode) === 'family'
    ? '家庭练习'
    : String(fields.mode) === 'competition'
      ? '比赛训练'
      : '课程练习';
}
export function formatDate(value: string) {
  return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(
    new Date(value),
  );
}
