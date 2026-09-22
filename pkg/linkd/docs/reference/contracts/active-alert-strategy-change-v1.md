# 策略索引变更通知 v1

状态：对应当前实现。`active-alert-by-strategy` 自动向同一输出 Redis 发布集合变更通知，
集合 key 和 member 的协议不变。配置示例见[Hook 使用指南](../../guides/active-alert-by-strategy.md)。

## 消息

通知始终启用，channel 固定为 `<key_prefix>:changes`，无需也不接受独立的 `notify_channel` 配置。
同前缀的所有租户和策略共用该 channel；下面消息对应的 channel 为 `alarmd:open_alerts:changes`。
载荷为 UTF-8 JSON，最多 64 KiB：

```json
{
  "version": 1,
  "database": 8,
  "bk_tenant_id": "system",
  "strategy_id": "123",
  "key": "alarmd:open_alerts:system:123"
}
```

`strategy_id` 使用与集合 key 一致的字符串；数字标签按 Hook 规则转为十进制字符串。
消息不包含密码、Alert 内容、fingerprint 或成员列表，不提供全局 revision、事件身份或历史回放。
`database` 是发布客户端实际选择的逻辑 DB；Pub/Sub channel 自身不按 DB 隔离。

## 触发条件

- `SADD` 实际新增一个成员：发布一次。
- `SREM` 实际移除一个成员：发布一次，包括删除最后一个成员、集合 key 消失的情况。
- 操作返回 0、策略缺失/为空而跳过、集合修改失败：不发布。
- 同一成员可能经历多次真实加入/移除，每次变化都通知；不是按 Alert 事件去重。
- 多个 Worker 同时添加已有/相同成员，或同时移除同一成员，只为实际变化的命令发布。
- 只覆盖 Hook 写入；其他客户端直接修改集合不会自动通知。共享前缀的 Hook 自动使用同一 channel。

集合修改与条件发布在同一 Lua 中执行，避免客户端先查询再修改造成竞争；没有订阅者时
`PUBLISH` 返回 0 不代表失败。所有此类 Hook 都需要脚本执行及发布权限。

## 消费与故障边界

订阅者将消息视为“这个 key 需要重新读取”，可按 database、tenant、key 合并通知。
订阅确认后先补读一次，并保留周期补读；重连后重新补读。读取时集合已再次变化或 key 不存在都是正常情况。

Redis Pub/Sub 是至多一次投递，断线期间的消息不会补发，也没有消费 ACK。Lua 的原子执行不提供
错误回滚：例如集合命令成功后 `PUBLISH` 被 ACL 拒绝，Hook 返回失败，但集合可能已经变化。
响应丢失时结果也可能不确定；无变化的重试不会额外发布。因此通知不能替代周期对账，也不证明下游已更新。

不同 DB 共用 channel 会收到彼此通知，消费者须核对 `database` 和租户；不同环境应使用不同 channel。
连接权限至少应包含原有集合操作、`EVAL` 及对应 channel 的 `PUBLISH`；不需要配置 keyspace notifications。

官方语义：[Redis Pub/Sub 投递与 DB 作用域](https://redis.io/docs/latest/develop/pubsub/)、
[Redis Lua](https://redis.io/docs/latest/develop/programmability/eval-intro/)。
