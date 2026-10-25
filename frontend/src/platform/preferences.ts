import { useEffect, useState } from 'react';
import { useAuth } from '../auth/context';

export const runColumnOptions = [
  ['agent', 'Agent / 版本'],
  ['source', '来源'],
  ['duration', '耗时'],
  ['cost', '费用'],
  ['created', '创建时间'],
] as const;
export type RunColumn = (typeof runColumnOptions)[number][0];
type Preferences = { columns: RunColumn[]; views: { name: string; search: string }[] };
const fresh = (): Preferences => ({ columns: runColumnOptions.map(([key]) => key), views: [] });
const fields = new Set([
  'status',
  'trigger',
  'actorId',
  'agent',
  'version',
  'source',
  'fromRun',
  'toRun',
  'minCost',
  'maxCost',
]);
function cleanSearch(search: string) {
  const query = new URLSearchParams(search);
  return new URLSearchParams(
    [...query].filter(([key, value]) => fields.has(key) && value.length <= 200),
  ).toString();
}
function read(key: string): Preferences {
  try {
    const data = JSON.parse(localStorage.getItem(key) || '{}') as Partial<Preferences>;
    return {
      columns: Array.isArray(data.columns)
        ? data.columns.filter((column) => runColumnOptions.some(([key]) => key === column))
        : fresh().columns,
      views: Array.isArray(data.views)
        ? data.views
            .filter((view) => typeof view.name === 'string' && typeof view.search === 'string')
            .slice(0, 10)
            .map((view) => ({ name: view.name.slice(0, 40), search: cleanSearch(view.search) }))
        : [],
    };
  } catch {
    return fresh();
  }
}
// UI filter/column preferences only; no fetched records, results, or credential values.
export function useRunPreferences() {
  const { user } = useAuth();
  const key = `agentlab.ui.runs.v1.${user?.id ?? 'anonymous'}`;
  const [value, setValue] = useState<Preferences>(() => read(key));
  useEffect(() => setValue(read(key)), [key]);
  const update = (next: Preferences) => {
    setValue(next);
    try {
      localStorage.setItem(key, JSON.stringify(next));
    } catch {
      /* preferences remain usable for this session */
    }
  };
  return {
    value,
    columns: (columns: RunColumn[]) => update({ ...value, columns }),
    save: (name: string, search: string) =>
      update({
        ...value,
        views: [
          ...value.views.filter((view) => view.name !== name.trim()),
          { name: name.trim().slice(0, 40), search: cleanSearch(search) },
        ].slice(-10),
      }),
    remove: (name: string) =>
      update({ ...value, views: value.views.filter((view) => view.name !== name) }),
  };
}
