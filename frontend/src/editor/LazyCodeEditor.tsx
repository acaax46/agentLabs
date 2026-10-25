import { lazy, Suspense } from 'react';
import type { CodeEditorProps } from './CodeEditor';

const CodeEditor = lazy(() => import('./CodeEditor'));

export function LazyCodeEditor(props: CodeEditorProps) {
  return (
    <Suspense
      fallback={
        <div className="code-editor-loading" role="status">
          正在加载代码编辑器…
        </div>
      }
    >
      <CodeEditor {...props} />
    </Suspense>
  );
}
