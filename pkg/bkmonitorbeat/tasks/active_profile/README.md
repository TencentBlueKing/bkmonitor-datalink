# Active Profile: Perf / Native CPU、Golang / Go CPU、Go Heap

Active Profile 由 `bkmonitorbeat` 在目标 Linux 机器上完成采集，不包含也不启动自研常驻
helper。当前实现覆盖一期协议中的两种采集方式，`java` 协议已定义、实现待后续阶段补齐：

| `collection_method` | `collection_type` | 采集手段 |
| --- | --- | --- |
| `perf` | `native_cpu` | 直接执行系统 `perf record` / `perf script`，再转换并符号化为 pprof |
| `golang` | `go_cpu` / `go_heap` | HTTP 读取目标进程的 `net/http/pprof` endpoint，产物已带符号 |

两者的调度、并发保护、取消、Event 与终态指标完全一致，差异只在采集与产物获取方式。

协议基线见 [`active-profiling-technical-sharing.md`](../../../../docs/active-profiling-technical-sharing.md)
与 [`active-profiling-data-model.md`](../../../../docs/active-profiling-data-model.md)。

## 两个执行入口

| 入口 | 调度类型 | started | finished | 取消 |
| --- | --- | --- | --- | --- |
| 常驻 Beat（NodeMan 下发） | `once_at` / `cron` | Beat 上报 | Beat 上报 | 支持 |
| 轻量 CLI（BK-JOB 下发） | `immediate` | SaaS 直接写执行表 | CLI 上报 | 支持 |

两个入口共用 `execution.go` 的公共编排层：进程解析、资源保护、采集、上报、取消、终态 Event
与终态运营指标完全一致，入口差异只有"是否上报 started"和上报的传输载体。

轻量 CLI 用 build tag `activeprofile` 或 `basetask` 注册，配置从 stdin 读入，避免命令行
长度限制，也不会把整段执行配置留在 `ps` 输出里：

```bash
bkmonitorbeat -c <主配置> -active-profile-run    < single_host_config.json
bkmonitorbeat -c <主配置> -active-profile-cancel < execution_key.json
bkmonitorbeat                -active-profile-match  < process_selector.json
```

前两个子命令必须显式传 `-c`：运行目录由主配置的 `path.data` 推导，这是常驻 Beat 与轻量 CLI
唯一的共同真值来源。`-active-profile-cancel` 对立即执行和计划任务通用。

## 匹配预览

`-active-profile-match` 供页面在提交前确认采集目标：JOB 在目标主机执行它并回收 stdout，
SaaS 直接解析。它只读不写——不解析主配置、不创建运行目录、不连 GSE，因此**不需要 `-c`**。

```json
{"process_selector": {"type": "command", "match_pattern": "java", "exclude_pattern": "grep"}}
```

```json
{"processes":[{"pid":2341,"process_name":"java","command":"java -jar app.jar","started_at":1789032616000}]}
```

三种进程定位方式（`command` / `pid_file` / `pid`）返回同一种结构。`command` 是命令行的字面
包含，`exclude_pattern` 是**先排除、后包含**的正则，与采集时的判断顺序一致。

做成 Beat 子命令而不是外部脚本，是因为它与采集共用 `resolveProcesses` 和
`ActiveProfileProcessSelector.Clean()`：预览结果与真正执行时的匹配规则不可能分叉。特别是
`exclude_pattern` 用 Go 的 `regexp`（RE2 语法，不支持 lookahead 与反向引用），平台侧校验必须
按同一套语法——否则通过了平台校验的表达式会让整份子配置在 `Clean()` 第一个出错的任务处
失败，该主机上所有计划任务一起作废。

输出约定：

- stdout 是**单个 JSON 对象**。这一模式下日志被丢弃（`DevNull`），避免单个进程的 WARN 混进
  结果；失败由非零退出码表达，此时 stdout 是 `{"error_type": ..., "message": ...}`，
  `error_type` 与 finished 同一套取值。
- 匹配不到进程时 `processes` 是空数组，退出码 0——页面要能显示"没有匹配到进程"。采集端把
  同样的情况当失败，是因为那时已经没有可采对象，两者语义不同。
- `command` 是完整命令行，不截断。它会进入 JOB 日志，而进程参数里可能带口令或令牌，这是
  这个接口固有的暴露面，由 JOB 日志的权限与保留期约束。
- `started_at` 是进程启动时间（Unix 毫秒）。PID 会被系统回收，页面冻结用户选择时靠它与
  `pid` 一起确认是同一个进程。
- `command` 与 `started_at` 只服务预览，**不进 Event**：Event 的 `processes` 按协议严格只有
  `pid` 与 `process_name`。

`command` 模式的预览只是当时的快照。协议要求每次执行重新匹配，页面真正冻结下来的是用户
勾选后的 `pid` 列表。

调用方还要处理两件与二进制本身有关的事：

- **不要把 Agent 安装路径写死。** 同一套环境里 `/usr/local/gse` 与 `/usr/local/gse2_*` 都
  可能存在，按固定路径拼出来的 `plugins/bin/bkmonitorbeat` 在部分主机上不存在，脚本会以
  `No such file or directory` 失败。最稳的定位方式是取正在运行的插件进程：
  `readlink -f /proc/$(pgrep -x bkmonitorbeat | head -1)/exe`，取不到再回落到配置路径。
- **老版本二进制没有这个子命令。** 此时 Go 的 flag 包打印 `flag provided but not defined:
  -active-profile-match` 并以**退出码 2** 结束，与"匹配失败"不是一回事，调用方应当识别成
  采集器版本过低。灰度期间新旧混布必然遇到。

## 节点管理下发模板

计划任务（`once_at` / `cron`）的子配置由节点管理渲染 `bkmonitorbeat_active_profile.conf.tpl`
写入 include 目录。模板在插件包的 `project.yaml` 里注册，只注册 Linux 三个架构——freebsd 包是
预编译二进制，不含这个任务类型。模板随插件包做版本管理：打包时 `config_templates[].version`
被改写成插件版本，SaaS 订阅时引用 `latest`。模板由 `script/generate_support_files/
write_active_profile_conf_tpl.sh` 生成，改结构要改生成脚本，不要直接改 `support-files` 下的
产物。

**字段结构由模板定义，SaaS 只按主机拼装值。** 契约是订阅 `params.context.host_tasks` 与
`params.context.target_hosts`：

```json
{
  "host_tasks": {
    "456": [
      {
        "task_id": 1206, "bk_tenant_id": "system", "bk_biz_id": 11, "bk_host_id": 456,
        "name": "native-once-demo", "collection_method": "perf", "collection_type": "native_cpu",
        "schedule": {"type": "once_at", "execute_at": 4070880000000, "timezone": "Asia/Shanghai"},
        "process_selector": {"type": "pid", "pids": "2341"},
        "collector_config": {"duration_sec": 15, "frequency_hz": 99, "stack_unwind": "fp"},
        "resource_guard_config": {"auto_stop_on_resource_pressure": true, "max_disk_usage": 1024, "min_mem_level": 100},
        "event_data_id": 110001, "profile_data_id": 110002,
        "profile_app_name": "builtin_profile_app", "profile_service_name": "builtin_profile_app"
      }
    ]
  },
  "target_hosts": [{"bk_host_id": 456, "ip": "10.0.0.1"}, {"bk_host_id": 457, "ip": ""}]
}
```

- key 是 `bk_host_id` 的**字符串**形式。模板用 `cmdb_instance.host.bk_host_id` 取当前主机那一组；
  取不到就下发 `tasks: []`，该主机上的主动 Profiling 计划任务随之全部移出调度。
- 模板按 `collection_method` / `collection_type` / `schedule.type` 只渲染协议规定的字段子集：
  golang 不渲染 `process_selector`，`go_heap` 不渲染 `duration_sec`，`once_at` 只有
  `execute_at`，`cron` 才有 `cron_expression` 与可选的生效窗口；`resource_guard_config` 整段可省略。
  task 里多出来的键不会进入子配置。
- `target_hosts` 是计划全部目标主机的快照，`ip` 为下发时 CMDB 内网 IP、查不到为空串。全部主机
  共用一份，放在公共上下文而不是每台主机的 task 里，避免订阅参数随主机数平方增长；模板把它原样
  渲染进每个 task，Beat 不用于采集，只写入 Event content。
- 本地运行参数 `perf_path` / `keep_artifacts` / `inline_symbolization` **不在模板里**：平台不
  下发，Beat 用默认值。
- 标量统一经 `tojson` 输出为 JSON 字面量（YAML 的合法子集），命令行规则、cron 表达式和任务名里
  的引号、冒号、`#`、`*` 都不会破坏 YAML。

模板依赖的三样能力都有节点管理的生产先例：在模板里直接取 `cmdb_instance.host.*` 并调用字典
`.get()`（bkmonitorbeat 主配置模板），`tojson` 过滤器（bk-collector 的
`bk-collector-application.conf.tpl`），以及在 context 里放按 `bk_host_id` 分组的映射（监控平台
PingServer 订阅的 `ip_to_items`，一台主机一个订阅）。

## 执行定位与产物标签

不使用 `run_id` / `profile_id`。一次主机执行由执行键定位：

```text
bk_tenant_id + bk_biz_id + task_id + scheduled_at + bk_host_id
```

三类执行都必须带正数 `task_id`：计划任务为计划 ID，立即执行为 SaaS 提交时建的执行行 ID。

pprof 每个样本携带 6 个标签：`bk_biz_id`、`task_id`、`scheduled_at`、`bk_host_id`、`pid`、
`comm`。执行时 IP、采集方式和采集类型不重复写入样本，它们已经保存在对应 Event 中。

## Event

`dimension` 只保留页面筛选、分桶与聚合需要的字段：`bk_tenant_id`、`bk_biz_id`、
`task_id`（仅计划任务）、`task_name`、`bk_host_id`、`schedule_type`、`scheduled_at`、
`collection_method`、`collection_type`、`status`，finished 再加 `process_names`、`process_pids`。
`target` 是执行时 IP，`timestamp` 是事件实际发生时间，不用 `scheduled_at` 冒充。

所有 dimension 值必须是标量字符串。自定义事件 schema 不接受数组，上报数组会被接收端以
`400 dimension '...' value expected string type` 直接拒绝，整条 Event 都不会落库。因此实际
匹配到的进程以逗号拼接成 `process_names` 与 `process_pids` 两个字符串上报（如
`java,java,nginx` 与 `1234,1235,812`）：按 PID 升序，同一 PID 只保留一次，同名进程不去重，
两者按位置一一对应，SaaS 据此还原进程名与 PID。页面对两个维度都做包含匹配，不加首尾分隔符。

进程名里的分隔符在写入 dimension 前归一化为 `_`（`comm` 由进程自行设置，可能含逗号），否则
进程名与 PID 的位置会错开；`event.content.processes` 保留真实进程名。`delivery_test.go` 里有
针对 dimension 类型约束、位置对应和进程名归一化的防回归断言。

其余信息进入 `event.content` 的紧凑 JSON：`schema_version`、`started_at`，finished 再加
`finished_at`、`execution_duration_ms`、`processes`、`message`，两者都携带本次实际
配置快照 `config`，计划任务再携带目标主机快照 `target_hosts`（立即执行的 CLI 不带，由 SaaS
上报的 Event 携带）。`target_hosts` 随目标主机数增长，不计入下面的上限，也不会被降级丢弃；
`event.content` 在 ES 中是 text 字段，没有 keyword 的长度限制。其余部分有 32 KiB 序列化上限，超限时按 `processes` →
`config` 的顺序降级重建，任何降级结果仍是一段可解析的合法 JSON，并在 `message`
中说明降级原因。

PID 模式的配置快照使用 `host_pids` 而不是单机 `pids`，SaaS 查询任务历史时才能按
`bk_host_id` 合并出完整的多主机配置。

采集端只上报 `succeed/partial_succeed/failed/timeout/cancelled`；`running` 与 `unknown`
由 SaaS 从 started/finished 派生，不写回 Event。符号化统计、执行阶段和结果码都不进入
Event，只留在本机日志。

finished 的 `event.content.error_type` 归类失败原因，SaaS 按类型给出展示文案，`message`
保留原始错误；成功、超时与取消不归类，为空串。归类依据错误链上的标记（`executor.MarkError`
附加，不改变错误文本），没有标记的归为 `collect_failed`：

| error_type | 归入的失败 |
| --- | --- |
| `schedule_expired` | once_at 配置到达本机时已过执行时间 |
| `task_busy` | 同一计划上一轮仍在执行 |
| `process_not_found` | 没有匹配到进程，或采集开始前目标进程全部退出 |
| `empty_profile` | 采集正常结束但没有样本 |
| `perf_unavailable` | 找不到 perf、`perf version` 失败、事件探测失败、字段集不支持、非 Linux |
| `go_endpoint_failed` | Go pprof endpoint 取数或解析失败 |
| `memory_limit` / `disk_limit` | 资源保护：可用内存低于阈值 / 临时文件超过上限 |
| `report_failed` | 产物投递失败 |
| `config_invalid` | 配置不被当前采集器支持，多为与 SaaS 版本不一致 |
| `processes_exited` | `partial_succeed` 的原因：部分进程在采集开始前退出 |
| `collect_failed` | 其余失败 |

### once_at 过期

配置在 `execute_at` 之后才到达本机时不再采集，但仍上报一条 `status=failed` 的 finished
（不发 started），`scheduled_at` 取原计划时间，`message` 说明配置晚到。否则用户在
任务历史里只能看到一片空白，无法区分"下发晚了"和"主机根本没起来"。

## 进程定位

`process_selector` 三种模式，每次执行重新解析，不设进程数量上限：

- `command`：`match_pattern` 是命令行的**直接包含字符串**（不是正则），`exclude_pattern`
  是可选排除正则。判断顺序与现有进程采集一致：先按排除正则跳过，再判断包含条件。
- `pid_file`：去空白后解析单个 PID。只读普通文件且不超过 4 KB，FIFO、设备文件和超长
  文件直接按非法跳过；错误信息只带路径，不回显文件内容，因为路径由平台用户填写而 Beat
  以 root 读取。
- `pid`：SaaS 已按主机拆分，只携带当前主机的 `pids`。

匹配结果为空即在解析阶段失败，不启动采集器。preflight 会把目标收敛为当前仍可采集的
进程：解析之后退出的进程被移除，只要还有一个目标就继续采集，此时主机终态为
`partial_succeed`；全部不可用则为 `failed`。

多进程用一次 `perf record -p pid1,pid2,...` 覆盖，一次 `perf script` 转成单个合并 pprof，
样本靠 `pid/comm` 区分。

`process_selector.type: system` 是不进下发协议的本地调试能力，平台不会下发。

## Golang 采集

Go 不使用 `process_selector`，协议要求配置中省略该段；目标由 `collector_config` 的
`ip_address` 与 `port` 决定，指向目标主机上的 `net/http/pprof` endpoint：

| `collection_type` | 请求 | `collector_config` |
| --- | --- | --- |
| `go_cpu` | `GET /debug/pprof/profile?seconds=<duration_sec>` | `ip_address`、`port`、`duration_sec` |
| `go_heap` | `GET /debug/pprof/heap` | `ip_address`、`port` |

`go_heap` 取的是瞬时快照，协议中没有 `duration_sec`；下发该字段会被清零，执行预算退化为
后处理余量，HTTP 请求单独使用 30 秒预算。`frequency_hz` 与 `stack_unwind` 属于 perf，
Golang 采集会清零，不进入 Event 配置快照。

`ip_address` 不做范围限制，按下发值直连（不走环境变量里的 HTTP 代理，否则本机请求会被
发到无关出口）。

响应本身就是带符号的 pprof，因此 Go 路径不涉及 `perf script` 转换与外部符号化，只补齐执行
定位标签。

**产物全程在内存里，正常路径不落盘。** endpoint 的响应读完时整份数据已经在内存中，解析后
的对象图比它还大，写盘再读回来上报不降低内存峰值、只多一次写读。响应体有 64 MiB 硬上限；
开关打开时解析前还要过内存预算，见「资源保护」。只有 `keep_artifacts` 打开时才为了事后检查在运行目录里留一份
`profile.pprof`，内容与内存中的完全一致。

因为不写文件，`max_disk_usage` 这项配额对它恒不触发；`min_mem_level` 仍然生效，见「资源
保护」。

运行目录本身仍然会创建：取消标记 `cancel.request` 依赖它。

产物标签与 perf 路径完全一致：`pid/comm` 由 endpoint 的监听地址与端口在**执行采集的主机上**
反查监听进程得到，是**尽力而为**的：

- 只在 endpoint 指向本机时反查：`ip_address` 是回环地址、未指定地址或本机网卡地址；填主机名
  时解析结果必须全部是本机地址。跨主机采集（Beat 在 B、进程在 A）直接跳过，否则会把 B 上
  恰好监听同端口的无关进程标进产物。
- 只认能接收发往 `ip_address` 连接的监听 socket：绑在该地址或通配地址上，同端口绑在其他
  本机地址上的进程不算。
- 权限不足、endpoint 由其他网络命名空间提供、或端口被多个进程以 `SO_REUSEPORT` 共享时同样
  拿不到唯一结果。

拿不到时 `pid/comm` 两个标签保持不存在、Event `processes` 为空，采集本身仍然成功。不写占位值。
跨主机时不改用 `/debug/pprof/cmdline` 补进程名：它拿不到 PID，而 Event `processes` 的每一项
都要求 `pid` 与 `process_name` 同时存在，SaaS 的 PID 候选与筛选都依赖 `pid`。

## 资源保护

`resource_guard_config` 的两个阈值单位均为 MB，**语义不同且相互独立**：

- `min_mem_level`：宿主可用内存低水位。启动前检查一次，运行期间每秒复查。Linux 取
  `MemAvailable` 与有限 cgroup v1/v2 可用量的较小值。
- `max_disk_usage`：**本次采集临时文件**的占用配额，统计的是本次运行目录的实际大小，
  不是宿主剩余空间。启动前必然为 0，因此只在运行期间复查。

开关关闭时整套保护关闭；单个阈值为 0 只关闭该项，不会连带关掉另一项。触发时通过运行
context 传给本次子进程组：perf / perf script 先 `SIGINT`，2 秒内未退出再 `SIGKILL`。
finished Event 使用独立的发送 context，因此熔断和取消之后终态仍有机会送达。

**开关打开时保证 Beat 自身不被 OOM，代价是采集以 `memory_limit` 失败。** 转换和 Go 解析都在
Beat 进程内完成，实测内存放大为最终 pprof 的 38~49 倍（perf）与未压缩大小的 12~19 倍
（Go），静态的转换上限挡不住，因此另加：

- 内存低水位不低于 Beat 的保留余量 `max(64MB, cgroup 内存上限的 20%)`，没有 cgroup 上限时取
  64MB；`min_mem_level` 更高时以它为准，配成 0 也不关闭这项保护。可用内存的口径不变。
- 启动时在低水位之上还要求 perf 128MB、Golang 32MB 的余量，不够直接拒绝。
- perf 转换在每秒复查之外，每新增 1024 个唯一样本同步复查一次，触发时与定时复查同样取消。
- Golang 在 `ParseData` 之前按剩余内存限长解压，并在进程级账本里占住
  `20MB + 20 × 未压缩大小`，放不下就拒绝；并发执行按账本累计。
- 本次拉起的 perf、perf script 与符号化工具 `oom_score_adj` 设为 1000，与 Beat 同处一个
  内存上限时内核优先杀它们。

开关关闭时以上都不生效，进程内转换的内存不受约束。

开关关闭还表示无条件采集。常驻 Beat 处在主配置 `resource_limit` 创建的 `collector-*`
cgroup 里时，本次采集拉起的 preflight 探测、perf record、perf script 和
`llvm-symbolizer` / `addr2line` 启动后会移到该 cgroup 的上一级，不再与 Beat 共用 CPU
和内存上限；Beat 进程以及在进程内完成的 profile 转换仍在上限内。cgroup v1/v2 都支持，
移不出时进程留在原 cgroup 继续执行并记告警。开关打开时子进程留在 cgroup 内。轻量 CLI
由 JOB 拉起，本来就不在 Beat 的 cgroup 里。

两种采集方式共用同一套策略，不按 `collection_method` 分叉：

- `min_mem_level` 判的是**宿主**可用内存，与采集方式无关，宿主吃紧就该停止采集。Golang
  采集的 HTTP 请求是 context 绑定的，熔断能中断在途 fetch——而 go_cpu 的绝大部分时间正好
  花在这个 fetch 上，所以这项保护对它是真实有效的。
- `max_disk_usage` 量的是本次运行目录的占用。Golang 采集正常不写文件，所以这项配额**恒不
  触发**；留着它比特例关掉更简单，也避免以后真加了落盘却忘了开保护。

`ParseData` 不接 context、不可中途打断，所以 Golang 采集只能在解析之前按预算拒绝。开关
关闭时只有响应体的 64 MiB 硬上限。

## 同 task 并发保护

锁域是单主机上的 `bk_tenant_id + bk_biz_id + task_id`，只对 `once_at/cron` 生效，立即
执行不参与。使用 `flock` 非阻塞文件锁而不是进程内布尔值，才能覆盖 reload、重启和多
Beat 进程重叠；持锁进程异常退出时由内核自动释放。抢锁失败不等待、不排队、不启动
采集器，直接上报 `status=failed` 与明确的忙碌原因。

调度器另有持久化标记，防止同一个计划 `scheduled_at` 在 reload/重启后重复派发；文件锁
只负责同一 task 不同轮次之间的互斥。

## 运行目录隔离

每次执行使用按执行键确定的独立运行目录 `<path.data>/active_profile/runs/<hash>`，采样文件、
转换临时文件和清理范围都限定其中，不同执行并行时互不覆盖。目录名只是本地路径，不进入业务
协议。进程启动时会清扫超过"执行预算 + 1 小时"的残留目录，回收上一个进程异常退出留下的
`perf.data`，以及给从未执行的 occurrence 写下的取消标记。

## 取消

立即执行与计划任务用同一套机制：取消命令按执行键在运行目录里写一个 `cancel.request`，
采集端每秒轮询，发现后取消本次执行的 context，由采集层回收自己拉起的 perf 子进程组，
最后上报 `status=cancelled` 的 finished。

**不使用信号。** 计划任务的采集只是常驻 Beat 里的一个 goroutine，向该进程发信号会影响
整个 Beat 和同进程的其他采集任务。取消命令只创建一个文件，因此不可能影响 Beat 本身；
watcher 的作用域就是这一轮执行，只做 `os.Stat`，随执行结束退出。代价是最多 1 秒延迟，
相对 15~600 秒的采样窗口可忽略。

取消标记按执行键隔离，因此只影响那一轮：cron 的下一轮照常触发，阻止未来触发是"停用任务"
这个独立动作。取消命令早于采集启动时标记已经落盘，采集启动会先检查一次并立即结束——属于
允许但不建议主动使用的行为。重复取消和目标已结束都是安全无操作。运行目录不可写时明确
报错，不会静默当成已取消。

`buildExecutionKey` 是执行键的唯一构造入口，采集端与取消端都经由它，两边不可能分叉。

## 运行目录

固定为 `<path.data>/active_profile`，由主配置的 `path.data` 推导：常驻 Beat 与 JOB 拉起的
轻量 CLI 必须得到同一个绝对路径，取消命令才能定位到采集进程。**它不是配置项**，平台不下发、
用户也不能覆盖；`path.data` 必须是绝对路径，否则 CLI 直接报错（相对路径会随两个进程各自的
工作目录解析成不同位置）。常驻 Beat 复用已加载的主配置，CLI 用 `cfgfile.Load` 自行读一次
——只做 YAML 解析，不创建 Beat 实例、不抢 PID 锁、不开本地存储；同一次读取里也拿到了上报
需要的 `output.bkpipe.endpoint`。

`keep_artifacts` 在两个入口语义一致：`false` 时转换成功后删 `perf.data`、执行结束删整个
运行目录；`true` 时保留 `perf.data` 与 `profile.pprof`。Golang 采集只有后者，且这是唯一能
拿到文件的方式——它的产物正常不落盘。产物是未压缩 protobuf，可直接用 pprof 工具打开。

## 超时

SaaS 不下发 `timeout/max_timeout`。Beat 按 `duration_sec + 300 秒` 推导 preflight、
record、perf script、pprof 转换和符号化的总执行截止时间。`go_heap` 的 `duration_sec`
为 0，总预算即那 300 秒，其中 HTTP 请求单独限 30 秒。

只有总预算耗尽才是 `timeout`。preflight 中每条 perf 命令单独限 10 秒，单条命令超时说明
perf 在本机启动过慢（典型是多个采集挤在 Beat 的 CPU 配额里），按 `failed` +
`perf_unavailable` 收敛，message 写明超时的步骤。

## perf 版本兼容

`perf script` 的字段集在 preflight 阶段探测，按顺序取第一个被本机 perf 接受的：

| 候选字段集 | perf 版本 | 第一列语义 |
| --- | --- | --- |
| `comm,pid,time,period,event,ip,sym,dso,dsoff` | ≥ 6.4 | raw ip，偏移来自 `dsoff` |
| `comm,pid,time,period,event,ip,sym,dso` | 3.x / 4.x+ | **非内核 DSO 本身就是文件偏移** |

`dsoff` 自 Linux 6.4 引入，低版本会在解析选项阶段直接拒绝。但引入 `dsoff` 的那组 patch
同时改变了 callchain 第一列的语义：6.4 之前对非内核帧打印的就是文件偏移。因此低版本
**不缺信息**，符号化质量与高版本一致，不是降级方案。

探测复用 preflight 已经录好的 probe perf.data，成本接近于零，并把"字段不被接受"这类失败
从采样窗口跑完之后提前到采样之前。两级都不被接受时在 preflight 阶段失败，错误信息里带上
perf 实际拒绝的字段。

地址换算统一为"有文件偏移就优先用它"：偏移与加载基址无关，两种语义下都正确。只有拿不到
偏移时才退回第一列，此时它只对非 PIE（`ET_EXEC`）有意义——这类镜像链接地址固定、不参与
ASLR，运行时地址本身等于 ELF 虚拟地址；`ET_DYN` 缺少偏移则无法反推加载基址。

## 符号化

`inline_symbolization: auto` 使用三级无损降级：优先 PATH 中的 `llvm-symbolizer
--inlines --output-style=JSON`，LLVM 不存在或协议失败时回退 `addr2line -i`，两者均不可用
时保留 `perf script` / ELF 普通符号。符号化是独立增强阶段，降级不会把整个执行改成失败。

单次任务最多增强 100,000 个唯一地址，LLVM 只启动一个进程，addr2line 最多同时覆盖 4 个
DSO，符号结果缓存估算上限 64 MiB，最多打开 128 个不超过 1 GiB 的 ELF 文件，每个地址最多
保留 64 层内联帧，所有进程共享 120 秒截止时间。外部工具只读本地 DSO，不连接 debuginfod；
运行环境采用固定最小白名单，不继承 Beat 的 Token、代理和动态加载器变量。自动发现会解析
符号链接，并拒绝非 root/Beat 账号所有或组/其他账号可写的命令及父目录。`off` 完全禁止
启动外部符号化工具。

符号化统计只进本机日志，不进 Event。`unknown=<帧数>/<总帧数>` 统计的是**帧出现次数**，
同一地址在多个样本里重复出现会重复计入，不等于样本占比；没有函数名的 location 在渲染侧
通常被丢弃而不是显示成 `[unknown]`，因此这个计数偏高不代表火焰图一定难看。定位要看紧随
其后的 `unknown_dso_top=[<dso>=<次数> ...]`（按次数降序，Top 20）：缺 symtab 的业务二进制、
JIT 匿名映射和容器内路径在那里能直接区分。

`symbolization=degraded` 只表示 `failures/skipped/unresolved` 中有非零项或有错误信息，
不代表符号名缺失——这些帧都会回落到 `perf script` 自己解出的符号，属于无损降级。

## pprof 转换上限

转换器不限制原始样本总数，重复调用栈只增加权重。为防止唯一聚合对象在两次资源检查之间
撑大 Beat，内部紧急边界为：每栈 1,024 帧、字符串 4,096 bytes、Mapping/DSO 8,192、
Location 1,000,000、Function 1,000,000、唯一 Sample 500,000、唯一 Sample 保留的 Location
引用合计 8,000,000。所有边界均在插入前检查，触发后停止 `perf script`，不写部分 pprof。
这些是不可由任务调大的节点保命上限，作用于合并后的整个主机产物。

## 已知边界

- 内核模块（`.ko`）的帧在 legacy 模式下会被当作用户态偏移处理。perf 把内核模块视为
  内核 DSO 并打印 raw ip，但按 DSO 名无法与普通文件区分。用户态 CPU profile 中内核
  模块占比极低，且其符号通常已由 perf 自己的 `sym` 字段解出，属于已知的次要偏差。
- `Location.Address` 的语义随 perf 版本不同（legacy 是文件偏移，dsoff 模式是 raw ip）。
  实际无害：pprof 的 Mapping 没有设 Start/Limit/Offset/BuildID，下游无法据此重新符号化。
- 目标进程在容器内时，DSO 路径是容器内路径，宿主上的符号化会失败；当前不做容器路径映射。
- 外部符号化工具解析畸形或恶意 ELF/DWARF 时，其自身瞬时内存无法由 Beat 的 Go 缓存上限
  约束。正式开启默认 `auto` 前需用目标发行版工具做畸形文件和大 DWARF 压测。
- 未增加 Beat 自身 RSS/堆的硬上限。开关关闭时，进程内转换过大仍可能让 Beat 被所在 cgroup
  OOM。

## 上报链路

Event 与 pprof 产物都经由**本机 GSE Agent** 上报，路由只靠平台注入的
`event_data_id` / `profile_data_id`。SaaS 不下发上报地址，也没有 Token。

两个入口的传输载体不同，载荷形态相同：

| 入口 | 载体 | 失败可见性 |
| --- | --- | --- |
| 常驻 Beat | Beat 自己的事件通道，由 `gse output` 发送 | 投递成功只表示已入队，实际发送结果看 Beat 的 gse 指标与日志 |
| 轻量 CLI | 自建 GSE 客户端 `SendSync` 直写 Agent socket | 写失败会直接收敛成主机终态与 CLI 退出码 |

CLI 的 Agent 地址取主配置的 `output.bkpipe.endpoint`——不猜 libgse 的内置默认路径，
Agent 安装目录随部署方式变化。连不上 Agent 时 CLI 在采集之前就失败，不浪费一次采样。

Event 载荷是 `{dataid, data:[{event_name, event:{content}, target, dimension, timestamp}]}`，
pprof 载荷是 `{dataid, data:[{data, app, biz_id, type, service_name}]}`。`app / service_name`
与两个 DataID 同源，由平台注入，用户不填，也不进入 Event `config`。

**`type` 取产物 pprof 的 `PeriodType.Type`**：perf 与 `go_cpu` 是 `cpu`，`go_heap` 是
`space`。它不是可选的展示字段——Profiling 结果表的清洗规则逐条读 `app / biz_id / type /
service_name` 四个键并各自落一列，缺 `type` 会让整条记录清洗不出来，表现为数据到了 Kafka
却始终不落库。采集端拿不到 `PeriodType` 时直接报错，不发一条注定入不了库的产物。

`type` 与 Event 的 `collection_type` 是两件事：前者是 pprof 自报的采样量纲，后者是下发的
采集类型，SaaS 选择 sample type 仍然读 Event。

**产物是未压缩的 protobuf**，采集端不做 gzip（`WriteUncompressed`），落盘与上报两处形态
一致。`data` 字段按 JSON 编码为 base64 字符串，与持续 Profiling 链路交给 GSE 的形态相同。
产物文件名是 `profile.pprof`，`keep_artifacts` 留下的那份可以直接用 pprof 工具打开，
不要去 `gunzip`。

单条 GSE 消息的体积上限由本机 Agent 决定，采集端不额外裁剪产物：超限只能表现为 Agent
侧拒绝，不会为了发得出去而丢样本。

终态运营指标 `bkm_active_profiling_execution` 走 Beat 内置 `gather_up_beat` 数据链路，
维度为 `bk_biz_id`、`bk_host_id`、`ip`、`status` 和仅计划任务携带的 `task_id`。只有常驻
Beat 上报：轻量 CLI 是一次性进程，不上报指标，只记日志——这是既定边界，不是待补齐项。
