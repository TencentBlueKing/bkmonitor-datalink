# 数据流与状态

Kafka 消息由 `RumEventDeserializationSchema` 解析为 `RumEvent`，再由 `RumJobTopology` 分成 Session 和 View 两路。

| 分支 | 进入条件 | 分组字段 | 输出 |
| --- | --- | --- | --- |
| Session | `session.id` 非空，事件时间大于 0 | 业务、应用、Session ID | `SessionEvent` |
| View | `view.id` 非空 | 业务、应用、View ID | `ViewEventDocument` |

同一事件可以同时进入两路。两路分别保存状态，将聚合结果交给各自的 Elasticsearch Sink；设置 `sink.type=stdout` 时输出到控制台。

## 输入

常用 Kafka 消息格式为包含 `items` 数组的 JSON，每个 item 对应一条 Span：

```json
{
  "bk_biz_id": 1,
  "items": [{
    "app_name": "demo",
    "trace_id": "8a9c04cff257c21fd7cb2e37ec0f75a9",
    "span_id": "65d098884e12b153",
    "span_name": "browser.view",
    "start_time": 1785241902220000,
    "end_time": 1785241902220000,
    "attributes": {
      "session.id": "session-1",
      "view.id": "view-1",
      "span_type": "view"
    }
  }]
}
```

作业也兼容历史 flat event 格式。时间统一转换为微秒，`status` 兼容历史别名 `spanStatus`。解析规则见 [反序列化实现](../../src/main/java/com/tencent/bk/bkmonitor/rum/serde/RumEventDeserializationSchema.java)。

## 窗口与状态

窗口使用处理时间。Session 和 View 默认连续 15 分钟没有新事件时关闭，最长持续 240 分钟；收到对应的 `session.phase=end` 或 `view.phase=end` 时立即关闭。活动窗口默认每 30 秒输出有变化的结果。

事件身份由 `bk_biz_id`、`app_name`、`trace_id` 和 `span_id` 组成。缺少身份字段的事件不参与聚合；状态保留期间，相同身份的事件只累计一次。

| 状态 | 收到未见过的有效事件时 |
| --- | --- |
| 无窗口、无清理标记 | 创建窗口 |
| 窗口活动中 | 更新聚合状态和空闲截止时间 |
| 窗口已关闭、状态仍保留 | 输出修正后的关闭快照，沿用原窗口 ID 和关闭信息 |
| 状态已清理、清理标记仍在 | 事件时间不超过 watermark 时拒绝聚合，否则创建新窗口 |
| 清理标记已过期 | 可以创建新窗口 |

首次关闭后，Session 和 View 状态默认均保留 120 分钟（2 小时）；清理后再保留同等时长的标记。参数见 [构建与运行](source_compile.md)。Watermark 用于清理后的迟到判定，不决定活动窗口的关闭时间。

`close_reason` 记录首次关闭原因：SDK 结束事件为 `normal`，空闲超时为 `idle_timeout`，达到最大生命周期为 `max_life`。

## 输出与审计

- 每个窗口创建时生成 `window_id`，随状态保存。同一窗口的增量、关闭和修正快照使用相同文档 ID；清理后重开生成新 ID。索引规则见 [索引命名](../index-naming.md)。
- Session 将身份缺失、关闭后修正和清理后被拒绝的迟到事件送入审计侧输出，目标由 `late.sink.type` 决定。
- 无法解析的 Kafka record 被跳过；合法 envelope 内无法绑定的 item 单独跳过。解析错误记录日志和计数器，不进入上述审计侧输出。

## 恢复与升级

旧租户 key、包名或状态结构变更涉及 Checkpoint/Savepoint 兼容性。当前没有旧状态迁移器；旧租户 key 或缺少 `window_id` 的状态需从空状态启动。

升级这类旧作业时：

1. 记录旧配置、Kafka 位点和需要重建的数据范围，停止旧作业并备份相关 ES 数据。
2. 更新两份索引模板；已有索引需添加 `window_id` 的 `keyword` 映射。模板更新只影响新索引。
3. 核对分表数和集群 ID，按租户与日期处理不带窗口 ID 的旧文档；新文档 ID 不会覆盖它们。
4. 从空状态启动，按需要回放 Kafka 中仍保留的源数据。默认 `latest` 不回放历史；源数据已过期时无法完整重建。
5. 查询和报表需兼容 `idle_timeout`、`max_life` 及历史 `timeout`，并按业务、应用、实体 ID 和窗口 ID 核对结果。

从包含该窗口的 Checkpoint 恢复时会沿用窗口 ID 和去重状态。冷启动、清理后重开，或恢复到尚未包含该窗口的 Checkpoint，可能产生新窗口；多个窗口计数不能直接相加作为去重总数。

窗口按处理时间关闭，历史回放的窗口边界可能与原运行不同。ES 使用至少一次投递和同 ID 覆盖写，当前没有阻止旧快照覆盖新快照的版本校验。测试范围见 [集成测试](integration-tests.md)。
