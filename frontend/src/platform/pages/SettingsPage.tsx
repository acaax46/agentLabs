import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router-dom';
import { changePassword, getBootstrap } from '../../api/client';
import { roleLabels, useAuth } from '../../auth/context';
import { usePlatform } from '../state';
import { formatBytes, formatDateTime } from '../model';
import { MutationError, QueryState } from '../shared';
import { KeyValue, PageHeader, SectionHeader, StatusBadge } from '../ui';
import { P1Availability } from '../p1Availability';

export function SettingsPage() {
  const auth = useAuth();
  const platform = usePlatform();
  const navigate = useNavigate();
  const user = auth.user!;
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const bootstrap = useQuery({ queryKey: ['bootstrap'], queryFn: getBootstrap, retry: false });
  const password = useMutation({
    mutationFn: async () => {
      if (next !== confirm) throw new Error('两次输入的新密码不一致。');
      await changePassword(current, next);
      auth.clear();
      navigate('/login', { replace: true });
    },
    retry: false,
  });
  return (
    <>
      <PageHeader
        title="设置"
        description="管理账户与平台服务状态。"
        actions={
          <button className="wb-button wb-button--secondary" onClick={platform.refresh}>
            刷新服务状态
          </button>
        }
      />
      <div className="wb-settings-grid">
        <P1Availability />
        <section className="wb-panel" id="account">
          <SectionHeader title="个人账号" />
          <KeyValue
            items={[
              { label: '显示名称', value: user.displayName },
              { label: '用户名', value: user.username },
              { label: '账号角色', value: roleLabels[user.role] },
              { label: '邮箱', value: user.email || '未填写' },
              { label: '工作区', value: platform.me?.workspace.name ?? '未知' },
              { label: '工作区 ID', value: platform.me?.workspace.id ?? user.tenantId },
              { label: '创建时间', value: formatDateTime(user.createdAt) },
            ]}
          />
          <p className="wb-muted">
            网页登录使用用户会话。机器调用使用绑定项目与 scope 的 API Key。
          </p>
        </section>
        <section className="wb-panel" id="environment">
          <SectionHeader
            title="平台服务状态"
            description="就绪状态来自后端探测，不代表云端 SLA。"
          />
          {platform.readiness ? (
            <>
              <StatusBadge tone={platform.readiness.ready ? 'success' : 'warning'}>
                {platform.readiness.ready ? '平台就绪' : '平台未就绪'}
              </StatusBadge>
              <KeyValue
                items={[
                  ...platform.readiness.checks.map((check) => ({
                    label: check.name,
                    value: check.ok ? '已通过' : check.detail || '不可用',
                  })),
                  {
                    label: '代码执行',
                    value: platform.readiness.capabilities.codeExecution
                      ? platform.readiness.capabilities.isolation
                      : platform.readiness.capabilities.codeExecutionReason || '不可用',
                  },
                  {
                    label: '单文件上传上限',
                    value: formatBytes(platform.readiness.capabilities.maxUploadBytes),
                  },
                  {
                    label: 'Run 输入上限',
                    value: formatBytes(platform.readiness.capabilities.maxRunInputBytes),
                  },
                ]}
              />
            </>
          ) : (
            <p className="wb-muted">平台能力未知，正在读取或服务不可用。可刷新重试。</p>
          )}
          <QueryState
            pending={bootstrap.isPending}
            error={bootstrap.error}
            retry={bootstrap.refetch}
          >
            {bootstrap.data && <p className="wb-muted">本地服务版本 v{bootstrap.data.version}</p>}
          </QueryState>
        </section>
        <section className="wb-panel" id="mvp">
          <SectionHeader title="接口与部署边界" />
          <ul className="wb-check-lines">
            <li>工作台通过 /api/v1/platform 使用当前会话，机器 API 使用 /v1 与项目 API Key。</li>
            <li>项目、运行、产物、用量和账本来自后端；浏览器不生成业务数据。</li>
            <li>试运行会预占预算，取消和撤权不能撤回已发生的外部副作用。</li>
            <li>商业支付未接入；预算和估算不代表充值余额或供应商最终账单。</li>
            <li>多租户、云端弹性与安全 SLA 仍需独立验收。</li>
          </ul>
          <Link className="wb-text-link" to="/connections">
            管理模型连接
          </Link>
        </section>
      </div>
      <section className="wb-panel wb-password-panel">
        <SectionHeader title="修改密码" description="修改成功后现有会话失效，需要重新登录。" />
        <form
          className="wb-form"
          onSubmit={(event) => {
            event.preventDefault();
            password.mutate();
          }}
        >
          <div className="wb-form-grid">
            <label className="wb-field">
              <span>当前密码</span>
              <input
                type="password"
                autoComplete="current-password"
                required
                value={current}
                onChange={(event) => setCurrent(event.target.value)}
              />
            </label>
            <label className="wb-field">
              <span>新密码</span>
              <input
                type="password"
                autoComplete="new-password"
                required
                minLength={10}
                maxLength={72}
                value={next}
                onChange={(event) => setNext(event.target.value)}
              />
            </label>
            <label className="wb-field">
              <span>确认新密码</span>
              <input
                type="password"
                autoComplete="new-password"
                required
                minLength={10}
                maxLength={72}
                value={confirm}
                onChange={(event) => setConfirm(event.target.value)}
              />
            </label>
          </div>
          <MutationError error={password.error} />
          <button className="wb-button wb-button--primary" disabled={password.isPending}>
            {password.isPending ? '正在更新…' : '更新密码并重新登录'}
          </button>
        </form>
      </section>
    </>
  );
}
