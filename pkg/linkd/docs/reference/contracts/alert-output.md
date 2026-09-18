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

输出规则：

- 创建、同等级 triggered、匹配活动级别的 resolved/closed 和内部直接关闭均输出当前 Alert；
- 等级升级使用 close_and_create 时按顺序输出旧 Alert closed 和新 Alert active；update_current 只输出同一 alert_id 的 active 快照，新 severity 生效，update_at 推进；
- 逐级 suppressed/orphaned 不产生独立 Alert 输出；同一事件内被接受的其他判定仍可输出。整个事件 suppressed/orphaned/rejected 和终态幂等重投不输出；
- 输出使用 `update_at` 作为快照时间，不暴露 Repository 的 `VersionToken`。

计划恢复时若 active 快照已被更晚的独立关闭推进为终态，不重发旧 active 快照。

Event 的 values/evaluations 不直接加入 Alert V1 信封；Alert severity 现在表示当前级别，可在同一 alert_id 内升级。
