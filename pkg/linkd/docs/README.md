# Linkd 文档中心

Linkd 项目仍处于早期开发阶段，本文档只描述当前代码、测试和已确认契约，不为未发布的旧内部模型
保留兼容说明页。

## 阅读顺序

1. [核心数据模型](design/define.md)：Event、EventProcessing、Alert、AlertLog 及其关系与不变量。
2. [项目 README](../README.md)：当前可运行能力、启动方式和实现边界。
3. [EventSource](modules/event-source.md)：来源身份、租户、fingerprint、Severity、MQ 和 Flow 边界。
4. [Cleaner](modules/cleaner.md)：纯清洗、`n → * → n`、批量副作用和 ACK。
5. [Lifecycle](modules/lifecycle.md)：Mailbox、Signal、lease、状态裁决和恢复。
6. [配置指南](guides/configuration.md)：YAML 配置、默认预算和校验规则。
7. [Standard Event 模拟器](guides/event-generator.md)：持续生成常见告警并推送到指定 EventSource。
8. [总体设计](design/architecture.md)：当前处理链路和模块边界。
9. [部署模式](design/deployment.md)：All-in-one 与三进程拓扑、职责和演进边界。
10. [消息消费运行时](design/message-consumption-runtime.md)：MQ 通用端口、确认和背压。
11. [核心存储契约](design/core-storage-contract.md)：Repository、CAS 和物理资源。
12. [外部协议](reference/contracts/README.md)：`standard` 输入和 Kafka Alert V1 输出。

## 当前文档与归档

`design/`、`modules/`、`guides/` 和 `reference/` 是现行文档，修改行为时必须同步更新。已经被当前
模型替换的早期占位页和重复模块页不再保留。

`research/` 与 `reviews/` 是带日期或 commit 基线的归档输入，不属于当前契约。阅读归档时必须以
其记录的版本为准，不能反向覆盖当前代码和设计。

## 文档分类

- `guides/`：使用、配置、部署和排障步骤。
- `design/`：跨模块流程、系统边界和重要取舍。
- `modules/`：单个实现模块的职责、输入输出和不变量。
- `reference/`：统一术语与外部协议。
- `research/`：带版本和证据的历史技术输入。
- `reviews/`：特定时间点的审查快照。
