# AgentLab 本地启动与 MVP 最小使用视频

本文说明如何在 Windows 本机启动 AgentLab，并录制一段可复核 MVP 主链路的最小使用视频。命令均从仓库根目录执行；其他系统把 PowerShell 写法替换为对应 shell 即可。评委讲解口径见 [黑客松演示与协作](hackathon-guide.md)。

按当前“对话”入口录屏时，使用[演示操作与口播脚本](demo-script.md)中的逐屏操作和台词；本文继续提供环境启动步骤，以及 Agent Playground / API 调试路径的补充说明。仓库已提供可直接选用的 [demo/orders.csv](../demo/orders.csv)。

本文按当前代码事实（2026-10-25）编写：浏览器工作台使用 MongoDB、真实模型连接和 Docker 沙箱完成文件分析 Run；服务端同时提供 9 个预设 Agent，包括通用代码生成、工作台搭建师、GPU 计算和代码 Agent。云端多租户、对象存储、KMS、支付和 SLA 不在本地 MVP 验收范围。边界以 README.md、docs/implementation-status.md 和 DESIGN.md 为准。预设清单见 [预设 Agent 与工具目录](preset-agents.md)。

## 1. 录制目标与边界

最小视频应展示一条真实、可复核的闭环：

1. 登录工作台并创建一个有累计预算的项目。
2. 使用已验证的模型连接，打开一个已发布的预设 Agent（视频默认使用文件分析 Agent）。
3. 上传一份 CSV，提交带费用上限的试运行。
4. 在 Run 详情看到状态、执行时间线和工具调用。
5. 打开真实生成的 CSV/PNG 产物，并在“用量与账单”核对本次用量。

这条路径足以证明工作台的 P0 文件分析链路。若还要证明外部应用可以使用同一发布版本，可追加 API Key 片段；该片段会短暂出现密钥明文，必须使用一次性演示密钥并在录制后撤销。

不要把以下内容旁白成已完成能力：云端弹性、多租户隔离、对象存储、KMS、商业支付、供应商最终账单、SLA 或完整安全认证。工作台金额是平台按本地冻结价格和实际证据形成的计量结果，不等于供应商最终账单；BYOK 模型费用显示为“由客户承担”。

## 2. 前置条件

| 项目     | 要求                                                                     |
| -------- | ------------------------------------------------------------------------ |
| 操作系统 | Windows 10/11，PowerShell；Docker Desktop 已启动                         |
| Node.js  | 22.20.x，版本见 .node-version                                            |
| pnpm     | 9.15.9                                                                   |
| Go       | 以 backend/go.mod 为准（README 当前记录为 Go 1.26.x）                    |
| Docker   | 能运行 MongoDB 和 agentlab/sandbox-python:20261024                       |
| 模型     | 可访问 OpenAI Chat Completions 兼容接口的账户；真实 Run 会消耗供应商额度 |
| 浏览器   | Chrome/Chromium，建议录制窗口 1440×900                                   |

首次进入仓库，在根目录执行一次依赖准备：

```powershell
$env:PATH = 'D:/node/nvm/v22.20.0;' + $env:PATH
node --version
pnpm --version
pnpm install --frozen-lockfile
go -C backend mod download
```

Node 22 路径不同就替换 PATH。不要修改全局 Node、pnpm、模型或凭据配置。

## 3. 配置模型连接与秘密

推荐使用仓库根目录的 .env.dev。该文件已被 .gitignore 忽略，不能提交，也不要在录屏中展示完整内容。

如果尚未创建配置文件：

```powershell
Copy-Item backend/.env.example .env.dev
notepad .env.dev
```

至少填写以下字段，值使用你自己的模型账户：

```dotenv
BASE_URL=https://<模型服务>/v1/chat/completions
API_KEY=<只保存在本机，不要提交>
AGENTLAB_MODEL_PROVIDER=openai-compatible
AGENTLAB_SANDBOX_BACKEND=docker
AGENTLAB_SANDBOX_IMAGE=agentlab/sandbox-python:20261024
```

常见的 OpenRouter 配置可以使用其 Chat Completions 地址。不要把供应商密钥写进前端环境文件、截图、浏览器 URL、日志或视频字幕。当前服务只接受 Chat Completions 连接，连接页面会把地址规范化为 /v1。若项目已有通过页面创建的连接，可以不配置 BASE_URL/API_KEY，在“连接”页手动添加并验证 BYOK 连接。

启动时，BASE_URL/API_KEY 只会被导入一次为“平台模型网关”待验证共享连接。服务启动后要在工作台“连接”页点击“验证”；验证只检查授权和模型列表，不代表所有模型已经完成推理验收。平台供模连接按平台价格计量，私人 BYOK 连接的模型费用由客户承担。

## 4. 启动本地服务

### 4.1 MongoDB、沙箱与演示账号

在第一个 PowerShell 窗口执行：

```powershell
pnpm db:up
pnpm build:sandbox
pnpm seed:demo
```

说明：

- db:up 启动 compose.test.yaml 中的 MongoDB 单节点副本集，默认监听 127.0.0.1:27018。
- build:sandbox 构建真实 Docker 文件分析沙箱；首次执行需要联网下载基础镜像和 Python 依赖。
- seed:demo 显式创建本地演示账号，同时保留旧资料演示数据；它不会伪造平台 Run、用量或消费记录。
- 内置 9 个 Agent（文件分析、表格清洗、数据质量检查、报表与图表、文档摘要、通用代码生成、工作台搭建师、GPU 计算、代码 Agent），以及 files.transform、code.execute、files.bundle 1.0.0，会在平台服务初始化时幂等发布，不需要手工插入 Agent。GPU 与代码 Agent 需要额外镜像，最小视频不必演示它们。
- 代码生成和工作台搭建 Agent 的 ZIP 是项目产物；它包含源码、说明和摘要 manifest，需要用户自行部署到目标环境，不会由本地 Agent 服务自动发布到公网。

### 4.2 API

在第二个 PowerShell 窗口执行：

```powershell
pnpm dev:api --env-file .env.dev
```

默认地址为 http://127.0.0.1:8787。API 进程必须保持运行。启动后，在第三个窗口发布本地开发价目：

```powershell
pnpm seed:platform
```

seed:platform 只补齐缺失的本地验证价格，不是商业定价，也不会创建虚假用量；应在 API 用 .env.dev 启动并导入模型连接后执行。

可做一个不含秘密的进程检查：

```powershell
Invoke-RestMethod http://127.0.0.1:8787/health/live
```

预期返回 {"status":"ok"}。/health/live 只说明 HTTP 进程存活；工作台首页“执行能力”卡片和“设置”页的能力检查才是平台数据库、文件存储与 Docker 沙箱的实际状态。旧的 /health/ready 是历史执行入口，不要用它替代平台 /capabilities 判断。缺少沙箱时平台会明确报告 codeExecution=false，不会退回宿主执行。

### 4.3 前端

在第四个 PowerShell 窗口执行：

```powershell
pnpm dev
```

打开 http://127.0.0.1:5173。Vite 会把 /api、/v1 和 /health 代理到 127.0.0.1:8787。默认端口可以用 AGENTLAB_API_PORT 和 AGENTLAB_WEB_PORT 调整，录制时建议保持默认值。

本地演示账号由 seed:demo 创建：

| 角色   | 用户名  | 初始密码               | 用途                                   |
| ------ | ------- | ---------------------- | -------------------------------------- |
| 管理员 | admin   | AgentLab-Admin-2026!   | 连接、项目授权、API Key 和价格相关操作 |
| 用户   | student | AgentLab-Student-2026! | 普通工作台访问演示                     |

这是当前原型的显式演示账号，不是云服务默认账号。首次登录后建议在“设置”修改密码；本机评审可使用管理员账号完成录制。

## 5. 准备最小输入文件

仓库已提供 demo/orders.csv，可直接在文件选择器中选用。在根目录查看内容：

```powershell
Get-Content -LiteralPath demo/orders.csv -Encoding UTF8
```

```csv
order_id,region,amount
A001,east,12.50
A002,west,20.00
A003,east,3.25
```

文件只有三行数据，适合录屏和人工复核。可预期东部金额为 15.75、西部金额为 20.00，但视频应以实际产物为准，不要在模型完成前先声称结果。不要使用含姓名、邮箱、真实订单或供应商秘密的文件。

## 6. 工作台配置

### 6.1 登录和能力检查

1. 登录页输入 admin 和演示密码，点击“登录工作台”。
2. 进入“概览”，确认“执行能力”显示“平台服务就绪”，且不是“此部署没有配置代码执行沙箱”。
3. 若提示服务未就绪，先检查 MongoDB、Docker 和 API 终端，不要继续录制失败状态。

### 6.2 验证模型连接

1. 打开侧栏“连接”。
2. 找到“平台模型网关”后点击“验证”，等待状态变为“已连接”。
3. 如果没有平台网关，点击“连接模型账户”，填写模型服务地址、供应商标识、模型列表和 API Key，勾选同意后点击“保存并验证”。只填写实际可访问的模型 ID，例如 deepseek/deepseek-v4.1-flash-1017。
4. 页面只显示连接元数据，不会再次显示密钥。列表中的“已连接”只代表声明的授权与模型列表验证通过。

若使用私人 BYOK 连接，在项目详情“可用连接”区域授权它；工作区共享连接会自动对当前项目可用。

### 6.3 创建项目和预算

1. 打开“项目”，点击“创建项目”。
2. 填写：
   - 项目名称：MVP 文件分析演示
   - 用途说明：录制 AgentLab 文件分析主链路
   - 环境：预发布（选择生产也不影响本地演示）
   - 币种：CNY
   - 项目累计预算：5
3. 点击“创建项目”，进入项目详情并点击“设为当前项目”。
4. 确认预算卡片显示预算上限、预占和可用预算。项目预算是累计额度，不按月重置，也不是充值余额。
5. 若连接是私人 BYOK，在“可用连接”选择它并点击“授权连接”；平台共享连接不需要此步。

## 7. 最小工作台视频脚本（推荐 2–3 分钟）

建议浏览器窗口 1440×900、100% 缩放、系统缩放 100%。启动完成后可只录制浏览器。真实 Run 等待时可以剪掉无变化片段，但必须保留提交后的至少一个进行中状态和最终状态，不能用静态截图冒充运行。

### 7.1 时间线

| 时间      | 画面与操作                                                     | 建议旁白                                                   | 需要留下的证据                             |
| --------- | -------------------------------------------------------------- | ---------------------------------------------------------- | ------------------------------------------ |
| 0:00–0:15 | 登录后停在“概览”                                               | “这是 AgentLab 工作台，运行、预算和执行能力都来自后端。”   | 黑色工作台、项目选择器、执行能力           |
| 0:15–0:35 | “项目”→“创建项目”，填写 MVP 文件分析演示 和 5 CNY              | “先建立项目边界并设置累计预算。”                           | 项目名、币种、预算上限                     |
| 0:35–0:50 | “连接”页验证平台模型网关，或展示已连接 BYOK                    | “模型连接在执行前单独验证，密钥不会回显。”                 | 来源、状态“已连接”                         |
| 0:50–1:20 | “Agent”→文件分析 Agent→“Playground”                            | “这里选择冻结的 AgentVersion、模型和输入文件。”            | Agent、版本 1.0.0、工具版本                |
| 1:20–1:40 | 上传 demo/orders.csv，填写任务指令                             | “上传 CSV，要求按地区汇总，并生成汇总文件和图表。”         | 文件上传、摘要校验通过                     |
| 1:40–1:55 | 保持费用上限 2 CNY、执行时间 300 秒、工具次数 20，勾选费用确认 | “提交前明确费用上限；模型、工具和托管执行按实际证据计量。” | 费用上限、承担方、复选框                   |
| 1:55–2:20 | 点击“提交试运行”，进入 Run 详情，展示时间线或“工具调用树”      | “Run 使用固定发布版本，事件和工具调用可以持续观察。”       | Run ID、状态、files.transform/code.execute |
| 2:20–2:45 | 等待“已完成”，展开“运行结果”和“输出产物”                       | “完成后，结果和产物都归属于当前项目。”                     | succeeded、结算状态、产物名称              |
| 2:45–3:00 | 查看 CSV/PNG 产物，再打开“用量与账单”                          | “最后核对产物、用量、币种和结算状态。”                     | 真实预览、Run 链接、用量明细               |

### 7.2 Playground 填写值

在文件分析 Agent 的 Playground 中：

- 发布版本：保持已发布 1.0.0。
- 模型连接：选择状态为可用的模型。若页面显示“工具调用能力尚未通过推理验证”，不要把它旁白成已验证。
- 任务指令：

```text
读取 orders.csv，按 region 汇总 amount，输出 summary.csv；再生成一张按地区展示金额的 chart.png。请用中文给出简短总结，并说明实际生成的文件名。不要修改输入文件。
```

- 上传输入文件：选择 demo/orders.csv，等待“服务端摘要校验通过”，确认文件出现在勾选列表。
- 费用上限：2（单位随项目显示为 CNY）。
- 最长执行时间：默认 300 秒。
- 工具调用次数：默认 20。
- 确认：勾选“我确认预占预算与费用影响”。

点击“提交试运行”后不要重复点击。写请求使用幂等键；若浏览器显示网络错误，先到“运行”或项目概览核对是否已创建，再决定是否用同一意图重试。

### 7.3 Run 详情证据

至少展示以下区域中的两个：

- 顶部 Run ID、Agent 名称、状态徽标和结算状态。
- “执行时间线”：准备、模型调用、工具调用和完成事件。
- “工具调用树”：files.transform 负责表格操作，code.execute 在隔离 Docker 沙箱中处理图表或其他代码。
- “运行结果”：Agent 返回的结构化 JSON。
- “输出产物”：文件名、类型、大小、来源 Run 和保留期。
- 右侧“运行与费用”：模型来源、连接、预占、费用上限、费用、耗时和 tokens；缺失指标显示“未知”，不会补零。

如果状态为 unknown（结果待核对），不要继续演示成成功，也不要自动重发或承诺退款；按 Run、工具调用和账本证据核对。

## 8. 可选：API Key 外部调用（45–90 秒）

这一步证明工作台和 /v1 使用同一 AgentVersion、权限和计量入口，不是最短工作台视频的必要步骤。

### 8.1 签发一次性演示密钥

1. 打开“API 密钥”，点击“创建 API Key”。
2. 名称填 video-demo。
3. 保留 runs:write、runs:read、files:write、artifacts:read、usage:read；如调用独立工具再勾选 tools:call。
4. 有效期选 30 天，月度上限可填 2 CNY，每分钟请求数保留 60。
5. 点击“创建密钥”，把完整密钥放到本机密码管理器或临时环境变量，勾选“我确认已将完整密钥保存到安全位置”，再点“我已保存”。

完整密钥只显示一次。录屏不要让完整值进入视频、终端历史、剪贴板录制或浏览器网络日志；终端画面只保留 `al_live_` 前缀即可。

### 8.2 PowerShell 调用 /v1

在单独的 PowerShell 窗口设置密钥；尖括号内容只放在本机环境变量：

```powershell
$env:AGENTLAB_API_KEY = '<复制的完整密钥>'
$api = 'http://127.0.0.1:8787/v1'
$headers = @{ Authorization = "Bearer $env:AGENTLAB_API_KEY" }

$fileBody = @{ name = 'orders.csv'; size = (Get-Item demo/orders.csv).Length } | ConvertTo-Json
$ticket = Invoke-RestMethod -Method Post -Uri "$api/files" -Headers ($headers + @{ 'Idempotency-Key' = 'video-file-0001' }) -ContentType 'application/json' -Body $fileBody
Invoke-WebRequest -Method Put -Uri ("http://127.0.0.1:8787" + $ticket.uploadUrl) -Headers $headers -InFile demo/orders.csv -ContentType 'application/octet-stream'

$agents = Invoke-RestMethod -Method Get -Uri "$api/agents" -Headers $headers
$models = Invoke-RestMethod -Method Get -Uri "$api/models" -Headers $headers
$agent = $agents.items | Where-Object { $_.id -eq 'agt_file_analysis' } | Select-Object -First 1
$model = $models.items | Where-Object { $_.usable -eq $true } | Select-Object -First 1
```

提交 Run 时把 $agent.latestVersion.id、$model.connectionId、$model.model 和 $ticket.id 作为字段：

```powershell
$runBody = @{
  agentVersionId = $agent.latestVersion.id
  modelConnectionId = $model.connectionId
  model = $model.model
  input = @{
    instruction = '按地区汇总 orders.csv 的 amount，并生成 summary.csv 和 chart.png。'
    artifactIds = @($ticket.id)
  }
  limits = @{ maxCostMicros = '2000000'; currency = 'CNY' }
} | ConvertTo-Json -Depth 6

$run = Invoke-RestMethod -Method Post -Uri "$api/runs" -Headers ($headers + @{ 'Idempotency-Key' = 'video-run-0001' }) -ContentType 'application/json' -Body $runBody
$run.id
```

预期 HTTP 202 并立即给出 Run ID。查看事件和产物：

```powershell
curl.exe --no-buffer "$api/runs/$($run.id)/events" -H "Authorization: Bearer $env:AGENTLAB_API_KEY" -H 'Accept: text/event-stream'
Invoke-RestMethod -Method Get -Uri "$api/artifacts?kind=output&runId=$($run.id)" -Headers $headers
```

写请求超时后必须保留同一个 Idempotency-Key 查询或重试，不要生成新键盲目重发。回到工作台“运行”，按“调用来源 = API”筛选，展示这条 Run 的来源为 API，再到“产物”或“用量与账单”核对结果。

### 8.3 清理

录制后立即在“API 密钥”页撤销 video-demo。仅为演示创建的模型连接也应撤销；撤销会阻止后续派发，但不能撤回已发送请求或已发生费用。清除临时环境变量，保留仓库提供的演示 CSV：

```powershell
Remove-Item Env:AGENTLAB_API_KEY -ErrorAction SilentlyContinue
```

不要执行 db:down，直到确认已保存截图或审计证据；该命令停止数据库容器但默认保留数据卷。

## 9. 录屏和剪辑建议

### 9.1 画面

- 浏览器建议 1440×900；Run ID、费用和状态应能直接读出。
- 主视频使用 100% 缩放；200% 是可访问性验收视口，可另录短片。
- 关闭浏览器通知、个人标签页、密码管理器弹窗和含秘密的终端环境变量展示。
- 终端只在启动成功或 API 调用时出现；不要展示 .env.dev 内容。
- 录制前清空剪贴板，录制后撤销演示 API Key。

### 9.2 节奏

- 登录、项目创建和连接验证保留完整操作，不要用静态图片替代。
- Run 等待可以加速或剪切，但保留一次状态变化和最终“已完成”。
- 产物预览保留 3–5 秒，让文件名、类型和摘要或图表可读。
- 旁白解释“预占”和“已结算”的区别；不要说“免费”“保证成功”或“供应商已收款”。
- 运行失败时保留失败代码和下一步说明，另起一次录制；不要把失败画面剪成成功。

推荐片尾字幕：

```text
AgentLab 本地 MVP：项目 → 连接 → Agent Playground → 隔离 Run → 产物 → 用量核对
本地验收使用 MongoDB、Docker 沙箱和真实模型连接；云端多租户、支付与 SLA 尚未验收。
```

## 10. 验收清单

- [ ] db:up、build:sandbox、seed:demo 已成功。
- [ ] API 与前端分别监听 127.0.0.1:8787 和 127.0.0.1:5173。
- [ ] /health/live 返回 status=ok，工作台执行能力显示真实状态。
- [ ] 模型连接为“已连接”，模型选择器至少有一个 usable 模型。
- [ ] 项目有 CNY 累计预算，当前项目选择正确。
- [ ] 上传文件显示“服务端摘要校验通过”。
- [ ] 提交前显示费用上限并勾选费用确认；视频没有展示秘密。
- [ ] Run 详情显示真实状态、时间线或工具调用、结果和产物。
- [ ] 至少一个 CSV/PNG 产物可预览或下载，且链接回对应 Run。
- [ ] “用量与账单”显示类别、数量、币种和结算状态；未知值保持“未知”。
- [ ] 若录 API 片段，Run 列表能筛选到“调用来源 = API”，演示密钥已撤销。
- [ ] 字幕和旁白没有声称云端、支付、SLA 或供应商最终账单已实现。

## 11. 常见问题

| 现象                               | 处理                                                                                              |
| ---------------------------------- | ------------------------------------------------------------------------------------------------- |
| db:up 超时或 MongoDB 不可用        | 确认 Docker Desktop 正在运行；检查 127.0.0.1:27018 是否被占用；再次执行 db:up。                   |
| 执行能力提示没有沙箱               | 确认已 build:sandbox，镜像为 agentlab/sandbox-python:20261024，并重启 API；服务不会回退宿主执行。 |
| 连接是“待验证”                     | 在“连接”页点击“验证”；若模型列表没有填写的 ID，修改模型列表或重建连接。                           |
| seed:platform 提示没有平台模型连接 | API 没用包含 BASE_URL/API_KEY 的 .env.dev 启动；重启 API 后再执行 seed:platform。                 |
| Playground 没有可用模型            | 先验证连接；私人 BYOK 还要在项目详情授权；检查当前项目和连接范围。                                |
| 上传后不能提交                     | 检查摘要校验、项目预算、执行能力和费用确认复选框。                                                |
| Run 长时间排队                     | 查看概览执行能力和 API 终端；不要重复提交，先在“运行”列表核对是否已创建。                         |
| Run 为“结果待核对”                 | 不自动重发、不承诺退款；保留预占并按证据核对。                                                    |
| 模型返回工具调用错误               | 查看 Run 错误和下一步；换用可用且已有工具调用证据的模型。                                         |
| 端口被占用                         | 关闭旧进程，或设置 AGENTLAB_API_PORT、AGENTLAB_WEB_PORT；前后端端口必须匹配。                     |

## 12. 停止服务

录制完成后，在 API 和前端终端按 Ctrl+C。若要停止 MongoDB 但保留数据卷：

```powershell
pnpm db:down
```

重新录制时按第 4 节顺序启动即可。普通启动不会自动创建演示账号，也不会自动恢复已撤销连接；需要时显式执行 seed:demo 或在工作台重新配置连接。
