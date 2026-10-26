# 活跃告警缓存与软限流

EventSource 的 `active_alert_limits` 同时声明活跃集合、软限流和可选成员变化通知。所有角色复用
`storage.redis`，不配置独立 Redis，不再使用 `active-alert-by-strategy` Hook。
Alert 存储是事实来源；Lifecycle 尽力增量维护缓存，控制面定期修复。

## 1. 来源配置

将以下片段合并到已有 EventSource，保留原 Kafka、清洗、丰富和其他输出配置，随后通过管理 API、
provider 或显式 `linkd event-source import --file <yaml>` 发布。常驻进程不会自动导入 YAML 来源。

```yaml
event_source_id: built_in_bk
active_alert_limits:
  enabled: true
  count_cache_ttl_milliseconds: 1000
  count_cache_max_groups: 4096
  timeout_milliseconds: 100
  rules:
    - name: strategy
      group_by: [labels.strategy_id]
      max_active: 5000
      key_prefix: alarmd:open_alerts
      notify:
        channel: alarmd:open_alerts:changes
        fields:
          bk_tenant_id: bk_tenant_id
          strategy_id: labels.strategy_id
```

控制面在同步、发布前，仅为已有且启用的 `built_in_bk` 补齐缺失的单字段
`labels.strategy_id` 规则。默认阈值为 5000，前缀和通知如上；已有策略规则不覆盖，显式
`enabled: false` 不强行补齐。已停用或删除的来源不会重新启用，不存在的来源不会自动创建。
启动及每 30 秒检查存量来源，通过同一 Record/Release 发布流程补齐；配置相同不重复发布。
Cleaner、Lifecycle 和通用配置默认值中没有来源名称判断。以后 KAC 显式发布完整规则后可以删除控制面的补齐逻辑。

其他来源可配置来源总量及每条策略的软限额：

```yaml
event_source_id: source_demo
active_alert_limits:
  enabled: true
  rules:
    - name: source-total
      group_by: []
      max_active: 1000
    - name: strategy
      group_by: [labels.strategy_id]
      max_active: 200
```

- `enabled` 省略时为 true；false 只关闭拦截，仍维护已配置规则的集合与通知。
- 普通来源省略整个配置时不启用该能力；清空规则后停止维护原集合；修改或移除前缀后，原前缀按需人工清理。
- `name` 在来源内唯一；`max_active` 为正整数，不用特殊阈值表示关闭。
- `group_by` 支持 `labels.*`、`dimensions.*`、`subject_system`、`subject_type`、`subject_id`。
  空数组表示来源总量；多个字段组成联合分组。字段缺失或为空时跳过该规则并记录指标。
- 分组使用 opening Event 的原始字段，不使用 Enrich 补丁。分组值转换为文本，数字 123 和字符串
  `"123"` 同组，`"00123"` 独立。后续事件不改变已存在 Alert 的归组。
- `key_prefix` 省略时派生部署、来源和规则作用域；自定义前缀必须由一个来源规则独占，不与其他规则嵌套或复用。
- `notify` 可省略；配置时必须给出 `channel` 和 `fields`。字段只直接映射租户、来源或分组路径，输出为字符串；不支持模板和表达式。

## 2. 集合与通知

集合类型为 Redis Set，成员为 fingerprint，没有活跃成员 TTL。缺少事件不能证明告警已经结束。

| 分组 | 集合 key |
| --- | --- |
| 无字段 | `<key_prefix>:<bk_tenant_id>` |
| 单字段 | `<key_prefix>:<bk_tenant_id>:<value>` |
| 多字段 | `<key_prefix>:<bk_tenant_id>:<JSON字符串数组>` |

默认 `built_in_bk` 策略集合为 `alarmd:open_alerts:<bk_tenant_id>:<strategy_id>`，只包含该来源。
成员实际增删时向 `alarmd:open_alerts:changes` 发布两字段 JSON；持续触发、重复更新和同组等级轮换不发送通知。
其他来源可以配置自己的集合和通知，运行时实现不判断 `built_in_bk`。
完整对外约定见[策略索引变更通知 v1](../reference/contracts/active-alert-strategy-change-v1.md)。

本能力只保存上述分组 Set，member 只有 fingerprint，不存 Alert ID 或反向分组信息。
默认前缀为 `linkd:active-alerts:<base64url(deployment)>:<event_source_id>:rule:<name>`。
不增加成员 Hash、租户目录、就绪标记、临时集合、独立计数或修复来源锁。
通知 channel 是 Pub/Sub 频道，不是 Redis key；修复复用原有 fingerprint 锁，不另建一套锁。

## 3. Cleaner 和故障行为

Cleaner 在 Event 幂等落库后、Mailbox 入队前判断，只对首次创建的纯 triggered Event 执行：

1. 同批按分组去重读取数量，来源任务内默认缓存 1000ms，最多 4096 个组；两项均可配置。
2. 分组未满直接放行，不查询 fingerprint；任一规则已满时，对各自分组 Set 批量查询 fingerprint 成员关系。
3. 对应满额分组中已存在的 fingerprint 放行，新 fingerprint 保存 `suppressed / alert_limited` 终态，原因是
   `active_alert_limit_exceeded`，诊断保存在 `processing.active_alert_limit`，包含规则、分组、数量和阈值。
4. 限流 Event 不进入 Mailbox、Enrich，正常确认消息；重投复用终态。既有 unprocessed Event 不因重投时额度变化被丢弃。

含 resolved/closed 的事件整条放行，包括混合触发；Lifecycle 不做第二次配额检查，也不预占额度。
查询失败时放行；缓存丢失或尚未补齐时按现有 Set 数量判断，允许重建期间超量。缓存写失败记录边界错误，不影响已经持久化的生命周期，由任务修复。
主流程缓存操作默认最多 100ms，不增加应用层重试；连接池与内部 Redis 客户端共用。

这是软限额：例如已有 199 条，一批 20 个新 fingerprint 可以全部通过。数量缓存、并发和在途事件均可能造成超量，
Redis 故障时也会放行，不保证固定超量上界。它减少后续丰富和生命周期开销，不限制 Event 存储写入量。

## 4. 后台修复与运维

```yaml
control_plane:
  active_index:
    poll_interval_seconds: 1
    reconcile_interval_seconds: 60
    operation_timeout_seconds: 60
    page_size: 2000
    max_bytes: 33554432
```

启动及周期读取已发布来源。每来源在有界扫描中读取租户、Alert ID、fingerprint 和配置的原始分组字段，
不读取 Enrich 或正文；ES 使用 PIT 分页，MySQL 单条 SELECT 流式交付。
先批量比较 active 与 Set，补齐缺失成员；再扫描当前规则前缀下的 Set，将成员按租户与来源分批回查存储。
只有缺失、残留或归组不符的成员才取得现有 fingerprint lease 并读取当前 Alert 后修复；健康成员不逐条查询或重复写入。
不维护临时 Redis 集合，不整体替换在线集合，不另建来源修复锁。存储查询失败时停止本轮，不能当作不存在。
扫描期间的新建告警即使不在批量结果中，也经锁内当前状态读取后决定是否保留。

Redis 丢失后可从数据库 active 重建。停用/删除来源的现存告警仍由真实终态决定是否离开集合。
每页和每来源有预算；修复可能因存储延迟、锁竞争或故障推迟，不承诺固定恢复时限。

观察控制面任务 `active-alert-indexes`、`source-limit-defaults`，以及
`linkd.active_alert_cache.operations` 的更新、修复、限流、缺失字段和降级结果。
指标不包含租户、策略或 fingerprint 等高基数字段。错误日志保留底层错误及已有身份，不记录正文和凭据。

## 5. 从旧 Hook 配置升级

停止旧版 Cleaner/Lifecycle 和策略投影写入者，将来源中的 `active-alert-by-strategy` Hook 删除，
改为本页 `active_alert_limits`。将原 key 前缀和 channel 写入规则；alarmd 改为读取与 Linkd
`storage.redis` 相同的实例及 DB，然后启动新版控制面与 Worker。
不保留独立输出 Redis、旧 Hook 运行时或双写兼容。历史 Release 不回写；更新来源发布新 Release。旧参数仅保留历史读取与脱敏，任何新发布都拒绝旧 Hook。
如果原来源仍声明已删除 Hook，先通过新版管理 API 替换配置，再启动该来源 Worker。

在 alarmd 使用数据前确认后台修复成功。重建期间空 key 不能证明没有活跃告警，不能据此批量关闭告警。
本地门禁和隔离 Redis 测试不代表目标环境已完成 alarmd 接入或生产容量验证。
