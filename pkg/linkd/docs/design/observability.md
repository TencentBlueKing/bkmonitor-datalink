# Linkd 可观测性设计

## 边界

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

`linkd.store.operations` 对所有 Repository 调用统一计数，`not_found` 作为查询结果保留，便于分析查询命中率。
Lifecycle 的 `find_active` 和 `find_terminal_by_event` 会把 `store.ErrNotFound` 作为正常控制流处理；它不会
因此成为处理失败。DevTools 的“存储异常速率”排除 `succeeded` 和 `not_found`，其余冲突、非法请求和
底层失败仍按结果分类展示。

Cleaner 和 Lifecycle 在拆分部署时继续输出上述职责指标。Control Plane 当前提供独立 endpoint、Resource、
Go/process 指标，以及三个 Elasticsearch 管理任务和 Redis Stream 管理任务的职责指标；Leader Election
尚未实现，因此 `linkd_control_plane_task_active_ratio` 同一任务出现多个 owner 时属于部署错误。

`linkd.event_source_id` 只来自已校验配置或 StoredEvent。Kafka partition 只进入 received、settled
和 lane gauge，不进入 histogram。reason code 使用封闭枚举，未知值归入 `other`。tenant、实体 ID、
fingerprint、Mailbox ID、topic、group、错误全文和 payload 禁止进入指标属性。

`linkd.pipeline.attempt.duration` 和 `linkd.store.operation.duration` 在 0.75～2.5 秒区间使用加密的固定
分桶，以区分 Elasticsearch `refresh=wait_for` 附近的尾延迟。DevTools 同时展示由 `_sum / _count`
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
