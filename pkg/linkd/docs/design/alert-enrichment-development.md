# Alert Enrichment 可开发设计

状态：开发基线。

本文将 [alarm_callback 迁移执行计划](alarm-callback-enrichment-migration.md) 中已经确认的业务规则，收敛为可直接编码的 Go 模块、配置、结果协议和实施顺序。本文优先定义 Enrichment 的代码位置、调用输入、处理器编排和 `Alert.enrich` 结构；旧文档继续提供字段来源、旧行为证据和迁移取舍。实现本设计时须同步更新旧文档中的 Event 输入、硬编码来源和旧分组结构描述，保持单一现行契约。

## 1. 目标与边界

本次实现以待创建的 `domain.Alert` 为丰富字段的唯一输入来源和丰富结果载体；`EnrichInput` 只保留 Alert，不再携带 `domain.Event`：

- Enricher 接收 `lifecycle.EnrichInput{Alert}`，后续路由、查询条件和字段加工统一读取 `EnrichInput.Alert` 的对应字段。
- Scope 的业务输入只持有 Alert 深拷贝；Processor 不接触 Event，也不修改 Scope 内的 Alert，所有结果通过返回值汇总。
- Enrichment 在新 Alert 持久化前同步执行；等级升级产生的新 Alert 同样执行。
- 同等级更新、恢复和关闭沿用已有生命周期，不重新丰富。
- Processor 只执行外部只读查询和确定性转换，不产生外部业务写入。
- CAS 冲突和消息恢复可能让尚未持久化的 Alert 再次执行丰富，因此所有 Processor 必须可重复调用。
- `SourceRawData` 不进入 Alert，Processor 不读取该字段。
- `ACCESS_OBJECT`、终态补查和智能算法专用图表增强继续排除。

旧迁移文档中用于丰富的所有 `Event.<field>` 读取统一替换为 `Alert` 对应字段读取；同名同义字段直接映射，名称不同的字段按下表映射，不机械替换。字段校验、优先级和失败规则保持原决定，实际取值以已完成基础构造和 Normalize 的 Alert 为准。丰富链路不再接收或克隆 Event；Alert 字段缺失或非法时沿用相应失败规则，不回退读取 Event。示例：

| 旧描述 | 开发时读取 |
| --- | --- |
| `Event.BKTenantID` | `Alert.BKTenantID` |
| `Event.EventSourceID` | `Alert.EventSourceID` |
| `Event.Content` | `Alert.Content` |
| `Event.Severity` | `Alert.Severity` |
| `Event.Labels` | `Alert.Labels` |
| `Event.Dimensions` | `Alert.Dimensions` |
| `Event.ExtraData` | `Alert.ExtraData` |
| `Event.SourceEventID` | `Alert.SourceEventID` |
| `Event.EventID` | 创建当前告警的事件对应 `Alert.TriggerEventID`，用于丰富降级日志；不是 `Alert.AlertID` |
| `Event.OccurredAt` | 创建告警时对应 `Alert.BeginAt`；只在旧规则确实需要发生时间时读取 |

本次只移除丰富链路对 Event 的依赖。Lifecycle 仍使用 Event 完成告警身份生成、状态决策与事件处理结果记录；不删除领域 Event，也不向 Alert 增加本次丰富不需要的 Event 专属字段。

## 2. 代码位置与包结构

Enrichment 根目录固定为：

```text
internal/lifecycle/enrich/
```

建议首版目录：

```text
internal/lifecycle/
├── enrich.go                         # Lifecycle 调用保护：panic/error/非法结果降级
└── enrich/
    ├── doc.go                        # 包职责和边界
    ├── enricher.go                   # Enricher 编排实现
    ├── processor.go                  # Processor 接口
    ├── result.go                     # ProcessorResult、Result、JSON 编码
    ├── status.go                     # 总状态计算
    ├── scope.go                      # 单次调用上下文与请求内查询复用
    ├── noop.go                       # 空处理链实现
    ├── assembly/                     # 注册表、配置解析、Processor 与 Adapter 装配
    ├── processors/
    │   ├── strategy/
    │   ├── resource/
    │   ├── display/
    │   ├── metric/
    │   ├── log/
    │   ├── apm/
    │   ├── k8s/
    │   └── source/
    └── datasources/
        ├── doc.go
        ├── platformstrategy/         # alarm_strategy_history 只读适配
        ├── kingeyestrategy/          # StrategyConfig / CloudStrategyConfig
        ├── cmdb/
        ├── meta/
        ├── metric/
        ├── log/
        ├── apm/
        ├── onemodel/                 # OneModel Elasticsearch 读适配
        └── dynamicgroup/             # Redis 动态分组读适配
```

目录名采用 `datasources`，符合 Go 包命名习惯。该目录只保存 Enrichment 使用的外部读取适配器、协议 DTO 和查询模型。Processor 的业务字段组装保留在 `processors/<type>`，数据库行、Redis 值、ES 文档和远端响应不得泄漏到 Lifecycle。

依赖方向固定为：`lifecycle` 定义 `AlertEnricher`、`EnrichInput` 与 `EnrichResult` 端口，`enrich` 实现该端口，`assembly → lifecycle + enrich + processors + datasources`，`processors → enrich`，`datasources` 实现消费方定义的窄读取接口。`lifecycle` 不导入具体 Processor 或 Adapter；进程装配层通过 `enrich/assembly` 构造实现并注入 Lifecycle。

首版只创建实际实现需要的子包。阶段三从 `strategy`、`resource`、`display`、`metric` 和 `source` 的基础监控切片开始，其余目录随真实实现增加。

## 3. 核心接口

### 3.1 Enricher

`Enrich` 接口仍以 `lifecycle.EnrichInput` 为入参，但输入结构收窄为只含 Alert。Lifecycle 继续定义接口、输入和返回端口，`internal/lifecycle/enrich` 提供具体实现：

```go
package lifecycle

type EnrichInput struct {
    Alert domain.Alert
}

type AlertEnricher interface {
    Enrich(ctx context.Context, input EnrichInput) (EnrichResult, error)
}

type EnrichResult struct {
    Status domain.EnrichStatus
    Data   domain.JSONObject
}
```

调用方必须传入已完成基础构造和 `Normalize()` 的 Alert 深拷贝。实现不得在本次调用结束后保存 Alert，也不得保存 Context；不得修改输入中的 `Labels`、`Dimensions`、`ExtraData` 或其他字段。Enricher 从 `input.Alert` 选择处理链并构造 Scope，后续字段读取只使用 Alert 副本；EnrichInput 与 Scope 均不持有 Event。

`internal/lifecycle/enrich.go` 继续承担调用保护：

1. Normalize 待创建 Alert 后，以 `EnrichInput{Alert: normalized.Clone()}` 调用 Enricher，不再克隆 Event。
2. 父 Context 取消时终止生命周期处理。
3. panic、返回 error、非法状态或非法 JSON 按现有保护规则降级。
4. 合法结果只写入 `Alert.EnrichStatus` 与 `Alert.Enrich`。
5. 再次 Normalize Alert 后交给 Repository。

内部调用保护函数收窄为 `enrichNewAlert(ctx context.Context, alert domain.Alert)`，调用方不再传入 Event。降级日志从保护函数持有的 normalized Alert 读取 `BKTenantID`、`TriggerEventID` 和 `AlertID`，分别填入 `bk_tenant_id`、`event_id` 和 `alert_id`；不读取交给 Enricher 的可被错误实现修改的输入副本。`TriggerEventID` 表示创建当前 Alert 的事件，不使用会随生命周期更新的 `LatestEventID` 代替。

### 3.2 Processor

每一种丰富资源对应一个 Processor。Processor 名称同时用于配置注册名和结果 key。

```go
package enrich

type Processor interface {
    Name() string
    Process(ctx context.Context, scope *Scope) (ProcessorResult, error)
}

type ProcessorResult struct {
    Status      domain.EnrichStatus
    Value       domain.JSONObject
    Diagnostics []Diagnostic
}
```

约束：

- `Process()` 的 `error` 只表达程序错误或无法封装的 Adapter 错误；可预期业务结果通过 `ProcessorResult` 表达。

- `Name()` 必须稳定，且与注册表名称相同。
- `Process()` 返回 `succeeded`、`partial` 或 `failed`；`pending` 只用于持久化前的 Alert 初始状态。
- `Value` 必须是 JSON object；无输出时使用空 object。
- 可预期的输入错误、0 条结果和依赖故障通过 `ProcessorResult` 表达。
- 父 Context 取消通过 Scope 检查并由编排器停止后续执行。
- 单个 Processor 的 panic 或非 nil error 由编排器转换为该 Processor 的 failed 结果，后续 Processor 继续执行；父 Context 取消保持中止语义。

### 3.3 Scope

已确认：Scope 的业务输入只持有 Alert 副本，不持有 Event 或整个 EnrichInput。Processor 顺序执行，跨 Processor 共享一次调用内的 Scope；Alert 输入只读，请求内查询结果仍由 Scope 惰性加载并复用：

```go
type Scope struct {
    // 构造时从 input.Alert 深拷贝，单次调用期间保持不变。
    alert domain.Alert
    // 请求内已加载的策略、分类、资源和外部查询结果。
}

// Alert 返回隔离副本，避免 Processor 改动影响后续处理器。
func (s *Scope) Alert() domain.Alert {
    return s.alert.Clone()
}
```

Scope 的职责：

- 构造 Scope 时仅接收 Alert 和所需 Data Sources，并通过 `Alert.Clone()` 保存隔离副本；不接收或保存整个 `EnrichInput`，也不暴露 `Event()` 读取入口。
- `Alert()` 返回隔离副本；Processor 和 Scope 的加载方法均从 Alert 对应字段构造查询条件，不回退读取 Event。
- 暴露类型明确的只读加载方法，例如平台历史策略、鲸眼配置和分类结果。
- 同一 Enrich 调用内复用已经取得的数据，避免多个 Processor 重复查询。
- 请求内复用不形成跨 Alert 缓存，不引入 TTL、刷新或失效协议。
- Processor 不读取前一个 Processor 的 JSON Value；共享信息通过 Scope 的类型化数据访问。
- Scope 不回写 Alert，也不把外部响应写入 `Alert.ExtraData`；查询结果保存在独立的请求内字段中。

## 4. Processor 注册名与职责

首版注册以下名称：

| Processor | 主要输出 | 主要职责 |
| --- | --- | --- |
| `strategy` | 策略 ID、名称、URL、data_source | 平台历史与鲸眼配置读取、策略展示字段 |
| `resource` | CMDB/Meta 模型实例、业务拓扑、动态分组、cw_labels | 资源定位和资源上下文 |
| `display` | title、content、object、dimensions、dimension_text | 展示加工 |
| `metric` | 指标名、单位、聚合、where、metric_query_params | 指标元数据与查询参数 |
| `log` | 日志主题、查询语句、关联信息 | 日志分类专用字段 |
| `apm` | 应用、服务、实例、接口、对端名称 | APM 条件分支 |
| `k8s` | 集群、Namespace、Workload、Pod、容器、节点 | K8s 条件分支 |
| `source` | source_id、source_name、meta_info | 来源补充字段 |

Processor 可以根据 Alert 与 Scope 分类结果判断当前告警是否适用。规则不适用时返回 `succeeded` 和空 Value，不生成失败诊断。

配置中同一 Processor 名称最多出现一次。未知名称和具体实现构造由 `enrich/assembly` 校验；重复名称与空 type 可在 `internal/config` 校验。任何错误都使进程启动失败。

## 5. EventSource 下的可配置处理链

### 5.1 配置模型

处理链配置挂在每个 `event_sources[]` 项下：

```go
type EventSource struct {
    // 既有字段省略。
    Enrich EnrichConfig `yaml:"enrich,omitempty"`
}

type EnrichConfig struct {
    Processors []EnrichProcessorConfig `yaml:"processors,omitempty"`
}

type EnrichProcessorConfig struct {
    Type string `yaml:"type"`
}
```

Processor 出现于列表即表示启用，列表顺序就是执行顺序和最终结果列表顺序。首版不增加 Processor 级 `enabled` 布尔值。

基础配置示例：

```yaml
event_sources:
  - event_source_id: built_in_bk
    enabled: true
    cleaner:
      type: standard
    fingerprint_mode: field
    fingerprint_field: source_alert_id
    severity_mapping:
      "1": critical
      "2": warning
      "3": info
    enrich:
      processors:
        - type: strategy
        - type: resource
        - type: display
        - type: metric
        - type: source
    storage:
      type: kafka
      kafka:
        brokers: ["127.0.0.1:9092"]
        topic: alerts
        consumer_group: linkd
```

日志来源可以配置：

```yaml
enrich:
  processors:
    - type: strategy
    - type: resource
    - type: display
    - type: metric
    - type: log
    - type: source
```

后续 K8s/APM 分支通过在同一链中加入 `k8s`、`apm` Processor 启用。主分类仍由 Alert 特征和鲸眼配置决定，Processor 列表只控制执行能力及顺序。

### 5.2 空链语义

`enrich.processors` 缺失或为空时使用 Noop：

```json
{
  "status": "succeeded",
  "processors": []
}
```

对应 `Alert.EnrichStatus` 为 `succeeded`。该行为覆盖任意 EventSource，无需在 Enricher 内硬编码 `built_in_bk`。

### 5.3 Lifecycle 的 EventSource 路由快照

Lifecycle 进程启动时从完整 `cfg.EventSources` 构造不可变路由表，包含 enabled 和 disabled 来源。每个来源映射到自己的 Processor Chain；disabled 只表示 Cleaner Flow 不启动，已持久化 Event 仍可能由 Lifecycle 恢复处理，因此其 Enrich 配置仍须可解析。

Alert.EventSourceID 在路由表中不存在时属于装配或持久化数据不一致，Enricher 返回 error，由 Lifecycle 现有保护降级为 failed。已知来源配置空链时执行 Noop。该区分防止拼写错误的来源悄悄绕过丰富。

### 5.4 外部连接配置

Processor 链属于 EventSource。数据库、Redis、Elasticsearch 和远端服务凭据属于进程级基础设施配置，由装配层按 `Alert.BKTenantID` 选择后注入 Data Source Adapter。凭据不复制到每条 EventSource，也不进入 Processor 结果。

## 6. Alert.enrich JSON 契约

### 6.1 顶层结构

`Alert.enrich` 固定为只有两个顶层 key 的 map：

```json
{
  "status": "partial",
  "processors": [
    {
      "strategy": {
        "status": "succeeded",
        "value": {
          "bk_strategy_id": 123,
          "monitor_template_id": "template-1",
          "strategy_config_id": "strategy-config-1",
          "strategy_name": "CPU 使用率",
          "url": "https://example/...",
          "data_source": "system"
        }
      }
    },
    {
      "resource": {
        "status": "partial",
        "value": {
          "bk_biz_id": 2,
          "bk_biz_name": "蓝鲸"
        },
        "diagnostics": [
          {
            "code": "dependency_invalid",
            "dependency": "dynamic_group",
            "fields": ["dynamic_group_id"]
          }
        ]
      }
    }
  ]
}
```

顶层字段：

| 字段 | 类型 | 规则 |
| --- | --- | --- |
| `status` | string | Processor 列表聚合状态 |
| `processors` | array | 顺序与 EventSource 配置一致 |

每个 `processors[]` 元素必须是只有一个 key 的 map；该结构在 Go 中定义为具名 map 类型，统一负责校验和 JSON 编码：

```go
type ProcessorEntry map[string]ProcessorEnvelope

type ProcessorEnvelope struct {
    Status      domain.EnrichStatus `json:"status"`
    Value       domain.JSONObject   `json:"value"`
    Diagnostics []Diagnostic        `json:"diagnostics,omitempty"`
}

type Payload struct {
    Status     domain.EnrichStatus `json:"status"`
    Processors []ProcessorEntry    `json:"processors"`
}
```

每个 `processors[]` 元素必须是只有一个 key 的 map；key 为 Processor 名称，value 为结果 envelope：

| 字段 | 类型 | 规则 |
| --- | --- | --- |
| `status` | string | `succeeded/partial/failed` |
| `value` | object | 该 Processor 的丰富字段；无值时为 `{}` |
| `diagnostics` | array，可省略 | 该 Processor 的结构化诊断 |

字段值不能直接平铺到 `Alert.enrich` 顶层。不同 Processor 的 Value 可以存在相同字段名，因为 Processor 名称提供命名空间。

顶层 `status` 作为 `Alert.EnrichStatus` 在 enrich payload 中的自描述镜像。写入时只允许由同一次聚合计算同时产生两者，读取校验要求一致。

### 6.2 总状态算法

按完整 Processor 结果列表计算：

| 列表状态 | 总状态 |
| --- | --- |
| 空列表 | `succeeded` |
| 所有 Processor 都是 `succeeded` | `succeeded` |
| 所有 Processor 都是 `failed` | `failed` |
| 其他组合，包括任一 `partial`，或 succeeded 与 failed 混合 | `partial` |

伪代码：

```go
func aggregateStatus(results []ProcessorResult) domain.EnrichStatus {
    if len(results) == 0 {
        return domain.EnrichStatusSucceeded
    }
    succeeded, failed := 0, 0
    for _, result := range results {
        switch result.Status {
        case domain.EnrichStatusSucceeded:
            succeeded++
        case domain.EnrichStatusFailed:
            failed++
        default:
            return domain.EnrichStatusPartial
        }
    }
    if succeeded == len(results) {
        return domain.EnrichStatusSucceeded
    }
    if failed == len(results) {
        return domain.EnrichStatusFailed
    }
    return domain.EnrichStatusPartial
}
```

`Alert.EnrichStatus` 保留为现有顶层索引字段，并必须等于 payload 的 `status`。实现通过 `Payload` 解码后校验，禁止直接按 `Alert.Enrich["status"]` 比较原始 JSON 字节。Repository 和 Kafka 输出继续保留 `enrich_status`。

### 6.3 Processor 状态

- `succeeded`：适用规则执行成功，或该 Processor 对当前 Alert 不适用。
- `partial`：该 Processor 已保留部分有效字段，同时存在输入或依赖问题。
- `failed`：该 Processor 无法完成自身核心职责；Value 通常为空，诊断保留。

查询成功且 0 条时，沿用旧规则输出空对象、空字符串、空列表或省略字段；该情况本身不新增 partial。平台策略历史和鲸眼配置缺失继续遵循已经确认的 partial 特例。

### 6.4 诊断结构

诊断归属于 Processor，因此不再使用旧设计里的 `groups` 字段；Processor key 已经表达受影响分组。

```go
type Diagnostic struct {
    Code       DiagnosticCode `json:"code"`
    Dependency string         `json:"dependency,omitempty"`
    Fields     []string       `json:"fields,omitempty"`
}
```

原因码保持：

- `missing_field`
- `invalid_field`
- `dependency_invalid`
- `classification_failed`

不保存请求、响应、凭据、完整异常文本或原始 payload。诊断列表应保持稳定顺序：按 Processor 执行过程中规则出现的固定顺序追加。

## 7. EnrichInput 与 Alert 字段契约

`EnrichInput` 只携带 Alert；Enricher 直接读取 `EnrichInput.Alert`，Processor 经 `Scope.Alert()` 读取其隔离副本。后续字段读取统一使用以下 Alert 路径：

| 信息 | Alert 路径 | 规则 |
| --- | --- | --- |
| 租户作用域 | `Alert.BKTenantID` | 外部连接选择与查询隔离 |
| 来源路由 | `Alert.EventSourceID` | 选择 EventSource Processor Chain |
| 平台策略 ID | `Alert.Labels.bk_strategy_id` | 数字 Scalar 正整数 |
| 平台策略历史 ID | `Alert.Labels.bk_strategy_history_id` | 数字 Scalar 正整数 |
| 来源业务 ID | `Alert.Labels.bk_biz_id` | 数字 Scalar 正整数 |
| 告警内容 | `Alert.Content` | display 文案输入 |
| 告警等级 | `Alert.Severity` | 内容文案算法等级匹配 |
| 观测与定位维度 | `Alert.Dimensions` | 保持既有标量类型和值 |
| 日志关联信息 | `Alert.ExtraData.log_related_info` | 可选字符串；错误类型使 log partial |
| 首次异常点时间 | `Alert.ExtraData.anomaly_begin_time` | 可选字符串，包含空字符串，原样输出 |
| 来源事件标识 | `Alert.SourceEventID` | 输出到 source.meta_info |
| 触发事件标识 | `Alert.TriggerEventID` | 丰富降级日志的 event_id，不替代来源事件标识或告警 ID |
| 告警开始时间 | `Alert.BeginAt` | 对应创建告警的 Event.OccurredAt；只在旧规则确实需要发生时间时读取 |

策略 Processor 和依赖 Scope 先读取平台历史，再读取鲸眼配置：

1. 用 `bk_strategy_history_id + bk_strategy_id` 联合定位 `alarm_strategy_history`。
2. 校验历史 `content.bk_biz_id == Alert.Labels.bk_biz_id`。
3. 按租户连接查询 StrategyConfig；明确 0 条时查询 CloudStrategyConfig。
4. 多匹配沿用底层第一条。
5. StrategyConfig 查询故障直接 partial，不进入 CloudStrategyConfig 类型回退。

所有租户选择使用 `Alert.BKTenantID`。平台库、鲸眼存储和 Redis 由装配层按该值选择连接；业务 ID 不承担租户推导。Lifecycle 负责从创建事件构造完整 Alert，并通过生命周期测试验证继承字段及 `TriggerEventID`、`BeginAt` 的映射；Enricher 不再接收两份输入，也不承担 Event/Alert 一致性校验。

## 8. Processor Value 字段

各 Processor 的 Value 沿用迁移计划中已经确认的字段名。

### 8.1 strategy

```text
bk_strategy_id
monitor_template_id
strategy_config_id
strategy_name
url
data_source
```

- `bk_strategy_id` 来自 Alert Labels。
- `monitor_template_id` 保留旧 `clean_strategy_id()` 完整返回行为。
- `strategy_config_id` 来自鲸眼 StrategyConfig 的 `config_id`，移除 `-` 后用于输出和 URL。
- URL 参考 `BaseClear.format_strategy_url`：`strategy_config_id` 先移除连字符；默认策略和云策略使用 `/#/kmc/manage/monitorStrategy/monitorStrategyDetails?id=<monitor_template_id>&strategy_config_id=<strategy_config_id>`；实例策略使用 `/#/kmc/scene/monitorViewDetails`，携带 `bk_obj_id`、`bk_inst_id`、`strategy_id=<monitor_template_id>`、`strategy_item_id=<strategy_config_id>`、`classId` 和 `object_model_code`。

### 8.2 resource

```text
bk_obj_id
bk_inst_id
model_id
model_inst_id
model_name
bk_biz_id
bk_biz_name
bk_set_id
bk_set_name
bk_module_id
bk_module_name
bk_cloud_id
bk_cloud_name
cloud_plat_id
dynamic_group_id
cw_labels
```

### 8.3 display

```text
title
content
object
dimensions
dimension_text
```

`dimensions` 保留生成顺序；`dimension_text` 使用副本按 name 升序后执行旧过滤和拼接。

### 8.4 metric

```text
display_name
metric_name
unit
result_table_id
metric_unique_id
aggregate_func
time_interval
where_condition
metric_query_params
anomaly_begin_time
```

普通 query_config 透传 `time_field` 和 `extend_fields`；`extend_fields` 必须为对象。智能算法专用图表增强不实现。

### 8.5 log

```text
log_theme_id
log_theme_name
log_query_string
log_relate_info
```

`Alert.ExtraData.log_related_info` 只接受字符串。缺失沿用旧空字符串语义；错误类型使 log Processor partial，并保留 display 对 Alert.Content 可独立完成的加工。

### 8.6 apm

```text
apm_app_id
apm_app_name
apm_app_alias
apm_service_name
apm_instance_name
apm_interface_name
apm_net_peer_name
```

### 8.7 k8s

```text
bcs_cluster_id
cluster_name
namespace
service
workload_kind
workload_name
pod_name
container_name
node
```

K8s 资源业务、模型和实例身份继续进入 resource Processor Value。

### 8.8 source

```text
source_id
source_name
meta_info
```

`source_id` 读取 `Alert.EventSourceID`；`source_name` 使用该 ID 与 `Alert.BKTenantID` 查询 Kingeye `alarm_collect_alarmsource.name`；`meta_info` 读取 `Alert.SourceEventID`。


## 9. Data Source Adapter

当前暂时无法访问真实外部数据，开发阶段允许 DataSource 先返回固定 mock 数据，具体临时约束见第 9.4 节。以下接口与读取规则仍是目标契约，不代表真实数据源已接通。

### 9.1 设计原则

- Adapter 只读。
- 每个外部系统使用明确查询结构和返回结构。
- Context 作为第一个参数并完整传播。
- 查询必须显式携带或通过已选连接绑定 `bk_tenant_id`。
- 0 条结果与查询错误使用不同返回语义。
- Adapter 不决定 Processor 状态；Processor 将查询结果映射为 succeeded/partial/failed。
- Adapter 不自动重试，不创建跨 Alert 缓存。

建议使用 `(value, found, error)` 或明确结果类型区分 0 条与故障：

```go
type PlatformStrategyHistoryReader interface {
    Get(
        ctx context.Context,
        tenantID string,
        strategyID int64,
        historyID int64,
    ) (PlatformStrategyHistory, bool, error)
}
```

### 9.2 请求内复用

平台历史、鲸眼配置、主分类、模型和实例等被多个 Processor 使用的数据由 Scope 惰性加载并缓存本次结果。第一次调用执行 Adapter，后续调用返回同一结果。缓存包括成功、0 条和失败结果，保证单次 Enrich 调用内行为一致。

### 9.3 已确认的特殊读取

Resource Processor 的实例定位统一调用 `datasources.OneModelClient.FindInstance`，直接读取 OneModel 的 Elasticsearch 后端。Client 按 `cw_object_model_code` 路由 CMDB、K8s、APM 与云实例索引，强制叠加 `bk_tenant_id` 和模型过滤，查询及响应均使用 OneModel ES 文档的扁平字段；返回第一条实例文档后由 Resource Processor 组装 resource Value。

连接配置位于 `lifecycle.datasources.onemodel`：`addresses` 与认证字段定义独立 ES 连接，`index_prefix` 定义 Kingeye CMDB 实例索引前缀，例如 `bk_monitor_base_` 对应 `bk_monitor_base_cmdb_instance`。K8s/APM/云模型继续使用 OneModel 的固定路由目标。该配置与 `storage.elasticsearch` 分别表达实例数据源和 Linkd Repository，部署可按实际集群填写相同或不同连接。

- 平台历史：按租户配置选择数据库连接；SQL 使用历史 ID 与策略 ID；解析 content 后校验业务 ID。
- 告警源：使用 GORM 按 `Alert.BKTenantID + Alert.EventSourceID` 查询 Kingeye `alarm_collect_alarmsource` 的 `name`；0 条与故障分别处理。
- 鲸眼配置：StrategyConfig 0 条时再读 CloudStrategyConfig；多条取第一条。
- 动态分组：按 Alert.BKTenantID 选择 Redis 连接；key 为 `<redis_key_prefix>dynamic_inst_group:{model_code}`。
- K8s：按 OneModel 当前 ES 模型路由和租户过滤读取；多条取第一条。
- 主机 IP/云区域：沿用旧条件与第一条选择规则。

### 9.4 临时约束：先使用 mock 数据

在真实数据源尚不可访问期间，CMDB、数据库表、Redis、Elasticsearch 及远端服务查询可以先由 `enrich/datasources` 中的 mock Adapter 提供一套固定样例，推进 Processor 开发和丰富链路验证。

- mock Adapter 实现与真实 Adapter 相同的窄读取接口，保留查询参数、明确返回类型以及 `(value, found, error)` 等语义；返回的是依赖查询数据，不是预先拼好的最终 enrich 结果，业务加工仍由 Processor 执行。
- 使用人工构造、无敏感信息且相互关联一致的固定数据；相同租户与查询条件得到稳定结果，不用当前时间或随机数生成返回值。按租户与查询条件匹配样例，未命中时按接口返回未找到，不向任意租户或任意 ID 无条件返回同一份成功数据。
- mock 仅由装配层显式注入，Processor 和 Scope 不判断是否为 mock。使用 mock 时不连接真实服务，也不要求真实连接凭据；真实 Adapter 查询失败时不得自动回退 mock。
- 仍遵循 Context 取消和只读约束；含 map、slice 等可变数据的返回值应隔离，避免一次调用修改固定样例后影响后续调用。未找到、依赖错误等路径通过测试替身补充验证，不以一份成功样例代替失败场景测试。
- 开发启动说明和验证记录必须标明使用 mock；通过 mock 只能说明相应逻辑和样例链路已验证，不能表述为 CMDB、数据库或其他真实数据源已接通、联调通过或可用于生产。

临时约束解除条件：真实访问条件具备后，在装配层替换为真实 Adapter，按第 9.3 节规则完成对应数据源的集成验证，再移除该数据源的临时 mock 装配。固定样例可继续作为测试夹具保留；实际使用 mock 的数据源及尚未验证的查询必须在交付记录中逐项注明。

## 10. 执行流程

```text
Lifecycle 构造并 Normalize 待创建 Alert
  → Clone 待创建 Alert，构造 EnrichInput{Alert}
  → 调用 Enrich(ctx, input EnrichInput)
  → 根据 input.Alert.EventSourceID 找到 EventSource enrich.processors
  → 仅用 input.Alert 与 Data Sources 构造 Scope，内部保存 Alert.Clone()，不传入 Event
  → 按配置顺序逐个执行 Processor
      → 从 Scope.Alert() 的对应字段读取输入
      → 返回单 Processor status/value/diagnostics
      → 编排器封装为单 key map 并追加列表
  → 根据完整列表计算总状态
  → 编码 {status, processors}
  → 校验 JSON 与 Alert.EnrichStatus 一致
  → Lifecycle 写回 Alert.EnrichStatus / Alert.Enrich
  → Repository 创建 Alert
  → FinalHook 输出完整 Alert 快照
```

Processor 失败不短路处理链。Processor 返回 error 或发生 panic 时，编排器生成 `failed` envelope：Value 为 `{}`，diagnostic code 为 `dependency_invalid`，dependency 使用 Processor 名称；日志记录脱敏后的内部错误。父 Context 取消立即终止，后续 Processor 不再执行，并沿用 Lifecycle 的取消语义。

## 11. 配置与装配改动

需要修改：

1. `internal/config.EventSource` 增加 `Enrich`。
2. EventSource clone/redaction/validation 覆盖 Processor 列表。
3. Lifecycle 进程根据 `cfg.EventSources` 建立 `event_source_id → Processor Chain` 的只读注册表。
4. Lifecycle Enricher 根据 `EnrichInput.Alert.EventSourceID` 选择处理链。
5. 已知来源无处理链时执行 Noop；未知 EventSourceID 返回 error 并由 Lifecycle 降级。
6. Data Source Runtime 在进程启动时创建，构造 Processor 时注入，进程退出时统一关闭。
7. 任一已配置 Processor 构造失败时 Lifecycle 进程启动失败。

凭据只出现在进程级 Data Source 配置和 Adapter 中；EventSource 的 Processor 配置只表达处理链。

当前开发装配可按第 9.4 节注入 mock Adapter，无需初始化对应真实连接或校验其凭据；Processor 注册、配置与构造校验仍须执行。

## 12. 与当前代码的明确差异

实现需要同步调整以下现状：

| 当前代码 | 目标形态 |
| --- | --- |
| `internal/lifecycle/enrich.go` 定义 `EnrichInput{Event, Alert}` | 输入收窄为 `EnrichInput{Alert}`，保留 `AlertEnricher.Enrich(ctx, input EnrichInput)` 方法，由 `internal/lifecycle/enrich` 提供实现 |
| `enrichNewAlert(ctx, event, alert)` 克隆 Event 并从 Event 读取降级日志身份 | 收窄为 `enrichNewAlert(ctx, alert)`，仅克隆 Alert；日志身份从 normalized Alert 的 `BKTenantID`、`TriggerEventID`、`AlertID` 读取 |
| `NoopEnricher` 位于 lifecycle 包 | 移到 `internal/lifecycle/enrich` |
| Lifecycle 进程固定注入 Noop | 按 EventSource 配置构造路由 Enricher |
| `Alert.Enrich` 可接受任意 object | 固定编码为 `{status, processors}` |
| `Alert.EnrichStatus` 单独设置 | 与 `Alert.Enrich.status` 同源计算并校验一致 |
| 诊断使用全局 groups | 诊断归属 Processor，删除 groups |
| 迁移计划按 `enrich.strategy/resource/...` 描述 | 对应字段进入同名 Processor 的 `value` |

这些调整发生在 Linkd 尚未发布稳定 Enrichment 契约的阶段，可以直接修改代码、测试和权威文档。

## 13. 开发顺序

每个步骤形成独立可审查提交，先保持编译和现有测试通过，再进入下一步。

### 阶段 A：框架和协议

1. 增加 EventSource Enrich 配置、clone 与基础校验测试；此步只改配置模型。
2. 创建 `internal/lifecycle/enrich` 的结果类型、JSON 编码和状态聚合纯函数测试。
3. 增加仅持有 Alert 副本的 Scope、Processor 接口、编排器、Noop、panic/error 隔离和 Alert 不可变测试。
4. 增加 `enrich/assembly`，完成注册表与 EventSource 路由构造测试。
5. 将 Lifecycle 输入收窄为 `EnrichInput{Alert}`，同步移除调用保护函数的 Event 参数与克隆，调整日志取值和输入隔离测试，再接入路由 Enricher；保持生命周期行为与保护语义不变。
6. 增加 Alert Enrich payload 与 `EnrichStatus` 一致性校验。
7. 更新配置指南、EventSource 模块、Lifecycle 模块、define 和 Kafka 输出契约。

### 阶段 B：基础监控矩阵

按以下调用链完成字段/依赖矩阵：

```text
entry → BasicEventProcessor → BaseConverter → BaseClear
```

覆盖顺序：

1. MonitorSource
2. CollectTask
3. NoData
4. SystemMetric
5. UptimeCheck

每条矩阵记录触发条件、Alert 输入、Data Source、Processor Value、0 条行为、故障行为、源码证据和测试样例。

当前实现进度（2026-09-03）：阶段 A 已完成，阶段 C 已完成一个
BASE_COLLECT 样例贯通。`strategy/resource/display/metric/source` 已可按 EventSource 配置运行；
resource 已通过 `datasources.OneModelClient` 直接读取 OneModel Elasticsearch，平台历史与鲸眼配置已使用真实 MySQL Adapter。
CMDB 业务拓扑、Redis 动态分组及其余远端服务联调仍待访问条件具备后完成。
阶段 B 的完整 MonitorSource、CollectTask、NoData、SystemMetric、UptimeCheck 行为矩阵，以及阶段 D
各扩展分类仍属于后续工作。

### 阶段 C：首个贯通切片

先实现配置链：

```text
strategy → resource → display → metric → source
```

使用一个 BASE_COLLECT 样例贯通：Alert → Processor 列表 → 总状态 → Alert 存储 → Kafka 快照。

外部查询暂按第 9.4 节使用固定 mock 数据贯通；该阶段的样例验证不依赖真实 DataSource 可用，不替代后续真实数据源集成验证。

### 阶段 D：扩展

依次增加：

1. DATA
2. LOG_METRIC / LOG_KEYWORD
3. UPTIME_CHECK 主分类
4. VMWARE
5. APM 条件分支
6. K8s 条件分支

## 14. 测试要求

### 14.1 配置测试

- 空链合法并产生 Noop。
- Processor 顺序得到保留。
- 未注册类型在 assembly 构造阶段启动失败。
- 重复类型启动失败。
- Clone 后修改列表不影响原配置。

### 14.2 编排器测试

- Enrich 入参为 `EnrichInput{Alert}`，编排器测试只需构造 Alert 与依赖，不构造 Event。
- 验证路由、查询条件和输出读取 Alert 的对应字段；覆盖 Labels、Dimensions、ExtraData 等动态字段的深拷贝隔离，Processor 修改 `Scope.Alert()` 返回的副本不影响 Scope 或调用方。
- Alert 字段缺失或非法时按对应规则处理，不引入 Event 查询或其他隐式来源补齐。
- 结果列表顺序与配置一致。
- 空链、全成功、全失败、部分成功、成功与失败混合的聚合状态。
- 不适用 Processor 返回 succeeded 空 Value。
- 单 Processor panic/意外错误转换为 failed 后继续执行。
- 父 Context 取消停止执行。
- Payload 解码后验证 `Alert.EnrichStatus == Payload.Status`。
- 每个列表项只有一个 Processor key。

### 14.3 Data Source 测试

当前先用 mock Adapter 和测试替身验证接口消费与返回语义；涉及真实连接、表结构、查询协议、权限及真实租户隔离的集成验证，在具备访问条件后执行并单独记录结果。

- 固定样例按租户与查询条件匹配，未命中返回未找到，不跨租户返回数据。
- 相同输入返回稳定结果，返回值修改不污染后续调用；Context 取消有效。
- 租户连接选择。
- 0 条与查询故障区分。
- 平台历史 ID、策略 ID 和 content 业务 ID 联合校验。
- StrategyConfig → CloudStrategyConfig 的 0 条回退。
- 多条取第一条。
- Redis key、OneModel ES 路由和租户过滤。
- 同一 Scope 内查询只执行一次。

### 14.4 Processor 测试

直接使用 Alert 与测试 Data Sources 构造 Scope，无需构造 Event 或 EnrichInput。

每个 Processor 至少覆盖：

- 正常输出。
- 不适用。
- 合法空结果。
- 缺字段与类型错误。
- 单依赖失败。
- 部分字段成功。
- 构造 Scope 后修改传入 Alert 的动态字段，不影响 Scope 内保存的副本；修改 `Scope.Alert()` 返回的动态字段，不影响 Scope、调用方或后续 Processor。
- JSON 合法空值与字段省略规则。

### 14.5 生命周期测试

- 首次创建与等级升级时，传给 Enricher 的 Alert 包含创建事件的继承字段，`TriggerEventID` 与 `BeginAt` 分别对应创建事件的 EventID 与 OccurredAt。
- 将原修改 `input.Event.Dimensions` 的输入隔离用例改为修改 `input.Alert` 的动态字段，验证保存的 Alert 与来源 Event 均不受影响。
- 丰富降级日志的租户、事件与告警 ID 来自保护函数持有的 normalized Alert；Enricher 修改其输入副本后返回 error 或 panic，也不能污染日志身份。
- 首次创建与等级升级执行丰富。
- 同级更新、恢复和关闭保留原丰富结果。
- CAS 冲突重试可重复执行。
- Processor 全失败时 Alert 仍可创建。
- Repository 与 FinalHook 得到完整 `{status, processors}`。

并发、Redis、ES 或共享状态相关实现完成后执行对应 race test，最终运行 `make check`。

## 15. 首阶段验收标准

框架阶段完成需同时满足：

- `internal/lifecycle/enrich` 成为 Enrichment 根模块。
- EventSource 能配置有序 Processor 链。
- Enricher 使用仅含 Alert 字段的 `lifecycle.EnrichInput`，保持 `Enrich(ctx, input EnrichInput)` 方法；Alert 按只读深拷贝处理，调用保护不再接收或克隆 Event。
- Scope 的业务输入只持有 Alert 深拷贝，不保存 Event 或整个 EnrichInput；后续路由、查询条件和字段加工全部从 Alert 对应字段读取，不依赖或回退读取 Event。
- `Alert.enrich` 严格输出两个顶层 key：`status` 和 `processors`。
- 每个 Processor 结果为单 key map，包含 status、value 和可选 diagnostics。
- 总状态算法通过完整组合测试。
- 外部读取实现集中在 `enrich/datasources`。
- Lifecycle 取消、panic 和非法结果保护继续有效。

首个业务切片完成需同时满足：

- BASE_COLLECT 至少一个真实分支完成字段与依赖矩阵。
- `strategy → resource → display → metric → source` 配置可运行。
- 固定依赖响应下生成稳定 Processor 列表。
- 当前可使用第 9.4 节的 mock 数据完成样例验收，并明确记录各数据源使用 mock，不据此声明真实数据源已接通。
- Alert 存储和 Kafka 输出包含相同丰富结构。
- 未完成的分类和真实外部联调明确记录。
