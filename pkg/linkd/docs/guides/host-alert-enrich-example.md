# 主机推送告警 Enrich 示例

> **说明：** 本文仅保留为 Linkd 对接 alarmd 告警事件，以及内置 Enrich 所需数据源依赖的场景示例。
> 标准告警事件的字段、必填规则和默认值请参考 [Linkd 标准事件](../reference/contracts/standard-event.md)。

本文以 BASE_COLLECT 主机 CPU 告警为例，按照当前 `standard` 输入格式，展示消息如何经过 Cleaner、Lifecycle 和 Enrich，最终关联平台策略、鲸眼策略、OneModel 主机实例、指标库和告警源。

本示例供 alarmd 推送给 Linkd 时参考，alarmd 生产者的实际接入状态需单独验证。Linkd 侧对应实现与回归测试位于：

```text
internal/enrich/assembly/router_test.go
TestBaseCollectRawEventRunsLifecycleEnrichment
```

## 1. 处理路径

当前 Enrich 接收 Event，在策略与生命周期裁决前保存结果，完整路径为：

```text
Kafka Record
  → RawEventMessage
  → StandardCleaner
  → EventDraft
  → EventFactory
  → Event
  → Enrich
      → strategy
      → resource
      → display
      → metric
      → source
  → Event 丰富结果 CAS
  → 策略与生命周期裁决
  → 创建或更新 Alert
```

每条不同 Event（包括更新、恢复和关闭）都先执行并保存自己的丰富结果；已冻结结果的重投直接复用。
新 Alert 复制 opening Event 对应等级的结果，已有 Alert 保留首次快照，不随后续 Event 刷新。
`update_current` 升级也保留该快照；`close_and_create` 新建的 Alert 复制本次 Event 的丰富结果，
不为新 Alert 再执行一遍 Enrich。

## 2. Kafka 输入

### 2.1 RawEventMessage 信封

Kafka Adapter 将下节的 Kafka Value 放入 `RawEventMessage.Payload`，并形成以下信封字段：

| 字段 | 示例 | 来源 |
| --- | --- | --- |
| `RecordID` | `kafka-linkd-base-collect-0001` | Kafka topic、partition、offset 形成的稳定记录身份 |
| `BKTenantID` | `tenant-1` | Kafka Header `bk_tenant_id`；Adapter 读取后写入信封 |
| `EventSourceID` | `built_in_bk` | 消费该 Topic 的 EventSource 配置 |
| `ReceivedAt` | `2026-09-01T00:00:02Z` | Kafka Record timestamp |
| `Payload` | 下节的 JSON | Kafka Value |

`StandardCleaner` 还会读取 Value 中的 `bk_tenant_id`。本例的 `related_tenant_id` 为空，
Header 与 Value 同时提供租户时必须一致；只提供其中一处也可。若配置了非空 `related_tenant_id`，
EventFactory 使用该配置覆盖租户；最终租户不能为空。

### 2.2 Kafka Value

以下 JSON 可以直接作为消息体。每个级别的 `severity`、`action` 和 `action_reason` 放在
`evaluations` 中，本次观测数值放在 `values` 中：

```json
{
  "bk_tenant_id": "tenant-1",
  "event_id": "source-event-1",
  "alert_id": "source-alert-1",
  "title": "CPU usage is high",
  "content": "Host 10.0.0.1 CPU usage reached 92.5%",
  "values": {
    "value": 92.5
  },
  "evaluations": [
    {
      "severity": "warning",
      "action": "triggered",
      "action_reason": ""
    }
  ],
  "dimensions": {
    "bk_inst_id": 101,
    "bk_target_ip": "10.0.0.1",
    "bk_target_cloud_id": 0
  },
  "subject": {
    "system": "cmdb",
    "type": "host",
    "id": "101",
    "name": "host-101"
  },
  "occurred_at": "2026-09-01T00:00:00Z",
  "produced_at": "2026-09-01T00:00:01Z",
  "labels": {
    "strategy_id": 123,
    "strategy_version": 1,
    "bk_biz_id": 2
  },
  "extra_data": {
    "anomaly_begin_time": "2026-09-01T00:00:00Z",
    "additional_dimensions": {
      "bk_host_id": 101
    }
  }
}
```

`event_id` 和 `alert_id` 分别是来源事件身份与来源告警身份，映射为 Event 的 `source_event_id` 和
`source_alert_id`；Linkd 内部 Event ID 由 EventFactory 生成。本例按 `source_alert_id` 关联，
因此 `alert_id` 必须非空，并在同一来源告警的后续消息中保持一致。生产者按该稳定 `alert_id`
选择 Kafka 分区；每次新的判定使用新的 `event_id`，重试和重投保持原事件身份。

### 2.3 告警等级

`evaluations[].severity` 使用 alarmd 输出的等级标识符，例如 `critical`、`warning`、`info`。
本例使用默认等级表和同名映射，因此 Kafka Value、Event 的判定项和新 Alert 的 severity 均为 `warning`。
使用其他来源标识符时，在 EventSource 的 `severity_mapping` 中配置对应关系。

`evaluations` 的通用数量、动作和来源等级映射约束见[Linkd 标准事件](../reference/contracts/standard-event.md#linkd-standard-通用输入规则)。
本例恢复或关闭时，对应等级分别发送 `resolved` 或 `closed`。

旧格式顶层的 `severity`、`action` 和 `action_reason` 不再用于构造判定，发送方需改用 `evaluations`。

例如，保留上面消息的其余字段，将 `evaluations` 替换为以下数组，即可表达同一数据点的两个级别判定：

```json
[
  { "severity": "critical", "action": "triggered", "action_reason": "严重阈值触发" },
  { "severity": "warning", "action": "resolved", "action_reason": "警告级别恢复" }
]
```

若当前有 warning 活动告警，更高级别的 critical 触发优先，按全局升级策略处理；warning 的恢复判定
记为被升级替代。若当前没有活动告警，则创建 critical 告警，warning 的恢复判定记为 orphaned。
后文 Enrich 输入和输出仍以单个 warning 触发的完整示例为准。

### 2.4 补充维度

`extra_data.additional_dimensions` 保存 alarmd 在原始数据维度之外补充的维度。字段名沿用现有监控链路的
`additional_dimensions`，并与顶层 `dimensions` 的来源事实边界保持清晰。

本例将告警检测维度写入顶层 `dimensions`，仅用于展示与资源丰富的补充维度写入
`extra_data.additional_dimensions`；两处的 key 保持互斥。通用类型与关联规则见
[Linkd 标准事件](../reference/contracts/standard-event.md#dimensions严格完整的告警维度)。本例直接使用
`source_alert_id` 作为 fingerprint。

### 2.5 时间语义

| 字段 | 定义 | alarmd 来源 |
| --- | --- | --- |
| `occurred_at` | 当前触发或恢复事件对应的事实发生时间 | 当前判定对应的 `record_ref.source_time` |
| `produced_at` | alarmd 首次生成本事件消息的时间 | 消息首次构造时刻；重试和重投保持原值 |
| `extra_data.anomaly_begin_time` | 本次异常周期中的首次异常点时间 | 触发窗口内最早异常点时间 |

`produced_at` 表达消息生产时间。Kafka send、broker append 和 producer ack 属于后续传输阶段；单独记录
Kafka 发布时间时使用 `published_at`，以区分消息生产时间和传输时间。

正常实时链路通常满足 `occurred_at <= produced_at <= received_at`。延迟数据、跨机器时钟偏差和历史重放
可能改变该顺序，因此该关系仅用于延迟观测。

### 2.6 观测数值

本例的 `values` 为 `{"value": 92.5}`，表示本次判定的 CPU 使用率数值快照；格式与缺失时的
处理见[Linkd 标准事件](../reference/contracts/standard-event.md#linkd-standard-通用输入规则)。

`values` 不参与 fingerprint，也不替代 `dimensions`、`labels` 或 `extra_data`。
当前数值快照保存在 Event 中，Alert 没有 `values` 或 `evaluations` 字段；本例的 Metric Processor
仍通过策略和指标库生成指标信息及查询参数，Display Processor 使用传入的 `content`。

## 3. EventSource 配置

```yaml
event_sources:
  - event_source_id: built_in_bk
    related_tenant_id: ""
    enabled: true

    cleaner:
      type: standard

    fingerprint_mode: field
    fingerprint_field: source_alert_id

    severity_mapping:
      warning: warning
    default_severity: warning

    enrich:
      processors:
        - type: strategy
          config:
            web_saas_module_url: https://kingeye.example.com/
        - type: resource
        - type: display
        - type: metric
        - type: source

    storage:
      type: kafka
      kafka:
        brokers:
          - 127.0.0.1:9092
        topic: linkd-base-collect
        consumer_group: linkd-base-collect-cleaner
        security:
          protocol: plaintext
```

这里使用：

```yaml
fingerprint_mode: field
fingerprint_field: source_alert_id
```

`StandardCleaner` 将 payload 的：

```json
{
  "alert_id": "source-alert-1"
}
```

投影为：

```go
Event.SourceAlertID = "source-alert-1"
```

`EventFactory` 使用该稳定来源身份作为 fingerprint，使同一租户、同一 EventSource 下的后续事件进入
同一生命周期关联范围。该范围最多有一个 active Alert；等级升级的 `close_and_create` 或告警终结后
再次触发会创建新的 Alert ID，因此相同 fingerprint 可以先后关联多条 Alert。

## 4. Enrich 输入 Event

Cleaner 和 EventFactory 生成 Event；Enrich 为各 evaluation 生成对应等级的结果，再由 Lifecycle
裁决是否创建 Alert。本例只有 warning 触发。进入 Enrich 前的核心字段节选如下；完整身份、指纹与
来源原文由 EventFactory 填入，不从 Alert 反向构造输入：

```go
domain.Event{
    BKTenantID:    "tenant-1",
    EventSourceID: "built_in_bk",
    EventSourceVersion: 1,
    EventEnrichment: domain.EventEnrichment{EnrichStatus: domain.EnrichStatusPending},

    Title:         "CPU usage is high",
    Content:       "Host 10.0.0.1 CPU usage reached 92.5%",
    Evaluations: []domain.EventEvaluation{{Severity: "warning", Action: domain.EventActionTriggered}},

    Dimensions: domain.DimensionMap{
        "bk_inst_id":         numberScalar(101),
        "bk_target_ip":       stringScalar("10.0.0.1"),
        "bk_target_cloud_id": numberScalar(0),
    },

    SubjectSystem: "cmdb",
    SubjectType:   "host",
    SubjectID:     "101",
    SubjectName:   "host-101",

    SourceEventID: "source-event-1",
    SourceAlertID: "source-alert-1",

    Labels: domain.DimensionMap{
        "strategy_id":      numberScalar(123),
        "strategy_version": numberScalar(1),
        "bk_biz_id":        numberScalar(2),
    },

    OccurredAt:     time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
    ProducedAt:     time.Date(2026, 9, 1, 0, 0, 1, 0, time.UTC),
    ReceivedAt:     time.Date(2026, 9, 1, 0, 0, 2, 0, time.UTC),
    CreateAt:       time.Date(2026, 9, 1, 0, 0, 2, 0, time.UTC),

    ExtraData: domain.JSONObject{
        "anomaly_begin_time": json.RawMessage(`"2026-09-01T00:00:00Z"`),
        "additional_dimensions": json.RawMessage(`{
            "bk_host_id": 101
        }`),
    },
}
```

### 4.1 BASE_COLLECT 必需标签

```json
{
  "strategy_id": 123,
  "strategy_version": 1,
  "bk_biz_id": 2
}
```

`strategy_id`、`strategy_version` 和 `bk_biz_id` 必须是大于零的整数。缺失字段产生 `missing_field`；
字符串、小数、零和负数产生 `invalid_field`。

### 4.2 Processor 输入矩阵

| Processor | 直接读取的 Event 字段 | 共享 Context / 外部依赖 |
| --- | --- | --- |
| `strategy` | `labels.strategy_id`、`labels.strategy_version`、`labels.bk_biz_id` | BK Strategy Snapshot、CW Strategy；实例策略 URL 还使用 OneModel |
| `resource` | 三个必需标签、`dimensions`、`extra_data.additional_dimensions` | CW Strategy、OneModel |
| `display` | `subject_name`、`dimensions`、`extra_data.additional_dimensions`、`content` | CW Strategy、BK Strategy Snapshot、指标目录、Resource Context |
| `metric` | 三个必需标签、`dimensions`、`extra_data.anomaly_begin_time` | CW Strategy、BK Strategy Snapshot、指标目录 |
| `source` | `bk_tenant_id`、`event_source_id`、`source_event_id` | AlarmSource |

## 5. 主机实例定位

`resource` Processor 读取 CW Strategy 的：

```text
object_model_code = cw-Host
```

存在 `dimensions.bk_inst_id=101` 时优先构造：

```go
enrich.InstanceQuery{
    ModelCode:  "cw-Host",
    InstanceID: "101",
}
```

数值维度 `101` 通过 `ScalarIdentity` 转换为稳定字符串 `"101"`，并查询 OneModel 根字段 `model_inst_id`。

主机实例 ID 缺失时，地址回退查询使用 `attribute_values` 的类型化投影：

```go
enrich.InstanceQuery{
    ModelCode: "cw-Host",
    AttributeFilters: []enrich.InstanceAttributeFilter{
        {Field: "bk_host_innerip", Type: enrich.InstanceAttributeKeyword, Value: "10.0.0.1"},
        {Field: "bk_cloud_id", Type: enrich.InstanceAttributeLong, Value: float64(0)},
    },
}
```

## 6. 配套依赖数据

RawEvent 只提供来源事实。五个 Processor 的完整输出还依赖以下外部记录。

### 6.1 平台策略版本快照

查询身份：

```text
strategy_id      = 123
strategy_version = 1
```

策略适配器按租户和 `strategy_id` 查询 `alarm_strategy_set_split_record.id`，
要求 `source_resource_version == strategy_version` 且记录启用、active、published。
配置内容来自该行 payload，旧配置表和历史表均不读取。发布业务与来源业务需通过丰富处理器的业务边界校验：

```text
bk_biz_id = 2
```

首个监控项的查询配置示例：

```json
{
  "id": 1,
  "unit": "percent",
  "alias": "A",
  "metric_id": "bk_monitor.system.cpu.usage",
  "agg_method": "avg",
  "agg_interval": 60,
  "metric_field": "usage",
  "agg_condition": [],
  "agg_dimension": [],
  "result_table_id": "system.cpu",
  "data_source_label": "bk_monitor",
  "data_type_label": "time_series"
}
```

### 6.2 鲸眼策略

鲸眼策略通过 Kafka `labels.strategy_id=123` 对应拆分记录主键。
下面是从 `payload.resolved_strategies` 转换出的内部丰富视图，不能将其当作旧配置表行。
默认与覆盖各用自身的拆分主键；`status.bk_strategy_id` 由适配器设为该主键。

关键数据：

```json
{
  "kind": "Strategy",
  "name": "CPU 使用率",
  "is_default": true,
  "monitor_template_id": 1,
  "config_id": "strategy-config-1",
  "object_model_code": "cw-Host",
  "spec": {
    "name": "CPU 使用率",
    "alarm_alias": "CPU 使用率过高",
    "data_source": "system",
    "strategy_item": {
      "agg_method": "avg",
      "agg_interval": 60,
      "functions": []
    }
  },
  "status": {
    "bk_strategy_id": 123
  }
}
```

### 6.3 OneModel 主机实例

统一实例 alias `kingeye_all_instance` 中的文档至少提供：

```json
{
  "bk_tenant_id": "tenant-1",
  "model_id": "cw-Host",
  "model_inst_id": "101",
  "entity_uid": "cw-Host|101",
  "bk_biz_ids": [2],
  "attributes": {
    "bk_obj_id": "host",
    "bk_host_id": 101,
    "bk_biz_id": 2,
    "bk_biz_name": "业务 2",
    "bk_host_innerip": "10.0.0.1",
    "bk_cloud_id": 0,
    "bk_cloud_name": "默认区域"
  },
  "attribute_values": [
    {"field_name": "bk_host_id", "long_values": [101]},
    {"field_name": "bk_host_innerip", "keyword_values": ["10.0.0.1"]},
    {"field_name": "bk_cloud_id", "long_values": [0]}
  ]
}
```

OneModel Client 会在查询中强制追加 `bk_tenant_id` 和 `model_id`，按 `model_inst_id` 或 nested `attribute_values` 查询，并在返回后复核租户、模型、实例和 `entity_uid`。Resource Processor 合并读取根字段与 `attributes`。

### 6.4 指标库

`metric` 需要包含类似指标定义（对应来源另登记在 `metricset`）：

```text
bk_tenant_id    = tenant-1
space_uid       = *
metricset_code  = system.cpu
metric_name     = usage
kind            = native
result_table_id = system.cpu
physical_field  = usage
model_id        = cw-Host
display_name    = CPU 使用率
unit            = percent
value_mapping   = []
dimensions      = [{"id":"bk_target_ip","name":"目标IP"}]
```

指标名称、单位和维度元数据统一读取 metricset 体系中的 `metric` 表。

### 6.5 告警源

`alarm_collect_alarmsource` 需要包含：

```text
bk_tenant_id = tenant-1
source_id     = built_in_bk
name          = 鲸眼监控
```

## 7. 预期 Enrich 结果

以下展示各 Processor 的结果片段。Event 按 `evaluations[].severity` 保存独立结果，完整信封见
[Event 丰富模型](../design/enrich.md)；新 Alert 只复制其 opening Event 对应等级的结果。

成功时，Event 的结果结构如下（省略完成时间和配置摘要，Processor value 在下文展开）：

```json
{
  "enrich_status": "succeeded",
  "enrich": {
    "evaluations": [
      {
        "severity": "warning",
        "status": "succeeded",
        "data": {
          "processors": [
            { "strategy": { "status": "succeeded", "value": {} } },
            { "resource": { "status": "succeeded", "value": {} } },
            { "display": { "status": "succeeded", "value": {} } },
            { "metric": { "status": "succeeded", "value": {} } },
            { "source": { "status": "succeeded", "value": {} } }
          ]
        }
      }
    ]
  }
}
```

本示例的关键输出如下。

### 7.1 Strategy

Strategy 输出沿用 Kafka labels 中的策略身份字段名：

```json
{
  "strategy_id": 123,
  "strategy_version": 1,
  "monitor_template_id": 1,
  "strategy_config_id": "strategyconfig1",
  "strategy_name": "CPU 使用率",
  "url": "/#/kmc/manage/monitorStrategy/monitorStrategyDetails?id=1&strategy_config_id=strategyconfig1",
  "data_source": "system"
}
```

`strategy_config_id` 会移除配置 ID 中的连字符。JSON 编码 URL 时，`&` 可能显示为 `\u0026`，解码后的 URL 语义一致。

### 7.2 Resource

```json
{
  "bk_obj_id": "host",
  "bk_inst_id": 101,
  "model_id": "cw-Host",
  "model_inst_id": "101",
  "model_name": "",
  "bk_biz_id": 2,
  "bk_biz_name": "业务 2",
  "bk_set_id": null,
  "bk_set_name": "",
  "bk_module_id": null,
  "bk_module_name": "",
  "bk_cloud_id": 0,
  "bk_cloud_name": "默认区域",
  "cloud_plat_id": null,
  "dynamic_group_id": [],
  "cw_labels": []
}
```

所有类型化字段都会输出；空值由下游按字段语义解释。

### 7.3 Display

```json
{
  "title": "CPU 使用率过高",
  "content": "Host 10.0.0.1 CPU usage reached 92.5%",
  "object": "host-101",
  "dimensions": [
    {
      "name": "bk_host_id",
      "value": 101,
      "real_key": "bk_host_id",
      "real_value": 101
    }
  ],
  "dimension_text": "bk_host_id(101)"
}
```

标题优先使用鲸眼策略的 `alarm_alias`。主机对象优先使用 Event 的 `subject_name`。
`extra_data.additional_dimensions` 中的 `bk_host_id` 进入维度展示；存在维度元数据时使用翻译后的名称，
示例展示缺少元数据时的字段名回退结果。

### 7.4 Metric

```json
{
  "display_name": "CPU 使用率",
  "metric_name": "usage",
  "unit": "percent",
  "result_table_id": "system.cpu",
  "metric_unique_id": "system.cpu.usage",
  "aggregate_func": "avg",
  "time_interval": 60,
  "where_condition": "bk_inst_id='101' and bk_target_cloud_id='0' and bk_target_ip='10.0.0.1'",
  "anomaly_begin_time": "2026-09-01T00:00:00Z"
}
```

`metric_query_params` 还会包含平台查询配置转换后的 `query_configs`、`function.time_compare` 和 `bk_biz_id`，这里省略展开内容。

### 7.5 Source

```json
{
  "source_id": "built_in_bk",
  "source_name": "鲸眼监控",
  "meta_info": "source-event-1"
}
```

## 8. 失败定位

常见结果：

| 条件 | 结果 |
| --- | --- |
| `strategy_id`、`strategy_version` 或 `bk_biz_id` 缺失或非法 | 相关 Processor 返回 failed，并携带 `missing_field` 或 `invalid_field` |
| 对应版本的平台策略快照未命中或内容非法 | 相关策略处理器产生 `dependency_invalid=kingeye_strategy`；文案模式使用明确的发布身份/版本错误 |
| CW Strategy 未命中 | 相关 Processor 产生 `dependency_invalid=kingeye_strategy` |
| OneModel 查询失败 | resource 产生 `dependency_invalid=onemodel` |
| 指标目录查询失败 | display/metric 产生 `dependency_invalid=metric_catalog` |
| AlarmSource 查询失败 | source 产生 `dependency_invalid=alarm_source` |
| 部分 Processor 成功、部分失败 | 顶层 `enrich_status=partial` |

Processor error 和 panic 会被 Chain 隔离，后续 Processor 继续执行。Enricher error、panic 或非法 payload
由 Lifecycle 降级为逐 evaluation 的 failed payload，保存到 Event 后继续裁决；不因此承诺一定创建 Alert。
取消和核心存储/CAS 失败仍走原重试或取消路径。

## 9. 实现验证

运行现有回归测试，验证 StandardCleaner、EventFactory、Lifecycle 与五个 Enrich Processor 的链路：

```bash
go test ./internal/enrich/assembly \
  -run TestBaseCollectRawEventRunsLifecycleEnrichment \
  -count=1
```

该回归测试使用内存 Repository 和模拟依赖数据，覆盖以下场景；真实 Kafka、MySQL、Elasticsearch
以及 alarmd 生产者需另行集成验证：

- Kafka 等价消息信封及 Value 字段；
- `strategy_id + strategy_version` 策略版本身份；
- `additional_dimensions` 的传递和展示；
- occurred、produced、received 三类时间映射；
- EventSource 配置；
- 平台策略和鲸眼策略；
- OneModel 主机实例；
- 指标目录；
- AlarmSource；
- Event 丰富后由 Lifecycle 创建 Alert；
- 五个 Enrich Processor 的关键输出断言。

相关契约：

- [Linkd 标准事件](../reference/contracts/standard-event.md)
- [Lifecycle 模块](../modules/lifecycle.md)
- [Event Enrich 现行设计](../design/enrich.md)
