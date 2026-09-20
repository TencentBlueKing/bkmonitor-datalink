# 数据流可观测性行业方案调研

## 调研信息

- 调研日期：2026-08-30
- 调研对象：OpenTelemetry Semantic Conventions 1.44.0、OpenTelemetry Collector、Apache
  Kafka 4.1、Apache Flink stable 文档、CloudEvents Distributed Tracing Extension
- 问题范围：高吞吐异步数据流如何划分 Trace、传播上下文，以及怎样组合指标、Trace 和日志
- 不在范围：具体观测后端选型、采样比例、告警阈值和 Linkd 生产容量估算

本调研只作为 [Linkd 可观测性与 Trace 设计](../design/observability.md) 的依据，不表示上述
系统或协议已经成为 Linkd 的运行依赖。

## 核心问题

Linkd 的处理链路不是一次同步请求，而是由 Kafka、队列、持久化状态、Worker、重试和补扫
组成的数据流。传统的“入口创建 Trace，所有后续操作都作为父子 Span 一直挂到结束”会遇到：

- 批量消费时一个 Span 只能有一个 parent，无法正确表达多条消息的多个生产上下文；
- topic 广播、队列扇出和重复投递会形成一对多关系；
- 事件可能在补扫或故障恢复后再次处理，Trace 生命周期可能跨越分钟甚至更久；
- 每条数据全量建 Span 的成本与业务吞吐线性增长；
- 一个 Alert 可以由多条 Event 持续推动，不能用一个长期 Trace 代替稳定业务身份。

因此需要判断 Trace 应表示“业务对象的一生”，还是“某次有界执行”。

## 参考案例

### OpenTelemetry Messaging：异步边界默认使用 Span Link

[OpenTelemetry Messaging Span 语义](https://opentelemetry.io/docs/specs/semconv/messaging/messaging-spans/)
把消息操作区分为 `create`、`send`、`receive`、`process` 和 `settle`。其中：

- `send` 使用 `PRODUCER`，`process` 使用 `CONSUMER`；
- 消息创建或发送上下文写入消息 carrier；
- 消费 Span 默认用 Span Link 关联消息创建上下文；
- 批量接收时每条消息可以提供各自的 Link，不要求选一条消息作为整个批次的 parent；
- 只有明确的单消息场景才可以把消息上下文直接作为 `process` Span 的 parent。

该规范说明 Span Link 是消息系统的默认关联方式，原因正是批处理、多个消费者以及消费发生在
另一个已有上下文中的情况。Kafka 专用语义还定义了
`messaging.system=kafka`、destination、partition、offset、consumer group、message ID 和 batch
message count 等属性；当前 messaging 语义仍处于 Development 状态，接入时需要锁定并记录所用
语义版本，不能假设字段名永远稳定。

对 Linkd 的启示：跨 Kafka、Redis 信号或内部异步任务时使用 Link；同进程直接调用仍使用普通
父子 Span。消息的重投可以产生新的消费执行，而不必伪装成原 Trace 的延长。

[OpenTelemetry Messaging Metrics](https://opentelemetry.io/docs/specs/semconv/messaging/messaging-metrics/)
同时定义了 `messaging.client.operation.duration`、`messaging.client.sent.messages`、
`messaging.client.consumed.messages` 和 `messaging.process.duration`，明确区分 broker client 操作
与应用处理耗时，并给出 duration histogram 的建议 bucket。Linkd 应复用这些标准指标，另外只
补充 Completion Tracker、进程内 retry、gap、pause/resume 和 ownership 等运行时特有状态。

### OpenTelemetry Collector：流量和队列健康以指标为主

[OpenTelemetry Collector 内部观测](https://opentelemetry.io/docs/collector/internal-telemetry/)
公开了 receiver accepted/refused、processor incoming/outgoing、exporter sent/send failed、发送
队列 size/capacity、enqueue failed、进程 CPU/内存等指标。

这些指标形成了一套适合数据流水线的基本闭环：

```text
入口接受/拒绝
  → 阶段输入/输出
  → 队列深度/容量
  → 出口成功/失败
```

Collector 文档还明确建议用队列 size/capacity 判断容量是否足够，并把持续的 refused、enqueue
failed 或 send failed 视为潜在数据丢失或下游故障信号。

对 Linkd 的启示：数据是否持续流动、是否积压和是否丢失，应由全量低基数指标回答；Trace 用于
解释某次慢处理或失败的内部路径，不负责提供全量计数。

### Apache Kafka：吞吐、lag 和客户端延迟是基础观测面

[Apache Kafka 4.1 Monitoring](https://kafka.apache.org/41/operations/monitoring/) 建议在客户端关注
消息/字节速率、请求速率/大小/耗时，消费侧关注各 partition 最大 lag 和最小 fetch rate；同时
提供 records consumed、records lag、request latency、requests in flight、commit latency 等
指标。

对 Linkd 的启示：Kafka broker/client 自带指标不能被应用自定义指标替代。Linkd 需要把 Kafka
lag、拉取/生产速率、提交失败、rebalance 与自身的处理耗时、队列等待、未处理 Event 年龄放在
同一看板中，才能区分“上游没数据”“Linkd 处理慢”和“下游无法提交”。

### Apache Flink：背压和端到端延迟比单条全量 Trace 更重要

[Apache Flink Metrics](https://nightlies.apache.org/flink/flink-docs-stable/docs/ops/metrics/) 提供
busy、idle、back-pressured time、mailbox queue size/latency、checkpoint duration/alignment、
records/bytes in/out 以及端到端延迟等指标；
[Back Pressure 文档](https://nightlies.apache.org/flink/flink-docs-release-1.17/docs/ops/monitoring/back_pressure/)
用 source 被背压解释下游 sink 消费更慢的传播关系。Flink 也提供 Trace reporter，但它没有用
“每条记录永久全量 Trace”取代上述运行指标。

对 Linkd 的启示：Worker 的 `inflight`、队列最老元素年龄、容量使用率和各阶段耗时必须能组合
定位背压。Flink checkpoint 指标不直接适用于 Linkd；Linkd 的恢复边界是幂等、CAS、pending
状态、重试和 reconciler，应为这些机制定义自己的指标。

### CloudEvents：事件信封可携带 W3C Trace Context

[CloudEvents Distributed Tracing Extension](https://github.com/cloudevents/spec/blob/main/cloudevents/extensions/distributed-tracing.md)
定义 `traceparent` 和可选 `tracestate`，并要求与协议自己的追踪头保持一致。多跳传输时，事件
扩展保存起始传输上下文，各跳的执行关系仍由对应协议和 OpenTelemetry 表达。

对 Linkd 的启示：Kafka header、队列消息元数据和未来 HTTP header 统一使用 W3C
`traceparent`/`tracestate` 即可，不需要为了追踪而把 Linkd 领域事件改造成 CloudEvent。外部
`baggage` 不能作为可信租户或权限来源。

## Trace 划分方案比较

| 方案 | 优点 | 主要问题 | 结论 |
|---|---|---|---|
| 一个 Alert 一个 Trace | 视觉上像完整生命周期 | 生命周期长、包含多 Event、并发和重开语义不清，Trace 无界 | 不采用 |
| 一个 Kafka batch 一个 Trace | Span 数较少 | 单条事件难定位，多生产上下文无法作为单一 parent，失败归因粗 | 只把 batch 当传输观测，不当业务 Trace |
| 一条 Event 端到端一个父子 Trace | 短链路容易理解 | 重投、扇出、补扫和长时间等待会破坏树形结构 | 只用于完全同步的短链路 |
| 每个阶段的每次处理尝试一个 Trace，通过 Link 关联 | 执行边界有界，适配批处理、重投、扇出和补扫 | 查询端需要支持 Link，并结合业务 ID 检索 | 采用 |

## 推荐结论

Linkd 采用“指标覆盖全量运行面，Trace 采样解释单次执行，稳定业务 ID 负责长期追溯”的组合：

1. 一个 Trace 表示一条数据在一个可执行阶段中的一次处理尝试，不表示 Alert 或 Event 的全部
   生命周期。
2. 同步函数和外部调用使用父子 Span；Kafka、队列、重投和扇出使用消息上下文加 Span Link。
3. `event_id`、`alert_id`、Kafka topic/partition/offset 是诊断关联键，不使用 `trace_id` 作为
   幂等键、业务身份或恢复依据。
4. 每次重投、补扫、归档补偿都允许创建新的 Trace，并用 `trigger`、`retry_count`、稳定原因码和
   业务 ID 描述本次尝试。
5. 吞吐、成功率、lag、队列深度、最老元素年龄、背压、重试和丢弃必须是全量指标；正常事件
   不做 100% Trace。
6. Trace context 只通过协议 header 或内部任务元数据传播，不进入 `AlertEvent`、`Alert`、
   `AlertLog` 的领域身份和一致性契约。

## 实现候选与风险

Go 侧可使用 OpenTelemetry Go API/SDK；它提供标准 Trace、Metric、propagation 和 OTLP 能力，
这些能力不属于 Go 标准库。若后续 Kafka 客户端选择 franz-go，可评估其
[`kotel`](https://pkg.go.dev/github.com/twmb/franz-go/plugin/kotel) hooks，但接入前必须核对：

- 使用的 OpenTelemetry messaging semantic convention 版本；
- 是否默认给每条消息建 Span，以及能否控制采样和属性；
- batch、process、commit/settle 和 rebalance 的 Span/指标语义；
- 是否会与 Linkd 手工阶段 Span 重复；
- 依赖版本、BSD-3-Clause 许可证、维护状态和 Go 1.26 兼容性。

当前仓库还没有 Kafka、Redis、真实存储或 Worker，实现尚不足以选择并验证具体 instrumentation
插件，因此本次不增加依赖。

## 未决问题

- 生产观测后端是否完整支持 Span Link 查询和 tail sampling；
- 正常流量 head sampling 的基线比例，以及错误、慢请求、重试、`rejected` 的保留策略；
- Kafka topic 是否继续按 alarm source 动态生成，这会直接影响 destination 属性基数；
- Redis 信号/数据队列最终使用的协议和消息元数据格式；
- `AlertPushTask` 或未来 outbox 是否需要保存可转发的技术 trace header；
- 每个环境允许的时序数、Trace 吞吐和保存期限。
