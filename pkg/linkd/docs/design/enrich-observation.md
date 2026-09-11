# Enrich Observation 设计

状态：已实现  
适用代码：`internal/lifecycle/enrich`、`internal/lifecycle`、`internal/telemetry`、`internal/lifecycle/process`  
相关总则：[可观测性设计](observability.md)  
实施记录：[Enrich 可观测性实施计划](enrich-observability-implementation-plan.md)

## 1. 定位

Enrich Observation 是 Lifecycle 内部同步丰富阶段的分层观测设计。它在现有 Lifecycle 阶段指标下提供总体、Processor 和 DataSource 三层归因：

```text
linkd.pipeline.attempt{linkd.stage="lifecycle"}
├── Repository：linkd.store.*
├── Enrich：linkd.enrich.*
│   ├── 总体：attempt / duration / inflight / payload size
│   ├── Processor：attempt / duration / diagnostic
│   └── DataSource：operation / duration
├── Alert 写入：linkd.store.*
└── FinalHook：linkd.final_hook.*
```

`linkd.pipeline.*{linkd.stage="lifecycle"}` 继续表示单个 Event 的完整 Lifecycle 裁决。`linkd.enrich.*` 只表示创建新 Alert 时同步执行的丰富阶段。更新、恢复、关闭、抑制以及已处理 Event 的短路路径不会重新执行 Enrich，也不会产生 Enrich attempt。

Observation 只产生诊断信号，不参与 Alert 状态、Enrich payload、CAS、Mailbox、Kafka ACK、重试和 DataSource 返回值的决策。

## 2. 三层 Observation

### 2.1 Lifecycle 总体层

接口定义在：

```text
internal/lifecycle/enrich_observer.go
```

核心类型：

```go
type EnrichObservation struct {
    EventSourceID string
    Status        domain.EnrichStatus
    Outcome       string
    ChainKind     EnrichChainKind
    Duration      time.Duration
    PayloadBytes  int64
}

type EnrichObserver interface {
    Started(ctx context.Context, eventSourceID string)
    Finished(ctx context.Context, observation EnrichObservation)
}
```

该层放在 `lifecycle.Processor.enrichNewAlert()`，因为这里掌握最终持久化语义：

```text
基础 Alert Normalize
→ 调用 AlertEnricher
→ 捕获 error / panic
→ 校验 enrich_status
→ Normalize 和校验 payload
→ 必要时生成标准 failed payload
→ 写入 Alert.EnrichStatus / Alert.Enrich
```

因此总体 Observation 的 `Status` 和 `PayloadBytes` 对应最终写入 Alert 的值，包括降级后的标准失败 payload。

#### 状态与 outcome

`Status` 使用领域协议值：

```text
succeeded
partial
failed
skipped
```

`Outcome` 描述 Enricher 调用及协议校验结果：

| outcome | 触发条件 |
| --- | --- |
| `completed` | Enricher 正常返回，状态和 payload 均合法；返回的 status 可以是 succeeded、partial、failed 或 skipped |
| `error` | `AlertEnricher.Enrich` 返回 error，或观察结束前出现其他调用级失败 |
| `panic` | `AlertEnricher.Enrich` panic，由 Lifecycle 恢复并生成标准 failed payload |
| `invalid_status` | Enricher 返回 pending 或未知状态 |
| `invalid_data` | payload Normalize 失败，或 payload 与 status/processor 聚合结果不一致 |

`completed + failed` 表示 Enricher 正常完成并明确报告丰富失败。`error + failed` 表示 Enricher 调用异常后由 Lifecycle 生成失败结果。两者对应不同排障方向。

#### 开始和结束

`Started` 在基础 Alert 完成 Normalize 后调用，使 inflight 只覆盖实际进入 Enricher 的调用。`Finished` 通过 defer 保证调用结束路径成对记录，并携带：

- 从 Enricher 调用准备到最终 Alert Enrich Normalize 的持续时间；
- 最终 `EnrichStatus`；
- 最终紧凑 JSON payload 大小；
- EventSource 对应的 Chain 类型。

### 2.2 Processor 层

接口定义在：

```text
internal/lifecycle/enrich/observer.go
```

核心类型：

```go
type ProcessorObservation struct {
    Processor   string
    Status      domain.EnrichStatus
    Outcome     string
    Duration    time.Duration
    Diagnostics []Diagnostic
}

type Observer interface {
    ProcessorFinished(ctx context.Context, observation ProcessorObservation)
}
```

该层由 `enrich.Chain` 在每个 Processor 周围执行，计时范围覆盖：

```text
Processor.Match
→ Processor.Process
→ ProcessorResult Normalize
```

Observation 顺序与配置中的 Processor 顺序一致。Chain 在记录后继续构造同一份 `ProcessorEntry`，所以观测状态与最终 payload 中的 Processor envelope 保持一致。

#### Processor outcome

| outcome | 触发条件 |
| --- | --- |
| `completed` | Match 和 Process 正常完成；包含 succeeded、partial、failed 和 skipped |
| `match_error` | `Match` 返回 error |
| `process_error` | `Process` 返回 error |
| `panic` | Match 或 Process panic，由 Chain 恢复为 failed ProcessorResult |
| `invalid_result` | Processor 返回非法 status，或 Value 无法 Normalize |

Match error、Process error 和 panic 继续使用现有错误隔离语义：当前 Processor 转换为 failed，后续 Processor 仍可执行。父 Context 取消会在 Processor 边界停止 Chain。

#### Diagnostics

Processor Observation 携带 `Diagnostics` 的深拷贝。Telemetry 只读取：

```text
Diagnostic.Code
Diagnostic.Dependency
```

`Diagnostic.Fields` 继续保存在 `Alert.enrich` 中，用于具体输入字段诊断；它不会成为指标标签。

### 2.3 DataSource 层

装饰器定义在：

```text
internal/telemetry/enrich_datasource.go
```

入口：

```go
func (r *Runtime) ObserveEnrichSources(sources enrich.Sources) enrich.Sources
```

它在保持 Reader interface 和返回值不变的前提下包装四类 DataSource：

```text
CWStrategyReader
MetricReader
AlarmSourceReader
OneModelReader
```

调用关系：

```text
Processor
→ Scope 缓存
→ observed Reader
→ GORM / HTTP Client
```

装饰器位于 Scope 缓存之后。CW Strategy、AlarmSource 和 OneModel 的请求级缓存命中不会重复记录外部调用；MetricLibrary 每次真实查询都会记录。

#### DataSource outcome

分类优先级：

| 顺序 | 条件 | outcome |
| --- | --- | --- |
| 1 | error chain 包含 `context.Canceled` 或 `context.DeadlineExceeded` | `canceled` |
| 2 | error chain 包含 `enrich.ErrInvalidDataSourceResponse` | `invalid_response` |
| 3 | 其他 error | `failed` |
| 4 | `found=true` | `found` |
| 5 | 其余 | `not_found` |

`ErrInvalidDataSourceResponse` 表示传输或查询已经返回，响应内容违反 Reader 契约。当前覆盖：

- OneModel JSON 解码失败；
- OneModel 租户、模型或实例身份不匹配；
- MetricLibrary `dimension_list` 解码失败；
- BK Strategy History content 解码失败；
- CW Strategy JSON 字段解码失败。

HTTP 状态异常、响应超限、SQL 错误和连接错误归类为 `failed`。

#### DataSource 与 operation

| datasource | operation |
| --- | --- |
| `cw_strategy` | `get_by_bk_strategy_id` |
| `bk_strategy` | `get_strategy` |
| `cw_strategy` | `get_by_bk_strategy_id` |
| `cw_strategy` | `get_by_monitor_template_id` |
| `metric_library` | `find_metric_library` |
| `alarm_source` | `get_alarm_source_name` |
| `onemodel` | `find_instance` |

## 3. Chain 分类

`EnrichRouteClassifier` 定义在 Lifecycle 包：

```go
type EnrichRouteClassifier interface {
    EnrichChainKind(eventSourceID string) EnrichChainKind
}
```

`assembly.Router` 实现该接口，并在创建路由时保存每个 EventSource 的类型：

| chain kind | 含义 |
| --- | --- |
| `configured` | EventSource 配置了至少一个 Processor，Router 使用 `enrich.Chain` |
| `noop` | EventSource 的 Processor 列表为空，Router 使用 `enrich.NoopEnricher` |
| `unknown` | Enricher 未实现 classifier，或 EventSource 不在 Router 路由表中 |

Lifecycle 对 `AlertEnricher` 做可选 interface 断言。测试 stub 和其他自定义实现自然落入 `unknown`，无需依赖 assembly 包。

## 4. 注入与依赖方向

生产装配位于：

```text
internal/lifecycle/process/process.go
internal/lifecycle/process/enrich.go
```

实际顺序：

```text
创建真实 DataSource Clients
→ telemetry.Runtime.ObserveEnrichSources
→ assembly.NewRouter
    → assembly.WithEnrichObserver
    → enrich.NewChain
        → enrich.WithObserver
→ lifecycle.NewProcessor
    → lifecycle.WithEnrichObserver
```

两类 observer 的注入位置具有不同职责：

- `openEnricher` 注入 DataSource 和 Processor observer，因为它组装 Enricher 内部结构；
- `lifecycle.NewProcessor` 注入总体 Enrich observer，因为 Lifecycle 掌握协议校验、error/panic 降级和最终写入值。

`assembly/options.go` 提供 Router 的可选注入 seam：

```go
assembly.WithEnrichObserver(telemetryRuntime.EnrichProcessorObserver())
```

Router 将同一个 observer 传给所有 configured Chain。空配置沿用 `NoopObserver`。这种设计保持 `assembly` 只依赖 `enrich.Observer`，具体 OTel 实现留在 `internal/telemetry`。

`lifecycle.NewProcessor` 使用可选参数保持已有构造方式：

```go
lifecycle.NewProcessor(
    repository,
    recentAlerts,
    idGenerator,
    enricher,
    finalHook,
    severity,
    clock,
    logger,
    lifecycle.WithEnrichObserver(telemetryRuntime.EnrichObserver()),
)
```

缺少 option 时使用包内 no-op observer。

## 5. Telemetry 适配

实现文件：

```text
internal/telemetry/enrich.go
internal/telemetry/enrich_datasource.go
internal/telemetry/metrics.go
```

所有 Enrich 指标共享当前进程的：

- OTel `MeterProvider`；
- Prometheus registry 和 `/metrics` endpoint；
- `service.name=linkd`；
- `service.namespace=kingeye`；
- `service.version`；
- `service.instance.id`；
- `linkd.role=lifecycle|all-in-one`。

### 5.1 指标

| OTel 名称 | Prometheus 名称 | 类型 | 来源 |
| --- | --- | --- | --- |
| `linkd.enrich.attempts` | `linkd_enrich_attempts_total` | Counter | 总体 Finished |
| `linkd.enrich.attempt.duration` | `linkd_enrich_attempt_duration_seconds` | Histogram | 总体 Finished |
| `linkd.enrich.inflight` | `linkd_enrich_inflight` | UpDownCounter / Gauge | 总体 Started/Finished |
| `linkd.enrich.payload.size` | `linkd_enrich_payload_size_bytes` | Histogram | 总体 Finished |
| `linkd.enrich.processor.attempts` | `linkd_enrich_processor_attempts_total` | Counter | ProcessorFinished |
| `linkd.enrich.processor.duration` | `linkd_enrich_processor_duration_seconds` | Histogram | ProcessorFinished |
| `linkd.enrich.processor.diagnostics` | `linkd_enrich_processor_diagnostics_total` | Counter | 每条 Diagnostic |
| `linkd.enrich.datasource.operations` | `linkd_enrich_datasource_operations_total` | Counter | Reader wrapper |
| `linkd.enrich.datasource.duration` | `linkd_enrich_datasource_duration_seconds` | Histogram | Reader wrapper |

耗时 Histogram 使用：

```text
0.001, 0.005, 0.01, 0.025, 0.05,
0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30 秒
```

Payload Histogram 使用：

```text
512, 1024, 2048, 4096, 8192,
16384, 32768, 65536, 131072, 262144 字节
```

### 5.2 标签归一化

Telemetry adapter 对枚举执行白名单归一化：

- 非法 status → `unknown`；
- 非法总体 outcome → `unknown`；
- 非法 chain kind → `unknown`；
- 未注册 Processor → `unknown`；
- 非法 Processor outcome → `unknown`；
- 非法 diagnostic code → `other`；
- 空 dependency → `none`；
- 未知 dependency → `other`。

当前允许的 dependency：

```text
kingeye_strategy
metric_library
onemodel
alarm_source
```

## 6. 基数和数据安全

允许进入 Enrich 指标的属性：

```text
linkd.event_source_id
linkd.status
linkd.outcome
linkd.chain_kind
linkd.processor
linkd.diagnostic_code
linkd.dependency
linkd.datasource
linkd.operation
```

以下信息不会进入指标：

```text
bk_tenant_id
event_id
alert_id
fingerprint
strategy_id
history_id
bk_biz_id
model_inst_id
metric_unique_id
result_table_id
Diagnostic.Fields
SQL、URL、错误全文、payload、凭据
```

EventSource ID 来自启动时已校验配置或 StoredEvent。其他动态输入均不会作为指标标签。

## 7. 失败与取消语义

- Enricher error 和 panic 由 Lifecycle 生成标准 failed payload，总体 Observation 记录降级后的最终结果；
- Processor error 和 panic 由 Chain 隔离为 failed ProcessorResult，后续 Processor 保持可执行；
- DataSource wrapper 原样返回 value、found 和 error；
- Context 取消优先归类为 `canceled`，Lifecycle 继续按已有取消语义退出；
- OTel provider 关闭时 Runtime 使用 no-op provider，业务调用路径保持一致；
- Observer interface 不接收完整 Alert、Event、DataSource query、payload 或 error 文本。

## 8. Console 展示

Lifecycle 页面包含独立 Enrich 区域，展示：

1. Enrich 状态速率；
2. 总体平均、P95、P99；
3. inflight；
4. Processor 状态速率和 P99；
5. Processor diagnostics；
6. DataSource 调用状态和 P99；
7. payload 平均、P95、P99；
8. Enrich 与 Lifecycle 平均耗时比例。

查询沿用 Console 的时间范围、计算窗口、instance selector、EventSource selector和空样本语义。Enrich 与 Lifecycle 平均耗时比例只用于判断阶段占用，Lifecycle attempt 还包含存储、裁决和 FinalHook 等工作。

## 9. 关键不变量

1. 一个进入 `enrichNewAlert` 的调用产生一对 Started/Finished；
2. 一个 configured Chain 中，每个已开始执行的 Processor 最多产生一条 ProcessorObservation；
3. Processor Observation 顺序与配置顺序一致；
4. DataSource operation 数量等于 observed Reader 的真实调用次数；
5. Scope 缓存命中不重复记录 DataSource operation；
6. 总体 Status 和 PayloadBytes对应最终 Alert Enrich；
7. Observation 不改变任何领域返回值、错误、重试和副作用顺序；
8. 指标标签保持低基数且不携带敏感业务数据。

## 10. 代码导航

| 关注点 | 文件 |
| --- | --- |
| 总体 observation 协议 | `internal/lifecycle/enrich_observer.go` |
| 总体调用与最终降级 | `internal/lifecycle/enrich.go` |
| Processor observation 协议 | `internal/lifecycle/enrich/observer.go` |
| Processor 执行和隔离 | `internal/lifecycle/enrich/enricher.go` |
| Chain 类型识别 | `internal/lifecycle/enrich/assembly/router.go` |
| Router observer option | `internal/lifecycle/enrich/assembly/options.go` |
| Reader seam 和 Scope 缓存 | `internal/lifecycle/enrich/scope.go` |
| 非法响应分类 | `internal/lifecycle/enrich/datasources/*.go` |
| 总体和 Processor OTel adapter | `internal/telemetry/enrich.go` |
| DataSource OTel adapter | `internal/telemetry/enrich_datasource.go` |
| instruments 和 histogram views | `internal/telemetry/metrics.go` |
| 生产装配 | `internal/lifecycle/process/enrich.go`、`process.go` |
| Console PromQL | `console/src/server/prometheus.ts` |
| Console 页面 | `console/src/web/pages/LifecyclePage.tsx` |
