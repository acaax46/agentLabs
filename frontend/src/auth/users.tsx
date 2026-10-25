import { useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { createUser, listUsers, resetUserPassword, updateUser } from '../api/client';
import type { AuthUser, CreateUserInput, Role, UpdateUserInput } from '../api/client';
import { ErrorPanel, FormError, Loading, Modal, Notice, PageHeading } from '../catalog/shared';
import { EditorField } from '../catalog/fields';
import { roleLabels, useAuth } from './context';

function UserForm({ record, onClose }: { record: AuthUser | null; onClose: () => void }) {
  const cache = useQueryClient();
  const [username, setUsername] = useState('');
  const [name, setName] = useState(record?.displayName || '');
  const [email, setEmail] = useState(record?.email || '');
  const [password, setPassword] = useState('');
  const [role, setRole] = useState<Role>(record?.role || 'user');
  const [status, setStatus] = useState<string>(record?.status || 'active');
  const [studentId, setStudentId] = useState('');
  const mutation = useMutation({
    mutationFn: () => {
      const common = {
        displayName: name,
        email,
        role,
        status: status as 'active' | 'disabled',
      };
      return record
        ? updateUser(record.id, { ...common, version: record.version } satisfies UpdateUserInput)
        : createUser({
            ...common,
            username,
            password,
            studentId: role === 'user' ? studentId : '',
          } satisfies CreateUserInput);
    },
    onSuccess: async () => {
      await cache.invalidateQueries({ queryKey: ['users'] });
      await cache.invalidateQueries({ queryKey: ['catalog'] });
      onClose();
    },
  });
  const submit = (event: FormEvent) => {
    event.preventDefault();
    mutation.mutate();
  };
  return (
    <form onSubmit={submit} className="user-form">
      <FormError error={mutation.error} />
      <div className="form-grid">
        {!record && (
          <div className="field">
            <label htmlFor="user-username">用户名</label>
            <input
              id="user-username"
              required
              minLength={3}
              maxLength={24}
              pattern="[a-zA-Z][a-zA-Z0-9_-]{2,23}"
              value={username}
              autoComplete="off"
              onChange={(event) => setUsername(event.target.value)}
            />
          </div>
        )}
        <div className="field">
          <label htmlFor="user-name">姓名或昵称</label>
          <input
            id="user-name"
            required
            maxLength={60}
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </div>
        <div className="field">
          <label htmlFor="user-email">邮箱</label>
          <input
            id="user-email"
            type="email"
            value={email}
            maxLength={254}
            onChange={(event) => setEmail(event.target.value)}
          />
        </div>
        <div className="field">
          <label htmlFor="user-role">账号角色</label>
          <select
            id="user-role"
            value={role}
            onChange={(event) => setRole(event.target.value as Role)}
          >
            {Object.entries(roleLabels).map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
        </div>
        <div className="field">
          <label htmlFor="user-status">账号状态</label>
          <select
            id="user-status"
            value={status}
            onChange={(event) => setStatus(event.target.value)}
          >
            <option value="active">可登录</option>
            <option value="disabled">已停用</option>
          </select>
        </div>
        {!record && (
          <div className="field">
            <label htmlFor="user-password">初始密码</label>
            <input
              id="user-password"
              type="password"
              autoComplete="new-password"
              minLength={10}
              maxLength={72}
              required
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </div>
        )}
        {role === 'user' && !record && (
          <EditorField
            field={{
              key: 'studentId',
              label: '关联既有学员（可选）',
              type: 'select',
              source: 'students',
              help: '留空会创建新的学员档案；一个学员档案只能绑定一个登录账号。',
            }}
            value={studentId}
            onChange={(value) => setStudentId(String(value))}
          />
        )}
      </div>
      {record?.studentId && (
        <p className="field-help">
          学员分班在 <Link to={`/students/${record.studentId}`}>关联档案</Link> 中维护。
        </p>
      )}
      <Notice>角色或状态更新后，该账号需要重新登录。</Notice>
      <div className="dialog-actions">
        <button
          type="button"
          className="button secondary"
          onClick={onClose}
          disabled={mutation.isPending}
        >
          取消
        </button>
        <button className="button" disabled={mutation.isPending}>
          {mutation.isPending ? '正在保存…' : '保存账号'}
        </button>
      </div>
    </form>
  );
}
export function UsersPage() {
  const { user } = useAuth();
  const cache = useQueryClient();
  const [q, setQ] = useState('');
  const [search, setSearch] = useState('');
  const [page, setPage] = useState(1);
  const query = useQuery({ queryKey: ['users', q, page], queryFn: () => listUsers(q, page) });
  const [editing, setEditing] = useState<AuthUser | null | undefined>(undefined);
  const [resetting, setResetting] = useState<AuthUser | null>(null);
  const [password, setPassword] = useState('');
  const reset = useMutation({
    mutationFn: () => resetUserPassword(resetting!.id, password),
    onSuccess: () => {
      setResetting(null);
      setPassword('');
      void cache.invalidateQueries({ queryKey: ['users'] });
    },
  });
  useEffect(() => {
    if (!resetting) {
      reset.reset();
      setPassword('');
    }
  }, [resetting]);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    setPage(1);
    setQ(search.trim());
  };
  return (
    <>
      <PageHeading
        eyebrow="管理 / 账号与权限"
        title="账号与权限"
        description="管理用户与管理员账号。注册页只创建用户账号。"
        action={
          <button className="button" onClick={() => setEditing(null)}>
            创建账号
          </button>
        }
      />
      <section className="list-panel">
        <form className="list-toolbar" onSubmit={submit}>
          <div className="search-box">
            <input
              type="search"
              aria-label="搜索账号"
              placeholder="用户名或姓名"
              value={search}
              onChange={(event) => setSearch(event.target.value)}
            />
            <button className="button secondary">搜索</button>
          </div>
          <span className="field-help">管理员可创建、编辑和停用其他账号。</span>
        </form>
        {query.isPending ? (
          <Loading />
        ) : query.isError ? (
          <ErrorPanel error={query.error} retry={() => void query.refetch()} />
        ) : (
          <>
            <div className="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th>姓名 / 用户名</th>
                    <th>角色</th>
                    <th>登录状态</th>
                    <th>权限范围</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {query.data.items.map((account) => (
                    <tr key={account.id}>
                      <td>
                        <strong className="record-title">{account.displayName}</strong>
                        <span className="record-code">{account.username}</span>
                      </td>
                      <td>{roleLabels[account.role]}</td>
                      <td>
                        <span
                          className={`status-badge ${account.status === 'active' ? 'active' : 'archived'}`}
                        >
                          {account.status === 'active' ? '可登录' : '已停用'}
                        </span>
                      </td>
                      <td>
                        {account.role === 'admin'
                          ? '全部资料'
                          : account.studentId
                            ? '本人资料'
                            : '未绑定学员'}
                      </td>
                      <td>
                        {account.canManage ? (
                          <div className="row-actions">
                            <button
                              type="button"
                              className="text-button"
                              onClick={() => setEditing(account)}
                            >
                              编辑权限
                            </button>
                            <button
                              type="button"
                              className="text-button"
                              onClick={() => setResetting(account)}
                            >
                              重置密码
                            </button>
                          </div>
                        ) : (
                          <span className="field-help">
                            {account.id === user?.id ? '当前账号' : '由管理员管理'}
                          </span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="pagination">
              <span>共 {query.data.total} 个账号</span>
              <div className="button-row">
                <button
                  className="button secondary"
                  disabled={page <= 1}
                  onClick={() => setPage((value) => value - 1)}
                >
                  上一页
                </button>
                <button
                  className="button secondary"
                  disabled={page * 20 >= query.data.total}
                  onClick={() => setPage((value) => value + 1)}
                >
                  下一页
                </button>
              </div>
            </div>
          </>
        )}
      </section>
      <Modal
        open={editing !== undefined}
        title={editing ? '编辑账号权限' : '创建登录账号'}
        onClose={() => setEditing(undefined)}
      >
        {editing !== undefined && (
          <UserForm
            key={editing?.id || 'new'}
            record={editing}
            onClose={() => setEditing(undefined)}
          />
        )}
      </Modal>
      <Modal
        open={!!resetting}
        title="重置账号密码"
        onClose={() => setResetting(null)}
        busy={reset.isPending}
      >
        <form
          onSubmit={(event) => {
            event.preventDefault();
            reset.mutate();
          }}
        >
          <p className="dialog-description">
            为「{resetting?.displayName}」设置新密码，已有登录会话会失效。
          </p>
          <FormError error={reset.error} />
          <div className="field">
            <label htmlFor="reset-password">新密码</label>
            <input
              id="reset-password"
              type="password"
              autoComplete="new-password"
              required
              minLength={10}
              maxLength={72}
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </div>
          <div className="dialog-actions">
            <button
              type="button"
              className="button secondary"
              data-initial-focus
              onClick={() => setResetting(null)}
              disabled={reset.isPending}
            >
              取消
            </button>
            <button className="button" disabled={reset.isPending}>
              {reset.isPending ? '正在更新…' : '更新密码'}
            </button>
          </div>
        </form>
      </Modal>
    </>
  );
}
