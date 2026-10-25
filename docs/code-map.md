# 当前代码地图

给第一次打开仓库的队友：主业务代码在哪、两套 API 怎么区分、改完该跑什么。产品规则见 [AGENTS.md](../AGENTS.md)；视觉见 [DESIGN.md](../DESIGN.md)。

## 先读哪几份

| 问题 | 文件 |
| --- | --- |
| 产品要做成什么样 | [docs/agentlab-platform-design.md](agentlab-platform-design.md) |
| 现在实际做成了哪一步 | [docs/implementation-status.md](implementation-status.md) |
| 怎么启动、怎么演示 | [README.md](../README.md)、[docs/local-start-and-mvp-video.md](local-start-and-mvp-video.md)、[docs/hackathon-guide.md](hackathon-guide.md) |
| 预设 Agent 有哪些 | [docs/preset-agents.md](preset-agents.md) |
| `/v1` 请求长什么样 | [contracts/platform.openapi.yaml](../contracts/platform.openapi.yaml) |
| 旧学习接口还在哪 | [contracts/openapi.yaml](../contracts/openapi.yaml) |

两份 OpenAPI 不要混用。`frontend/src/api/generated.ts` 由旧契约生成，不要手改。

## 目录

| 路径 | 现在做什么 |
| --- | --- |
| `frontend/src/platform/` | 黑色工作台页面、会话状态、Run / 工具 / 用量 UI |
| `frontend/src/layout/` | 壳层、导航（概览、项目、Agent、工具、运行、连接、密钥、用量、产物） |
| `frontend/src/design-tokens.css`、`frontend/src/workbench.css` | 运行时视觉变量与工作台样式 |
| `frontend/src/learning/`、`training/`、`records/` | 旧学习/资料界面，保留但不作为默认产品入口 |
| `backend/internal/platform/` | 托管平台：目录、Run、工具、预算、账本、沙箱调度 |
| `backend/internal/connection/` | 模型连接与凭据（加密存放，不进沙箱或日志） |
| `backend/internal/sandbox/`、`backend/internal/gateway/` | 容器执行与模型请求转发 |
| `runtime/sandbox/` | Docker 沙箱镜像（CPU / CUDA / 代码 Agent CLI） |
| `scripts/` | 本地启动、契约核对、真实模型验收 |
| `assets/` | 品牌元数据、设计 tokens、本机验收 JSON |
| `compose.test.yaml` | 本地 MongoDB 单节点副本集（默认 `127.0.0.1:27018`） |

Hydro/、OpenSandbox/ 是被忽略的上游参考，不要把它们的品牌或工程规则抄进本仓库规范。

## 两条 API

同一套平台业务有两个入口：

- 工作台会话：`/api/v1/platform/*`，浏览器登录后使用。
- 机器接口：`/v1/*`，API Key（`al_live_...`），可单独监听。

HTTP 默认只绑 loopback。`/health/live` 只表示进程活着；执行器、Docker、文件存储是否可用看工作台状态或 `/health/ready`、`/capabilities`。

旧 `/api/v1/auth`、`/learning`、`/training` 仍在代码里，不是黑客松主演示路径。

## 一次 Run 大致怎么走

1. 工作台或 `/v1` 创建文件票据并上传字节。
2. `POST /runs` 带 `agentVersionId`、模型连接、指令、费用上限和幂等键；立即返回 202。
3. Worker 从 MongoDB 领取租约，预占项目/密钥预算，拉起对应沙箱。
4. 模型在执行域外被调用；工具在无网容器内执行。密钥不会进入工作文件、终端或前端查询接口。
5. 事件经 SSE 推出；产物写入本地 blob 目录；用量只追加。结果未知时不自动重发、不盲目退款。

进程内 Map 不能冒充队列、预占或账本。本地 DPAPI/文件根密钥不等于云端 KMS。

## 改完跑什么

文档/元数据：核对链接、JSON、资产路径即可。

前端：`pnpm --filter @agentlab/frontend check`、`pnpm lint`；布局/交互再加对应 Playwright（1440px 与 390px）。

平台后端：`go -C backend test ./internal/platform/...`。涉及沙箱或真实模型时用 `pnpm test:sandbox`、`pnpm smoke:platform`、`pnpm accept:real-models`，不要在没 Docker / 没模型账户时假装通过。

未开放且不要用前端假成功的能力：草稿与版本发布、工具审批、Webhook、成员与项目审计、自建协议验证、保留期调整。工作台「尚未开放的完善功能」与此一致。
