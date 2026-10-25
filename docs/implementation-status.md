# 实施现状与迁移边界

核对日期：2026-10-25。本文记录本地代码与迁移边界，不是云服务上线公告。当前托管平台接口与本次计费增量以 `backend/internal/platform`、工作台和 `contracts/platform.openapi.yaml` 为准。商业支付尚未接入。

## 用户对话入口（2026-10-25）

用户侧新增 `/chat`：先选 Agent、模型、CPU/GPU 沙箱与每轮上限，再进入聊天。普通用户登录/注册默认进入对话，管理员概览、API 调试与完整 Run 详情保留。聊天支持纯文本发起、文件上传、多轮追问、刷新后恢复历史、执行进度默认折叠、停止本轮和产物侧边预览；390px 支持附件、横向历史入口及覆盖预览面板。

后端新增 MongoDB `platform_conversations` 和对话 Run 索引；`/conversations` 创建/查询历史，`/conversations/{id}/messages` 幂等创建每轮 Run。轮次、预算预占和入队同事务；六路同键重试不重复执行，审计或预算失败整体回滚。跨身份或跨项目统一拒绝。每轮恢复已授权旧输入和输出，最多 32 文件/64 MiB（同时受部署上限约束）；对话最多 100 轮，上下文只携带最近最多 12 个完整轮次/32 KiB 公开内容。过期记录或文件拒绝继续，结果未知阻止下一轮。

内置引擎使用供应商 Chat Completions 流，`reply.delta` 只呈现公开 summary，支持中文、JSON 转义和 Unicode；工具参数、reasoning 字段不进入回复流。CLI 引擎采用实际消息事件更新，尚非逐 token streaming。容器每轮回收，跨轮保存的是消息与文件，不能声称保留进程、内存、依赖安装和 CLI 原生状态。

已验证 MongoDB 的多轮上下文/文件恢复、并发幂等、审计回滚、未知阻塞、索引计划和流中断处理；真实 Docker 两轮 CSV 计算验证上一轮汇总恢复并正确翻倍；真实 server Smoke 验证创建对话、文本追问、增量事件、产物预览和持久历史，模型为本地受控服务。工作台四视口回归 108/108、登录/注册与原生缩放检查 7/7 通过，含 1440px/390px 聊天链路及静态 HTML 预览隔离；源码类输出使用有上限的文本预览，保留权限校验。新增契约为 53 个操作，69/69 真实响应、67 个工作台类型通过。未重复调用真实供应商。

收尾检查：启用 MongoDB 必跑标记的平台全量 Go 测试、平台 vet、后端全量编译、前端 TypeScript、格式与生产构建通过；lint 无错误，保留 17 条既有警告。最后的桌面/手机聊天专项 6/6 通过。

## GPU 执行与代码 Agent 引擎（2026-10-25）

本节记录 GPU 和代码 Agent 引擎的实现、边界与本机验收。服务端目录现在有 9 个平台预设 Agent：5 个数据类、通用代码生成、工作台搭建师、GPU 计算 Agent、代码 Agent（可选引擎）。

**GPU（本机 RTX 3070 Laptop）**

- 执行：Docker 沙箱新增显卡租约。`AGENTLAB_SANDBOX_GPUS` 列出可租用的卡（留空即关闭），`AGENTLAB_SANDBOX_GPU_IMAGE` 指向 CUDA 镜像 `agentlab/sandbox-python-cuda:20261025`（CPU 沙箱 + PyTorch 2.7.1+cu118 + scikit-learn 1.5.2；cu118 不依赖 CUDA 小版本兼容，适配本机 526.98 驱动）。容器只多 `--gpus device=N` 与 `NVIDIA_DRIVER_CAPABILITIES=compute,utility`，其余与 CPU 沙箱相同（无网络、只读根、去掉全部 capability、非 root）。
- 每张卡先在 CUDA 镜像里跑一次真实矩阵运算，通过后才可租用；检查在后台进行（成功缓存 5 分钟、失败 30 秒），未完成时规格显示“正在检查”，就绪探针与受理不等待它。一次代码执行独占整卡；等卡超过 5 分钟返回“未启动、不计费”的工具失败，而不是结果未知。
- 规格与价格：新增 `gpu-rtx3070-laptop-4-8`（4 vCPU / 8 GiB / 1 × RTX 3070 Laptop，CNY 0.0012/秒、USD 0.00024/秒，其中每卡秒 600 / 120 微单位）；L4 规格改为按硬件判断可用性。规格的 `gpuModel` 与显卡名逐词匹配：`L4` 不会匹配 L40S，`RTX 3070 Laptop` 不会匹配台式 3070。**本机卡的每卡秒价格是替你定的开发预设（约为 L4 预设的 30%，按算力粗略折算），请确认或改价；它只用于验证计量链路，不是商业报价。**
- Agent 版本新增可选 `resourceProfiles`：列出时只能用其中的规格（省略 `resourceProfile` 时取第一个当前可用的），未列出时只能用 CPU 规格；旧版本 JSON 与摘要不变（有测试）。新增预设“GPU 计算 Agent”（`agt_gpu_compute` / `agv_gpu_compute_1_0_0`，沙箱类别 `python-cuda-v1`），只能使用 GPU 规格。

**代码 Agent 引擎（pi / OpenCode / Codex CLI / Claude Code）**

- 之前为什么不能选：平台 Run 只有内置循环——宿主机调用模型，沙箱每次只跑一段无网络的 Python。四个 CLI 只装在开发镜像 `agentlab/agents:validated-20261023` 里，没有接入 Run：它们自己就是 Agent 循环，要在沙箱里调用模型（而沙箱无网络、真实密钥不得进入），要整段会话的工作区，用量还得由平台计量。旧版运行页的 agent 下拉框只是标签，并未启动 CLI。另外 Codex CLI 0.164.0 已移除 Chat Completions（`wire_api = "chat"` is no longer supported），只支持 Responses；Claude Code 只支持 Anthropic Messages；网关原先只有 Chat Completions。
- 现在：Run 新增 `harness`（执行引擎）。CLI 在会话容器里跑完整个任务，容器保持 `--network none`；CLI 看到的模型地址是容器回环上的转发器（`runtime/sandbox/harness.py`），请求通过标准输入输出帧（会话协议 v1）交给宿主机。宿主机按协议白名单重写请求：固定冻结的模型、限制输出 token、强制非流式，去掉 `models`/`provider`/`plugins`/`n`/`user`、`previous_response_id`/`store`、`mcp_servers` 等字段，剥离联网搜索、代码解释器、MCP 等服务端工具，拒绝图片/文件/音频，然后用执行域外的真实凭据转发，记录供应商用量后才交回答复；需要流式的 CLI 由转发器把完整答复合成为 SSE。
- 每个模型请求先过预算闸门（含会话已占用的沙箱秒），后台每秒复核，超限即结束会话、不发出请求；结果未知不重发、保留预占待核对；会话不能续跑，执行节点失联时资源用量记为待核对。
- 计费：沙箱按冻结规格计容器从启动到移除的秒数，会话期间等待模型的时间也计入（用量备注写明）；托管执行费照常；CLI 自己的工具步骤作为“引擎报告”事件记录，不是平台工具调用，不另计费。
- 新增预设“代码 Agent（可选引擎）”（`agt_code_agent` / `agv_code_agent_1_0_0`）：引擎 pi（默认）、opencode、codex、claude-code，规格 cpu-2-4 / cpu-4-8 / cpu-1-1，模型请求上限为工具调用次数 + 6，单步输出上限 16384 token。现有预设版本仍固定内置循环（发布版本不可变）。镜像 `agentlab/sandbox-agents:20261025`（CPU 沙箱 + Node 22.23.3、git、ripgrep，CLI 取自已固定的 tools 镜像，构建不需要 npm 源）。
- 工作台：Playground 增加“执行引擎”选择（不可用的引擎置灰并写原因；内置版本写明固定，并链接到代码 Agent）；资源规格按版本策略过滤（GPU Agent 只见 GPU 规格，CPU 版本不再列出 GPU）；Run 详情显示引擎。契约新增 `harness`、`harnesses`、`resourceProfiles`、`gpuModel`、`capabilities.harnesses`。

**验证（2026-10-25，Windows + Docker Desktop 29.7.2 / WSL2，RTX 3070 Laptop，驱动 526.98）**

- `pnpm test:sandbox:gpu`：CUDA 检查通过（8191 MiB）；锁定的 GPU 沙箱里 2048×2048 矩阵乘 0.5–0.8 秒，网络被阻断、根只读；GPU Agent 端到端（脚本化模型）在卡上拟合线性模型（weight 3.000），22–31 个沙箱秒按 1200 微元/秒计费。
- `pnpm test:sandbox:sessions`：四个 CLI 各在真实会话容器里对接脚本化模型，2 次模型请求完成，shell 工具在无网络沙箱内执行、交回文件与最终答复（pi 3.7 秒、OpenCode 6.8 秒、Codex CLI 8.7 秒、Claude Code 4.0 秒，不含真实模型时间）。
- `pnpm accept:code-agents`（真实 OpenRouter 通道，deepseek/deepseek-v4.1-flash-1017，仅经公开 API Key 监听）：四个引擎与 GPU Agent 全部第一次尝试通过；summary.csv 汇总正确；每个沙箱的 `printenv` 都不含供应商密钥或地址；GPU 拟合 weight 3.000 / bias 1.000；账本、预算与用量一致；服务日志、事件、产物与证据不含密钥。每个引擎 3–5 次模型请求、20–24 个沙箱秒，单次 0.0137–0.0316 CNY；供应商侧合计花费 0.0106 美元。记录见 `assets/code-agent-acceptance.json`。
- Go：sandbox / gateway / platform 全部包通过（MongoDB 副本集，`AGENTLAB_REQUIRE_MONGO_TESTS=1`）；新增 GPU 租约与型号匹配、会话帧协议、协议改写与计量、会话计费、预算截停、结果未知、引擎准入、节点失联的测试。`pnpm check:platform-contract`：64/64 个真实响应与契约吻合，62 个满足工作台类型。前端 TypeScript、lint（0 错误，17 条既有警告）、Prettier 通过。Playwright 平台套件 96/96 通过（含新增的执行引擎与 GPU 规格用例，1440px、1024px、200% 缩放与 390px 四种视口）。
- 为适配新目录顺序，改了几处按“第一个 Agent”取文件分析 Agent 的测试与脚本（`prepareRun`、契约样本、`accept-real-models.mjs`、`smoke-platform.mjs`、两条真实浏览器用例），改为按 `agt_file_analysis` 取；`pricing.spec.ts` 中“CPU 版本列出禁用的 L4”改为“不列出 GPU”。

**未验证与限制**

- GPU：只在一张本机 RTX 3070 Laptop + WSL2 上验证；显存在不同运行之间是否清零、gVisor/Kata 下的 GPU、多卡、MIG 均未验证；GPU 容器共享宿主内核与 GPU 驱动，隔离弱于 CPU 沙箱。每次 code.execute 都重新启动 CUDA 镜像（实测约 9–10 秒冷启动）；多个 GPU 运行轮流占卡，没有排队公平性。`torch.compile`（需要可执行的临时目录）和 DataLoader 多进程（共享内存小）不可用，已写进 Agent 提示词。
- 会话：节点失联后不能续跑（CLI 状态随容器消失）；CLI 的工具步骤只是自报；会话的 `/work` 允许执行（便于运行构建脚本），`/tmp` 仍 noexec；每个会话最多 400 次模型请求、500 条事件。Codex CLI 与 Claude Code 依赖连接实际提供 `/responses`、`/messages`，本次只验证了 OpenRouter 通道，其他供应商需实测；Claude Code 对非 Anthropic 模型会记录 `unrecognized_model` 日志。模型输出不确定，每个引擎只跑了一个小任务，不代表复杂任务的质量、时延或成本。
- 价格：RTX 3070 Laptop 每卡秒价是开发预设，待确认。
- 容量：没有压测；会话占用执行 slot 的时间远长于单次代码执行。

## 前端接入与验收状态（2026-10-25）

后端新增的工作区、项目可用 Agent 数量和存储/调整用量已接入黑色工作台：外壳与设置页读取 `/me.workspace`，项目详情读取 `counts.agents`，用量汇总显示服务端返回的 storage/adjustment 类别与说明，存储明细没有 Run 链接且按“不适用 / 含在基础额度内”展示。工作台类型与真实契约样例同步覆盖这些字段。

本地验证门槛包含当前认证/连接回归、原生 200% 缩放、四视口平台 P0 套件、平台契约、后端全量测试、沙箱和 Smoke。仓库不配置远程 CI；这些命令只在开发机按需运行，结果以本地日志和 `.cache` 证据为准。

## P0 分工外收尾（2026-10-25）

本轮关闭前端接入、本地验证门槛和文档状态同步；真实供应商、存储计量策略、`/me` 工作区和项目 Agent 数量的后端实现记录保留在文末。

- **P0-07/20 预算口径**：项目创建和详情统一“项目累计预算”，说明不会按月重置；API Key 月度额度继续按月控制，不改已有账户数据。
- **用量只追加**：人工核对在同一事务内追加 `platform_usage_resolutions` 和差额记录，原始 `platform_usage` 不再改写；查询投影关联核对证据展示当前结算状态。并发核对只允许一次提交，审计失败全部回滚；原始用量逐字段不变、汇总不重复计费已有 MongoDB 回归。
- **P0-05/08/25 认证与本地验证**：默认 E2E 运行当前登录/注册/改密/退出/移动抽屉、真实连接验证/轮换/撤销/归属与原生缩放测试；旧认证/连接测试另存 `*.legacy.spec.ts`，旧业务测试源码保留。平台 P0 套件、契约和后端检查均保留本地命令，不依赖远程 CI。
- **P0-03/24 键盘**：共享 Tabs 增加方向键、Home/End 和单一 Tab 焦点；四种视口验证键盘打开原生文件选择器、上传后提交、通过导航领取产物、Esc 归还焦点。自动化只在文件选择器打开后提供所选文件，不把系统文件对话框本身宣称为人工无障碍认证。
- **原生 200% 缩放**：新增独立 Chromium 临时扩展与浏览器配置，通过 `chrome.tabs.setZoom/getZoom` 验证真实倍数为 2，并检查登录、项目表单和工作台页面的 CSS 视口重排；不是 `deviceScaleFactor` 替代缩放。测试不修改用户浏览器配置，临时目录在本任务缓存下清理。
- **P0-09 Excel**：复用已有真实容器工作簿测试，并补 Run 镜像冻结断言；生成真实 XLSX、上传、隔离执行 pandas/openpyxl、下载后再次打开验证数值。此项使用可控模型响应，不触碰另一位 Agent 的真实模型验收。
- **版本与计费证据**：保留已发布工具内容不变，目录返回独立 `billingPolicy`，明确资源另列与旧算力包含两种规则；Run 受理时保存本地 OCI 镜像 sha256 ID，执行只使用该 ID 并禁止隐式拉取，运行详情可查。Agent 版本的 profile 不再被当作实际镜像摘要。

验证记录：`go test ./internal/platform/...` 全平台重跑通过（225 秒），核对不可变/并发/审计回滚在其中；真实 Docker 的 Excel、资源秒与固定镜像测试通过，`go vet ./internal/platform/...` 通过。默认真实认证/连接/原生缩放 E2E **7/7**、四视口 P0 回归 **84/84** 通过。格式、TypeScript、lint（17 条现存警告、0 错误）、生产构建通过；平台契约检查 **62/62** 个后端响应吻合，60 个满足工作台类型。前端接入 workspace、Agent 数量、storage/adjustment 用量后，本地相关回归已重新运行并通过。

## MVP 核心需求复核（2026-10-24）

**结论：本机浏览器 MVP 的主要业务闭环已实现；按 DESIGN.md 第 14.2 节，工作区/Agent 数量/存储用量接入与本地验证门槛已完成。** 真实模型、Docker 和外部 API 调用已有成功证据；云端多租户、支付与 SLA 仍未实现。当前服务端目录包含 9 个平台预设 Agent；用户自定义 Agent 的草稿、发布、版本审批与工具权限链路仍未开放。

复核依据为 [MVP 必须项](../DESIGN.md#142-p0mvp-必须项)、当前源码、[脱敏真实验收记录](../assets/p0-acceptance.json)，以及本次对现有验收服务的只读核验。平台供模 Run `run_bc33cd850f514db97436bab890952778`、外部 API + BYOK Run `run_f87bb99018a9af146bb6f3794dedc9d0` 均为 succeeded / settled，模型为 `deepseek/deepseek-v4.1-flash-1017`，后者实际使用了两类工具。这不代表 MiniMax、GLM 已分别通过真实推理验收。

本轮重新执行：平台套件 96/96（含执行引擎与 GPU 规格回归）、全量格式、TypeScript、lint、生产构建和平台契约检查均通过。lint 有 17 条既有警告，构建有包体积提示。64 个后端真实响应符合契约，其中 62 个满足工作台声明类型。真实 GPU、session 容器和代码 Agent 证据来自本机验收；本轮没有重复消费代码 Agent 供应商额度。

| 必须项 | 复核结果 | 事实、证据与边界 |
| --- | --- | --- |
| P0-01 设计变量 | 已实现 | 77 个根变量一致；减少动效覆盖单独生效。 |
| P0-02 应用壳 | 已实现 | 侧栏、顶栏、项目切换、移动抽屉、inert、Esc 与焦点归还。 |
| P0-03 共享组件 | 基本实现 | 字段错误、toast、模态、状态、工具树、精确金额与事件恢复可用；尚无逐组件、全部交互态的完整验收清单。 |
| P0-04 图标 | 已实现 | 工作台使用代码原生单色 SVG，过渡品牌图片保留。 |
| P0-05 登录/注册 | 已实现、回归待迁移 | 当前认证与黑色页面可用；旧认证浏览器测试仍断言旧路由，见问题 4。 |
| P0-06 概览 | 已实现 | 真实项目预算、进行中与需处理 Run，无业务占位数据。 |
| P0-07 项目 | 已实现 | 创建、累计预算、授权、服务端 Agent/Key 数量和项目过滤可用。 |
| P0-08 连接 | 已实现 | 平台供模/BYOK、验证/轮换/撤销、来源与项目授权；列表验证不等于各模型推理通过。 |
| P0-09 Agent/Playground | 实现完成、验收有边界 | 9 个平台预设 Agent 的不可变版本、上传、模型/资源、限制、费用确认与 curl 可用；GPU Agent 需要本机 CUDA 条件，代码 Agent 支持固定版本执行引擎；用户自定义 Agent 草稿/发布仍属 P1；尚无 Excel 端到端验收记录。 |
| P0-10 运行列表 | 已实现 | 状态/来源筛选、分页、API Key 名称、自动刷新与更新时间。 |
| P0-11 Run 详情 | 已实现 | 游标事件与断线恢复、工具树、停止/未知状态、费用拆项、预算变动与产物入口。 |
| P0-12 产物 | 已实现 | 元数据、CSV/JSON/文本、隔离 Markdown/PNG、短时签名下载与过期/归属检查。 |
| P0-13 API Key | 已实现 | 项目/scope/期限/费用/速率限制、只显一次与保存确认、撤销与归属。 |
| P0-14 用量 | 已实现（只披露存储） | 模型、托管、沙箱、工具、BYOK、storage/adjustment、时间筛选与账本可用；存储按服务端日采样披露，不进入应付金额。 |
| P0-15 设置 | 已实现 | 账户、角色、服务端工作区、改密与退出；浏览器为当前平台入口。 |
| P0-16 真实数据 | 已实现 | 主业务来自后端，不使用浏览器持久化业务对象；未开放模块受能力控制。 |
| P0-17 幂等 | 已实现 | 持久回执与事务，相同内容手动重试复用键。 |
| P0-18 秘密 | 已实现 | 执行域外保管，API Key 明文只在临时创建态显示，不进入业务查询或浏览器存储。 |
| P0-19 权限 | 已实现（本机） | 项目归属、scope、下载、撤销与统一 404；不等于云多租户验收。 |
| P0-20 费用表达 | 部分完成 | 微单位、币种、未知、BYOK、费用确认已实现；预算周期和工具旧计量描述需统一。 |
| P0-21 不可逆操作 | 已实现 | 停止、撤销 Key/连接均有确认、影响说明与安全初始焦点。 |
| P0-22 预览隔离 | 已实现 | sandbox iframe + CSP，阻止远程 Markdown 资源与脚本，清理日志控制序列。 |
| P0-23 真实端到端 | 本机主链路通过 | 已有真实 DeepSeek + MongoDB + Docker + 无会话 API Key 调用及产物证据。 |
| P0-24 布局/可访问性 | 本机通过 | 1440px、1024px、390px、等效 200% 和原生 Chromium 200% 缩放、键盘主链路、减少动效与对比度均有本机证据。 |
| P0-25 工程 | 本机通过 | 格式/类型/lint/构建/契约通过；认证/连接和平台 P0 套件均由本地命令运行。 |

优先收尾项：

1. **项目预算被误称为月度预算。** `frontend/src/platform/pages/ProjectsPage.tsx` 两处使用“月度预算”，但 `backend/internal/platform/projects.go` 的项目预算账户使用空 `period` 和累计 `spent`，没有按月重置。只有 API Key 限额按月份建立账户。应采用累计项目预算并修正文案，或实现真正的周期预算；当前不能承诺下月自动恢复项目额度。
2. **待核对结算会改写原始用量。** 已由追加核对记录与查询投影关闭，原始用量保持不可变。
3. **存储尚未计量。** 已由后端日采样披露关闭；当前只披露、不计费，尚无存储额度、超额规则或价目。
4. **远程 CI 未配置。** 项目按本地开发机验收，MongoDB、Docker、浏览器和真实模型依赖不上传 GitHub；对应本地命令和结果保留在 README 与 `.cache` 记录中。
5. **版本和计费说明有缺口。** 已发布工具的计量描述仍称“沙箱算力已包含在托管执行中”，新价目可按 `sandboxBilling=separate` 另列，须遵守不可变发布规则修订版本或补充当前策略展示。Agent 的 `Sandbox` 为 profile 名，未固定实际镜像 digest；总设计要求的镜像可复现性还未完整实现。
6. **验收剩余边界。** 真实模型质量、并发容量、云端多租户、支付与 SLA 仍不在本机 MVP 证据范围；前端 P0 回归使用受控接口，真实后端主链路由 live acceptance 和公开 API Smoke 覆盖。

P1 已提前完成工具目录/契约展示、独立异步 Tools API 后端、基础账本及预算阈值提示。仍未完成用户自定义 Agent 的草稿/发布管理、工具独立调用工作台与历史、审批、账单导出/完整筛选、成员管理、Webhook/outbox、多语言 SDK 示例与高级检索。支付、云弹性、多租户、对象存储与 SLA 属于后续范围，不作为本机 MVP 主链路缺失项。

## P0 工作台补齐与真实主链路验收（已有实施记录）

工作台已补齐服务端游标分页与 URL 筛选、运行列表自动刷新、API Key 来源、项目 Agent/密钥数量与共享连接、模块能力导航、工具次数限制、逐文件上传校验状态、断线事件续传、工具调用树、费用与项目账本、分段预算及 80% 提醒、一次性密钥保存确认、CSV/JSON/文本与隔离 Markdown/PNG 预览、请求 ID、会话过期提示、键盘焦点和减少动效。金额精确值可展开，未知数量/金额使用明确标记，不将已知部分或 BYOK 的空金额当成结算总额。

真实 Playwright 验收使用独立 MongoDB 数据库、生产前端构建、已有模型账户与 `agentlab/sandbox-python:20261024` Docker 镜像。1440px、1024px、390px 和 200% 等效 CSS 视口均跑通项目、上传、真实模型调用、真实代码执行、CSV/PNG 生成、图片预览、签名下载和账本。桌面还跑通私人 BYOK、项目连接授权、一次性密钥与无会话外部 `/v1/runs` 调用，工作台能显示机器身份名称；BYOK 模型金额为 null、不计入平台应收。另验证跨项目 Run/产物与不存在资源统一返回 404、密钥撤销后返回 401、实际取消与释放预占。

证据：`frontend/tests/live-platform` 是可重复运行的真实验收，`frontend/tests/platform` 为界面/异常回归；`.cache/p0-live-evidence.json` 记录脱敏的真实 Run、容器边界与产物摘要，`.cache/p0-acceptance-results` / `.cache/p0-ui-results` 保存本机结果。77 个设计变量与目标值一致，16 组正文、按钮、状态、输入边界与焦点对比度通过计算，记录在 `assets/design-validation.json`。Go 的 P0 统计、项目归属及真实容器测试通过；前端 lint 无错误、TypeScript、相关格式和生产构建通过。

这些证据只覆盖本机部署。200% 用 720×500 CSS 视口与 2 倍设备像素比验证等效重排，并不声称验收所有浏览器缩放实现。存储实计量/计费后端尚未提供证据，界面保持未知；微虚拟机、gVisor/Kata、云对象存储、云端多租户、支付、供应商最终账单与 SLA 均不在此次通过范围。以下计费阶段中的“模型使用夹具”只描述该阶段测试，不覆盖本节后续的真实模型验收。

本次计费增量以当前 `backend/internal/platform` 与工作台代码为准：已新增三款 Flash 模型起始价（OpenRouter 接口基础价快照、来源与日期、固定人民币换算）、管理员价格版本管理、沙箱规格价格与 Run 冻结、节点资源限制、隔离执行秒计量、预算与账本拆项。工作台选择规格与 API `resourceProfile` 使用同一路径。管理员 tenant 共享渠道属于平台供模，私人渠道属于 BYOK。旧托管价已含算力时禁止叠加资源费；GPU 资源需显式配置 CUDA 镜像、设备索引和真实 CUDA 检查，未配置或检查失败时拒绝运行；GPU 价格按卡型预设，不宣称商业报价。本段仅记录此次变更，不将历史占位说明或现有本地接口推广为云支付、多节点调度或 SLA 上线。

本次验证：MongoDB 副本集上的完整平台测试、价格并发导入/不可变快照/幂等提交/未知用量测试、真实 Docker 2 vCPU / 4 GiB 沙箱执行与资源秒结算、64 个真实响应的契约检查，以及 1440px / 390px 共 24 项 Playwright 界面测试均通过；前端 lint（现存警告，无错误）、TypeScript、相关格式与生产构建通过。模型响应在测试中使用可控夹具，未消费真实模型额度；GPU 已完成本机 RTX 3070 Laptop 的真实 CUDA 与 Agent 验收，商业支付未验收。

## 托管 Agent / Tools API 后端（本机 MVP）

范围：`backend/internal/platform`（服务）及子包 `api`（HTTP）、`money`、`ids`、`blob`、`gateway`、`sandbox`、`table`；`backend/cmd/server`（进程角色与公开监听）、`backend/cmd/seed-platform`（开发价格表）；`runtime/sandbox`（沙箱镜像与协议）；[contracts/platform.openapi.yaml](../contracts/platform.openapi.yaml)（49 个操作）。运行方式见 [README](../README.md)。

**已实现并有自动化测试**

- 项目、成员、预算账户与只追加账本；API Key（只存摘要，含 scope、限流、月度上限、到期、撤销，明文只显示一次）；工作台会话与 API Key 两种身份，访问他人资源一律返回 404。
- 文件两步上传（流式写入、内容识别、保留期、签名下载链接、安全响应头）；Run 受理在一个事务内完成校验、价格冻结、预占与入队，写操作幂等；Agent 循环使用 OpenAI 兼容 function calling 与内置工具 `code.execute`、`files.transform`，对话日志使中断后可续跑，并有预算闸门、取消，以及结果未知时冻结预占、由管理员凭证据核对。
- 独立 Tools API、用量与账本查询、项目概览、JSON 与 SSE 事件（服务端序号续传）；审计事件与业务写入同一事务，审计失败则业务回滚。
- 进程角色、租约与 attempt 令牌、失联 worker 恢复、由集群锁保护的过期文件清理、优雅停止。

**扩容设计与对应证据**

| 设计点                                               | 证据                                                                                                                                        |
| ---------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| API 副本无状态，可任意增减                           | 两个独立构造的副本互相验证下载链接与 API Key；在一个副本撤销的密钥在另一副本立即失效                                                        |
| 领取运行是一次带索引的原子更新，成本不随队列长度增长 | 22 条热点查询用 `explain` 断言走指定索引、无全表扫描与内存排序；该测试发现并修复了领取排序与单次运行用量/账本读取的索引缺口                 |
| 并发正确性                                           | 预算与并发额度的原子条件更新、取消与领取的竞态、8 路争抢集群锁只有一个获胜者；`-race` 通过                                                  |
| 存储与执行后端可替换                                 | `blob.Store`（目前为共享目录）与 `sandbox.Runner`（目前为 Docker，可配置 gVisor/Kata 运行时）接口；对象存储后端未实现                       |
| 数据模型可分片                                       | 业务集合索引均以租户为前缀，兼容 `{tenantId, projectId}` 分片键；API Key 前缀须全局唯一，分片时该集合需保持不分片（设计推断，未做分片验证） |

**资金与证据规则（有测试）**：金额为整数微单位并以字符串传输；未知用量保持 null，估算标为 estimated；已发出而结果未知的请求冻结预占、不重发不退款；取消与失败不撤回已发生的模型与沙箱用量，排队中取消零费用。托管执行费与时间上限从当前尝试开始计量：节点失联与等待恢复的时间既不计费，也不占用运行的时间上限（此前按首次启动计，会在节点故障后多收费并使短调用无法恢复，已修正并加回归测试）。

**验证记录（2026-10-24，Windows + Docker Desktop，最终代码状态；2026-10-25 的复验与真实模型验收见文末）**

- `go build`、`go vet`、golangci-lint v2.15.0 通过；`go test ./...` 全部包通过；按本地命令统计总覆盖率 86.8%（门槛 70%），平台包 84.8%。
- `go test -race ./internal/platform/...` 在 Linux 容器内通过（本机没有 C 编译器）。
- `pnpm test:sandbox`：真实容器中的 13 项检查（非 root、无 capability、无网络、只读根、无宿主挂载与秘密、内存/进程数/磁盘/输出上限、超时终止、无遗留容器），以及让 Agent 运行真实 pandas/matplotlib 代码并取回 CSV 与 PNG 的完整流程。单次冷启动容器约 1 秒，来自本机 Docker Desktop，不是生产数字。
- `pnpm smoke:platform`：真实服务进程、真实 Docker 沙箱、受控的 OpenAI 兼容替身模型，仅经公开 API Key 监听走完上传、运行、事件流、产物、签名下载、用量、账本与独立工具调用，并断言服务商密钥与 API Key 从未进入模型请求。Windows 上不检查优雅停止（已用 Ctrl+Break 手工验证退出码为 0），Linux 作业检查 SIGTERM。
- `pnpm check:platform-contract`：契约有效、可生成类型化客户端、60 个真实响应与契约逐项吻合，其中 59 个满足工作台声明的 TypeScript 类型。
- 平台契约、沙箱、Smoke 与竞态检查均按本地命令验证，不配置 GitHub Actions。

**未实现、未验证与已知限制**

- 隔离是普通容器，与宿主共享内核；gVisor/Kata 只有配置入口，未验证。GPU 只在本机 RTX 3070 Laptop + WSL2 上验收，其他硬件和增强隔离未验证。
- 没有压测，没有容量或延迟数字：上述扩容能力是结构性设计加正确性测试，不是性能承诺。
- 限流按 API 进程计数，多副本下只是近似；排队是全局先进先出，没有按项目的公平调度或优先级；SSE 通过每秒轮询数据库实现（可改为 change stream）；工作进程没有 HTTP 健康端点。
- 未实现：回调/Webhook 与 outbox、工具审批流、项目成员管理接口、自建模型端点、对象存储后端、Excel 原生读写、账本导出、云端多租户与 KMS、商业支付。
- 上述 2026-10-24 的验证只用受控替身模型；三款 Flash 的真实供应商验收见文末“后端四项收尾”。价格为开发或预设价目，不是商业定价。共享连接沿用旧的本地连接存储，租户范围仍是单租户的本机空间。

以下各节保留文档重置时的历史基线与后续边界，不覆盖上方此次计费实现和验证。

## 1. 代码中已有的基础

| 领域          | 当前事实                                                         | 新定位下的处理                                            |
| ------------- | ---------------------------------------------------------------- | --------------------------------------------------------- |
| 前端          | React/TypeScript，浏览器与 Wails 共用；仍以旧学习/管理页面为主   | 保留运行，后续迁移为项目、Agent、Tools、Runs 与账单控制台 |
| 登录          | 本地用户与管理员，密码哈希、会话及后端授权                       | 复用；云端租户、项目和机器 API 身份另做                   |
| 持久化        | MongoDB 副本集，资料、任务、预算与运行记录                       | 复用事务与索引方法；新集合不等于已迁移                    |
| 模型账户      | 个人/共享 API Key 连接、验证、轮换、撤销，秘密在执行域外保管     | 复用；共享范围仍是旧本地空间，需补项目授权                |
| 模型调用      | 宿主发起 OpenAI 兼容文本请求，记录响应与用量                     | 已有单轮模型路径；未完成任意 Agent 的多步工具调用循环     |
| 执行          | 独立 Linux guest 中运行 OpenSandbox execd，可信控制面在 guest 外 | 作为隔离验证基础；云端弹性、节点调度与 SLA 待实现         |
| 队列          | MongoDB job、租约、attempt、事件与恢复逻辑                       | 复用；对外 Run API 和多节点能力需重新验收                 |
| 版本/结果校验 | 发布快照、冻结文件、独立判题与结果版本                           | 作为不可变发布和产物验收的参考；不是新行业规则库          |
| 预算/用量     | 内部预算预占、价格版本、用量证据和只追加账本                     | 旧维度含课堂对象；需迁移到租户/项目/身份/Run              |
| 商业支付      | 尚未建立面向客户的完整收款与结算服务                             | 不把预算余额称为已充值余额，不把测试价格称为供应商账单    |
| Tools API     | 有执行与连接器实验代码，没有正式对外通用 Tools API               | 新契约、授权、工具版本与单独计费待实施                    |

现有用户材料、账户与 API Key 的本地加密秘密不因文档改版删除。旧业务数据需要后续迁移方案，不能直接把班级 ID 当项目 ID 或把学生 ID 当 API Key ID。

## 2. 运行入口仍按现有代码

- 本地网页默认 http://127.0.0.1:5173，API 默认 http://127.0.0.1:8787。
- 现有业务接口主要为 /api/v1/auth、/api/v1/connections、/api/v1/learning、/api/v1/training、/api/v1/runtime 及旧资料/记录接口，实际范围以 [当前 OpenAPI](../contracts/openapi.yaml) 和路由代码为准。
- [平台总设计](agentlab-platform-design.md) 中的 /v1/runs、/v1/tools 等是未来目标，不是当前可调用地址。
- 当前 HTTP 服务限定数值 loopback，使用本地会话与来源约束；不能直接将它暴露公网当作云 API。
- Wails 保留为当前桌面入口，未来云控制台不要求客户安装桌面端。

## 3. 现有账户连接的实际行为

代码见 backend/internal/connection。连接保存后初始待验证；通过固定模型列表端点核验授权与允许模型。模型列表验证不等于所有模型都完成了推理测试。秘密不由页面查询或原生接口导出。

凭据以 AES-GCM 加密；根密钥来自 AGENTLAB_CREDENTIAL_KEY 或宿主文件。Windows 使用当前用户 DPAPI，其他已实现文件路径要求受限权限；这不是已经部署的云端 KMS。

已有 BASE_URL/API_KEY 可在启动时迁移一次为本地共享连接；正常页面录入不需要环境变量。重启不恢复已撤销连接，任务使用固定的 connectionId 与 generation。已发送而结果未知的请求保留预占并待核对。

机构 SSO、第三方 OAuth、通用 MCP 账户授权、跨租户凭据托管和自建模型私网链路尚不能从该实现推定为已完成。

## 4. 前端现状与新视觉

[DESIGN.md](../DESIGN.md) 的黑色极客风已应用到前端 MVP 外壳与主要工作台页面：frontend/src/design-tokens.css 写入目标语义变量，frontend/src/workbench.css 承载应用壳、共享控件和新页面样式。导航现为概览、项目、Agent、工具、运行、连接、API 密钥、用量与账单、产物和设置。

当前边界：

- 登录、账户连接、密码修改与现有服务状态继续调用本地 /api/v1 接口。
- 项目、Agent、Run、API Key、用量、账本与产物使用 frontend/src/platform/state.tsx 的 localStorage 演示数据；页面明确标记 MVP 占位，不发起真实执行、不扣费、不发放可用密钥。
- 旧学习、资料、班级、训练与判题页面源码仍保留，但没有进入新路由和导航；旧 CSS 文件归档保留、不再被新入口导入。
- 黑色外壳与页面迁移尚未经过真实业务数据、云端多租户、商业支付或正式 SLA 验收；构建通过不等于后端能力上线。

[assets/brand.json](../assets/brand.json) 记录品牌方向；[design-tokens.target.json](../assets/design-tokens.target.json) 是目标变量快照，当前应用通过 design-tokens.css 实现同一语义值。旧图标图集和旧画廊已从 assets 目录清理；frontend/public 中服务旧学习页面的素材仍不属于新版视觉依据。

## 5. 后续需要新增的工程能力

1. 租户/项目边界、项目 API Key、scope、速率与并发限额。
2. 正式 AgentVersion/ToolVersion、可发布模板、真正的工具调用循环。
3. 对外异步 Run/ToolCall 契约、SSE 恢复、签名回调与产物授权。
4. 云端执行节点、弹性资源、对象存储和独立凭据服务。
5. 项目化预算、工具与算力计量、商业支付和费用对账。
6. 黑色工作台导航、页面与状态迁移。

功能优先级与验收条件按总设计阶段执行。上述云端 API、持久化与商业能力尚未启动；前端本地占位数据不改变这些事实。

## 6. 验证记录的使用规则

仓库已有 Go、MongoDB、Playwright 和真实 guest 验证代码，也有本机 .cache 下的历史结果。历史截图、passed 字段和某次真实调用不证明新平台已经通过同等验收；每次实现后按变更范围运行必要检查。

前端变更已执行 lint、TypeScript 与生产构建，并用 Playwright 检查桌面、1024px 与 390px 布局；没有重跑依赖旧页面和真实后端的业务测试，也未生成真实执行记录或修改 CI、依赖、OpenAPI 与生成客户端。

旧设计文档和分散验证叙述已移除，其有用边界与现状集中在本文及总设计中。产品源码、测试源码、锁文件和历史二进制素材继续保留；它们描述的是实际实现或历史资产，不构成另一套新产品规范。

## 后端四项收尾：关闭记录（2026-10-25）

本节关闭上方“P0 分工外收尾”所说的文末四项清单：三款 Flash 的真实供应商验收、存储计量、`/me` 工作区、项目 Agent 数量。后端字段已接入 `frontend/`，工作台显示 workspace、Agent 数量、storage/adjustment、执行引擎和资源策略。上方 2026-10-24 复核表保留为问题发现记录，其中 P0-14 “存储只有未知提示”、P0-07 与 P0-02/15 的相关行以本节为准。

| 条目 | 条款 | 结论 | 实现与证据 |
| --- | --- | --- | --- |
| 真实模型验收 | P0-23（后端部分） | 本机通过 | `pnpm accept:real-models`：真实供应商（`.env.dev` 的 OpenRouter 通道）、真实 Docker 沙箱、独立数据库，仅经公开 API Key 监听。minimax、deepseek、glm 三款 Flash 各跑一次 CSV 分析和一次 Excel 分析，另有一次 BYOK 运行（glm）与独立 `code.execute` / `files.transform` 调用。最终一次 8 个场景全部第一次尝试通过。 |
| 存储计量 | P0-14 | 后端已实现（只披露，不计费） | 每个项目每个 UTC 日采样一次仍在保留期内的文件；`GET /usage?category=storage` 返回 `mib_day` 行（向上取整，不足 1 MiB 按 1 MiB），状态 `not_applicable`、金额 null、备注“含在基础额度内、不单独计费”。没有文件的项目没有存储行，而不是 0。用量汇总每组新增 `note`，说明为何没有平台费用；`not_applicable` 不再算“金额未知”。`AGENTLAB_STORAGE_SAMPLE_SECONDS` 控制采样间隔。证据：`storage_test.go` 的 4 项存储测试（含 3 个节点 9 路并发只记 1 行、循环自行采样且不重复、次日自动补一行、过期文件不再计入）、`artifact_expiry` 索引断言、配置校验，以及 HTTP 级的 `TestProjectDetailAndUsageBreakdownTellTheTruthOverHTTP`。 |
| `/me` 工作区 | P0-02、P0-15 | 已实现 | `/me` 对用户与 API Key 身份都返回 `workspace {id, name}`，名称来自 `AGENTLAB_WORKSPACE_NAME`（默认“AgentLab 工作区”，1–60 字符）。证据：`TestTheShellLearnsTheWorkspaceAndRoleFromTheBackend`（HTTP）、`TestTheWorkspaceIsDescribedToThePeopleWorkingInIt`。 |
| 项目详情数量 | P0-07 | 已实现 | `counts` 新增 `agents`（已发布 Agent 数；Agent 属于目录，不属于某个项目）。`connections` 与 `apiKeys` 改为只统计此刻可用的：连接须与工作区共享或已授予该项目，且已验证、未过期、未撤销；Key 须未撤销、未过期。证据：`TestProjectDetailCountsWhatTheProjectCanUseRightNow`、`TestProjectDetailAndUsageBreakdownTellTheTruthOverHTTP`。 |

**存储口径（我替你做的产品决定，请确认）。** 总设计 7.1 要求存储有“基础额度、保留期与超额规则”。目前只有保留期（默认 30 天，可按项目调整）；没有额度数值、超额规则和存储价目。所以本轮只做披露：不进入预算、账本与应付，也不能称为“已免费”或“已结算”。要对存储收费，须先发布价目、额度与超额规则，并把日采样改为字节·秒级计量（日采样粒度不足以计费）。该口径已写入总设计 7.1.2。

**BYOK 的“由客户承担”。** 自带账户的模型用量单列为 `not_applicable`、金额 null，用量汇总该组的 `note` 写明“模型费用由客户在自己的供应商账户承担，平台不代收”；运行仍按平台供应的工具与托管执行计费。`TestARunOnTheCustomersOwnAccountIsBilledForWhatThePlatformSuppliesOnly` 覆盖：运行成本只含工具与执行、项目已花费额等于运行成本、令牌数照常记录。

**真实模型验收的结果与发现。** 记录见 `assets/p0-backend-acceptance.json`（脱敏：只含 Run 与产物摘要，不含密钥或文件内容）。最终一次（2026-10-25 01:58）：CSV 分析 deepseek 11 秒、minimax 34 秒、glm 64 秒，Excel 分析 glm 14 秒、deepseek 17 秒、minimax 34 秒，BYOK（glm）53 秒。结果按产物内容判定，不看模型自述：汇总值 east 15.75 / west 20，PNG 与 xlsx 都是真文件。平台供模的模型用量均为供应商上报并已结算；BYOK 模型用量无平台金额；用量结算合计 71699 微 CNY，等于账本结算合计，项目预算没有残留预占；存储以 1 MiB·日披露；服务日志与记录不含供应商密钥或 API Key。本轮三次真实运行合计的供应商侧花费约 0.014 美元，最终一次 0.0078 美元。

- **发现：`files.transform` 的嵌套 `options` 对部分模型不友好。** glm 反复发出不带 `options` 的 `summarize` 调用（最终一次的 CSV 运行里 6 次，约 3 万 token），minimax 把数组包成 `{item: …}`，两者最终都靠 `code.execute` 完成；失败调用不计费，代价是多耗 token 与时间。工具报错现在带参数示例并有测试，但没有改变 glm 的行为。根治要发布 `files.transform` 新版本（把常用选项展平为顶层字段）和对应 Agent 新版本；已发布版本不可变，本轮没有做。
- 模型输出不确定，每个场景最多两次尝试并全部记录；换模型版本后需要重跑。

**工作台接入已完成。** 外壳与设置页读取 `/me.workspace`；用量页展示 `storage` 行与各组 `note`，`not_applicable` 显示为“不适用 / 含在基础额度内”，存储行没有 `runId` 时不生成 Run 链接；项目详情读取 `counts.agents`；用量类别筛选支持 `storage` 与 `adjustment`。

**本轮验证（2026-10-25，Windows + Docker Desktop）。**

- `gofmt`、`go build`、`go vet`、golangci-lint v2.15.0：0 个问题。
- `go test ./...`（本地 `-covermode=atomic -coverpkg=./internal/...` 命令，`AGENTLAB_REQUIRE_MONGO_TESTS=1`，不跳过）全部包通过，总覆盖率 86.5%（门槛 70%）。
- `go test -race ./internal/platform/...` 在 Linux 容器内通过。
- `pnpm test:sandbox`：真实容器隔离与资源限额 13 项，以及 Agent 真实 pandas/matplotlib 运行与 Excel 工作簿运行，均通过且无跳过。
- `pnpm smoke:platform`：通过（受控替身模型，真实服务进程与 Docker 沙箱；Windows 上不检查优雅停止）。
- `pnpm check:platform-contract`：契约有效（49 个操作），62 个真实响应与契约逐项吻合，其中 60 个满足工作台声明的 TypeScript 类型（`/me` 与 `budget.put` 未声明类型）。
- Prettier 格式检查（`scripts`、契约、`package.json`）通过。

**未验证与限制。**

- 存储采样每轮聚合全部仍在保留期的文件，规模变大后应改为按项目增量计数；没有压测，也没有容量数字。
- 真实验收只覆盖 3 行的 CSV 与 Excel、三款模型各一次、本机单节点，不代表复杂任务质量、延迟分布、并发容量或成本；通过不是保证。
- 价格是预设价目，不是供应商账单；记录的供应商花费是供应商侧用量差值，不是平台计费。
- 本轮新增测试只在本机验证；仓库不配置远程 CI。真实代码 Agent 与 GPU 验收资产见 `assets/code-agent-acceptance.json`。
- 云端多租户、对象存储后端、KMS、支付与 SLA 不在本轮范围。
