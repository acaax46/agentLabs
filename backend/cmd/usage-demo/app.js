const $ = (id) => document.getElementById(id);
let current;
const states = { failed: '失败', running: '运行中', succeeded: '已完成', unknown: '结果未知' };
const money = (value) => `$${(value / 1000000).toFixed(6)} USD（测试）`;
function text(parent, tag, value) {
  const element = document.createElement(tag);
  element.textContent = value;
  parent.append(element);
}
function render(data) {
  current = data;
  $('window').textContent =
    `窗口：${data.windowId} · 操作：${data.operation?.operationId || '未开始'}`;
  $('metrics').replaceChildren();
  for (const [label, value] of [
    ['已结算', data.balance.Spent],
    ['预占待结算', data.balance.Held],
    ['可用额度', data.balance.Limit - data.balance.Spent - data.balance.Held],
  ]) {
    const card = document.createElement('div');
    text(card, 'span', label);
    text(card, 'strong', money(value));
    $('metrics').append(card);
  }
  $('attempts').replaceChildren();
  for (const attempt of data.attempts) {
    const card = document.createElement('article');
    text(card, 'h3', `${attempt.attemptId} · ${states[attempt.status] || attempt.status}`);
    text(
      card,
      'p',
      `模型：${attempt.reportedModel || attempt.model} · 输入 ${attempt.usage.inputTokens ?? '未知'} / 输出 ${attempt.usage.outputTokens ?? '未知'} Token · ${attempt.timing.wallMs} ms`,
    );
    if (attempt.errorMessage) text(card, 'p', attempt.errorMessage);
    if (attempt.usage.cachedInputTokens != null)
      text(
        card,
        'p',
        `其中缓存输入 ${attempt.usage.cachedInputTokens}，推理输出 ${attempt.usage.reasoningTokens ?? '未提供'} Token。`,
      );
    $('attempts').append(card);
  }
  $('status').textContent = data.operation ? states[data.operation.status] : '尚未开始。';
  $('retry').disabled = !data.operation?.retryAvailable;
  $('retry-note').textContent =
    data.operation?.status === 'unknown'
      ? '请求可能已执行，保留预算预占；需核对结果，不能自动重发。'
      : '重试会追加一次尝试，保留原窗口和错误记录。';
  $('ledger').replaceChildren();
  for (const item of data.ledger)
    text(
      $('ledger'),
      'p',
      `${item.attemptId} · ${item.type} · ${money(item.units)} · ${item.priceVersion}`,
    );
  if (!data.ledger.length) text($('ledger'), 'p', '尚无结算记录。');
}
async function request(path, payload) {
  $('error').textContent = '';
  const response = await fetch(
    path,
    payload
      ? {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'X-AgentLab-Demo': 'fixture' },
          body: JSON.stringify(payload),
        }
      : {},
  );
  if (!response.ok) throw new Error(await response.text());
  render(await response.json());
}
async function act(path, body) {
  for (const id of ['start', 'unknown', 'retry']) $(id).disabled = true;
  try {
    await request(path, body);
  } catch (error) {
    $('error').textContent = error.message;
  } finally {
    $('start').disabled = $('unknown').disabled = false;
    $('retry').disabled = !current?.operation?.retryAvailable;
  }
}
$('start').onclick = () => act('/api/start', { scenario: 'before-send' });
$('unknown').onclick = () => act('/api/start', { scenario: 'unknown' });
$('retry').onclick = () =>
  act('/api/retry', {
    operationId: current.operation.operationId,
    attemptId: current.attempts.at(-1).attemptId,
  });
request('/api/state').catch((error) => {
  $('error').textContent = error.message;
});
