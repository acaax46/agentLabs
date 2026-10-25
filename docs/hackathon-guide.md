# AgentLab 黑客松协作与演示说明

本文给参赛队友和评委讲解：本仓库现在能演示什么、不能宣称什么、最短启动路径，以及文档该怎么补。产品目标仍以 [平台总设计](agentlab-platform-design.md) 为准；已实现边界以 [实施现状](implementation-status.md) 为准。开发命令以[本地开发指南](development.md)为准。

## 一句话

AgentLab 是面向开发者的托管 Agent 执行与 Tools API 工作台：在隔离沙箱里跑预设 Agent 或独立工具，自选平台模型或自己的供应商账户，拿回产物、运行记录和分项账本。

本机已用真实模型、MongoDB 与 Docker 容器跑通主链路。这不代表云端多租户、商业支付、对象存储、KMS 或 SLA 已实现。

## 演示什么

最短可复核闭环（推荐 2-3 分钟）：

1. 用演示账号登录黑色工作台，确认执行器状态不是“本机没有代码执行沙箱”。
2. 打开一个有累计预算的项目，确认连接页的平台模型为“已连接”。
3. 用「文件分析 Agent」上传 `demo/orders.csv`，提交一次真实 Run。
4. 打开 Run 详情：时间线、工具调用、产物、用量与结算状态都来自后端，不要用静态截图代替。
5. 可选 45-90 秒：签发 API Key，用 `/v1` 再提交一次同一 AgentVersion，证明工作台和机器接口共用同一套权限与账本。

现场逐屏操作与口播见 [演示操作与口播脚本](demo-script.md)，按当前“对话”入口走文件分析和多轮修改；环境准备见 [本地启动与 MVP 视频](local-start-and-mvp-video.md)。九个预设 Agent 的适用场景见 [预设 Agent 目录](preset-agents.md)。

可选加分项（机器条件满足再演示，不满足就明确跳过）：

- GPU 计算 Agent：本机有已探测通过的 NVIDIA 卡，且已构建 CUDA 沙箱镜像。
- 代码 Agent（可选引擎）：已构建 `agentlab/sandbox-agents:20261025`，并说明沙箱无网、模型请求只经平台转发。

## 不要这样讲

| 容易说滑的句子           | 正确说法                                            |
| ------------------------ | --------------------------------------------------- |
| 已经是云端多租户 SaaS    | 本机工作台有工作区/项目边界；云端多租户未实现       |
| 已经对接商业支付/充值    | 项目累计预算是额度，不是已充值余额                  |
| 供应商账单已经对平       | 本机按明示规则计量；供应商最终账单未核对            |
| 沙箱失败会回退到本机执行 | 没有沙箱就拒绝执行，不会降级到宿主                  |
| 用户可以发布自己的 Agent | 九个平台预设已发布；草稿/发布链路未开放             |
| 存储已经按量收费         | 存储只暴露、按日采样，状态为不适用/未知，不是已结算 |
| GPU 价格已是商业报价     | 本机 RTX 3070 Laptop 单价是验证用估算，不是确认报价 |
| 评测分数等于产品已上线   | 验收记录在 `assets/*.json`，只证明当时本机范围      |

演示账号 `admin` / `student` 只属于当前本地原型，不是产品默认账号。密钥、`.env.dev`、完整 API Key 不得出现在录屏、截图或提交材料里。

## 最短启动

```powershell
pnpm db:up
pnpm build:sandbox
pnpm seed:demo
pnpm dev:api --env-file .env.dev
# 另开终端
pnpm seed:platform
pnpm dev
```

浏览器打开 http://127.0.0.1:5173 ，API 在 http://127.0.0.1:8787 。演示账号见[本地开发指南](development.md#本地演示账号)。没有 Docker 或沙箱镜像时，工作台应显示代码执行不可用，不要用 mock 假装成功。

## 队友可以补什么

适合分开提交、且不和总设计抢话的文档：

- 演示脚本、录屏清单、故障排查：补 [本地启动与 MVP 视频](local-start-and-mvp-video.md)。
- 预设 Agent / 工具怎么选：补 [预设 Agent 目录](preset-agents.md)。
- 代码从哪改起：看 [当前代码地图](code-map.md)。
- 视觉改动：先读根目录 [DESIGN.md](../DESIGN.md)，同步 `assets/design-tokens.target.json`、`frontend/src/design-tokens.css` 与 `frontend/src/workbench.css`。

不要另写一份产品定位或第二套配色。不要把旧教育/班级/题库文档改名为项目文档。事实变更写进 [实施现状](implementation-status.md)，并附可复核证据。

文档或元数据变更只检查引用、JSON 语法、资产路径和规范一致性；不要为改文档启动数据库或跑无关产品测试。
