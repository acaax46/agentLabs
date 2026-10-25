import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { getRecord } from '../api/client';
import { ErrorPanel, Notice } from '../catalog/shared';
import { learning } from './api';
import type { components } from '../api/generated';
type Bundle = components['schemas']['LearningJudgeBundle'];
function BundleSelect({
  problemId,
  selected,
  onChange,
}: {
  problemId: string;
  selected: string;
  onChange: (id: string) => void;
}) {
  const query = useQuery({
    queryKey: ['learning', 'bundles', problemId],
    queryFn: () => learning<Bundle[]>('bundles', problemId),
  });
  const [text, setText] = useState(''),
    [editing, setEditing] = useState(false);
  const problem = useQuery({
    queryKey: ['catalog', 'record', 'problems', problemId],
    queryFn: () => getRecord('problems', problemId),
  });
  const save = useMutation({
    mutationFn: async () => {
      const cases: unknown = JSON.parse(text);
      if (!Array.isArray(cases)) throw new Error('请填写用例 JSON 数组。');
      return learning<Bundle>('bundle', '', { problemId, matcher: 'tokens', cases });
    },
    onSuccess: async (b) => {
      onChange(b.id);
      setText('');
      setEditing(false);
      await query.refetch();
    },
  });
  return (
    <section className="learning-panel">
      <label className="field">
        {problem.data?.name || '题目'} · 私有测试包
        <select
          aria-label={`题目 ${problemId} 的私有测试包`}
          value={selected}
          onChange={(e) => onChange(e.target.value)}
        >
          <option value="">仅公开练习，不开放正式提交</option>
          {query.data?.map((b) => (
            <option key={b.id} value={b.id}>
              {b.digest.slice(0, 12)} · {b.count} 个用例
            </option>
          ))}
        </select>
      </label>
      {query.isError && <ErrorPanel error={query.error} />}
      {problem.data?.canEdit && (
        <button className="text-button" type="button" onClick={() => setEditing(!editing)}>
          {editing ? '收起测试包编辑' : '新增私有测试包'}
        </button>
      )}
      {editing && (
        <>
          <Notice>
            用例只存入服务端私有集合。发布后固定版本，学生和执行 VM 不会收到标准答案或完整题包。
          </Notice>
          <label className="field">
            私有用例 JSON
            <textarea
              value={text}
              rows={8}
              onChange={(e) => setText(e.target.value)}
              placeholder={'[{"input":"…","expected":"…"}]'}
            />
          </label>
          <button
            type="button"
            className="button secondary"
            disabled={save.isPending || !text.trim()}
            onClick={() => save.mutate()}
          >
            保存测试包
          </button>
          {save.error && <ErrorPanel error={save.error} />}
        </>
      )}
    </section>
  );
}
export function JudgeBundleChoices({
  courseId,
  judges,
  onChange,
}: {
  courseId: string;
  judges: Record<string, string>;
  onChange: (value: Record<string, string>) => void;
}) {
  const course = useQuery({
    queryKey: ['catalog', 'record', 'courses', courseId],
    queryFn: () => getRecord('courses', courseId),
    enabled: !!courseId,
  });
  const problemIds =
    (course.data?.fields as { problemIds?: string[] } | undefined)?.problemIds || [];
  return (
    <>
      <h3>正式评测设置</h3>
      <p>为需要正式提交的题目关联私有测试包；留空时只提供公开练习。</p>
      {course.isError && <ErrorPanel error={course.error} />}{' '}
      {problemIds.map((id) => (
        <BundleSelect
          key={id}
          problemId={id}
          selected={judges[id] || ''}
          onChange={(value) => {
            const next = { ...judges };
            if (value) next[id] = value;
            else delete next[id];
            onChange(next);
          }}
        />
      ))}
    </>
  );
}
