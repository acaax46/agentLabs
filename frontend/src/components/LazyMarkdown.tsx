import { lazy, Suspense } from 'react';

const Markdown = lazy(() => import('./Markdown').then((module) => ({ default: module.Markdown })));

export function LazyMarkdown({
  value,
  className,
  emptyText = '尚未填写内容。',
}: {
  value: string;
  className?: string;
  emptyText?: string;
}) {
  if (!value.trim()) return <p className="markdown-empty text-muted">{emptyText}</p>;
  return (
    <Suspense
      fallback={
        <p className="markdown-empty text-muted" role="status">
          正在加载内容…
        </p>
      }
    >
      <Markdown value={value} className={className} emptyText={emptyText} />
    </Suspense>
  );
}
