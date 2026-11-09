# 文档目录

## 使用与维护

| 文档 | 内容 |
| --- | --- |
| [构建与运行](overview/source_compile.md) | 环境、配置和启动命令 |
| [参数说明](overview/parameters.md) | 全部应用参数、默认值、单位、约束及配置示例 |
| [部署](overview/operation.md) | Flink 集群与作业提交 |
| [可观测指标](overview/observability.md) | 指标口径、Prometheus 名称、查询示例和排障用途 |
| [升级说明](overview/architecture.md#恢复与升级) | 旧状态与索引处理 |
| [集成测试](overview/integration-tests.md) | 故障恢复测试的运行方式与范围 |

## 实现与数据

| 文档 | 内容 |
| --- | --- |
| [数据流与状态](overview/architecture.md) | 输入、聚合、窗口和恢复规则 |
| [代码结构](overview/code_framework.md) | 包与主要类的职责 |
| [SDK 字段](RUM-SDK-Fields.md) | 上游字段参考 |
| [Span 清单](RUM-Span.md) | 上游事件类型 |
| [Session 字段表](RUM-Session-Schema.csv) | 字段来源与定义 |
| [预计算字段](Precalculate-index-fields.md) | Elasticsearch 输出字段 |
| [索引命名](index-naming.md) | 索引、分表与文档 ID |

## 开发规范

- [代码风格](specification/code-style.md)
- [Review 清单](specification/review.md)
- [Git 工作流](specification/git-workflow.md)
- [提交信息](specification/commit-spec.md)
- [更新日志](specification/changelog-spec.md)
