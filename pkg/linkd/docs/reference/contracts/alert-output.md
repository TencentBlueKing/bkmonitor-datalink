# Kafka Alert V1 输出契约

FinalHook 在 Alert 发生真实变更后发送一个完整 V1 快照：

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
    "event_source_id": "source-a"
  }
}
```

`cause.type` 只允许 `source_event | user_operation | system_operation`，`cause.id` 是对应 Event ID 或稳定 operation ID。Kafka headers 同步携带 `message_id`、`schema_version`、`bk_tenant_id`、`alert_id`、`cause_type` 和 `cause_id`。

`message_id` 由租户、`alert_id` 和 `update_at` 确定性生成；partition key 由租户和 `alert_id` 确定性生成。生产端等待 all-ISR ACK，失败不伪装成已投递。

输出规则：

- 创建、同等级 triggered、resolved、closed 和内部直接关闭均输出当前 Alert；
- 等级升级按顺序输出旧 Alert closed 和新 Alert active 两个快照；
- suppressed、orphaned、rejected 和纯幂等重投不输出 Alert 快照；
- 输出使用 `update_at` 作为快照时间，不暴露 Repository 的 `VersionToken`。
