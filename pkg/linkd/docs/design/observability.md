# Linkd 可观测性设计

## 主流程日志规则

进程统一使用 `internal/logging.New` 创建的结构化 slog Logger。JSON 与 text 输出都默认携带
`source`（日志调用函数、文件、行号），无需开启 debug。源码位置是记录错误的边界；结合
`operation`、Processor 名称和 `error` 错误链定位具体失败功能，不能把边界行号当成底层依赖的报错行号。

| 字段 | 约定 |
| --- | --- |
| `module` | 固定模块名：`cleaner`、`lifecycle`、`enrich`、`consume`、`policy`、`taskdispatch`、`kac`、`controlplane`、`activeindex`、`dynamicconfig` |
| `operation` | 代码中固定的功能名，例如 `normalize`、`event_store`、`mailbox_enqueue`、`processor_match`、`processor_process`、`confirm`、`close_projection` |
| `error` | 有错误对象时必须保留错误链，使用 `%w` 逐层说明操作；不能只打印 `processing_failed` 或错误类型 |
| `reason_code` / `error_code` | 保留当前已有分类，供聚合检索；分类不能替代 `error` |
| `bk_tenant_id`、`event_source_id`、`event_source_version` | 已知时携带租户、来源和发布版本；清洗前租户可能尚未解析，不猜测填值 |
| `message_id`、`event_id`、`alert_id` | 携带本阶段已有身份，不为日志生成新业务 ID |
| `lane`、`position`、`mailbox_id`、`attempts` | 消费、入队、确认及重试的定位信息；单批确认记录 lane 和消息数量 |
| `processor`、`dependencies`、`severity`、`diagnostics`、`trace` | Enrich 的处理器名、失败依赖、等级、稳定诊断，以及自定义规则/操作 ID |
| `operation_id`、`cause_id`、`action_id`、`hook_name` | 主动关闭、输出通知、动作和 Hook 的已有调用身份 |

日志级别与记录位置：

- `ERROR`：来源任务退出、无法继续运行的故障；记录任务、来源、阶段和完整错误链。
- `WARN`：单条消息终结、依赖失败、部分失败、降级或需要重试；记录当前操作的实际后果。
- `INFO`：进程和来源任务启动、停止等低频状态变化。
- `DEBUG`：正常 Mailbox 消费完成等高频成功信息。正常处理器成功/不匹配不逐条打印日志。
- 底层返回错误并由最终决定丢弃、重试或降级的边界记录；不要在每层机械重复打印。
  被转换为 `partial/failed` 的错误必须在丢失原始 error 前保留。业务跳过或未匹配没有错误对象时，只记业务原因。
- 正常父 Context 取消不作为 Enrich/任务故障告警；超时、退出时额外清理失败仍需记录。
  panic 记录有限调用栈和固定分类，不打印 panic value。

`error` 字段在统一 Handler 中去除 URL 用户信息及查询参数、MySQL DSN 凭据、常见 password/token/
Authorization 值，并限制为 8KiB。该处理不修改业务错误对象。适配器仍禁止把请求头、完整 DSN、
Event 原文、告警正文或依赖响应正文拼入错误；任意未标记的敏感字符串不能依赖通用脱敏识别。
错误文本不写入指标标签，也不加入 Event/Alert 丰富快照；指标继续使用已有低基数分类。

### 覆盖位置

| 链路 | 错误定位 |
| --- | --- |
| Cleaner | 原始消息解码/映射拒绝、存储逐项失败、Mailbox 入队、批次重试、Broker 确认及失败诊断写入 |
| Lifecycle | Signal 解码、lease 获取/续租/释放、Mailbox 读取/确认、Event 读取/处理、Enrich 降级、内容生成、计划保存、Hook 和轻量通知发布 |
| Enrich | Match/Process 异常或 panic、结果与补丁应用失败、依赖读取降级；自定义规则错误保留 rule/operation ID |
| 策略与控制面 | 抑制、屏蔽、合并降级与后台执行、来源同步、Redis Stream 管理和活跃告警索引 |
| KAC / 主动关闭 | 投影、refresh、动作发送、队列确认；同步关闭的错误在转成 HTTP 502 前记录 |
| 消费与任务 | 处理器 panic/非法结果、重试耗尽、批次确认、来源初始化/运行/清理失败 |

Enrich 每个失败 Processor / 等级记录一条诊断。依赖错误在本次 Scope 中保留，到 Processor 边界合并记录，
即使 Processor 返回 `partial` 和 nil error，也不会丢失具体的依赖错误。Scope 不跨 Event 共享，正常路径
不创建错误集合，成功处理器不输出日志。

示例（字段值为示意，行号以实际输出为准）：

```json
{
  "level": "WARN",
  "msg": "enrich processor failed",
  "module": "enrich",
  "operation": "processor_process",
  "processor": "resource",
  "bk_tenant_id": "tenant-a",
  "event_source_id": "built_in_bk",
  "event_id": "event-123",
  "severity": "critical",
  "status": "partial",
  "error": "strategy_publication: read strategy publication: connection refused",
  "source": {"function": "linkd/internal/enrich.(*Chain).logProcessorFailure", "file": "linkd/internal/enrich/logging.go", "line": 60}
}
```

排查先按 `event_id` 或 `message_id` 查同条事件；按 `module=enrich` 和 `processor=resource` 聚合查同类故障。
检查 `operation`、`error`、`source`，再看 `reason_code` / `status` 判断是终结、重试还是局部降级。
上例的 `partial` 只描述丰富结果，不代表整个生命周期失败。

### Lifecycle 失败结果

`discard lifecycle event` 表示当前 Event 处理终结，随后确认 Mailbox 队首，继续处理后续 Event。
`reason_code=processing_failed` 是普通处理失败的分类，不是底层错误原因；明确临时错误最多执行三次，
耗尽时为 `retry_exhausted`。`stage` 与 `operation` 区分 `event_read`、`event_identity` 和 `event_process`，
生命周期处理内部的具体操作由错误链说明。

失败时可能已经产生部分副作用，需结合 Event 保存的执行计划和关联 Alert 判断影响。
若失败终态保存失败，`persist discarded event diagnostic failed` 另外记录保存错误，不阻塞后续 Event。

## Lifecycle 与 ES 合批指标口径

Event Processor 使用 `linkd_pipeline_attempt_duration_seconds`；Signal Handler 单独使用
`linkd_messaging_handler_duration_seconds`。一次 Signal 可能处理多个 Event，两种耗时不能混算。
各阶段 P99 不相加；Cleaner 成功规范化数含重复投递，Lifecycle 完成速率取成功 Mailbox ACK，
两者不取最小值作为端到端吞吐，也不直接相减推算积压。

Lifecycle 的裁决、Event 尝试/耗时和 Mailbox process 指标将纯动作背压单列为 `deferred`；
`ErrBusy`、`ErrCapacity` 及纯等待错误链不再计入 `failed`。混合存储错误、取消、非法输入仍计为失败。
这些计数的单位是处理尝试，同一 Event 延后后再次执行会再次计数，不能解释为不同消息的最终失败数量。

Event 内部分段使用 `linkd_lifecycle_event_phase_duration_seconds`，每次尝试对固定环节
记录独占耗时，嵌套调用从父环节中扣除，未执行的环节也记录零样本；各环节具有相同的尝试分母，
因此其均值可以相加。该计时器仅属于调用内的 Processor 副本，不跨 fingerprint 共享可变状态；
阶段总耗时、执行耗时、等待耗时及子步骤统一携带 `linkd.event_source_id`，取自本次输入 Event，
失败、重放与取消路径同样保留来源。Prometheus 导出为 `linkd_event_source_id`；查询可按来源精确过滤，
分子、分母与子步骤覆盖率必须使用同一条件。未过滤时仍按尝试次数跨来源/Worker 加权，旧的无来源
标签样本仅参与汇总，不能追溯拆分。不增加租户、Event ID、fingerprint 或来源版本维度。
使用单调时间计时，不依赖业务时钟，也不改变失败、重试和持久化语义。
该指标与 ES 物理批次指标的采样单位不同，不相加。指标展示由 Kingeye Admin 提供，口径以本节为准。

以下指标统一以 `linkd_elasticsearch_write_batch_` 为前缀，
`linkd_batch_kind=read|write` 区分 `_mget` 与 `_bulk`。跨来源请求不添加租户、Event ID 或来源标签。

| 后缀 | 类型 | 语义 |
| --- | --- | --- |
| `batches_total` | Counter | 已执行的请求尝试次数；结果分 succeeded / partial_failed / failed |
| `items_total` | Counter | 子操作逐项结果；succeeded / failed，failed 包含结果未知 |
| `operations` | Histogram | 每批子操作数；count 是请求数，sum 是提交的操作数 |
| `size_bytes` | Histogram | 编码后的物理请求字节数 |
| `queue_duration_seconds` | Histogram | 每批首项从入队到开始执行，包含聚合与执行槽位等待 |
| `duration_seconds` | Histogram | 请求执行与响应解码时间，包含网络，不包含入队等待，不等同于 ES took |
| `phase_duration_seconds` | Histogram | 按 `linkd_batch_phase` 拆分诊断阶段，读写分别统计，无样本不填零 |
| `triggers_total` | Counter | 按 `linkd_batch_trigger` 统计聚合提交原因，不等同于物理请求次数 |

诊断阶段的采样单位不同，均值不能直接相加：`admission` 每个已接收调用记录调用总量槽位等待；
`collect` 每个聚合组记录首项取得调用槽位至收集完成（含请求预处理、读元信息编码与队列等待）；
`worker_slot` 每个聚合组记录共享执行槽位等待；
`operation_queue` 每个实际发送的子操作记录从取得调用槽位至请求开始的等待（含请求解析、编码与前序切片）；
`encode` 每个物理请求记录请求体组装（不含此前为切片预算做的预编码；读写均复用已编码操作）；
`connection`、`request_write`、`first_byte` 分别为连接获取、取得连接至请求写完、写完至响应首字节；
`response_body` 为响应体读取及关闭，`response_decode` 为顶层 JSON 解码，不含逐项结果分发。
`response_items` 记录物理请求逐项结果的解析和映射，不包含随后向调用方包装并交付响应。
`server_took` 使用 Bulk 响应提供的毫秒计时，`paired_execution` 记录相同请求的客户端执行区间；
只对有合法非负 `took` 且响应结构完整的写响应同时采样，包括逐项失败。缺失、非法值和读取请求不补零，
不能拿两者与包含其他样本的全量批次均值相减。`took` 不是纯写盘时间，两端差值也不等于网络耗时。
Go 运行时另采集 runnable goroutine 的调度延迟分布及 GC CPU 时间估算；后者不是停顿时长，
也不能直接除以操作系统进程 CPU 时间计算占比。它只适合与同口径 Go CPU classes 指标比较。
HTTP 阶段只记录完成的回调，同一逻辑请求重连时累计完成阶段；连接获取包含建连，首字节包含网络、
ES 处理和本机调度，不能直接当作 ES 服务端 took。诊断阶段不使用旧耗时的 schema 标签。

触发原因是 `operations`（数量上限）、`bytes`（写侧原始字节预算，读侧编码后字节预算）、
`deadline`（首项期限）、`ready`（零等待模式下只合并已就绪项）。
读侧复用写侧的触发逻辑，默认派生最长 10ms 的短窗口；达到数量或字节阈值仍立即发送。
编码后的字节限制可能再切片，取消也可能使整组无需发送。
Console 单独展示同批写请求的服务端/客户端计时，并分别展示读写阶段均值、触发速率与 `rate(duration_seconds_sum)` 推导的平均执行占用；
平均占用不是峰值，不能单凭低均值排除短时槽位拥堵。

耗时桶覆盖 0.5ms～30s，使用固定 `linkd_metric_schema=2` 标记；Console 使用该口径。
均值按 sum/count 计算；分位数按合并后的 histogram 计算，不能相加实例 P99。
保留原 Repository 逻辑操作指标，用于与物理 Bulk 的执行/排队成本对照。

Console 的批次次数及写操作总量使用所选时间范围的 `increase` 估算，并在范围结束时取值；
速率、均值、分位数使用独立的滚动计算窗口。成功表示收到成功逐项结果，不表示搜索已刷新可见；
失败/未知不表示写入必定未生效。未接入与无样本不当作零，空闲窗口中的均值和分位数不填零。
只读配置展示与实例运行遥测区分，不能仅凭 YAML 推导值认定所有实例已应用该配置。

## 边界

Cleaner `mailbox_enqueue` 与 `mailbox_signal` 的步骤耗时现在按一次逻辑入队批次记录，
items 仍是实际 Event 数。一个批次存在多个结果类型时分别记录该批耗时，不能跨 outcome 相加；
不再将这两个步骤的平均耗时解释为单条 Redis 往返。失败计数包含未执行或结果未知的未完成项。

可观测信号用于诊断运行状态，不能替代 Event、EventProcessing、Alert 和 AlertLog 等领域事实，也不能参与 event ID、fingerprint、CAS、Kafka offset 或业务幂等。

- Metric 覆盖吞吐、结果、延迟、积压、背压、重试和恢复，属性必须低基数。
- Trace 表示一条消息在一个有界阶段的一次处理尝试，不跨越 Alert 的完整生命周期。
- Log 记录错误、冲突、重试和异常状态；正常成功路径不逐条写 INFO。
- AlertLog 保存业务流水，保留策略不得依赖 Trace 采样结果。

## 数据流

```text
RawEventMessage MQ
  -> cleaner attempt
  -> lane 内 Event + EventProcessing 批量持久化
  -> Redis Mailbox + lifecycle signal
  -> lifecycle attempt
  -> Alert / AlertLog
  -> EventSource.hooks（Kafka Alert V1 / KAC Alarm / Redis 活跃策略索引）
```

消息生产与消费、Redis Mailbox Signal 之间使用独立 root span 与 Span Link；同进程同步子操作使用 child span。Broker 重投产生新的处理 trace，并通过稳定 `record_id/event_id/alert_id/cause_id` 关联。

Enricher 在创建 Alert 的 lifecycle attempt 内同步执行，不设计独立异步队列。内部 `CloseAlert` 是独立有界执行，cause 使用稳定 operation ID。

## 指标目录与注册约束

业务指标的实际创建和目录登记共用 `internal/telemetry` 中的 `instrumentRegistry`。
`metrics.go`、`dispatch.go`、`write_batch.go` 的每个声明同时提供中文短名称、模块、用途和维度；
单位、HELP 描述与 instrument 类型从传给 OTel 的同一组选项和构造器读取，不维护独立指标名清单。
注册时检查必填元数据、分类、维度词表，以及 OTel 名和导出名冲突。

`MetricCatalog()` 使用 no-op Meter 执行同一套注册流程，再从与运行时相同的 Go/process collector
发现原生指标，按名称返回只读目录。ES 批次 instrument 也在初始化时统一注册；原有“启用观察器后
上报零值”的行为保留。控制面在创建 API Handler 时构造一次目录，请求阶段不触发业务 I/O、采集或
配置更新。进程重启至包含新指标的二进制后，API 与 Console 自动包含新定义，不需要手工维护页面。

新增业务指标必须在现有注册流程中使用带元数据的构造器，例如：

```go
meter.Int64Counter(
    "linkd.enrich.attempts",
    describeMetric("事件丰富尝试结果", "enrich", "throughput",
        "linkd.event_source_id", "linkd.status", "linkd.outcome", "linkd.chain_kind"),
    metric.WithUnit("{attempt}"),
    metric.WithDescription("Event 同步丰富尝试结果"),
)
```

模块、用途使用目录中已有分类；新增维度同时在 `metric_dimensions.go` 说明语义和可选条件，仍须
满足低基数约束。构造器不嵌入原始 Meter，避免新增指标跳过元数据。结构测试遍历生产代码中的
instrument 声明，检查没有绕过登记、没有未进入目录的延迟注册路径；真实 scrape 测试对照目录
检查名称、类型、HELP 和实际标签，既有直方图桶与数值行为继续由原测试验证。

目录与 exporter 共用现有 Prometheus 名称翻译策略，使用 exporter 已依赖的 `otlptranslator`
实现，不重新实现单位后缀和标签转义规则。该库由间接依赖改为直接使用，没有新增依赖版本。
Go/process 自动发现先保证新增项可见，现有指标有中文短名称，新出现的未知原生指标暂用通用中文
标题并保留上游 HELP；后续可完善中文说明。Resource 和抓取标签与业务维度分开说明，不伪造不存在
的 tenant、Alert ID 标签。

运行观测页面由 Kingeye Admin 提供。

## 已实现 Metric

Prometheus exporter 使用单一 `telemetry.metrics.prometheus.listen_address`。每个常驻进程都初始化独立
telemetry runtime，Resource 的 `linkd.role` 区分 cleaner、lifecycle、control-plane 和 all-in-one。

现场性能诊断可显式启用独立的 `telemetry.profiling` pprof 服务。该服务默认关闭并强制绑定
IP 回环地址，不与 Prometheus 端口共用，也不应通过反向代理或容器端口暴露到业务网络。
`block_profile_rate` 和 `mutex_profile_fraction` 会增加采样成本，只在有界诊断窗口开启；
进程退出时停止服务并复位运行时采样率。容量结论需要比较正常段与降速段的 CPU、allocs、
block、mutex 和 execution trace，不能用单张 CPU 火焰图代替因果分析。

| 指标族                             | 作用                                                                      |
| ---------------------------------- | ------------------------------------------------------------------------- |
| `linkd.messaging.*`                | receive、redelivery、handler、inflight、retry、settlement、lane、shutdown |
| `linkd.cleaner.step.*`             | transform、Event store、Mailbox enqueue/signal、source ACK                |
| `linkd.cleaner.flow.active`        | 当前进程实际运行的 EventSource Flow                                       |
| `linkd.cleaner.backpressure.*`     | Signal 积压检查、近似未完成量、暂停状态和暂停/恢复转换                     |
| `linkd.lifecycle.result.items`     | Event 判定的动作摘要（多动作使用 mixed）、state、ProcessOutcome 和有限 reason code                    |
| `linkd.lifecycle.mailbox.*`        | peek/process/ack 与单次 drain 数量                                        |
| `linkd.lifecycle.lease.operations` | acquire、renew、release 结果                                              |
| `linkd.final_hook.*`               | FinalHook transport、结果和耗时                                           |
| `linkd.control_plane.task.*`       | 固定管理任务的 owner、执行结果、耗时和最近成功时间                        |
| `linkd.elasticsearch_alert_archiver.*` | Alert Archiver 批次与累计归档工作量                                   |
| `linkd.redis_stream.*`             | Stream 条目/内存、Group/Consumer、PEL、lag、年龄、软上限和安全裁剪        |
| `linkd.store.*`                    | Repository 操作、耗时、幂等重放和冲突                                     |
| `linkd.lifecycle.recent_alert_cache.*` | Recent Alert 命中、缺失、写入、解码失败和冲突修复                      |
| `linkd.enrich.*`                 | Event 同步丰富的总体结果、Processor、DataSource、在途调用和载荷大小 |

`linkd.store.operations` 对所有 Repository 调用统一计数，`not_found` 作为查询结果保留，便于分析查询命中率。
Lifecycle 的 `find_active` 和 `find_terminal_by_event` 会把 `store.ErrNotFound` 作为正常控制流处理；它不会
因此成为处理失败。Console 的“存储异常速率”排除 `succeeded` 和 `not_found`，其余冲突、非法请求和
底层失败仍按结果分类展示。

消费 Observer 在创建时缓存固定 stage、transport、EventSource、outcome 组合的不可变 AttributeSet，
并按有限 lane 缓存 partition AttributeSet。缓存只减少逐条指标上报的切片构造和属性排序，
不改变标签集合、基数或指标口径。

Cleaner 和 Lifecycle 在拆分部署时继续输出上述职责指标。Control Plane 当前提供独立 endpoint、Resource、
Go/process 指标，以及后台管理任务的职责指标。调度中心通过 Redis 租约保持独占；其他管理任务没有
独立选主，`linkd_control_plane_task_active_ratio` 同一任务出现多个 owner 时仍需检查重复部署。

`linkd.event_source_id` 只来自已校验配置或 StoredEvent。Kafka partition 只进入 received、settled
和 lane gauge，不进入 histogram。reason code 使用封闭枚举，未知值归入 `other`。tenant、实体 ID、
fingerprint、Mailbox ID、topic、group、错误全文和 payload 禁止进入指标属性。

`linkd.pipeline.attempt.duration` 和 `linkd.store.operation.duration` 保留 0.75～2.5 秒区间的加密固定
分桶，用于对比移除 Elasticsearch Event/Alert 数据路径 `refresh=wait_for` 前后的尾延迟。Console 同时展示由 `_sum / _count`
计算的平均耗时和 P95/P99；平均值不受 histogram 桶内插值影响，分位数仍是所选时间窗内的近似值。
Cleaner 页面使用 `linkd.pipeline.attempt.duration` 展示整体平均耗时、P95 和 P99，并使用
`linkd.cleaner.step.duration` 展示已埋点步骤的平均耗时、P95 和 P99。`receive` 当前没有独立步骤耗时，
只展示消息拉取速率，不以零值或整体耗时伪造步骤曲线。
处理状态摘要中的吞吐取各 `linkd.stage` 当前尝试速率的最小值，该数值仅作为聚合展示；平均耗时按
阶段求和，P99 阶段值之和只作保守诊断，不等同于严格的端到端 P99。Inflight 保留 `linkd.stage`
维度分别展示，缺失阶段保持未知，不补成零。Cleaner 和 Lifecycle 页面分别按 `clean` 与 `lifecycle`
过滤并展示本阶段吞吐、P99、平均耗时和在途消息，不使用另一阶段的数据补值。

Kafka assignment/offset/lag、Signal Group `lag + pending` 和 Mailbox List 扫描是 Console 直接读取的
当前快照。Cleaner 的 `linkd.cleaner.backpressure.*` 则最多每 3 秒按请求路径采样一次目标 Group；查询失败
和未知 lag 会记录为 fail-open，Group 缺失记录为暂停。配置 Redis 与 Lifecycle 后，
`control_plane.redis_stream` 默认启用，控制面周期采集 Redis Stream 的 `XLEN`、`MEMORY USAGE`、Group、Consumer、
PEL、最大 lag、最老条目/Pending 年龄和软上限超量，并通过自身 `/metrics` 暴露。指标不携带 Stream 或
Group 名称，避免配置值形成高基数标签；显式设置 `enabled: false` 关闭任务时不生成虚假零值。

`linkd.lifecycle.recent_alert_cache.operations` 使用固定 `linkd.operation` 和 `linkd.outcome` 记录 current、
ended、terminal 和 repair 操作。Console 展示操作速率与读取命中率；指标不携带租户、AlertID、EventID、
fingerprint、MailboxID 或缓存 key。

Enrich 使用 `linkd_enrich_attempts_total`、`linkd_enrich_attempt_duration_seconds`、
`linkd_enrich_inflight` 和 `linkd_enrich_payload_size_bytes` 记录 Event 丰富的最终状态、执行结果、并发占用与载荷大小；
使用 `linkd_enrich_processor_attempts_total`、`linkd_enrich_processor_duration_seconds` 和
`linkd_enrich_processor_diagnostics_total` 下钻到固定 Processor；使用
`linkd_enrich_datasource_operations_total` 和 `linkd_enrich_datasource_duration_seconds` 区分 Reader 的
`found/not_found/failed/canceled/invalid_response`。这些指标共享 Lifecycle/All-in-one 的 OTel Resource，
并保留 `linkd.pipeline.*{linkd.stage="lifecycle"}` 作为完整 Event 裁决口径。指标标签只允许已校验的
EventSource ID、固定 Processor、固定 DataSource/operation、状态和原因枚举；租户、实体 ID、查询字段、
错误全文和 payload 不进入指标。

丰富指标按处理步骤记录，不是新建 Alert 数量：尚未完成丰富的触发、恢复、关闭 Event 都会生成逐等级结果，
显式启用的活跃快照复用步骤使用 Processor `outcome=reused`，保留 `status=succeeded`，耗时记录本次恢复结果的时间；
其余步骤仍正常执行，DataSource 只记录实际发生的查询。
已冻结结果的重投直接复用；多个 evaluation 的结果保存在同一 Event 信封。该结果指标不证明后续
丰富 CAS、策略裁决或 Alert 持久化已经成功，完整处理仍看 Lifecycle 与 Repository 指标。

固定任务使用 `linkd_control_plane_task_active_ratio`、`linkd_control_plane_task_runs_total`、
`linkd_control_plane_task_run_duration_seconds` 和 `linkd_control_plane_task_last_success_seconds`。`linkd_task`
使用 [ControlPlaneTask](../../internal/telemetry/control_plane.go) 的固定枚举，包括存储维护、调度、
策略发布/复查、合并裁决/关系和可靠投影/动作循环；不接受任意任务名或业务身份。
`linkd_outcome` 只允许 `succeeded/failed`。
最近成功时间是 Unix 秒；未执行成功时不生成虚假零值。

当前任务状态由 `internal/controlplane/taskstate` 显式注入装配和管理 API，使用有锁、有界的内存快照；
最多每任务 64 个子流程，不保存原始错误、凭据或业务载荷。API 返回单进程 owner、启动和采样时间，
启用配置、生命周期、完整轮次和子流程结果彼此独立。Prometheus 负责跨重启的历史趋势，不作为当前 owner
唯一来源。运行状态由 Kingeye Admin 展示。

轮次口径：调度为一次 tick；Provider 为一次 Pull 与完整 Apply；动态配置为读取到安装完整轮次；
策略索引主任务为目标发现/初始化轮次，目标子项保留逐策略刷新数量与失败数；Redis 主任务为来源分页扫描，
其历史指标沿用逐 Stream reconcile 口径。管理 API 只展示服务生命周期，不伪造周期执行计数。


Alert Archiver 通过 `linkd_elasticsearch_alert_archiver_scanned_alerts_total`、
`linkd_elasticsearch_alert_archiver_archived_alerts_total` 和
`linkd_elasticsearch_alert_archiver_failed_alerts_total` 记录累计扫描、完成和隔离失败量；
`last_batch_scanned`、`last_batch_items`、`last_batch_failed` 三个 gauge 记录最近一个非空批次结果，积压清空后的
空扫描不会把它们覆盖为零。批内存在失败时任务轮次
outcome 为 `failed`，但成功项仍计入完成量，失败项保留 Active 并在后续扫描重试，不会因此终止 Archiver 或数据面。

Prometheus endpoint 暴露以下 Redis Stream 指标：

- 当前状态：`linkd_redis_stream_exists`、`linkd_redis_stream_expected_group_present`、
  `linkd_redis_stream_entries`、`linkd_redis_stream_entries_added`、`linkd_redis_stream_memory_bytes`；
- 消费状态：`linkd_redis_stream_consumer_groups`、`linkd_redis_stream_consumers`、
  `linkd_redis_stream_pending`、`linkd_redis_stream_consumer_group_max_lag`、
  `linkd_redis_stream_oldest_entry_age_seconds`、`linkd_redis_stream_oldest_pending_age_seconds`；
- 容量与任务：`linkd_redis_stream_max_entries`、`linkd_redis_stream_entries_above_max`、
  `linkd_redis_stream_reconcile_operations_total`、`linkd_redis_stream_reconcile_duration_seconds`、
  `linkd_redis_stream_trimmed_entries_total`、`linkd_redis_stream_trim_required_ratio`、
  `linkd_redis_stream_trim_safe_ratio`、`linkd_redis_stream_trim_last_entries`。

`consumer_group_max_lag=-1` 表示 Redis 无法计算 lag。`entries_above_max>0` 不等于裁剪故障：如果超出的
条目尚未被所有 Group 确认，控制面必须继续保留；应结合 `pending`、`max_lag` 和最老 Pending 年龄告警。
`trim_last_entries` 记录整轮多批裁剪的累计量，不是最后一条 `XTRIM` 命令的返回值。

## KAC 输出观测

当前使用 `linkd.kac.items`（阶段成功/失败条数）、`linkd.kac.duration`（耗时）、
`linkd.kac.queue.messages`（含 PEL 的积压）与 `linkd.kac.queue.oldest_age`（最早等待）。
阶段标签包括 enqueue/projection/action/refresh/read/decode/settle/action_buffer；业务身份仅写日志。
旧 action/projection runner、最近页和未确认回执指标已移除。Console `/kac-output` 展示进程指标，
不带租户或 Alert 条件；缺失时序不解释为零。详见[动作 V3](../reference/contracts/kac-action-delivery-v3.md#console-运行观测)。

## 建议 Span

| Span                         | 边界                                                    | 主要结果                         |
| ---------------------------- | ------------------------------------------------------- | -------------------------------- |
| `linkd.clean.process`        | 单条 RawEventMessage 的纯清洗尝试                       | cleaner type、outcome、reason    |
| `linkd.clean.persist`        | 单 lane 连续前缀的 Event 批量创建、Mailbox 入队和确认   | batch size、outcome、reason      |
| `linkd.lifecycle.process`    | 单条 Event 到 accepted/suppressed/orphaned 或可恢复失败 | action、outcome、CAS conflict    |
| `linkd.enrich.process`       | Event 策略裁决前的一次同步丰富                           | succeeded/partial/failed         |
| `linkd.final_hook.process`   | Alert change 到 Kafka ACK 或 hook 失败流水              | cause type、destination、outcome |
| `linkd.direct_close.process` | CloseAlert command 到 CAS、流水和 FinalHook             | actor type、outcome              |

Span、Metric 和默认日志不得包含完整 payload、凭据、未经脱敏的 `source_raw_data/extra_data/enrich`、租户或 Event/Alert ID 等高基数值。业务 ID 只在按访问控制保存的 Trace/结构化诊断中按需记录，不进入指标标签。

## Context 与失败语义

当前同步和异步调用统一传播 `context.Context`。Trace 接入后使用 W3C `traceparent`，外部 baggage
默认不传播。遥测后端失败不得改变领域结果或 offset/ACK；无法确定业务持久化和输出结果的错误仍
必须按处理协议重试或阻塞，不能因为“已经打点”而确认消息。

当前已接入 OpenTelemetry Metric API 与 Prometheus exporter；Trace、exemplar 和 OTLP exporter 尚未实现，相关 span 名是落地约束而非已运行事实。

公共任务调度的中心与 worker 指标见 [任务调度可观测性](task-scheduling-observability.md)，
包含心跳、授权、交接、元数据和副本缺额；它们与 Lifecycle fingerprint lease 指标分别统计。

## 策略匹配、窗口与统计采样

2026-10-08新增以下正式运行观察；模拟与只读预览不计入。

| OTel 名称 | 类型 | 口径 |
| --- | --- | --- |
| `linkd.policy.match.duration` | 秒 Histogram | 单次条件/目标求值耗时，包括只读资源查询，不含 Redis 执行和统计写入；按 suppression/shield/merge 及 matched/not_matched/unavailable 分类 |
| `linkd.policy.check.delay` | 秒 Histogram | 屏蔽 next_check_at 或合并 deadline 到实际检查时刻的非负差；复查重复观察，不等于解除/合并完成延迟 |
| `linkd.policy.statistics.samples` | Counter | 逐策略 Redis 采样 recorded/failed/dropped；失败不改变业务处理结果 |
| `linkd.policy.state.initializations` | Counter | clip/aggregation/merge 的 created，及可确认的防抖残缺 repaired；重放不再计创建 |

完整缓存丢失与正常首次创建无法区分，均记录 created，不臆测故障恢复次数。
所有 Prometheus 标签仅有有限类别；租户、策略和运行对象身份不作为标签。
逐策略统计使用 Redis 有界小时桶，通过管理 API 查询，口径及预算以
[执行观察 API](../reference/contracts/policy-api.md#逐策略执行观察)为准。
