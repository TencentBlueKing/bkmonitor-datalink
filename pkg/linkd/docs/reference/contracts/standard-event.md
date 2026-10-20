# Linkd 标准事件（RawEvent）

本文是 Linkd `standard` Cleaner 输入 RawEvent 的完整契约，同时说明 Kingeye/KAC 告警源
插件的发布前基础校验及第三方语义映射。下文 JSON 是插件投递的 **Kafka Value**；
主机丰富链路的具体取值见[主机推送告警 Enrich 示例](../../guides/host-alert-enrich-example.md)。
KAC 当前发布前校验见
`src/kingeye/kac/alarm_collect/linkd/raw_event.py`，插件调用方式见
`src/kingeye/kac/alarm_collect/clients_v2/baseclient.py`。本文描述当前代码与对接边界，
不再使用旧的嵌套 `kingeye.alert-event` / `schema_version: 1.0.0` 消息格式。

## 消息与来源身份

第三方拉取插件实现 `BaseClientV2.pull_raw_events()`，每次返回若干条 JSON object。KAC 在
`BaseClientV2.start()` 中先强制注入认证租户、校验整批，再逐条发送到告警源 `linkd_channel.config.topic`。
Kafka Value 就是下面的 RawEvent；`RawEventMessage` 信封由 Linkd Kafka Adapter 构造，不由插件填写。

KAC 的 `linkd_source_id` 对应 Linkd `EventSourceID`，由来源配置选择，不放进 Value。KAC 发送时
Kafka key 使用 `bk_tenant_id:fingerprint`，header 提供当前租户的 `bk_tenant_id`，另带 `X-Secret`。
当前 Linkd 会透传 `X-Secret`，尚未校验它。`RecordID` 来自 Kafka topic / partition / offset，
`ReceivedAt` 来自 Kafka record timestamp；这两个值也不由插件填写。发布等待 broker ACK，
批次中途失败时先前已确认的记录无法回滚，因此同一事件重试须保持稳定身份。

```json
{
  "bk_tenant_id": "tenant-1",
  "event_id": "source-event-1",
  "fingerprint": "source-alert-1",
  "title": "CPU usage is high",
  "content": "Host 10.0.0.1 CPU usage reached 92.5%",
  "values": { "value": 92.5 },
  "evaluations": [
    { "severity": "warning", "action": "triggered", "action_reason": "" }
  ],
  "dimensions": {
    "bk_inst_id": 101,
    "bk_target_ip": "10.0.0.1",
    "bk_target_cloud_id": 0
  },
  "subject": { "system": "cmdb", "type": "host", "id": "101", "name": "host-101" },
  "occurred_at": "2026-09-01T00:00:00Z",
  "produced_at": "2026-09-01T00:00:01Z",
  "labels": { "strategy_id": 123, "strategy_version": 1, "bk_biz_id": 2 },
  "extra_data": {
    "anomaly_begin_time": "2026-09-01T00:00:00Z",
    "additional_dimensions": { "bk_host_id": 101 }
  }
}
```

`fingerprint` 是同一告警触发、恢复和关闭的稳定关联身份。提供时必须为合法非空字符串（最多 128 bytes），直接采用并跳过 EventSource 指纹生成规则；显式 null、空值或非法类型报错。未提供时，其他 Linkd 生产者继续使用既有来源指纹规则。
`event_id` 可选，仅保存来源事件编号。KAC V2 要求 fingerprint，不要求或补造 alert_id；标准 HTTP 接入方不填写租户，由认证后的 KAC 注入。Linkd 内部 Event/Alert ID 仍由系统生成。

## Linkd `standard` 通用输入规则

Kafka Adapter 先构造只读 `RawEventMessage`：稳定 `RecordID`、租户、配置确定的 `EventSourceID`、
稳定 `ReceivedAt`、headers 和原始 payload。当前 Kafka Adapter 从 header 读取 `bk_tenant_id`，
使用 record timestamp 作为 `ReceivedAt`。`standard` Cleaner 要求 Value 是单个 UTF-8 JSON object；
重复 key、尾随 JSON、顶层数组和已知字段的非法类型或取值都会被确定性拒绝。未知字段不会进入
Event 的已知字段，只保留在完整的 `Event.source_raw_data` 快照中。

| 字段 | Linkd 通用规则 |
| --- | --- |
| `bk_tenant_id` | Value 与 header 可只提供一处；两处非空时须一致，且最终租户不能为空。EventSource 的 `related_tenant_id` 非空时由该配置覆盖。 |
| `fingerprint` | 可选直接指纹；提供时优先采用，未提供时执行来源生成规则。KAC V2 要求必填。 |
| `event_id`、`alert_id` | 分别映射到 `Event.source_event_id`、`Event.source_alert_id`，通用 Cleaner 允许为空；EventFactory 使用稳定 `RecordID` 和 Kafka record timestamp 生成内部 Event ID；来源 ID 不承担消息唯一性。具体来源若按 `source_alert_id` 关联，仍须提供稳定且非空的 `alert_id`。 |
| `title`、`content` | `title` 是事件标题，最多保留 2000 个 Unicode 字符，超出自动截断；`subject_name` 使用相同上限。`content` 是可选的来源内容，仍保留 1 MiB 上限；缺失 `content` 不从标题推导。 |
| `evaluations` | 必填，1–32 项；每项 `action` 只能是 `triggered`、`resolved`、`closed`，`severity` 为来源等级标识或关闭专用标记 `__ALL__`。来源等级映射后不得重复；数组顺序不决定裁决顺序，未出现的等级不隐含恢复或关闭。 |
| `values` | 可选的扁平数值对象，最多 256 项；key 为 1–256 bytes，value 必须是有限数字，不接受字符串、布尔值、null 或嵌套值。缺失、显式 null 和 `{}` 都形成空对象，不给缺失的数值项补零。 |
| `dimensions`、`labels` | 扁平标量对象，value 只接受字符串、有限数字或布尔值；通用 Cleaner 对缺失或显式 null 规范为空对象。维度与标签各自的业务完整性由来源负责。 |
| `subject` | 可选的来源对象声明，子字段为 `system`、`type`、`id`、`name`；缺失时不推断对象身份。 |
| `occurred_at`、`produced_at` | 分别表示事实发生时间、首次生成时间；缺失时各自回退到稳定的 `ReceivedAt`，提供时须为合法时间。 |
| `extra_data` | 可选的来源扩展对象；其中的 `additional_dimensions` 须为合法标量对象，且不得与顶层 `dimensions` 重名。 |

每项 `evaluations[].severity` 保留来源原值；默认 SeverityResolver 依次使用
`severity_mapping`、全局同名 Severity、来源 `default_severity` 和全局 `default_severity`。
`__ALL__` 是 alarmd 全级别关闭使用的保留标记，原样进入 Event，不经过上述映射或默认值回退，
也不需要配置到全局等级表；全局等级表禁止使用该名称。它只允许与 `action: closed` 一起使用，
用于关闭同一 `(bk_tenant_id, event_source_id, fingerprint)` 下任意级别的当前活动 Alert：

```json
{
  "fingerprint": "source-alert-1",
  "evaluations": [
    { "severity": "__ALL__", "action": "closed", "action_reason": "strategy_inactive" }
  ]
}
```

该片段省略租户，实际投递仍须由 Value、header 或来源配置提供租户。关闭保留 Alert 原级别，
以该 Event 的 `occurred_at` 和 `action_reason` 保存来源结束时间和原因；alarmd 的
`strategy_inactive`、`strategy_absent`、`target_out_of_scope` 等原因原样保留。找不到活动 Alert 时
结果为 `orphaned`，不会新建或改写历史终态告警。若同一事件还有普通 `triggered`，先完成
全级别关闭，再由最高触发级别创建新 Alert；该关闭不会被级别升级策略替代。

同一事件的所有判定共享本次的 `values`、`dimensions` 和 `occurred_at`；`values` 不参与 fingerprint。
`event_source_id`、`event_source_version`、`related_alert_ids`、`received_at`、
`create_at` 和 `source_raw_data` 即使出现在 Value 中，也不能覆盖 EventFactory 的结果。
完整 Value 保存到 `Event.source_raw_data`，不写入日志，也不在 Elasticsearch 建索引。

StandardCleaner 将已知来源字段投影为 EventDraft；EventFactory 负责租户、来源、标准等级、
fingerprint、确定性 Event ID、接收时间和原始快照。Event 创建或幂等命中并成功进入对应
Redis Mailbox 后，原消息才允许确认；Signal 只唤醒 Mailbox，不承载单条 Event 的处理责任。
这些规则描述 Linkd 通用接收边界；下文 KAC V2 插件在发布到 Kafka 前还有额外校验。

## 无数据描述的检测事实

2026-10-14 确认：`content_mode: bkmonitor_description` 正式接入 alarmd 已有的无数据事实，
保持 alarmd 输出契约不变。输入同时满足以下条件时，内容构建器创建
`当前指标(X)已经有N个周期无数据上报`：

- `dimensions.__NO_DATA_DIMENSION__` 按既有 `ScalarProvided` 真值规则成立，
  且 `extra_data.evaluation_family` 为 `"no_data"`；标记与 family 冲突返回 `nodata_identity_invalid`。
  标记为 `false`、`0` 或空字符串时不进入无数据分支，普通指标描述照常处理。
- 事件标签绑定的同租户、同策略版本发布配置中，`strategy_item.no_data_config.is_enabled`
  为 `true`，整数 `level` 对应当前 opening Alert 的等级（1→critical、2→warning、3→info）；
  不满足则返回 `nodata_configuration_invalid`，不借用普通指标算法等级。
- `extra_data.no_data_periods` 为 JSON 整数，范围 `[1, 1000000]`。缺失返回
  `nodata_evidence_missing`，null、字符串、小数、非正数和超限返回 `nodata_evidence_invalid`。
  配置的 `continuous` 不替代这次检测的周期数。

`X` 使用同版本发布配置的监控项名称规则；`N` 原样使用来源周期数。
无数据分支不要求 `values.value`、单位或普通指标算法，不为缺测值补零。
`window.anomalies` 和 `anomaly_begin_time` 只描述滚动窗口，不能补算累计异常或数据延时。
因此本分支**不生成**旧 bk-monitor 的“并且数据上报延时M个周期”，也不宣称复现其
两套 checkpoint 的全部语义；这是对历史逐字一致目标的已确认收窄。

普通指标描述及精确版本配置校验保持既有契约。生成内容只赋给 Alert，Event.content、
SourceRawData、ExtraData 保持不变。已创建 Alert 在后续同级触发或 `update_current` 升级时，
按 content_mode 使用本次来源原文或基于本次 Event 和精确策略历史版本重新生成描述，
与时间、revision 一起写入；恢复、关闭和低等级抑制保留已有内容。
无数据描述与所有其他生成文案采用统一降级：任何生成错误、panic 或非法/空白输出，
来源原文非空白时保留原文；为空/空白时采用“告警已触发，详细内容暂不可用。”。
创建及持续触发入口记录原因码和 `source_fallback/default_fallback`，只读预览返回同样的
`content_diagnostic`；不记录原文或任意依赖错误文本。
事件本身的身份/合法性检查仍在构建器外执行，整个处理 Context 取消时保留事件重试。
内容生成以外的存储、准入和队列错误遵循原有规则。完整边界见
[任务恢复与内容边界](task-resume.md#操作顺序与内容边界)。降级不修正 Event 字段或检测判定，
也不表示缺失的检测事实已补齐；新 Alert.content 仍经校验并冻结到 EventPlan。
真实输入的验证与部署边界见[无数据接入验证](../../research/samples/2026-10-14-nodata-content/README.md)。

## 算法描述与原文保留

2026-10-17 确认：`bkmonitor_description` 是内容转换，不作为 Event 处理成功的前置条件。
分支不适用、未知算法、配置/事实缺失、版本绑定不可读或描述依赖读取失败时，Lifecycle 与 opening Event
只读预览均优先保留非空白原始 `Event.content`；原文为空/空白时使用固定文案，不将单纯文案失败变成 `processing_failed`。
普通文案 panic 和非法输出也遵循统一降级；事件/来源/租户入口隔离、取消与来源大小约束继续生效；不修改来源数据，不查询其他租户或最新策略来补文本。
原文保留诊断不包含正文；已完成 Event 的重投不重新生成内容。

按 alarmd `kingeye/master@9fd58a2fd` 的默认编译器与 `linkdoutput.Converter` 核对当前算法：

| 算法 | 描述输入与行为 |
| --- | --- |
| Threshold / PingUnreachable 来源映射 | 沿用阈值和 Ping 模板 |
| SimpleRingRatio / SimpleYearRound | 发布配置的下降/上升比例与当前值 |
| AdvancedRingRatio / AdvancedYearRound | 各方向间隔、avg/last、下降/上升比例与当前值 |
| RingRatioAmplitude | 前一时刻比较、threshold/ratio/shock 条件与当前值 |
| YearRoundAmplitude / YearRoundRange | days/method/ratio/shock 条件与当前值 |
| OsRestart | 事件中的当前运行时长；不重算历史检测 |
| ProcPort | 当前进程值及有界 display_name/nonlisten/not_accurate_listen/bind_ip 维度，事实不足则降级 |
| 无数据 | 事件自带 no_data_periods，沿用独立无数据模板 |

标准事件不携带历史比较值、具体命中方向或每个算法的检测结果。沿用本轮 b6 场景边界，
缺少历史证据时保留来源/固定文案，不杜撰比较值或从当前目录补历史名称。
监控项聚合优先使用同快照声明的顶层/查询聚合方式；衍生指标由指标定义内部聚合，不另套前缀。
显式提交的自定义 content 优先，允许空串；控制面传输不可用是运行时故障，保留原事件重试，不冻结降级结果。

## 实例来源与告警维度

### `subject`：实例数据来源

`subject` 是来源对告警对象的声明：`system` 标识实例数据所属的命名空间，`type` 和 `id`
分别是该命名空间中的对象类型与实例身份，`name` 是来源给出的展示名称。上游已确认身份时，
可以标注为 CMDB（如 `system=cmdb`、主机类型和主机实例 ID）或 OneModel
（如 `system=onemodel`、模型类型和该模型下的实例 ID）；`type/id` 应保持所属系统的原始语义，
不能把展示名或 IP 猜成实例 ID。无法定位到具体资源时可以省略 `subject`，或只提供已知的
`name`，不要伪造 `system/type/id`。对象未定位不影响来源事件本身的记录。

当前 Linkd 将 `subject` 原样投影为 Event 的 `subject_system/type/id/name`，创建 Alert 时继承；
`subject_system` 尚无 `cmdb/onemodel` 枚举校验，`system` 的取值本身也不会自动触发对应数据源查询。
资源丰富仍按场景、策略和维度中的可信身份定位实例；查不到实例时保留来源事实，
由 Processor 返回相应诊断或降级结果。若 EventSource 把 `subject_system/type/id` 配为
fingerprint 字段，被引用的字段就必须非空且在同一告警的后续事件中稳定；未定位实例的来源
应改用稳定的 `source_alert_id` 等可用身份。字段含义见[核心模型](../../design/define.md)，
关联规则见[EventSource](../../modules/event-source.md)，资源定位与降级见[丰富设计](../../design/enrich.md)。

### `dimensions`：严格完整的告警维度

`dimensions` 应完整、原样地承载**产生本次判定的全部告警维度**：按来源检测规则逐项保留键、
标量值和 JSON 类型，不为缩小消息而只发其中用于 fingerprint 的几项，也不以 `subject`、`labels`
或 `extra_data.additional_dimensions` 替代。来源缺少某个真实维度时不得补造 `""`、`0` 或
`false`；这些都是有值的维度。触发、恢复、关闭同一来源告警时，参与关联身份的维度必须保持稳定。

顶层 `dimensions` 只接受扁平的字符串、有限数字和布尔值；不得包含 `null`、数组或嵌套对象。
alarmd 额外取得、仅用于展示或资源丰富的字段放在 `extra_data.additional_dimensions`，
并与顶层维度保持 key 互斥。Linkd 的维度型 fingerprint 只读取顶层 `dimensions`；
配置引用的维度缺失或无效时会确定性拒绝消息。Enrich 可以读取维度作资源定位、展示和指标查询，
但不会回写 Event 的来源维度。NoData 等依赖维度标记的场景，也必须由上游将标记写入标准
`dimensions`；只留在旧协议的 `event.tags` 或原始快照中不会参与分类。

“严格完整”是生产者相对于其检测规则的语义责任，当前 Linkd/KAC 并没有跨来源的统一维度清单，
因此解析器只检查字段类型与已配置的 fingerprint 所需键，不能证明某来源已发送全部检测维度。
一般格式允许缺失 `dimensions` 并规范为 `{}`；实际来源若依赖这些维度关联或丰富，
必须在发布前自行校验完整性。主机来源的具体分工见
[主机推送告警示例](../../guides/host-alert-enrich-example.md#24-补充维度)。

## KAC 告警源插件发布前的字段校验

`validate_raw_event(event, tenant_id=当前租户)` 逐条执行以下检查。类型声明列出的可选字段
并不表示运行时对每个可选字段做了完整结构校验；未列出的更细约束仍以 Linkd Cleaner / Domain 为准。

| 字段 | KAC 当前校验 |
| --- | --- |
| 顶层 | 必须是 JSON object，且整个对象可序列化为有限数值的 JSON |
| `bk_tenant_id` | 必须是字符串，且与当前租户 ID 完全相等 |
| `fingerprint`、`title` | 必须是非空字符串 |
| `evaluations` | 必须是 1–32 项的数组；每项是 object |
| `evaluations[].action` | 必须为 `triggered`、`resolved` 或 `closed` |
| `evaluations[].severity` | 必须是非空字符串，且同一数组内不得重复原始 severity |
| `dimensions`、`labels` | 若存在，必须是 object；key 为非空字符串，value 为字符串、数字或布尔值，不能为 null |
| `values` | 若存在，必须是最多 256 项的 object；key 为非空字符串，value 为数字且不能为布尔值 |

以下字段在 KAC 发布前不是必填项。Linkd 的 `StandardCleaner` 解析 Value 后，
`EventFactory` 和 `Event.Normalize` 对缺失值作如下处理；默认值写入 Linkd 的 Event，
不会修改 Kafka Value，也不会回填到保存原文的 `Event.source_raw_data`。

| 可选字段 | Linkd 缺失时的结果 | 需要注意 |
| --- | --- | --- |
| `content` | Event 的 `content` 为 `""` | 不根据 `title` 自动生成内容 |
| `evaluations[].action_reason` | 对应判定的 `action_reason` 为 `""` | 不根据 action 自动生成原因 |
| `dimensions`、`labels` | Event 中分别规范为 `{}` | 不从 `subject` 或其他字段补维度、标签；KAC 发布前校验会拒绝显式 `null` |
| `values` | Event 中规范为 `{}` | 不给缺失的数值项补零；KAC 发布前校验会拒绝显式 `null` |
| `subject` 及其各子字段 | Event 的 `subject_system/type/id/name` 分别为 `""` | 不根据 `dimensions` 推断对象身份 |
| `occurred_at`、`produced_at` | 各自使用 Kafka record timestamp 形成的稳定 `RawEventMessage.ReceivedAt` | 两者独立回退；不会用当前处理时间或用其中一个字段推导另一个；非法时间格式会解析失败 |
| `extra_data` | Event 中规范为 `{}` | 不自动生成 `anomaly_begin_time` 或 `additional_dimensions`；若显式提供后者，仍须是合法对象且不能与 `dimensions` 重名 |

`content`、`action_reason`、`subject`、两个时间字段及 `extra_data` 显式为 JSON `null` 时，
Linkd 也会先解码为零值，再按上表处理；空时间字符串则会解析失败。
字段类型错误仍可能导致拒绝。其中 `dimensions`、`labels`、`values` 的显式 `null` 虽可在 Linkd 侧形成空对象，
但不满足当前 KAC 的发布前校验，KAC 插件应省略字段或发送 `{}`。

KAC 校验 fingerprint/title/evaluations、动作、等级、可选字段基本类型及 JSON 可序列化性；时间具体格式、extra_data.additional_dimensions、action_reason 长度和来源 severity 映射后的冲突仍由 Linkd 校验。Linkd 仍会检查 payload 为单个 UTF-8
JSON object、重复 key、合法时间、维度及等级等约束。`values` 的 key 长度为 1–256 bytes，值须是
有限数字；`extra_data.additional_dimensions` 与顶层 `dimensions` 不得有重复 key。
`evaluations[].action_reason` 最多 256 bytes。生产者应在发布前按这些约束构造数据，
不能把通过 KAC 发布前校验等同于 Linkd 已接收。

主机示例为了完成 BASE_COLLECT 的策略、资源、指标与告警源丰富，还需要
`labels.strategy_id`、`labels.strategy_version`、`labels.bk_biz_id` 均为正整数；
这是**该丰富链路**的要求，不是所有 `standard` RawEvent 的通用必填字段。
`occurred_at` 表示事实发生时间，`produced_at` 表示消息首次生成时间；重试保留原值。
检测维度放在 `dimensions`，补充展示/资源维度放在 `extra_data.additional_dimensions`，
`values` 仅记录扁平观测数值，且不参与 fingerprint。

## 与KAC `AlarmEvent` 字段映射关系

KAC 旧 `AlarmEvent` 是 `src/kingeye/kac/alarm/models.py` 的 ES 文档，字段由
`src/kingeye/base/domains/alarm/models/alarm_event_document.py` 定义。它同时包含来源事实、
KAC 处理状态和丰富后的字段，**不能整体序列化为 Linkd RawEvent**。下表是新插件构造 payload
时的语义映射，不表示现有旧插件或旧 ES 记录已经自动迁移。

| 旧 KAC `AlarmEvent` 字段                                               | Linkd 输入                                        | 转换边界                                                                                                                                      |
|---------------------------------------------------------------------|-------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------|
| `bk_tenant_id`                                                      | `bk_tenant_id`                                  | 使用当前租户；与 Kafka header 一致                                                                                                                  |
| `event_id`                                                          | 通常作为 `fingerprint` 的候选                             | 旧 KAC 用它关联同一告警的活动与终态；核对来源是否在整个生命周期保持稳定，再用于来源告警身份                                                                                          |
| `alarm_id`                                                          | 通常作为 `event_id` 的候选                             | 旧 KAC 每条告警记录的身份；必须确认每次新判定唯一、重投稳定，并按租户与来源隔离，不能把当前时间或随机新值用作重试身份                                                                             |
| `name`、`content`                                                    | `title`、`content`                               | `title` 必须非空；来源详情保留原意                                                                                                                     |
| `action`、`level`                                                    | `evaluations[].action`、`evaluations[].severity` | `firing → triggered`，`resolved → resolved`，`close → closed`；`heartbeat` 没有直接对应动作。旧 `remind/warning/fatal` 必须与 EventSource 的 Severity 配置匹配 |
| `alarm_time`                                                        | `occurred_at`                                   | 从旧时间格式转换为带时区的 RFC3339 字符串；不要将 `storage_time` 当成事实时间                                                                                       |
| `object`、`bk_obj_id`、`bk_inst_id`                                   | `subject.name/type/id`                          | 有确切 CMDB 身份时设置 `subject.system=cmdb`；仅有展示字符串时不伪造实例 ID                                                                                     |
| `bk_biz_id`、可核实的检测维度                                                | `labels.bk_biz_id`、`dimensions`                 | BASE_COLLECT 的业务 ID 放入 labels；检测维度逐项转成标量，`dimension_info` 文本不能原样充当对象                                                                      |
| `strategy_id` 等策略身份                                                 | `labels.strategy_id`、`labels.strategy_version`  | 只有能取得正整数策略 ID **及其版本**时才满足主机示例的策略丰富；旧 `strategy_id` 本身不足以推断版本                                                                             |
| `meta_info` 等来源扩展                                                   | `extra_data`                                    | 选择可序列化且有明确语义的来源字段；避免把处理结果或敏感 payload 整体塞入                                                                                                 |
| `source_id`、`source_name`                                           | 来源配置与 `source` 丰富结果                             | `source_id` 是旧 KAC 告警源 ID；Linkd `EventSourceID` 取 `linkd_source_id`，不是直接复制旧 ID                                                            |
| `status`、`close_time`、`conductor`、`notify_status` 及已丰富的 CMDB / 展示字段 | 无直接输入映射                                         | 这些是旧 KAC 的处理状态、操作时间或派生结果；Linkd 生命周期与 Enrich 自行产生对应状态和投影；close_time 由 evaluations[].action=closed 和 occurred_at 共同裁决                       |

REST V2 直接接收 standard，仓库第三方插件直接构造相同协议，不先创建旧 KAC 告警结构。旧字段名只用于存量配置的发布映射，不是运行时兼容消息。

## 对接状态与继续阅读

KAC 全部适配器、模板、上传与导入统一 V2。extra_clean_config 和来源过滤仅在 Linkd 执行，顺序为清洗后过滤；过滤是正常结果，确认消息但不创建 Event 或进入生命周期。同步 ingest/preview 复用同一 Cleaner，output=standard 返回标准字段与 filtered_samples。独立恢复由 KAC 写入可信 kac_ingest_mode=recovery header，普通 payload 不能选择该用途。alarmd 继续直接推 Kafka；本地测试不等于第三方和真实双租户联调。

- [主机推送告警 Enrich 示例](../../guides/host-alert-enrich-example.md)
- [Cleaner 模块](../../modules/cleaner.md)
- [EventSource 模块](../../modules/event-source.md)
- [KAC Alarm Hook 输出契约](kac-alarm-output.md)：这是 Linkd **输出到 KAC** 的另一方向，字段不可与本页输入混用。

### 来源恢复查询

KAC 保留来源接口轮询，但候选告警读取 Linkd 的 `GET /api/v1/alerts`，不依赖 KAC 兼容文档中的来源身份。
请求必须包含 `bk_tenant_id` 和 `event_source_id`，`status=active` 包含仍活动的屏蔽及合并阻塞告警。
返回原始 Alert 字段及 `extra_data`，不以丰富结果覆盖来源事实。接口使用管理 JWT；`limit` 为 1–32，
默认 32，`after` 是绑定租户、来源和状态的分页游标。省略 `status` 查询该来源的保留历史；
每页返回 `items` 和 `next`。查询不承诺跨页事务快照，终态仍通过正常来源消息进入 Lifecycle。

## KAC V2 原生接入（2026-10-19）

standard 新增可选 fingerprint；提供时直接采用并跳过来源生成规则，缺失时保留既有算法。
KAC V2 要求该字段必填，不再要求 event_id/alert_id，租户由 KAC 认证后注入。
Cleaner 的 rules 按 bk_tenant_id 限定 assignments（term/regexp_extract）及 filter。
正式与 ingest/preview 共用；正常过滤返回 filtered_samples，消费确认但不创建 Event。
独立恢复通过可信传输头 kac_ingest_mode=recovery 跳过触发规则。
