# Cleaner

Cleaner 负责把一条 EventSource 的 MQ 原始消息确定性转换为 Event，并在确认原消息前完成 Event
持久化和 Lifecycle Mailbox 入队。它不查询或修改 Alert，也不执行生命周期状态裁决。

## 1. 输入、输出和边界

```text
MQ Delivery
  → RawEventMessage
  → SourceCleaner
  → EventDraft
  → EventFactory
  → domain.Event 或 Discard
```

输入信封包含稳定 `record_id`、租户、`event_source_id`、`received_at`、headers 和原始 payload。
payload 不能覆盖信封来源、租户、接收时间或消息身份。EventSource 的详细边界见
[EventSource](event-source.md)，当前 `standard` payload 见[输入契约](../reference/contracts/raw-event.md)。

Cleaner 的成功终态有三种：

- 新建或幂等命中 `unprocessed` Event，并成功加入对应 Redis Mailbox；
- Repository 返回的幂等 Event 已是终态，跳过 Mailbox；
- 输入属于确定性无效数据，已得到允许确认的 Discard 结果。

处理超时、存储错误、Mailbox 错误和确认错误都不是 Discard。

## 2. 关键抽象

### 2.1 SourceCleaner 与 EventFactory

```go
type SourceCleaner interface {
    Clean(ctx context.Context, message RawEventMessage) (EventDraft, error)
}
```

SourceCleaner 只解释来源 payload，不能访问 Repository、Mailbox 或 MQ Session。当前只注册
`standard`：接受单个 JSON object，把已知字段投影为 EventDraft；未知字段只在完整原始快照中保留，
重复 key、尾随 JSON、非法 action 和已知字段类型错误会被拒绝。

EventDraft 不包含租户、来源、Event ID、fingerprint、标准 severity、接收时间和原始 payload，具体
SourceCleaner 因而不能覆盖这些字段。EventFactory 统一处理：

- EventSource 租户覆盖；
- 通过 SeverityResolver 执行 mapping、全局同名和 default 规则；
- occurred_at、produced_at 的稳定时间回退；
- 确定性 Event ID；
- 通过 FingerprintResolver 执行 EventSource fingerprint 规则；
- 完整 source_raw_data 快照；
- 动态对象深拷贝、UTC 规范化和完整 Event 校验。

SeverityResolver 和 FingerprintResolver 是可替换的配置驱动算法边界；默认实现与 Cleaner type 无关。
Event ID 和 source_raw_data 仍由 EventFactory 固定生成，避免具体 Cleaner 改写核心身份和来源事实。

### 2.2 Processor

```go
type Processor interface {
    Process(ctx context.Context, message consume.Message) (ProcessResult, error)
}
```

Processor 是 worker pool 执行的纯计算边界，只返回 Event 或 Discard。它不得写存储、写 Mailbox、确认
消息或在返回后继续产生副作用。当前 `MapperProcessor` 把确定性解析/校验错误转为 Discard，把 Context
取消和超时保留为可重试错误。

### 2.3 副作用端口

```go
type EventBatchWriter interface {
    CreateEvents(ctx context.Context, events []domain.Event) ([]store.CreateEventItemResult, error)
}

type MailboxWriter interface {
    EnqueueBatch(ctx context.Context, events []domain.Event) ([]MailboxEnqueueResult, error)
}
```

两个端口都要求输入输出位置一一对应。Event 创建区分请求级失败与逐项创建、幂等、冲突和暂时失败；
Mailbox 逐项追加 Event ID，允许少量重复引用。Cleaner Runtime 不依赖 MySQL、Elasticsearch 或 Redis
客户端类型。

### 2.4 Runtime 与 Session

`cleaner.Runtime` 只依赖 `consume.Session`、Processor 和两个副作用端口。Session 提供 Receive、Confirm、
Close 及可选 lane Pause/Resume、ownership 事件。Kafka 是当前 FlowFactory 装配的首个适配器，不是
Cleaner 核心依赖。

## 3. `n → * → n` 处理流程

每个 enabled EventSource 拥有一条独立 Flow。假设其 consumer group 当前拥有 n 个 lane：

```text
n 个 lane Receive
  → * 个 worker 并发纯清洗
  → n 个 lane 各自恢复连续顺序
  → lane 内 Event batch
  → lane 内 Mailbox enqueue
  → lane 内 Confirm
```

详细步骤：

1. Runtime 有界 Receive，并按 Delivery 到达顺序追加到各 lane 队列；
2. 消息立即进入共享 worker pool，不使用 fingerprint 或 `OrderKey` 串行纯计算；
3. worker 可以乱序返回 Event、Discard 或暂时错误；
4. 每个 lane 只从队首选择已经连续完成的前缀；
5. 数量、原始消息字节或最长等待时间任一达到阈值时启动该 lane 的批次；
6. 调用 `CreateEvents`，保留逐项结果；
7. 从队首开始，把已 Stored 或 Discard 的连续位置推进；Repository 返回终态的重投直接跳过 Mailbox；
8. 只有 Stored → MailboxEnqueued、StoredTerminal 或 Discard 的连续 Receipt 前缀可以 Confirm；
9. Confirm 成功后释放对应 inflight 配额并继续该 lane。

不同 lane 不合并 Event 批次或 Mailbox 写入。全局只用 `max_concurrent_batches` 限制同时执行副作用的
lane 数量，因此一个慢 lane 不会阻塞其他 lane。

## 4. 连续前缀与部分成功

```text
lane:       100   101   102   103
worker:     done  slow  done  done
可副作用:  100
```

- Discard 是可跨越的确定性完成位置；
- Processor Retry、Event 暂时失败或 Mailbox 暂时失败形成缺口；
- 后续 worker 即使完成，也只能留在 reorder buffer；
- Bulk 中 100 成功、101 暂时失败、102 成功时，可以先 Mailbox/Confirm 100；102 不得越过 101；
- Event 已写入但 Mailbox 失败时，重试通过稳定 Event ID 收敛为幂等创建；
- Mailbox 已写入但 Confirm 失败时，重投可能再次追加同一 Event ID；Lifecycle 在第一次处理写入 Event
  终态后短路后续引用。

累计确认适配器由 Session 再次验证 Receipt 是 lane 连续前缀；Cleaner Runtime 与 Kafka adapter 都不会
根据 offset 数值猜测不存在的消息位置。

## 5. 背压与资源预算

每条 Flow 同时受以下预算约束：

- worker 数；
- 单批消息数和字节数；
- 全局 inflight 消息数和字节数；
- 单 lane 高水位与恢复低水位；
- 同时执行的 lane 批次数；
- 单次处理超时、Retry 次数/累计时间；
- shutdown drain 时间。

支持 `LaneController` 的 Session 在达到高水位时只暂停该 lane，降到低水位后恢复。不支持时停止整个
Receive，直到容量下降。适配器返回超过本次 ReceiveLimits 的数据会使 Runtime 失败并关闭 Session，
防止突破内存硬上限。

所有 Cleaner Flow 还共享一个 Lifecycle Signal 背压检查器。它最多每 3 秒执行一次 `XINFO GROUPS`，
以目标 Group 的 `lag + pending` 作为近似未完成 Signal 数：达到 100000 暂停整个 Flow 的 Kafka fetch，
降到 80000 恢复，中间区间保持原状态。Kafka topic 暂停期间仍每秒 Poll 一次以维持 consumer group；
已有 inflight 继续完成。查询失败、超时或 lag 未知时 fail-open，明确缺少目标 Group 时暂停等待 Lifecycle
创建。该数值不是精确 Event 数，允许采样超调和冗余 Signal 高估。

默认值和 EventSource 局部覆盖见[配置指南](../guides/configuration.md)。默认值只用于功能验证，生产
预算必须通过真实载荷压测确定。

## 6. Kafka ownership

Kafka Session 使用 `BlockRebalanceOnPoll`。Runtime 接管 Delivery 后允许 rebalance，并处理：

- assigned：建立 lane 所有权；
- revoked：停止对应 lane 的新工作，在 drain 预算内完成并提交仍安全的连续前缀，然后释放所有权；
- lost：立即 fencing，旧结果不能再提交；
- close：允许 rebalance，剩余未确认消息由新 owner 重投。

Kafka 支持多个有界 poll 结果同时在途和 partition Pause/Resume。确认时按 partition 校验 Receipt 前缀，
commit 失败保留 Receipt 重试，不清除已经完成的业务副作用。

## 7. 故障恢复

| 故障位置                 | 恢复行为                                      |
| ------------------------ | --------------------------------------------- |
| Event 写入前             | 原消息未确认，由 MQ 重投                      |
| Event 成功、Mailbox 前   | MQ 重投，Event create 收敛为幂等              |
| Mailbox 成功、Confirm 前 | MQ 可能重投并产生重复引用，由 Event 终态收敛  |
| Confirm 成功后           | MQ 不再负责恢复，Redis Mailbox 必须可靠保存   |
| shutdown/revoked 超时    | 丢弃剩余内存状态，由 MQ 重投未确认消息        |
| lost ownership           | 不提交旧 Receipt，由新 owner 从已提交位置恢复 |

Cleaner 不扫描 unprocessed Event 补发 Mailbox。确认后 Redis 整体丢失无法从 Kafka offset 精确恢复，
这是部署必须通过 Redis 持久化、复制和备份承担的数据安全边界。

## 8. 当前实现范围

- 已实现 `standard`、Kafka FlowFactory、MySQL/Elasticsearch Event batch、Redis Mailbox 和单元/race 测试；
- MySQL batch 当前逐项幂等写入，没有多行 SQL 优化；
- Elasticsearch 使用 `_bulk?refresh=false` 的逐项 create；成功表示主分片已确认写入，不保证 `_search`
  立即可见，重复身份通过 realtime GET 核对；
- RabbitMQ 和 Redis Streams 有通用 Session，但尚未进入 EventSource Cleaner 装配；
- RocketMQ、声明式 Cleaner 脚本和生产容量压测尚未实现。
