# AgentLab 技术架构

以下两张图由 Archify 按当前源码生成，展示本机已实现的技术链路。云端多租户、对象存储、KMS 与商业支付的规划见[平台总设计](agentlab-platform-design.md)，当前实现边界见[实施现状](implementation-status.md)。

## 平台整体架构

React + TypeScript 工作台与外部应用通过两套入口访问同一 Go 平台业务。MongoDB 副本集保存业务状态、Run 队列、事件、用量与账本；Worker 领取租约后编排执行，模型请求在容器执行域外转发，代码在无网 Docker 沙箱中运行，文件字节写入本地 Blob 存储。

[![AgentLab 技术架构：工作台与外部 API 接入 Go 平台，MongoDB 持久化队列和账本，Worker 连接模型网关、隔离沙箱与本地文件存储](diagrams/agentlab-platform.png)](diagrams/agentlab-platform.png)

[SVG 矢量版](diagrams/agentlab-platform.svg)

## 核心 MVP 技术链路

以文件分析 Agent 的内置引擎为例：上传文件后，提交指定版本、模型、费用上限与幂等键；预算预占、Run 入队、首条事件和幂等凭据在同一事务内提交，成功后返回 `202`。Worker 经模型与工具循环生成产物，追加用量并按证据结算；工作台或 API 客户端通过 SSE 查看进度、预览和下载结果。图中纵向位置表示逻辑顺序，不表示实测耗时。

[![核心 MVP 时序：文件上传、事务预占入队、Worker 领取租约、模型调用、无网代码执行、产物保存、结算和 SSE 交付](diagrams/agentlab-mvp.png)](diagrams/agentlab-mvp.png)

[SVG 矢量版](diagrams/agentlab-mvp.svg)

对话每轮创建同类 Run，跨轮保存消息与文件，容器按轮回收。`files.transform` 与 `files.bundle` 在可信 Worker 内执行；独立 Tools API 复用工具和账本，跳过模型循环。已派发的未知结果保留预占待核对，不盲目重发或退款；BYOK 模型费由客户承担。

本文直接展示从 Archify 交互版截取的高清 PNG，点击图片可查看原图。交互版与图源保留在本机 `.archify/`，不随仓库提交。[图表来源与重新生成说明](diagrams/README.md)。
