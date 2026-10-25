# AgentLab 工作台前端

目标是托管 Agent 执行与 Tools API 平台的控制台。产品见 [总设计](../docs/agentlab-platform-design.md)，目标视觉见 [DESIGN.md](../DESIGN.md)，目录规则见 [AGENTS.md](AGENTS.md)。

当前已落地黑色工作台 MVP 外壳：`src/workbench.css` 与 `src/design-tokens.css` 使用 DESIGN.md 的 --al-\* 语义变量，导航覆盖概览、项目、Agent、工具、运行、连接、API 密钥、用量与账单、产物和设置。旧课程、班级、题库与判题页面保留源码但没有进入新路由和导航。

工作台主业务使用后端新增的 `/api/v1/platform` 接口，以同源 Cookie 鉴权，写请求携带 `X-AgentLab-Request: platform`。`src/platform/api.ts` 的类型按 `backend/internal/platform` 的公开视图定义；现有 OpenAPI 生成客户端继续服务旧 `/api/v1` 接口，不手工改写。

已对接项目列表与创建、预算配置、暂停与恢复、项目连接授权、已发布 Agent 与版本读取、模型选择、SHA-256 文件上传、运行提交与停止、增量事件、工具调用记录、产物预览与短时授权下载、API Key 签发与撤销、用量、账本和服务能力。顶栏切换当前项目，业务数据由 React Query 读取；不再读取或写入 localStorage 演示状态。

P1 增加独立工具调用调试与历史、发布快照对比、多语言调用示例、运行筛选与视图、日志筛选下载、真实费用趋势、按币种汇总、当前页 CSV、Excel 预览与图表、批量 ZIP 和运行证据包。`Ctrl/⌘ K` 打开命令面板，输入框外按 `?` 查看快捷键。只在浏览器保存用户的视图和列偏好，不保存业务对象。逐项完成情况与后端缺口见 [P1 状态](docs/p1-status.md)；草稿发布、审批、Webhook、成员管理、自建端点能力验证和保留期修改仍未开放。

试运行会提交真实任务并预占预算，必须确认费用影响。运行使用固定发布版本、模型连接、输入文件引用和费用/时间上限。创建项目、运行和密钥保留同内容手动重试的幂等键，不自动重试写操作；完整 API Key 仅在创建弹窗的临时状态中显示，不进入持久化存储或查询缓存。金额以微单位字符串传输，使用 BigInt 转换和格式化；未知指标保留未知，BYOK 模型费用标为客户承担。

现有栈为 React + TypeScript + Vite，Tailwind 4 只映射统一主题。从项目根目录启动数据库、API 与 `pnpm dev`；本目录可执行 `pnpm dev`、`pnpm lint`、`pnpm check`、`pnpm build`。Node/pnpm 与集成测试命令见 [根 README](../README.md)。

Vite 代理 `/api`、`/v1` 和 `/health` 到 `AGENTLAB_API_PORT`（默认 8787）。机器调用示例使用 `/v1` 和项目 API Key，工作台本身使用会话接口。登录、账户连接、密码修改保留原有传输方式。Wails 当前没有平台原生绑定，相关页面明确提示改用浏览器，不回退到本地业务数据。

界面回归：`pnpm test:e2e:platform`。覆盖 1440px、1024px、390px 与 200% 等效视口，按接口结构验证分页、断线续传、键盘、上传校验、密钥保存确认、权限能力、Markdown 隔离、精确金额与费用来源。此套使用受控接口，不计入真实主链路验收。结果写入 `.cache/p0-ui-results`。

`pnpm test:e2e` 是当前产品的真实认证/连接回归（独立 MongoDB 测试库、受控 `/models` 端点，无真实模型消费），同时验证原生 Chromium 200% 标签页缩放。原生缩放需要支持临时扩展的完整 Chromium；可用 `AGENTLAB_ZOOM_BROWSER_PATH` 指定已安装的 Chromium，普通 Chrome 的发行版可能禁用此扩展启动方式。旧认证/连接用例保留为 `*.legacy.spec.ts`，旧业务用例不作为当前产品门槛。

P1 定向回归：`pnpm test:e2e:p1`，结果写入 `.cache/p1-ui-results`。如测试端口占用，可指定 `AGENTLAB_PLATFORM_TEST_WEB_PORT`。格式检查使用根目录提供的 Prettier。

`pnpm test:e2e:p1:regression` 单独存放原有工作台回归。启动下述独立验收后端并构建前端后，`pnpm test:e2e:p1:acceptance` 验证 1440px/390px 的真实独立文件工具调用、结算、历史与授权 ZIP 下载，不触发模型推理调用；结果在 `.cache/p1-acceptance-results`。

真实验收前确保 `compose.test.yaml` 的 MongoDB 副本集运行、`agentlab/sandbox-python:20261024` 镜像已构建，且仓库根目录 `.env.dev` 中配置了可用的 `BASE_URL`、`API_KEY`。在本目录先执行 `pnpm dev:acceptance`，它使用新建验收数据库、临时宿主根密钥、独立产物目录和端口 18879，不修改现有账户或生产配置。在另一终端执行 `pnpm build`，再执行 `pnpm test:e2e:acceptance`。验收使用生产构建、真实模型与 Docker 容器，会消耗真实模型额度；不启用模拟模型回退。浏览器不在 Playwright 默认目录时设置 `AGENTLAB_BROWSER_PATH`，其他本地配置文件可通过 `AGENTLAB_P0_ENV_FILE` 指定。

真实验收覆盖创建项目、上传、平台供模、CSV/PNG 产物、隔离图片预览、授权下载、账本、BYOK 连接、项目授权、一次性密钥、无会话的外部 `/v1` 调用及 API 来源、跨项目拒绝、撤销与取消。结果位于 `.cache/p0-acceptance-results`；禁止在报告中记录真实密钥，验收不启用含密钥的网络 trace。Docker 为容器隔离；多租户、云端弹性、商业支付与 SLA 仍未验收。存储没有计量证据时显示未知，不把缺失数据当作零费用。
