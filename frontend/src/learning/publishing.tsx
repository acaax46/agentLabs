import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useParams } from 'react-router-dom';
import { EmptyState, ErrorPanel, Loading, Modal, Notice, PageHeading } from '../catalog/shared';
import { formatDate } from '../catalog/modules';
import { LazyMarkdown } from '../components/LazyMarkdown';
import { LazyCodeEditor } from '../editor/LazyCodeEditor';
import { learning } from './api';
import type { Release } from './api';
import { JudgeBundleChoices } from './judging';

export function PublishingPanel({
  id,
  version,
  canEdit,
  courseId,
}: {
  id: string;
  version: number;
  canEdit: boolean;
  courseId: string;
}) {
  const cache = useQueryClient();
  const [judges, setJudges] = useState<Record<string, string>>({});
  const [opened, setOpened] = useState(false),
    [starter, setStarter] = useState('');
  const query = useQuery({
    queryKey: ['learning', 'releases', id],
    queryFn: () => learning<Release[]>('releases', id),
  });
  const [key, setKey] = useState(crypto.randomUUID());
  const publish = useMutation({
    mutationFn: ({
      input,
      requestKey,
    }: {
      input: {
        version: number;
        files: { path: string; content: string }[];
        judges: Record<string, string>;
      };
      requestKey: string;
    }) => learning<Release>('publish', id, input, requestKey),
    onSuccess: () => {
      setOpened(false);
      setKey(crypto.randomUUID());
      void cache.invalidateQueries({ queryKey: ['learning'] });
    },
  });
  const revoke = useMutation({
    mutationFn: (releaseId: string) => learning('revoke', releaseId),
    onSuccess: () => void cache.invalidateQueries({ queryKey: ['learning'] }),
  });
  return (
    <section className="learning-panel">
      <div className="section-heading">
        <div>
          <h2>任务发布</h2>
          <p>发布会固定课程、题目、沙箱模板与初始文件；后续草稿编辑不影响已有代码空间。</p>
        </div>
        {canEdit && (
          <button
            className="button"
            onClick={() => {
              publish.reset();
              setKey(crypto.randomUUID());
              setOpened(true);
            }}
          >
            发布任务版本
          </button>
        )}
      </div>
      {query.isPending ? (
        <Loading />
      ) : query.isError ? (
        <ErrorPanel error={query.error} retry={() => void query.refetch()} />
      ) : query.data.length ? (
        <ul className="release-history">
          {query.data.map((r) => (
            <li key={r.id}>
              <Link to={`/learning/releases/${r.id}`}>发布版本 {r.revision}</Link>
              <span>
                {formatDate(r.publishedAt)} · {r.revoked ? '已撤回' : '已发布'}
              </span>
              {canEdit && !r.revoked && (
                <button
                  className="text-button danger-text"
                  onClick={() => {
                    if (
                      window.confirm(
                        '撤回此版本将阻止新的运行，并请求停止相关任务；用户的代码空间成果会保留。',
                      )
                    )
                      revoke.mutate(r.id);
                  }}
                >
                  撤回版本 {r.revision}
                </button>
              )}
            </li>
          ))}
        </ul>
      ) : (
        <p>尚未发布，用户的代码空间不可见。</p>
      )}
      {revoke.error && <ErrorPanel error={revoke.error} />}
      <Modal open={opened} onClose={() => setOpened(false)} title="发布任务版本">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            publish.mutate({
              input: {
                version,
                files: [{ path: 'main.py', content: starter }],
                judges: { ...judges },
              },
              requestKey: key,
            });
          }}
        >
          {publish.error && <ErrorPanel error={publish.error} />}
          <Notice>
            请确认班级、课程、题目和截止时间。用户将获得此刻的内容快照。发布后需要调整时，请编辑草稿并发布新版本。
          </Notice>
          <div className="field">
            <span className="field-label">初始 main.py</span>
            {opened && (
              <LazyCodeEditor
                value={starter}
                onChange={setStarter}
                path="main.py"
                ariaLabel="初始 main.py"
                height={320}
              />
            )}
          </div>
          <JudgeBundleChoices courseId={courseId} judges={judges} onChange={setJudges} />
          <div className="dialog-actions">
            <button className="button secondary" type="button" onClick={() => setOpened(false)}>
              取消
            </button>
            <button className="button" disabled={publish.isPending}>
              {publish.isPending ? '正在发布…' : '确认发布'}
            </button>
          </div>
        </form>
      </Modal>
    </section>
  );
}
export function LearningTasks() {
  const query = useQuery({
    queryKey: ['learning', 'tasks'],
    queryFn: () => learning<Release[]>('releases'),
  });
  return (
    <>
      <PageHeading
        eyebrow="管理 / 已发布内容"
        title="已发布任务"
        description="查看已发布的内容版本；代码空间保存的成果会在刷新和重新登录后保留。"
      />
      {query.isPending ? (
        <Loading />
      ) : query.isError ? (
        <ErrorPanel error={query.error} retry={() => void query.refetch()} />
      ) : query.data.length ? (
        <div className="portal-grid">
          {query.data.map((r) => (
            <Link className="portal-card" to={`/learning/releases/${r.id}`} key={r.id}>
              <span className="status-badge active">发布版本 {r.revision}</span>
              <h2>{r.snapshot.assignment.name}</h2>
              <p>
                {r.snapshot.course.name} · {r.snapshot.problems.length} 道题目
              </p>
              <span>查看任务</span>
            </Link>
          ))}
        </div>
      ) : (
        <EmptyState
          title="暂无已发布任务"
          description="内容发布后，任务会出现在这里。"
          icon="assignments"
        />
      )}
    </>
  );
}
export function ReleasePage() {
  const { id = '' } = useParams();
  const query = useQuery({
    queryKey: ['learning', 'release', id],
    queryFn: () => learning<Release>('release', id),
  });
  if (query.isPending) return <Loading />;
  if (query.isError) return <ErrorPanel error={query.error} />;
  const r = query.data;
  const fields = r.snapshot.assignment.fields as { deadline?: string; submissionLimit?: number };
  return (
    <>
      <PageHeading
        eyebrow="任务 / 发布版本"
        title={r.snapshot.assignment.name}
        description={`${r.snapshot.course.name} · 发布版本 ${r.revision}`}
      />
      <div className="record-meta">
        <span>发布于 {formatDate(r.publishedAt)}</span>
        <span>{r.revoked ? '此版本已撤回' : '已发布'}</span>
        <span>{fields.deadline ? `截止 ${formatDate(fields.deadline)}` : '无截止时间'}</span>
      </div>
      <section className="learning-panel">
        <h2>任务说明</h2>
        <LazyMarkdown
          value={
            r.snapshot.assignment.description ||
            r.snapshot.course.description ||
            '按照课程题目完成练习。'
          }
        />
        <p className="field-help">内容摘要 {r.digest.slice(0, 16)} · 当前版本不会随草稿修改变化</p>
      </section>
      {r.snapshot.problems.map((p) => {
        const f = p.fields as { statement?: string; samples?: { input: string; output: string }[] };
        return (
          <section className="learning-panel" key={p.id}>
            <h2>
              {p.code} · {p.name}
            </h2>
            <LazyMarkdown value={f.statement ?? ''} emptyText="尚未提供题面。" />
            {f.samples?.map((sample, i) => (
              <div className="sample-grid" key={i}>
                <div>
                  <h3>公开样例 {i + 1} 输入</h3>
                  <pre>{sample.input}</pre>
                </div>
                <div>
                  <h3>输出</h3>
                  <pre>{sample.output}</pre>
                </div>
              </div>
            ))}
          </section>
        );
      })}
    </>
  );
}
