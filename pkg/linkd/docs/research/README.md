# 技术调研

本目录保存技术选型、行业方案比较、源码调研和实验验证记录。调研结论是设计输入，不自动成为现行架构或已实现能力。

每份调研至少应包含：

- 背景、问题和不在本次范围内的事项；
- 调研日期、目标版本、源码提交或资料链接；
- 候选方案、评价维度和可复现证据；
- 推荐结论、适用边界、风险及未决问题；
- 结论落地后对应的设计文档、模块文档或代码位置。

当调研结论被采纳时，应更新 [设计文档](../design/README.md) 或 [功能模块](../modules/README.md)；本目录保留论证过程，不能作为现行行为的唯一依据。

## 调研记录

| 文档 | 内容 |
|---|---|
| [benchmarks/README.md](benchmarks/README.md) | 带明确源码、平台、配置和负载模型的性能压测记录 |
| [2026-09-06-lifecycle-throughput-optimization-summary.md](2026-09-06-lifecycle-throughput-optimization-summary.md) | Alert 归档、Stream、Cleaner、Lifecycle 合批与最终吞吐配置的阶段性优化汇总 |
| [2026-09-06-lifecycle-execution-flow.md](2026-09-06-lifecycle-execution-flow.md) | Lifecycle 处理顺序、并发控制、中转队列及一致性边界审查 |
| [profiles/2026-09-07-lifecycle-gc/README.md](profiles/2026-09-07-lifecycle-gc/README.md) | Lifecycle/Cleaner 正常段、降速段和优化后的 pprof SVG 与 top 摘要 |
| [data-pipeline-observability.md](data-pipeline-observability.md) | OpenTelemetry、Kafka、Flink 与 CloudEvents 的数据流指标和 Trace 划分方案 |
| [kingeye-alarm-callback-data-dependencies.md](kingeye-alarm-callback-data-dependencies.md) | Kingeye `callback_utils` 告警数据生成的外部依赖、字段来源、调用时机及对 Linkd 丰富设计的输入 |
| [alarm-callback-input-field-inventory.md](alarm-callback-input-field-inventory.md) | 指定 `kac/alarm_callback` 的回调读取字段、14 个旧测试样本，以及 S01–S18 的鲸眼配置覆盖情况；新映射与按需读取监控平台的原则见迁移计划 |
