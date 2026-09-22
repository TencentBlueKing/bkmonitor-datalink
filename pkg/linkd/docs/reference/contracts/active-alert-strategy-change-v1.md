# 策略索引变更通知 v1

状态：对应当前实现。控制面策略投影任务向同一输出 Redis 发布集合变更通知，
集合 key 和 member 的协议不变。配置示例见[Hook 使用指南](../../guides/active-alert-by-strategy.md)。

## 消息

通知始终启用，channel 固定为 `<key_prefix>:changes`，无需也不接受独立的 `notify_channel` 配置。
同前缀的所有租户和策略共用该 channel；下面消息对应的 channel 为 `alarmd:open_alerts:changes`。
载荷为 UTF-8 JSON，最多 64 KiB：

```json
{
  "bk_tenant_id": "system",
  "strategy_id": "123"
}
```

`strategy_id` 使用与集合 key 一致的字符串；数字标签按 Hook 规则转为十进制字符串。
消息严格只含 `bk_tenant_id` 和 `strategy_id` 两个字符串字段，不携带 `version`、`database`、`key`、
密码、Alert 内容、fingerprint 或成员列表，不提供全局 revision、事件身份或历史回放。
Redis 连接、DB 和 `key_prefix` 由发布者与订阅者预先约定，消费者据此拼接集合 key。

## 触发条件

- 控制面发布的完整 fingerprint 集合与旧集合不同：发布一次。
- 完整结果为空而旧集合非空：删除正式集合并发布一次。
- 内容相同、查询失败、快照不完整、租约失效：不发布。
- Hook 仅提交刷新提示，不直接修改集合或发送对外通知；多个提示可以合并成一次刷新。
- 外部客户端直接修改集合不会自动通知；禁止其他写入者与控制面共同维护正式集合。

最终切换与条件通知在同一 Lua 中执行，没有订阅者时 `PUBLISH` 返回 0 仍是成功。
一次通知可能对应多个成员变化，不按告警事件或成员数量计数。

## 消费与故障边界

订阅者将消息视为“这个租户的策略集合需要重新读取”，可按 channel、租户、策略合并通知。
集合 key 为 `<key_prefix>:<bk_tenant_id>:<strategy_id>`，读取使用订阅者预先配置的 Redis 连接与 DB。
订阅确认后先补读一次，并保留周期补读；重连后重新补读。读取时集合已再次变化或 key 不存在都是正常情况。重建期间 key 不存在不能证明没有告警，应结合投影就绪状态。

Redis Pub/Sub 是至多一次投递，断线期间的消息不会补发，也没有消费 ACK。Lua 的原子执行不提供
错误回滚：例如集合命令成功后 `PUBLISH` 被 ACL 拒绝，控制面记录发布通知失败，但集合已经变化。
响应丢失时结果也可能不确定；无变化的重试不会额外发布。因此通知不能替代周期对账，也不证明下游已更新。

Pub/Sub 不按 DB 隔离。不同 DB 共用 channel 会收到彼此通知，消息本身无法区分 DB；
不同环境或需要区分的 DB 应配置不同 `key_prefix`，从而派生不同 channel。
控制面还需内部队列、租约、临时集合、RENAME 和 UNLINK 权限；详见使用指南。不需要 keyspace notifications。

官方语义：[Redis Pub/Sub 投递与 DB 作用域](https://redis.io/docs/latest/develop/pubsub/)、
[Redis Lua](https://redis.io/docs/latest/develop/programmability/eval-intro/)。
