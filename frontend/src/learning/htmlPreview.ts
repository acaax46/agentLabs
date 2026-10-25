import type { File as LearningFile } from './api';

export type HtmlPreview = {
  url: string;
  revoke: () => void;
};

function resolvePath(htmlPath: string, target: string) {
  if (/^(?:[a-z]+:|#)/i.test(target)) return '';
  const clean = target.split(/[?#]/, 1)[0] || '';
  const base = htmlPath.includes('/') ? htmlPath.slice(0, htmlPath.lastIndexOf('/') + 1) : '';
  const parts = `${base}${clean.replace(/^\.\//, '')}`.split('/');
  const stack: string[] = [];
  for (const part of parts) {
    if (!part || part === '.') continue;
    if (part === '..') stack.pop();
    else stack.push(part);
  }
  return stack.join('/');
}

function findFile(files: LearningFile[], path: string) {
  if (!path) return undefined;
  return (
    files.find((file) => file.path === path) ||
    files.find((file) => file.path.toLowerCase() === path.toLowerCase())
  );
}

export function buildHtmlPreview(files: LearningFile[]): HtmlPreview {
  const htmlFile =
    files.find((file) => file.path.toLowerCase() === 'index.html') ||
    files.find((file) => /\.html?$/i.test(file.path));
  const urls: string[] = [];
  const objectUrl = (content: string, type: string) => {
    const url = URL.createObjectURL(new Blob([content], { type }));
    urls.push(url);
    return url;
  };
  const revoke = () => {
    for (const url of urls) URL.revokeObjectURL(url);
  };
  if (!htmlFile) {
    return {
      url: objectUrl(
        '<!doctype html><meta charset="utf-8"><p>还没有可预览的 HTML 文件。</p>',
        'text/html',
      ),
      revoke,
    };
  }
  let html = htmlFile.content;
  const deferred: string[] = [];
  const csp =
    "<meta http-equiv=\"Content-Security-Policy\" content=\"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:; font-src data:; form-action 'none';\">";
  html = /<head[^>]*>/i.test(html)
    ? html.replace(/<head[^>]*>/i, (tag) => `${tag}\n    ${csp}`)
    : `${csp}\n${html}`;
  html = html.replace(/<link\b[^>]*>/gi, (tag) => {
    const href = /\bhref\s*=\s*["']([^"']+)["']/i.exec(tag)?.[1];
    if (!href || !/\.css(?:[?#].*)?$/i.test(href)) return tag;
    const file = findFile(files, resolvePath(htmlFile.path, href));
    return file ? `<style>\n${file.content}\n</style>` : tag;
  });
  html = html.replace(
    /<script\b([^>]*?)\bsrc\s*=\s*["']([^"']+)["']([^>]*)>([\s\S]*?)<\/script>/gi,
    (tag, before: string, src: string, after: string, body: string) => {
      const file = findFile(files, resolvePath(htmlFile.path, src));
      if (!file) return tag;
      if (/\bdefer\b/i.test(`${before}${after}`)) {
        deferred.push(file.content);
        return '';
      }
      return `<script${before}${after}>\n${file.content}\n${body}</script>`;
    },
  );
  if (deferred.length) {
    const scripts = deferred.map((content) => `<script>\n${content}\n</script>`).join('\n');
    html = /<\/body>/i.test(html)
      ? html.replace(/<\/body>/i, `${scripts}\n</body>`)
      : `${html}\n${scripts}`;
  }
  return { url: objectUrl(html, 'text/html'), revoke };
}
