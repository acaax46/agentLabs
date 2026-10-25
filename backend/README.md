# AgentLab 后端

Go HTTP 与 Wails 入口共享 internal/app。新产品边界见 [平台总设计](../docs/agentlab-platform-design.md)，代码事实见 [实施现状](../docs/implementation-status.md)。

当前仍是带本地账户、资料管理、模型连接和执行原型的单空间服务。新租户/项目、API Key、AgentVersion、ToolVersion、对外 Run API 和商业结算尚未实现；本轮不修改接口、数据库或 worker。

主要可复用模块：

| 模块                       | 当前职责                                   |
| -------------------------- | ------------------------------------------ |
| internal/auth、access      | 本地用户/管理员、会话与授权                |
| internal/connection        | 模型账户元信息、宿主加密秘密、验证与撤销   |
| internal/catalog、training | 旧资料与学习原型；后续按迁移计划处理       |
| internal/learning          | 冻结版本、工作区、持久队列、预算与账本原型 |
| internal/execution         | 模型客户端、运行中心与 Linux guest 适配    |
| internal/records           | 只追加的评测、用量和审计                   |
| internal/storage           | MongoDB 副本集连接                         |

开发从根目录执行 pnpm db:up 和 pnpm dev:api；演示账号用 pnpm seed:demo 显式初始化。当前 HTTP 仅监听 loopback，桌面使用相同身份边界。环境变量见 .env.example，不能向客户执行域注入真实账户秘密。

现有检查为 go test ./...、go vet ./...，真实 MongoDB 检查通过根目录 pnpm test:integration。Wails 入口使用 desktop build tag，桌面构建从根目录 pnpm build:desktop 执行。真实 guest 与模型协议验证单独进行，不能用 fixture 结果冒充上线能力。

后续迁移保持 MongoDB、事务、幂等、租约、fencing、outbox、未知结果对账与不可变价格证据。旧字段不能仅改名后成为新项目权限或收费对象。
