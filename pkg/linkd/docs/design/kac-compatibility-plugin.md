# KAC 全局兼容插件

状态：2026-11-12。Linkd 为业务事实来源，KAC 保留查询和处置。当前实现使用 Redis Streams 轻量通知、控制面批量写入与独立动作发送。

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

上述为旧 KAC 源码基线，不是目标环境实际配置快照，也不代表当前 Linkd 新索引的轮转方式。部署时需要一个公共的 KAC ES 连接及原索引 alias；
不能从租户名、EventSource 或 blueking.app_code 猜出原索引位置。
当前 Linkd 新文档改为按 Alert.CreateAt 划分 UTC 时间桶，默认 7 天，原点与 Linkd Repository 一致（1970-01-05 UTC）。
物理索引为 `{alias}-YYYYMMDD`，缺失桶在首次写入前按需创建，所有桶加入原查询 alias。
控制面移除模板中的 ILM 声明，保留原模板其他运维参数和已有索引；旧索引上的 ILM 策略不修改。
本次不实现容量滚动、历史数据删除、
转储、reindex 或其他不可逆清理操作。

## 3. 当前写入与运行边界

新建使用已有字段转换；更新只写 Linkd 拥有的可变字段，保留 KAC 处置字段和标签。
原处置态保存在兼容文档的 `linkd_disposal_status` 中。查询 alias 保留，已有文档原位更新；新文档及重试固定写入创建时间桶。位置使用有界内存缓存，不写同步元数据索引。
字段、稳定身份和同步关闭详见[兼容投影契约](../reference/contracts/kac-alert-projection-v1.md)。

并发默认 64、500 条/10ms 展批、4MiB 请求，动作独立 64 并发、10,000 条缓冲。配置见[配置指南](../guides/configuration.md#kac-全局插件配置)。
优雅重启停止接收和启动新工作，等待已开始的请求结束；未开始消息留在 Stream，单活动控制面重启后恢复。
失败记录日志和指标，结束当前消息；不迁移或删除旧任务数据。两端按[动作 V3](../reference/contracts/kac-action-delivery-v3.md)配套部署。

当前本地真实 ES/Redis、KAC HTTP、Celery 与终态审计验证及压测范围见[验收记录](../research/2026-10-14-kac-output-v3-validation.md)。

## 4. Kingeye 查询兼容性

2026-11-12 核对 Kingeye 本地源码：`AlarmApiFunc.get_alarm_list`、`get_alarm_event_detail`
通过 `AlarmEvent.get_query_instance` / `query_alarms` 搜索原查询 alias；
`AlarmEvent.get_alarm_id_index_mapping` 从搜索结果取得 `_index` 和 `_id`，处置与标签更新使用该物理位置。
因此保留原 alias 并纳入所有新桶即可延续这些查询、聚合与更新路径，不需要改为拼接日期索引。
生命周期日志使用独立的 Linkd AlertLog 查询 alias，与本次变更无关。

桶按创建时间划分，查询的最新发生时间、关闭时间和状态可能对应很早创建的告警；
不能仅依据这些筛选时间裁剪查询桶，否则会漏查长期告警。桶数增加可能扩大查询分片范围，
本次不包含长期容量下的性能验收，也不宣称已经通过 Kingeye 在线页面端到端验收。
