# 设计文档

| 文档                                                             | 定位                                                   |
| ---------------------------------------------------------------- | ------------------------------------------------------ |
| [define.md](define.md)                                           | Event、EventProcessing、Alert、AlertLog 的权威数据模型 |
| [architecture.md](architecture.md)                               | 处理链路、模块边界和可靠性边界                         |
| [deployment.md](deployment.md)                                   | All-in-one 与三进程部署拓扑、职责和演进边界            |
| [core-storage-contract.md](core-storage-contract.md)             | Repository、处理元数据、CAS 与物理资源                 |
| [message-consumption-runtime.md](message-consumption-runtime.md) | MQ 通用消费端口、Cleaner `n → * → n`、确认和背压边界   |
| [observability.md](observability.md)                             | 日志、指标和诊断约束                                   |
| [event-source-dynamic-configuration.md](event-source-dynamic-configuration.md) | 待评审：EventSource 独立管理、同步、版本与 Flow 生效；未实现 |
| [dynamic-configuration.md](dynamic-configuration.md)             | 待评审：Severity 等选定配置项动态化，独立于 EventSource 项目；未实现 |
| [task-scheduling-protocol.md](task-scheduling-protocol.md)         | 待评审：中心调度、停止握手、失联自停、超时强切及发布防抖；跨模块复用 |
| [alarm-callback-enrichment-migration.md](alarm-callback-enrichment-migration.md) | alarm_callback 迁移执行计划 v1，已确认的实施基线，尚未实现 |

EventSource、Cleaner 和 Lifecycle 放在[功能模块](../modules/README.md)，稳定术语与外部协议放在[参考资料](../reference/README.md)，
使用和部署步骤放在[指南](../guides/README.md)。历史调研和审查快照不能覆盖 `define.md`、代码和测试
表达的现行契约。
