import 'katex/dist/katex.min.css';
import ReactMarkdown from 'react-markdown';
import type { Components } from 'react-markdown';
import rehypeHighlight from 'rehype-highlight';
import rehypeKatex from 'rehype-katex';
import remarkGfm from 'remark-gfm';
import remarkMath from 'remark-math';

const components: Components = {
  a({ node: _node, href, children, ...props }) {
    const external = !!href && /^https?:\/\//i.test(href);
    return (
      <a
        {...props}
        href={href}
        target={external ? '_blank' : undefined}
        rel={external ? 'noreferrer noopener' : undefined}
      >
        {children}
      </a>
    );
  },
  table({ node: _node, children, ...props }) {
    return (
      <div className="markdown-table">
        <table {...props}>{children}</table>
      </div>
    );
  },
  input({ node: _node, ...props }) {
    return <input {...props} disabled readOnly />;
  },
  img({ node: _node, alt, ...props }) {
    return <img {...props} alt={alt || ''} loading="lazy" decoding="async" />;
  },
};

export function Markdown({
  value,
  className = '',
  emptyText = '尚未填写内容。',
}: {
  value: string;
  className?: string;
  emptyText?: string;
}) {
  if (!value.trim()) return <p className="markdown-empty text-muted">{emptyText}</p>;
  return (
    <div className={`markdown ${className}`.trim()}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm, remarkMath]}
        rehypePlugins={[
          [rehypeKatex, { throwOnError: false, strict: false }],
          [rehypeHighlight, { detect: false }],
        ]}
        components={components}
      >
        {value}
      </ReactMarkdown>
    </div>
  );
}
