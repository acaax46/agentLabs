import { useEffect, useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Link, useSearchParams } from 'react-router-dom';
import { downloadArtifact, get } from '../api';
import type { Artifact, Preview } from '../api';
import { usePlatform } from '../state';
import { formatBytes, formatDateTime } from '../model';
import { MutationError, QueryState, RequireProject, usePage } from '../shared';
import { Pagination } from '../controls';
import { IsolatedPreview, PngPreview } from '../preview';
import { CodeBlock, EmptyState, Modal, PageHeader, StatusBadge } from '../ui';
import { BatchDownload, DataChart, ExcelPreview } from '../ArtifactEnhancements';

function ArtifactPreview({ artifact }: { artifact: Artifact }) {
  const textSupported = ['csv', 'tsv', 'json', 'txt', 'md'].includes(artifact.type);
  const preview = useQuery({
    queryKey: ['platform', 'preview', artifact.id],
    queryFn: ({ signal }) =>
      get<Preview>(`/artifacts/${encodeURIComponent(artifact.id)}/preview`, {}, signal),
    enabled: artifact.status === 'ready' && textSupported,
    retry: false,
  });
  const download = useMutation({
    mutationFn: () => downloadArtifact(artifact.id, artifact.name),
    retry: false,
  });
  return (
    <div className="wb-artifact-detail">
      <dl className="wb-fact-list">
        <div>
          <dt>大小</dt>
          <dd>{formatBytes(artifact.sizeBytes)}</dd>
        </div>
        <div>
          <dt>SHA-256</dt>
          <dd className="wb-mono">{artifact.sha256}</dd>
        </div>
        <div>
          <dt>保留至</dt>
          <dd>{formatDateTime(artifact.expiresAt)}</dd>
        </div>
        <div>
          <dt>状态</dt>
          <dd>{artifact.status}</dd>
        </div>
      </dl>
      {artifact.status === 'ready' ? (
        artifact.type === 'xlsx' ? (
          <ExcelPreview artifact={artifact} />
        ) : artifact.type === 'png' ? (
          <PngPreview id={artifact.id} name={artifact.name} />
        ) : !textSupported ? (
          <p className="wb-muted">该类型仅提供下载。</p>
        ) : (
          <QueryState pending={preview.isPending} error={preview.error} retry={preview.refetch}>
            {preview.data && (
              <>
                {preview.data.columns ? (
                  <div className="wb-table-wrap">
                    <table className="wb-table">
                      <thead>
                        <tr>
                          {preview.data.columns.map((column, index) => (
                            <th key={index}>{column}</th>
                          ))}
                        </tr>
                      </thead>
                      <tbody>
                        {preview.data.rows?.map((row, index) => (
                          <tr key={index}>
                            {row.map((value, column) => (
                              <td key={column}>{value}</td>
                            ))}
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                ) : artifact.type === 'md' ? (
                  <IsolatedPreview
                    text={preview.data.text}
                    title={`${artifact.name} Markdown 预览`}
                  />
                ) : (
                  <CodeBlock label={`preview.${artifact.type}`} copy>
                    {preview.data.text ?? '该类型不提供文本预览，请下载查看。'}
                  </CodeBlock>
                )}
                {preview.data.truncated && <p className="wb-muted">预览已截断，完整内容请下载。</p>}
              </>
            )}
          </QueryState>
        )
      ) : (
        <p className="wb-muted">
          {artifact.status === 'expired'
            ? '文件已过期，不能预览或下载。'
            : '文件尚未就绪，不能预览或下载。'}
        </p>
      )}
      {artifact.status === 'ready' && preview.data?.columns && (
        <DataChart
          columns={preview.data.columns}
          rows={preview.data.rows ?? []}
          truncated={preview.data.truncated}
        />
      )}
      <p className="wb-field-help">保留期由服务端控制，当前尚未开放保留期调整。</p>
      <MutationError error={download.error} />
      <div className="wb-form-actions">
        <button
          className="wb-button wb-button--primary"
          disabled={artifact.status !== 'ready' || download.isPending}
          onClick={() => download.mutate()}
        >
          {download.isPending ? '正在授权…' : '下载产物'}
        </button>
      </div>
    </div>
  );
}

export function ArtifactList({
  projectId,
  runId,
  active = false,
}: {
  projectId: string;
  runId?: string;
  active?: boolean;
}) {
  const artifactPage = usePage<Artifact>('/artifacts', { projectId, runId }, !!projectId, active);
  const query = { ...artifactPage, data: artifactPage.data?.items };
  const [selected, setSelected] = useState<Artifact | null>(null);
  const [marked, setMarked] = useState<string[]>([]);
  const [params, setParams] = useSearchParams();
  const deepId = params.get('artifact') ?? '';
  const deep = useQuery({
    queryKey: ['platform', 'artifact', deepId],
    queryFn: ({ signal }) => get<Artifact>('/artifacts/' + encodeURIComponent(deepId), {}, signal),
    enabled: !!deepId,
    retry: false,
  });
  useEffect(() => {
    if (deepId && deep.data) setSelected(deep.data);
  }, [deepId, deep.data]);
  useEffect(() => setMarked([]), [projectId, runId, artifactPage.page]);
  const close = () => {
    setSelected(null);
    if (deepId) {
      const next = new URLSearchParams(params);
      next.delete('artifact');
      setParams(next, { replace: true });
    }
  };
  const [filter, setFilter] = useState('');
  const items =
    query.data?.filter((item) => item.name.toLowerCase().includes(filter.toLowerCase())) ?? [];
  return (
    <>
      <div className="wb-toolbar">
        <label className="wb-search">
          <input
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
            aria-label="搜索产物"
            placeholder="搜索文件名"
          />
        </label>
        <button className="wb-button wb-button--secondary" onClick={() => void query.refetch()}>
          刷新
        </button>
      </div>
      <BatchDownload
        artifacts={query.data?.filter((artifact) => marked.includes(artifact.id)) ?? []}
      />
      <MutationError error={deep.error} />
      <QueryState pending={query.isPending} error={query.error} retry={query.refetch}>
        {!items.length ? (
          <EmptyState title="暂无产物" description="上传的输入文件与运行输出将在此显示。" />
        ) : (
          <div className="wb-table-wrap">
            <table className="wb-table">
              <thead>
                <tr>
                  <th>选择</th>
                  <th>文件</th>
                  <th>类型 / 大小</th>
                  <th>来源</th>
                  <th>状态</th>
                  <th>到期时间</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {items.map((artifact) => (
                  <tr key={artifact.id}>
                    <td>
                      <label className="wb-check">
                        <input
                          aria-label={`选择产物 ${artifact.name}`}
                          type="checkbox"
                          checked={marked.includes(artifact.id)}
                          disabled={
                            artifact.status !== 'ready' ||
                            new Date(artifact.expiresAt).getTime() <= Date.now()
                          }
                          onChange={(event) =>
                            setMarked((current) =>
                              event.target.checked
                                ? [...current, artifact.id]
                                : current.filter((id) => id !== artifact.id),
                            )
                          }
                        />
                        <span className="wb-sr-only">选择</span>
                      </label>
                    </td>
                    <td>
                      <span className="wb-cell-stack">
                        <strong>{artifact.name}</strong>
                        <small>{artifact.summary}</small>
                      </span>
                    </td>
                    <td>
                      {artifact.type.toUpperCase()} / {formatBytes(artifact.sizeBytes)}
                    </td>
                    <td>
                      {artifact.runId ? (
                        <Link
                          className="wb-table-link wb-mono"
                          to={`${artifact.runId.startsWith('tc_') ? '/tool-calls' : '/runs'}/${artifact.runId}`}
                        >
                          {artifact.runId}
                        </Link>
                      ) : (
                        '上传输入'
                      )}
                    </td>
                    <td>
                      <StatusBadge tone={artifact.status === 'ready' ? 'success' : 'warning'}>
                        {
                          { ready: '可用', pending: '待上传', expired: '已过期', failed: '失败' }[
                            artifact.status
                          ]
                        }
                      </StatusBadge>
                    </td>
                    <td>{formatDateTime(artifact.expiresAt)}</td>
                    <td>
                      <button
                        className="wb-button wb-button--secondary"
                        onClick={() => setSelected(artifact)}
                      >
                        查看产物
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </QueryState>
      <Pagination
        nextCursor={artifactPage.data?.nextCursor}
        page={artifactPage.page}
        pending={artifactPage.isFetching}
        onNext={artifactPage.next}
        onPrevious={artifactPage.previous}
      />
      <Modal
        open={!!selected}
        onClose={close}
        title={selected?.name ?? '产物'}
        description={selected?.summary}
        size="lg"
      >
        {selected && (
          <ArtifactPreview
            key={selected.id}
            artifact={query.data?.find((artifact) => artifact.id === selected.id) ?? selected}
          />
        )}
      </Modal>
    </>
  );
}
export function ArtifactsPage() {
  const { projectId } = usePlatform();
  const [params] = useSearchParams();
  return (
    <>
      <PageHeader
        title="产物"
        description="读取后端文件元数据与受限文本预览。下载前校验归属并签发短时授权链接。"
      />
      <RequireProject>
        <section className="wb-panel">
          <ArtifactList
            key={projectId}
            projectId={projectId}
            runId={params.get('run') ?? undefined}
          />
        </section>
      </RequireProject>
    </>
  );
}
