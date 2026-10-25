# AgentLab 前端规则入口

从此目录启动工具时，先阅读 [根目录规则](../AGENTS.md)；任何页面、文案、图标和样式任务必须阅读 [DESIGN.md](../DESIGN.md)。产品对象与行为见 [平台总设计](../docs/agentlab-platform-design.md)。

- 目标为托管 Agent 与 Tools API 工作台：项目、Agent、工具、运行、连接、API 密钥、产物、用量与账单。旧学习/班级页面是迁移前实现，不指导新信息架构。
- 当前批准的视觉是黑色极客风：近黑、石墨、电青、少量紫色，细边框、小圆角、单色 SVG 与等宽技术信息。旧奶油/Soft Clay 规范不再生效。
- 黑色工作台使用 src/workbench.css 与 --al-\* 变量。项目、Agent、Run、API Key、用量、账本与产物来自真实 /api/v1/platform；不使用 localStorage 或 sessionStorage 保存业务对象。真实模型、MongoDB 与 Docker 容器验收记录见 docs/implementation-status.md，不能推广为云端 SLA。
- 后续用 --al-\* 统一语义变量，Tailwind 只映射同一主题。保留 React/TypeScript 与语义组件，不引入大型 UI 套件或远程字体。
- 现有 Learning Bloom 图形作为过渡资产保留，不擅自改选其他探索 Logo；新界面不继续采用软陶位图。不得用 CSS filter 伪造重绘。
- 实现加载/空/错/未知/取消/审批状态，保留键盘焦点和停止入口。左侧无可见滚动条但可滚动；检查 1440px 和 390px。
- 费用与状态区分实际、估算与未知；试运行标明费用影响，BYOK 与平台应收分开。旧 /api/v1、已实现 /api/v1/platform 与 /v1 的契约分开；未开放能力不得以占位数据冒充。
- src/api/generated.ts 由契约生成；桌面嵌入资源由构建复制，不手工改。浏览器/桌面使用受控传输边界，任何产物预览都不获得控制台或原生权限。
- 文档任务只核对引用、配置与素材；源码任务按根目录规则运行相关 lint、类型、格式、浏览器和构建检查。
