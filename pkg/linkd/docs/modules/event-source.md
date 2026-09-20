# EventSource

EventSource 是一类事件来源在 Linkd 进程中的静态定义。它不是 Event/Alert 一样的持久化领域对象，
而是 Cleaner 生成 Event 时使用的来源、租户、标准化和 MQ subscription 权威配置。

## 1. 职责

一条 EventSource 同时决定：

- `event_source_id`：写入所有 Event 和 Alert 的稳定来源身份；
- Event 归属租户来自消息，还是被 `related_tenant_id` 强制覆盖；
- 使用哪个 `SourceCleaner` 解释 payload；
- 用哪些稳定 Event 字段生成 fingerprint；
- 如何把来源 severity 映射为 Linkd Severity；
- 从哪个 MQ subscription 接收消息；
- 该来源 Cleaner Flow 的局部运行预算。

EventSource 不负责 Alert 状态裁决、Event/Alert 持久化实现或 Lifecycle lease。

## 2. 配置模型

| 字段                 | 约束                                     | 语义                                            |
| -------------------- | ---------------------------------------- | ----------------------------------------------- |
| `event_source_id`    | 1–32 bytes，`^[a-zA-Z0-9_-]+$`，全局唯一 | 稳定来源身份                                    |
| `related_tenant_id`  | 0–64 bytes                               | 非空时强制覆盖消息租户；为空时消息必须提供租户  |
| `enabled`            | 必填 bool                                | 是否创建并运行该来源 Flow                       |
| `cleaner.type`       | 当前只支持 `standard`                    | SourceCleaner 注册名                            |
| `cleaner.runtime`    | 可选，非零字段覆盖顶层 cleaner           | 该来源的 worker、批次、inflight、重试和关闭预算 |
| `fingerprint_mode`   | `field/fields`                           | 单字段原值或多字段摘要                          |
| `fingerprint_field`  | field 模式必填                           | 默认 `source_alert_id`                          |
| `fingerprint_fields` | fields 模式 1–32 项                      | 多字段按路径排序后计算 SHA-256                  |
| `severity_mapping`   | 来源值 → 已定义 Severity name            | 来源等级映射                                    |
| `default_severity`   | 已定义 Severity name                     | 来源值无法映射为标准 name 时的兜底              |
| `storage.type`       | 当前必须为 `kafka`                       | 当前字段名表示输入 MQ 类型                      |
| `storage.kafka`      | brokers/topic/consumer_group/security    | Kafka subscription 与认证配置                   |

当前文件配置要求每条 EventSource 都提供 storage，包括 disabled 来源。相同标准化 brokers、topic 和
consumer_group 的 subscription 不允许在两个 EventSource 中重复，避免同一消费责任被重复装配。

完整 YAML 示例和 Cleaner 默认预算见[配置指南](../guides/configuration.md)。

## 3. Fingerprint

fingerprint 是 Lifecycle 查找 active Alert 和构造 MailboxID 的唯一业务关联键。EventSource 只能引用：

- `source_alert_id`；
- `condition_key`；
- `subject_system`、`subject_type`、`subject_id`；
- `dimensions.<key>`。

field 模式要求目标值是 1–128 bytes 的非空字符串并直接作为 fingerprint。fields 模式保留字段路径、
标量类型和值后计算十六进制 SHA-256；配置顺序不影响结果。字段缺失或值非法时，消息按确定性错误
Discard，不降级到随机值或进程内顺序。

同一业务问题必须在所有 action 上产生相同 fingerprint。生产者还应使用同一稳定来源身份作为 MQ
partition key，使同一 fingerprint 原则上进入同一 lane。

## 4. Severity

全局 Severity 表默认是 `critical(1)`、`warning(2)`、`info(3)`，priority 越小越严重。自定义 levels
整体替换默认表，name 和 priority 必须唯一。

EventFactory 按以下顺序选择 Event severity：

```text
source severity
  → severity_mapping 命中值
  → 原值命中全局 Severity name
  → EventSource.default_severity
  → global severity.default_severity
```

Event 和 Alert 只保存 Severity name。Lifecycle 在处理 triggered Event 时通过当前进程冻结的 Severity
表比较等级；配置修改需要重启，不会回溯修改已有对象。

## 5. Flow 装配

关键抽象为：

```go
type FlowFactory interface {
    NewFlow(ctx context.Context, source config.EventSource) (Flow, error)
}

type Flow interface {
    Run(ctx context.Context) error
}
```

`cleaner.Scheduler` 在启动时深拷贝并校验全部 EventSource，然后为每个 enabled 来源并发创建一条独立
Flow。每条 Flow 拥有自己的 MQ Session、Cleaner Runtime、确认状态和重试状态；不同来源不得共享
这些可变状态。

当前 Scheduler 采用 fail-fast：任一 Flow 创建失败、panic、异常返回或运行失败都会取消其他 Flow，
等待它们完成清理后返回聚合错误。当前没有来源级健康状态、自动重建或部分可用控制面。

默认 `FlowFactory` 当前只把 EventSource 的 Kafka 配置装配为 `consume/kafka.Session`。Cleaner Runtime
本身与 MQ 解耦；增加其他来源适配器时，需要同时扩展 EventSource storage 配置和 FlowFactory，不能在
Cleaner 核心中判断具体 MQ 类型。

## 6. 生命周期与变更边界

- EventSource 在进程启动时加载并冻结；当前不支持热更新或 revision；
- 修改、启用或停用来源需要重启对应进程；
- `event_source_id`、fingerprint 和 Severity name 会进入持久化对象，变更前必须评估存量数据关系；
- 当前没有 EventSource 数据库、控制面同步或来源级迁移协议；
- 当前配置只接受 `event_sources[].event_source_id`，不提供其他来源字段名的别名。
