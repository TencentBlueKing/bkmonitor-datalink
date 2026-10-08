# alarmd-cli

面向运维取证的独立 Go 客户端。服务端负责操作目录、输入合同、预算和证据判定；客户端只处理环境登录、通道调用、凭据与输出。没有内置业务 operation，也不访问 K8s、Redis 或任意内部 URL。

## 安装

支持 macOS arm64、Linux amd64，以及 Windows 10/11 x64（普通用户、本地 NTFS、PowerShell 5.1/7）；运行只需单个二进制。下载对应版本的归档及 `SHA256SUMS` 后先校验，再解压到自己的可执行目录：

```sh
# macOS；Linux 可改用 sha256sum -c SHA256SUMS
shasum -a 256 -c SHA256SUMS
tar -xzf alarmd-cli_<version>_<os>_<arch>.tar.gz
./alarmd-cli --version
./alarmd-cli --help
```

Windows 使用 ZIP，以下命令在归档所在目录执行，将版本替换为实际版本：

```powershell
$archive = 'alarmd-cli_<version>_windows_amd64.zip'
$entry = Get-Content .\SHA256SUMS | Where-Object { ($_ -split '\s+', 2)[1] -eq "./$archive" }
if (@($entry).Count -ne 1) { throw 'Missing or duplicate checksum entry' }
$expected = ($entry -split '\s+', 2)[0]
if ((Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash -ne $expected) { throw 'Checksum mismatch' }
Expand-Archive -LiteralPath $archive -DestinationPath .\alarmd-cli
.\alarmd-cli\alarmd-cli.exe --version
.\alarmd-cli\alarmd-cli.exe --help
```

可把解压目录加入用户 PATH，或使用 exe 的完整路径；无需管理员权限、Go 或 Bash。Windows Server、ARM64、共享盘和非 NTFS 配置位置不在当前支持范围。需要企业代码签名的终端策略应由发布方另行配置。

`--help` 与 `--version` 无需配置或网络。制品是否已部署与服务端是否可达需要另行验收。

## 从零开始

先打开已知环境的可观测取证通道（observability evidence channel，下文简称 OB）CLI 授权页面，输入部署管理员密钥，确认后生成一次性授权码。alarmd 自己校验该密钥，授权范围为部署级运维取证。管理员密钥通过部署 Secret 配置为 `cli.admin_key`，只在页面当次授权时输入，不保存到 CLI 配置；CLI 只持有兑换后的短时会话和续期凭据。

授权码有两种交给 CLI 的方式。浏览器和 CLI 在同一台机器上时，页面会给出一条 `alarmd-cli auth listen --url <entry> --port <port> --state <state>` 命令，在 CLI 所在机器上运行它，页面把授权码交给本机 `127.0.0.1:<port>`，登录一次后命令退出。不在同一台机器上时，运行 `auth login` 后粘贴授权码，终端不回显；也支持从受保护 stdin 读取。授权码不接受普通命令行参数。

```sh
alarmd-cli auth login
alarmd-cli profile list
alarmd-cli discover --env <environment_id>
alarmd-cli describe <operation> --env <environment_id>
alarmd-cli invoke <operation> --env <environment_id> --input '{"field":"value"}'
# 参数也可以读取已有的 JSON 文件
alarmd-cli invoke <operation> --env <environment_id> --input @input.json
```

PowerShell 5.1/7 推荐通过文件传入 JSON，避免内联引号差异：

```powershell
$json = '{"field":"value"}'
[System.IO.File]::WriteAllText('C:\work\input.json', $json, [System.Text.UTF8Encoding]::new($false))
.\alarmd-cli.exe invoke <operation> --env <environment_id> --input '@C:\work\input.json'
# 私有 CA 使用本机绝对路径；输入授权码时不回显。
.\alarmd-cli.exe auth login --ca-cert 'C:\work\private CA.pem'
```

输入 JSON 文件必须使用 UTF-8、无 BOM。PowerShell 5.1 的默认 `Out-File` 编码不能直接用于该文件。stdout 是 UTF-8 JSON；使用 PowerShell 处理中文输出时，可先设置 `[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)`。

登录自动导入授权码中的稳定 `environment_id`、名称和入口；可选 `auth login --env <id>` 用于核对环境。其余远程命令必须显式传 `--env`。`profile use <id>` 仅记录人工偏好，不会给远程命令隐式选择环境。服务端更新操作目录后客户端无需升级；先读 discover 摘要，按需读 describe 中的 schema、limits、parameter_sources 和示例。

每次 invoke 在本进程先 describe 一次，再携带当前 revision 调用一次。没有持久 schema 缓存、完整客户端 schema 校验器或自动重试。`catalog_changed` 说明合同变化，应重新查阅 describe 后决定是否再次调用。`next_call` 只是建议，客户端不会自行执行。

## 给 Agent 的用法

本节写给代人取证的 Agent，每一步都可以照着执行。操作名和参数一律以服务端返回为准，不要凭记忆或本文推测。

### 1. 前提：登录由人完成

- 登录只能由人完成：人在授权页输入管理员密钥，页面给出授权码或一条 `auth listen` 命令，由人执行或粘贴。Agent 不接触、也不索要管理员密钥和授权码。
- Agent 拿到的是已经登录的环境。开始前确认：

  ```sh
  alarmd-cli profile list                         # 找到 environment_id，不显示凭据
  alarmd-cli auth status --env <environment_id>   # 由服务端核对会话
  ```

- 任何命令返回 `error.code` 为 `credentials_expired` 时，停下来，把输出里的登录页地址和命令交给人，等人重新登录后再继续。
- 每个远程命令都显式带 `--env <environment_id>`。`profile use` 只记录人的偏好，不会替命令选环境。

### 2. 固定调用顺序：discover → describe → invoke

```sh
alarmd-cli discover --env <environment_id>
alarmd-cli describe <operation> --env <environment_id>
alarmd-cli invoke <operation> --env <environment_id> --input @input.json
```

1. `discover`：列出当前服务端提供的操作。只使用这里列出的操作名。不同部署、不同版本的操作目录可能不同，本文提到的操作也以 discover 为准。
2. `describe <operation>`：读 schema（必填字段、类型、枚举、上下限）、limits、parameter_sources（每个参数取自哪一步的哪个字段）和 examples，按它填参数。ID 类参数（例如 `query_group`、`object_digest`）只从上一步的结果里取，不要自己构造。
3. 把参数写进 JSON 文件，用 `--input @文件` 调用。参数来自上一步的 `next_call` 时，把其中的 `params` 原样存成文件。
4. 读结果里的 `next_call`：它是服务端建议的下一步列表，每项有 `operation`、`params`、`reason`，可能不止一项；客户端不会自动执行。需要继续时，按 `operation`（以及 `params` 里的区分字段，例如 `view`）选出要走的那一项，照它的 `params` 发起下一次 invoke。

调用节奏：

- 一次只发一个调用，不要并发。服务端每个进程同一时刻只执行一个取证读取，忙时返回 `request_budget_exceeded`；每个会话每分钟最多 30 次 invoke，超出返回 `rate_limited`。遇到这两种，稍后重试同一个调用。
- `catalog_changed`：操作目录在 describe 与 invoke 之间变了，这次调用没有执行。重新 describe 该操作，按新的 schema 核对参数后再 invoke。

### 3. 读结果

stdout 是一个 JSON 对象（`--help` 除外），进度和提示写在 stderr。

| 退出码 | 含义 | Agent 怎么做 |
| --- | --- | --- |
| 0 | 完整结果 | 读 `result` |
| 3 | 部分证据 | 读 `evidence.limitations`，结论里写明依据不完整；按 limitations 和 `next_call` 补读 |
| 1 | 调用、协议、配置或落盘失败；`accept` 有 FAIL 或 READ_FAILED 项时也是 1 | 读 `error.code` 和 `error.message`，按上一节处理；不要换参数盲试 |
| 2 | 命令或输入无效 | 重新 describe，按 schema 改输入 |

- 退出码只说明这次调用的结果，不说明业务是否健康。部署和策略的状态在 `result` 里。
- stdout 最多 20 KiB。每次调用的完整脱敏响应都保存在 `meta.result_file`（绝对路径）。`result_omitted=true` 时直接读这个文件，不要为此再调用服务端。
- `evidence.complete=false` 或 `evidence.limitations` 非空时，结论要带上这些限制。
- 字段含义看 describe 返回的 summary 和输出 schema。

### 4. 常用排障路径

`diagnose` 和 `accept` 是 CLI 内置的组合命令，直接运行；其余都通过 `invoke <operation>` 调用。下面每条写成"问题 → 调用 → 看哪些字段"。

**某条策略为什么没告警**

1. `alarmd-cli diagnose --env <environment_id>`：逐页读 `diagnose.environment` 并核对覆盖（退出码 3 表示覆盖不成立）。在 `result.strategies` 里找 `strategy_id` 为 `<strategy_id>` 的行，看 `verdict`、`action`、`reason` 和 `dispositions`（每条的 `scope`、`disposition`、`reason`、`field_path`、`detail`）。
2. `invoke strategy.get`，输入 `{"strategy_id":"<strategy_id>"}`：看 `standing`、`dispositions` 和 `plans[]`（每个运行对象的 `query_group`）。
3. `invoke object.get`：参数取 strategy.get 的 `next_call` 里 `operation` 为 `object.get` 的那一项。看 `anomaly`、`facts`、`records_status` 和 `records`（保留的生命周期记录，只保留约一小时，没有时不出现）。
4. `invoke slot.get`：输入 `query_group` 和 `evaluation_time`（Slot 的 Unix 秒）。object.get 只为它保留记录里的 Slot 给出 `slot.get` 的 `next_call`；没告警的策略常常没有保留记录，这时 `evaluation_time` 取第 1 步那一行的 `plans[].last_full_slot`（最近一轮读完整的 Slot，有的话）。它重建历史 Slot 的查询条件、关联保留记录，不请求查询服务。
5. `invoke slot.query`：参数取 slot.get 的 `next_call`。它按保留条件现在重查一次，结果 `kind=requery_now`。看 `query.completion`（`completeness`、`data_state`、`route_details`，查询被拒时还有已脱敏的 `error_excerpt`）和 `query.series`。默认只返回部分序列，`query.truncated=true` 且 limitations 含 `output_truncated`，退出码为 3，这是正常的；要看更多序列按 describe 的上限传 `max_series`、`max_points`。重查可能包含迟到数据，不等于当时 Slot 读到的输入。

**部署是否健康**

1. `alarmd-cli accept --env <environment_id>`（可加 `--expect-build <build_prefix>`）：逐项给出 PASS、FAIL、INFO、READ_FAILED、NOT_BUILT 或 UNDECIDED，任何 FAIL 或 READ_FAILED 时退出码为 1。`column=governance` 的项是策略负责人要改的，不算部署失败。全部读数在 `meta.result_file`。
2. `invoke fleet.get`：看 `health`（整体结论），再看对象计数 `expected`、`covered`、`healthy`、`unknown` 与 `anomalies_total`。
3. `invoke k8s.pods`：看 Deployment 和各 Pod 的就绪、重启次数、上次退出原因。需要时再用 `k8s.events`（可选输入 `pod`）、`k8s.logs`（必填 `pod`；可选 `previous`、`lines`，按子串过滤用 `contains`，配 `since_seconds` 限定时间窗），`lifecycle.get` 看各副本的启动与停止记录。

**策略配置为什么被拒或被改写**

1. `invoke strategy.get`，输入 `{"strategy_id":"<strategy_id>"}`：看 `dispositions` 里每条的 `disposition`（例如 `CONFIG_REJECTED`、`UNSUPPORTED_PHASE2_CAPABILITY`、`CONFIG_NORMALIZED`）、`reason`、`field_path` 和 `detail`。
2. `invoke strategy.config`，输入 `{"view":"source","strategy_id":"<strategy_id>"}`：读当前策略缓存里的配置（`value`；凭据类字段已省略，省略了什么记在 `omitted`），按 `field_path` 对照被拒的字段。要读已发布的不可变对象时用 `view=published`：必填 `strategy_id`、`query_group`、`object_digest`，可选 `business`、`tenant`，都取自 strategy.get 的 `plans[]`；strategy.get 的 `next_call` 里 `view=published` 的那一项已经带齐，直接用它的 `params`。

**数据是否迟到、首读是否读早**

1. `invoke strategy.get` 取得 `plans[].query_group`。
2. `invoke lookback.get`，输入 `{"owner_query_group":"<query_group>"}`：由持有该运行对象的副本回答（`meta.owner` 写明是哪个副本）。结果是这个副本的整体读数（`scope=answering_replica`），不只这一个 QG：在 `result.stats` 的列表里按 `query_group` 找它。看各档复查的变化、到齐时刻分布、按事实分的样本类别，以及 `result.stats.read_early`（连续整窗读早的 QG：当前有效的 time_delay 与建议值；为空表示没有）。

### 5. 安全边界

- 所有操作都是只读取证，不修改部署或策略。
- 不要把凭据写进命令参数、对话、日志或证据。CLI 的输出不含凭据。
- 配置目录（默认是用户配置目录下的 `alarmd-cli/`，可由 `ALARMD_CLI_CONFIG_DIR` 指定）里的 `profiles.json` 含会话和续期凭据，不要读取、复制或外传。
- `results/` 下的结果文件是脱敏后的响应，但仍含策略、运行对象、查询条件等部署内部信息。只引用需要的片段，不要整目录外传或提交到公开仓库。
- 取证只走 CLI 提供的操作，不要绕开它直接访问 K8s、Redis 或内部地址。

## Slot 取证

需要服务端的操作目录里有 `slot.get` 和 `slot.query`（用 `discover` 查看）。先查看操作合同：

```sh
alarmd-cli describe slot.get --env <environment_id>
alarmd-cli describe slot.query --env <environment_id>
```

从 `object.get` 的 `next_call` 取得 `slot.get` 参数；后者只读取保留证据和查询预览，再从其 `next_call` 选择一个 `slot.query`，将 `params` 原样保存为 JSON 文件并通过 `--input @文件名` 调用。大结果从 `meta.result_file` 读取。`slot.query` 默认由当前 owner 执行，也可按 describe 的合同显式传 `replica` 选择实例。

查询结果的 `kind=requery_now` 表示按保留条件发起的本次重查，可能包含迟到数据，不能当作原 Slot 的完整历史输入或告警重放。返回 partial（退出码 3）时检查 `evidence.limitations`、查询完成状态和截断标记；历史合同缺失时不能用当前配置替代。

## 部署本身的 K8s 读数

需要带 K8s 只读取证的服务端，以及 chart 为 alarmd 渲染的只读 Role（CLI 开启即带）。三个操作都由入口副本用自己 Pod 的 ServiceAccount 只发 GET，只读 alarmd 自己的 Deployment：

```sh
# Deployment 状态，各 Pod 的阶段、就绪、重启次数与上次退出原因
alarmd-cli invoke k8s.pods --env <environment_id>
# Deployment、ReplicaSet、Pod 上的事件，新的在前；可只看一个 Pod
alarmd-cli invoke k8s.events --env <environment_id> --input '{"pod":"<pod>"}'
# 容器日志末尾；previous=true 读上一次运行（崩溃前）
alarmd-cli invoke k8s.logs --env <environment_id> --input '{"pod":"<pod>","previous":true,"lines":200}'
```

读不到时按失败码区分：`k8s_rbac_forbidden`（Role 没建或被关）、`k8s_service_account_not_mounted`、`k8s_apiserver_unreachable`、`k8s_not_found`（含从未写过的 previous 日志）、`k8s_not_in_scope`（不是 alarmd 的 Pod 或容器）等，不会以空列表冒充"没有事件"。所有副本都挂掉时这条路读不到，只能用 kubectl。

## 会话与环境

```sh
alarmd-cli auth status --env <environment_id>
alarmd-cli auth logout --env <environment_id>
# 只有核实同一环境确实迁移入口后，才明确重新绑定
alarmd-cli auth login --rebind
```

登录即完成一次配对：兑换得到短时会话和一个续期凭据，都存在 0600 的 profile 里。除 logout 外，每个远程命令发出前，如果会话已过期或离过期不到一分钟，先用续期凭据换一个新会话和下一个续期凭据。invoke 请求另外携带 `renew_if_due=true`，是否续期由服务端准入决定。配对连续 30 天未使用、被撤销或管理员密钥轮换后失效，此后每个命令都返回 `credentials_expired`，附登录页地址和 `auth login` 命令，需要人重新登录。没有 daemon 或保活心跳。本地 `expires_at` 仅是提示：另一进程可能已经续期，服务端始终负责裁决。

logout 先请求远端撤销，再清理匹配的本地凭据。网络失败也会按会话 ID 与 token 摘要条件清理，同时明确 `remote_revocation_confirmed=false`、退出码 1。旧会话 A 的迟到响应或 logout 不能覆盖或删除新登录的 B；同一会话的并发 expiry 回写只保留较新期限。退出后保留无凭据的环境入口绑定及既有证据文件。

HTTP 或 HTTPS 由部署入口决定，CLI 按授权码内的原协议连接并保存，不要求公网证书，不自动升级、降级或跟随重定向。HTTP 直接运行 `auth login` 即可，结果的 `meta.client_transport` 标记 `encrypted=false`。

HTTPS 默认使用系统信任库。私有 CA 可通过 `auth login --ca-cert /absolute/path/ca.pem` 按环境保存并追加信任根，仍校验证书链和主机名；后续请求需要保留该 CA 文件。接受自签证书或域名不匹配时，使用 `auth login --insecure-tls`，后续该环境请求跳过证书链和主机名校验。两选项互斥且仅适用于 HTTPS，授权码和服务端不能自行开启；重新成功登录不带 `--insecure-tls` 即恢复正常校验。`profile list` 和取证回执显示实际模式。

兑换不会附带已有 token、cookie 或自定义身份头。入口不接受 URL userinfo/query/fragment 和路径穿越。同 environment_id 的 origin（含协议）改变时，使用 `auth login --rebind` 确认新的环境绑定。

## 输出与证据

除 help 外 stdout 都是 JSON；进度与非致命提示写 stderr。

| 退出码 | 含义 |
| --- | --- |
| 0 | 完整调用成功 |
| 3 | 部分证据，检查 `evidence.limitations` |
| 1 | 调用、协议、配置或落盘失败 |
| 2 | 命令或输入无效 |

业务健康状态保留在 `result`，不与调用成败混淆。完整脱敏通道响应一次原子保存，并在 `meta.result_file` 返回绝对路径。未知可选字段和大整数保留；stdout 最多 20 KiB，必要时省略 result 并标记 `result_omitted=true`，直接读取结果文件即可，不需要重查服务端。客户端删除秘密字段并替换已知 token/grant，服务端仍须执行自己的安全字段投影。

凭据位于平台用户配置目录下的 `alarmd-cli/profiles.json`；`ALARMD_CLI_CONFIG_DIR` 可指定独立目录。Unix 目录权限 0700、凭据/锁/结果文件 0600。Windows 默认为 `%APPDATA%\alarmd-cli`，使用受保护 DACL，仅允许当前用户与 SYSTEM 访问；这不阻止管理员接管权限。临时文件在私有目录中创建，写入内容前保护权限。响应在 `results/` 下保留，客户端不自动删除证据。勿把凭据配置目录上传到工单或公开仓库。

Windows 配置路径必须位于本地固定 NTFS 卷，受管理对象须归当前用户所有；拒绝网络路径、路径中的 junction/symlink/reparse point 及硬链接文件。权限设置失败返回配置错误。APPDATA 被重定向时，请通过 `$env:ALARMD_CLI_CONFIG_DIR = 'C:\Users\<user>\alarmd-cli-config'` 指定本地私有目录。不要把覆盖目录指向已有的公共目录。

多进程通过 `.lock` 串行读取、更新和提交配置，续期也在同一锁内完成；进程退出后系统释放锁，不需手动删除 `.lock`。文件写入采用同目录临时文件、Sync、Close 和替换，不先删除旧文件。Windows 替换可能因外部程序占用而失败，此时返回错误并保留旧文件；该流程不承诺断电事务。远端兑换/续期已成功而本地保存失败时，以实际错误和服务端凭据状态处理，不代表远端操作被撤销。

固定边界：网络期限 30 秒；服务端响应最大 8 MiB，超限拒绝解码；参数 JSON 最大 1 MiB；授权码最大 64 KiB。没有不经服务端校验的任意 endpoint、operation 枚举或业务诊断逻辑。

## 构建与验证

需要 Go 1.23 或更新版本：

```sh
go test -race ./...
go vet ./...
go build -buildvcs=false -trimpath -ldflags '-X main.version=dev' -o alarmd-cli .
sh scripts/release.sh v0.1.0
```

Windows 本地构建（运行不需要 CGO）：

```powershell
go test -count=1 -timeout=10m ./...
go vet ./...
$env:CGO_ENABLED = '0'
go build -buildvcs=false -trimpath -ldflags '-X main.version=dev' -o alarmd-cli.exe .
.\alarmd-cli.exe --version
```

`-race` 需要 CGO 及兼容的 C 编译器，不要使用上述 CGO=0 配置运行 race 测试。

发布脚本在 `dist/<version>/` 生成 darwin-arm64、linux-amd64 TAR 归档、windows-amd64 ZIP、版本与源码回执，以及覆盖所有归档的 `SHA256SUMS`。脚本需要 Unix shell、Go、tar 和 zip。测试覆盖 TLS 兑换、前缀路由、环境/scope、重定向拒绝、过期提示下续期、部分大结果与脱敏、revision 变化不重试、迟到响应/CAS、未知远端撤销、跨进程锁与续期，以及真实二进制的零配置 help/version 和 HTTP/私有 CA 命令链。Windows 专项验证实际 DACL、junction/硬链接拒绝、占用导致替换失败时保留旧文件。测试依赖本机随机端口，不连接线上系统。

GitHub 工作流 `.github/workflows/alarmd-cli.yml` 执行三平台测试、Linux race 和独立 OB/Redis 黑盒。人工触发可生成三平台候选归档，并在对应原生 runner 校验和冒烟；不会自动发布 Release。Windows runner 是 Windows Server，仅作为自动化证据，不能替代 Windows 10/11 桌面验收。鲸盾接入暂未配置。

发布前还须完成 [Windows 验收清单](docs/windows-acceptance.md)。真实终端隐藏输入、Ctrl+C 恢复、另一普通用户访问拒绝、浏览器回调和真实服务端兼容性须分别记录，不以编译或 fixture 通过代替。
