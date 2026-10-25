import { EmptyState, SectionHeader } from './ui';

export const unavailableP1 = [
  ['versions', '草稿与版本发布', '已发布版本可对比；草稿编辑、发布与默认版本切换尚未开放。'],
  ['approvals', '工具审批', '批准、拒绝与审批记录尚未开放，等待审批的动作保持原状态。'],
  ['webhooks', 'Webhook', '回调配置、签名密钥、投递记录和补投尚未开放。'],
  ['members', '成员与审计', '项目邀请、角色修改和项目审计查询尚未开放。'],
  ['custom-endpoints', '自建模型能力验证', '现有兼容连接可配置地址；独立协议与能力验证尚未开放。'],
  ['retention', '保留期调整', '当前只展示服务端保留期，延长或缩短保留期尚未开放。'],
] as const;
export function P1Availability() {
  return (
    <section className="wb-panel">
      <SectionHeader
        title="尚未开放的完善功能"
        description="这些操作需要服务端支持，当前版本不提供模拟成功的配置入口。"
      />
      <dl className="wb-fact-list">
        {unavailableP1.map(([key, name, reason]) => (
          <div key={key}>
            <dt>{name}</dt>
            <dd>未开放 · {reason}</dd>
          </div>
        ))}
      </dl>
      <p className="wb-muted">新品牌图形属于可选独立提案，继续使用现有过渡 Logo。</p>
    </section>
  );
}
export function P1Unavailable({ feature }: { feature: (typeof unavailableP1)[number][0] }) {
  const item = unavailableP1.find(([key]) => key === feature)!;
  return <EmptyState title={`${item[1]}未开放`} description={item[2]} />;
}
