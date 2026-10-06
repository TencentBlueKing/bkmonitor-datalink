# 核心数据模型

本文是 Linkd 当前核心数据的权威定义，覆盖 `Event`、`EventProcessing`、`Alert`、`AlertLog` 以及
存储快照中的 `VersionToken`。字段、状态和不变量以当前 Go 类型、Repository 契约和测试为准。

EventSource 的配置与运行边界见 [EventSource](../modules/event-source.md)，对象如何产生和推进见
[Cleaner](../modules/cleaner.md) 与 [Lifecycle](../modules/lifecycle.md)。

## 1. 对象关系

```text
EventSource（静态配置）
  └─ 产生多个 Event
       ├─ EventProcessing：该 Event 的生命周期处理结果
       └─ accepted/suppressed 时 related_alert_id → Alert
                                      └─ 多条 AlertLog
```

- `Event` 是不可变的来源事实，只有 `related_alert_id` 可以由 Lifecycle 补写；
- `EventProcessing` 是 Event JSON 之外的技术处理状态；
- `Alert` 是由一个或多个被接受 Event 推进的当前生命周期快照；
- `AlertLog` 是不可变操作和输出流水，不是 Alert 当前态的一部分；
- `StoredEvent = Event + EventProcessing + VersionToken`；
- `StoredAlert = Alert + VersionToken`。

Event、Alert 和 AlertLog 是独立对象，Repository 不提供跨对象事务。跨对象步骤依靠稳定身份、幂等
创建、单对象 CAS 和 Lifecycle 恢复逻辑收敛。

## 2. 通用约束

- 所有对象都在 `bk_tenant_id` 作用域内读写；任何 ID、查询、锁和缓存键不得绕过租户；
- ID 由稳定输入确定性生成，重投时不变，不使用当前时间或随机值兜底；
- 所有时间进入领域对象前统一为 UTC，存储精度为纳秒；
- `dimensions`、`labels` 只接受扁平字符串、有限数字和布尔值；
- `source_raw_data`、`extra_data`、`enrich` 和 AlertLog `params` 是规范化 JSON object；
- 动态对象会被深拷贝，调用方不能通过共享 map 修改已存领域事实；
- `VersionToken` 只由 Repository 解释，调用方只能从读取结果原样交回同一对象的 CAS。

## 3. Event

Event 是 `RawEventMessage` 经来源 Cleaner 和 EventFactory 标准化后的来源事实。

### 3.1 字段

| 字段                               | 约束                           | 语义                                                                      |
| ---------------------------------- | ------------------------------ | ------------------------------------------------------------------------- |
| `bk_tenant_id`                     | 1–64 bytes                     | 归属租户；由信封或 EventSource `related_tenant_id` 决定                   |
| `event_source_id`                  | 1–32 bytes，`^[a-zA-Z0-9_-]+$` | 产生 Event 的 EventSource                                                 |
| `event_id`                         | 1–160 bytes                    | UTC 秒、租户、来源和 64-bit 稳定摘要组成的可解析身份                       |
| `fingerprint`                      | 1–128 bytes                    | Lifecycle 关联 active Alert 的业务键，由 EventSource 配置生成             |
| `related_alert_id`                 | 0–256 bytes                    | Cleaner 创建时为空；accepted/suppressed Event 写入关联 Alert ID            |
| `title`                            | 0–256 bytes                    | 来源标题；当前校验允许为空                                                |
| `content`                          | 0–1 MiB                        | 来源描述                                                                  |
| `severity`                         | 1–32 bytes                     | EventSource 映射后的 Linkd Severity name                                  |
| `action`                           | `triggered/resolved/closed`    | 来源事实对生命周期的动作                                                  |
| `action_reason`                    | 0–256 bytes                    | 动作原因，和 Event 内容分离                                               |
| `condition_key` / `condition_name` | 各 0–256 bytes                 | 稳定观测条件及展示名称                                                    |
| `dimensions`                       | 扁平 `DimensionMap`            | 参与检索和可选 fingerprint 计算的维度                                     |
| `subject_system`                   | 0–32 bytes                     | 来源声明的对象命名空间，当前不校验枚举                                    |
| `subject_type`                     | 0–128 bytes                    | 来源对象类型                                                              |
| `subject_id` / `subject_name`      | 各 0–256 bytes                 | 来源对象身份和展示名称                                                    |
| `occurred_at`                      | 非零 UTC 时间                  | 事实发生时间；来源缺失时使用稳定 received_at                              |
| `produced_at`                      | 非零 UTC 时间                  | 来源产生时间；来源缺失时使用稳定 received_at                              |
| `received_at`                      | 非零 UTC 时间                  | MQ 适配器提供的稳定接收时间                                               |
| `create_at`                        | 非零 UTC 时间                  | 当前 EventFactory 使用 received_at，保证重投确定性                        |
| `source_event_id`                  | 0–256 bytes                    | 来源事件身份；为空时 record ID 参与 Event ID 计算                         |
| `source_alert_id`                  | 0–256 bytes                    | 来源告警身份；只有被 fingerprint 配置引用时参与关联                       |
| `source_raw_data`                  | JSON object                    | 完整来源 payload，不在 Elasticsearch 建索引                               |
| `labels`                           | 扁平 `DimensionMap`            | 来源或接入侧标签                                                          |
| `extra_data`                       | JSON object                    | 不进入核心字段的来源扩展数据                                              |

### 3.2 不变量

- 新 Event 的 `related_alert_id` 必须为空；
- Event 创建后，除 `related_alert_id` 外的字段不得变化；
- 相同 `(bk_tenant_id, event_id)` 和相同内容是幂等重投，内容不同是身份冲突；
- `EventProcessing.state=accepted|suppressed` 时 `related_alert_id` 必填；
- orphaned、rejected Event 的 `related_alert_id` 必须为空。

## 4. EventProcessing

EventProcessing 是存储层与 Lifecycle 之间的处理元数据，不属于 Event JSON，也不是通用消息 Inbox。

| 字段           | 约束                                                | 语义                                                                                                                                                      |
| -------------- | --------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `state`        | `unprocessed/accepted/suppressed/orphaned/rejected` | Event 生命周期处理状态                                                                                                                                    |
| `outcome`      | 终态必填                                            | 具体裁决结果，例如 `alert_created`、`alert_updated`、`alert_rotated`、`alert_recovered`、`alert_closed`、`alert_suppressed`、`event_orphaned`、`rejected` |
| `reason_code`  | 可空                                                | 稳定低基数原因，例如 active Alert 不存在、等级升级或等级抑制                                                                                              |
| `processed_at` | 终态必填                                            | Lifecycle 完成裁决的 UTC 时间                                                                                                                             |

`unprocessed` 不允许带 outcome、reason 或 processed_at。终态结果与 `related_alert_id` 通过一次 Event
CAS 一起写入，避免出现 accepted 但未关联 Alert 的快照。

## 5. Alert

Alert 是一次异常的当前生命周期快照。它从 opening Event 创建；后续 Event 只推进生命周期字段，
不覆盖继承字段。

### 5.1 字段分组

| 分组     | 字段                                                                                           | 语义                                                  |
| -------- | ---------------------------------------------------------------------------------------------- | ----------------------------------------------------- |
| 身份     | `alert_id`                                                                                     | UTC 秒、租户、来源和 opening Event 摘要组成的可解析身份 |
| 关联     | `bk_tenant_id`、`event_source_id`、`fingerprint`                                               | active Alert 的唯一关联范围                           |
| 继承事实 | `title`、`content`、`severity`、condition、dimensions、subject、source IDs、labels、extra_data | 从 opening Event 复制，创建后不可修改                 |
| 当前状态 | `status`                                                                                       | `active/recovered/closed`；后两者为不可重新打开的终态 |
| 当前进度 | `latest_event_id`、`last_occurred_at`、`update_at`                                             | 最近被接受 Event 及严格单调的服务端快照时间           |
| 创建锚点 | `trigger_event_id`、`begin_at`、`create_at`                                                    | `create_at` 继承 opening Event 创建时间，创建后不变   |
| 终态     | `end_at`、`end_type`、`end_reason`                                                             | active 时必须为空；终态时必须完整                     |
| 丰富     | `enrich_status`、`enrich`                                                                      | 创建前同步计算一次，不覆盖来源事实                    |

`end_type` 只允许 `source/user/system/severity_upgrade`。recovered 的 end_type 固定为 source；closed
可以由来源关闭、用户/系统直接关闭或等级升级产生。

`enrich_status` 允许 `pending/succeeded/partial/failed`，但正常创建流程只产生 succeeded、partial
或 failed；当前 Noop Enricher 产生 succeeded 和空对象。

### 5.2 不变量

- 同一 `(bk_tenant_id, event_source_id, fingerprint)` 同时最多一个 active Alert；
- 继承字段和创建锚点在 CAS 更新中必须保持不变；
- `update_at` 必须严格大于旧快照，同一 Event 幂等重投不得推进它；
- active Alert 不得包含 end 字段；终态 Alert 必须包含合法 end_at/end_type；
- recovered/closed 不可再修改或重新打开；同一问题再次发生时创建新的 Alert；
- 后续 Event 与 Alert 的继承字段差异不会改写 Alert，需要最新来源描述时按 latest_event_id 回查 Event。

## 6. AlertLog

AlertLog 是围绕一条 Alert 的不可变流水，记录状态操作、抑制和最终输出结果。它不承担当前状态，
也不作为 CAS 对象。

| 字段             | 约束                                  | 语义                                                                            |
| ---------------- | ------------------------------------- | ------------------------------------------------------------------------------- |
| `log_id`         | 非空稳定身份                          | 由 Event/operation/cause、Alert 和 operation kind 等稳定输入生成                |
| `bk_tenant_id`   | 非空                                  | 与 Alert 相同的租户作用域                                                       |
| `alert_id`       | 非空                                  | 所属 Alert                                                                      |
| `operator_kind`  | `source/user/system`                  | 操作发起方                                                                      |
| `operation_kind` | `trigger/recover/close/suppress/push` | 状态操作或最终输出动作                                                          |
| `params`         | JSON object                           | 操作特有参数，例如 event_id、operation_id、reason、hook destination、message_id |
| `created_time`   | 非零 UTC 时间                         | 操作或输出结果发生时间                                                          |

相同稳定输入重复追加必须返回既有流水；相同 `log_id` 但内容不同是身份冲突。FinalHook 成功和失败都
会形成 push AlertLog，使外部输出结果可审计，但失败流水不会回滚已经完成的 Alert 状态变化。

## 7. Severity

Severity 是进程级、运行期冻结的有序 name 表。默认值为 `critical(1)`、`warning(2)`、`info(3)`，
priority 越小越严重。自定义 levels 整体替换默认表，name 和 priority 必须分别唯一；Event 和 Alert
只保存 name，不保存 priority。

EventSource 先使用 `severity_mapping` 映射来源值；未命中但原值已经是全局 Severity name 时直接使用，
否则使用来源 `default_severity`，再退回全局 `default_severity`。配置变更不会回溯改写存量 Event 或 Alert，当前实现也不会扫描存量数据校验
Severity name 兼容性。

## 8. 状态归属

| 问题                                 | 权威对象                              |
| ------------------------------------ | ------------------------------------- |
| 来源陈述了什么                       | Event                                 |
| Event 是否已被 Lifecycle 处理        | EventProcessing                       |
| 当前异常是否仍成立                   | Alert.status                          |
| 当前 Alert 由哪个 Event 推进到哪里   | Alert 的 latest/end 字段              |
| 谁执行了什么操作、输出是否成功       | AlertLog                              |
| 消息是否可由 MQ 再次投递             | MQ Receipt/offset，不进入上述领域对象 |
| 某 fingerprint 还有哪些 Event 待处理 | Redis Mailbox，不进入上述领域对象     |
