# 按故障选择证据

接口参数与限制以 `api describe` 为准；本文件只描述调查逻辑。调用示例：

```bash
linkd-cli api call alerts.list --profile test --query bk_tenant_id=tenant-a --query event_source_id=source-a --query limit=20
linkd-cli api call events.get --profile test --path id=event-a --query bk_tenant_id=tenant-a
```

`test`、`tenant-a` 等只是示意，不能直接当作用户环境。命令成功的业务响应位于 `data`，保留内部时间戳、分页与状态。

## 事件没有形成预期告警

- 核对 `server.capabilities`、`event-sources.get`、`scheduling.get`。区分编辑 revision、已发布版本、Worker 实际任务和运行状态。
- 用 `events.list/get` 查看明确租户、来源、时间窗口的事件。区分事件不存在、未关联、处理失败和终态处理；以实际返回字段为准。
- 用 `alerts.list/get` 和 `alert-logs.list/stats` 关联告警及处理流水。使用 `related_alert_id`、`alert_id`、`fingerprint`、`source_event_id` 等已登记过滤器；过滤器能力可能依赖存储类型。
- 接入阶段异常检查 `runtime.cleaner` 和 `kafka.inspect`；事件之后异常检查 `runtime.lifecycle`、Redis PEL/Mailbox 和丰富状态。重投及 ES refresh 可能产生暂时可见性差异，不能仅凭一次列表无结果下结论。
- 不创建伪事件、不跳过失败消息、不删除队列或缓存来“验证”猜测。

## 积压、延迟或吞吐下降

- 同一时间窗口联合查看 `runtime.processes`、对应角色 runtime、`metrics.query`；先读 `metrics.catalog` 理解单位及指标语义。
- Kafka 使用 `kafka.inspect` 对照来源、topic、partition、消费组、lag。来源映射不明确时先定位，不能任选 topic 宣告无数据。
- Redis 使用 `redis.inspect/pending/mailboxes/leases`，**显式传 event_source_id**。省略时部分接口选择首个来源。观察 PEL、重投次数、idle、Mailbox 深度和 lease 状态，保留扫描截断标识。
- 控制面用 `runtime.control-plane` 看任务状态，再结合 `elasticsearch.performance/topology` 和归档 backlog。CPU、UP、一次低 lag 不足以证明积压已恢复；对比有时间戳的连续快照和最老未处理时间。
- 限流、扩容或改变并发前先形成下游容量预算与验证方案；CLI 不提供这些操作时，不假装支持，也不擅自通过其他工具执行。

## 丰富失败或字段异常

- 查询目标 Alert、来源发布版本和 `enrich.config`，区分当前已发布规则与未发布编辑规则。
- 读取 `alert-logs.list` 的历史处理流水，关联失败时间和当时的事件/配置证据；当前规则和当前预览只能证明当前现象，不能单独解释历史根因。
- 用 `enrich.preview` 传租户、来源，以及已有 `alert_id` 或临时 `alert` 二选一。关注 trace、status、changes 和 previous_changes；预览不证明历史处理当时使用了同样的规则或数据。
- 用 `onemodel.search/related` 检查模型、实例、关系和租户条件。分页保持条件与 limit；放弃查询时用 `onemodel.close` 释放最后返回的游标。游标过期需重新查询，不把 410 当成没有实例。
- 区分字段缺失与 null、查询未命中与查询失败。超限、超时或数据源不可用不能被解释为成功的空集合。
- 遇到 429、503 或超时先停止重复预览，检查可用性和预算；确认瞬时故障且确有取证必要时，间隔至少 5 秒最多再查询一次。仍失败就报告缺失证据，不循环重试。

## 配置、调度和策略索引不一致

- `server.config` 仅代表 Console 的脱敏启动配置；用 `dynamic-config.get` 对照控制面快照、last_success、digest 和各 Worker 状态。
- `event-sources.get/list` 与 `scheduling.get` 联合核对启用状态、发布进度、任务分配及运行代次。来源列表使用 after 分页，不能遗漏后续页。
- `strategy-index.targets/browse/reconcile` 都是只读检查；`reconcile` 名称不代表修复。精确检查传租户和策略 ID。
- 先读取 `strategy-audits.latest/get` 的已有结果。启动或取消审计必须最终确认：任务会扫描目标来源/Hook 下的全部租户并消耗资源，不能声称它只影响当前租户。
- 索引对比不具备跨 Redis 与存储的事务快照；并发告警变化、ES refresh、扫描上限会影响判断。确认不一致后再提出处置，不能自动修复。
