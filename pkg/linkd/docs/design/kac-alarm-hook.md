# KAC Alarm Hook 设计

KAC 固定消息协议与字段映射校验位于 `internal/enrich/kingeye`，Kafka 发送运行时保留在 `internal/lifecycle/kachook`；配置包只依赖纯协议。

> 状态：已实现首版，并完成本地 PM2/Docker Kafka 端到端验证；目标 KAC 环境的消费与持久化仍待联调。
>
> 取证基线：Linkd `c1d490a9`；Kingeye `c78683a9eecf4b50840a9f58e7228d8fca3be4`。Kingeye 取证工作区存在未跟踪的 `src/envs/`，本文只读取源码与测试。

## 1. 背景与结论

Linkd 当前在 Alert 发生真实变化后，通过 EventSource 的具名 `FinalHook` 输出最终快照。现有 `type: kafka`
发送 Linkd 自有 Kafka Alert V1 信封，适合通用消费者；KAC 告警中心当前消费另一套扁平 JSON Alarm
消息，两套协议在字段语义、动作枚举、等级枚举、时间格式和 Kafka key 上均有差异。

本方案保留现有 `type: kafka`，新增 `type: kac`：

```text
Lifecycle FinalHookInput
  ├─ type: kafka → Linkd Kafka Alert V1
  └─ type: kac   → KAC Alarm JSON → KAC alarm_collect_topic
```

建议把 KAC 兼容转换和 Kafka 投递封装在 `internal/lifecycle/kachook`。Lifecycle 继续只依赖
`FinalHook.Execute(ctx, input)`，`domain.Alert` 保持 Linkd 领域模型，现有 Kafka Alert V1 保持原协议。

首版直接连接配置中的 KAC Kafka broker/topic，复用 Linkd 的 Kafka 安全配置和 producer 构造能力。
Kingeye 每批调用内部 API 获取连接、创建 producer、发送、flush、close 的做法属于旧运行时实现；Linkd
按 EventSource Release 持有长生命周期 client，并在任务换代排空后关闭。

## 2. 目标和非目标

### 2.1 目标

- 将 Lifecycle 已持久化的 Alert 变化转换成 KAC 当前 `ALARM_COLLECT_TOPIC` 可消费的单条 Alarm JSON；
- 保持创建、更新、恢复、关闭和等级升级的输出顺序；
- 使用稳定身份支持重试去重，并让同一 Linkd Alert 的消息进入同一 Kafka partition；
- 显式传播 `bk_tenant_id`，保证 KAC 存储和后续处理按租户分组；
- 从 Linkd 核心 Alert 与已保存的 Enrich Processor 结果生成 KAC 字段；
- 在 EventSource 发布前校验配置，在实际调用中报告转换或投递失败；
- 支持与通用 Kafka Hook 并存，用独立实例名、目标、指标和 AlertLog 审计两条输出。

### 2.2 非目标

- 不修改 `domain.Alert` 来承载 KAC DTO；
- 不修改 `type: kafka` 的 Kafka Alert V1 value、key、headers 或 message ID；
- 不把 Kingeye `alarm_callback` 的 Python 继承树和随机 ID 生成方式迁入 Linkd；
- 不通过 Hook 再次查询 Kingeye MySQL、Redis、OneModel 或策略服务；所需业务字段由 Alert Enrich 提供；
- 不在首版增加 Outbox、补偿扫描或 Hook 失败重放队列；
- 不改造 KAC 消费端的 offset 提交和存储流程；该链路现存风险单独治理；
- 不把 `alarm_callback` 的内部 `kingeye.base.alarm.AlarmEvent` 当作对外消息 DTO。

## 3. 取证：KAC 链路中的三层数据

理解 KAC 契约时需要区分以下三层。

### 3.1 alarm_callback 内部 AlarmEvent

Kingeye 的 `BaseProcessor.process_alarms` 先构造内部 `kingeye.base.alarm.AlarmEvent`，其 `id` 来自当前时间
生成的数值池；随后 Cleaner 再把它转换成待投递字典。该对象包含 Converter/Cleaner 的处理中间状态，
不直接发送到 Kafka。

证据路径均相对于 Kingeye 仓库根目录：

- `src/kingeye/kac/alarm_callback/processors/base.py:89-139`：构造内部 AlarmEvent，并处理 recovered/closed；
- `src/kingeye/kac/alarm_callback/converter/base.py:587-665`：形成内部事件字段；
- `src/kingeye/kac/alarm_callback/cleaner/base.py:303-327`：转换为最终字典。

### 3.2 alarm_callback 清洗后的 KAC Alarm 字典

`BaseClear.base_alarm_fields` 读取 SDK `Alarm.__annotations__`，逐个执行 `clean_<field>`，再追加
`field_extra_info`、`metric_query_params`、`dynamic_group_id`、`cw_labels` 及场景扩展字段。活动消息省略
`close_time` 和 `close_reason`。

SDK 基础字段见 `src/kingeye/kingeye_kit/module_sdk/kac/entity/alarm_entity.py:5-136`，实际组装规则见
`src/kingeye/kac/alarm_callback/cleaner/base.py:303-327`。SDK 构造器参数中存在 `strategy_info`，
`__annotations__` 未声明它，因此当前 `push_key` 不会输出该字段。

### 3.3 KAC alarm_collect_topic 的真实消费契约

Producer 对每个字典发送一条 UTF-8 JSON object，Kafka key 取 `event_id`，没有 headers；一批告警会产生
多条 record。证据见 `src/kingeye/kingeye_kit/module_sdk/kac/service/alarm_service.py:13-35`。

消费者逐条执行 UTF-8 decode、`json.loads` 和 `get_clean_alarm_schema()` 校验，再把合法 object 交给
`alarm_pipeline`。证据见 `src/kingeye/kac/alarm/management/commands/alarmpipeline.py:34-55`。

当前 JSON Schema 的最低要求见
`src/kingeye/kac/alarm_collect/clients/validate.py:88-108`：

| 字段 | 类型与约束 |
| --- | --- |
| `item` | string，允许空字符串 |
| `name` | string，至少 1 字符 |
| `event_id` | string，至少 1 字符 |
| `alarm_time` | string，形如 `YYYY-MM-DD HH:mm:ss` |
| `content` | string，至少 1 字符 |
| `action` | `firing \| resolved \| close \| heartbeat` |
| `level` | `remind \| warning \| fatal` |
| `object` | string，允许空字符串 |

Schema 没有设置 `additionalProperties: false`，因此 KAC 接受扩展字段。KAC 实际处理还依赖
`alarm_id`、`source_id` 和租户等字段；它们属于**运行语义必需字段**，强度高于当前 JSON Schema 的最低校验。
KAC `AlarmEvent` ES 模型已声明 `bk_tenant_id`，见
`src/kingeye/base/domains/alarm/models/alarm_event_document.py:180-229`。

## 4. 建议的 KAC Alarm V1 兼容消息

### 4.1 消息形态

每次 Hook 调用发送一个扁平 JSON object。首版保持 KAC 当前字段名及值类型，额外显式发送
`bk_tenant_id`。以下示例展示 active Alert 创建消息：

```json
{
  "alarm_id": "linkd-123e4567-e89b-52d3-a456-426614174000",
  "source_id": "built_in_bk",
  "source_name": "鲸眼监控",
  "item": "CPU 使用率",
  "metric_name": "usage",
  "name": "CPU 使用率过高",
  "event_id": "linkd-20260901000002.tenant-1.built_in_bk.a1b2c3d4e5f60708",
  "alarm_time": "2026-09-01 08:00:00",
  "content": "Host 10.0.0.1 CPU usage reached 92.5%",
  "action": "firing",
  "level": "warning",
  "object": "host-101",
  "bk_tenant_id": "tenant-1",
  "bk_biz_id": "2",
  "bk_biz_name": "业务 2",
  "bk_set_id": "",
  "bk_set_name": "",
  "bk_module_id": "",
  "bk_module_name": "",
  "bk_cloud_id": "0",
  "bk_cloud_name": "默认区域",
  "bk_obj_id": "host",
  "bk_inst_id": "101",
  "bk_service_id": "",
  "meta_info": "source-event-1",
  "strategy_id": "1",
  "strategy_name": "CPU 使用率",
  "dimension_info": "bk_host_id(101)",
  "model_inst_id": "101",
  "model_id": "cw-Host",
  "model_name": "主机",
  "anomaly_begin_time": "2026-09-01T00:00:00Z",
  "metric_unique_id": "system.cpu.usage",
  "result_table_id": "system.cpu",
  "Namespace": "",
  "time_interval": "60",
  "aggregate_func": "avg",
  "where_condition": "bk_inst_id='101'",
  "unit": "percent",
  "data_source": "system",
  "field_extra_info": {
    "strategy_name": {
      "url": "/#/kmc/manage/monitorStrategy/monitorStrategyDetails?id=1&strategy_config_id=strategyconfig1"
    }
  },
  "metric_query_params": "{\"expression\":\"A\",\"query_configs\":[]}",
  "dynamic_group_id": [],
  "cw_labels": []
}
```

该示例是本方案目标 DTO，字段值来自 Linkd 示例数据，未经过真实 KAC 环境验证。

### 4.2 身份、动作与时间

临时兼容说明：KAC 详情及相关 URL 路由当前使用 `[\w-]` 形式的路径参数约束，Linkd AlertID 中的句点无法匹配。
KAC Hook 因此将 `alarm_id` 改写为带 `linkd-` 前缀的稳定 UUID；`event_id` 继续保留 Linkd AlertID，
用于 KAC 按事件关联 firing 与 resolved/close。KAC 路由放宽后可再评估是否恢复直接使用 Linkd AlertID。

| KAC 字段/Record 属性 | 建议规则 | 理由 |
| --- | --- | --- |
| `event_id` | `"linkd-" + Alert.AlertID` | 同一 Alert 的 firing/resolved/close 使用同一稳定关联身份；前缀标识 Linkd 来源；KAC 按 event_id 查找待终结活动告警 |
| `alarm_id` | `"linkd-" + UUIDv5(tenant, AlertID, UpdateAt, outcome)` | 生成 KAC URL 路由可接受的记录身份；同一快照重试稳定，不同生命周期快照可区分 |
| Hook `message_id` | `"linkd-" + SHA-256(tenant, hook name, AlertID, UpdateAt, outcome)` | 每次快照调用身份稳定且可区分；AlertLog 使用该值审计 |
| Kafka key | UTF-8 `event_id` 原值 | 与 KAC 现有 producer 一致；同一 Alert 生命周期进入同一 partition |
| Record value | 单个 KAC Alarm JSON object | 与当前消费者逐 record 解码方式一致 |
| Record timestamp | `Alert.UpdateAt` | 表达 Linkd 快照更新时间 |
| headers | 首版不发送 | KAC 当前消费者不读取 headers；身份与租户位于 value |
| `active` | `action=firing` | 对应 KAC 活动告警动作 |
| `recovered` | `action=resolved` | 对应来源恢复 |
| `closed` | `action=close` | KAC 枚举值是 `close` |
| `alarm_time` | `Alert.BeginAt` 转到固定 `Asia/Shanghai` 时区，格式化为 `2006-01-02 15:04:05` | 同一生命周期固定为首次异常时间 |
| `close_time` | 终态 `Alert.EndAt` 转到固定 `Asia/Shanghai` 时区 | active 消息省略该字段 |
| `close_reason` | 终态 `Alert.EndReason` | active 消息省略该字段 |

KAC 的恢复/关闭流程按 `event_id` 查找活动告警，再以终态消息自己的 `alarm_id` 记录 resolve/close 事件，
见 `src/kingeye/kac/alarm/celery_tasks.py:150-215`。这要求 `event_id` 在 Linkd Alert 整个生命周期内保持稳定；
使用 `LatestEventID` 或 cause Event ID 会破坏关联。

同等级 update 继续发送 `firing`。KAC 旧链路允许同一 `event_id` 出现多个 firing record，后续由 KAC
合并/抑制链处理。生产切换前必须在影子 Topic 验证 Linkd update 频率对 KAC 告警数量、合并和通知的影响；
若产品期望只把 Alert 创建和终态交给 KAC，应把“跳过 `alert_updated`”作为显式 Hook 配置或新协议版本，
不能在实现中隐式丢弃。

`close_and_create` 升级先发送旧 Alert 的 close，再发送新 Alert 的 firing，两个 AlertID/event_id 不同。
`update_current` 升级发送原 Alert 的 firing，event_id 不变，level 使用新级别，alarm_id 按新 UpdateAt/outcome 生成；
不重新丰富。两种策略在目标 KAC 环境中的合并、通知与持久化行为仍需联调。

### 4.3 等级映射

KAC 固定接受 `fatal/warning/remind`。Kingeye 旧代码使用 `1→fatal, 2→warning, 3→remind`，证据见
`src/kingeye/common/constant/cw_constant.py:91-98`。KAC Hook 在代码中固定采用以下映射：

```text
critical → fatal
warning  → warning
info     → remind
```

出现其他 Linkd Severity name 时，本次 Hook 转换失败并形成 `hook_failed`。该映射属于 KAC Adapter 的
兼容规则；调整 Linkd 全局 Severity 集合时需同步修改 KAC Hook 代码和契约测试。

### 4.4 时间时区

KAC 时间字段缺少时区偏移，Kingeye 旧 Converter 使用进程本地时区格式化时间。Linkd 领域时间统一为
UTC。KAC Hook 在代码中固定使用 `Asia/Shanghai`，所有 KAC 无偏移时间字段统一转换到该时区，再按
`2006-01-02 15:04:05` 格式输出。实现初始化时加载该 IANA Location；加载失败应作为程序构建或启动错误。

## 5. Alert/Enrich 到 KAC 的字段映射

### 5.1 读取规则

KAC converter 先调用 `enrich.DecodePayload(Alert.Enrich)`，再按 Processor 名读取 envelope：

- `succeeded` 和 `partial` 的 `value` 可参与转换；
- `failed`、`skipped` 提供空值；
- 重复 Processor 名、字段类型错误或无法解析的 `metric_query_params` 使本次 Hook 失败；
- 未配置某个 Processor 时使用表中的核心字段回退；
- KAC Schema 要求的非空字段在转换后统一校验；
- converter 只读 Alert 快照，不修改 Alert、Enrich 或共享 map。

现有类型化结果位于 [`models/output.go`](../../internal/enrich/models/output.go)，信封协议位于
[`enrich/result.go`](../../internal/enrich/result.go)。

### 5.2 基础字段

| KAC 字段 | Linkd 来源 | 转换和缺失行为 |
| --- | --- | --- |
| `alarm_id` | `Alert.BKTenantID + Alert.AlertID + Alert.UpdateAt + outcome` | 生成稳定 UUID 并增加 `linkd-` 前缀，必填 |
| `event_id` | `Alert.AlertID` | 增加 `linkd-` 前缀，必填 |
| `bk_tenant_id` | `Alert.BKTenantID` | 原样，必填 |
| `source_id` | `source.source_id` → `Alert.EventSourceID` | 字符串，运行语义必填 |
| `source_name` | KAC Hook 常量 | 固定为 `鲸眼监控` |
| `item` | `metric.display_name` → `strategy.strategy_name` | KAC 允许空字符串 |
| `metric_name` | `metric.metric_name` | 缺失输出空字符串 |
| `name` | `display.title` → `Alert.Title` | 最终为空则失败 |
| `alarm_time` | `Alert.BeginAt` | 按固定 `Asia/Shanghai` 时区格式化，必填 |
| `content` | `display.content` → `Alert.Content` | 最终为空则失败；不生成占位文案 |
| `action` | `Alert.Status` | active/recovered/closed 映射为 firing/resolved/close |
| `level` | `Alert.Severity` | KAC Hook 固定映射；未知 Severity 使 Hook 失败 |
| `object` | `display.object` → `Alert.SubjectName` → `Alert.SubjectID` | 缺失输出空字符串 |
| `close_time` | `Alert.EndAt` | active 省略；终态必填 |
| `close_reason` | `Alert.EndReason` | active 省略；终态允许空字符串 |

### 5.3 Strategy、Resource、Display、Metric 与 Source

| KAC 字段 | Enrich 来源 | 规则 |
| --- | --- | --- |
| `strategy_id` | `strategy.monitor_template_id` | 十进制字符串；保留旧 KAC 字段语义 |
| `strategy_name` | `strategy.strategy_name` | 缺失输出空字符串 |
| `field_extra_info.strategy_name.url` | `strategy.url` | 始终输出 object，URL 可空 |
| `data_source` | `strategy.data_source` | 字符串 |
| `bk_obj_id` / `bk_inst_id` | `resource.bk_obj_id` / `resource.bk_inst_id` | 标量转稳定文本；null 转空字符串 |
| `model_id` / `model_inst_id` / `model_name` | `resource` 同名字段 | 字符串 |
| `bk_biz_id/name` | `resource.bk_biz_id/name` | ID 转稳定文本；null 转空字符串 |
| `bk_set_id/name` | `resource.bk_set_id/name` | 标量转稳定文本；多值需 Enrich 先提供 KAC 约定的逗号分隔文本 |
| `bk_module_id/name` | `resource.bk_module_id/name` | 同上 |
| `bk_cloud_id/name` | `resource.bk_cloud_id/name` | ID 转稳定文本 |
| `dynamic_group_id` | `resource.dynamic_group_id` | 保持字符串数组；空数组也输出 |
| `cw_labels` | `resource.cw_labels` | 保持字符串数组；空数组也输出 |
| `name` / `content` / `object` | `display.title/content/object` | 见基础字段回退规则 |
| `dimension_info` | `display.dimension_text` | 字符串；结构化 `display.dimensions` 不直接发送到基础字段 |
| `metric_name` | `metric.metric_name` | 字符串 |
| `unit` | `metric.unit` | 字符串 |
| `result_table_id` | `metric.result_table_id` | 字符串 |
| `metric_unique_id` | `metric.metric_unique_id` | 字符串 |
| `aggregate_func` | `metric.aggregate_func` | 字符串 |
| `time_interval` | `metric.time_interval` | 标量转稳定文本，null 转空字符串 |
| `where_condition` | `metric.where_condition` | 字符串 |
| `anomaly_begin_time` | `metric.anomaly_begin_time` | 有值时输出；nil 时输出 JSON null，以匹配现有样例 |
| `metric_query_params` | `metric.metric_query_params` | 规范 JSON 后编码成 JSON 字符串；nil 使用 `{}`；已经是字符串时先严格解码为 JSON object，再规范编码一次，拒绝非法 JSON 和二次编码 |
| `meta_info` | `source.meta_info` → `Alert.SourceEventID` | 字符串 |

`bk_service_id` 和 `Namespace` 当前 Enrich 设计没有等价值，首版固定输出空字符串，与已取证旧实现一致
（`src/kingeye/kac/alarm_callback/cleaner/base.py:600-617`、
`src/kingeye/kac/alarm_callback/cleaner/base.py:751-752`）。

### 5.4 场景扩展字段

KAC ES 模型允许动态字段，旧 Cleaner 会追加日志、APM、K8s 和云平台字段。Linkd KAC Hook 当前读取
`log` 和 `apm` Processor 的类型化输出；对应 Processor 未配置、失败或跳过时保留固定默认值。K8s 专用字段
仍使用默认值，云平台 ID 由 `resource.cloud_plat_id` 提供。稳定输出形状如下：

```json
{
  "log_theme_id": 0,
  "log_theme_name": "",
  "log_query_string": "",
  "log_relate_info": "",
  "apm_app_id": 0,
  "apm_app_name": "",
  "apm_app_alias": "",
  "apm_service_name": "",
  "apm_instance_name": "",
  "apm_interface_name": "",
  "apm_net_peer_name": "",
  "bcs_cluster_id": "",
  "cluster_name": "",
  "namespace": "",
  "service": "",
  "workload_kind": "",
  "workload_name": "",
  "pod_name": "",
  "container_name": "",
  "cloud_plat_id": ""
}
```

默认值按字段类型固定：数值 ID `log_theme_id`、`apm_app_id` 使用 `0`，其余扩展字段使用空字符串，
保证 payload 字段形状和类型稳定。`log/apm` Processor 成功或 partial 且携带有效值时，Hook 写入真实字段；
诊断、Processor status、完整 Alert、ExtraData 和任意未知 Enrich 字段保持在 Linkd 内部。

### 5.5 当前字段缺口

以下差距会直接影响首版可用范围：

1. `name` 和 `content` 是 KAC 非空必填字段，Linkd 核心模型允许为空。转换后为空时记录 `hook_failed`，
   不生成 `--` 等占位内容；
2. KAC 现有基础 Schema 对 `bk_tenant_id/source_id/alarm_id` 约束较弱，Linkd KAC Hook 将三者提升为
   本地运行语义必填项；
3. `bk_set_id`、`bk_module_id` 的多值表达尚未在 Linkd Enrich 类型中冻结为单值或逗号分隔值，真实
   多集群/多模块样例联调前保持未决；
4. `metric_query_params` 在 Linkd 中是结构化 JSON，在 KAC 当前消息中是 JSON 字符串，首版执行一次
   明确编码，禁止字符串二次编码；
5. Alert 普通更新保留 opening Event 的 title/content/enrich。KAC 收到的是 Linkd 当前 Alert 快照语义，
   不承诺带上 latest Event 的新文案；
6. 直接关闭只有 Alert 和 operation cause，依然可以生成 KAC close；`meta_info` 保留 opening source
   event 身份，close 原因来自 Alert.EndReason；
7. KAC Hook 已读取 `log/apm` Processor 的真实值；未配置、失败或跳过时保留固定类型默认值。K8s 专用字段
   当前仍使用默认值，待 KAC 映射与真实回放闭环。

## 6. Hook 配置

建议配置模型：

```yaml
event_sources:
  - event_source_id: built_in_bk
    hooks:
      - name: alert-v1
        type: kafka
        config:
          brokers: [kafka-output.example.com:9092]
          topic: linkd-alert-v1
          client_id: linkd-alert-v1
          max_message_bytes: 1048576
          security:
            protocol: plaintext

      - name: kac-alarm-center
        type: kac
        config:
          brokers: [kac-kafka.example.com:9092]
          topic: bk_alarm_collect_topic
          client_id: linkd-kac-alarm
          max_message_bytes: 1048576
          security:
            protocol: sasl_plaintext
            sasl:
              mechanism: plain
              username: linkd
              password: ""
```

`type: kac` 仅使用 Kafka 连接参数：

| 字段 | 约束 |
| --- | --- |
| `brokers/topic/client_id/max_message_bytes/security` | 复用现有 Kafka Hook 校验和默认值 |

等级映射、`Asia/Shanghai` 时区和 `source_name=鲸眼监控` 由 `internal/lifecycle/kachook` 常量固定，
EventSource 配置不暴露对应字段。`type: kafka` 与 `type: kac` 均拒绝 Redis 参数；管理接口对 Kafka
SASL password 和 TLS private key 沿用现有脱敏与保留旧凭据逻辑。

输出连接使用发布配置中的固定 broker/topic。首版不调用 Kingeye
`get_kafka_connection_info`；运行时动态发现会引入额外 HTTP 鉴权、缓存、超时和版本一致性问题。KAC
Kafka 目标变化时发布新 EventSource Release，调度器排空旧任务后装配新 client。

## 7. 模块和接口设计

建议文件结构：

```text
internal/lifecycle/kachook/
  doc.go            包职责和兼容边界
  config.go         KAC 专属配置与校验
  message.go        KACAlarmV1 DTO、Enrich 解码和字段转换
  identity.go       alarm_id/message_id 与 partition key
  hook.go           FinalHook 实现、编码、大小检查和 ProduceSync
  message_test.go   golden/字段/动作/时间/失败测试
  hook_test.go      Kafka record 和 producer 结果测试
```

外部 seam 保持现状：

```go
type FinalHook interface {
    Execute(context.Context, FinalHookInput) (FinalHookResult, error)
}
```

`kachook.Hook` 内部可以使用两个小 seam：

```go
type Converter interface {
    Convert(FinalHookInput) (KACAlarmV1, error)
}

type Producer interface {
    ProduceSync(context.Context, ...*kgo.Record) kgo.ProduceResults
    Close()
}
```

生产代码可直接把 Converter 作为 Hook 私有实现，测试通过私有构造函数注入 fake producer。该结构把
KAC 字段兼容、Kafka I/O 和 Lifecycle 编排隔离开，调用方只学习现有 FinalHook 接口。

实现修改点：

- `internal/config/hooks.go`：增加 `HookTypeKAC`，并让 `KACConfig()` 复用 Kafka 连接参数、默认值、校验、clone 和 redact；
- `internal/lifecycle/process/hooks.go`：注册 `kachook.New` 并纳入反向关闭；
- `internal/lifecycle/kachook`：新增转换与投递实现；
- 配置测试、装配测试、Lifecycle Hook 测试和契约 fixture；
- 配置指南、Lifecycle 模块、术语和外部契约在实现时同步更新。

Kafka client 的 broker、安全配置和 producer options 应下沉复用 `internal/kafkaclient`。KAC Hook 与通用
Kafka Hook 各自拥有 client 和序列化器，避免通过调用 `kafkahook.Hook.Execute` 再拆改 V1 payload。

## 8. 执行时序与可靠性

### 8.1 时序

```text
Alert create/CAS 成功
  → Recent Alert Cache 成功（Elasticsearch 场景）
  → 按 EventSource.hooks 顺序
      → kafka Hook（若排在前）
      → kac Hook
          → 校验 FinalHookInput
          → 解码 Enrich Processor values
          → 转换并校验 KACAlarmV1
          → JSON marshal + 大小检查
          → ProduceSync(all ISR ACK)
      → 后续 Hook
  → 统一写 push AlertLog
  → Event CAS accepted
```

Hook 顺序由来源配置决定。close_and_create 升级采用“旧 Alert 全部 Hook → 新 Alert 全部 Hook”；
update_current 升级仅执行当前 Alert 的 Hook。

### 8.2 重复与失败

- `ProduceSync` 返回成功表示 broker 按 all-ISR ACK 确认该 record；
- `FinalHook` 的既有语义会把普通 KAC 转换或发送错误记录为 `hook_failed`，并继续后续 Hook 与 Event 终态 CAS；
- 已完成的 Alert 状态以及本次 Event 成功处理不会因普通 KAC Hook 失败回滚；当前没有独立失败补偿队列；
- Kafka 成功后进程在 Event CAS 前退出，Lifecycle 重试可能再次发送同一 record；
- `alarm_id` 在同一 Alert 快照重试中稳定，Kafka key 在同一 Alert 生命周期内稳定，`message_id` 在相同快照重试中稳定；
- KAC 当前消费者和存储没有完整的 message_id 去重契约，联调必须验证相同 `alarm_id` 重投的实际结果；
- 每次 KAC Hook 调用的转换、编码、超限或 producer 错误形成该实例的 `hook_failed` AlertLog，随后继续执行后续 Hook；
- 父 Context 取消遵循现有 Lifecycle 语义，停止当前及后续 Hook，并保留 Mailbox 队首用于恢复；
- 首版不增加 Hook 内应用层重试循环，使用 franz-go client 的受控 broker 重试和 Lifecycle 恢复；
- 消息超过 `max_message_bytes` 时发送失败，不裁剪 content、log_relate_info 或查询参数，以免生成语义
  不完整且无法识别的 KAC 告警。

Kingeye 当前 KAC consumer 在业务处理前提交 offset，见
`src/kingeye/kac/alarm/management/commands/alarmpipeline.py:196-209`。这会形成 KAC 进程崩溃时的丢失窗口，
属于接收端剩余风险；Linkd Hook 成功只能证明 Kafka broker 已确认。

### 8.3 顺序

同一 Alert 使用稳定 `event_id` 作为 key，单个 Kafka partition 内保持 producer 顺序。跨 Alert、跨
partition 没有全局顺序。单个来源多个 Lifecycle 副本仍由 Mailbox lease 串行处理同一 fingerprint；
close_and_create 等级升级在一个 Processor 调用中先关旧 Alert 再开新 Alert；update_current 只更新原 Alert。

配置变更通过旧任务排空、新任务接管生效。旧 Release 和新 Release 指向不同 Topic 时，两个 Topic
之间没有顺序保证，变更需配合灰度和 KAC 消费切换。

## 9. 安全与可观测性

- 所有 payload、Kafka key、日志和 AlertLog 均带稳定租户作用域；
- SASL password、TLS private key 只进入受保护 EventSource Release，管理读取保持脱敏；
- 日志禁止记录完整 KAC payload、content、log_relate_info、metric_query_params 或凭据；
- 转换错误记录稳定低基数分类，例如 `invalid_kac_field`、`invalid_enrich_payload`、
  `payload_too_large`、`produce_failed`；详细错误进入受控 error chain，日志只保留租户、AlertID、Hook 名和目标；
- 复用 `linkd.final_hook.operations` 与 `linkd.final_hook.duration`，标签中
  `hook.name=kac-alarm-center`、`transport=kafka`、`destination=<topic>`；
- `FinalHookResult` 建议使用 `Transport="kafka"`、`Destination=<topic>`、`MessageID=<stable hook message_id>`；
- AlertLog 的 `hook_succeeded/hook_failed` 用于审计，不能作为 KAC 已持久化证明；
- 上线看板至少观察发送成功率、失败分类、耗时、payload 大小分布、KAC error topic 数量，以及
  Linkd Alert 与 KAC Alarm 的创建/终态数量差异。

## 10. 动态发布、灰度与回滚

配置属于单个 EventSource Release。运行中的来源以控制面持久化版本为准；修改本地 YAML 后需要通过
Console/API/provider 或显式 `linkd event-source import` 发布。发布成功表示配置快照已创建，任务实际
切换状态仍需从 runtime 查看。

目标 KAC 必须预先存在与输出 `source_id` 对应且已启用的 AlarmSource；正式 Topic、ACL、分区和
Consumer Group 也由 KAC 所有者准备。Linkd 配置验证只校验本地参数，不证明 KAC AlarmSource 存在、
Kafka 可达或 KAC consumer 已接管 Topic。

建议灰度步骤：

1. 在 KAC 创建独立影子 Topic 和隔离消费组；
2. 在一个测试 EventSource 上增加 `type: kac`，保留现有 `type: kafka`；
3. 回放脱敏的 firing/update/resolved/close/等级升级样例；
4. 对比 Linkd Alert、push AlertLog、Kafka record、KAC 校验结果和影子存储；
5. 验证重复 record、乱序实验、跨租户相同 AlertID、Enrich partial/failed 和超大 payload；
6. 对比旧 alarm_callback 与新 KAC Hook 的必填字段、告警数量、关联、合并、屏蔽和通知；
7. 达到验收阈值后，把 KAC 正式消费切到新 Topic，保留有期限的旧链路观测。

回滚通过发布一个移除或停用 `type: kac` 的 EventSource Release 完成，`type: kafka` 持续输出。Topic 切换
期间避免双生产进入同一个正式 KAC consumer；需要双写比较时使用独立影子 Topic。Hook 配置切换不会
清理 KAC 已接收数据，也不会迁移旧 Topic 积压。

## 11. 测试与联调验收

### 11.1 单元测试

- `KACAlarmV1`：全部字段、空值、标量转文本、JSON-in-string 和场景扩展默认值；
- 动作：active/recovered/closed，close_and_create 的旧 close 与新 firing，以及 update_current 的同 event_id firing 和新 level；
- 身份：`alarm_id` 为 `linkd-` 加稳定 UUID，`event_id` 为 `linkd-` 加 AlertID；相同快照的两者及 message_id 均稳定，不同 update/outcome/hook 实例可区分；
- 等级：固定三项映射、未知 Linkd Severity 失败；
- 时间：UTC 到固定 `Asia/Shanghai`、秒级截断、终态缺少 EndAt；
- Enrich：succeeded/partial/failed/skipped、缺 Processor、非法类型、重复 Processor；
- 必填：空 name/content；`source_name=鲸眼监控` 始终输出；
- Kafka：topic/key/value/timestamp、all-ISR ACK、逐项错误、取消、关闭幂等和 payload 超限；
- Config：严格 YAML/JSON、未知参数、凭据脱敏、参数互斥和 16 Hook 上限。

### 11.2 契约测试

从 Kingeye 当前 `get_clean_alarm_schema()` 固化一份带来源 commit 的测试 fixture，并用以下消息验证：

- BASE_COLLECT 主机告警；
- 同一 event_id 的 firing → firing update → resolved；
- source close；
- severity upgrade；
- log keyword 扩展；
- K8s/APM 扩展；
- partial enrich；
- 跨租户相同业务身份；
- 相同 record 重投。

KAC Schema 当前只表达最低约束。契约测试还要启动或调用 KAC `alarm_pipeline` 的隔离测试入口，验证
`source_id` 查找、租户准备、firing 存储以及 resolved/close 按 event_id 关联。

### 11.3 完成标准

- `type: kafka` 的现有 golden tests 全部保持通过；
- `type: kac` 生成的所有样例通过目标 KAC commit 的 JSON Schema；
- KAC 已注册并启用目标 `source_id`，且 Topic/ACL/分区/Consumer Group 的实际参数已留档；
- firing 后 KAC 产生可查询 Alarm，resolved/close 能终结对应 event_id；
- 相同 Hook 调用重放使用相同 `alarm_id/event_id/message_id`，且 KAC 侧重复行为已被实测记录并获得接收方确认；
- 两个租户的相同来源身份不会串联；
- 断开 Kafka、认证失败、broker 超时和 payload 超限均形成可观察失败，Lifecycle 无 goroutine/client 泄漏；
- `go test -race` 覆盖 KAC Hook client 生命周期；
- 最终执行 `make check`，外部集成测试单独记录环境和结果。

## 12. 分阶段实施

### 阶段 1：冻结接收契约

- 与 KAC 所有者确认目标部署 commit、Topic 和认证；
- 用真实 KAC Schema 与消费者测试冻结 KAC Alarm V1 fixture；
- 确认 `event_id="linkd-" + Linkd AlertID`、`alarm_id="linkd-" + stable UUID` 和同级 update 发送策略；
- 确认多集群/多模块 ID 的文本格式。

### 阶段 2：实现纯转换器

- 新增 KAC DTO、Enrich 解码、字段白名单、身份和校验；
- 完成 golden、动作、等级、时间、缺字段和扩展场景单测；
- 该阶段不连接 Kafka。

### 阶段 3：实现 Hook 与配置

- 注册 `type: kac`，复用 Kafka client/security；
- 完成 ProduceSync、大小限制、取消、关闭、脱敏和指标；
- 更新配置、Lifecycle、术语与外部契约文档。

### 阶段 4：影子联调

- 独立 Topic 双输出；
- 验证数量、字段、关联、重复、顺序、租户、错误 Topic 和 KAC 存储结果；
- 记录容量、延迟和最大 payload 数据。

### 阶段 5：正式切换

- 发布包含正式 KAC Hook 的 EventSource Release；
- 按来源逐步切换，观察数量和终态闭环；
- 保留 `type: kafka` 作为 Linkd 通用协议输出；
- 按发布 Release 移除 KAC Hook 完成回滚。

## 13. 本地验证记录

2026-09-14 使用 `configs/linkd.pm2.local.yaml`、PM2 和本地 Docker 中的 Kafka、Elasticsearch、Redis、
Kingeye MySQL 完成首版链路验证：

```bash
go build -o ./bin/linkd ./cmd/linkd
LINKD_CONFIG=configs/linkd.pm2.local.yaml pm2 restart linkd-all-in-one --update-env
./bin/linkd event-source import \
  --config configs/linkd.pm2.local.yaml \
  --file configs/linkd.pm2.local.yaml
python3 scripts/publish_host_alert.py \
  --count 1 --interval 0 \
  --topic linkd-base-collect \
  --bootstrap-server localhost:9092 \
  --kafka-container linkd-kafka \
  --tenant-id system --strategy-id 123 --biz-id 2 --host-id 101
```

实际从 `linkd-pm2-kac-alarms` 消费到一条 KAC JSON，验证结果：

- Kafka key 与 `event_id` 相同并带 `linkd-` 前缀；当时验证版本的 `alarm_id` 与二者相同，当前临时适配已将其改为 `linkd-` 加稳定 UUID，待重新联调；
- `source_name=鲸眼监控`、`action=firing`、`level=warning`；
- UTC `08:09:00` 转换为 `Asia/Shanghai` 的 `16:09:00`；
- `log_theme_id=0`、`apm_app_id=0`，K8s/云平台字符串扩展字段为空；
- `metric_query_params` 是可解析 JSON object 的字符串；
- KAC Schema 的八个最低必需字段全部存在。

该验证覆盖 Linkd 到 Kafka record。目标 KAC consumer、AlarmSource 注册、ES 持久化、恢复/关闭关联和
重复消息处理仍需在 KAC 联调环境验证。

## 14. 待确认问题

以下问题会影响外部契约，实施阶段 1 完成前保持开放：

1. 目标 KAC 的准确代码版本、`get_clean_alarm_schema()` 内容及 `ALARM_COLLECT_TOPIC` 名称；
2. KAC 正式环境是否已要求每条消息显式包含 `bk_tenant_id`，缺失时的默认租户行为是否继续存在；
3. KAC 对重复 `alarm_id` 的真实幂等行为；
4. 同等级 `alert_updated` 应全部发送、按频率合并，还是只发送创建和终态；
5. `bk_set_id/bk_module_id` 多值采用逗号分隔、数组扩展字段，还是只保留一个确定值；
6. KAC 对 `metric_query_params` 是否继续要求 JSON 字符串，目标版本能否接受结构化 object；
7. KAC 接收端能否增加稳定 message ID 去重和“业务完成后提交 offset”的可靠性改造；
8. 直接关闭、系统关闭和等级升级的 `close_reason` 是否需要 KAC 专用文案映射；
9. Enrich `failed/partial` 时，哪些字段缺失仍允许发送，哪些来源需要严格阻断；
10. 生产双链路期间采用影子 Topic，还是由 KAC 提供隔离的验证来源和存储空间。

推荐先冻结第 1～5 项，再实现纯转换器；它们共同决定身份、消息数量、终态关联和时间语义。
