import { useState } from 'react';
import type { FormEvent, ReactNode } from 'react';
import { useMutation } from '@tanstack/react-query';
import { Link, Navigate, useNavigate } from 'react-router-dom';
import { ErrorState, Icon } from '../platform/ui';
import { roleHomes, useAuth } from './context';
import { Field } from '../platform/controls';
import { MutationError } from '../platform/shared';

export function Forbidden() {
  const { user } = useAuth();
  return (
    <section className="wb-access-panel">
      <span className="wb-empty-icon">
        <Icon name="shield" size={22} />
      </span>
      <h1>当前账号无权访问</h1>
      <p>此操作需要更高的工作区权限。资源归属与范围仍由服务端校验。</p>
      <Link className="wb-button wb-button--secondary" to={user ? roleHomes[user.role] : '/login'}>
        返回概览
      </Link>
    </section>
  );
}

export function Permit({ allowed, children }: { allowed: boolean; children: ReactNode }) {
  return allowed ? <>{children}</> : <Forbidden />;
}

export function AuthPages({ registration = false }: { registration?: boolean }) {
  const auth = useAuth();
  const navigate = useNavigate();
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [show, setShow] = useState(false);
  const mutation = useMutation({
    mutationFn: async () => {
      if (registration) await auth.signUp({ username, displayName: name, email, password });
      else await auth.signIn(username, password);
    },
    onSuccess: () => navigate('/overview', { replace: true }),
  });

  if (auth.pending) {
    return (
      <main className="wb-auth-loading" role="status">
        <span className="wb-spinner" />
        正在确认登录状态…
      </main>
    );
  }
  if (auth.user) return <Navigate to="/overview" replace />;

  const submit = (event: FormEvent) => {
    event.preventDefault();
    mutation.mutate();
  };

  return (
    <main className="wb-auth">
      <section className="wb-auth-story">
        <Link className="wb-brand wb-auth-brand" to="/login">
          <img src={`${import.meta.env.BASE_URL}brand/logo-128.png`} alt="" />
          <span>
            <strong>AgentLab</strong>
            <small>托管 Agent 与 Tools API 平台</small>
          </span>
        </Link>

        <div className="wb-auth-story-copy">
          <span className="wb-auth-kicker">
            <i className="wb-live-dot" aria-hidden="true" />
            HOSTED AGENT EXECUTION
          </span>
          <h1>
            选择模型与工具，
            <br />
            <span>托管执行任务。</span>
          </h1>
          <p>
            从项目开始配置
            Agent，授权工具并在隔离环境中运行。交付结构化结果、可复核的产物和分项费用。
          </p>
          <ul className="wb-auth-points">
            <li>
              <Icon name="layers" size={17} /> 项目、版本与 API Key 统一边界
            </li>
            <li>
              <Icon name="terminal" size={17} /> 工作台调试与 API 使用同一发布版本
            </li>
            <li>
              <Icon name="usage" size={17} /> 实际、估算与待核对费用分开展示
            </li>
          </ul>
        </div>

        <span className="wb-auth-story-foot">文件与数据任务 · 隔离执行 · 可核对用量</span>
      </section>

      <section className="wb-auth-panel">
        <div className="wb-auth-card">
          <div className="wb-auth-switch" role="tablist" aria-label="登录或注册">
            <Link to="/login" className={!registration ? 'is-active' : undefined}>
              登录
            </Link>
            <Link to="/register" className={registration ? 'is-active' : undefined}>
              注册
            </Link>
          </div>

          <div className="wb-auth-card-head">
            <span className="wb-eyebrow">
              <span className="wb-eyebrow-mark" aria-hidden="true" />
              {registration ? '创建成员账号' : '工作台登录'}
            </span>
            <h2>{registration ? '加入 AgentLab' : '继续你的工作台'}</h2>
            <p>
              {registration
                ? '成员账号用于访问工作台；项目权限与 API Key 由项目边界单独管理。'
                : '使用现有账号登录。工作台页面与真实服务权限分开校验。'}
            </p>
          </div>

          {auth.error && (
            <ErrorState
              title="暂时无法读取登录状态"
              description="请确认本地 API 服务已启动，然后重试。"
              action={
                <button
                  type="button"
                  className="wb-button wb-button--secondary wb-button--sm"
                  onClick={auth.refresh}
                >
                  重新检查
                </button>
              }
            />
          )}
          {auth.expired && (
            <div className="wb-callout wb-callout--warning" role="alert">
              登录会话已过期，请重新登录。进行中的服务端任务不会因此撤回。
            </div>
          )}

          <form className="wb-auth-form" onSubmit={submit}>
            <MutationError error={mutation.error} />
            <Field label="用户名" name="username" required error={mutation.error}>
              <input
                autoComplete="username"
                value={username}
                required
                maxLength={24}
                pattern="[a-zA-Z][a-zA-Z0-9_-]{2,23}"
                onChange={(event) => setUsername(event.target.value)}
                placeholder="例如 dev_lin"
              />
            </Field>
            {registration && (
              <>
                <label className="wb-field">
                  <span>姓名或昵称</span>
                  <input
                    autoComplete="name"
                    value={name}
                    required
                    maxLength={60}
                    onChange={(event) => setName(event.target.value)}
                    placeholder="工作台显示名称"
                  />
                </label>
                <label className="wb-field">
                  <span>邮箱（可选）</span>
                  <input
                    type="email"
                    autoComplete="email"
                    value={email}
                    maxLength={254}
                    onChange={(event) => setEmail(event.target.value)}
                    placeholder="用于账号通知"
                  />
                </label>
              </>
            )}
            <label className="wb-field">
              <span>
                密码
                <small className="wb-required" aria-hidden="true">
                  必填
                </small>
              </span>
              <div className="wb-password">
                <input
                  type={show ? 'text' : 'password'}
                  aria-label="密码"
                  autoComplete={registration ? 'new-password' : 'current-password'}
                  required
                  minLength={registration ? 10 : undefined}
                  maxLength={72}
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  placeholder={registration ? '至少 10 个字符' : '输入密码'}
                />
                <button
                  type="button"
                  className="wb-text-button"
                  aria-pressed={show}
                  onClick={() => setShow((value) => !value)}
                >
                  {show ? '隐藏' : '显示'}
                  <Icon name={show ? 'eyeOff' : 'eye'} size={16} />
                </button>
              </div>
            </label>
            {registration && (
              <p className="wb-field-help">
                用户名为 3–24 位字母、数字、下划线或连字符；管理员账号由现有管理员创建。
              </p>
            )}
            <button
              className="wb-button wb-button--primary wb-auth-submit"
              disabled={mutation.isPending}
            >
              {mutation.isPending ? '正在验证…' : registration ? '注册并进入工作台' : '登录工作台'}
            </button>
          </form>

          <p className="wb-auth-note">
            {registration
              ? '注册只创建成员身份，不会自动获得项目管理员权限或可用 API Key。'
              : '用户与管理员共用入口，角色与资源权限由服务端校验。'}
          </p>
        </div>
        <span className="wb-auth-caption">AgentLab · 托管 Agent 与 Tools API 平台</span>
      </section>
    </main>
  );
}
