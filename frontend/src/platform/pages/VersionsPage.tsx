import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link, useParams } from 'react-router-dom';
import { get } from '../api';
import type { Agent, AgentVersion } from '../api';
import { QueryState } from '../shared';
import { CodeBlock, PageHeader, SectionHeader, StatusBadge } from '../ui';
import { formatDateTime } from '../model';

function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(',')}]`;
  if (value && typeof value === 'object')
    return `{${Object.entries(value)
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([key, item]) => `${JSON.stringify(key)}:${canonical(item)}`)
      .join(',')}}`;
  return JSON.stringify(value) ?? 'null';
}
export function VersionComparison({ versions }: { versions: AgentVersion[] }) {
  const [left, setLeft] = useState(versions[0]?.id ?? '');
  const [right, setRight] = useState(versions[1]?.id ?? versions[0]?.id ?? '');
  const a = versions.find((version) => version.id === left);
  const b = versions.find((version) => version.id === right);
  const rows: [string, unknown, unknown][] = [
    ['版本', a?.version, b?.version],
    ['提示词摘要', a?.promptDigest, b?.promptDigest],
    ['工具与版本', a?.tools, b?.tools],
    ['输出 schema', a?.outputSchema, b?.outputSchema],
    ['默认与最大限制', a?.limits, b?.limits],
    ['输入格式', a?.inputFormats, b?.inputFormats],
    ['沙箱配置', a?.sandbox, b?.sandbox],
    ['内容摘要', a?.digest, b?.digest],
  ];
  return (
    <section className="wb-panel">
      <SectionHeader title="发布版本对比" description="比较后端不可变快照。已有 Run 保持原版本。" />
      <div className="wb-form-grid">
        {(
          [
            ['左侧版本', left, setLeft],
            ['右侧版本', right, setRight],
          ] as const
        ).map(([label, value, change]) => (
          <label className="wb-field" key={label}>
            <span>{label}</span>
            <select value={value} onChange={(event) => change(event.target.value)}>
              {versions.map((version) => (
                <option key={version.id} value={version.id}>
                  {version.version} · {formatDateTime(version.createdAt)}
                </option>
              ))}
            </select>
          </label>
        ))}
      </div>
      {versions.length < 2 && (
        <p className="wb-muted">当前只有一个可访问发布版本，尚无第二份快照可比较。</p>
      )}
      <div className="wb-table-wrap">
        <table className="wb-table">
          <thead>
            <tr>
              <th>冻结项</th>
              <th>左侧</th>
              <th>右侧</th>
              <th>差异</th>
            </tr>
          </thead>
          <tbody>
            {rows.map(([label, before, after]) => (
              <tr key={label}>
                <th>{label}</th>
                <td>
                  <pre className="wb-diff-value">
                    {typeof before === 'string'
                      ? before
                      : (JSON.stringify(before, null, 2) ?? '未知')}
                  </pre>
                </td>
                <td>
                  <pre className="wb-diff-value">
                    {typeof after === 'string' ? after : (JSON.stringify(after, null, 2) ?? '未知')}
                  </pre>
                </td>
                <td>
                  <StatusBadge
                    tone={canonical(before) === canonical(after) ? 'neutral' : 'warning'}
                  >
                    {canonical(before) === canonical(after) ? '相同' : '已变化'}
                  </StatusBadge>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="wb-muted">镜像摘要尚未提供。草稿编辑、发布新版本及 API 默认版本切换未开放。</p>
      {a && (
        <details>
          <summary>查看完整冻结快照</summary>
          <CodeBlock copy>{JSON.stringify(a, null, 2)}</CodeBlock>
        </details>
      )}
    </section>
  );
}
export function VersionsPage() {
  const { id = '' } = useParams();
  const query = useQuery({
    queryKey: ['platform', 'agent', id],
    queryFn: ({ signal }) =>
      get<{ agent: Agent; versions: AgentVersion[] }>(
        `/agents/${encodeURIComponent(id)}`,
        {},
        signal,
      ),
    retry: false,
  });
  return (
    <QueryState pending={query.isPending} error={query.error} retry={query.refetch}>
      {query.data && (
        <>
          <PageHeader
            title={`${query.data.agent.name} · 版本`}
            description="查看和比较已发布版本。"
            actions={
              <Link className="wb-button wb-button--primary" to={`/agents/${id}`}>
                打开 Playground
              </Link>
            }
          />
          <VersionComparison versions={query.data.versions} />
        </>
      )}
    </QueryState>
  );
}
