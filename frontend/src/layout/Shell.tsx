import { useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { useMutation } from '@tanstack/react-query';
import { Link, NavLink, useLocation, useNavigate } from 'react-router-dom';
import { roleLabels, useAuth } from '../auth/context';
import { usePlatform } from '../platform/state';
import { EmptyState, Icon } from '../platform/ui';
import type { IconName } from '../platform/ui';
import { CommandPalette } from '../platform/CommandPalette';

type Item = { href: string; label: string; icon: IconName; badge?: number };
type Group = { id: string; label: string; items: Item[] };

const groupDefinitions: { id: string; label: string; items: Omit<Item, 'badge'>[] }[] = [
  {
    id: 'workspace',
    label: '工作台',
    items: [
      { href: '/chat', label: '对话', icon: 'agents' },
      { href: '/overview', label: '概览', icon: 'overview' },
      { href: '/projects', label: '项目', icon: 'projects' },
      { href: '/agents', label: 'Agent', icon: 'agents' },
      { href: '/tools', label: '工具', icon: 'tools' },
      { href: '/tool-calls', label: '工具调用', icon: 'tools' },
      { href: '/runs', label: '运行', icon: 'runs' },
    ],
  },
  {
    id: 'access',
    label: '访问与计量',
    items: [
      { href: '/connections', label: '连接', icon: 'connections' },
      { href: '/keys', label: 'API 密钥', icon: 'keys' },
      { href: '/usage', label: '用量与账单', icon: 'usage' },
      { href: '/artifacts', label: '产物', icon: 'artifacts' },
    ],
  },
];

function detailLabel(pathname: string) {
  if (/^\/projects\/[^/]+$/.test(pathname)) return '项目详情';
  if (/^\/agents\/[^/]+$/.test(pathname)) return 'Playground';
  if (/^\/runs\/[^/]+$/.test(pathname)) return '运行详情';
  if (/^\/tool-calls\/[^/]+$/.test(pathname)) return '调用详情';
  return null;
}

export function Shell({ children }: { children: ReactNode }) {
  const auth = useAuth();
  const user = auth.user!;
  const platform = usePlatform();
  const { projects, selectProject, me } = platform;
  const location = useLocation();
  const navigate = useNavigate();
  const [mobile, setMobile] = useState(() => window.innerWidth < 768);
  const [opened, setOpened] = useState(false);
  const [collapsedGroups, setCollapsedGroups] = useState<Record<string, boolean>>({});
  const sidebar = useRef<HTMLElement>(null);
  const menu = useRef<HTMLButtonElement>(null);
  const modules = platform.readiness?.capabilities.modules;

  const groups = useMemo<Group[]>(
    () =>
      groupDefinitions.map((group) => ({
        ...group,
        items: group.items
          .filter(
            (item) =>
              modules?.[item.href === '/tool-calls' ? 'tools' : item.href.slice(1)] !== false,
          )
          .map((item) =>
            item.href === '/runs'
              ? {
                  ...item,
                  badge: undefined,
                }
              : item,
          ),
      })),
    [modules],
  );

  const active = groupDefinitions
    .flatMap((group) => group.items)
    .find(
      (item) => location.pathname === item.href || location.pathname.startsWith(`${item.href}/`),
    );
  const detail = detailLabel(location.pathname);
  const pageLabel = location.pathname === '/settings' ? '设置' : (active?.label ?? '概览');
  const signout = useMutation({
    mutationFn: auth.signOut,
    onSuccess: () => navigate('/login', { replace: true }),
  });

  useEffect(() => {
    setOpened(false);
  }, [location.pathname]);

  useEffect(() => {
    if (location.hash) return;
    window.scrollTo({ top: 0, left: 0 });
  }, [location.pathname, location.hash]);

  useEffect(() => {
    const changed = () => {
      setMobile(window.innerWidth < 768);
      if (window.innerWidth >= 768) setOpened(false);
    };
    window.addEventListener('resize', changed);
    return () => window.removeEventListener('resize', changed);
  }, []);

  useEffect(() => {
    if (!mobile || !opened) return;
    const previous = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    const focusFrame = window.requestAnimationFrame(() => {
      sidebar.current?.querySelector<HTMLElement>('a,button')?.focus();
    });
    const trap = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setOpened(false);
        window.requestAnimationFrame(() => menu.current?.focus());
      }
      if (event.key !== 'Tab') return;
      const targets = sidebar.current?.querySelectorAll<HTMLElement>(
        'a[href],button:not(:disabled)',
      );
      if (!targets?.length) return;
      const first = targets[0]!;
      const last = targets[targets.length - 1]!;
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener('keydown', trap);
    return () => {
      window.cancelAnimationFrame(focusFrame);
      document.body.style.overflow = previous;
      document.removeEventListener('keydown', trap);
    };
  }, [mobile, opened]);

  const closeMobile = () => {
    setOpened(false);
    window.requestAnimationFrame(() => menu.current?.focus());
  };

  useEffect(() => {
    const params = new URLSearchParams(location.search);
    const id = params.get('project');
    if (id && projects.some((project) => project.id === id)) selectProject(id);
  }, [location.search, projects, selectProject]);

  return (
    <div className="wb-shell">
      {mobile && opened && (
        <button className="wb-scrim" onClick={closeMobile} aria-label="关闭导航菜单" />
      )}
      <aside
        ref={sidebar}
        className={`wb-sidebar ${opened ? 'is-open' : ''}`}
        id="workspace-navigation"
        role={mobile ? 'dialog' : undefined}
        aria-modal={mobile && opened ? true : undefined}
        aria-label={mobile ? '工作台导航' : undefined}
        aria-hidden={mobile && !opened ? true : undefined}
        inert={mobile && !opened}
      >
        <div className="wb-brand-row">
          <Link to="/overview" className="wb-brand" aria-label="AgentLab 工作台首页">
            <img src={`${import.meta.env.BASE_URL}brand/logo-128.png`} alt="" />
            <span>
              <strong>AgentLab</strong>
              <small>托管 Agent 与 Tools API</small>
            </span>
          </Link>
          {mobile && (
            <button
              type="button"
              className="wb-icon-button"
              onClick={closeMobile}
              aria-label="关闭菜单"
            >
              <Icon name="close" size={18} />
            </button>
          )}
        </div>

        <div className="wb-workspace-card">
          <span className="wb-workspace-avatar" aria-hidden="true">
            AL
          </span>
          <div>
            <strong>{me?.workspace.name ?? '工作区'}</strong>
            <span>
              <i className="wb-live-dot" aria-hidden="true" />
              {me ? `工作区 · ${me.workspace.id}` : '正在读取工作区'}
            </span>
          </div>
          <Link to="/settings" className="wb-icon-button" aria-label="工作区设置">
            <Icon name="settings" size={16} />
          </Link>
        </div>

        <nav className="wb-nav" aria-label="主导航">
          {groups.map((group) => {
            const collapsed = collapsedGroups[group.id] === true;
            return (
              <div className="wb-nav-group" key={group.id}>
                <button
                  type="button"
                  className="wb-nav-group-label"
                  aria-expanded={!collapsed}
                  aria-controls={`nav-${group.id}`}
                  onClick={() =>
                    setCollapsedGroups((current) => ({
                      ...current,
                      [group.id]: !current[group.id],
                    }))
                  }
                >
                  <span>{group.label}</span>
                  <Icon name={collapsed ? 'chevronRight' : 'chevronDown'} size={14} />
                </button>
                <div id={`nav-${group.id}`} hidden={collapsed} className="wb-nav-items">
                  {group.items.map((item) => (
                    <NavLink
                      key={item.href}
                      to={item.href}
                      className={({ isActive }) => `wb-nav-link ${isActive ? 'is-active' : ''}`}
                    >
                      <Icon name={item.icon} size={18} />
                      <span>{item.label}</span>
                      {item.badge ? <em>{item.badge}</em> : null}
                    </NavLink>
                  ))}
                </div>
              </div>
            );
          })}
        </nav>

        <div className="wb-sidebar-foot">
          <NavLink to="/settings" className="wb-nav-link">
            <Icon name="settings" size={18} />
            <span>设置</span>
          </NavLink>
          <Link to="/settings#account" className="wb-account-chip">
            <span className="wb-account-avatar">{user.displayName.slice(0, 1)}</span>
            <span>
              <strong>{user.displayName}</strong>
              <small>{roleLabels[user.role]}</small>
            </span>
            <Icon name="chevronRight" size={14} />
          </Link>
          <button
            type="button"
            className="wb-signout"
            disabled={signout.isPending}
            onClick={() => signout.mutate()}
          >
            <Icon name="logout" size={16} />
            {signout.isPending ? '正在退出…' : '退出登录'}
          </button>
          {signout.error && (
            <p className="wb-field-error" role="alert">
              {signout.error.message}
            </p>
          )}
        </div>
      </aside>

      <main className="wb-main" inert={mobile && opened}>
        <header className="wb-topbar">
          <div className="wb-topbar-left">
            {mobile && (
              <button
                ref={menu}
                type="button"
                className="wb-icon-button"
                onClick={() => setOpened(true)}
                aria-label="打开导航菜单"
                aria-expanded={opened}
                aria-controls="workspace-navigation"
              >
                <Icon name="menu" size={20} />
              </button>
            )}
            <nav className="wb-breadcrumb" aria-label="当前位置">
              <Link to="/overview">工作台</Link>
              <Icon name="chevronRight" size={13} />
              <span>{pageLabel}</span>
              {detail && (
                <>
                  <Icon name="chevronRight" size={13} />
                  <span>{detail}</span>
                </>
              )}
            </nav>
          </div>
          <div className="wb-topbar-right">
            <CommandPalette />
            <label className="wb-project-switcher">
              <span className="wb-sr-only">当前项目</span>
              <select
                aria-label="当前项目"
                value={platform.projectId}
                disabled={platform.pending || !platform.projects.length}
                onChange={(event) => {
                  platform.selectProject(event.target.value);
                  if (location.search) navigate(location.pathname, { replace: true });
                }}
              >
                {!platform.projects.length && (
                  <option value="">{platform.pending ? '正在加载项目…' : '暂无项目'}</option>
                )}
                {platform.projects.map((project) => (
                  <option key={project.id} value={project.id}>
                    {project.name}
                  </option>
                ))}
              </select>
            </label>
            <Link className="wb-env-chip" to="/settings#mvp">
              <span aria-hidden="true" />
              {platform.readiness
                ? platform.readiness.ready
                  ? '服务就绪'
                  : '服务未就绪'
                : '能力未知'}
            </Link>
            <Link className="wb-topbar-user" to="/settings#account">
              <span className="wb-account-avatar">{user.displayName.slice(0, 1)}</span>
              <span>
                <strong>{user.displayName}</strong>
                <small>{roleLabels[user.role]}</small>
              </span>
            </Link>
          </div>
        </header>

        <div className="wb-mvp-strip" role="status">
          <Icon name="shield" size={15} />
          <p>
            <strong>平台接口</strong>
            项目、运行、密钥、用量与产物来自后端。试运行会预占预算，实际能力以服务状态为准。
          </p>
          <Link to="/settings#mvp">查看边界</Link>
        </div>

        <div className="wb-content" key={platform.projectId}>
          {active &&
          modules?.[active.href === '/tool-calls' ? 'tools' : active.href.slice(1)] === false ? (
            <EmptyState title="未开放" description="此部署尚未开放该功能。" />
          ) : (
            children
          )}
        </div>
      </main>
    </div>
  );
}
