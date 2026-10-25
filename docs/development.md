# AgentLab 本地开发指南

本文集中说明开发环境、本地启动、模型与沙箱配置、API 调用、检查和构建。项目介绍与路线图见[项目首页](../README.md)，逐屏演示见[操作与口播脚本](demo-script.md)，录制准备见[本地启动与最小视频指南](local-start-and-mvp-video.md)。产品范围以[平台总设计](agentlab-platform-design.md)与[实施现状](implementation-status.md)为准。

[开发环境](#开发环境) · [本地启动](#启动本地工作台) · [演示账号](#本地演示账号) · [检查与构建](#检查与构建)

## 开发环境

Node 22.20.x（见 .node-version）、pnpm 9.15.9、Go 1.26.x（见 backend/go.mod），本地数据库使用 Docker Compose。Windows 桌面运行需要 WebView2；其他桌面平台须另验构建依赖。

默认 Node 16 无法运行当前前端工具。本机可在 PowerShell 当前会话选择已安装版本，不修改全局配置：

```powershell
$env:PATH = 'D:/node/nvm/v22.20.0;' + $env:PATH
node --version
pnpm --version
pnpm install --frozen-lockfile
go -C backend mod download
```

其他机器使用自己的 Node 22 路径。锁文件定义具体依赖，不从文档中另选版本。

## 启动本地工作台

以下命令均从仓库根目录执行。首次准备数据库、CPU 沙箱与演示账号：

```powershell
pnpm db:up
pnpm build:sandbox     # 首次需联网下载基础镜像与 Python 依赖
pnpm seed:demo
```

在一个终端启动 API：

```powershell
pnpm dev:api --env-file .env.dev
```

模型可通过工作台“账户连接”添加并验证；使用已有开发配置时，将自己的 `BASE_URL` / `API_KEY` 保存在根目录 `.env.dev`。尚无该文件可参考 [backend/.env.example](../backend/.env.example)，或使用不带 `--env-file` 的 `pnpm dev:api`，再通过页面添加连接。普通启动不自动读取 `.env.dev`，秘密不得提交或写入运行日志。

在另一个终端启动前端：

```powershell
pnpm dev
```

API 默认位于 `http://127.0.0.1:8787`，浏览器打开 `http://127.0.0.1:5173`，使用[本地演示账号](#本地演示账号)登录。进入“账户连接”验证模型；用 `.env.dev` 启动时，共享模型连接会在启动时导入。随后在另一个终端发布开发价目：

```powershell
pnpm seed:platform
```

价目仅供本地验证计量，应在导入连接之后发布，才会覆盖对应模型；它不是商业定价，也不生成虚假用量。随后创建项目、配置累计预算并授权模型连接，即可从“对话”选择 Agent、模型和沙箱，上传文件并提交任务。逐步操作与排查见[本地启动与最小视频指南](local-start-and-mvp-video.md)。

数据库必须是可用的 MongoDB 副本集；连接失败时应用拒绝启动。当前工作台 HTTP API 仅允许本机 loopback。没有沙箱镜像或 Docker 时，平台 `GET /capabilities` 报告 `codeExecution=false` 与原因，代码执行被拒绝，不会退回宿主执行。

停止本地数据库并保留数据卷：

```powershell
pnpm db:down
```

历史 Linux guest 验证使用 `pnpm build:guest`；`AGENTLAB_LEARNING_EXECUTOR_READY=0` 可关闭历史执行器用于普通回归。它与当前平台的 Docker 沙箱能力分别检查。

## 托管 Agent / Tools API

用户侧从工作台“对话”开始：预选 Agent、模型和沙箱后，在聊天框里发消息、上传文件并继续追问。回复与折叠执行进度增量更新，产物卡片可打开预览。消息历史与文件由后端保存，每轮启动并回收容器，不在等待下一条消息时持续占用；每次发送按显示的费用上限预占。普通用户登录默认进入对话，Agent 页面仍提供 API 调试与完整 Run 记录。

后端 MVP 在 `backend/internal/platform`：开发者用 API Key 上传文件、提交 Agent 运行或直接调用工具，订阅事件、取回产物并核对用量与账本。同一组操作有两个入口：`/v1`（API Key，可单独监听用于公网）和 `/api/v1/platform`（工作台会话）。完整契约见 [contracts/platform.openapi.yaml](../contracts/platform.openapi.yaml)。

### API 调用示例

在工作台创建项目、设置预算并签发 API Key（密钥明文只显示一次）后，开发者只需要 `/v1`：

```bash
KEY=al_live_...   # 创建密钥时显示的明文
API=http://127.0.0.1:8787/v1

# 1. 声明并上传文件（uploadUrl 已含 /v1 前缀）
curl -sS -X POST $API/files -H "Authorization: Bearer $KEY" -H "Idempotency-Key: demo-file-0001" \
  -H "Content-Type: application/json" -d '{"name":"orders.csv","size":<字节数>}'
curl -sS -X PUT "http://127.0.0.1:8787<uploadUrl>" -H "Authorization: Bearer $KEY" --data-binary @orders.csv

# 2. 选择 Agent 版本与可用模型，然后提交运行（202，立即返回）
curl -sS $API/agents -H "Authorization: Bearer $KEY"
curl -sS $API/models -H "Authorization: Bearer $KEY"
curl -sS -X POST $API/runs -H "Authorization: Bearer $KEY" -H "Idempotency-Key: demo-run-0001" -H "Content-Type: application/json" -d '{
  "agentVersionId": "...", "modelConnectionId": "...", "model": "...",
  "input": {"instruction": "按地区汇总订单金额并画图", "artifactIds": ["<文件 id>"]},
  "limits": {"maxCostMicros": "3000000", "currency": "CNY"}}'

# 3. 订阅事件（SSE，断线用 Last-Event-ID 续传），再取结果与产物
curl -sSN $API/runs/<run id>/events -H "Authorization: Bearer $KEY" -H "Accept: text/event-stream"
curl -sS "$API/artifacts?kind=output&runId=<run id>" -H "Authorization: Bearer $KEY"
```

金额一律是微单位的十进制字符串（1 元 = 1 000 000）并带币种；重试写请求时保持同一个 `Idempotency-Key`，返回的是原运行，不会重复执行或重复预占。

## GPU 与代码 Agent 引擎（可选）

```powershell
pnpm build:sandbox:cuda     # 在 CPU 沙箱之上加 PyTorch 2.7.1+cu118 与 scikit-learn（约 3 GB）
pnpm build:sandbox:agents   # 在 CPU 沙箱之上加 Node 22、git、ripgrep 与固定版本的 pi / OpenCode / Codex CLI / Claude Code
$env:AGENTLAB_SANDBOX_GPUS = "0"                                         # 可租用的 NVIDIA 显卡序号；留空即不开放 GPU
$env:AGENTLAB_SANDBOX_GPU_IMAGE = "agentlab/sandbox-python-cuda:20261025"
$env:AGENTLAB_SANDBOX_AGENT_IMAGE = "agentlab/sandbox-agents:20261025"
$env:AGENTLAB_SANDBOX_CPUS = "4"; $env:AGENTLAB_SANDBOX_MEMORY_MB = "8192" # GPU 规格需要 4 vCPU / 8 GiB
```

在同一个 PowerShell 窗口执行 `pnpm dev:api --env-file .env.dev`，这些设置才会传给新启动的 API 进程。也可将以下配置写入根目录 `.env.dev`，之后通过同一启动命令加载；修改后需重启 API：

```dotenv
AGENTLAB_SANDBOX_GPUS=0
AGENTLAB_SANDBOX_GPU_IMAGE=agentlab/sandbox-python-cuda:20261025
# 可选：代码 Agent 引擎，需先构建对应镜像
AGENTLAB_SANDBOX_AGENT_IMAGE=agentlab/sandbox-agents:20261025
AGENTLAB_SANDBOX_CPUS=4
AGENTLAB_SANDBOX_MEMORY_MB=8192
```

代码 Agent 引擎需要显式配置 `AGENTLAB_SANDBOX_AGENT_IMAGE`；即使 Docker 中已有镜像，未配置时仍会提示“此部署没有可用的代码 Agent 沙箱镜像”。GPU 默认关闭；“此执行节点没有开放 GPU”表示 API 进程没有收到显卡序号配置。启动后会后台运行 CUDA 检查，短暂显示“正在检查”时稍后刷新即可。

- GPU：Docker 需带 NVIDIA 运行时（Docker Desktop + WSL2 已提供）。每张列出的卡先在 CUDA 镜像里跑一次真实矩阵运算，通过后才出现在规格里；规格按型号逐词匹配显卡名，GPU 按卡型计价、每次代码执行独占整卡，容器仍无网络、只读根、非 root。本机预设为 `gpu-rtx3070-laptop-4-8`（RTX 3070 Laptop），只有声明了 GPU 规格的 Agent 版本（如“GPU 计算 Agent”）能使用。
- 代码 Agent 引擎：“代码 Agent（可选引擎）”的运行可选 `harness`（`pi`、`opencode`、`codex`、`claude-code`）。CLI 在无网络的沙箱里运行整个任务，模型请求经容器回环地址上的转发器、通过标准输入输出到宿主机：宿主机校验请求、固定冻结的模型、限制输出 token、去掉路由/插件/服务端状态与付费服务端工具，用真实凭据转发并记录供应商用量后才把答复交给 CLI；沙箱里没有真实密钥或供应商地址。Codex CLI 只支持 Responses 协议、Claude Code 只支持 Anthropic Messages 协议，所用连接必须提供 `/responses` 或 `/messages`，否则请求被拒绝且不计费。会话期间容器一直占用所选规格，沙箱秒含模型等待。

## 扩容与部署

- 进程角色由 `AGENTLAB_ROLE` 决定：`all`（默认，开发与小规模）、`api`（只服务 HTTP）、`worker`（只执行运行）。API 副本无状态，增加副本即增加请求容量；增加 worker 进程或 `AGENTLAB_WORKER_CONCURRENCY` 即增加并行运行数。运行由 MongoDB 中一次带索引的原子操作领取，租约与 attempt 令牌防止失联的 worker 覆盖新的执行者。
- 所有副本必须共享：同一个 MongoDB 副本集、同一个文件存储（目前是 `AGENTLAB_BLOB_DIR` 指向的共享目录；对象存储后端尚未实现）、同一个 `AGENTLAB_CREDENTIAL_KEY`（连接凭据解密与下载链接签名都由它派生）。执行 worker 的节点需要 Docker（或兼容 CLI）与沙箱镜像；`AGENTLAB_SANDBOX_SLOTS` 限制本节点同时运行的容器数，`AGENTLAB_SANDBOX_RUNTIME=runsc` 之类可换用 gVisor/Kata 加强隔离。
- `AGENTLAB_PLATFORM_LISTEN_ADDR` 另开一个只服务 `/v1`、`/healthz`、`/readyz` 的监听，只认 API Key、不读取浏览器 Cookie，用于放在 TLS 终结代理之后。`/readyz` 会真实运行一次沙箱容器，沙箱不可用时报告降级而不是假装可用。
- 优雅停止：收到 SIGTERM/Ctrl+C 后不再领取新运行，进行中的运行最多再获得 `AGENTLAB_DRAIN_SECONDS`（默认 90）完成，之后交给租约恢复；容器编排的终止宽限期应不短于它。
- 当前限制（详见 [实施现状 §7](implementation-status.md)）：没有压测数据；限流按进程计数；排队是全局先进先出，没有按项目的公平调度；事件流以数据库轮询实现；隔离级别是普通容器。

## 本地演示账号

以下账号仅由显式 seed:demo 初始化创建，普通启动不插入账号。密码修改后再次初始化不会重置。登录地址为 `http://127.0.0.1:5173/#/login`；这些账号仅供本地演示。

| 角色   | 用户名  | 初始密码               | 登录后默认入口 |
| ------ | ------- | ---------------------- | -------------- |
| 管理员 | admin   | AgentLab-Admin-2026!   | /#/overview    |
| 用户   | student | AgentLab-Student-2026! | /#/chat        |

student 是保留的演示用户名，角色为 user，不表示新平台还采用教育角色。初始化会建立标明演示的旧资料，不生成虚构的评测、用量或消费记录。

## 当前配置

| 环境变量                              | 用途                                                                 |
| ------------------------------------- | -------------------------------------------------------------------- |
| AGENTLAB_MONGO_URI                    | 默认 mongodb://127.0.0.1:27018/?replicaSet=rs0&directConnection=true |
| AGENTLAB_MONGO_DATABASE               | 默认 agentlab；测试使用独立数据库                                    |
| AGENTLAB_LISTEN_ADDR                  | 默认 127.0.0.1:8787，仅数值 loopback                                 |
| AGENTLAB_API_PORT / AGENTLAB_WEB_PORT | Vite 代理和开发端口                                                  |
| AGENTLAB_DEV_ORIGIN                   | 额外获准的本机开发来源                                               |
| AGENTLAB_CREDENTIAL_KEY               | 宿主根密钥，32 随机字节的 Base64；不是前端环境变量                   |
| AGENTLAB_CREDENTIAL_KEY_FILE          | 可选宿主根密钥文件；默认用户配置目录                                 |
| AGENTLAB_CONNECTION_ALLOWED_ORIGINS   | 显式授权的私有/测试网关 origin                                       |
| BASE_URL / API_KEY                    | 兼容已有开发模型配置；启动时仅迁移一次为待验证连接                   |

原型的凭据密文保存于 MongoDB；Windows 根密钥受 DPAPI 保护。备份数据库还需单独保管原根密钥及必要的宿主保护信息，不能仅备份业务库。完整环境字段见 [backend/.env.example](../backend/.env.example)。`--env-file` 只加载 [scripts/dev-api.mjs](../scripts/dev-api.mjs) 允许的字段；MongoDB 地址等其他字段需在启动命令所在终端设置环境变量。

## 检查与构建

按变更范围选择检查。仅文档或元数据变更只核对链接、格式与资产路径，不需要启动数据库或运行产品测试。

```powershell
pnpm contracts
pnpm check:platform-contract   # 需要 MongoDB；核对平台契约、真实响应与工作台类型
pnpm format:check
pnpm lint
pnpm --filter @agentlab/frontend check
pnpm check
pnpm build
pnpm build:desktop
```

前端类型从 OpenAPI 生成；Wails 资源由构建脚本复制，不手工修改。桌面构建产物不是已签名安装包。

真实数据库与浏览器检查：

```powershell
pnpm db:up
pnpm test:integration
pnpm --filter @agentlab/frontend exec playwright install chromium
pnpm test:e2e
pnpm test:e2e:live
```

托管平台还有两类需要真实依赖的检查：`pnpm test:sandbox`（先 `pnpm build:sandbox`）在真实容器里验证隔离、资源限额，并让 Agent 运行真实的 pandas/matplotlib 代码；`pnpm smoke:platform` 启动真实的服务进程，仅经公开 API Key 监听走完整条开发者链路；`go -C backend test -race ./internal/platform/...` 需要 C 编译器，本机缺少时在 Linux 容器内运行。

`pnpm accept:real-models` 会消耗真实供应商额度：用 `.env.dev` 里的真实供应商，对三款 Flash 模型各跑一次 CSV 与 Excel 分析、一次自带账户（BYOK）运行和独立工具调用，按产物内容判定结果，并核对用量、账本、预算、存储披露与秘密泄露；加 `--record` 会写出脱敏记录 `assets/p0-backend-acceptance.json`。模型输出不确定，每个场景最多两次尝试并全部记录。需要 MongoDB、Docker 与沙箱镜像，不进入 CI。

GPU 与代码 Agent 引擎另有真实检查：`pnpm test:sandbox:gpu` 在真实显卡上跑 CUDA 检查与隔离的 PyTorch 运算，并让“GPU 计算 Agent”端到端训练一个小模型、按卡型计费（需要 NVIDIA 运行时、两个镜像与 MongoDB）；`pnpm test:sandbox:sessions` 让四个 CLI 各在真实会话容器里对接脚本化模型，验证只经转发器取模型、工具在无网络沙箱内执行并交回文件（不消耗模型额度）；`pnpm accept:code-agents` 用 `.env.dev` 的真实供应商经公开 API Key 监听跑四个引擎与 GPU Agent，按产物内容判定，并检查沙箱环境变量里没有任何凭据（消耗少量真实额度，`--record` 写出 `assets/code-agent-acceptance.json`）。这些都不进入 CI。

普通浏览器回归和真实 guest 场景是不同门槛。可用 AGENTLAB_BROWSER_PATH 指定已有 Chromium。测试端口与数据库由现有配置隔离；受控接口回归与真实依赖验收分别记录，验收范围见[实施现状](implementation-status.md)。

## 仓库结构

| 目录              | 当前用途                                                        |
| ----------------- | --------------------------------------------------------------- |
| frontend/         | React + TypeScript 黑色工作台；对话、项目、运行、产物与用量界面 |
| backend/          | Go 平台 API、任务与工具编排、预算账本、沙箱执行；保留历史业务   |
| contracts/        | 本地 /api/v1 契约（openapi.yaml）与托管平台 /v1 契约            |
| runtime/          | Linux guest、OpenSandbox、CLI/MCP 的开发验证资产                |
| docs/             | 总设计、实施现状、技术图、演示说明、预设目录与代码地图          |
| assets/           | 品牌元数据、目标 tokens、验收证据与演示成品                     |
| scripts/          | 现有开发、构建与检查编排                                        |
| compose.test.yaml | 本地 MongoDB 单节点副本集                                       |

Hydro/、OpenSandbox/ 是被忽略的上游参考目录；.cache/、.tools/ 和 build/ 为本地工具或产物，不作为产品规范。
