# 设计文档

自定义丰富开发入口：[CMDB/常规规则、补丁、SDK 与预览](custom-enrichment.md)。

图解入口：[整体架构图](architecture.md#整体架构) · [事件处理流程图](architecture.md#处理链路)。

实施与验收记录：[Event 丰富与抑制、屏蔽、合并开发方案](event-enrich-and-alarm-policies.md)。该文档包含
业务决策和阶段证据，带日期的历史记录不覆盖下列文档对当前代码的说明。

| 文档                                                             | 定位                                                   |
| ---------------------------------------------------------------- | ------------------------------------------------------ |
| [policy-runtime-scaling.md](policy-runtime-scaling.md) | 候选索引、共享依赖与 60 秒时效、屏蔽主索引、合并批量运行和验证记录；通知暂缓 |
| [runtime-capacity-and-recovery.md](runtime-capacity-and-recovery.md) | 并发、容量、失败确认与重试审查清单及配置边界 |
| [package-structure.md](package-structure.md) | 当前 Go 包职责、依赖方向和装配边界 |
| [define.md](define.md)                                           | Event、EventProcessing、Alert、AlertLog 的权威数据模型 |
| [kac-alarm-hook.md](kac-alarm-hook.md)                           | KAC Alarm 兼容 Hook、字段映射与可靠性边界           |
| [kac-compatibility-plugin.md](kac-compatibility-plugin.md) | 全局 KAC 插件、原索引直接维护、字段所有权与验收边界 |
| [kac-alarm-source-debug-preview.md](kac-alarm-source-debug-preview.md) | 首版已实现并完成本地 HTTP/ES/原调试组件联调：verification 样本复用、来源规则及不执行丰富的同步预览；全主站验收待验证 |
| [architecture.md](architecture.md)                               | 整体架构图、事件处理流程图、模块与可靠性边界             |
| [deployment.md](deployment.md)                                   | All-in-one 与三进程部署拓扑、职责和演进边界            |
| [core-storage-contract.md](core-storage-contract.md)             | Repository、处理元数据、CAS 与物理资源                 |
| [message-consumption-runtime.md](message-consumption-runtime.md) | MQ 通用消费端口、Cleaner `n → * → n`、确认和背压边界   |
| [task-scheduling-observability.md](task-scheduling-observability.md) | 中心与 worker 调度指标、语义和验证入口 |
| [observability.md](observability.md)                             | 日志、指标和诊断约束                                   |
| [enrich.md](enrich.md) | 内置丰富场景、Observation 和 KAC 迁移状态 |
| [alert-content-generation.md](alert-content-generation.md) | 新 Alert 内容生成迁移的实施草案、数据前置条件和验收范围 |
| [custom-enrichment.md](custom-enrichment.md) | 自定义 CMDB/字段规则、补丁、SDK、预览和转换 |
| [event-source-dynamic-configuration.md](event-source-dynamic-configuration.md) | EventSource 管理、发布、多副本与分片上限 |
| [active-alert-index.md](active-alert-index.md) | 主流程增量维护活跃缓存、Cleaner 软限流与控制面差异修复 |
| [dynamic-configuration.md](dynamic-configuration.md)             | Severity 来源同步、持久化恢复与在线应用，默认关闭 |
| [task-scheduling-protocol.md](task-scheduling-protocol.md)         | 中心调度、停止握手、有界自停和多副本；跨模块复用 |

EventSource、Cleaner 和 Lifecycle 放在[功能模块](../modules/README.md)，稳定术语与外部协议放在[参考资料](../reference/README.md)，
使用和部署步骤放在[指南](../guides/README.md)。历史调研和审查快照不能覆盖 `define.md`、代码和测试
表达的现行契约。

- [配置驱动事件生成器](eventgen.md)：数据表、模板、生命周期与动态运行控制。
