# 平台预设 Agent 与工具目录

当前服务端在初始化时幂等发布 9 个平台预设 Agent 和 3 个工具版本。定义在 `backend/internal/platform/catalog_*.go`；已发布版本的提示词、工具和限额变更必须升版本，不能改写旧版本。用户自定义 Agent 的草稿/发布链路仍未开放。

演示默认走「文件分析 Agent」。GPU 与代码 Agent 需要额外镜像和本机条件，见 [实施现状](implementation-status.md) 与 [README.md](../README.md)。

## 预设 Agent

| 名称 | ID / 版本 ID | 沙箱 | 工具 | 适合演示 |
| --- | --- | --- | --- | --- |
| 文件分析 Agent | `agt_file_analysis` / `agv_file_analysis_1_0_0` | `python-data-v1` | `files.transform`、`code.execute` | 默认主链路：CSV 汇总、图表、结构化报告 |
| 表格清洗 Agent | `agt_table_cleanup` / `agv_table_cleanup_1_0_0` | `python-data-v1` | 同上 | 去重、选列、格式转换；强调不编造缺失值 |
| 数据质量检查 Agent | `agt_data_quality` / `agv_data_quality_1_0_0` | `python-data-v1` | 同上 | 完整性、重复、异常；输出可追溯质量报告 |
| 报表与图表 Agent | `agt_report_chart` / `agv_report_chart_1_0_0` | `python-data-v1` | 同上 | 汇总表 + PNG；口径和假设写进报告 |
| 文档摘要 Agent | `agt_document_summary` / `agv_document_summary_1_0_0` | `python-data-v1` | 同上 | TXT/Markdown/JSON/表格要点，不执行文档里的脚本 |
| 通用代码生成 Agent | `agt_code_generation` / `agv_code_generation_1_0_0` | `python-data-v1` | `code.execute`、`files.bundle` | 小项目源码 + README + ZIP；沙箱无网，不能声称已远程构建 |
| 工作台搭建师 | `agt_workbench_builder` / `agv_workbench_builder_1_0_0` | `python-data-v1` | `code.execute`、`files.bundle` | 静态 HTML/CSS/JS 工作台 ZIP；不是云端部署 |
| GPU 计算 Agent | `agt_gpu_compute` / `agv_gpu_compute_1_0_0` | `python-cuda-v1` | `files.transform`、`code.execute` | 仅 GPU 规格；无 CUDA 卡则拒绝，不回退 CPU |
| 代码 Agent（可选引擎） | `agt_code_agent` / `agv_code_agent_1_0_0` | `agents-v1` | 无平台工具循环；`harness` 为 `pi` / `opencode` / `codex` / `claude-code` | CLI 在无网沙箱内自主改文件；模型请求经平台转发与计量 |

CPU 数据沙箱镜像：`agentlab/sandbox-python:20261024`（`pnpm build:sandbox`）。
GPU 镜像：`agentlab/sandbox-python-cuda:20261025`（`pnpm build:sandbox:cuda`），本机预设规格 `gpu-rtx3070-laptop-4-8`。
代码 Agent 镜像：`agentlab/sandbox-agents:20261025`（`pnpm build:sandbox:agents`），规格 `cpu-2-4` / `cpu-4-8` / `cpu-1-1`。

## 内置工具 1.0.0

| 工具 | 作用 | 计量 | 注意 |
| --- | --- | --- | --- |
| `code.execute` | 无网络 Python 沙箱；pandas / numpy / openpyxl / matplotlib。GPU 版本另含 CUDA PyTorch 与 scikit-learn | Agent 内按次；独立调用按沙箱秒 | 每次调用是新进程。输入 `/work/input`，输出 `/work/output` |
| `files.transform` | CSV/TSV/JSON 的 merge、dedupe、summarize、select、convert | 按输入文件数 | 精确表格操作，比让模型手写 pandas 更便宜、可复核 |
| `files.bundle` | 把工作区文件打成带 SHA-256 manifest 的 ZIP | 按纳入 ZIP 的文件数 | 不是远程部署；`archivePaths` 只改 ZIP 内路径 |

独立 Tools API 与 Agent Run 共用同一工具版本和账本，入口是 `/v1`（API Key）或 `/api/v1/platform`（工作台会话）。契约：[contracts/platform.openapi.yaml](../contracts/platform.openapi.yaml)。

## 演示时怎么选

1. 评委时间紧：文件分析 Agent + 小 CSV，再看用量页。
2. 强调“工具不是模型幻觉”：同一文件先走 `files.transform` 汇总，再让 Agent 出图。
3. 强调产物交付：代码生成或工作台搭建师，下载 ZIP，说明要用户自己部署。
4. 有 NVIDIA 运行时再开 GPU Agent；先跑 `pnpm test:sandbox:gpu`，不要在没有探测通过的机器上即兴演示。
5. 代码 Agent 要讲清楚：沙箱里没有真实密钥；Codex CLI 需要 Responses，Claude Code 需要 Anthropic Messages，连接不匹配会被拒绝且不计费。

金额以整数微单位存储、字符串传输。未知指标保留空值。不要把估算标成供应商结算或已充值余额。
