# Linkd 总体设计

本设计以 [`define.md`](define.md) 为领域最终事实。早期工作树和讨论稿不是可兼容版本，不增加别名、
迁移或双写路径；需要版本化的输入、输出协议在各自契约中独立定义。

## 处理链路

```text
MQ RawEventMessage
  -> 按 EventSource 选择 SourceCleaner
  -> worker pool 并发生成 Event 与稳定 event_id/fingerprint
  -> 各 lane 独立恢复连续顺序并批量创建 Event(unprocessed)
  -> 各 lane 写入 Redis Mailbox 后确认上游消息
  -> Mailbox Signal + Redis fingerprint lease
  -> Lifecycle Processor
     -> accepted: 创建或推进 Alert，并回写 related_alert_id
     -> suppressed: Alert 不变，写确定性抑制流水并关联 related_alert_id
     -> orphaned: 不创建 Alert
  -> FinalHook 输出 Kafka Alert V1 快照
```

## 模块边界

- `internal/config` 严格读取 YAML，启动时冻结全局 Severity 表和 EventSource 定义；未知 Cleaner 类型直接失败。
- `internal/cleaner` 的 Processor 通过具体 SourceCleaner 解析来源事实，再由 EventFactory 补齐受控字段并构造 `domain.Event`；专用 Runtime 负责消息队列无关的并发、lane 内连续批量副作用和确认。内置 `standard` 接收 JSON object；租户、来源、稳定 record ID 和接收时间来自信封，完整 payload 写入 `source_raw_data`。
- `internal/store` 保存 Event、独立处理元数据、Alert 和 AlertLog。Elasticsearch 数据进程只访问控制面
  存储管理任务准备好的 alias；Lifecycle 只提交 Alert 逻辑终态，Active 到 History 的搬迁由控制面异步完成。
- `internal/controlplane/process` 装配并监督控制面管理任务；当前分别运行 Elasticsearch Schema 与 Active 资源对账、
  时间桶维护、终态 Alert 归档，以及 Redis Signal Stream 状态采集和安全裁剪，后续任务继续加入该进程，
  不增加独立常驻 command。
- `internal/lifecycle` 只按 `(bk_tenant_id, event_source_id, fingerprint)` 关联 active Alert，执行等级比较、状态流转、同步 Enricher、CAS 重试和部分成功恢复。
- `internal/lifecycle/kafkahook` 根据 Alert change/cause 输出 V1 完整快照，不要求每次变更都存在 Event。
- `devtools` 只读查询当前资源，不提供任意查询或写入能力。

## 身份与租户

- 所有数据、查询、锁、信号和缓存键都带租户作用域。
- EventID/AlertID 使用 `<YYYYMMDDHHMMSS>.<tenant>.<event_source_id>.<16 hex>`；时间固定按 UTC 秒解析，
  摘要使用不同 domain seed 的 SHA-256 前 64 bit。Event 摘要包含完整 received_at 和稳定来源身份，Alert
  摘要包含 opening Event，因此重投不变。
- fingerprint 只由 EventSource 配置的稳定 Event 字段生成；`source_alert_id` 只能作为 fingerprint 输入，不能形成查询旁路。
- `related_tenant_id` 非空时强制覆盖消息信封中的租户。

## 一致性与恢复

Repository 不提供 Event、Alert、AlertLog 与 Kafka 的跨对象事务。生命周期通过以下手段收敛部分成功：

- Event 处理结果和 `related_alert_id` 使用同一次 CAS 更新；
- Alert 使用存储专属 `VersionToken` 有界重试；
- fingerprint Redis lease 降低同一问题的并发竞争；
- Redis Mailbox 用单一 List 保存待处理 Event ID，空到非空时原子写入唤醒 Signal；
- Event 去重由 Repository 负责；Mailbox 允许少量重复引用，终态 Event 在 Lifecycle 中直接短路；
- Elasticsearch Lifecycle 使用共享 Redis Recent Alert 缓存保存最近 refresh 窗口内的 Alert 写入；Event 裁决先查
  缓存，写 Alert 后先更新缓存，借此取消数据路径上的 search refresh 等待；
- Cleaner 以目标 Signal Group 的 `lag + pending` 做 3 秒缓存的近似全局背压；
- 使用 `latest_event_id + end_type` 恢复来源终结或等级升级；
- AlertLog、直接关闭 operation 和 Kafka message ID 均使用稳定输入生成确定性身份。

上游消息只能在 Event 已持久化且 Mailbox 入队成功后确认。确认前由 MQ 重投恢复；确认后 Redis
Mailbox 数据安全依赖 Redis 自身持久化和复制。并发、批次、重试和关闭等待必须有硬上限并传播
`context.Context`。

## 当前实现边界

当前已实现领域对象、配置、`standard` Cleaner、内存/MySQL/Elasticsearch Repository、Redis
Mailbox、生命周期、同步 Enricher、内部 `CloseAlert`、Kafka V1 FinalHook、rawgen、DevTools 和双
Repository E2E 测试入口。真实外部服务 E2E 需要显式启用；HTTP/CLI/Kafka 直接关闭入口和声明式
Cleaner 脚本引擎不在当前范围。

后续交付只提供两种正式部署模式：用于测试和小规模部署的 `all-in-one`，以及可独立扩缩容的 Cleaner、
Lifecycle、控制面（API / Leader / Manager）三进程模式。常驻进程入口对应 `linkd run cleaner`、
`linkd run lifecycle` 和 `linkd run control-plane`；职责、故障域和当前完成度见[部署模式与进程拓扑](deployment.md)。

## 详细设计入口

- [核心数据模型](define.md)：Event、EventProcessing、Alert、AlertLog；
- [EventSource](../modules/event-source.md)：来源配置与 Flow 装配；
- [Cleaner](../modules/cleaner.md)：原始消息到 Event、批量副作用和 ACK；
- [Lifecycle](../modules/lifecycle.md)：Mailbox、状态裁决、恢复和输出；
- [消息消费运行时](message-consumption-runtime.md)：MQ 通用 Session、Receipt、Outcome 和背压；
- [核心存储契约](core-storage-contract.md)：Repository、批量创建和 CAS。
