import { useQuery } from '@tanstack/react-query';
import { renderToStaticMarkup } from 'react-dom/server';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { artifactBlob } from './api';
import { QueryState } from './shared';

export function IsolatedPreview({
  text,
  png,
  title,
}: {
  text?: string;
  png?: string;
  title: string;
}) {
  const theme = getComputedStyle(document.documentElement);
  const color = (name: string) => theme.getPropertyValue(`--al-color-${name}`).trim();
  const content = png
    ? `<img alt="产物图片预览" src="${png}" />`
    : renderToStaticMarkup(
        <ReactMarkdown
          remarkPlugins={[remarkGfm]}
          skipHtml
          components={{
            img: ({ alt }) => <span>[图片已阻止：{alt}]</span>,
            a: ({ children }) => <span>{children}</span>,
          }}
        >
          {text ?? ''}
        </ReactMarkdown>,
      );
  const html = `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'"><style>body{margin:16px;background:${color('workspace-bg')};color:${color('workspace-text')};font:14px/1.6 system-ui;overflow-wrap:anywhere}img{max-width:100%;height:auto}table{border-collapse:collapse}td,th{border:1px solid ${color('border')};padding:8px}pre{overflow:auto}code{font-family:monospace}</style>${content}</html>`;
  return (
    <iframe
      className="wb-isolated-preview"
      title={title}
      sandbox=""
      referrerPolicy="no-referrer"
      srcDoc={html}
    />
  );
}
export function PngPreview({ id, name }: { id: string; name: string }) {
  const query = useQuery({
    queryKey: ['platform', 'png', id],
    queryFn: async ({ signal }) => {
      const blob = await artifactBlob(id, signal);
      return new Promise<string>((resolve, reject) => {
        const reader = new FileReader();
        reader.onerror = () => reject(new Error('图片读取失败。'));
        reader.onload = () => resolve(String(reader.result));
        reader.readAsDataURL(new Blob([blob], { type: 'image/png' }));
      });
    },
    retry: false,
    gcTime: 0,
  });
  return (
    <QueryState pending={query.isPending} error={query.error} retry={query.refetch}>
      {query.data && <IsolatedPreview png={query.data} title={`${name} 图片预览`} />}
    </QueryState>
  );
}
