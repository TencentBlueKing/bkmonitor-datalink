---
name: alarmd-cli
description: 排查 alarmd 告警检测时使用：策略为什么没告警或告警不对、检测对象与 Slot、副本与舰队状态、离线快照，以及经 SRE 通道对部署做 Kubernetes 只读取证。通过 alarmd-cli 读 alarmd 自己的状态。
---

# alarmd-cli

alarmd-cli 是 alarmd 自带的命令行，读 alarmd 自己的状态：舰队与副本、策略与检测对象、诊断、快照，以及 SRE 通道的 Kubernetes 取证。stdout 是 JSON。

## 找环境

    alarmd-cli profile list

结果里的 `environment_id` 就是后面每条远程命令都要显式带的 `--env`。profile 不含凭据。

## 授权只能由人完成

没有可用的 profile，或命令返回 `credentials_expired` 时，停下来请用户在 alarmd 授权页取得授权码后运行 `alarmd-cli auth login`，或运行授权页给出的 `alarmd-cli auth listen` 命令。不要索要，也不要接受聊天里贴来的管理员密钥、授权码或令牌。

## 工作流

    alarmd-cli discover --env <environment_id>                    # 操作目录与摘要
    alarmd-cli describe <operation> --env <environment_id>        # 参数、限额、effect 与示例
    alarmd-cli invoke <operation> --env <environment_id> --input @input.json
    alarmd-cli diagnose --env <environment_id>                    # 全部策略的诊断
    alarmd-cli snapshot --env <environment_id>                    # 一次离线完整采集

- 只用 discover 列出的操作名，参数按 describe 填；ID 类参数只从上一步结果里取。
- `next_call` 是建议的下一步，不会自动执行；需要时照它的 `params` 调用。
- 快照写成文件后，用 `--snapshot <文件>` 配合 discover、describe、invoke、diagnose 离线读。
- 输出被省略时（`result_omitted`），读 `meta.result_file` 指向的文件。
- 退出码：0 完整；3 部分结果，读 `evidence.limitations` 与 `next_call`；1 调用、配置或协议错误；2 命令或输入不合法。

## 版本

CLI 在 stderr 提示与部署版本不一致时，按提示里给出的安装命令重装，不要自己拼参数。

## 边界

- describe 给出的 `effect` 为 `read` 的操作随时可用。
- 其余操作（执行、删除、重启、扩缩、改镜像、apply 等会改动部署的）只在用户明确要求时执行，并先说明要做什么。
- Secret 只给指纹，不要设法输出明文。不要把凭据写进命令参数、对话、日志或证据。
