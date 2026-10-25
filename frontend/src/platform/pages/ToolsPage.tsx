import { useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { get } from '../api';
import type { Tool } from '../api';
import { usePlatform } from '../state';
import { formatUnitPrice, riskLabels } from '../model';
import { QueryState } from '../shared';
import { CodeBlock, EmptyState, Modal, PageHeader, StatusBadge } from '../ui';
import { ToolPlayground } from './ToolCallsPage';

export function ToolsPage() {
  const { readiness } = usePlatform();
  const [params, setParams] = useSearchParams();
  const query = useQuery({
    queryKey: ['platform', 'tools'],
    queryFn: ({ signal }) => get<{ items: Tool[] }>('/tools', {}, signal),
    retry: false,
  });
  const [selected, setSelected] = useState<Tool | null>(null);
  const debugging = query.data?.items.find((tool) => tool.name === params.get('debug'));
  return (
    <>
      <PageHeader
        title="工具"
        description="查看实际工具版本、输入输出约束、计量规则和执行能力。"
        actions={
          <Link className="wb-button wb-button--secondary" to="/tool-calls">
            独立调用历史
          </Link>
        }
      />
      {debugging && <ToolPlayground key={debugging.id} tool={debugging} />}
      <QueryState pending={query.isPending} error={query.error} retry={query.refetch}>
        <section className="wb-panel">
          {!query.data?.items.length ? (
            <EmptyState title="暂无可用工具" description="当前后端没有发布工具版本。" />
          ) : (
            <div className="wb-table-wrap">
              <table className="wb-table">
                <thead>
                  <tr>
                    <th>工具 / 版本</th>
                    <th>说明</th>
                    <th>风险</th>
                    <th>计量价格</th>
                    <th>能力</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {query.data.items.map((tool) => (
                    <tr key={tool.id}>
                      <td className="wb-mono">
                        {tool.name}@{tool.version}
                      </td>
                      <td>{tool.summary}</td>
                      <td>{riskLabels[tool.risk]}</td>
                      <td>
                        {tool.prices.length
                          ? tool.prices.map((price) => (
                              <p key={price.id}>
                                {formatUnitPrice(price.perUnitMicros, price.currency)}{' '}
                                {price.currency} / {price.unit}
                              </p>
                            ))
                          : '尚未定价'}
                      </td>
                      <td>
                        <StatusBadge
                          tone={
                            tool.name === 'code.execute' && !readiness?.capabilities.codeExecution
                              ? 'warning'
                              : 'info'
                          }
                        >
                          {tool.name === 'code.execute' && !readiness?.capabilities.codeExecution
                            ? '执行不可用或未知'
                            : tool.status === 'available'
                              ? '可用'
                              : '测试版'}
                        </StatusBadge>
                      </td>
                      <td>
                        <button
                          className="wb-button wb-button--primary wb-button--sm"
                          onClick={() => setParams({ debug: tool.name })}
                        >
                          调试
                        </button>
                        <button
                          className="wb-button wb-button--secondary"
                          onClick={() => setSelected(tool)}
                        >
                          查看契约
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
      </QueryState>
      <Modal
        open={!!selected}
        onClose={() => setSelected(null)}
        title={selected ? `${selected.name}@${selected.version}` : '工具'}
        description={selected?.description}
        size="lg"
      >
        {selected && (
          <>
            {selected.billingPolicy && <p className="wb-callout">{selected.billingPolicy}</p>}
            <p>外部副作用：{selected.sideEffects}</p>
            <p>重试规则：{selected.retry}</p>
            <p>
              审批：
              {selected.requiresApproval ? '需要审批；当前工作台未开放审批操作。' : '无需审批'}
            </p>
            <CodeBlock label="inputSchema">
              {JSON.stringify(selected.inputSchema, null, 2)}
            </CodeBlock>
            <CodeBlock label="outputSchema">
              {JSON.stringify(selected.outputSchema, null, 2)}
            </CodeBlock>
            <CodeBlock label="sample" copy>
              {JSON.stringify(selected.sample, null, 2)}
            </CodeBlock>
          </>
        )}
      </Modal>
    </>
  );
}
