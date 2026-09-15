# 消息消费与 Cleaner 运行时

本文描述当前已经实现的消息消费边界。通用消费运行时位于 `internal/consume`，Cleaner 专用
`n → * → n` 运行时位于 `internal/cleaner`。两者都只依赖消息队列端口，不把 Kafka offset、
Redis Stream ID 或 RabbitMQ delivery tag 暴露给业务处理器。

## 1. 可靠性边界

Linkd 不建设通用持久化 Inbox、Retry Store 或统一 DLQ。进程在确认消息前崩溃时，由上游 MQ
重新投递；业务副作用必须使用稳定身份幂等。

```text
MQ Session
  → 有界 Receive
  → 业务处理
  → 业务结果可靠落地
  → Session.Confirm
```

必须始终满足：

- `Message.ID` 在同一业务事实重投时保持稳定；
- `Receipt` 只在创建它的 Session 内有效，不持久化、不记录到日志；
- 确认不得越过尚未完成的累计确认位置；
- Session 关闭或 lane 所有权丢失后，不再使用旧 Receipt 确认；
- 队列、批次、在途消息、载荷字节、重试和关闭等待都有硬上限；
- 已确认消息不再由上游 MQ 恢复，因此确认前必须完成所有不可丢失副作用。

该模型提供 at-least-once，不提供 MQ 与业务存储之间的 exactly-once 或原子事务。

## 2. 核心端口

通用 Session 端口为：

```go
type Session interface {
    Capabilities() Capabilities
    Receive(ctx context.Context, limits ReceiveLimits) ([]Delivery, error)
    Confirm(ctx context.Context, receipts []Receipt) error
    Close(ctx context.Context) error
}
```

`Delivery` 包含 MQ 无关的 `Message`、不透明 `Receipt` 和只服务于调度的 `DeliveryMeta`。其中：

- `DeliveryMeta.Lane` 表达确认与所有权分片；Kafka 中是 topic partition；
- `DeliveryMeta.Position` 只用于诊断，不参与业务身份；
- `Message.OrderKey` 是通用 Handler 的可选业务串行键；
- Cleaner 不使用 `OrderKey`，只在副作用阶段按 lane 恢复传输连续顺序。

可选能力由窄接口表达：

- `LanePauser.Pause`：通用 Runtime 在 lane 被阻塞时暂停后续拉取；
- `LaneController.Pause/Resume`：Cleaner 按高低水位暂停和恢复单个 lane；
- `FlowController.PauseFlow/ResumeFlow`：Cleaner 因 Lifecycle Signal 积压暂停整个 Kafka topic fetch；
- `OwnershipSession.OwnershipEvents/AllowOwnershipChanges`：处理 assigned、revoked、lost；
- `RuntimeValidator.ValidateRuntime`：适配器校验 Broker 租约和运行时预算。

没有实现 `LaneController` 的 Session 在单 lane 达到上限时退化为停止整个 Receive，不伪造分片级流控。

## 3. 通用消费运行时

`consume.Runtime` 面向单条 `Handler`，负责有界拉取、worker pool、`OrderKey` 串行、进程内重试、
连续确认水位和优雅退出。

Handler 只返回以下结构化结果：

| Outcome    | 是否确认 | 行为                                          |
| ---------- | -------: | --------------------------------------------- |
| `Complete` |       是 | 业务结果已可靠落地                            |
| `Discard`  |       是 | 确定性错误已形成允许跳过的终态                |
| `Retry`    |       否 | 进入有次数和时间上限的内存退避队列            |
| `Defer`    |       否 | 释放 worker 后重新排队，不消耗普通 Retry 预算 |
| `Block`    |       否 | 暂停可暂停 lane，并使当前 Runtime 失败退出    |

`Defer` 仍保留 Receipt 和 `OrderKey` 占用。Lifecycle 在 lease 锁忙或主动让出 Mailbox 时使用它，
避免锁等待长期占用 worker。

确认语义由 `Capabilities.Settlement` 决定：

- `SettlementIndividual`：每个 Receipt 可以独立确认；
- `SettlementCumulative`：只能提交同一 lane 已完成的连续队首前缀。

确认失败不会回滚已经完成的业务副作用；Runtime 保留 Receipt 并重试确认。确认结果不确定时允许
MQ 后续重投，业务幂等负责收敛。

## 4. Cleaner `n → * → n`

每个启用的 EventSource 拥有独立 Flow。Cleaner 核心流程为：

```text
n 个 MQ lane
  → 共享 worker pool 并发执行纯 Processor
  → 每个 lane 恢复 Receive 顺序并选择连续完成前缀
  → 每个 lane 独立 CreateEvents
  → 每个 lane 独立写入 Redis Mailbox
  → 每个 lane 确认连续 Receipt 前缀
```

`Processor` 只执行 `RawEventMessage → Event/Discard`，不得写存储、写 Mailbox 或确认消息。worker
可以乱序完成，但副作用只能从 lane 队首连续推进：

- `Discard` 是可跨越的确定性完成位置；
- Processor 或存储暂时失败形成缺口，只阻塞所属 lane；
- 数量、原始消息字节或最长等待时间任一达到阈值即可触发批次；
- 每个 lane 同时最多一个副作用批次；
- 不同 lane 不合并 Event 持久化或 Mailbox 写入，全局只限制同时执行的 lane 批次数；
- Bulk 部分成功时先推进已经连续成功的前缀，更后的成功项不得越过中间失败项进入 Mailbox；
- 只有 `Stored → MailboxEnqueued` 或 `Discard` 的连续前缀可以确认。

达到 lane 高水位时，支持 `LaneController` 的适配器暂停该 lane；降到低水位后恢复。全局消息数、
全局字节数和同时持久化批次数继续限制总资源占用。

Lifecycle Signal 的近似全局背压在下一次 Receive 前检查。Kafka 实现通过 `PauseFetchTopics` 停止接管
新数据，但继续执行有界 Poll 维持 consumer group 和 rebalance 协议；恢复低水位后再恢复 fetch。

同一 fingerprint 应由生产者使用稳定 key 投递到同一 MQ 分片。Cleaner 不用 fingerprint 串行纯计算；
如果同一 fingerprint 跨 lane，只承诺 Redis Mailbox 实际成功入队的顺序。

## 5. Kafka 所有权和确认

Kafka 是当前 Cleaner FlowFactory 装配的首个适配器，但不是 Cleaner 核心依赖。适配器当前实现：

- `BlockRebalanceOnPoll`；
- 多个有界 poll 结果同时在途；
- partition 级 `PauseFetchPartitions/ResumeFetchPartitions`；
- 每 partition 校验 Receipt 必须是连续前缀，再提交其中最高 record 的下一 offset；
- commit 失败时保留 Receipt，允许重试；
- assigned 后建立 lane 所有权；
- revoked 时由 Cleaner 限时排空安全前缀，完成后释放所有权；
- lost 时立即 fencing，旧结果不得再提交；
- Close 前允许 rebalance，未确认记录交给后续 owner 重放。

Kafka header 默认使用 `message_id`、`bk_tenant_id` 和 `order_key`。Cleaner 不依赖 `order_key`；
缺少业务 `message_id` 时适配器退回 `topic/partition/offset`，这只能保证当前 Kafka record 重投稳定，
不能识别生产者重新发布的同一业务事实。

## 6. 其他已实现适配器

| 适配器        | 确认模式        | 恢复来源                     | 当前约束                                                  |
| ------------- | --------------- | ---------------------------- | --------------------------------------------------------- |
| Kafka         | lane 累计确认   | committed offset 后重投      | Cleaner 当前实际装配                                      |
| Redis Streams | 逐条 `XACK`     | PEL + `XAUTOCLAIM`           | `claim_min_idle` 必须大于处理与重试总预算；Lifecycle 使用 |
| RabbitMQ      | 单条 manual ACK | Channel 关闭后重投未确认消息 | delivery tag 只在原 Channel 有效；使用 prefetch           |

Redis Streams 和 RabbitMQ 已实现通用 Session 及无 Broker 单元测试，但没有通过 EventSource 配置装配
为 Cleaner 来源。RocketMQ 适配器尚未实现。

## 7. 关闭与故障窗口

正常关闭顺序为：停止 Receive、在关闭预算内处理安全前缀、确认可证明完成的 Receipt、取消剩余
内存任务、关闭 Session。超时后不会等待无限时间，未确认消息由 MQ 恢复。

| 故障位置                          | 结果                                               |
| --------------------------------- | -------------------------------------------------- |
| Processor 或业务写入前崩溃        | 原消息未确认，由 MQ 重投                           |
| Event 已写入、Mailbox 入队前崩溃  | MQ 重投，稳定 Event ID 收敛重复创建                |
| Mailbox 已入队、确认前崩溃        | MQ 可能重投并追加重复引用，由 Event 终态收敛       |
| 确认成功后 Redis Mailbox 整体丢失 | 无法从 MQ offset 精确恢复，依赖 Redis 持久化与复制 |
| Kafka revoked                     | 限时提交仍拥有 partition 的安全连续前缀            |
| Kafka lost                        | 不提交旧结果，等待新 owner 重投                    |

## 8. 配置与验证边界

Cleaner 默认预算和 EventSource 局部覆盖见[配置指南](../guides/configuration.md)。代码默认值用于功能
验证，不代表已经达到生产吞吐目标。

当前单元和 race 测试覆盖并发乱序完成、lane 缺口、Bulk 部分成功、三种 flush 条件、多 poll 在途、
partition 暂停/恢复、连续 commit、commit 失败以及 revoked/lost fencing。普通 `make check` 不连接
真实 Broker；外部服务 E2E 只有显式设置 `LINKD_E2E=1` 才执行。尚未完成生产容量压测，不能仅因
Elasticsearch 使用 `_bulk` 就宣称达到生产吞吐目标。
