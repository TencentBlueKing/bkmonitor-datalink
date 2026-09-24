# 使用 AI 调试和维护 Linkd

`linkd-cli` 面向 Linkd 开发和维护人员，通过 Console 的 Basic Auth 入口提供结构化诊断及受控运维。
`linkd-ops` skill 指导 AI 沿实际处理链路收集证据、解释异常，并在操作前完成最终人工确认。

客户端使用现有 Console `/local-api` 接口，不直接连接 Kafka、Redis、MySQL 或 Elasticsearch。
API 目录属于本次构建的接口快照；目标 Console 未部署相应能力时会返回明确错误，不自动切换其他入口。

## 构建和安装

在 Linkd Go 模块目录执行（Go 版本以 `go.mod` 为准）：

```bash
make cli
./bin/linkd-cli version
make cli-install
```

默认安装到 `~/.local/bin/linkd-cli`，将该目录加入 PATH。可通过 `CLI_INSTALL_DIR` 指定安装目录；
`VERSION` 和 `GIT_COMMIT` 沿用仓库构建变量。本步骤不发布制品或部署服务。

## 配置环境

通过标准输入传入密码，避免将密码作为命令参数写入历史或进程列表。例如在 Bash 终端本机输入：

```bash
read -r -s -p 'Console password: ' LINKD_INPUT_PASSWORD
printf '\n'
printf '%s' "$LINKD_INPUT_PASSWORD" | linkd-cli config set test \
  --url https://console.example.com/linkd \
  --username maintainer --password-stdin
unset LINKD_INPUT_PASSWORD
linkd-cli config list
linkd-cli config use test
linkd-cli config show test
```

示例地址和环境名需要替换成实际值，不要在 AI 对话中提供密码。配置文件包含明文 Basic Auth 凭据，
仅通过文件权限保护，不宣称加密存储；`config list/show/set` 的输出均脱敏。

- 默认位置：`~/.config/linkd-cli/config.yaml`；设置绝对路径 `XDG_CONFIG_HOME` 后使用其下的 `linkd-cli/config.yaml`。
- `--config <path>` 优先指定客户端配置文件；它与 Linkd 服务 YAML 是两种独立配置。
- `--profile <name>` 优先于当前 profile。首个 profile 自动选中；删除当前 profile 后不会静默切换到其他环境。
- 目录权限为 `0700`、文件为 `0600`，保存采用同目录临时文件原子替换。读取拒绝宽松权限和配置文件符号链接。
- `config set` 完整替换指定 profile，不修改其他 profiles，也不联网验证账号密码。
- 默认请求超时 30 秒，通过 `config set --timeout-seconds` 配置为 1–120 秒；服务端自身可以提前超时。
- 支持 HTTP/HTTPS 和 Console 部署子路径。账号密码必须成对配置，HTTPS 校验证书；所有重定向均拒绝，请配置最终基础地址。

## 接口发现和只读诊断

```bash
linkd-cli api list
linkd-cli api describe alerts.list
linkd-cli api call server.version --profile test
linkd-cli api call server.capabilities --profile test
linkd-cli api call alerts.list --profile test \
  --query bk_tenant_id=tenant-a --query event_source_id=source-a --query limit=20
linkd-cli api call alerts.get --profile test \
  --path id=alert-a --query bk_tenant_id=tenant-a
linkd-cli api call redis.pending --profile test --query event_source_id=source-a
```

`api list/describe` 离线工作，无需配置凭据。目录记录方法、参数、读写属性、影响、示例及分页说明。
`--path key=value` 和 `--query key=value` 可重复，但相同参数不允许重复赋值；值由客户端编码，不接受预转义的路径身份。
路径身份必须是单段，不能包含 `/`、`\`、`%` 或点路径。请求体通过 `--body-file request.json` 或 `--body-file -` 提供 JSON。

支持版本/能力、脱敏配置、运行角色、来源/调度/动态配置、Events/Alerts/AlertLogs、Kafka/Redis/ES、指标、
策略索引/审计、OneModel、丰富预览和管理操作。具体能力以 `api describe` 与目标服务版本为准。
领域对象（如 EventSource `spec` 和 OneModel `where`）仍由服务端做完整业务校验；客户端不复制领域模型。

成功向 stdout 输出一份 JSON：

```json
{"profile":"test","operation":"alerts.list","http_status":200,"data":{"items":[],"nextCursor":"opaque-cursor","warnings":[]}}
```

返回体保持在 `data` 下，大整数、分页和 `partial/unavailable` 等状态得到保留；敏感键及连接凭据会被脱敏。
`data` 可能是对象、数组或 null。HTTP 请求成功不表示业务健康，应继续检查内部状态和采样时间。
错误写 stderr，退出码为 1；成功（含帮助和 dry-run）为 0。错误包含稳定分类和可用的 HTTP 状态，不回显原始后端错误响应。
帮助信息为文本，普通命令为 JSON。响应上限 16 MiB；请求体上限见接口目录，告警关闭为 4096 字节。

客户端不自动重试或翻页。实体查询将 `data.nextCursor` 传回 `cursor`；OneModel 使用 `next_cursor`，
保持租户、模型、条件和 limit 不变，放弃查询用 `onemodel.close` 释放快照。
来源列表使用 `after`。查询范围与分页预算仍受服务端限制。省略列表租户可能进行跨租户查询；
Redis 排障应显式提供来源，避免服务端默认选择首个来源。

## 操作保护和最终确认

以下接口默认拒绝执行：`event-sources.apply/delete`、`alerts.close`、`strategy-audits.start/cancel`。
每次调用都必须显式传入 `--allow-write`，配置和环境变量不能永久开启写入。只读 POST（丰富预览、
OneModel 查询及释放快照）无需此开关。客户端没有任意 URL、HTTP 方法、明文凭据查询或其他绕过入口。

先准备完整请求，执行零网络请求的预览：

```bash
linkd-cli api describe alerts.close
linkd-cli api call alerts.close --profile test --path id=alert-a \
  --body-file close-request.json --dry-run
```

AI 必须展示目标环境和地址、租户及具体对象范围、最终参数、变更差异、实际影响和验证方式，
**即使用户此前已经授权，也必须等待用户对本次最终方案的明确确认后，才能执行：**

```bash
linkd-cli api call alerts.close --profile test --path id=alert-a \
  --body-file close-request.json --allow-write
```

确认后参数、文件内容、环境、范围或影响变化，需要重新确认。批量操作只能覆盖明确列出的集合。
再次提交前先只读核对结果，并再次确认；关闭告警时保持全部操作字段不变，来源变更使用已核对的 revision。

`--dry-run` 仅验证本地结构，不证明对象存在、权限有效或业务配置正确。`--allow-write` 只解除客户端拦截，
无法验证对话中的人工确认；最终确认流程由 skill 及使用它的 AI 宿主执行，不是服务端权限控制或不可绕过的授权凭证。

超时、网络失败、5xx、响应超限、JSON 无法解析或本地输出失败可能发生在操作已经生效之后。错误中的 `outcome_unknown`
提示结果未确认，不承诺回滚。关闭可能触发 FinalHook；来源 202 表示受理而非所有 Worker 已切换；
审计启动/取消虽然不修改业务数据，仍创建或改变后台任务，因此也需要最终确认。

## 安装 Skill

```bash
linkd-cli skills list
linkd-cli skills install --agent all --dry-run
linkd-cli skills install --agent all
linkd-cli skills install --agent claude-code --scope project --project-dir /path/to/project
```

安装必须指定 `--agent`：`codex`、`claude-code`、`cursor`、`gemini-cli` 或 `all`。默认 `--scope user`；
项目级默认当前目录，可以通过 `--project-dir` 指定已存在的目录。

| Agent | 用户级 | 项目级 |
| --- | --- | --- |
| Codex / Cursor / Gemini CLI | `~/.agents/skills/linkd-ops` | `.agents/skills/linkd-ops` |
| Claude Code | `~/.claude/skills/linkd-ops` | `.claude/skills/linkd-ops` |

目录核对日期为 2026-09-24，依据 [Codex](https://learn.chatgpt.com/docs/build-skills)、
[Claude Code](https://code.claude.com/docs/en/skills)、[Cursor](https://prod.cursor.com/docs/skills)、
[Gemini CLI](https://geminicli.com/docs/cli/using-agent-skills/) 官方文档。
共享目录可以被多个已安装的 Agent 发现，`--agent all` 只复制两份内容，不制造重复 skill。

skill 完整内嵌在二进制中，安装不联网、不修改 Agent 权限配置。相同内容重复安装不写文件；
不同内容默认拒绝，使用 `--force` 才覆盖随包文件，保留其他用户文件，拒绝 skill 内符号链接。
跨目录安装不保证事务性；错误可能留下已完成的文件，检查输出后重复安装可继续完成。

Skill 入口及维护源为 [linkd-ops/SKILL.md](../../internal/aicli/skills/linkd-ops/SKILL.md)，
按需引用排障路径和操作确认单；安装不等于验证 Agent 已加载，必要时刷新或重启宿主并核对 skill 列表。

## 验证与边界

```bash
go test -race ./internal/aicli ./cmd/linkd-cli
make cli
make check
```

测试使用临时配置、临时安装目录和 mock Console，验证实际二进制的认证、只读调用、操作拦截、dry-run 及安装行为。
它们不依赖真实中间件，不证明真实环境的调度、投递或存储已经通过验证。

维护 CLI 时，新增接口必须登记明确读写属性、参数及影响并补充相应契约测试；同步检查 skill 的诊断路径。
仅格式化本次 CLI 文件可使用 `make fmt GO_FILES="..." FORMAT_CONSOLE=0`，完整格式门禁仍由 `make check` 执行。
