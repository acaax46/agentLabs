import { useState } from 'react';
import { CodeBlock, SectionHeader, Tabs } from './ui';

export function integrationExamples(body: unknown, origin: string) {
  const json = JSON.stringify(body, null, 2);
  const quoted = JSON.stringify(json);
  return {
    curl: [
      `# 超时重试时复用同一 REQUEST_KEY`,
      'REQUEST_KEY="$(uuidgen)"',
      `curl '${origin}/v1/runs'`,
      '  -H "Authorization: Bearer $AGENTLAB_API_KEY"',
      "  -H 'Content-Type: application/json'",
      '  -H "Idempotency-Key: $REQUEST_KEY"',
      `  --data '${json.replaceAll("'", "'\\''")}'`,
    ]
      .map((line, index, lines) => (index > 1 && index < lines.length - 1 ? line + ' \\' : line))
      .join('\n'),
    python: `import json, os, uuid, urllib.request\n\npayload = json.loads(${quoted})\nrequest_key = str(uuid.uuid4())  # 超时后保留并复用此键\nrequest = urllib.request.Request(\n    ${JSON.stringify(`${origin}/v1/runs`)},\n    data=json.dumps(payload).encode("utf-8"),\n    headers={"Authorization": "Bearer " + os.environ["AGENTLAB_API_KEY"],\n             "Content-Type": "application/json", "Idempotency-Key": request_key},\n    method="POST")\nwith urllib.request.urlopen(request, timeout=30) as response:\n    print(json.load(response)["id"])\n# 不自动重发写操作。`,
    javascript: `import { randomUUID } from 'node:crypto';\nconst payload = ${json};\nconst requestKey = randomUUID(); // 超时后保留并复用\nconst response = await fetch(${JSON.stringify(`${origin}/v1/runs`)}, {\n  method: 'POST',\n  headers: { Authorization: 'Bearer ' + process.env.AGENTLAB_API_KEY,\n    'Content-Type': 'application/json', 'Idempotency-Key': requestKey },\n  body: JSON.stringify(payload), signal: AbortSignal.timeout(30000)\n});\nif (!response.ok) throw new Error('Run request failed: ' + response.status);\nconsole.log((await response.json()).id);\n// 不自动重发写操作。`,
    go: `package main\n\nimport ("bytes"; "context"; "fmt"; "io"; "net/http"; "os"; "time")\n\nfunc main() {\n  ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second); defer cancel()\n  requestKey := os.Getenv("AGENTLAB_REQUEST_KEY") // 新意图唯一，超时重试复用\n  if requestKey == "" { panic("set AGENTLAB_REQUEST_KEY") }\n  req, err := http.NewRequestWithContext(ctx, "POST", ${JSON.stringify(`${origin}/v1/runs`)}, bytes.NewReader([]byte(${quoted})))\n  if err != nil { panic(err) }\n  req.Header.Set("Authorization", "Bearer " + os.Getenv("AGENTLAB_API_KEY"))\n  req.Header.Set("Content-Type", "application/json")\n  req.Header.Set("Idempotency-Key", requestKey)\n  resp, err := http.DefaultClient.Do(req); if err != nil { panic(err) }; defer resp.Body.Close()\n  if resp.StatusCode != 202 { panic(fmt.Sprintf("Run request failed: %d", resp.StatusCode)) }\n  io.Copy(os.Stdout, resp.Body)\n}`,
  };
}
export function ApiExamples({ body }: { body: unknown }) {
  const [language, setLanguage] = useState<'curl' | 'python' | 'javascript' | 'go'>('curl');
  const examples = integrationExamples(body, window.location.origin);
  return (
    <>
      <SectionHeader
        title="API 调用示例"
        description="环境变量提供项目密钥。网络超时后保留幂等键，不自动重发写操作。"
      />
      <Tabs
        label="API 示例语言"
        value={language}
        onChange={setLanguage}
        items={[
          { value: 'curl', label: 'curl' },
          { value: 'python', label: 'Python' },
          { value: 'javascript', label: 'JavaScript' },
          { value: 'go', label: 'Go' },
        ]}
      />
      <CodeBlock label={language} copy>
        {examples[language]}
      </CodeBlock>
      <details>
        <summary>事件恢复与回调边界</summary>
        <p>传入最后收到的事件序号，服务器从之后补齐，重复事件按 ID 去重。</p>
        <CodeBlock
          label="SSE / curl"
          copy
        >{`curl --no-buffer '${window.location.origin}/v1/runs/<RUN_ID>/events?after=<LAST_EVENT_SEQUENCE>' -H "Authorization: Bearer $AGENTLAB_API_KEY" -H 'Accept: text/event-stream'`}</CodeBlock>
        <p className="wb-muted">Webhook 配置与签名协议尚未开放；当前使用事件流或带游标查询。</p>
      </details>
    </>
  );
}
