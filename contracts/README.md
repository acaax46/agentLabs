# AgentLab 接口契约

本目录的 openapi.yaml 描述现有本地 /api/v1 接口，包含已实现的认证、资料、连接与运行原型。pnpm contracts 生成 frontend/src/api/generated.ts；本轮两者均未改动。

[platform.openapi.yaml](platform.openapi.yaml) 描述已实现的托管 Agent API 与 Tools API（OpenAPI 3.1）。同一组操作有两个入口：`/v1`（API Key，可单独监听用于公网）和 `/api/v1/platform`（工作台会话）。它与上面的 openapi.yaml 相互独立，前端类型应从它生成，不要手工改写 frontend/src/api/generated.ts。

`pnpm check:platform-contract`（需要 MongoDB）保证契约、后端和工作台一致：校验文档本身并生成类型化客户端到 .cache；运行后端的 `TestRecordSamples`，把每个响应按 `unevaluatedProperties: false` 逐项对照 schema（多出或缺少的字段都会失败）；再用 TypeScript 编译器核对这些响应符合 frontend/src/platform/api.ts 声明的类型。后端响应结构、契约或工作台类型任一变化，都应重新运行它；新增的接口还需要在 `TestRecordSamples` 与脚本的 `expectations` 里登记。

[平台总设计](../docs/agentlab-platform-design.md) 仍是产品与阶段目标；回调与签名投递、工具审批、对象存储等未实现的部分以 [实施现状](../docs/implementation-status.md) 为准。

新对外接口必须有租户/项目身份、API Key scope、幂等、预算和资源归属校验。长任务返回可查询的 Run/ToolCall，支持恢复事件、签名回调与产物授权。工作台管理面、执行数据面和凭据管理权限分别设计。

不得为纯文档迁移修改当前响应、把本地会话接口开放公网，或手工改写生成类型。
