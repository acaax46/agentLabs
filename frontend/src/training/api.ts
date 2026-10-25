import { APIError } from '../api/client';
import type { Sample } from '../api/client';
import type { Workspace } from '../learning/api';

export type TrainingProblem = {
  id: string;
  code: string;
  name: string;
  difficulty: 'easy' | 'medium' | 'hard';
  language: 'python' | 'html';
  tags: string[];
  statement: string;
  inputDescription: string;
  outputDescription: string;
  timeLimitMs: number;
  memoryLimitMb: number;
  samples: Sample[];
  initialCode: string;
  source: string;
  order: number;
};

export type TrainingSection = {
  id: string;
  title: string;
  document: string;
  problemIds: string[];
};

export type TrainingProgress = {
  courseId: string;
  completedSectionIds: string[];
  lastSectionId: string;
  updatedAt: string;
};

export type TrainingCourse = {
  id: string;
  code: string;
  name: string;
  description: string;
  sections: TrainingSection[];
  source: string;
  order: number;
  updatedAt: string;
  progress: TrainingProgress;
  completedSections: number;
};

export type TrainingCourseSummary = {
  id: string;
  code: string;
  name: string;
  description: string;
  source: string;
  sectionCount: number;
  completedSections: number;
  lastSectionId: string;
  updatedAt: string;
};

const routes: Record<string, [string, (id: string) => string]> = {
  problems: ['GET', () => 'problems'],
  problem: ['GET', (id) => `problems/${encodeURIComponent(id)}`],
  open: ['POST', (id) => `problems/${encodeURIComponent(id)}/workspace`],
  courses: ['GET', () => 'courses'],
  course: ['GET', (id) => `courses/${encodeURIComponent(id)}`],
  progress: ['PUT', (id) => `courses/${encodeURIComponent(id)}/progress`],
};

export async function training<T>(action: string, id = '', payload: unknown = {}): Promise<T> {
  const native = window.go?.app?.Desktop;
  if (native) {
    const response = await native.Training({ action, id, key: '', payload });
    if (response.error) throw new APIError(response.error.message, response.error.code);
    return response.data as T;
  }
  const route = routes[action];
  if (!route) throw new Error('未知训练操作');
  const [method, path] = route;
  let suffix = '';
  if (action === 'problems') {
    const query = payload as { q?: string; difficulty?: string };
    const params = new URLSearchParams();
    if (query.q) params.set('q', query.q);
    if (query.difficulty) params.set('difficulty', query.difficulty);
    const value = params.toString();
    if (value) suffix = `?${value}`;
  }
  let response: Response;
  try {
    response = await fetch(`/api/v1/training/${path(id)}${suffix}`, {
      method,
      credentials: 'same-origin',
      signal: AbortSignal.timeout(15000),
      headers: {
        Accept: 'application/json',
        'X-AgentLab-Request': 'training',
        ...(method !== 'GET' ? { 'Content-Type': 'application/json' } : {}),
      },
      ...(method !== 'GET' ? { body: JSON.stringify(payload) } : {}),
    });
  } catch {
    throw new APIError('暂时无法连接训练服务，请稍后重试。');
  }
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    throw new APIError(body.message || '训练操作未完成。', body.code);
  }
  return response.json() as Promise<T>;
}

export async function openTrainingProblem(id: string): Promise<Workspace> {
  return training<Workspace>('open', id);
}
