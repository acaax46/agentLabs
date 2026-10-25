# README 技术图

图表基于提交 `085fb08d7e6f5de5fe23b528ffb83fbebff16e9b` 的代码，生成于 2026-10-25。使用 Archify 3.1.0。仓库保留 PNG 截图与 SVG 导出；交互版 HTML、可编辑 JSON 图源及生成检查结果仅保留在生成机器的 `.archify/`，不随仓库提交。

| 图表                                | README 静态图                                                    |
| ----------------------------------- | ---------------------------------------------------------------- |
| 当前本机技术架构                    | [PNG 截图](agentlab-platform.png) · [SVG](agentlab-platform.svg) |
| 核心 MVP：文件分析 Agent 的一次 Run | [PNG 截图](agentlab-mvp.png) · [SVG](agentlab-mvp.svg)           |

README 展示 PNG 截图，来源为本机保存的交互版 HTML。截图使用深色静态模式、1440px 宽视口与 2 倍像素密度，包含标题、完整图形、图例和说明卡；平台架构图为 2816×2590，MVP 时序图为 2816×4670。点击 README 图片可查看原图。

## 如何阅读

架构图按调用与存储职责分组，不代表这些组件已部署为独立微服务。API 与 Worker 既可同进程运行，也可通过 `AGENTLAB_ROLE` 拆分；持久队列在 MongoDB 中，没有额外的 Redis 或消息中间件。SSE 从持久事件轮询读取，文件字节使用本地文件系统，归属和摘要保存在 MongoDB。

MVP 时序图展示内置文件分析引擎的一次 Run，模型与工具步骤按需循环。`files.transform`、`files.bundle` 使用可信 Worker 内的内置实现；`code.execute` 才进入隔离代码容器。对话的每轮消息复用 Run 链路，跨轮恢复的是消息和文件。可选 CLI 引擎、GPU 的实现与验收范围见[实施现状](../implementation-status.md)。时序间距不代表实测耗时；SSE 在整个运行期间持续推送，图末集中展示它的读取和交付关系。

费用预占先于执行；证据完整时结算并释放剩余预占。已派发结果未知时保留预占待核对，不自动重发或盲目退款。BYOK 模型费由客户承担；存储当前仅披露用量，金额为 `null`。图表不表示云端多租户、商业支付、对象存储、KMS 或 SLA 已实现。

## 重新生成

在项目根目录使用 `.node-version` 指定的 Node。以下命令需要生成机器上保留的 `.archify/` 图源；新克隆仓库应先使用 Archify 根据当前源码重新生成图源。将下例中的技能路径替换为本机 Archify 安装目录；生成过程只读取源码并写入图表，不需要数据库、Docker 或模型调用。

```powershell
$archify = '<Archify 安装目录>/bin/archify.mjs'

node $archify finalize architecture `
  .archify/architecture-agentlab-platform-20261025-185018/candidate.json `
  .archify/architecture-agentlab-platform-20261025-185018/agentlab-platform.html `
  --repo-root . --quality showcase --json

node $archify finalize sequence `
  .archify/sequence-agentlab-mvp-20261025-185018/candidate.json `
  .archify/sequence-agentlab-mvp-20261025-185018/agentlab-mvp.html `
  --repo-root . --quality showcase --json
```

`finalize` 校验图源、源码引用、生成文件及真实浏览器显示，旁置的 `*.finalize-summary.json` 保存检查结果。若系统 Chrome 无法建立检查管道，可用当前进程的 `ARCHIFY_CHROME` 指向已安装的无界面 Chromium；不修改全局浏览器配置。

内容变更时先重新核对源码，更新图源中的仓库提交与引用行号。已产生浏览器证据的 HTML 改稿时，给命令增加新的 `--out-dir <本图目录>/review-2`，保留此前证据；后续请求使用新时间戳目录。完成后打开交互版，选择“导出 → SVG（深色）”，分别保存为本目录的 `agentlab-platform.svg`、`agentlab-mvp.svg`，并核对 README 引用。SVG 是交互版的静态导出，不能替代 HTML 的检查凭据。

更新 README 截图时，在浏览器打开同一交互版 HTML，设为深色、静态，使用 1440px 宽视口和 2 倍像素密度，截取标题至说明卡底部的完整区域。截图时收起导出、缩放等浮层控件，分别保存为 `agentlab-platform.png`、`agentlab-mvp.png`；若布局变化，同步修订上方图片尺寸。PNG 是浏览器截图，SVG 是矢量导出，两者均来自同一份图源。
