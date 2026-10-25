import { useEffect, useRef, useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { get } from './api';
import { usePlatform } from './state';
import { MutationError } from './shared';
import { Icon, Modal } from './ui';

const destinations = [
  ['/overview', '概览'],
  ['/projects', '项目'],
  ['/agents', 'Agent'],
  ['/tools', '工具'],
  ['/tool-calls', '工具调用'],
  ['/runs', '运行'],
  ['/connections', '连接'],
  ['/keys', 'API 密钥'],
  ['/usage', '用量与账单'],
  ['/artifacts', '产物'],
  ['/settings', '设置'],
];
const kinds = [
  ['runs', 'Run'],
  ['tool-calls', 'ToolCall'],
  ['projects', '项目'],
  ['agents', 'Agent'],
  ['artifacts', '产物'],
];
export function CommandPalette() {
  const [open, setOpen] = useState(false);
  const [help, setHelp] = useState(false);
  const [query, setQuery] = useState('');
  const [kind, setKind] = useState('runs');
  const platform = usePlatform();
  const navigate = useNavigate();
  const list = useRef<HTMLDivElement>(null);
  const go = (path: string) => {
    setOpen(false);
    navigate(path);
  };
  const lookup = useMutation({
    mutationFn: async () => {
      const id = query.trim();
      if (!/^[\w.@-]{1,160}$/.test(id)) throw new Error('请输入完整资源 ID。');
      type Resource = { id: string; projectId?: string };
      const response = await get<Resource | { agent: Resource }>(
        `/${kind}/${encodeURIComponent(id)}`,
      );
      const result =
        kind === 'agents' && 'agent' in response ? response.agent : (response as Resource);
      if (!result.id) throw new Error('资源响应缺少 ID，请刷新后重试。');
      if (result.projectId) platform.selectProject(result.projectId);
      if (kind === 'projects') platform.selectProject(result.id);
      go(
        kind === 'artifacts'
          ? `/artifacts?project=${encodeURIComponent(result.projectId ?? '')}&artifact=${encodeURIComponent(result.id)}`
          : `/${kind}/${encodeURIComponent(result.id)}`,
      );
    },
    retry: false,
  });
  useEffect(() => {
    const shortcut = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        if (document.querySelector('dialog[open]') && !open) return;
        setQuery('');
        setOpen((value) => !value);
      }
      if (
        event.key === '?' &&
        !document.querySelector('dialog[open]') &&
        !(
          event.target instanceof HTMLElement &&
          event.target.closest('input,textarea,select,[contenteditable="true"]')
        )
      ) {
        event.preventDefault();
        setHelp(true);
      }
    };
    window.addEventListener('keydown', shortcut);
    return () => window.removeEventListener('keydown', shortcut);
  }, [open]);
  return (
    <>
      <button
        className="wb-button wb-button--ghost wb-button--sm wb-command-trigger"
        aria-label="打开命令面板"
        onClick={() => {
          lookup.reset();
          setOpen(true);
        }}
      >
        <Icon name="search" size={16} />
        <span>搜索与跳转</span>
        <kbd>Ctrl / ⌘ K</kbd>
      </button>
      <Modal
        open={open}
        onClose={() => setOpen(false)}
        title="搜索与跳转"
        description="页面快捷导航，或按完整 ID 查询有权访问的资源。"
      >
        <form
          className="wb-form"
          onSubmit={(event) => {
            event.preventDefault();
            lookup.mutate();
          }}
        >
          <label className="wb-field">
            <span>搜索页面或资源 ID</span>
            <input
              data-initial-focus
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'ArrowDown') {
                  event.preventDefault();
                  list.current?.querySelector<HTMLButtonElement>('button')?.focus();
                }
              }}
            />
          </label>
          <div className="wb-command-results" ref={list}>
            {destinations
              .filter(
                ([path, label]) =>
                  (!query ||
                    label!.toLowerCase().includes(query.toLowerCase()) ||
                    path!.includes(query)) &&
                  platform.readiness?.capabilities.modules?.[
                    path === '/tool-calls' ? 'tools' : path!.slice(1)
                  ] !== false,
              )
              .map(([path, label]) => (
                <button
                  className="wb-button wb-button--ghost"
                  type="button"
                  key={path}
                  onClick={() => go(path!)}
                >
                  {label}
                </button>
              ))}
          </div>
          <label className="wb-field">
            <span>ID 查询类型</span>
            <select value={kind} onChange={(event) => setKind(event.target.value)}>
              {kinds.map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
          </label>
          <MutationError error={lookup.error} />
          <button
            className="wb-button wb-button--secondary"
            disabled={!query.trim() || lookup.isPending}
          >
            {lookup.isPending ? '查询中…' : '按完整 ID 查询'}
          </button>
          <button type="button" className="wb-text-link" onClick={() => setHelp(true)}>
            快捷键说明
          </button>
        </form>
      </Modal>
      <Modal open={help} onClose={() => setHelp(false)} title="快捷键说明">
        <dl className="wb-fact-list">
          <div>
            <dt>Ctrl / ⌘ + K</dt>
            <dd>打开搜索与跳转</dd>
          </div>
          <div>
            <dt>?</dt>
            <dd>输入框外打开此说明</dd>
          </div>
          <div>
            <dt>Esc</dt>
            <dd>关闭当前弹窗或移动导航</dd>
          </div>
          <div>
            <dt>Tab / Shift + Tab</dt>
            <dd>依次移动键盘焦点</dd>
          </div>
          <div>
            <dt>Enter / Space</dt>
            <dd>激活按钮、展开详情或切换选项</dd>
          </div>
        </dl>
      </Modal>
    </>
  );
}
