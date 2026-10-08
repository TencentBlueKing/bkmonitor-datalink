# Kafka Alert V1 输出契约

`EventSource.hooks` 中 `type: kafka` 的每个实例在 Alert 发生真实变更后发送一个完整 V1 快照：

```json
{
  "message_id": "<opaque-digest>",
  "schema_version": "1",
  "bk_tenant_id": "system",
  "alert_id": "<opaque-digest>",
  "update_at": "2026-09-01T00:00:00Z",
  "cause": {
    "type": "source_event",
    "id": "<opaque-digest>"
  },
  "enrich_status": "succeeded",
  "alert": {
    "event_source_id": "source-a",
    "event_source_version": 1
  }
}
```

`cause.type` 只允许 `source_event | user_operation | system_operation`，`cause.id` 是对应 Event ID 或稳定 operation ID。Kafka headers 同步携带 `message_id`、`schema_version`、`bk_tenant_id`、`alert_id`、`cause_type` 和 `cause_id`。

`message_id` 由租户、`alert_id` 和 `update_at` 确定性生成；partition key 由租户和 `alert_id` 确定性生成。生产端等待 all-ISR ACK，失败不伪装成已投递。

Alert 快照中的 `event_source_version` 为正整数，记录创建时继承的来源发布版本；同级更新与 update_current 升级不覆盖。
完整 Alert 快照现在包含必填 `revision`（1..2^53-1）以及 `projection` 目标水位。业务变更推进 revision，
单独确认同步水位不生成新业务快照，也不触发本 Hook。目标引用只含来源版本，不包含 endpoint 或凭据。
这两个字段不改变 V1 的 message_id/partition key 规则；新的可靠 KAC 投影协议与任务独立实现，正式生产装配仍在开发，
不能把收到此 Kafka 消息当作 KAC 已同步或已可搜索的确认。

[KAC 动作投递 V2](kac-action-delivery-v2.md)另行定义冻结动作、持久重试、投影可见性与接收端受理确认。
Lifecycle 已提供原子动作意图与独立入队端口，正式进程和调度尚未装配。本 Kafka Hook 仍使用原普通失败语义，
不继承新协议的可靠保证；配置装配须避免对同一处置目的端同时启用旧 action Hook 和新可靠出口。

输出规则：

- 创建、同等级 triggered、匹配活动级别的 resolved/closed 和内部直接关闭均输出当前 Alert；
- 等级升级使用 close_and_create 时按顺序输出旧 Alert closed 和新 Alert active；update_current 只输出同一 alert_id 的 active 快照，新 severity 生效，update_at 推进；
- 逐级 suppressed/orphaned 不产生独立 Alert 输出；同一事件内被接受的其他判定仍可输出。整个事件 suppressed/orphaned/rejected 和终态幂等重投不输出；
- 输出使用 `update_at` 作为快照时间，不暴露 Repository 的 `VersionToken`。

计划恢复时若 active 快照已被更晚的独立关闭推进为终态，不重发旧 active 快照。

Event 的 values/evaluations 不直接加入 Alert V1 信封；Alert severity 现在表示当前级别，可在同一 alert_id 内升级。

## 策略状态变化

通用 Kafka Hook 同样接受等级变化、屏蔽状态变化和合并窗口释放；信封及 `message_id` 规则不变，
使用对应的 source_event/system_operation cause，并发送完整 Alert 快照。是否允许处置仍由
Lifecycle 的 `state/action` Hook 分工和 admission 决定，不能将任意 active 状态同步解释为新触发。
合并变化的稳定操作、窗口、关系和本次处置资格在待输出快照的 `merge_change` 中保存；
kind 区分 release/member_link/parent_ready/member_unlink/parent_recover。
父终结后的 member_unlink 仅同步状态并记录 merge_end 流水，不发送子告警 action。
parent_recover 使用 alert_recovered outcome，仅在父曾被放行时发送恢复处置。
