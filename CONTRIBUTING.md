# 如何给 AgentLab 提变更

黑客松期间也按仓库现有规则改，不另起一套流程。完整约束在 [AGENTS.md](AGENTS.md)；演示边界在 [docs/hackathon-guide.md](docs/hackathon-guide.md)。

## 开工前

1. 从仓库根目录启动工具，不要把子目录当成另一个产品。
2. 先读 [本地开发指南](docs/development.md) 的启动方式，以及 [docs/code-map.md](docs/code-map.md) 的代码位置。
3. 涉及页面或文案时读 [DESIGN.md](DESIGN.md)。不要复活奶油/Soft Clay 风格，也不要用 CSS 滤镜给旧 PNG 改色。
4. 新业务走 MongoDB，不要新增 SQLite 持久化。

## 提交时注意

- 只改与本次目标有关的文件。不要为了“看起来迁移完成”删除旧学习代码、用户材料或验收 JSON。
- 不要手改 `frontend/src/api/generated.ts`。平台契约改 `contracts/platform.openapi.yaml`，旧学习契约改 `contracts/openapi.yaml`，两套不要混。
- 密钥、`.env.dev`、完整 API Key 不进仓库、不进日志、不进截图。
- 金额用整数微单位存储；未知指标保持空值。没有计量证据就不要写已结算金额。
- 文档目标不自动改 OpenAPI、生成客户端、数据库或生产配置。

## 验证

| 变更类型 | 最少检查 |
| --- | --- |
| 仅文档/元数据 | 链接、JSON 语法、资产路径、与总设计/实施现状是否矛盾 |
| 前端逻辑 | lint、TypeScript；交互/布局再跑对应 Playwright |
| 平台后端 | 相关 Go 测试；动到事务/并发时用 MongoDB，不要用进程内状态顶替 |
| 真实执行 | Docker 沙箱与真实模型；缺沙箱就应拒绝执行 |

本地验证命令见[本地开发指南](docs/development.md#检查与构建)，不要把一次本机通过写成云端验收。
