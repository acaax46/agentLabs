# AgentLab 项目规则

本文件是 Codex 与 OpenCode 共用的项目入口，适用于本目录及产品代码。请从项目根目录启动开发工具；子目录规则只补充本文件，不另建相冲突的产品和视觉规范。

## 产品与文档

- AgentLab 的目标是面向开发者与团队的托管 Agent 执行和 Tools API 平台。工作台用于配置、调试、发布、运行观测与费用管理；用户可选择平台模型、自己的供应商账户或自建模型服务。
- 唯一平台总设计：[docs/agentlab-platform-design.md](docs/agentlab-platform-design.md)。代码事实与迁移边界：[docs/implementation-status.md](docs/implementation-status.md)。实际开发命令：[README.md](README.md)。演示口径、预设目录与代码位置：[docs/hackathon-guide.md](docs/hackathon-guide.md)、[docs/preset-agents.md](docs/preset-agents.md)、[docs/code-map.md](docs/code-map.md)。涉及任务前读取相关部分。
- 新主业务为租户/空间、项目、Agent 与 Tool 的版本、Run、连接、API Key、产物、用量和账本。旧教育、课堂、班级、题库与判题不再作为默认产品定位。
- 黑色工作台主业务已接入真实 /api/v1/platform；/v1 为同一业务的机器接口。P0 主链路已用真实模型、MongoDB 与 Docker 容器验证；这不代表云端多租户、商业支付、对象存储或 SLA 已实现。没有计量证据的存储项保持未知，不得伪造已结算金额。
- 不因重写文档删除现有业务代码、数据库、用户材料、账户、图片或测试证据；不机械地把班级改名为项目、学生改名为 API 调用身份。

## 架构与业务约束

- 产品代码在 frontend/、backend/，已实现接口契约在 contracts/。Hydro/、OpenSandbox/ 是上游参考，不把其品牌与工程规则复制为平台规范。
- 数据库确定使用 MongoDB，禁止新增 SQLite 持久化；本地/CI 使用 compose.test.yaml。采用事务、唯一索引、持久队列、租约、attempt、fencing 与 outbox，不能用进程内状态冒充持久业务状态。
- 当前账户为用户/管理员。已实现 API Key 是绑定租户、项目、scope、预算和期限的机器身份，不是角色 Header；网页、原生绑定、API、产物下载与后续回调均需校验资源归属。
- 模型与工具凭据保留在执行域外，模板保存连接引用。不得向 Agent 工作文件、VM、终端、提示词、日志或前端查询接口输出真实秘密。当前本地 DPAPI/文件根密钥不等于云端 KMS 已实现。
- BYOK 和自建模型费用由客户自行承担；平台供模、执行、工具和存储按明示规则计量。统一余额、分项账单，不重复计收同一资源，不把内部文件操作包装成隐形收费项。
- 运行、用量与审计记录只追加。金额以整数微单位存储、JSON 字符串传输并显示币种；未知指标保留 null，估算不能标为供应商结算或已充值余额。
- 写入、派发、预占与事件按业务事务组织。幂等重试返回原任务；已派发的未知结果不盲目重发或退款。取消、撤权与回滚不能撤回已经发生的外部副作用。
- 不直接在宿主执行不可信用户代码补齐沙箱功能，不因隔离或网关失效自动降级为开放执行。具体执行平台、启动延迟、并发、回收和成本须通过真实验收。
- 状态与能力由后端事实驱动。当前平台接口见 contracts/platform.openapi.yaml；旧 /api/v1 接口及生成客户端仍由 contracts/openapi.yaml 描述，不混用两份契约。

## 视觉约束：黑色极客工作台

涉及页面、组件、文案、图标或品牌前，必须阅读根目录 [DESIGN.md](DESIGN.md)。它是当前视觉标准；旧奶油/Soft Clay 规范不再生效。

- 新方向为近黑底、石墨面板、电青主色、少量紫色强调、细边框、紧凑工作区和等宽技术信息。炫酷来自清晰的运行结构与克制光效，不牺牲可读性。
- 目标值记录在 assets/design-tokens.target.json。frontend/src/design-tokens.css 是运行时变量载体，frontend/src/workbench.css 消费同一 --al-\* 语义变量；调整变量时同步修订 DESIGN.md、目标 JSON 与运行时文件，不建立第二套颜色系统。
- 后续导航和操作使用统一的单色 SVG 线描图标。旧 Soft Clay、其他探索图标与历史画廊只作档案；不能为匹配深色主题对旧 PNG 使用 CSS 滤镜改色。
- 保留现有 AgentLab 字标及 Learning Bloom 图形作为过渡资产，不自动改选其他探索 Logo，也不宣称新图形已制作。具体素材状态由 assets/brand.json 记录。
- 普通正文至少 14px、辅助信息至少 12px；常规控件高 40px，移动主要目标至少 44px。采用 4px 间距体系，控件圆角 6px、卡片 10px、面板 12px。禁止奶油背景、软陶大图、厚底按钮和大面积炫光。
- 左侧导航不显示可见滚动条，但保留溢出滚动、键盘访问和移动抽屉。检查 1440px 与 390px；日志和表格在自身容器滚动，不撑宽整页。
- 实现键盘焦点、加载、空、错误、禁用、审批、取消和未知状态。颜色配文字/图形，品牌色和绿色状态都不是安全认证；费用与不可逆操作提供明确说明。

## 工程与验证

- 延用 React + TypeScript + 语义 CSS，Tailwind 4 仅作统一主题工具层；优先复用共享组件，不为换主题引入大型 UI 套件或远程字体。
- Node 使用 .node-version 指定的 22.20.x，pnpm 9.15.9；Go 版本以 go.mod 与本机受测工具链为准。不要更改全局工具、模型、凭据或权限配置。
- 仅文档/元数据变更检查引用、JSON 语法、资产路径与规范一致性，不启动数据库、不重启应用、不运行无关产品测试。
- 前端实现变更运行 lint、TypeScript 和相关格式检查；交互/布局变更运行对应 Playwright，检查桌面与 390px；构建变更执行构建。后端按实际业务影响验证 MongoDB 事务、并发与真实执行。
- frontend/src/api/generated.ts 从 OpenAPI 生成，桌面嵌入资源从前端构建复制，均不手工改写。文档目标不触发自动修改 OpenAPI、生成客户端、数据库或生产配置。
- API Key、模型协议、商业支付、OAuth、工具目录、云端多租户和安全 SLA 均按实际实现与证据描述，不能将历史原型测试或一次调用推广为完整产品验收。
