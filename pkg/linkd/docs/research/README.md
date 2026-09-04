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
| [data-pipeline-observability.md](data-pipeline-observability.md) | OpenTelemetry、Kafka、Flink 与 CloudEvents 的数据流指标和 Trace 划分方案 |
| [kingeye-alarm-callback-data-dependencies.md](kingeye-alarm-callback-data-dependencies.md) | Kingeye `callback_utils` 告警数据生成的外部依赖、字段来源、调用时机及对 Linkd 丰富设计的输入 |
