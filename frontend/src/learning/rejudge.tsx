import { useMutation } from '@tanstack/react-query';
import { useAuth } from '../auth/context';
import { ErrorPanel } from '../catalog/shared';
import { learning } from './api';
import type { Job } from './api';
export function RejudgeAction({ jobId }: { jobId: string }) {
  const { state } = useAuth();
  const mutation = useMutation({
    mutationFn: () => learning<Job>('rejudge', jobId, {}, crypto.randomUUID()),
  });
  if (!state.permissions.catalogWrite) return null;
  return (
    <section className="learning-panel">
      <h2>重判</h2>
      <p>使用原始冻结源码和测试包新增结果版本，保留既有成绩记录。</p>
      <button
        className="button secondary"
        disabled={mutation.isPending}
        onClick={() => {
          if (window.confirm('为这份原始提交创建新的判题结果版本？')) mutation.mutate();
        }}
      >
        创建重判任务
      </button>
      {mutation.data && (
        <p role="status">
          已排队，结果版本 {mutation.data.resultVersion}。完成后可在评测记录查看。
        </p>
      )}
      {mutation.error && <ErrorPanel error={mutation.error} />}
    </section>
  );
}
