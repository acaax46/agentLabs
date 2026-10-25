import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { artifactBlob, fetchArtifactDownload, get } from './api';
import type { Artifact, EventPage, Ledger, Page, Run, ToolCall, UsageLine } from './api';
import { readSpreadsheet } from './spreadsheet';
import { makeZip, saveBlob, saveText } from './files';
import { QueryState, MutationError } from './shared';
import { stripTerminalControls } from './logs';

export function DataChart({
  columns,
  rows,
  truncated = false,
}: {
  columns: string[];
  rows: string[][];
  truncated?: boolean;
}) {
  const numeric = columns
    .map((_, index) => index)
    .filter((index) =>
      rows.some((row) => row[index]?.trim() !== '' && Number.isFinite(Number(row[index]))),
    );
  const [labelColumn, setLabelColumn] = useState(0);
  const [valueColumn, setValueColumn] = useState(numeric[0] ?? 0);
  const data = rows
    .slice(0, 50)
    .filter((row) => row[valueColumn]?.trim() !== '' && Number.isFinite(Number(row[valueColumn])))
    .map((row) => ({
      label: row[labelColumn] ?? '',
      value: Number(row[valueColumn]),
      raw: row[valueColumn] ?? '',
    }));
  const max = Math.max(1, ...data.map((row) => Math.abs(row.value)));
  return (
    <details className="wb-data-chart">
      <summary>用预览数据绘图</summary>
      <p className="wb-muted">
        仅绘制本次预览中最多 50 行的纯数值，不代表完整文件。{truncated ? '预览已截断。' : ''}
      </p>
      <div className="wb-form-grid">
        <label className="wb-field">
          <span>图表标签列</span>
          <select
            value={labelColumn}
            onChange={(event) => setLabelColumn(Number(event.target.value))}
          >
            {columns.map((column, index) => (
              <option key={index} value={index}>
                {column}
              </option>
            ))}
          </select>
        </label>
        <label className="wb-field">
          <span>图表数值列</span>
          <select
            value={valueColumn}
            onChange={(event) => setValueColumn(Number(event.target.value))}
          >
            {numeric.map((index) => (
              <option key={index} value={index}>
                {columns[index]}
              </option>
            ))}
          </select>
        </label>
      </div>
      {data.length ? (
        <div className="wb-chart-scroll">
          <svg
            role="img"
            aria-label={`${columns[valueColumn]} 预览柱状图`}
            viewBox={`0 0 640 ${data.length * 28 + 24}`}
            className="wb-numeric-chart"
          >
            <line x1="340" x2="340" y1="0" y2={data.length * 28 + 10} className="wb-chart-axis" />
            {data.map((row, index) => {
              const width = Math.abs(row.value / max) * 180;
              return (
                <g key={index}>
                  <title>
                    {row.label}: {row.raw}
                  </title>
                  <text x="8" y={index * 28 + 20}>
                    {row.label.slice(0, 28)}
                  </text>
                  <rect
                    x={row.value < 0 ? 340 - width : 340}
                    y={index * 28 + 5}
                    width={width}
                    height="18"
                  />
                  <text x="530" y={index * 28 + 20}>
                    {row.raw.slice(0, 15)}
                  </text>
                </g>
              );
            })}
          </svg>
        </div>
      ) : (
        <p className="wb-muted">没有可绘制的纯数值列。</p>
      )}
    </details>
  );
}
export function ExcelPreview({ artifact }: { artifact: Artifact }) {
  const query = useQuery({
    queryKey: ['platform', 'xlsx', artifact.id],
    queryFn: async ({ signal }) => {
      if (artifact.sizeBytes > 8 * 1024 * 1024)
        throw new Error('Excel 预览上限 8 MB，请下载查看。');
      return readSpreadsheet(await artifactBlob(artifact.id, signal));
    },
    retry: false,
    gcTime: 0,
  });
  return (
    <QueryState pending={query.isPending} error={query.error} retry={query.refetch}>
      {query.data && (
        <>
          <p className="wb-muted">
            {query.data.name} · 前 50 行、20
            列以内。公式不执行，只显示缓存值；加密、过大或不支持的结构请下载查看。
          </p>
          <div className="wb-table-wrap">
            <table className="wb-table">
              <thead>
                <tr>
                  {query.data.columns.map((column, index) => (
                    <th key={index}>{column}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {query.data.rows.map((row, index) => (
                  <tr key={index}>
                    {row.map((cell, column) => (
                      <td key={column}>{cell}</td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <DataChart
            columns={query.data.columns}
            rows={query.data.rows}
            truncated={query.data.truncated}
          />
        </>
      )}
    </QueryState>
  );
}
export function BatchDownload({ artifacts }: { artifacts: Artifact[] }) {
  const [done, setDone] = useState(0);
  const mutation = useMutation({
    mutationFn: async () => {
      if (!artifacts.length || artifacts.length > 10) throw new Error('一次选择 1–10 个可用文件。');
      if (
        artifacts.some(
          (file) => file.status !== 'ready' || new Date(file.expiresAt).getTime() <= Date.now(),
        )
      )
        throw new Error('所选文件含未就绪或已过期项。');
      if (artifacts.reduce((sum, file) => sum + file.sizeBytes, 0) > 20 * 1024 * 1024)
        throw new Error('单次批量下载上限为 20 MB。');
      setDone(0);
      let bytes = 0;
      const files: { name: string; bytes: Uint8Array }[] = [];
      for (const artifact of artifacts) {
        const blob = await fetchArtifactDownload(artifact.id);
        bytes += blob.size;
        if (bytes > 20 * 1024 * 1024) throw new Error('实际下载总大小超过 20 MB，已停止打包。');
        files.push({ name: artifact.name, bytes: new Uint8Array(await blob.arrayBuffer()) });
        setDone(files.length);
      }
      files.push({
        name: 'artifact-manifest.json',
        bytes: new TextEncoder().encode(
          JSON.stringify(
            artifacts.map((file) => ({
              id: file.id,
              name: file.name,
              sha256: file.sha256,
              projectId: file.projectId,
              runId: file.runId,
              expiresAt: file.expiresAt,
            })),
            null,
            2,
          ),
        ),
      });
      saveBlob('agentlab-artifacts.zip', makeZip(files));
    },
    retry: false,
  });
  return (
    <div>
      <button
        className="wb-button wb-button--secondary"
        disabled={!artifacts.length || mutation.isPending}
        onClick={() => mutation.mutate()}
      >
        {mutation.isPending
          ? `下载并打包 ${done}/${artifacts.length}`
          : `批量下载 ZIP（${artifacts.length}）`}
      </button>
      <small className="wb-field-help">
        当前页选择，最多 10 个文件 / 20 MB；逐项重新授权后打包，附文件摘要清单。
      </small>
      <MutationError error={mutation.error} />
    </div>
  );
}
export function RunEvidence({ run }: { run: Run }) {
  const [bounded, setBounded] = useState(false);
  const mutation = useMutation({
    mutationFn: async () => {
      const fresh = await get<Run>(`/runs/${encodeURIComponent(run.id)}`);
      const [calls, artifacts, usage, ledger] = await Promise.all([
        get<{ items: ToolCall[] }>(`/runs/${encodeURIComponent(run.id)}/tool-calls`),
        get<Page<Artifact>>('/artifacts', { projectId: run.projectId, runId: run.id, limit: 100 }),
        get<Page<UsageLine>>('/usage', { projectId: run.projectId, runId: run.id, limit: 100 }),
        get<Page<Ledger>>('/billing/ledger', {
          projectId: run.projectId,
          runId: run.id,
          limit: 100,
        }),
      ]);
      let cursor = 0;
      let page: EventPage;
      const events: EventPage['items'] = [];
      do {
        page = await get<EventPage>(`/runs/${encodeURIComponent(run.id)}/events`, {
          after: cursor,
          limit: 100,
        });
        events.push(
          ...page.items.map((event) => ({
            ...event,
            message: stripTerminalControls(event.message),
          })),
        );
        if (page.cursor <= cursor) break;
        cursor = page.cursor;
      } while (page.items.length === 100 && events.length < 1000);
      const partial =
        !!artifacts.nextCursor ||
        !!usage.nextCursor ||
        !!ledger.nextCursor ||
        events.length >= 1000;
      setBounded(partial);
      saveText(
        `${run.id}-evidence.json`,
        JSON.stringify(
          {
            generatedAt: new Date().toISOString(),
            scope:
              '读取时点快照；最多100条各类明细和1000个事件。后续页由 nextCursor 标记，原始产物另行下载。',
            partial,
            run: fresh,
            toolCalls: calls.items,
            artifacts,
            usage,
            ledger,
            events: { items: events, cursor, terminal: page.terminal },
          },
          null,
          2,
        ),
        'application/json;charset=utf-8',
      );
    },
    retry: false,
  });
  return (
    <div>
      <button
        className="wb-button wb-button--secondary"
        disabled={mutation.isPending}
        onClick={() => mutation.mutate()}
      >
        {mutation.isPending ? '整理证据中…' : '导出运行证据包 JSON'}
      </button>
      <small className="wb-field-help">
        快照含版本、结果、事件、调用、产物摘要和用量账本；原始文件另行下载。
      </small>
      {bounded && (
        <p className="wb-field-error">部分明细仍有后续页，证据包内已记录游标，不能视为完整历史。</p>
      )}
      <MutationError error={mutation.error} />
    </div>
  );
}
