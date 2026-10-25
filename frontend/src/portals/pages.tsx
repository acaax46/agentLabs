import { Link } from 'react-router-dom';
import { roleAreas, useAuth } from '../auth/context';
import { Icon, Notice, PageHeading } from '../catalog/shared';

export function PortalHome() {
  const { user, state } = useAuth();
  if (!user) return null;
  return (
    <>
      <PageHeading
        eyebrow={roleAreas[user.role]}
        title={`欢迎回来，${user.displayName}`}
        description="选择训练题目自主练习，或继续课程资源。"
      />
      <section className="portal-intro">
        <div>
          <span className="status-badge active">{roleAreas[user.role]}</span>
          <h2>从一道训练题开始，在沙箱里验证想法。</h2>
          <p>
            训练题目面向所有用户；课程资源由管理员按班级安排。训练记录和课程进度都会保存在账号中。
          </p>
        </div>
        <Icon name="courses" spot />
      </section>
      <section className="portal-grid">
        <Link className="portal-card" to="/student/training">
          <Icon name="tests" />
          <h2>训练题目</h2>
          <p>浏览所有公开题目，进入代码沙箱自主练习。</p>
          <span>查看详情</span>
        </Link>
        <Link className="portal-card" to="/student/resources">
          <Icon name="courses" />
          <h2>课程资源</h2>
          <p>按小节阅读课程文档，学习进度会自动保存。</p>
          <span>查看详情</span>
        </Link>
      </section>
      <Notice>
        训练题目可以运行公开样例；课程资源按小节记录学习进度。正式评测仍由管理员安排。
      </Notice>
      {state.permissions.recordsRead.includes('usage-records') && (
        <Link className="back-link" to="/usage-records">
          查看我的用量记录
        </Link>
      )}
    </>
  );
}
