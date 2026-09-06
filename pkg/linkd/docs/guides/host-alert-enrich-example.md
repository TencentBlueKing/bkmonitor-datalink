# 主机推送告警 Enrich 示例

本文以 BASE_COLLECT 主机 CPU 告警为例，展示一条 Kafka 消息如何经过 Cleaner、Lifecycle 和 Enrich，最终关联平台策略、鲸眼策略、OneModel 主机实例、指标库和告警源。

示例与当前测试保持一致：

```text
internal/lifecycle/enrich/assembly/router_test.go
TestBaseCollectRawEventRunsLifecycleEnrichment
```

## 1. 处理路径

当前 Enrich 接收 Lifecycle 创建的新 Alert，完整路径为：

```text
Kafka Record
  → RawEventMessage
  → StandardCleaner
  → EventDraft
  → EventFactory
  → Event
  → Lifecycle 创建 Alert
  → Enrich
      → strategy
      → resource
      → display
      → metric
      → source
```

Enrich 在新 Alert 持久化前同步执行。同等级更新、恢复和关闭沿用 Alert 已保存的丰富结果。

## 2. Kafka 输入

### 2.1 RawEventMessage 信封

Kafka Adapter 会形成以下等价信封：

```go
cleaner.RawEventMessage{
    RecordID:      "kafka-linkd-base-collect-0001",
    BKTenantID:    "tenant-1",
    EventSourceID: "built_in_bk",
    ReceivedAt:    time.Date(2026, 9, 1, 0, 0, 2, 0, time.UTC),
    Headers: map[string][]byte{
        "bk_tenant_id": []byte("tenant-1"),
    },
    Payload: []byte(`{
      "event_id": "source-event-1",
      "alert_id": "source-alert-1",
      "title": "CPU usage is high",
      "content": "Host 10.0.0.1 CPU usage reached 92.5%",
      "severity": "warning",
      "action": "triggered",
      "action_reason": "",
      "condition_key": "cpu_usage",
      "condition_name": "CPU 使用率",
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
        "bk_strategy_id": 123,
        "bk_strategy_history_id": 70001,
        "bk_biz_id": 2
      },
      "extra_data": {
        "anomaly_begin_time": "2026-09-01T00:00:00Z"
      }
    }`),
}
```

信封字段来源：

| 字段 | 示例 | 来源 |
| --- | --- | --- |
| `RecordID` | `kafka-linkd-base-collect-0001` | Kafka topic、partition、offset 形成的稳定记录身份 |
| `BKTenantID` | `tenant-1` | Kafka Header `bk_tenant_id` |
| `EventSourceID` | `built_in_bk` | 消费该 Topic 的 EventSource 配置 |
| `ReceivedAt` | `2026-09-01T00:00:02Z` | Kafka Record timestamp |
| `Payload` | 标准事件 JSON | Kafka Value |

### 2.2 Kafka Value

以下 JSON 可以直接作为消息体：

```json
{
  "event_id": "source-event-1",
  "alert_id": "source-alert-1",
  "title": "CPU usage is high",
  "content": "Host 10.0.0.1 CPU usage reached 92.5%",
  "severity": "warning",
  "action": "triggered",
  "action_reason": "",
  "condition_key": "cpu_usage",
  "condition_name": "CPU 使用率",
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
    "bk_strategy_id": 123,
    "bk_strategy_history_id": 70001,
    "bk_biz_id": 2
  },
  "extra_data": {
    "anomaly_begin_time": "2026-09-01T00:00:00Z"
  }
}
```

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
"alert_id": "source-alert-1"
```

投影为：

```go
Event.SourceAlertID = "source-alert-1"
```

`EventFactory` 使用该稳定来源身份生成 fingerprint，使同一来源告警的后续 triggered、resolved 和 closed Event 关联到同一个 Alert。

## 4. Enrich 输入 Alert

Cleaner 和 EventFactory 生成 Event；Lifecycle 使用 opening Event 构造 Alert。进入 Enrich 前的核心字段如下：

```go
domain.Alert{
    BKTenantID:    "tenant-1",
    EventSourceID: "built_in_bk",

    Title:         "CPU usage is high",
    Content:       "Host 10.0.0.1 CPU usage reached 92.5%",
    Severity:      "warning",
    ConditionKey:  "cpu_usage",
    ConditionName: "CPU 使用率",

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
        "bk_strategy_id":         numberScalar(123),
        "bk_strategy_history_id": numberScalar(70001),
        "bk_biz_id":              numberScalar(2),
    },

    ExtraData: domain.JSONObject{
        "anomaly_begin_time": json.RawMessage(`"2026-09-01T00:00:00Z"`),
    },
}
```

### 4.1 BASE_COLLECT 必需标签

```json
{
  "bk_strategy_id": 123,
  "bk_strategy_history_id": 70001,
  "bk_biz_id": 2
}
```

三个值必须是大于零的整数。缺失字段产生 `missing_field`；字符串、小数、零和负数产生 `invalid_field`。

### 4.2 Processor 输入矩阵

| Processor | 直接读取的 Alert 字段 | 共享 Context / 外部依赖 |
| --- | --- | --- |
| `strategy` | 三个 BASE_COLLECT 标签 | BK Strategy History、CW Strategy；实例策略 URL 还使用 OneModel |
| `resource` | 三个标签、`dimensions.bk_inst_id`；主机地址回退使用 `bk_target_ip + bk_target_cloud_id` | CW Strategy、OneModel |
| `display` | `subject_name`、`dimensions`、`content` | CW Strategy、BK Strategy History、MetricLibrary、Resource Context |
| `metric` | 三个标签、`dimensions`、`extra_data.anomaly_begin_time` | CW Strategy、BK Strategy History、MetricLibrary |
| `source` | `bk_tenant_id`、`event_source_id`、`source_event_id` | AlarmSource |

## 5. 主机实例定位

`resource` Processor 读取 CW Strategy 的：

```text
object_model_code = cw-Host
```

存在 `dimensions.bk_inst_id=101` 时优先构造：

```go
enrich.InstanceQuery{
    ModelCode: "cw-Host",
    Filters: map[string]any{
        "cw_object_model_inst_id": "101",
    },
}
```

数值维度 `101` 通过 `ScalarIdentity` 转换为稳定字符串 `"101"`，与 OneModel `cw_object_model_inst_id` 的文本身份一致。

主机实例 ID 缺失时，地址回退查询使用：

```go
enrich.InstanceQuery{
    ModelCode: "cw-Host",
    Filters: map[string]any{
        "bk_host_innerip": "10.0.0.1",
        "bk_cloud_id":     float64(0),
    },
}
```

## 6. 配套依赖数据

RawEvent 只提供来源事实。五个 Processor 的完整输出还依赖以下外部记录。

### 6.1 平台策略历史

查询身份：

```text
strategy_id = 123
history_id  = 70001
```

历史内容中的业务必须匹配：

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

索引中的文档至少提供：

```json
{
  "bk_tenant_id": "tenant-1",
  "cw_object_model_code": "cw-Host",
  "cw_object_model_inst_id": "101",
  "bk_obj_id": "host",
  "bk_host_id": 101,
  "bk_biz_id": 2,
  "bk_biz_name": "业务 2",
  "bk_host_innerip": "10.0.0.1",
  "bk_cloud_id": 0,
  "bk_cloud_name": "默认区域"
}
```

OneModel Client 会在查询中强制追加 `bk_tenant_id` 和 `cw_object_model_code`，并在返回后再次校验租户、模型和实例身份。

### 6.4 指标库

`home_application_monitormetriclibrary` 需要包含类似记录：

```text
bk_tenant_id      = tenant-1
table_id          = system.cpu
field_name        = usage
object_model_code = cw-Host
field_cn_name     = CPU 使用率
unit              = percent
is_deleted        = false
```

MonitorMetric 表已经废弃，指标名称、单位和维度元数据统一读取 MonitorMetricLibrary。

### 6.5 告警源

`alarm_collect_alarmsource` 需要包含：

```text
bk_tenant_id = tenant-1
source_id     = built_in_bk
name          = 鲸眼监控
```

## 7. 预期 Enrich 结果

成功时，顶层协议为：

```json
{
  "status": "succeeded",
  "processors": [
    { "strategy": { "status": "succeeded", "value": {} } },
    { "resource": { "status": "succeeded", "value": {} } },
    { "display": { "status": "succeeded", "value": {} } },
    { "metric": { "status": "succeeded", "value": {} } },
    { "source": { "status": "succeeded", "value": {} } }
  ]
}
```

本示例的关键输出如下。

### 7.1 Strategy

```json
{
  "bk_strategy_id": 123,
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
  "dimensions": [],
  "dimension_text": ""
}
```

标题优先使用鲸眼策略的 `alarm_alias`。主机对象优先使用 Alert 的 `subject_name`。

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
| 三个 BASE_COLLECT 标签缺失或非法 | 相关 Processor 返回 failed，并携带 `missing_field` 或 `invalid_field` |
| 平台策略历史未命中或内容非法 | strategy/display/metric 产生 `dependency_invalid=platform_strategy_history` |
| CW Strategy 未命中 | 相关 Processor 产生 `dependency_invalid=kingeye_strategy` |
| OneModel 查询失败 | resource 产生 `dependency_invalid=onemodel` |
| MetricLibrary 查询失败 | display/metric 产生 `dependency_invalid=metric_library` |
| AlarmSource 查询失败 | source 产生 `dependency_invalid=alarm_source` |
| 部分 Processor 成功、部分失败 | 顶层 `enrich_status=partial` |

Processor error 和 panic 会被 Chain 隔离，后续 Processor 继续执行。Enricher error、panic 或非法 payload 会由 Lifecycle 降级为固定 failed payload，同时保留 Alert 创建流程。

## 9. 本地验证

目标单元测试：

```bash
go test ./internal/lifecycle/enrich/assembly \
  -run TestBaseCollectRawEventRunsLifecycleEnrichment \
  -count=1
```

该测试落地了本文中的：

- Kafka 等价消息信封；
- Standard payload；
- EventSource 配置；
- 平台策略和鲸眼策略；
- OneModel 主机实例；
- MonitorMetricLibrary；
- AlarmSource；
- Lifecycle 创建 Alert；
- 五个 Enrich Processor 的关键输出断言。

相关契约：

- [Standard Raw Event](../reference/contracts/raw-event.md)
- [Lifecycle 模块](../modules/lifecycle.md)
- [Enrich Observation](../design/enrich-observation.md)
