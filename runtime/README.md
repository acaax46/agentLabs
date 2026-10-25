# AgentLab 运行环境与验证资产

本目录保留隔离执行、CLI 和连接器的开发验证资产。目标架构以 [平台总设计](../docs/agentlab-platform-design.md) 为准，实际产品能力见 [实施现状](../docs/implementation-status.md)。本轮没有构建镜像、启动服务或修改运行参数。

## 当前目录职责

| 路径                                 | 实际用途                                                        |
| ------------------------------------ | --------------------------------------------------------------- |
| guest/                               | Linux guest 与 OpenSandbox execd 的实际执行路径                 |
| agents/                              | 多种 Agent CLI 的开发工作镜像；安装不代表账户已授权或协议已适配 |
| cc-switch/                           | 本地配置管理与受限导出工具；不是云端凭据服务                    |
| vm/                                  | QEMU TCG 启动验证资产，不代表商用节点性能                       |
| mcp/                                 | 候选连接器和风险清单，不是已上线 Tools API 目录                 |
| judge/、problem-banks/               | 旧教育原型与独立结果校验参考                                    |
| versions.lock.json、assets.lock.json | 版本、摘要与资产来源记录                                        |

业务控制面、秘密、预期结果和费用记录留在不可信执行环境外。执行器不得通过缺少隔离时直接运行宿主命令来补全能力。

## 当前 guest 检查

```powershell
pnpm build:guest
pnpm test:e2e:live
```

以上是现有原型的真实 Linux guest 检查，不代表新平台的云端租户、API Key、Tools API、弹性或计费验收。QEMU TCG 用于本地功能验证，商用节点需重新测量性能与成本。

## 独立开发实验链路

以下命令保留给已有 OpenSandbox Docker 控制服务和 CLI 环境验证；普通资料管理与本轮文档更新不需要运行：

```powershell
python runtime/scripts/fetch_assets.py
docker build -t agentlab/agents:validated-20261023 runtime/agents
python runtime/scripts/prepare.py
docker compose --env-file .cache/runtime/compose.env -f runtime/compose.yaml up -d opensandbox
python runtime/scripts/verify_opensandbox.py
python runtime/scripts/verify_judge.py
```

该控制服务持有开发 Docker 控制能力，只允许本机管理面访问；.cache/runtime 中的密钥和完整解析配置不发给用户或 Agent。镜像、下载摘要与许可状态以锁文件和实际资产为准，不把历史机器缓存镜像作为通用发行承诺。

CC Switch 项目隔离启动器仍为 scripts/start-cc-switch.ps1；其配置空间与执行 VM 分离。配置导出只能包含固定网关与无权限占位信息，不导出真实秘密、订阅登录文件或宿主 hooks。

停止此独立开发链路时使用对应 compose 配置，默认保留数据：

```powershell
docker compose --env-file .cache/runtime/compose.env -f runtime/compose.yaml --profile management down
```

不要执行全局 Docker prune，不清理其他项目或客户数据。未来托管服务需另行实现节点池、租约回收、网络与会话身份、受管凭据、产物存储和运行资源计量。
