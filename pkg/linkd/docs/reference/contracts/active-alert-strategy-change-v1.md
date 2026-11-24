# 策略索引变更通知 v1

状态：对应当前 `built_in_bk` 默认限流配置。活跃集合和通知由主流程增量维护，控制面差异修复同样可以发布通知。
规则格式和升级步骤见[活跃告警缓存与软限流](../../guides/active-alert-by-strategy.md)。

## 消息与集合

默认规则声明集合前缀 `alarmd:open_alerts` 和频道 `alarmd:open_alerts:changes`，可在来源配置中显式替换。
这份契约对应的配置保持两个字符串字段：

```json
{"bk_tenant_id":"system","strategy_id":"123"}
```

集合为 Redis Set，key 是 `<key_prefix>:<bk_tenant_id>:<strategy_id>`，member 为 fingerprint。
只包含 `built_in_bk` 的活动 Alert，使用 opening Event 原始 `labels.strategy_id`，不应用 Enrich 补丁。
数字转十进制文本，123 与字符串 `"123"` 共用集合，字符串 `"00123"` 独立。
租户、来源及规则以外的其他来源不会加入默认集合。连接和 DB 使用 Linkd 的 `storage.redis`。

消息不携带版本、数据库、密钥、正文、成员列表或可靠事件身份。channel 和字段映射在通用规则中声明，
缓存实现没有 `built_in_bk` 的协议分支。其他规则的自定义通知不属于此两字段协议。

## 触发条件

- 真正新增或移除 fingerprint 时发布；最后一个成员移除后集合自然消失。
- 持续触发、重复更新、重复终结、同一分组的等级轮换不发布。
- 后台实时修复增删成员时同样发布；扫描或查询失败不解释为空集合。
- 成员操作与发布使用同一 Redis Lua。Lua 不提供错误回滚，通知失败或响应丢失不能证明集合未变更。

不保证一条告警事件对应一条通知。订阅者收到消息后重读集合，可以按频道、租户、策略合并提示。

## 消费与故障边界

订阅确认、重连后补读，并保留周期补读。Pub/Sub 是至多一次投递，没有消费 ACK 或断线补发；
无变化的重试不会专门补发通知。它不证明 alarmd 已经更新自身状态。

集合没有 TTL，告警长时间无事件不等于已结束。首次重建或 Redis 丢失期间空集合不能证明没有活跃告警；
alarmd 在用空结果判定关闭前需确认缓存初始化及修复状态。本能力不维护额外的 Redis 就绪 key，
修复任务成功时间见控制面 `active-alert-indexes`；一次扫描成功不代表数据库和 Redis 具有线性一致性。

Pub/Sub 不按 DB 隔离。不同环境或数据库若需要区分通知，应在规则里配置不同 channel。
旧版本策略投影写入者必须先停止；旧 Hook 的独立 Redis 连接需要改为与 `storage.redis` 对齐。

官方语义：[Redis Pub/Sub](https://redis.io/docs/latest/develop/pubsub/)、
[Redis Lua](https://redis.io/docs/latest/develop/programmability/eval-intro/)。
