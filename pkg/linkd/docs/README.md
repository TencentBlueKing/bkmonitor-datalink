# Linkd 文档中心

Linkd 项目仍处于早期开发阶段。文档中心区分当前代码、测试和已确认契约与尚未实现的开发方案；
不为未发布的旧内部模型保留兼容说明页。

图解入口：[整体架构图](design/architecture.md#整体架构) · [事件处理流程图](design/architecture.md#处理链路)。
两张图统一在总体架构文档维护，包含同步处理、控制面后台推进及外部系统边界。

## 开发实施方案

- [Event 丰富与抑制、屏蔽、合并开发方案](design/event-enrich-and-alarm-policies.md)：Event Enrich 前移、
  字段变更、KAC 三类策略配置与行为、控制面任务、Console、可靠输出与降级及验收矩阵。
  当前代码流程见上述图解；业务决策、历史实施证据和
  [当前能力与剩余差距](design/event-enrich-and-alarm-policies.md#112-当前能力与剩余差距2026-10-08)分别标明。

## 阅读顺序

1. [核心数据模型](design/define.md)：Event、EventProcessing、Alert、AlertLog 及其关系与不变量。
2. [项目 README](../README.md)：当前可运行能力、启动方式和实现边界。
3. [EventSource](modules/event-source.md)：来源身份、租户、fingerprint、Severity、MQ 和 Flow 边界。
4. [Cleaner](modules/cleaner.md)：纯清洗、`n → * → n`、批量副作用和 ACK。
5. [Lifecycle](modules/lifecycle.md)：Mailbox、Signal、lease、状态裁决和恢复。
6. [配置指南](guides/configuration.md)：YAML 配置、默认预算和校验规则。
7. [Standard Event 模拟器](guides/event-generator.md)：持续生成常见告警并推送到指定 EventSource。
8. [总体设计](design/architecture.md)：整体架构图、事件处理流程图、模块和可靠性边界。
9. [部署模式](design/deployment.md)：All-in-one 与三进程拓扑、职责和演进边界。
10. [消息消费运行时](design/message-consumption-runtime.md)：MQ 通用端口、确认和背压。
11. [核心存储契约](design/core-storage-contract.md)：Repository、CAS 和物理资源。
12. [Linkd 标准事件](reference/contracts/standard-event.md)：RawEvent 输入、KAC V2 校验与旧 `AlarmEvent` 字段映射。
13. [其他外部协议](reference/contracts/README.md)：Kafka Alert V1 和 KAC Alarm 输出。

Kubernetes 部署入口：[Helm 部署与 worker 分组](guides/helm.md)。
多级别事件改造的测试环境切换见[重置升级指南](guides/multilevel-event-upgrade.md)；
[核心模型 HTML 说明页](linkd-core-model.html)提供当前模型与处理流程的可视化概览。

## 当前文档与归档

KAC 当前出口见[全局兼容插件](design/kac-compatibility-plugin.md)：Linkd 直接维护 `alarm_event`，
确认兼容文档可搜索后再可靠通知获准动作。外部 KAC 接入与生产切流需另行验证。
原 Kafka 转换路径见 [KAC Alarm Hook 设计](design/kac-alarm-hook.md)，不能将旧 Hook 等同于全局插件的可靠性保证。

EventSource 管理与部分全局配置动态化独立演进：

- [EventSource 动态配置](design/event-source-dynamic-configuration.md)：来源独立管理、主动拉取/API 修改、版本与 Flow 生效，已接入实现。
- [部分配置动态化](design/dynamic-configuration.md)：Severity 等选定配置项的消费者、生效和恢复边界，不包含来源管理，已实现，默认关闭。

跨模块运行协议：[中心化任务调度](design/task-scheduling-protocol.md)，由 EventSource 首先接入，后续可供
Lifecycle 等模块复用；包含停止确认、失联自停、超时强切及服务/容器发布防抖，当前采用有界自停模型。

`design/`、`modules/`、`guides/` 和 `reference/` 是现行文档，修改行为时必须同步更新。已经被当前
模型替换的早期占位页和重复模块页不再保留；旧的 alarm_callback 丰富迁移计划也未纳入本目录。
当前接口以[丰富设计](design/enrich.md)与[核心模型](design/define.md)为准。

`research/` 与 `reviews/` 是带日期或 commit 基线的归档输入，不属于当前契约。阅读归档时必须以
其记录的版本为准，不能反向覆盖当前代码和设计。

## 文档分类

- `guides/`：使用、配置、部署和排障步骤。
- `design/`：跨模块流程、系统边界和重要取舍。
- `modules/`：单个实现模块的职责、输入输出和不变量。
- `reference/`：统一术语与外部协议。
- `research/`：带版本和证据的历史技术输入。
- `reviews/`：特定时间点的审查快照。
