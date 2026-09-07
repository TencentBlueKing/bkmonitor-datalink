# Linkd 可观测性设计

## Lifecycle 与 ES 合批指标口径

Event Processor 使用 `linkd_pipeline_attempt_duration_seconds`；Signal Handler 单独使用
`linkd_messaging_handler_duration_seconds`。一次 Signal 可能处理多个 Event，两种耗时不能混算。
各阶段 P99 不相加；Cleaner 成功规范化数含重复投递，Lifecycle 完成速率取成功 Mailbox ACK，
两者不取最小值作为端到端吞吐，也不直接相减推算积压。

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
DevTools 单独展示同批写请求的服务端/客户端计时，并分别展示读写阶段均值、触发速率与 `rate(duration_seconds_sum)` 推导的平均执行占用；
平均占用不是峰值，不能单凭低均值排除短时槽位拥堵。

耗时桶覆盖 0.5ms～30s，使用固定 `linkd_metric_schema=2` 标记；DevTools 使用该口径。
均值按 sum/count 计算；分位数按合并后的 histogram 计算，不能相加实例 P99。
保留原 Repository 逻辑操作指标，用于与物理 Bulk 的执行/排队成本对照。

DevTools 的批次次数及写操作总量使用所选时间范围的 `increase` 估算，并在范围结束时取值；
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
  -> FinalHook Kafka Alert V1
```

消息生产与消费、Redis Mailbox Signal 之间使用独立 root span 与 Span Link；同进程同步子操作使用 child span。Broker 重投产生新的处理 trace，并通过稳定 `record_id/event_id/alert_id/cause_id` 关联。

Enricher 在创建 Alert 的 lifecycle attempt 内同步执行，不设计独立异步队列。内部 `CloseAlert` 是独立有界执行，cause 使用稳定 operation ID。

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
| `linkd.lifecycle.result.items`     | Event action、state、ProcessOutcome 和有限 reason code                    |
| `linkd.lifecycle.mailbox.*`        | peek/process/ack 与单次 drain 数量                                        |
| `linkd.lifecycle.lease.operations` | acquire、renew、release 结果                                              |
| `linkd.final_hook.*`               | FinalHook transport、结果和耗时                                           |
| `linkd.control_plane.task.*`       | 固定管理任务的 owner、执行结果、耗时和最近成功时间                        |
| `linkd.elasticsearch_alert_archiver.*` | Alert Archiver 批次与累计归档工作量                                   |
| `linkd.redis_stream.*`             | Stream 条目/内存、Group/Consumer、PEL、lag、年龄、软上限和安全裁剪        |
| `linkd.store.*`                    | Repository 操作、耗时、幂等重放和冲突                                     |
| `linkd.lifecycle.recent_alert_cache.*` | Recent Alert 命中、缺失、写入、解码失败和冲突修复                      |
| `linkd.enrich.*`                 | 新 Alert 同步丰富的总体结果、Processor、DataSource、在途调用和载荷大小 |

`linkd.store.operations` 对所有 Repository 调用统一计数，`not_found` 作为查询结果保留，便于分析查询命中率。
Lifecycle 的 `find_active` 和 `find_terminal_by_event` 会把 `store.ErrNotFound` 作为正常控制流处理；它不会
因此成为处理失败。DevTools 的“存储异常速率”排除 `succeeded` 和 `not_found`，其余冲突、非法请求和
底层失败仍按结果分类展示。

消费 Observer 在创建时缓存固定 stage、transport、EventSource、outcome 组合的不可变 AttributeSet，
并按有限 lane 缓存 partition AttributeSet。缓存只减少逐条指标上报的切片构造和属性排序，
不改变标签集合、基数或指标口径。

Cleaner 和 Lifecycle 在拆分部署时继续输出上述职责指标。Control Plane 当前提供独立 endpoint、Resource、
Go/process 指标，以及三个 Elasticsearch 管理任务和 Redis Stream 管理任务的职责指标；Leader Election
尚未实现，因此 `linkd_control_plane_task_active_ratio` 同一任务出现多个 owner 时属于部署错误。

`linkd.event_source_id` 只来自已校验配置或 StoredEvent。Kafka partition 只进入 received、settled
和 lane gauge，不进入 histogram。reason code 使用封闭枚举，未知值归入 `other`。tenant、实体 ID、
fingerprint、Mailbox ID、topic、group、错误全文和 payload 禁止进入指标属性。

`linkd.pipeline.attempt.duration` 和 `linkd.store.operation.duration` 保留 0.75～2.5 秒区间的加密固定
分桶，用于对比移除 Elasticsearch Event/Alert 数据路径 `refresh=wait_for` 前后的尾延迟。DevTools 同时展示由 `_sum / _count`
计算的平均耗时和 P95/P99；平均值不受 histogram 桶内插值影响，分位数仍是所选时间窗内的近似值。
Cleaner 页面使用 `linkd.pipeline.attempt.duration` 展示整体平均耗时、P95 和 P99，并使用
`linkd.cleaner.step.duration` 展示已埋点步骤的平均耗时、P95 和 P99。`receive` 当前没有独立步骤耗时，
只展示消息拉取速率，不以零值或整体耗时伪造步骤曲线。
处理状态摘要中的吞吐取各 `linkd.stage` 当前尝试速率的最小值，该数值仅作为聚合展示；平均耗时按
阶段求和，P99 阶段值之和只作保守诊断，不等同于严格的端到端 P99。Inflight 保留 `linkd.stage`
维度分别展示，缺失阶段保持未知，不补成零。Cleaner 和 Lifecycle 页面分别按 `clean` 与 `lifecycle`
过滤并展示本阶段吞吐、P99、平均耗时和在途消息，不使用另一阶段的数据补值。

Kafka assignment/offset/lag、Signal Group `lag + pending` 和 Mailbox List 扫描是 DevTools 直接读取的
当前快照。Cleaner 的 `linkd.cleaner.backpressure.*` 则最多每 3 秒按请求路径采样一次目标 Group；查询失败
和未知 lag 会记录为 fail-open，Group 缺失记录为暂停。启用
`control_plane.redis_stream` 后，控制面周期采集 Redis Stream 的 `XLEN`、`MEMORY USAGE`、Group、Consumer、
PEL、最大 lag、最老条目/Pending 年龄和软上限超量，并通过自身 `/metrics` 暴露。指标不携带 Stream 或
Group 名称，避免配置值形成高基数标签；未启用任务时不生成虚假零值。

`linkd.lifecycle.recent_alert_cache.operations` 使用固定 `linkd.operation` 和 `linkd.outcome` 记录 current、
ended、terminal 和 repair 操作。DevTools 展示操作速率与读取命中率；指标不携带租户、AlertID、EventID、
fingerprint、MailboxID 或缓存 key。

Enrich 使用 `linkd_enrich_attempts_total`、`linkd_enrich_attempt_duration_seconds`、
`linkd_enrich_inflight` 和 `linkd_enrich_payload_size_bytes` 记录新 Alert 丰富的最终状态、执行结果、并发占用与载荷大小；
使用 `linkd_enrich_processor_attempts_total`、`linkd_enrich_processor_duration_seconds` 和
`linkd_enrich_processor_diagnostics_total` 下钻到固定 Processor；使用
`linkd_enrich_datasource_operations_total` 和 `linkd_enrich_datasource_duration_seconds` 区分 Reader 的
`found/not_found/failed/canceled/invalid_response`。这些指标共享 Lifecycle/All-in-one 的 OTel Resource，
并保留 `linkd.pipeline.*{linkd.stage="lifecycle"}` 作为完整 Event 裁决口径。指标标签只允许已校验的
EventSource ID、固定 Processor、固定 DataSource/operation、状态和原因枚举；租户、实体 ID、查询字段、
错误全文和 payload 不进入指标。

四个固定任务使用 `linkd_control_plane_task_active_ratio`、`linkd_control_plane_task_runs_total`、
`linkd_control_plane_task_run_duration_seconds` 和 `linkd_control_plane_task_last_success_seconds`。`linkd_task`
只允许 `elasticsearch-schema-and-active-reconciler`、`elasticsearch-bucket-manager`、
`elasticsearch-alert-archiver`、`redis-stream-manager`；`linkd_outcome` 只允许 `succeeded/failed`。
最近成功时间是 Unix 秒；未执行成功时不生成虚假零值。

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

## 建议 Span

| Span                         | 边界                                                    | 主要结果                         |
| ---------------------------- | ------------------------------------------------------- | -------------------------------- |
| `linkd.clean.process`        | 单条 RawEventMessage 的纯清洗尝试                       | cleaner type、outcome、reason    |
| `linkd.clean.persist`        | 单 lane 连续前缀的 Event 批量创建、Mailbox 入队和确认   | batch size、outcome、reason      |
| `linkd.lifecycle.process`    | 单条 Event 到 accepted/suppressed/orphaned 或可恢复失败 | action、outcome、CAS conflict    |
| `linkd.enrich.process`       | 新 Alert 创建前的一次同步丰富                           | succeeded/partial/failed         |
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
