# 更新日志

## [Unreleased]

### Added

- 增加 [可观测指标说明](docs/overview/observability.md)，介绍指标含义、统计口径和 Prometheus 查询方式。
- 增加应用参数说明，列出默认值、单位、约束、配置优先级与运行示例。
- 增加 `integration-tests` profile，使用 Kafka、Elasticsearch 和 Flink MiniCluster 验证 TaskManager 故障恢复。

### Changed

- 精简作业指标，聚焦输入质量、窗口状态和 Elasticsearch 写入结果。
- 项目名称和 Maven `artifactId` 统一为 `rum-compute`，同步 JAR、Docker 构建和部署文档。
- 精简项目说明、部署文档和开发指南，修正 JSON 配置及 REST 恢复参数说明。
- 部署文件移至 `k8s-deploy/`，索引模板移至 `docs/elasticsearch/`，同步文档、Compose 和测试引用。
- Java 包名改为 `com.tencent.bk.bkmonitor.rum.*`。
- Session/View 默认空闲关闭时间为 15 分钟，最大生命周期为 240 分钟；关闭后的状态保留期统一为 120 分钟（2 小时）。
- 关闭原因增加 `idle_timeout` 和 `max_life`，迟到修正保留首次关闭信息。
- Session/View 按业务、应用和实体 ID 隔离状态；每个窗口生成 `window_id` 并参与 ES 文档 ID。旧状态及索引处理见 [恢复与升级](docs/overview/architecture.md#恢复与升级)。
- 日志配置（`log4j2.xml`）从 JAR 内置改为通过集群 ConfigMap 挂载注入，打包阶段排除内置配置，避免覆盖外部配置。

### Fixed

- 调整 Session/View 索引模板优先级，解决模板匹配范围重叠造成的安装冲突。
- 统一窗口定时器调度，修复截止时间重合时的定时器删除问题。
- 区分首次迟到和状态已清理的实体，允许保留期内修正关闭窗口。
- 分表数随 emitter 配置传递，修复缓存重建和多作业配置相互影响的问题。
- envelope 内单个 Span 类型错误不再影响后续合法 item 的解析。

## [1.0.0] - 2026-09-18

### Added

- 从 Kafka 读取 RUM 事件，按 Session/View 聚合并写入 Elasticsearch。
- 处理时间窗口、事件身份去重、迟到审计和作业停止处理。
- REST API 提交和 JobFlow 接入说明。
