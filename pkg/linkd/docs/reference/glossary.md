# Linkd 术语

权威字段与状态矩阵见 [`define.md`](../design/define.md)。

| 术语            | 定义                                                                                              |
| --------------- | ------------------------------------------------------------------------------------------------- |
| RawEventMessage | MQ 无关的接入信封，保存稳定 record ID、租户、来源、接收时间和原始 payload                         |
| lane            | 消息队列的确认与所有权分片；Kafka 中对应 topic partition，不表达 fingerprint 业务串行范围         |
| Mailbox         | 按租户、来源和 fingerprint 标识的 Redis 待处理 Event ID 队列                                      |
| Signal          | 只表示某个 Mailbox 需要处理的 Redis Stream 唤醒消息，不绑定单个 Event                             |
| SourceCleaner   | 由 EventSource 选择的来源解析器，只把 payload 中的来源事实解析为 EventDraft                       |
| EventDraft      | Cleaner 提取的来源事实，不含 EventFactory 独占的身份、标准化结果和原始快照                         |
| Event           | 标准化后的不可变来源事实；生命周期只允许补写 `related_alert_id`                                   |
| EventProcessing | 独立于 Event JSON 的技术元数据，包含 state、outcome、reason 和 processed_at                       |
| EventSource     | 配置中的事件源定义，包含租户覆盖、Cleaner、fingerprint、Severity 和 MQ subscription               |
| fingerprint     | EventSource 按稳定 Event 字段生成的 Alert 关联键；Lifecycle 唯一关联条件为租户、来源、fingerprint |
| Severity        | 全局有序等级表；priority 越小越严重，Event/Alert 只保存 name                                      |
| Alert           | 从首个 triggered Event 创建的一次异常生命周期；继承字段创建后永久锁定                             |
| active          | Alert 当前仍成立                                                                                  |
| recovered       | 来源 resolved Event 使 Alert 进入的终态                                                           |
| closed          | 来源关闭、直接关闭或等级升级使 Alert 进入的终态                                                   |
| AlertLog        | 独立、确定性标识的不可变流水，记录状态操作、抑制和最终输出结果                                    |
| VersionToken    | Repository 专属 CAS 令牌，不进入领域 JSON 或外部消息                                              |
| accepted        | Event 已被生命周期接受并关联 Alert                                                                |
| suppressed      | 低等级 triggered Event 被 active 高等级 Alert 抑制，Event 不关联 Alert                            |
| orphaned        | resolved/closed 未找到 active Alert，Event 不关联 Alert                                           |
| rejected        | Event 在清洗或领域校验阶段被确定性拒绝                                                            |
| cause           | FinalHook 变更原因，包含 source event、user operation 或 system operation 的类型和稳定 ID         |

## 动态配置候选术语

以下术语分别用于[EventSource 动态配置](../design/event-source-dynamic-configuration.md)和
[部分配置动态化](../design/dynamic-configuration.md)，尚未成为实现或外部契约。两项独立管理版本与发布，
不定义跨来源与全局配置的统一 ConfigRelease。

| 术语 | 候选定义 |
| --- | --- |
| EventSourceRecord | 带管理作用域、资源版本与管理者的来源定义，仅属于 EventSource 项目 |
| EventSourceRelease | 单来源一次发布的完整不可变配置快照，不包含全局配置；与 Record 兼容 ES/MySQL 单对象操作 |
| SeverityPolicy | 部分配置动态化中的全局等级定义与默认值，独立于来源版本管理 |
| SourceActivation | 来源订阅在各 partition offset 区间使用的规则版本及切换代次 |
| desired / applied revision | 期望发布版本与运行时实际应用版本，保存成功不等于生效成功 |

## 中心调度候选术语

以下术语用于[中心化任务调度协议](../design/task-scheduling-protocol.md)，尚未实现。

| 术语 | 候选定义 |
| --- | --- |
| TaskKey | 带 deployment、管理/租户作用域、模块与稳定资源/分片身份的互斥执行单元 |
| assignment_epoch | 同一 TaskKey 的执行代次，每次重新分配递增，不等于来源发布版本 |
| session_id | 本次 worker 进程启动的会话身份，重启不可复用 |
| StoppedConfirmed | 中心原子确认旧任务已停止，允许后续分配 |
| ForcedStopped | 授权到期及安全余量后完成必要隔离的强切决议，不伪造为 worker 的停止报告 |
| FastRestart | 满足模块停止契约后快速完成停止、确认和新分配，不跳过互斥交接 |
