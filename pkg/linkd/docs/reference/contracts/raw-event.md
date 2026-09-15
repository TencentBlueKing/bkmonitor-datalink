# `standard` 来源 Cleaner 输入契约

消息队列适配器首先形成只读 `RawEventMessage`：稳定 record ID、租户、配置确定的 event_source_id、
稳定 received_at、headers 和原始 payload。当前 Kafka 适配器从 header 读取 bk_tenant_id，并使用
record timestamp 作为 received_at。`event_sources[].cleaner.type=standard` 时，payload
必须是一条 UTF-8 JSON object；重复 key、尾随 JSON、数组顶层和已知字段非法值会被确定性拒绝。
未知字段不参与 Event 构造，只在完整 `source_raw_data` 快照中保留。

```json
{
  "event_id": "source-event-1",
  "alert_id": "source-alert-1",
  "title": "CPU high",
  "content": "CPU usage is high",
  "severity": "P2",
  "action": "triggered",
  "action_reason": "",
  "condition_key": "cpu",
  "condition_name": "CPU 使用率",
  "dimensions": { "host": "host-1", "usage": 92.5 },
  "subject": {
    "system": "cmdb",
    "type": "host",
    "id": "1",
    "name": "host-1"
  },
  "occurred_at": "2026-09-01T00:00:00Z",
  "produced_at": "2026-09-01T00:00:01Z",
  "labels": { "team": "ops" },
  "extra_data": {}
}
```

- action 必填且只允许 `triggered | resolved | closed`。
- payload severity 是来源原值；默认 SeverityResolver 依次使用 severity_mapping、全局同名 Severity、来源 default_severity 和全局 default_severity。
- event_id 和 alert_id 分别映射为 Event.source_event_id 和 Event.source_alert_id，二者都允许为空。
- bk_tenant_id、event_source_id、related_alert_id、fingerprint、received_at、create_at 和 source_raw_data 即使出现在 payload 中也不会覆盖 EventFactory 的结果，只会保留在原始快照中。
- `RawEventMessage.bk_tenant_id` 来自适配器；EventSource.related_tenant_id 非空时强制覆盖消息租户，否则消息租户必填。
- occurred_at 缺失时使用稳定 received_at；produced_at 缺失时同样使用 received_at。
- event_id 为空时使用稳定 record ID 参与 Linkd Event ID 摘要。
- 完整 payload 自动保存为 Event.source_raw_data；不记录到日志，也不在 Elasticsearch 建索引。

StandardCleaner 只把已知来源字段展开为 EventDraft；EventFactory 独占租户、来源、标准 severity、
fingerprint、确定性 Event ID、接收时间和完整原始快照。Event 在所属 lane 中
create-only 成功并写入 Redis Mailbox 后，原 delivery 才允许确认；Signal 只负责唤醒该 Mailbox，
不携带单个 Event 的处理责任。
