import { useEffect, useId, useRef } from 'react';
import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { APIError } from '../api/client';
import { statusLabels } from './modules';

export function Icon({
  name,
  spot = false,
  className = '',
}: {
  name: string;
  spot?: boolean;
  className?: string;
}) {
  return (
    <img
      className={`icon ${spot ? 'spot-icon' : ''} ${className}`}
      src={`${import.meta.env.BASE_URL}icons/soft-clay/${name}.png`}
      alt=""
      aria-hidden="true"
    />
  );
}
export function Status({ value }: { value: string }) {
  return <span className={`status-badge ${value}`}>{statusLabels[value] || value}</span>;
}
export function Notice({
  children,
  tone = 'info',
  role,
}: {
  children: ReactNode;
  tone?: string;
  role?: 'alert' | 'status';
}) {
  return (
    <div className={`notice ${tone}`} role={role}>
      {children}
    </div>
  );
}
export function ErrorPanel({ error, retry }: { error: unknown; retry?: () => void }) {
  return (
    <section className="error-panel" role="alert">
      <h2>暂时无法完成操作</h2>
      <p>{error instanceof Error ? error.message : '请稍后重试。'}</p>
      {retry && (
        <button className="button secondary" onClick={retry}>
          重新加载
        </button>
      )}
    </section>
  );
}
export function Loading({ text = '正在读取资料…' }: { text?: string }) {
  return (
    <div className="loading-state" role="status">
      <span className="loading-dot" />
      {text}
    </div>
  );
}
export function EmptyState({
  title,
  description,
  href,
  action = '新建记录',
  icon = 'assignments',
}: {
  title: string;
  description: string;
  href?: string;
  action?: string;
  icon?: string;
}) {
  return (
    <div className="empty-state">
      <Icon name={icon} spot />
      <h2>{title}</h2>
      <p>{description}</p>
      {href && (
        <Link className="button" to={href}>
          {action}
        </Link>
      )}
    </div>
  );
}
export function FormError({ error }: { error: unknown }) {
  if (!error) return null;
  return (
    <Notice tone="danger" role="alert">
      {error instanceof Error ? error.message : '保存失败。'}
      {error instanceof APIError && error.code === 'VERSION_CONFLICT' && (
        <p>返回详情可查看最新版本，当前输入仍保留在此页。</p>
      )}
    </Notice>
  );
}
export function PageHeading({
  eyebrow,
  title,
  description,
  action,
}: {
  eyebrow: string;
  title: string;
  description?: string;
  action?: ReactNode;
}) {
  return (
    <header className="page-heading">
      <div>
        <div className="eyebrow">{eyebrow}</div>
        <h1>{title}</h1>
        {description && <p>{description}</p>}
      </div>
      {action}
    </header>
  );
}
export function Modal({
  open,
  title,
  children,
  onClose,
  busy = false,
}: {
  open: boolean;
  title: string;
  children: ReactNode;
  onClose: () => void;
  busy?: boolean;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const opener = useRef<HTMLElement | null>(null);
  useEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;
    if (open) {
      opener.current = document.activeElement as HTMLElement;
      dialog.showModal();
      dialog.querySelector<HTMLElement>('[data-initial-focus]')?.focus();
    } else if (dialog.open) {
      dialog.close();
      opener.current?.focus();
    }
  }, [open]);
  return (
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      onCancel={(event) => {
        event.preventDefault();
        if (!busy) onClose();
      }}
      onClose={() => {
        if (open && !busy) onClose();
      }}
    >
      <div className="modal-heading">
        <h2 id={titleId}>{title}</h2>
        <button
          className="icon-button"
          type="button"
          onClick={onClose}
          disabled={busy}
          aria-label="关闭弹窗"
        >
          关闭
        </button>
      </div>
      {children}
    </dialog>
  );
}
