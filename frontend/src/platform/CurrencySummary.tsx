import { useState } from 'react';
import { useQueries } from '@tanstack/react-query';
import { get } from './api';
import type { UsageSummary } from './api';
import { usePlatform } from './state';
import { Money } from './controls';
import { QueryState } from './shared';

export function CurrencySummary({ from, to }: { from?: string; to?: string }) {
  const { projects } = usePlatform();
  const [selected, setSelected] = useState<string[]>([]);
  const queries = useQueries({
    queries: selected.map((projectId) => ({
      queryKey: ['platform', 'currency-summary', projectId, from, to],
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        get<UsageSummary>('/usage/summary', { projectId, from, to }, signal),
      retry: false,
    })),
  });
  const groups = new Map<
    UsageSummary['currency'],
    {
      settled: bigint;
      pending: bigint;
      settledKnown: boolean;
      pendingKnown: boolean;
      count: number;
    }
  >();
  queries.forEach((query) => {
    if (!query.data) return;
    const data = query.data;
    const group = groups.get(data.currency) ?? {
      settled: 0n,
      pending: 0n,
      settledKnown: true,
      pendingKnown: true,
      count: 0,
    };
    group.settled += BigInt(data.settledMicros);
    group.pending += BigInt(data.pendingMicros);
    group.count++;
    group.settledKnown =
      group.settledKnown &&
      !data.groups.some((item) => item.status === 'settled' && item.amountKnown === false);
    group.pendingKnown =
      group.pendingKnown &&
      !data.groups.some((item) => item.status === 'pending' && item.amountKnown === false);
    groups.set(data.currency, group);
  });
  return (
    <details className="wb-panel">
      <summary>多项目按币种汇总</summary>
      <p className="wb-muted">
        选择最多 5 个已加载项目；分别汇总，不做汇率换算。沿用时间范围，不沿用单次 Run 或密钥筛选。
      </p>
      <div className="wb-check-grid">
        {projects.map((project) => (
          <label className="wb-check" key={project.id}>
            <input
              type="checkbox"
              checked={selected.includes(project.id)}
              disabled={!selected.includes(project.id) && selected.length >= 5}
              onChange={(event) =>
                setSelected((current) =>
                  event.target.checked
                    ? [...current, project.id]
                    : current.filter((id) => id !== project.id),
                )
              }
            />
            {project.name} · {project.currency}
          </label>
        ))}
      </div>
      {selected.length > 0 && (
        <QueryState
          pending={queries.some((query) => query.isPending)}
          error={queries.find((query) => query.error)?.error ?? null}
          retry={() => queries.forEach((query) => void query.refetch())}
        >
          <div className="wb-table-wrap">
            <table className="wb-table">
              <thead>
                <tr>
                  <th>币种</th>
                  <th>项目数</th>
                  <th>已结算</th>
                  <th>待核对</th>
                </tr>
              </thead>
              <tbody>
                {[...groups].map(([currency, group]) => (
                  <tr key={currency}>
                    <td>{currency}</td>
                    <td>{group.count}</td>
                    <td>
                      <Money
                        currency={currency}
                        value={group.settledKnown ? group.settled.toString() : null}
                      />
                    </td>
                    <td>
                      <Money
                        currency={currency}
                        value={group.pendingKnown ? group.pending.toString() : null}
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </QueryState>
      )}
    </details>
  );
}
