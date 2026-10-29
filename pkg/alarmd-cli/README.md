# alarmd-cli

登录得到本部署的运维会话（标识沿用 `deployment_ops_readonly`）：取证读取、Pod 命令执行与
工作负载管理都在这一个会话里，不再另行申请执行权限。服务端不再接受早先单独申请的执行会话，
带 `exec-v1.` 前缀的续期凭据按 `renewal_expired_or_revoked` 拒绝，重新登录即可。每次执行和
管理操作都在服务端留一条审计记录（`sre_action` 日志与 `sre_actions_total` 计数）。

CLI 与服务端一起升级：较早的 CLI 用固定 30 秒请求期限，短于 `pod.exec`（45 秒）和
`slot.query`（最长 2 分钟）的执行期限，这些调用会在服务端完成前被本地切断。

`invoke` 可用 `--params-file path.json` 传入嵌套 JSON 参数；`--params-file -`
从 stdin 读取。`--stdin-file script.txt` 与 `--script-file script.txt` 等价，只把
文件内容填入 describe 登记的 `parameter_source: stdin` 字符串参数，CLI 不执行本地
shell。二者也支持 `-`，且不能与参数 JSON 同时消费 stdin。`--input JSON|@file`
保持兼容，与 `--params-file` 互斥。

调用前读取 describe 的权限、请求字节上限和 `execution_timeout_ms`，请求期限为
操作期限加 `request_limits` 声明的 admission 与传输余量；未声明余量时用 5 秒，旧服务端未声明期限时保留 30 秒。每次执行只发送一次
invoke，执行请求遇到超时、断连或损坏回执时返回 `remote_state_unknown`，并保存带请求摘要和目标的脱敏结果文件；用户先检查已有执行事实再明确重试。结果沿用
脱敏、原子写入的私有 `meta.result_file`，保留服务端返回的目标和远端执行状态。

面向运维取证的独立 Go 客户端。服务端负责操作目录、输入合同、预算和证据判定；客户端只处理环境登录、通道调用、凭据与输出。没有内置业务 operation，也不访问 K8s、Redis 或任意内部 URL。

## 安装

首版支持 macOS arm64 和 Linux amd64；运行只需单个二进制。下载对应版本的归档及 `SHA256SUMS` 后先校验，再解压到自己的可执行目录：

```sh
# macOS；Linux 可改用 sha256sum -c SHA256SUMS
shasum -a 256 -c SHA256SUMS
tar -xzf alarmd-cli_<version>_<os>_<arch>.tar.gz
./alarmd-cli --version
./alarmd-cli --help
```

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

登录自动导入授权码中的稳定 `environment_id`、名称和入口；可选 `auth login --env <id>` 用于核对环境。其余远程命令必须显式传 `--env`。`profile use <id>` 仅记录人工偏好，不会给远程命令隐式选择环境。服务端更新操作目录后客户端无需升级；先读 discover 摘要，按需读 describe 中的 schema、limits、parameter_sources 和示例。部署自带与它同一提交构建的 alarmd-cli，并在 `api/cli/version` 报出版本；发行版 CLI（版本号为 `x.y.z`）在登录和每次 discover 时读一次，与本地版本不同就在 stderr 提示，并给出从这个部署安装匹配版本的命令（`curl -fsSL <入口>/api/cli/install.sh | sh -s -- --server <入口>`）。不自动更新；部署不报版本（旧部署 404、未打包 CLI 的 503）或本地是开发构建时不提示。

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

- 调用可以并行：服务端不限制同时进行的调用数，每个调用只受自己的大小上限与期限约束（以 describe 的 limits 为准）。每会话每分钟 600 次调用的限额只用来拦住不停循环的客户端，超过时返回 `rate_limited`。
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

`diagnose`、`accept` 和 `snapshot` 是 CLI 内置的组合命令，直接运行；其余都通过 `invoke <operation>` 调用。下面每条写成"问题 → 调用 → 看哪些字段"。

**没有 agent 的环境：先做一份诊断快照**

1. `alarmd-cli snapshot --env <environment_id> [--out <目录>]`：按服务端 `discover` 给出的每会话调用速率，读操作目录、`fleet.get`、全部 `diagnose.environment` 分页、一组固定读取、`fleet.get` 里不正常的副本（逐个读运行配置、指标名单与关键指标族、日志）和不在检测的策略（`strategy.get` 与 `object.get`），写成一个 `alarmd-snapshot-<环境>-<UTC 时刻>.tar.gz` 和旁边的 `.sha256`。计划调用数和时长上限（调用数按速率折成分钟，再加一次调用的最长期限 30 秒）在开始收集前写进 `manifest.json`；超过时长或 512 MiB 的读取记为 `skipped_by_bound`。每会话调用速率由同一登录下的所有调用共用（另开终端跑的命令也算），所以一次读取可能被 `rate_limited` 拒绝；被拒的调用没有执行，快照按应答的 `Retry-After`（没有时等到本机时钟的下一分钟再多 1 秒）等待后重问同一读取，每项最多问 3 次。等待的时间没有调用能用，所以时长上限随每次等待顺延，顺延总量不超过上限本身（manifest 的 `budget_wait_seconds` 记总等待）；放不下的等待不等，该项记为 `refused`（原因 `rate_limited`）。其他拒绝不重问。退出码：0 每项都收到了；3 只在有没收到的项时（`refused`、`timeout`、`skipped_by_bound`、`not_in_catalog`，逐项写在 manifest 里）；应答状态为 `partial` 的读取算收到，按 `partial` 连同原因记进 manifest，不让退出码变成 3；1 什么都没写成。
2. 快照里保留主机地址、主机名和业务 ID（诊断要用），凭据已脱敏；交给谁就等于给了谁这个环境的这些信息。`manifest.json` 的 `redactions` 不为空，说明服务端漏脱了凭据，按缺陷上报。
3. agent 读快照用同一套命令，加 `--snapshot <文件>`：`discover`、`describe <operation>`、`invoke <operation> --input …`、`diagnose`。读到的形状与在线相同，`meta.snapshot` 写明文件和收集时刻；快照里没有按这组参数读过的，返回 `not_in_snapshot` 并列出它读过的参数。读快照不连网络，`--env` 可省略；给了而与快照的环境不一致时返回退出码 2 并说出快照是哪个环境的，拿到一份不知来源的快照时可以这样核对。

**Pod / 原生 UQ 取证与运维**

1. `describe pod.targets` 后在一个命名空间里按标签选择器发现目标（`namespace` 不填时是 alarmd 所在的命名空间；不支持跨命名空间列举），继续用 `pod.get` 的真实 UID、containerID 和 imageID；日志、执行和删除必须使用完整身份。`k8s.workloads` 也可以用 `namespace` 指定任意一个命名空间。
2. `pod.logs` 有界读取并复核前后身份。`pod.exec` 的参数文件提供 `target`、`argv`、`timeout_ms`、`output_bytes`；脚本经 `--script-file` 填入 stdin。例如 argv 可为目标实际存在的 `/bin/sh -s`。有 `/bin/sh` 的容器由它让命令单独占一个进程组运行（镜像有 `setsid` 就用它，没有就用 shell 的作业控制；dash、busybox ash 在没有终端时会关掉作业控制），并在期限（向上取整到整秒）到时终止整个进程组，回执 `supervision` 为 `sh_group`；两者都不可用时为 `sh_pid`，只能终止命令本身，它派生的进程可能残留，而且残留的进程拿着输出时连接要等本地期限切断；期限与命令自己结束同时发生时按退出码判，143/137 为超时，其他为完成并带 `deadline_raced`。没有 `/bin/sh`（或没有 `sleep`）的容器直接执行 argv，回执 `remote_state` 为 `remote_unsupervised`、`unsupervised_reason` 为 `no_shell`/`no_sleep`：期限内结束带退出码，到期只能切断连接（`stream_cut`），命令在目标里可能还在运行。监督进程自己没起来时回执带 `bootstrap_exit_code` 和 `bootstrap_class`（只给类别，不给原文）。本 CLI 不认识的 `remote_state`（更新的服务端才有的状态）按服务端原样显示，不当作无效回执；升级 CLI 后才按名字解释。
   管理操作：`pod.delete`（按发现时的 UID 删除，Pod 已被替换时不删）、`workload.restart`（与 kubectl rollout restart 相同）、`workload.scale`（回执写明原副本数），都按发现时的 UID 写入，目标已被替换时返回 `pod_target_changed`。
   换镜像：`workload.set_image`（参数 `namespace`、`name`、Deployment 的 `uid`、`container`、`image`）立即返回 `receipt_id` 和原镜像，之后由 alarmd 盯滚动：新镜像起不来就自动换回原镜像；镜像被别人改了就不动（`superseded`）。改的是 alarmd 自己的 Deployment 也可以：被替换的进程交出回执，由其他进程接着盯。用 `workload.rollout`（`receipt_id`，或 `namespace`+`name` 取最近一张）读当前镜像、原镜像、滚动进度和结束状态；回执保留 7 天。能改哪些 Deployment 由通道的集群级 ServiceAccount 角色决定（与 `workload.restart` 同一个 patch 权限）；apiserver 拒绝时返回 `rollout_forbidden`。
3. 执行回执中的 `remote_completed`、`exit_code`、输出截断与前后身份各自解释；操作成功不代表脚本成功。连接异常时 `remote_state_unknown` 表示远端事实待核对，禁止自动重试；启动阶段超时（未收到监督进程的启动回执）同样返回 `remote_state_unknown`，命令仍可能在远端运行，最长到它自己的超时；执行完成但事后身份读取失败仍是部分证据。
4. `uq.egresses` 返回原生出口：部署没有登记时，是一个继承当前 UQ 地址、tenant/space 按请求校验的默认出口。`uq.query` 必须使用登记端点、允许的 tenant/space 和从实际页面取证得到的 QueryTs JSON。首版支持结构化 metric/log QueryTs；拒绝直接 SQL、物理存储覆盖和 BKData 旁路，不接受调用方 URL 或身份头。
5. UQ 回执保留 actual_url、tenant/space、原始请求摘要与 HTTP/业务状态。metric 保留脱敏 native JSON 与游标；log/原始行在主题脱敏前只保留状态、数量和摘要，省略正文/携值游标并标记部分证据，内容取页面业务脱敏后的投影。时间戳使用登记单位的 10 位秒或 13 位毫秒字符串，查询窗最多 24 小时。请求摘要用于核对这次请求，跨语言摘要须先核对序列化方式。
6. 普通 Trace 先从 `uq.egresses` 选择登记的 `trace_targets`，再 `describe uq.trace`，以 egress、target、body 调用。原生请求使用 `bkapm`、实际登记 table_id、`time_field: start_time`、秒字符串窗口、显式 limit（1–1000）和非负 from；共享表保留业务/应用的单值 eq AND 条件。返回脱敏原始 Span，时间与耗时保持微秒；`trace_page` 描述固定窗口、投影、returned/total/more，后续页显式读取。该操作沿用当前 UQ 直连，出口 URL 省略时继承 runtime UQ 地址，查询来源沿用当前 query_source。

页面业务装配与授权证据由页面所在系统的诊断入口提供；部署级查询授权不代表受影响页面用户的权限。

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

1. `invoke strategy.get`，输入 `{"strategy_id":"<strategy_id>"}`：看 `dispositions` 里每条的 `disposition`（例如 `CONFIG_REJECTED`、`UNSUPPORTED_PHASE2_CAPABILITY`、`CONFIG_NOTED`）、`reason`、`field_path` 和 `detail`。
2. `invoke strategy.config`，输入 `{"view":"source","strategy_id":"<strategy_id>"}`：读当前策略缓存里的配置（`value`；凭据类字段已省略，省略了什么记在 `omitted`），按 `field_path` 对照被拒的字段。要读已发布的不可变对象时用 `view=published`：必填 `strategy_id`、`query_group`、`object_digest`，可选 `business`、`tenant`，都取自 strategy.get 的 `plans[]`；strategy.get 的 `next_call` 里 `view=published` 的那一项已经带齐，直接用它的 `params`。

**数据是否迟到、首读是否读早**

1. `invoke strategy.get` 取得 `plans[].query_group`。
2. `invoke lookback.get`，输入 `{"owner_query_group":"<query_group>"}`：由持有该运行对象的副本回答（`meta.owner` 写明是哪个副本）。结果是这个副本的整体读数（`scope=answering_replica`），不只这一个 QG：在 `result.stats` 的列表里按 `query_group` 找它。看各档复查的变化、到齐时刻分布、按事实分的样本类别，以及 `result.stats.read_early`（连续整窗读早的 QG：当前有效的 time_delay 与建议值；为空表示没有）。

### 5. 安全边界

- 以 describe 的 `effect` 为准：`exec` 是执行或管理操作，会改变集群里的对象，每次调用都留审计记录；不要自动重试。
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

## 集群资源的检查与写入（SRE 通道）

SRE 通道有集群管理权限（RBAC 组只读，不给 bind/escalate）。三个操作走同一个服务端 apply 路径，只认 chart 授权的种类（chart 之外的种类答 `not_granted_kind`，Role、ClusterRole 与绑定答 `rbac_not_dry_run` / `rbac_not_writable`，都不发出）：

```sh
# 部署前检查：每个对象以 dryRun=All 交给 apiserver，准入 webhook、schema、配额、冲突照常检查，什么都不落库
helm template <release> <chart> -n <ns> -f <values> > render.yaml
python3 -c 'import json,sys;print(json.dumps({"manifests":open(sys.argv[1]).read(),"namespace":sys.argv[2]}))' render.yaml <ns> > dry.json
alarmd-cli invoke k8s.dry_run --env <environment_id> --input @dry.json
# 写入：字段管理者 alarmd-sre；冲突时答 conflict，force=true 接管；结果给出字段差异，Secret 只显示键与指纹
alarmd-cli invoke k8s.apply --env <environment_id> --input @apply.json
# 删除：按先前 k8s.list 读到的 uid；kube-system、kube-public、kube-node-lease、default 与 alarmd 所在命名空间不删
alarmd-cli invoke k8s.delete --env <environment_id> --input '{"api_version":"networking.k8s.io/v1","kind":"Ingress","namespace":"<ns>","name":"<name>","uid":"<uid>"}'
```

Secret 的值只从本地文件经 `--input @文件` 传入，从不出现在应答、审计与日志里；CLI 到 alarmd 若是明文 HTTP，值会以明文经过集群网络。每次写入都留 `sre_action` 审计。对象带 `meta.helm.sh/release-name` 时应答写明 `helm_release`：下一次 `helm upgrade` 会改回或冲突。

**卸载一个 helm release（如 bk-monitor）**，按顺序：

1. 列出这个 release 拥有的对象：对每个命名空间级种类 `k8s.list {"kind":"<Kind>","label_selector":"app.kubernetes.io/managed-by=Helm","paths":["metadata.annotations"]}`，留下 `meta.helm.sh/release-name` 是它的；集群级的同样列一遍。
2. 逐个 `k8s.delete`，带读到的 `uid`。先删工作负载（Deployment、StatefulSet、DaemonSet、Job、CronJob），再删 Service、Ingress、ConfigMap、Secret 等。
3. RBAC 对象（Role、RoleBinding、ClusterRole、ClusterRoleBinding）通道不写，列给人用 kubectl 删。
4. 最后删 helm 自己的记录：release 命名空间里 `sh.helm.release.v1.<release>.v*` 这些 Secret（`k8s.list {"kind":"Secret","namespace":"<ns>","label_selector":"owner=helm,name=<release>"}`，逐个按 uid 删）。删了以后 `helm list` 里就没有这个 release；在那之前不要删，否则 helm 会把剩下的对象当成孤儿。

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

凭据位于平台用户配置目录下的 `alarmd-cli/profiles.json`；`ALARMD_CLI_CONFIG_DIR` 可指定独立目录。目录权限 0700、凭据/锁/结果文件 0600。响应在 `results/` 下保留，客户端不自动删除证据。勿把凭据配置目录上传到工单或公开仓库。

固定边界：invoke 网络期限取 describe 的操作期限、admission 期限和传输余量之和，旧服务端为 30 秒；服务端响应最大 256 MiB（服务端自己的上限是所在 Pod 内存的 1/32，16–256 MiB），超限拒绝解码；输入文件最大 16 MiB，并落实 describe 的请求和 stdin 上限；授权码最大 64 KiB。没有不经服务端校验的任意 endpoint、operation 枚举或业务诊断逻辑。此版本不承诺 Windows。

## 构建与验证

需要 Go 1.23 或更新版本：

```sh
go test -race ./...
go vet ./...
go build -buildvcs=false -trimpath -ldflags '-X main.version=dev' -o alarmd-cli .
sh scripts/release.sh v0.1.0
```

发布脚本在 `dist/<version>/` 生成 darwin-arm64、linux-amd64 归档、版本与源码回执，以及 `SHA256SUMS`。测试覆盖 TLS 兑换、前缀路由、环境/scope、重定向拒绝、过期提示下续期、部分大结果与脱敏、revision 变化不重试、迟到响应/CAS、未知远端撤销，以及真实二进制的零配置 help/version。测试依赖本机随机端口，不连接线上系统。
