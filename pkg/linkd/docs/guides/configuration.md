# 配置与启动

配置是严格单文档 YAML。未知字段、重复来源 ID、重复 Kafka subscription、无效 Cleaner、fingerprint
路径和 severity 引用都会使进程启动失败。

```yaml
cleaner:
  worker_count: 8
  max_batch_messages: 128
  max_batch_bytes: 4194304
  batch_wait_milliseconds: 20
  max_concurrent_batches: 2
  max_inflight_messages: 512
  max_inflight_bytes: 16777216
  max_inflight_per_lane: 256
  resume_inflight_per_lane: 128
  process_timeout_seconds: 30
  retry_max_attempts: 3
  retry_max_elapsed_seconds: 120
  shutdown_drain_timeout_seconds: 30

severity:
  default_severity: warning
  levels:
    - { name: critical, priority: 1 }
    - { name: warning, priority: 2 }
    - { name: info, priority: 3 }

event_sources:
  - event_source_id: source-a
    related_tenant_id: ""
    enabled: true
    cleaner:
      type: standard
      runtime:
        worker_count: 16
    fingerprint_mode: field
    fingerprint_field: source_alert_id
    severity_mapping:
      P1: critical
      P2: warning
    default_severity: warning
    storage:
      type: kafka
      kafka:
        brokers: [127.0.0.1:9092]
        topic: linkd-raw-source-a
        consumer_group: linkd-source-a
        security: { protocol: plaintext }
```

顶层 `cleaner` 是每条 EventSource Flow 的默认预算；`event_sources[].cleaner.runtime` 只覆盖非零
字段。每条 Flow 内共享清洗 worker pool，但 Event 持久化、Mailbox 入队和原消息确认始终按 lane 独立推进，
`max_concurrent_batches` 只限制同时执行副作用的 lane 数量，不启用跨 lane 合批。

未声明 severity 或 levels 为空时整体使用默认表。自定义表整体替换默认表；name 和 priority 必须唯一，
priority 越小越严重。EventSource mapping/default 的目标必须存在于全局表。来源值先匹配 mapping；未命中
但已经是全局 name 时直接使用，否则依次使用来源和全局 default_severity。

fingerprint_mode=field 默认读取 source_alert_id，目标必须是非空字符串且不超过 128 bytes；fields 模式
接受 1–32 个稳定路径并按路径排序、保留标量类型计算 SHA-256。允许
`source_alert_id/condition_key/subject_system/subject_type/subject_id/dimensions.<key>`，缺失维度直接拒绝。

EventSource 各字段的职责、fingerprint/Severity 规则和“一来源一 Flow”边界见
[EventSource 文档](../modules/event-source.md)。

Lifecycle 使用单 Redis List Mailbox 保存待处理 Event ID。默认 `key_prefix=linkd:lifecycle:mailbox`、单
Mailbox 上限 128、单次持锁最多排空 512 条。Signal Stream/Group 默认为
`linkd:lifecycle:signals` / `linkd-lifecycle`。Signal payload 使用独立的 `schema_version` 校验；代码
不提供其他字段名或 namespace 的兼容读取。

Cleaner 对目标 Signal Group 启用近似全局背压：默认每 3 秒最多执行一次 `XINFO GROUPS`，查询超时
1 秒，`lag + pending >= 100000` 时暂停新 Kafka fetch，降到 80000 时恢复。要求
`0 < low_watermark < high_watermark`、TTL 为 1～60 秒且查询超时不大于 TTL。查询失败或 lag 未知
fail-open，明确缺少 Group 时暂停。

控制面可分别配置 Elasticsearch 三项管理任务的周期，以及 Redis Signal Stream 的指标采集和安全裁剪：

```yaml
control_plane:
  elasticsearch:
    schema_and_active_reconcile_interval_seconds: 3600
    bucket_reconcile_interval_seconds: 21600
    archive_interval_seconds: 30
    archive_batch_size: 1000
    archive_worker_count: 4
  redis_stream:
    reconcile_interval_seconds: 60
    operation_timeout_seconds: 10
    max_entries: 100000
    trim_batch_size: 10000
```

`max_entries` 是软上限。只有 Stream 超过该值时才启动裁剪，每轮最多检查并删除
`trim_batch_size` 条安全前缀。控制面会读取全部 Consumer Group 的 `last-delivered-id` 和最老 PEL ID，
只删除所有 Group 都已经确认的连续前缀；未读或 Pending Signal 即使使 Stream 暂时超过上限也会保留。
配置的 `lifecycle.signal.group` 不存在、跨命令观察到 PEL 正在变化，或无法证明边界安全时，本轮只采集
指标而不裁剪。该任务要求同时配置 `storage.redis` 和 `lifecycle`，但不要求使用 Elasticsearch
Repository，因此 Redis-only 控制面也可以独立启动。

完整存储、Redis、lifecycle retry/lease/mailbox、Kafka output 和 telemetry 示例见仓库
[`configs/linkd.yaml`](../../configs/linkd.yaml)。常用命令：

```bash
linkd config validate --config /etc/linkd/linkd.yaml
linkd config print --config /etc/linkd/linkd.yaml
linkd run cleaner --config /etc/linkd/linkd.yaml
linkd run lifecycle --config /etc/linkd/linkd.yaml
linkd run control-plane --config /etc/linkd/linkd.yaml
linkd run all-in-one --config /etc/linkd/linkd.yaml
```

`config print` 会隐藏 MySQL、Redis、Elasticsearch 和 Kafka 认证信息。进程运行期间 EventSource 和
Severity 配置冻结；修改配置需要重启进程。

`cleaner`、`lifecycle`、`control-plane` 和 `all-in-one` 都会初始化自己的 telemetry runtime。启用
Prometheus exporter 后，每个进程分别暴露 `/metrics`；部署在独立 Pod 时可以使用相同端口，共享宿主
网络时则需要使用不同配置文件设置不冲突的 `listen_address`。Redis Stream 管理指标由执行任务的
`control-plane` 或 `all-in-one` endpoint 暴露，指标名、单位和告警含义见[可观测性设计](../design/observability.md)。

## Elasticsearch schema 重建

当前 Elasticsearch schema version 为 3，Event、Alert 和 AlertLog 的完整稳定领域字段直接保存在
`_source` 根层。Event、AlertHistory、AlertLog 默认使用 7 天 UTC 时间桶；Active Alert 使用单一热索引。
`control-plane` 当前分别装配 Elasticsearch Schema 与 Active 资源对账、时间桶维护和终态 Alert 归档任务。三项任务共享
连接和进程级监督；前两项按独立周期执行，归档按连续批量循环执行。数据进程遇到缺失 write alias 时直接失败。这些任务没有独立
启动 command。

时间桶可分别配置：

```yaml
storage:
  elasticsearch:
    # 单节点本地环境可设为 0；生产环境按节点拓扑和容灾要求设置或省略。
    number_of_replicas: 0
    time_partition:
      event_bucket_days: 7
      alert_history_bucket_days: 7
      alert_log_bucket_days: 7
      precreate_past_buckets: 1
      precreate_future_buckets: 1
      max_buckets_per_entity: 512
      max_future_skew_seconds: 300
```

`schema_and_active_reconcile_interval_seconds` 只驱动模板、Active Alert 索引和静态 alias 对账；
`bucket_reconcile_interval_seconds` 只驱动当前预创建窗口内的时间桶和 alias 维护；
`archive_batch_size` 是终态 Alert 单次扫描上限，`archive_worker_count` 是批内最大并发 Worker 数；默认每批
1000 条、4 个 Worker，配置范围分别为 1～10000 和 1～64，且 Worker 数不得超过批量上限。每个 Worker
使用 Bulk create History 和 Bulk CAS delete Active，单个 Bulk 子批最多 500 条并受 Repository 16 MiB
请求上限约束。`archive_interval_seconds` 是积压清空、整轮无进展或请求失败后的等待时间；有归档进展时
批次连续执行，不等待该间隔。单次搜索仍受 64 MiB 响应上限保护，超过时会自动缩小本轮拉取数量。

独立 `control-plane` 启动时会先按“Schema 与 Active 资源对账、时间桶对账”的顺序完成一次准备，再启动管理任务；
归档不阻塞数据面启动。
选择 Elasticsearch Repository 时三项任务自动启用；省略 `control_plane.elasticsearch` 时使用上述默认值，
显式配置该段只调整任务周期、归档批次和并发度，不改变任务所有权。

`number_of_replicas` 未配置时，Linkd 不覆盖 Elasticsearch 的模板默认值。显式配置后，`control-plane`
或 `all-in-one` 会把该值写入 Linkd 模板，之后新建的物理索引会继承该配置；已有索引不会随配置变更
被自动修改。单节点本地环境设为 `0` 可以避免因无法分配副本而长期处于 `yellow`，但这也意味着没有
副本冗余，不应直接照搬到生产环境。已有索引如需调整，应由操作者明确选择目标后通过动态 index
settings 原地修改，不需要删除索引或 reindex。

历史消息回放前显式准备桶：

```bash
linkd storage prepare --config /etc/linkd/linkd.yaml \
  --from 2026-08-01T00:00:00Z --to 2026-09-01T00:00:00Z
```

从旧 schema 切换时必须执行一次破坏式重建：

1. 停止所有使用该 `index_prefix` 的 Linkd 写入进程；
2. 确认旧数据不需要保留，或先在 Linkd 外部完成所需备份；
3. 显式删除 `<index_prefix>-*` 物理索引、旧模板和冲突 alias；
4. 清理或更换 Redis Mailbox/Signal 中引用旧 EventID 的状态；
5. 先启动 `linkd run control-plane`，确认 schema、Active 索引、当前及相邻桶和 alias 对账成功；
6. 再启动 cleaner/lifecycle；使用 all-in-one 时首次对账会在接管消息前同步完成；
7. 检查 DevTools Elasticsearch 拓扑后恢复上游流量。

删除操作不可恢复。本仓库不提供旧 schema 的 reindex、双读或双写逻辑，也不会在启动时替操作者执行
删除。本轮不自动执行 retention；达到 `max_buckets_per_entity` 时 Bucket Manager 会失败并要求人工处理。
