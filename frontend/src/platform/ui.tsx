import { useEffect, useId, useRef, useState } from 'react';
import type { CSSProperties, ReactNode } from 'react';
import type { BillingStatus, RunStatus, ToolCallStatus } from './model';
import { billingStatusLabels, runStatusLabels, toolCallStatusLabels } from './model';
import { useFeedback } from './feedback';

export type IconName =
  | 'overview'
  | 'projects'
  | 'agents'
  | 'tools'
  | 'runs'
  | 'connections'
  | 'keys'
  | 'usage'
  | 'artifacts'
  | 'settings'
  | 'plus'
  | 'search'
  | 'chevronRight'
  | 'chevronDown'
  | 'menu'
  | 'close'
  | 'logout'
  | 'user'
  | 'copy'
  | 'check'
  | 'warning'
  | 'error'
  | 'clock'
  | 'play'
  | 'stop'
  | 'file'
  | 'download'
  | 'external'
  | 'shield'
  | 'terminal'
  | 'filter'
  | 'refresh'
  | 'arrowRight'
  | 'dots'
  | 'sparkles'
  | 'layers'
  | 'database'
  | 'cpu'
  | 'fork'
  | 'eye'
  | 'eyeOff'
  | 'upload';

const paths: Record<IconName, ReactNode> = {
  upload: (
    <>
      <path d="M12 16V3m-5 5 5-5 5 5" />
      <path d="M4 15v5h16v-5" />
    </>
  ),
  eyeOff: (
    <>
      <path d="m3 3 18 18M10 5.2 12 5c5 0 9 7 9 7l-3 3M6 6c-3 2-5 6-5 6s4 7 11 7c2 0 4-.7 6-2" />
      <path d="M10 10a3 3 0 0 0 4 4" />
    </>
  ),
  overview: (
    <>
      <rect x="3" y="3" width="7" height="7" rx="1.2" />
      <rect x="14" y="3" width="7" height="7" rx="1.2" />
      <rect x="3" y="14" width="7" height="7" rx="1.2" />
      <rect x="14" y="14" width="7" height="7" rx="1.2" />
    </>
  ),
  projects: (
    <path d="M3 7.5A1.5 1.5 0 0 1 4.5 6H9l2.2 2.2h8.3A1.5 1.5 0 0 1 21 9.7v7.8a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 17.5z" />
  ),
  agents: (
    <>
      <path d="m11 3 1.7 4.8L17.5 9.5l-4.8 1.7L11 16l-1.7-4.8L4.5 9.5l4.8-1.7z" />
      <path d="m18 14 .9 2.6 2.6.9-2.6.9L18 21l-.9-2.6-2.6-.9 2.6-.9z" />
    </>
  ),
  tools: (
    <path d="M14.8 5.2a4.2 4.2 0 0 0-5.6 5.6L4 16l3 3 5.2-5.2a4.2 4.2 0 0 0 5.6-5.6l-2.5 2.5-2.9-.1-.1-2.9z" />
  ),
  runs: (
    <>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="m10 9 5 3-5 3z" />
    </>
  ),
  connections: (
    <>
      <path d="M10 13a5 5 0 0 0 7.1 0l2-2A5 5 0 0 0 12 4l-1.1 1.1" />
      <path d="M14 11a5 5 0 0 0-7.1 0l-2 2A5 5 0 0 0 12 20l1.1-1.1" />
    </>
  ),
  keys: (
    <>
      <circle cx="8" cy="8" r="4" />
      <path d="m11 11 9 9M16 16l2-2M18.5 18.5l2-2" />
    </>
  ),
  usage: (
    <>
      <path d="M4 20V10M10 20V4M16 20v-7M22 20H2" />
    </>
  ),
  artifacts: (
    <>
      <path d="m12 3 8 4.5v9L12 21l-8-4.5v-9z" />
      <path d="m4 7.5 8 4.5 8-4.5M12 12v9" />
    </>
  ),
  settings: (
    <>
      <path d="M4 7h9M17 7h3M4 17h3M11 17h9" />
      <circle cx="15" cy="7" r="2" />
      <circle cx="9" cy="17" r="2" />
    </>
  ),
  plus: <path d="M12 5v14M5 12h14" />,
  search: (
    <>
      <circle cx="11" cy="11" r="7" />
      <path d="m16.5 16.5 4 4" />
    </>
  ),
  chevronRight: <path d="m9 6 6 6-6 6" />,
  chevronDown: <path d="m6 9 6 6 6-6" />,
  menu: <path d="M4 6h16M4 12h16M4 18h16" />,
  close: <path d="m6 6 12 12M18 6 6 18" />,
  logout: (
    <>
      <path d="M10 4H5a1 1 0 0 0-1 1v14a1 1 0 0 0 1 1h5" />
      <path d="M14 8l4 4-4 4M18 12H9" />
    </>
  ),
  user: (
    <>
      <circle cx="12" cy="8" r="4" />
      <path d="M4.5 21a7.5 7.5 0 0 1 15 0" />
    </>
  ),
  copy: (
    <>
      <rect x="8" y="8" width="11" height="11" rx="1.5" />
      <path d="M16 8V5.5A1.5 1.5 0 0 0 14.5 4h-9A1.5 1.5 0 0 0 4 5.5v9A1.5 1.5 0 0 0 5.5 16H8" />
    </>
  ),
  check: <path d="m5 12 4.5 4.5L19 7" />,
  warning: (
    <>
      <path d="M12 3 2.5 20h19z" />
      <path d="M12 9v5M12 17.5v.1" />
    </>
  ),
  error: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="m9 9 6 6M15 9l-6 6" />
    </>
  ),
  clock: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7v5l3 2" />
    </>
  ),
  play: <path d="m8 5 11 7-11 7z" />,
  stop: <rect x="6" y="6" width="12" height="12" rx="1" />,
  file: (
    <>
      <path d="M6 3h8l4 4v14H6z" />
      <path d="M14 3v5h4" />
    </>
  ),
  download: (
    <>
      <path d="M12 4v11M7 10l5 5 5-5" />
      <path d="M4 20h16" />
    </>
  ),
  external: (
    <>
      <path d="M14 4h6v6M20 4l-9 9" />
      <path d="M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5" />
    </>
  ),
  shield: (
    <>
      <path d="M12 3 5 6v6c0 4 3 7 7 9 4-2 7-5 7-9V6z" />
      <path d="m9 12 2 2 4-4" />
    </>
  ),
  terminal: (
    <>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="m7 9 3 3-3 3M13 15h4" />
    </>
  ),
  filter: <path d="M3 5h18l-7 8v6l-4 2v-8z" />,
  refresh: (
    <>
      <path d="M20 11a8 8 0 0 0-14.9-3M4 13a8 8 0 0 0 14.9 3" />
      <path d="M20 4v7h-7M4 20v-7h7" />
    </>
  ),
  arrowRight: <path d="M4 12h16m-6-6 6 6-6 6" />,
  dots: (
    <>
      <circle cx="5" cy="12" r="1" />
      <circle cx="12" cy="12" r="1" />
      <circle cx="19" cy="12" r="1" />
    </>
  ),
  sparkles: (
    <>
      <path d="m12 3 1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8z" />
      <path d="m19 16 .7 2.3L22 19l-2.3.7L19 22l-.7-2.3L16 19l2.3-.7z" />
    </>
  ),
  layers: (
    <>
      <path d="m12 3 9 5-9 5-9-5z" />
      <path d="m3 12 9 5 9-5M3 16l9 5 9-5" />
    </>
  ),
  database: (
    <>
      <ellipse cx="12" cy="6" rx="8" ry="3" />
      <path d="M4 6v6c0 1.7 3.6 3 8 3s8-1.3 8-3V6" />
      <path d="M4 12v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6" />
    </>
  ),
  cpu: (
    <>
      <rect x="6" y="6" width="12" height="12" rx="2" />
      <path d="M9 1v3M15 1v3M9 20v3M15 20v3M1 9h3M1 15h3M20 9h3M20 15h3" />
    </>
  ),
  fork: (
    <>
      <circle cx="6" cy="5" r="2" />
      <circle cx="18" cy="5" r="2" />
      <circle cx="12" cy="19" r="2" />
      <path d="M6 7v2a3 3 0 0 0 3 3h6a3 3 0 0 0 3-3V7M12 12v5" />
    </>
  ),
  eye: (
    <>
      <path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12z" />
      <circle cx="12" cy="12" r="3" />
    </>
  ),
};

export function Icon({
  name,
  size = 20,
  className = '',
}: {
  name: IconName;
  size?: number;
  className?: string;
}) {
  return (
    <svg
      className={`wb-icon ${className}`}
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {paths[name]}
    </svg>
  );
}

export type BadgeTone =
  | 'neutral'
  | 'primary'
  | 'accent'
  | 'success'
  | 'warning'
  | 'danger'
  | 'info';

export function StatusBadge({
  tone = 'neutral',
  children,
  dot = true,
}: {
  tone?: BadgeTone;
  children: ReactNode;
  dot?: boolean;
}) {
  return (
    <span className={`wb-badge wb-badge--${tone}`}>
      {dot && <span className="wb-badge-dot" aria-hidden="true" />}
      {children}
    </span>
  );
}

export function runTone(status: RunStatus): BadgeTone {
  switch (status) {
    case 'succeeded':
      return 'success';
    case 'running':
    case 'preparing':
      return 'primary';
    case 'queued':
      return 'info';
    case 'failed':
      return 'danger';
    case 'unknown':
    case 'cancelling':
      return 'warning';
    case 'cancelled':
      return 'neutral';
  }
}

export function runBadge(status: RunStatus) {
  return <StatusBadge tone={runTone(status)}>{runStatusLabels[status]}</StatusBadge>;
}

export function billingTone(status: BillingStatus): BadgeTone {
  switch (status) {
    case 'settled':
      return 'success';
    case 'estimated':
      return 'warning';
    case 'pending':
      return 'info';
    case 'not_applicable':
      return 'neutral';
  }
}

export function billingBadge(status: BillingStatus) {
  return <StatusBadge tone={billingTone(status)}>{billingStatusLabels[status]}</StatusBadge>;
}

export function toolCallTone(status: ToolCallStatus): BadgeTone {
  switch (status) {
    case 'succeeded':
      return 'success';
    case 'dispatched':
      return 'primary';
    case 'waiting_approval':
      return 'warning';
    case 'failed':
      return 'danger';
    case 'unknown':
      return 'warning';
    case 'planned':
      return 'info';
  }
}

export function ToolCallBadge({ status }: { status: ToolCallStatus }) {
  return <StatusBadge tone={toolCallTone(status)}>{toolCallStatusLabels[status]}</StatusBadge>;
}

export function PageHeader({
  eyebrow,
  title,
  description,
  actions,
  meta,
}: {
  eyebrow?: string;
  title: string;
  description?: ReactNode;
  actions?: ReactNode;
  meta?: ReactNode;
}) {
  return (
    <header className="wb-page-head">
      <div className="wb-page-head-copy">
        {eyebrow && (
          <div className="wb-eyebrow">
            <span className="wb-eyebrow-mark" aria-hidden="true" />
            {eyebrow}
          </div>
        )}
        <h1>{title}</h1>
        {description && <p>{description}</p>}
        {meta && <div className="wb-page-meta">{meta}</div>}
      </div>
      {actions && <div className="wb-page-actions">{actions}</div>}
    </header>
  );
}

export function SectionHeader({
  title,
  description,
  action,
}: {
  title: string;
  description?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="wb-section-head">
      <div>
        <h2>{title}</h2>
        {description && <p>{description}</p>}
      </div>
      {action}
    </div>
  );
}

export function EmptyState({
  icon = 'layers',
  title,
  description,
  action,
}: {
  icon?: IconName;
  title: string;
  description: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="wb-empty">
      <span className="wb-empty-icon">
        <Icon name={icon} size={22} />
      </span>
      <h2>{title}</h2>
      <p>{description}</p>
      {action}
    </div>
  );
}

export function ErrorState({
  title = '暂时无法完成操作',
  description = '请稍后重试。已发生的操作结果可能尚未确认，请先核对再重试。',
  action,
}: {
  title?: string;
  description?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="wb-error" role="alert">
      <Icon name="error" size={22} />
      <div>
        <h2>{title}</h2>
        <p>{description}</p>
        {action}
      </div>
    </div>
  );
}

export function SpendingMeter({
  spent,
  budget,
  label,
}: {
  spent: number;
  budget: number;
  label: string;
}) {
  const ratio = budget > 0 ? Math.min(1, spent / budget) : 0;
  const percent = budget > 0 ? Math.min(100, (spent / budget) * 100) : 0;
  const tone = ratio >= 0.9 ? 'danger' : ratio >= 0.7 ? 'warning' : 'primary';
  return (
    <div
      className={`wb-meter wb-meter--${tone}`}
      role="img"
      aria-label={`${label}，已使用 ${percent.toFixed(1)}%`}
    >
      <span style={{ width: `${percent}%` }} />
    </div>
  );
}

export function CodeBlock({
  children,
  copy,
  label,
}: {
  children: string;
  copy?: boolean;
  label?: string;
}) {
  return (
    <div className="wb-code">
      <div className="wb-code-bar">
        <span>{label ?? 'JSON'}</span>
        {copy && <CopyButton value={children} label="复制" />}
      </div>
      <pre>
        <code>{children}</code>
      </pre>
    </div>
  );
}

export function CopyButton({
  value,
  label = '复制',
  className = 'wb-button wb-button--ghost wb-button--sm',
}: {
  value: string;
  label?: string;
  className?: string;
}) {
  const [copied, setCopied] = useState(false);
  const notify = useFeedback();
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
    } catch {
      const area = document.createElement('textarea');
      area.value = value;
      area.setAttribute('readonly', '');
      area.style.position = 'fixed';
      area.style.opacity = '0';
      document.body.appendChild(area);
      area.select();
      document.execCommand('copy');
      area.remove();
    }
    setCopied(true);
    notify('已复制');
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setCopied(false), 1600);
  };
  return (
    <button type="button" className={className} onClick={() => void copy()}>
      <Icon name={copied ? 'check' : 'copy'} size={15} />
      {copied ? '已复制' : label}
    </button>
  );
}

export function Modal({
  open,
  title,
  description,
  children,
  onClose,
  size = 'md',
}: {
  open: boolean;
  title: string;
  description?: ReactNode;
  children: ReactNode;
  onClose: () => void;
  size?: 'sm' | 'md' | 'lg';
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const opener = useRef<HTMLElement | null>(null);
  useEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;
    if (open && !dialog.open) {
      opener.current = document.activeElement as HTMLElement;
      dialog.showModal();
      window.requestAnimationFrame(() => {
        dialog.querySelector<HTMLElement>('[data-initial-focus]')?.focus();
      });
    } else if (!open && dialog.open) {
      dialog.close();
    }
  }, [open]);
  return (
    <dialog
      ref={ref}
      className={`wb-dialog wb-dialog--${size}`}
      aria-labelledby={titleId}
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
      onClose={() => {
        if (open) onClose();
        opener.current?.focus();
      }}
    >
      <div className="wb-dialog-shell">
        <header className="wb-dialog-head">
          <div>
            <h2 id={titleId}>{title}</h2>
            {description && <p>{description}</p>}
          </div>
          <button
            type="button"
            className="wb-icon-button"
            onClick={onClose}
            aria-label="关闭对话框"
          >
            <Icon name="close" size={18} />
          </button>
        </header>
        <div className="wb-dialog-body">{children}</div>
      </div>
    </dialog>
  );
}

export function Divider() {
  return <div className="wb-divider" role="presentation" />;
}

export function KeyValue({
  items,
  columns = 2,
}: {
  items: { label: string; value: ReactNode; wide?: boolean }[];
  columns?: number;
}) {
  return (
    <dl className="wb-kv" style={{ '--wb-kv-columns': columns } as CSSProperties}>
      {items.map((item) => (
        <div key={item.label} className={item.wide ? 'is-wide' : undefined}>
          <dt>{item.label}</dt>
          <dd>{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}

export function Tabs<T extends string>({
  items,
  value,
  onChange,
  label,
}: {
  items: { value: T; label: string; count?: number }[];
  value: T;
  onChange: (value: T) => void;
  label: string;
}) {
  return (
    <div className="wb-tabs" role="tablist" aria-label={label}>
      {items.map((item) => (
        <button
          key={item.value}
          type="button"
          role="tab"
          tabIndex={value === item.value ? 0 : -1}
          aria-selected={value === item.value}
          className={value === item.value ? 'is-active' : undefined}
          onClick={() => onChange(item.value)}
          onKeyDown={(event) => {
            const index = items.findIndex((entry) => entry.value === item.value);
            const next =
              event.key === 'Home'
                ? 0
                : event.key === 'End'
                  ? items.length - 1
                  : event.key === 'ArrowRight'
                    ? (index + 1) % items.length
                    : event.key === 'ArrowLeft'
                      ? (index - 1 + items.length) % items.length
                      : -1;
            if (next < 0) return;
            event.preventDefault();
            onChange(items[next]!.value);
            event.currentTarget.parentElement
              ?.querySelectorAll<HTMLButtonElement>('[role="tab"]')
              [next]?.focus();
          }}
        >
          {item.label}
          {item.count !== undefined && <span>{item.count}</span>}
        </button>
      ))}
    </div>
  );
}
