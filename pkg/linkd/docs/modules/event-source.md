# EventSource

EventSource 是由控制面管理并持久化发布的来源配置。Record/Release 兼容 ES/MySQL，运行时按固定 Release
创建对应的 Cleaner/Lifecycle Flow；内部合并来源只创建 Lifecycle。配置和调度详见[动态来源设计](../design/event-source-dynamic-configuration.md)。

## 1. 职责

普通 Kafka EventSource 同时决定：

- `event_source_id`：写入所有 Event 和 Alert 的稳定来源身份；
- Event 归属租户来自消息，还是被 `related_tenant_id` 强制覆盖；
- 使用哪个 `SourceCleaner` 解释 payload；
- 用哪些稳定 Event 字段生成 fingerprint；
- 如何逐项把 evaluations 的来源 severity 映射为 Linkd Severity，并拒绝映射后重复级别；
- 从哪个 MQ subscription 接收消息；
- 该来源 Cleaner Flow 的局部运行预算；
- Alert 变更后按顺序执行的具名输出插件与各自参数。

EventSource 不负责 Alert 状态裁决、Event/Alert 持久化实现或 Lifecycle lease。

KAC 新建告警源时只生成 `linkd_source_id` 和 `linkd_channel.config.topic`，不会自动向 Linkd 发布 EventSource。
要启用消费，需通过 Linkd 控制面 API 或配置导入发布 EventSource，令 `event_source_id` 等于
`linkd_source_id`，`storage.kafka.topic` 等于 `linkd_channel.config.topic`，并配置 brokers、consumer_group
等必填项。运行时先按发布的 EventSource 订阅 Kafka；`source` 丰富处理器在 Event 策略判定前按租户与
`event_source_id` 回查 KAC 告警源，读取其原始 `id` 和名称。该回查不负责创建或更新 Kafka 订阅。

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
| `hooks` | 可选有序列表，最多 16 项，name 唯一 | 来源发布中的输出插件；空列表不输出，详见 [Lifecycle](lifecycle.md#24-enricher-与-finalhook) |
| `enrich.processors`  | 有序且 type 不重复                       | 每条 Event 在策略前执行的丰富链                 |
| `storage.type`       | `kafka/internal_merge`                       | 外部订阅或系统内部合并输入                      |
| `storage.kafka`      | brokers/topic/consumer_group/security    | Kafka subscription 与认证配置                   |

当前文件配置要求每条 EventSource 都提供 storage，包括 disabled 来源；只有 kafka 类型要求 storage.kafka。相同标准化 brokers、topic 和
consumer_group 的 subscription 不允许在两个 EventSource 中重复，避免同一消费责任被重复装配。

来源的 enrich 只保存处理链和规则。外部连接使用进程顶层 `resources.mysql`、`resources.onemodel`、
`resources.kingeye_display`，不进入新发布的 Record/Release。发布时按处理规则校验公共资源是否完整，
Lifecycle 为当前 Chain 按需建立连接，任务退出时关闭。处理规则变化继续触发来源任务换代；
公共资源变化需要重启控制面和 Lifecycle。旧 `enrich.datasources` 配置入口已移除。

完整 YAML 示例和 Cleaner 默认预算见[配置指南](../guides/configuration.md)。

### KAC 全局插件

KAC 兼容存储和处置通知统一由部署级 `plugins.kac` 启用。所有来源、所有租户以及
`builtin_alarm_merge` 自动生效；EventSource 不保存 KAC 连接、Token、目标或启用开关。
`kac_targets`、`resources.kac_delivery` 和 `projection_endpoint` 已移除，旧配置明确报错。

Lifecycle 创建 Alert 时绑定固定插件目标 `kac`，同时启用状态同步和获准动作。
目标的 `source_version` 只记录 opening Event 的来源版本，不再用于解析地址或凭据；
来源改版不会改变既有任务的业务快照或处置资格。冻结计划重试不重新选择目标。

全局插件直接维护 KAC 原 `alarm_event` 索引，兼容存储与处置通知共用同一租户/Alert/目标锁空间。
普通状态 Hook 可并存；Alert 已绑定可靠动作时，旧 `type: kac` Kafka Hook 跳过输出，不进行故障回退。
不设计首次启用时的 Alert 回填、旧目标迁移或切换流程。

配置见[全局 KAC 插件](../guides/configuration.md#kac-全局插件配置)，可靠性见
[兼容存储 V1](../reference/contracts/kac-alert-projection-v1.md)及
[动作投递 V2](../reference/contracts/kac-action-delivery-v2.md)。

### 内部合并来源

`storage.type=internal_merge` 只允许保留 ID `builtin_alarm_merge`；普通 Kafka 来源不能占用该 ID。
该类型禁止 Kafka、Cleaner、指纹选择器和 severity mapping，Cleaner 副本规范化为 0，不探测 Kafka
元数据。Lifecycle 仍使用正常的来源 Release、Mailbox、租约和输出配置，默认 Enrich/Hook 为空。

`EnsureMergeSource` 仅在不存在时创建发布，保留用户后续配置、停用与删除。配置 Lifecycle 的控制面
在调度历史检查和首次初始化成功后调用它；已有来源而 Redis 调度历史丢失时，仍须显式停机初始化，
不能借自动创建内置来源绕过该保护。内部 Event 的 `merge_origin`
属于不可变事实，必须引用固定裁决；外部 EventFactory 不接受内部来源。Console 隐藏不适用的
Kafka/Cleaner 表单，只编辑 Lifecycle 与该来源自己的 Enrich/Hook。

下面第 3、4 节的外部映射规则仅适用于 Kafka 来源；内部合并身份和模板遵循
[合并开发方案](../design/event-enrich-and-alarm-policies.md#83-成功后的主告警与父子关系)。

## 3. Fingerprint

fingerprint 是 Lifecycle 查找 active Alert 和构造 MailboxID 的唯一业务关联键。EventSource 只能引用：

- `source_alert_id`；
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

Event 和 Alert 只保存 Severity name。Lifecycle 每次裁决通过当前进程的完整 Severity 快照比较等级。动态配置默认关闭；启用后在线应用，
旧 mapping 引用已删除等级不阻止 Flow 装配。未映射且无显式来源默认值的未知标准等级保留到 Event，
由 Lifecycle 标记 rejected；未知等级活动 Alert 在下次处理同一 fingerprint 时关闭。详见[动态配置](../design/dynamic-configuration.md)。

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

Event、Alert 均保存 event_source_version。Event 绑定生成时的 Release，Alert 继承创建 Event 的版本，同级更新与 update_current 升级不覆盖。
不保存 offset 版本区间，未落库消息按当前任务配置处理；已持久化相同原始事实的重投复用原 Event。
来源只通过 API/provider 或显式 import 命令修改，启动不自动导入 YAML。旧数据不自动迁移或清理。
