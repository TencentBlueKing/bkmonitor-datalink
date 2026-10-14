# KAC 全局兼容插件

状态：2026-10-14。Linkd 为业务事实来源，KAC 保留查询和处置。当前实现使用 Redis Streams 轻量通知、控制面批量写入与独立动作发送。

## 1. 职责与流程

普通链路：保存 Alert → 轻量消息入队 → 控制面批量读取最新 Alert 并投影 → 独立 refresh 与并发动作投递。
主动关闭共用执行通道，直接 `refresh=true` 后返回。动作资格仍由原 Lifecycle 业务裁决决定。
插件覆盖所有来源与租户；全局开启后跳过旧 KAC Kafka Hook。不同租户的身份、队列数据和索引定位保持隔离。
不新增任务库、补偿扫描、投影 CAS、hash、快照、水位或确认记录。

## 2. KAC 索引基线

取证日期：2026-10-08；Kingeye `1874066858`，以下三个源码文件在取证时无工作区修改：

- `src/kingeye/kac/alarm/models.py`：`AlarmEvent.Index` 和 `AlarmEvent.Meta`。
- `src/kingeye/base/domains/alarm/models/alarm_event_document.py`：字段定义及旧状态枚举。
- `src/kingeye/base/infras/elasticsearch/base_document_tenant.py`：`_init_index`。

| 项目 | 取证结果及实现约束 |
| --- | --- |
| alias | `{KEY_PREFIX}alarm_event`；前缀来自 KAC 部署配置，不能用 Linkd 自身的 index_prefix 替换 |
| 引导索引 | `{alias}-000001`；已有 alias 时复用既有物理索引，不新建一套替代历史数据 |
| ILM | `{KEY_PREFIX}alarm_event_policy`，hot rollover 为 `max_size=30gb`、`max_age=60d` |
| 模板 | KAC 模板名为 alias，pattern 为 `{alias}*`；接管时检查已有模板，不叠加冲突的另一套定义 |
| 字段 | 保留 Keyword、Text、Integer/Object 等现有类型及查询语义 |
| 时间 | alarm_time、close_time、storage_time 使用 `yyyy-MM-dd HH:mm:ss`；保持现有时区转换约定 |
| 分析器 | 保留 split_by_whitespace_analyzer、char_filter 及 text.keyword 子字段 |
| 动态字段 | 保留 dynamic=true 和顶层 all_as_text 动态模板 |
| settings | 与目标 KAC 部署的 shards、replicas、max_result_window、max_terms_count、字段数量上限对齐 |

上述为源码基线，不是目标环境实际配置快照。部署时需要一个公共的 KAC ES 连接及原索引 alias；
不能从租户名、EventSource 或 blueking.app_code 猜出原索引位置。
索引维护由 Linkd 控制面承担，ILM 实际轮转仍由 Elasticsearch 执行。接管不隐含新增历史数据删除、
转储、reindex 或其他不可逆清理操作。

## 3. 当前写入与运行边界

新建使用已有字段转换；更新只写 Linkd 拥有的可变字段，保留 KAC 处置字段和标签。
原处置态保存在兼容文档的 `linkd_disposal_status` 中。alias/rollover 保留，位置使用有界内存缓存，不写同步元数据索引。
字段、稳定身份和同步关闭详见[兼容投影契约](../reference/contracts/kac-alert-projection-v1.md)。

并发默认 64、500 条/10ms 展批、4MiB 请求，动作独立 64 并发、10,000 条缓冲。配置见[配置指南](../guides/configuration.md#kac-全局插件配置)。
优雅重启停止接收和启动新工作，等待已开始的请求结束；未开始消息留在 Stream，单活动控制面重启后恢复。
失败记录日志和指标，结束当前消息；不迁移或删除旧任务数据。两端按[动作 V3](../reference/contracts/kac-action-delivery-v3.md)配套部署。

当前本地真实 ES/Redis、KAC HTTP、Celery 与终态审计验证及压测范围见[验收记录](../research/2026-10-14-kac-output-v3-validation.md)。
