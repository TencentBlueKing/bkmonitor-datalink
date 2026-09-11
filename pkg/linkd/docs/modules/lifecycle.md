# Lifecycle

Elasticsearch runtime 默认使用进程内共享合批器，将不同 Mailbox 已就绪的 Event result CAS、Alert
CAS/create 和 AlertLog create 合并发送。现有校验、缓存修复和错误映射由 Repository 继续负责；
调用方收到对应 item 成功后才进入下一步，不能提前输出或 ACK。批次内 CAS 校验与冲突核对使用
realtime `_mget`；Lifecycle 的 Event 点读排除不参与裁决的 `source_raw_data`，终态 CAS 使用局部
Bulk update，只修改 `related_alert_id` 和 `processing`。公共 Event 查询仍返回完整文档。
写批次参数自动匹配 Lifecycle 并发，不允许独立手调：
单批上限为并发的一半（向下取整、最少 1、最多 128），写侧等待为该上限的毫秒值（单项不等待），
执行上限为并发数与 32 的较小值；默认并发 32 对应 16 项、16ms、32 批，字节预算默认 4 MiB。
读侧等待取写侧等待与 10ms 的较小值，使用独立收集队列；达到操作数或字节上限立即发送，
未满则在首项期限到达时发送，不等待写期限；
读写共享执行并发上限。实际批次大小还受调用方并发与写入依赖限制。完整配置见[配置指南](../guides/configuration.md)。

Lifecycle 负责把已经持久化的 unprocessed Event 裁决为 Alert 创建、推进、终结、等级升级、抑制或
孤儿结果，并写回 EventProcessing。它不接收原始 MQ payload，也不负责 Cleaner 的 Event 创建和 ACK。

## 1. 核心概念

### 1.1 关联键

Lifecycle 查找 active Alert 的唯一条件是：

```text
(bk_tenant_id, event_source_id, fingerprint)
```

`source_alert_id` 只是 Event 来源事实，只有 EventSource 明确选择它生成 fingerprint 时才间接参与
关联，Lifecycle 不提供绕过 fingerprint 的查询旁路。

### 1.2 MailboxID

```text
MailboxID = CorrelationKey(bk_tenant_id, event_source_id, fingerprint)
```

MailboxID 同时是 Redis Mailbox 身份、Lifecycle Signal 的 `Message.ID/OrderKey` 和分布式 lease identity。
同一 Mailbox 中只保存属于同一租户、来源和 fingerprint 的 Event ID。

### 1.3 Mailbox 与 Signal

Redis Mailbox 只使用 `<key_prefix>:<mailbox_id>:events` List，按成功入队顺序保存 Event ID。它不再维护
去重 Set、scheduled marker 或全局精确计数；单 Mailbox 默认上限为 128。

Signal schema v1 只携带租户、来源、fingerprint、MailboxID 和入队时间，不绑定单个 Event。入队 Lua 先
读取 `LLEN` 并检查单箱上限；原长度为零时先 `XADD` Signal，再 `RPUSH` Event ID，非空时只追加 List。
脚本运行时错误不会回滚先前命令，因此这个顺序把失败方向限制为最多产生一次空唤醒，不会形成无 Signal
的非空 Mailbox。

Mailbox 允许相同 Event ID 出现多次。Event 业务去重由 Repository 的稳定身份负责；Lifecycle 逐条处理，
已经终态的 Event 由 `ProcessEvent` 直接返回保存结果。

### 1.4 Lease

lease 保护同一 Mailbox 的跨进程串行处理。RedisLocker 使用 `SET NX PX` 获取随机 token，并通过
compare-token Lua 续租和释放；旧 owner 不能误续租或删除新 owner 的 lease。

### 1.5 Recent Alert 缓存

Elasticsearch 后端使用所有 Lifecycle 实例共享的 Redis Recent Alert 缓存跨越搜索 refresh 窗口。缓存
从 Mailbox `key_prefix` 派生 namespace，分别按 MailboxID 保存最近的 current Alert、按租户和 EventID
保存最近的 terminal Alert。TTL 等于 Active Alert `refresh_interval + 5s`，默认为 10 秒。value 包含
完整 Alert 快照和存储 `VersionToken`，但它不是
Alert 事实源，也不会保存全部 active Alert。

Event 裁决先查缓存；命中 active 时直接使用，命中 terminal 时把它视为“当前无 active”，并通过 ended
条目恢复同一 Event 的终结或等级升级。只有 Redis 明确返回 key 不存在时才查询 Elasticsearch；Redis
错误、缓存损坏或身份不一致均保留 Mailbox 队首重试。MySQL 后端不启用该缓存。

## 2. 关键抽象

### 2.1 Scheduler Handler

```go
type EventReader interface {
    GetEvent(ctx context.Context, tenantID, eventID string) (store.StoredEvent, error)
}

type Mailbox interface {
    Peek(ctx context.Context, mailboxID string) (string, error)
    AckHead(ctx context.Context, mailboxID, eventID string) error
}

type EventProcessor interface {
    ProcessEvent(ctx context.Context, stored store.StoredEvent) (ProcessResult, error)
}

type Locker interface {
    Acquire(ctx context.Context, mailboxID string) (Lease, error)
    Renew(ctx context.Context, lease Lease) error
    Release(ctx context.Context, lease Lease) error
}
```

Handler 只负责编排 Mailbox、lease 和 Processor，不包含 Alert 状态机。Handler 从 Repository 读取并校验
Mailbox 队首 Event 后把同一 `StoredEvent` 快照交给 Processor；Processor 仅在并发冲突重裁决时重新读取，
避免正常路径重复 realtime GET。

### 2.2 Processor

`lifecycle.Processor` 的依赖是窄能力：

- `store.Repository`：Event/Alert/AlertLog 读写和单对象 CAS；
- `RecentAlertCache`：Elasticsearch 最近 refresh 窗口内 Alert 写入和终态恢复锚点；
- `AlertIDGenerator`：由 opening Event 生成稳定 Alert ID；
- `SeverityTable`：比较 incoming Event 与 active Alert 等级；
- `AlertEnricher`：创建新 Alert 前同步丰富；
- `FinalHook`：Alert 真实变化后的外部输出；
- `Clock`：服务端状态时间；
- `Logger`：记录降级和失败上下文。

Processor 最多进行 3 次 CAS 裁决循环。Version conflict 时必须重新读取 Event/Alert 并重新裁决，不能
拿旧 replacement 盲目覆盖。

### 2.3 Enricher 与 FinalHook

```go
type AlertEnricher interface {
    Enrich(ctx context.Context, input EnrichInput) (EnrichResult, error)
}

type FinalHook interface {
    Execute(ctx context.Context, input FinalHookInput) (FinalHookResult, error)
}
```

Enricher 在每次新 Alert 持久化前同步执行，允许 succeeded/partial/failed。首次创建与等级升级产生的
新 Alert 都执行丰富；同等级推进和终态转换保留已有丰富结果。error、panic、非法状态或
非法 JSON/协议降级为 failed 的固定 payload，同时保留 Alert 创建流程。

Enricher 输入只包含已完成基础构造和 Normalize 的 Alert 深拷贝。具体实现位于
`internal/lifecycle/enrich`，Scope 保存 Alert 深拷贝并向每个 Processor 返回隔离副本；处理器只通过
返回值追加丰富信息。Lifecycle 按 EventSource 路由有序链，单 Processor error/panic 会形成 failed
信封并继续执行，父 Context 取消会立即停止。

`Alert.enrich` 固定只包含 `processors` 顶层 key；每个 processors 元素是以稳定处理器名
为唯一 key 的 envelope，包含 status、value 和可选 diagnostics。`Alert.enrich_status` 是总状态的
唯一持久化字段，领域校验根据 Processor 状态重新聚合并要求一致。

当前 `strategy → resource → display → metric → source` 的 BASE_COLLECT 样例使用固定 mock DataSource
贯通。真实平台历史、鲸眼配置和资源数据源仍待接入与集成验证。

FinalHook 在 Alert 真实变化后按当前任务 Release 的 `EventSource.hooks` 顺序执行。
每个实例接收独立快照，各自产生成功或失败 push AlertLog，实例名参与流水幂等身份和指标。
单个插件 error、独立超时、panic 或非法结果不阻止后续插件；父上下文取消则停止处理。
流水仍与本次状态操作一起提交；失败流水写入成功不回滚已经完成的 Alert 状态。
空列表不输出，同类型允许多个实例；每个来源至多 16 个实例，名称必须唯一。

插件客户端归来源任务所有，发布切换排空旧任务后关闭旧客户端，部分装配失败释放已创建客户端。
Kafka 插件保持 [Kafka Alert V1](../reference/contracts/alert-output.md) 协议及 message ID 不变。
配置方法见[来源 hooks](../guides/configuration.md#eventsource-hooks)。

`active-alert-by-strategy` 独立配置 Redis，执行规则如下：

| Alert 快照 | 操作 |
| --- | --- |
| `active` | `SADD <prefix>:<bk_tenant_id>:<strategy_id> <fingerprint>` |
| `recovered`、`closed` | `SREM <prefix>:<bk_tenant_id>:<strategy_id> <fingerprint>` |
| 缺失或空字符串 `labels.strategy_id` | 跳过，无 push 流水 |
| 布尔或非法 `strategy_id` | hook 失败，不写 Redis |

策略标签只读取 `strategy_id`，不回退 `bk_strategy_id`；字符串原样使用，数字转为稳定十进制文本。
集合不设 TTL；租户强制隔离，来源隔离由前缀负责。同前缀、同租户、同策略、同 fingerprint
共用一个成员，关闭任一告警都会删除，没有引用计数。升级时先执行旧告警的所有关闭 hook，
再执行新告警的所有创建 hook。

独立超时使用 `timeout_milliseconds`，默认 1000ms，遵从父上下文更早的截止时间。
普通 Redis 错误只记录失败，不补偿、不自动重建或回填，也不保护乱序旧快照。
任务使用当前发布版本，不按 Alert 创建版本寻找旧 hook。修改 Redis 目标或前缀不迁移、清理旧集合。

## 3. Signal 处理流程

Lifecycle 进程使用 Redis Streams Session 和通用 `consume.Runtime` 消费 Signal：

```text
Redis Signal
  → 校验 Signal 与 transport metadata
  → Acquire(MailboxID)
  → 循环读取并处理 Mailbox 队首
  → Mailbox 为空
  → Complete / XACK Signal
```

具体顺序：

1. 解码严格 Signal schema，校验 MessageID、TenantID、OrderKey 与 payload 一致；
2. 只尝试一次 lease；锁忙返回 `Defer(lock_retry_delay)`，立即释放 worker；
3. 持锁后启动定时续租，续租失败取消当前处理；
4. `Peek` 队首 Event ID；
5. 读取 StoredEvent，并校验其 EventSource/fingerprint 与 Signal 一致；
6. 调用 `Processor.ProcessEvent`；
7. 只有 Processor 成功后才以 Event ID 校验并 `LPOP` 队首；
8. 重复直到 Mailbox 为空或达到 `max_drain_events`；
9. Mailbox 为空时直接 Complete Signal；
10. 达到排空上限但仍非空时释放 lease 并返回 `Defer(0)`，让 Signal 回到本地公平队列；
11. 最终释放 lease；释放失败只记录警告，不能误删新 owner 的锁。

默认单次最多排空 128 条。ProcessEvent 失败或进程崩溃时队首尚未移除，后续 owner 会重新执行；
Processor 必须幂等。最后一次 `LPOP` 前发生的新入队会由当前 Handler 继续读取；`LPOP` 后的空到非空
入队会创建新的 Stream ID，确认旧 Signal 不会删除新 Signal。处理期间已创建的新 Signal 可能成为冗余
空唤醒，可以安全完成。

## 4. Event 状态裁决

| Event               | active Alert | EventProcessing | Alert 结果                                          |
| ------------------- | ------------ | --------------- | --------------------------------------------------- |
| triggered           | 不存在       | accepted        | 创建 active Alert                                   |
| triggered，同等级   | 存在         | accepted        | 推进 latest_event_id、last_occurred_at、update_at   |
| triggered，更高等级 | 存在         | accepted        | 旧 Alert closed/severity_upgrade，新建 active Alert |
| triggered，更低等级 | 存在         | suppressed      | Alert 不变，追加 suppress AlertLog                  |
| resolved            | 存在         | accepted        | Alert recovered/source                              |
| closed              | 存在         | accepted        | Alert closed/source                                 |
| resolved/closed     | 不存在       | orphaned        | 不创建 Alert                                        |
| 非法状态组合        | 任意         | rejected        | 不修改 Alert                                        |

等级只对 triggered 比较。resolved/closed 不比较等级，否则低等级终结 Event 可能永远无法结束已有
Alert。Alert 的继承字段始终来自 opening Event，后续 Event 的 title/content/dimensions/subject 差异
不会覆盖它。

`last_occurred_at` 取最近被接受 Event 的 occurred_at，乱序 Event 可以使它小于旧值；`update_at` 使用
`max(now, current.update_at + 1ns)` 保持严格单调。

## 5. 各分支副作用顺序

### 5.1 创建

```text
构造稳定 Alert ID
  → Enrich
  → CreateAlert(refresh=false)
  → Recent Alert current SET EX 5
  → FinalHook
  → trigger + push AlertLog Bulk(refresh=false)
  → Event CAS accepted + related_alert_id(refresh=false)
```

### 5.2 同等级推进

```text
Alert CAS(refresh=false)
  → Recent Alert current SET EX 5
  → FinalHook
  → push AlertLog Bulk(refresh=false)
  → Event CAS accepted(refresh=false)
```

### 5.3 来源终结

```text
Alert CAS recovered/closed(refresh=false)
  → Recent Alert current + ended MULTI/EXEC EX 5
  → FinalHook
  → recover/close + push AlertLog Bulk(refresh=false)
  → Event CAS accepted(refresh=false)
```

### 5.4 等级抑制

```text
suppress AlertLog Bulk(refresh=false)
  → Event CAS suppressed(refresh=false)
```

抑制不修改 Alert，不发送 Alert 快照，Event 不写 related_alert_id。

### 5.5 等级升级

```text
旧 Alert CAS closed/severity_upgrade(refresh=false)
  → Recent Alert terminal current + ended MULTI/EXEC EX 5
  → Create 新 Alert(refresh=false)
  → Recent Alert current 覆盖为新 active Alert，ended 保留
  → 旧 Alert FinalHook
  → 新 Alert FinalHook
  → close/push/trigger/push AlertLog Bulk(refresh=false)
  → Event CAS accepted 并关联新 Alert(refresh=false)
```

升级输出旧 Alert closed 和新 Alert active 两条 Kafka 快照。

### 5.6 Elasticsearch 终态归档

Lifecycle 的终态提交点是 Active Alert CAS 成功，不包含物理归档。Active 文档 `_id` 只由
`bk_tenant_id + alert_id` 生成，因此已经终结但尚未归档的旧 Alert 不会占用新 Alert 的文档身份，
等级升级可以在同一次裁决中直接创建新的 active Alert。

Control Plane 的 Alert Archiver 使用稳定游标连续扫描 Active 索引中的终态文档，将每批数据拆给有界 Worker。
Worker 先 Bulk create-only 写入对应 History 时间桶，再只对 History 已确认成功或内容一致的幂等副本按 Active
原 `_seq_no/_primary_term` 执行 Bulk delete。单项失败保留 Active 并在下一轮扫描重试，不阻塞当前轮后续
Alert，也不让 Lifecycle 重试已经成功的终态 CAS。归档过渡期间 Active 与 History 中允许存在内容完全一致的
同一 Alert，逻辑读取将其折叠为一个对象。History create 和 Active delete 都使用 `refresh=false`，
409 幂等冲突使用 realtime `_mget` 核对；搜索可见性由 Elasticsearch refresh 最终提供，不属于归档提交点。

## 6. 幂等与部分成功恢复

上述步骤不是一个事务。Processor 使用下列稳定锚点恢复：

- Event 已是终态：直接返回保存的 Processing 结果；
- active Alert 的 `latest_event_id == event_id`：Alert 已推进，补齐后续输出和 Event CAS；
- active Alert 的 `trigger_event_id == event_id`：Alert 已由该 Event 创建；
- `FindAlertEndedByEvent` 命中 `latest_event_id + end_type=source`：来源终结 CAS 已完成；
- `FindAlertEndedByEvent` 命中 `latest_event_id + end_type=severity_upgrade`：旧 Alert 已关闭，继续创建或读取确定性新 Alert；
- AlertLog 和 Kafka message ID 均由稳定输入生成，重复追加/投递收敛为幂等。

同一次裁决产生的 AlertLog 在一次 Bulk 中提交。只有全部日志项成功后才执行独立的 Event CAS，避免
Bulk 部分成功时 Event 已经终态而日志无法由现有重试路径补齐。Alert 与 Event/AlertLog 位于不同
索引，Bulk 不提供跨文档事务。

Elasticsearch Lifecycle 专用的 Alert create/CAS、AlertLog Bulk 和最终 Event CAS 都使用
`refresh=false`。Alert 写入成功后必须先更新 Redis Recent Alert 缓存，缓存失败时不能继续 FinalHook、
AlertLog 或 Event CAS。Active 索引 `refresh_interval` 由
`storage.elasticsearch.active_alert_refresh_interval_seconds` 配置，默认 5 秒；缓存 TTL 在此基础上
再增加 5 秒安全余量，默认 10 秒。缓存过期后搜索结果应已经可见；CAS
冲突通过物理文档 realtime GET 回读并修复缓存。重复 Mailbox 引用仍通过 Event realtime GET 立即看到
终态；若其他部分成功窗口发生重投，仍允许 FinalHook 重复，下游必须按稳定 Kafka message ID 去重。

Elasticsearch 不使用 Active 文档 `_id` 额外防御跨进程 fingerprint 重复；正常处理路径依赖 Mailbox
lease 串行化同一 fingerprint。物理归档由 Alert Archiver 异步完成，不属于 Event 的处理成功条件。

如果副作用完成但 Event CAS 前崩溃，Event 仍在 Mailbox 队首；重试从上述锚点继续，不重复关闭 Alert
或创建不同身份的流水。

重复 Mailbox 引用在 Event 已终态后不会再次执行领域副作用；这不等价于外部 exactly-once。若
FinalHook 已完成但 Event 终态 CAS 尚未成功，恢复仍可能再次调用 FinalHook，下游继续按稳定
Kafka `message_id` 去重。

## 7. 直接关闭

`CloseAlert` 是内部应用用例，不通过 Event 或 Mailbox：

```go
type CloseAlertCommand struct {
    OperationID  string
    BKTenantID   string
    AlertID      string
    OperatorKind user | system
    OperatorID   string
    Reason       string
    EffectiveAt  time.Time
}
```

它只允许关闭 active Alert，不创建 Event，不修改 latest_event_id。CAS 成功后以 operation ID 作为
cause 执行 FinalHook，再把 close 与 push AlertLog 一次批量写入。相同命令可补齐流水和输出；不同
命令不能改写已有终态。

当前没有 HTTP、CLI 或 Kafka 直接关闭入口。

## 8. 恢复与数据安全边界

- Signal 未 XACK 时，Redis Streams PEL/XAUTOCLAIM 负责重新投递；
- Signal XACK 只清理 PEL 引用，不删除 Stream entry；启用 `control_plane.redis_stream` 后由控制面在
  软长度上限触发时，只裁剪所有 Consumer Group 都已经确认的连续前缀；
- Event 从 Mailbox 移除前崩溃，队首仍可重放；
- lease 锁忙只 Defer，不消耗普通 Retry 次数；
- Cleaner 只有 Event 持久化和 Mailbox 入队都成功后才确认上游 MQ；
- 不再扫描 unprocessed Event 补发 Signal；
- 上游 MQ 已确认后，如果 Redis Mailbox 整体丢失，无法从 offset 精确恢复；
- 部署必须为 Redis 配置满足该风险级别的持久化、复制和备份。

当前没有运行中的 Event Replayer。未来若增加恢复任务，应只扫描超过 Elasticsearch refresh 安全窗口
仍为 `unprocessed` 的 Event，并重新交给 Processor 收敛；AlertLog 不承担当前状态，且 orphaned、
rejected 等结果不一定生成日志，不能单独作为“Event 已处理”的判据。

配置中的 Signal、Mailbox、lease、Retry 和并发预算见[配置指南](../guides/configuration.md)。
