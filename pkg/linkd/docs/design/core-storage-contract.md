# 核心存储契约

领域字段以 [`define.md`](define.md) 为准。本文件只定义 Repository 的逻辑操作和故障边界。

## Repository 端口

`store.Repository` 组合四个窄接口：

- `EventStore`：Event 批量幂等创建、读取和处理结果 CAS；
- `AlertStore`：Alert 创建、读取、active 关联查询和快照 CAS；
- `AlertLogStore`：不可变流水批量追加和时间线分页；
- `QueryStore`：按 Event 查询其处理结果和已关联 Alert。

调用方只依赖这些逻辑端口，不解析 MySQL version、Elasticsearch index/seq_no 或物理路由。

## 对象与版本

- `StoredEvent = Event + EventProcessing + VersionToken`。processing 不进入 Event JSON，状态为
  `unprocessed | accepted | suppressed | orphaned | rejected`。
- `StoredAlert = Alert + VersionToken`。
- `VersionToken` 对调用方不透明，只能从一次读取原样交给同一对象的 CAS；MySQL 内部使用 version，
  Elasticsearch 使用物理 `_index/_id/_seq_no/_primary_term`。
- Event、Alert 和 AlertLog 是独立对象，Repository 不承诺跨对象事务。

## Event

- `CreateEvent` 只接受 `related_alert_id` 为空的新 Event；相同身份和内容为幂等，内容不同返回
  `ErrIdentityConflict`。
- `CreateEvents` 按输入顺序返回逐项创建、幂等、冲突或暂时失败结果；请求级错误表示无法可靠解释
  整批响应。`CreateEvent` 委托 batch-of-one，避免维护两套语义。
- Elasticsearch 使用 `_bulk?refresh=wait_for` 的逐项 `create`；409 回读并校验内容，429/5xx 保持
  可重试。MySQL 首版通过统一批量接口逐项写入，不宣称多行 SQL 性能。
- `CompareAndSetEventResult` 在一次 CAS 中同时写 processing 和 `related_alert_id`。accepted 与 suppressed
  必须关联 Alert；orphaned 与 rejected 必须保持为空。
- `ListEventsByAlert` 按 `related_alert_id + received_at + event_id` 稳定分页；ES 使用 Alert 生命周期边界
  缩小 Event 桶范围，跨度过大时回退到 read alias。
- Elasticsearch 的 Event 结果 CAS 使用 `refresh=false`：成功只表示写请求已经完成，不保证新的
  processing 状态已经能被 `_search` 查询到。单 Event `GetEvent` 根据稳定 EventID 路由到写 alias 并使用
  realtime GET，使 Lifecycle 能立即识别重复 Mailbox 引用对应的终态；列表和统计查询仍接受 refresh 延迟。

## Alert

- 同一 `(bk_tenant_id, event_source_id, fingerprint)` 同时最多一个 active Alert。MySQL 用唯一键约束；
  Elasticsearch 依赖 lifecycle lease、active 查询和覆盖 refresh 窗口的 Redis Recent Alert 缓存保证正常处理路径中的
  唯一性，不额外维护全局 fingerprint 唯一索引。
- Elasticsearch Active 与 History 文档都使用租户与 `alert_id` 摘要作为 `_id`。归档过渡期间同一
  Alert 可以同时存在于两个索引，内容必须一致；不同 Alert 即使 fingerprint 相同也使用不同 `_id`。
- Alert CAS 必须保留所有继承字段和创建锚点，只允许推进生命周期字段；终态 Alert 不可再替换。
- `FindAlertEndedByEvent` 只匹配 `latest_event_id` 等于目标 Event 且 `end_type` 为 source 或
  severity_upgrade 的终态 Alert，用于恢复跨对象部分成功。
- Lifecycle 专用 Alert create/CAS 使用 `refresh=false`，写成功后必须先更新 Recent Alert 缓存再推进
  Hook、日志和 Event；普通 Repository 契约仍可使用 `refresh=wait_for` 保证独立调用后的搜索可见性。
- CAS 冲突修复按 VersionToken 指向的物理文档执行 realtime GET，不依赖 `_search` refresh。

## AlertLog 与查询

- `AppendAlertLogs` 按输入顺序返回逐项结果，不承诺整批事务；`AppendAlertLog` 委托 batch-of-one。
  相同 log_id 和内容为幂等，内容不同返回 `ErrIdentityConflict`；
- Elasticsearch 使用 `_bulk?refresh=false` 追加 AlertLog；409 使用 realtime GET 核对内容。Bulk
  成功不保证日志已经能被 `_search` 或 PIT 查询到，调用方必须接受 refresh interval 内的短暂缺口；
- `ListAlertLogs` 只读取指定租户和 Alert，按 `created_time + log_id` 稳定升序分页；
- 默认分页 100，最大 500；cursor 绑定对象类型、租户、父 Alert 和物理读目标，不能跨查询复用；
- `QueryAlertByEvent` 总是返回 Event；Event accepted/suppressed 且 related_alert_id 存在时返回关联 Alert；
- 查询组合多个对象时不承诺事务快照。

## 物理资源

- MySQL：`linkd_events`、`linkd_alerts`、`linkd_alert_logs`。
- Elasticsearch 稳定读 alias 为 `<prefix>-events`、`<prefix>-alerts`、`<prefix>-alerts-active`、
  `<prefix>-alert-history`、`<prefix>-alert-logs`。Event、AlertHistory、AlertLog 物理索引按 UTC 时间桶创建，
  Active Alert 使用单一热索引，`refresh_interval` 默认 5 秒且可通过 YAML 配置；时间桶默认 7 天。
- 数据进程只通过 `require_alias=true` 写入 Bucket Manager 创建的 per-bucket write alias。
  `control-plane` 分别装配 Schema 与 Active 资源对账、时间桶维护和遗留终态 Alert 归档任务；三者共享连接，
  前两项使用独立周期，归档使用连续批量循环，且没有各自的常驻 command。缺失目标时数据写入和归档失败，
  不在调用路径自行建索引。
- ES `_source` 直接保存完整 Event、Alert 或 AlertLog 领域字段，不使用通用 `payload` 包装。EventProcessing
  作为 Event 根字段旁的 `processing` object 保存，Lifecycle 查询使用 `processing.state`。
- ES mapping 为 strict；dimensions/labels 使用 flattened。`source_raw_data`、`extra_data`、`enrich` 和
  AlertLog `params` 显式存在但 `enabled=false`，展示文本显式存在但不建立倒排索引或 doc values。
- 当前 ES schema version 为 3；索引 mapping `_meta` 记录实体、role、桶周期与起止时间。配置不匹配时
  对账任务失败，不自动迁移或删除已有数据。
- Alert 与 AlertHistory 的 `_source` 和 mapping 完全一致。Lifecycle 的终态 CAS 只更新 Active，成功后
  不等待物理搬迁；归档任务持续扫描终态文档，由有界 Worker 先 Bulk create-only 写 History，再只对已确认
  存在于 History 的项目按 Active 原版本 Bulk delete。单项失败保留 Active 并在后续扫描重试，不回滚
  已成立的逻辑终态，也不终止其他归档项或数据面进程。
- Event、Alert 和 AlertLog 使用租户与对象 ID 摘要作为 `_id`；Alert CAS 使用
  `_seq_no/_primary_term`。
- 本轮不自动删除旧桶；每类物理桶有硬上限。历史回放必须先用 `linkd storage prepare --from --to`
  显式准备目标范围。
