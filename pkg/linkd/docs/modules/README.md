# 功能模块

核心数据模型见 [`docs/design/define.md`](../design/define.md)。本目录只保留理解系统主流程所需的
EventSource、Cleaner 和 Lifecycle，不再为 Event、Alert、Enricher 或配置维护重复的薄页面。

| 模块        | 文档                               | 当前职责                                                       |
| ----------- | ---------------------------------- | -------------------------------------------------------------- |
| EventSource | [event-source.md](event-source.md) | 来源身份、租户、fingerprint、Severity、MQ subscription 和 Flow |
| Cleaner     | [cleaner.md](cleaner.md)           | `RawEventMessage -> SourceCleaner -> EventDraft -> EventFactory -> Event` |
| Lifecycle   | [lifecycle.md](lifecycle.md)       | Mailbox 调度、Alert 状态裁决、AlertLog、Enricher 和 FinalHook  |

配置见[配置指南](../guides/configuration.md)，Event/Alert/AlertLog 字段不在模块页重复定义。
