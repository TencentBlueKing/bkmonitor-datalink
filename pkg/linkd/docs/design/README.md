# 设计文档

自定义丰富开发入口：[CMDB/常规规则、补丁、SDK 与预览](custom-enrichment.md)。

| 文档                                                             | 定位                                                   |
| ---------------------------------------------------------------- | ------------------------------------------------------ |
| [package-structure.md](package-structure.md) | 当前 Go 包职责、依赖方向和装配边界 |
| [define.md](define.md)                                           | Event、EventProcessing、Alert、AlertLog 的权威数据模型 |
| [kac-alarm-hook.md](kac-alarm-hook.md)                           | KAC Alarm 兼容 Hook、字段映射与可靠性边界           |
| [architecture.md](architecture.md)                               | 处理链路、模块边界和可靠性边界                         |
| [deployment.md](deployment.md)                                   | All-in-one 与三进程部署拓扑、职责和演进边界            |
| [core-storage-contract.md](core-storage-contract.md)             | Repository、处理元数据、CAS 与物理资源                 |
| [message-consumption-runtime.md](message-consumption-runtime.md) | MQ 通用消费端口、Cleaner `n → * → n`、确认和背压边界   |
| [task-scheduling-observability.md](task-scheduling-observability.md) | 中心与 worker 调度指标、语义和验证入口 |
| [observability.md](observability.md)                             | 日志、指标和诊断约束                                   |
| [enrich.md](enrich.md) | 内置丰富场景、Observation 和 KAC 迁移状态 |
| [custom-enrichment.md](custom-enrichment.md) | 自定义 CMDB/字段规则、补丁、SDK、预览和转换 |
| [event-source-dynamic-configuration.md](event-source-dynamic-configuration.md) | EventSource 管理、发布、多副本与分片上限 |
| [active-alert-index.md](active-alert-index.md) | 控制面统一维护策略缓存、原子发布与故障恢复 |
| [dynamic-configuration.md](dynamic-configuration.md)             | Severity 来源同步、持久化恢复与在线应用，默认关闭 |
| [task-scheduling-protocol.md](task-scheduling-protocol.md)         | 中心调度、停止握手、有界自停和多副本；跨模块复用 |

EventSource、Cleaner 和 Lifecycle 放在[功能模块](../modules/README.md)，稳定术语与外部协议放在[参考资料](../reference/README.md)，
使用和部署步骤放在[指南](../guides/README.md)。历史调研和审查快照不能覆盖 `define.md`、代码和测试
表达的现行契约。
