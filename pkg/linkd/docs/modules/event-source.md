# EventSource

EventSource 是由控制面管理并持久化发布的来源配置。Record/Release 兼容 ES/MySQL，运行时按固定 Release
创建 Cleaner/Lifecycle Flow；配置和调度详见[动态来源设计](../design/event-source-dynamic-configuration.md)。

## 1. 职责

一条 EventSource 同时决定：

- `event_source_id`：写入所有 Event 和 Alert 的稳定来源身份；
- Event 归属租户来自消息，还是被 `related_tenant_id` 强制覆盖；
- 使用哪个 `SourceCleaner` 解释 payload；
- 用哪些稳定 Event 字段生成 fingerprint；
- 如何把来源 severity 映射为 Linkd Severity；
- 从哪个 MQ subscription 接收消息；
- 该来源 Cleaner Flow 的局部运行预算；
- Alert 变更后按顺序执行的具名输出插件与各自参数。

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
| `hooks` | 可选有序列表，最多 16 项，name 唯一 | 来源发布中的输出插件；空列表不输出，详见 [Lifecycle](lifecycle.md#23-enricher-与-finalhook) |
| `enrich.processors`  | 有序且 type 不重复                       | 创建新 Alert 时执行的丰富处理链                 |
| `enrich.datasources` | 由 Processor 依赖决定                    | 随来源 Release 发布的共享 MySQL 与 Elasticsearch 物理连接 |
| `storage.type`       | 当前必须为 `kafka`                       | 当前字段名表示输入 MQ 类型                      |
| `storage.kafka`      | brokers/topic/consumer_group/security    | Kafka subscription 与认证配置                   |

当前文件配置要求每条 EventSource 都提供 storage，包括 disabled 来源。相同标准化 brokers、topic 和
consumer_group 的 subscription 不允许在两个 EventSource 中重复，避免同一消费责任被重复装配。

`enrich.datasources` 以物理连接为复用边界：`mysql` 由策略、告警源和指标 Reader 共用同一连接池，
`elasticsearch` 由 OneModel 等索引 Reader 共用同一 Transport。它们与 Processor Chain 使用同一发布版本。
来源发布会校验依赖完整性，Lifecycle worker 只为当前 Chain 选择并建立连接，来源任务退出时关闭连接。
配置变化进入执行摘要并触发对应来源任务换代。管理接口默认对 MySQL 密码、Elasticsearch API Key 和
Basic Auth 密码脱敏。

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

## 5. Enrichment 路由

每个 Lifecycle 任务按调度分配的 EventSource Release 冻结 `event_source_id → Processor Chain` 路由，
发布变更通过停止确认后重启任务生效。数据源连接由进程持有并共享；空链使用成功的 Noop，
未知来源表示配置与持久化数据不一致并进入 Lifecycle 丰富降级。当前已注册 `strategy/resource/display/metric/source`，列表顺序同时决定执行顺序和
`Alert.enrich.processors` 输出顺序。

丰富数据源使用进程静态配置的 MySQL 与 OneModel Elasticsearch 连接；配置与联调边界见
[配置指南](../guides/configuration.md)。

## 6. Flow 装配与变更

中心调度器分别规划 Cleaner/Lifecycle 副本，默认 all，支持标签、数字数量和 0；enabled=false 为总停用。
Cleaner 的数量自动受 Kafka topic partition 数限制。每个进程同源同角色最多一个 Flow，all-in-one 两角色可共存。
两类 Flow 复用进程连接资源；任务各自拥有消费 Session、确认和重试状态。
配置变更通过停止报告/确认后重新分配，失联按有界自停与超时强切恢复。

Event、Alert 均保存 event_source_version。Event 绑定生成时的 Release，Alert 继承创建 Event 的版本，普通更新不覆盖。
不保存 offset 版本区间，未落库消息按当前任务配置处理；已持久化相同原始事实的重投复用原 Event。
来源只通过 API/provider 或显式 import 命令修改，启动不自动导入 YAML。旧数据不自动迁移或清理。
