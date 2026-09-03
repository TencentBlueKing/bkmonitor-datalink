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
