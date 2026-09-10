# 配置与启动

## 本地 pprof 诊断

`telemetry.profiling` 默认关闭。只在有界现场诊断中开启，并使用与 Prometheus 不同的回环端口：

```yaml
telemetry:
  profiling:
    enabled: true
    listen_address: 127.0.0.1:9474
    block_profile_rate: 100000
    mutex_profile_fraction: 10
```

开启后提供标准 `/debug/pprof/`、CPU、allocs、heap、goroutine、block、mutex 和 trace endpoint。
监听地址必须是 IP 回环地址；配置非回环地址会使进程在接管消息前启动失败。block/mutex 采样
存在运行时开销，普通运行不要配置这些字段。不同常驻角色需要使用不同端口。

## Kafka 分区恢复延迟

Cleaner 的组批等待仅在批次尚未执行、存在空闲执行槽位且期限尚未到达时驱动定时唤醒。
执行中和等待槽位的批次由完成通知推进，不能用过期组批期限忙等；批次槽位在通知之前释放。
重试、撤销所有权与退出截止时间独立调度，不依赖忙等检查期限。

Cleaner 的 ES Event Bulk 遇到多个 create 冲突时，使用有界 realtime `_mget` 核对内容和最新
processing，避免重复投递比例升高时逐条 GET 占住批次槽位。单个冲突保留 realtime GET；
读取失败不重放本批已成功创建的项，也不跳过内容或租户校验。

`event_sources[].storage.kafka.fetch_max_wait_milliseconds` 默认 100，允许 10～5000。
它限制 broker 等待空 fetch 的时间，不是 Cleaner 的批次等待，也不是 ES 合批期限。
显式配置可用于对照；普通运行无需手调。较短的等待会增加空闲 fetch 请求量，
但避免某个有积压分区从暂停恢复后，被同 broker 上其他空分区的长轮询拖住数秒。
单分区在途上限、暂停/恢复滞回与连续 ACK 规则不变。

独立定向集成测试（创建并清理唯一测试 topic/group，不使用业务 topic）：

```bash
LINKD_TEST_KAFKA_BROKERS=127.0.0.1:9092 go test -race ./internal/consume/kafka -run TestKafkaPartitionTailFetchWait -count=3 -v
```

测试使用同 broker 的三个分区，仅向 partition 1 预写 1024 条有序消息，停止生产后再消费，
按 256 条模拟达到 lane 上限，每次暂停 200ms 并持续 Poll 空分区，比较 5s 与 100ms 的恢复空档。
这个测试定位 Kafka Session 的恢复行为，不代表 ES/Lifecycle 端到端吞吐。

## Lifecycle Elasticsearch 合批

Lifecycle 默认并发为 32；`lifecycle.elasticsearch_write_batch` 默认启用，仅作用于 Elasticsearch
Lifecycle runtime。Event result update、Alert CAS/create、AlertLog create 跨独立调用合并为 Bulk，
CAS 校验和冲突核对的 realtime GET 合并为 `_mget`。Lifecycle Event 点读通过 `_source` 投影排除
不参与裁决的 `source_raw_data`，Event 终态 update 只写 `related_alert_id` 和 `processing`；
公共 Event 查询仍返回完整文档。每项成功后调用方才继续缓存、输出和 ACK；
批次没有跨文档事务语义，Cleaner 与 Archiver 不使用这个队列。

```yaml
lifecycle:
  concurrency: 32
  elasticsearch_write_batch:
    enabled: true
    max_bytes: 4194304
```

`max_bytes` 可配置为 1～16 MiB。其他调度参数只读，由 Lifecycle 并发 C 在启动时自动推导：
单批操作数 B = `min(128, max(1, floor(C / 2)))`；写侧最大等待为 B 毫秒，B=1 时不等待；
读侧最大等待为 `min(10, 写侧最大等待)` 毫秒，B=1 时同样不等待；
执行并发上限为 `min(32, C)`。数量阈值或首项等待期限达到即发送，
编码字节预算也可提前触发发送。realtime 点读使用独立队列和短收集窗口，
复用写侧的满批/字节/期限触发规则；达到上限立即发送，不等待写批次期限、不计入写操作阈值。
读写请求共享总执行并发上限。读取元信息与 mget 外层 JSON/分隔符一并计入字节预算，
请求仍使用 realtime 语义；短窗口不改变 CAS、逐项结果或失败恢复边界。
单个合法超预算操作独立发送，仍受
Repository 单文档/单请求硬上限保护。排队及执行中的调用总数上限为 `concurrency`。
操作数是 ES 文档操作数，不是 Event 数；同一 Event 的依赖写入不能预先入队。
单批至多占用一半调用方，给其他阶段留出流水线余量；执行上限不会超过调用方数量。
等待预算按每项 1ms 取值，这不是 ES 执行时间估计，也不是在线自适应控制。
单批封顶 128 项/128ms。压测表明，继续随高并发放大到 192～256 项会让更多依赖步骤
等待同一个响应，并放大客户端解析、GC 和调度延迟；因此高并发只增加调用/在途预算，
不再继续扩大单个物理批次。
低流量仍可能由等待期限触发部分批次，实际并行度、吞吐和最优值取决于负载与 ES 服务时间。

| Lifecycle 并发 | 操作数上限 | 写最大等待 | 读最大等待 | 执行并发上限 |
| --- | ---: | ---: | ---: | ---: |
| 1 | 1 | 0ms | 0ms | 1 |
| 8 | 4 | 4ms | 4ms | 8 |
| 32 | 16 | 16ms | 10ms | 32 |
| 64 | 32 | 32ms | 10ms | 32 |
| 256 | 128 | 128ms | 10ms | 32 |
| 384 | 128 | 128ms | 10ms | 32 |
| 512 | 128 | 128ms | 10ms | 32 |
| 1024 | 128 | 128ms | 10ms | 32 |

旧的 `max_operations`、`wait_milliseconds`、`max_concurrent_batches` YAML 键已删除，
严格解析会拒绝它们；应移除这些键，只配置 `lifecycle.concurrency`。
`read_wait_milliseconds` 同样是派生字段，不接受 YAML 手动配置；Console 生效配置展示上述四项值。
当前不是热更新：调整并发后需重启。
调用取消不会取消其他调用已经发送的批次；该调用不继续 ACK，按可能部分成功重试。
关闭时取消未完成批次并等待 worker 退出后关闭连接池。设 `enabled: false` 可做同并发对照。

配置是严格单文档 YAML。未知字段、重复来源 ID、重复 Kafka subscription、无效 Cleaner、fingerprint
路径和 severity 引用都会使进程启动失败。

仓库跟踪的 `configs/linkd.yaml` 和 `configs/linkd.pm2.yaml` 只提供无凭据示例。本机 MySQL、Redis、
Elasticsearch 或 Kafka 需要认证时，复制一份本地配置并仅在副本中填写凭据：

```bash
cp ./configs/linkd.yaml ./configs/linkd.local.yaml
chmod 600 ./configs/linkd.local.yaml
go run ./cmd/linkd config validate --config ./configs/linkd.local.yaml
```

`configs/*.local.yaml` 和 `.env*` 已被 Git 忽略。普通命令使用 `--config` 选择本地文件；PM2 和 Console
也可以通过 `LINKD_CONFIG` 使用同一份 YAML。当前 Linkd 进程不提供逐项环境变量或 secret-file 覆盖，
生产部署必须把配置文件作为受权限保护的 Secret 挂载，且不得提交包含凭据的副本。

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
    enrich:
      processors:
        - { type: strategy }
        - { type: resource }
        - { type: display }
        - { type: metric }
        - { type: source }
    storage:
      type: kafka
      kafka:
        brokers: [127.0.0.1:9092]
        topic: linkd-raw-source-a
        consumer_group: linkd-source-a
        security: { protocol: plaintext }
```

`event_sources[].enrich.processors` 是新 Alert 创建前的有序丰富链。当前注册名为
`strategy/resource/display/metric/source`；空列表输出 `{"status":"succeeded","processors":[]}`。
重复类型和空 type 在来源配置校验时拒绝；未知处理器或缺少所需数据源配置会使对应
Lifecycle 任务启动失败。路由按任务固定的 Release 创建，来源发布后通过停止确认与重新调度生效，
不从启动 YAML 装配路由。停用来源按调度协议停止任务，积压保留。

丰富数据源使用 `lifecycle.datasources` 中静态配置的 MySQL 与 OneModel Elasticsearch 连接。
进程启动时打开已配置的数据源并供各来源任务共享，新增来源可以直接使用这些连接；修改数据源
连接配置需要重启进程。真实依赖的联调结果需单独验证，普通单元测试使用 mock 不代表生产链路已验证。

顶层 `cleaner` 是每条 EventSource Flow 的默认预算；`event_sources[].cleaner.runtime` 只覆盖非零
字段。每条 Flow 内共享清洗 worker pool，但 Event 持久化、Mailbox 入队和原消息确认始终按 lane 独立推进，
`max_concurrent_batches` 只限制同时执行副作用的 lane 数量，不启用跨 lane 合批。

Elasticsearch HTTP 连接池不增加独立 YAML。每个进程按职责并发预算派生 4～1024 个每节点连接上限：
Cleaner 使用各 enabled 来源有效 `max_concurrent_batches` 总和加 2，Lifecycle 使用 `concurrency + 4`，
Control Plane 使用 `archive_worker_count + 4`。每个 Repository runtime 拥有独立连接池，关闭时不会影响
同进程其他职责。

未声明 severity 或 levels 为空时整体使用默认表。自定义表整体替换默认表；name 和 priority 必须唯一，
priority 越小越严重。EventSource mapping/default 的目标必须存在于全局表。来源值先匹配 mapping；未命中
但已经是全局 name 时直接使用，否则依次使用来源和全局 default_severity。

fingerprint_mode=field 默认读取 source_alert_id，目标必须是非空字符串且不超过 128 bytes；fields 模式
接受 1–32 个稳定路径并按路径排序、保留标量类型计算 SHA-256。允许
`source_alert_id/condition_key/subject_system/subject_type/subject_id/dimensions.<key>`，缺失维度直接拒绝。

EventSource 各字段的职责、fingerprint/Severity 规则和“一来源一 Flow”边界见
[EventSource 文档](../modules/event-source.md)。

Redis 支持直连单节点和 Sentinel 两种发现模式。`mode` 省略时默认为 `standalone`，继续使用单个
`address`：

```yaml
storage:
  redis:
    mode: standalone
    address: redis.example.com:6379
    username: linkd
    password: redis-data-secret
    database: 0
```

Sentinel 模式必须省略 `address`，提供 master 名称和至少一个 Sentinel seed：

```yaml
storage:
  redis:
    mode: sentinel
    username: linkd
    password: redis-data-secret
    database: 0
    sentinel:
      master_name: linkd-master
      addresses:
        - sentinel-a.example.com:26379
        - sentinel-b.example.com:26379
        - sentinel-c.example.com:26379
      username: sentinel-user
      password: sentinel-secret
```

顶层 `username/password` 用于 Sentinel 返回的数据节点，`sentinel.username/password` 只用于 Sentinel
自身认证；两组认证都可按服务端配置省略。Cleaner、Lifecycle、Redis Stream Session 和 Control Plane
统一通过这些 seed 发现并跟随当前 master。所有读写都指向 master，不把只读命令路由到 replica，避免
Mailbox、lease 和 Recent Alert 缓存出现读写不一致。Sentinel 负责故障发现和切换，但不能替代 Redis
持久化、复制和备份；`config print` 会同时隐藏数据节点和 Sentinel 密码。

开发环境可通过 `LINKD_TEST_REDIS_SENTINEL_ADDRESSES`（逗号分隔）和
`LINKD_TEST_REDIS_SENTINEL_MASTER_NAME` 启用 Sentinel 发现集成测试；需要认证时分别补充
`LINKD_TEST_REDIS_USERNAME/PASSWORD` 和 `LINKD_TEST_REDIS_SENTINEL_USERNAME/PASSWORD`。

Lifecycle 使用单 Redis List Mailbox 保存待处理 Event ID。默认 `key_prefix=linkd:lifecycle:mailbox`、单
Mailbox 上限 128、单次持锁最多排空 128 条。Signal Stream/Group 默认为
`linkd:lifecycle:signals` / `linkd-lifecycle`。Signal payload 使用独立的 `schema_version` 校验；代码
不提供其他字段名或 namespace 的兼容读取。

`lifecycle.signal.max_batch_messages` 默认 64，只限制单次 Redis Stream 读取和确认批次；
`max_inflight_messages` 才限制从接收到安全确认之间的总在途 Signal。后者省略时按
`max(2 × lifecycle.concurrency, max_batch_messages)` 派生，允许用两个并发窗口覆盖 I/O 等待，
但不会因提高 Handler 并发而保留四倍消息。重试预算继续包含在同一在途上限内，默认不超过
`min(lifecycle.concurrency, max_inflight_messages / 2)`。

Cleaner 同一 lane 的已落库前缀通过有界 Redis 脚本批量入 Mailbox，每次最多 128 项且参数预算
不超过 1 MiB，超过预算顺序切片，不新增凑批等待。每项仍只在 Mailbox 从空变为非空时生成 Signal；
首个失败之后不执行后项，已成功切片保留结果。传输失败时当前切片结果未知，重投允许重复引用，
依赖 Lifecycle 终态短路收敛；不允许以批量 Pipeline 越过失败项执行后项。Stream 当前仍按已有配置共享，
本次不按 EventSource 拆分。Redis 脚本没有事务回滚，Signal 先于 List 追加，异常至多留下空唤醒，
不能将已经入队的 Event 留在没有 Signal 的 Mailbox 中。

Signal 仅在 Handler 安全完成后确认。Redis 按消息 ID 独立确认，同 lane 已完成项可以合入
尚未发送的确认批次，上限复用 `lifecycle.signal.max_batch_messages`（默认 64）；
不新增凑批等待，通道空闲立即发送。确认成功后才释放在途名额，失败保持原批重试；
已发送或等待重试的批次不再扩大。Kafka 的连续 offset 前缀确认不受影响。

选择 Elasticsearch Repository 时，Lifecycle 还会从 Mailbox prefix 派生
`<key_prefix>:recent-alert` namespace，保存最近 Alert 写入。缓存 TTL 没有独立配置项，始终等于
Active Alert `refresh_interval + 5s`，默认为 10 秒。Redis 读写错误不会回落到可能尚未 refresh 的
Elasticsearch，而是保留 Event 重试。生产 Redis 应使用 `noeviction`，避免 current/ended key 被提前
淘汰。MySQL Repository 不创建这些 key。

Cleaner 对目标 Signal Group 启用近似全局背压：默认每秒最多执行一次 `XINFO GROUPS`，查询超时
1 秒。省略水位时，高水位按 Lifecycle 在途上限的 4 倍派生，低水位按 2 倍派生；默认并发 32 时
分别为 256 和 128，压测配置并发 256、在途 512 时分别为 2048 和 1024。达到高水位暂停新 Kafka
fetch，降到低水位恢复，中间区间保持原状态。要求 `0 < low_watermark < high_watermark`、TTL 为
1～60 秒且查询超时不大于 TTL。查询失败或 lag 未知时保持上次准入状态，明确缺少 Group 时暂停，
避免短暂观测故障解除已经生效的背压。该水位统计的是 `lag + pending` Signal，并不等同于精确的
Mailbox Event 数量；容量判断必须同时观察 Mailbox 深度。

控制面可分别配置 Elasticsearch 三项管理任务的周期，以及 Redis Signal Stream 的指标采集和安全裁剪：

```yaml
control_plane:
  elasticsearch:
    schema_and_active_reconcile_interval_seconds: 3600
    bucket_reconcile_interval_seconds: 21600
    archive_interval_seconds: 5
    archive_batch_size: 1000
    archive_worker_count: 1
  redis_stream:
    reconcile_interval_seconds: 10
    operation_timeout_seconds: 3
    max_entries: 100000
    trim_batch_size: 10000
    max_trim_entries_per_cycle: 100000
```

`max_entries` 是软上限。只有 Stream 超过该值时才启动裁剪；`trim_batch_size` 限制单条
Redis 命令的删除量，`max_trim_entries_per_cycle` 限制单轮累计删除量。后者省略时默认为
`10 × trim_batch_size`，且必须不小于单批、不超过单批的 100 倍。每批后控制面都重新读取全部
Consumer Group 的 `last-delivered-id` 和最老 PEL ID，直到回落到软上限、达到单轮预算、
没有删除进展或无法证明新边界安全。任务只删除所有 Group 都已经确认的连续前缀；
未读或 Pending Signal 即使使 Stream 暂时超过上限也会保留。
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

`lifecycle.datasources.alarm_source` 配置 Kingeye MySQL 连接，source Processor 使用 GORM 按 `bk_tenant_id + EventSourceID` 查询 `alarm_collect_alarmsource.name`。

`lifecycle.datasources.onemodel` 配置 Resource Processor 使用的 OneModel Elasticsearch 读连接。`index_prefix` 使用 Kingeye ES 前缀；例如 `bk_monitor_base_` 会查询 `bk_monitor_base_cmdb_instance`，K8s、APM 和云模型由 OneModel Client 路由到对应固定实例索引。该连接独立于 `storage.elasticsearch`，两段配置可以指向同一集群。

`config print` 会隐藏 MySQL、Redis、Elasticsearch 和 Kafka 认证信息。进程运行期间 EventSource 和
Severity 配置冻结；修改配置需要重启进程。

`cleaner`、`lifecycle`、`control-plane` 和 `all-in-one` 都会初始化自己的 telemetry runtime。启用
Prometheus exporter 后，每个进程分别暴露 `/metrics`；部署在独立 Pod 时可以使用相同端口，共享宿主
网络时则需要使用不同配置文件设置不冲突的 `listen_address`。Redis Stream 管理指标由执行任务的
`control-plane` 或 `all-in-one` endpoint 暴露，指标名、单位和告警含义见[可观测性设计](../design/observability.md)。

## 中间件版本要求

### Redis

部署建议使用 Redis 7.2 系列。当前没有覆盖所有版本的兼容测试矩阵；命令层面的最低要求为
Redis 6.2，但这不代表 Redis 6.2 已完整支持。以下结论核对于 2026-09-09：

| Redis 版本 | 当前支持与验证边界 |
| --- | --- |
| 低于 6.2 | 不满足当前 Stream 命令要求，不支持部署。 |
| 6.2 | 具备所需的 Stream 命令，但缺少部分监控字段，未完成完整链路验证，不建议部署。 |
| 7.0 | 从当前代码及命令要求判断具备所需功能，尚无该版本的专项验证记录。 |
| 7.2.16 | 已有真实业务链路及多进程任务调度验证记录；7.2 系列作为当前部署建议基线。 |
| 8.0.0 | 已有 Redis 协议集成测试记录，覆盖范围不等同于完整业务链路验证。 |
| 其他版本 | 未逐一验证，不因版本号更高而自动承诺兼容。 |

版本下限来自 [XAUTOCLAIM](https://redis.io/docs/latest/commands/xautoclaim/) 和
[XTRIM 的 MINID/LIMIT 选项](https://redis.io/docs/latest/commands/xtrim/)，两者均从 Redis 6.2 开始提供。
消费积压 `lag` 与累计入流 `entries-added` 等监控字段从 Redis 7.0 才提供，见
[XINFO GROUPS](https://redis.io/docs/latest/commands/xinfo-groups/) 和
[XINFO STREAM](https://redis.io/docs/latest/commands/xinfo-stream/)。在 Redis 6.2 上，当前客户端与指标处理
可能把缺失字段显示为 `0`，不能据此判断没有积压。

已验证版本及具体场景见 [2026-09-07 核心任务调度验证记录](../reviews/2026-09-07-task-scheduling-validation.md)。
这些记录对应当时的代码与测试范围，不代表当前所有功能已在每个版本上重新验证。

连接形态支持直连单节点和 Sentinel 主从高可用；当前不支持原生 Redis Cluster 分片集群。
Sentinel 模式仍连接 master 进行读写，不能替代数据节点的持久化、复制和备份。

### Elasticsearch

**Elasticsearch 最低部署版本为 7.10，低于 7.10 不在支持范围内。** 此下限是部署要求，
不表示所有 7.10 及以上版本都已通过兼容性验证，也不构成对后续大版本的自动兼容承诺。
已有 [核心任务调度验证记录](../reviews/2026-09-07-task-scheduling-validation.md) 使用 Elasticsearch 7.17.7；
该记录不能替代 Elasticsearch 7.10 或其他版本的专项验证。

## Elasticsearch schema 重建

`storage.elasticsearch.number_of_shards` 可选，范围 1～1024，仅设置新建索引的主分片数；
省略时使用 ES 默认值。修改该配置不会重分片已有索引，已有数据应另行设计 reindex 或 split，
不能把模板更新当作数据迁移。双节点实验使用两个主分片、零副本，并核对各主分片实际分布。

当前 Elasticsearch schema version 为 3，Event、Alert 和 AlertLog 的完整稳定领域字段直接保存在
`_source` 根层。Event、AlertHistory、AlertLog 默认使用 7 天 UTC 时间桶；Active Alert 使用单一热索引，
模板和控制面对账会把 Active Alert 的 `refresh_interval` 设为
`storage.elasticsearch.active_alert_refresh_interval_seconds`，默认 5 秒。Event、Alert History 和 AlertLog
时间桶模板的 `refresh_interval` 由 `storage.elasticsearch.refresh_interval_seconds` 配置，同样默认 5 秒，
用于减少持续写入时的 refresh 开销；这些对象通过搜索读取时需要接受该近实时可见性窗口。Recent Alert 缓存 TTL 自动为 Active 配置加 5 秒，
为缓存过期后的查询回源提供可见性边界。可配置范围为 1～3600 秒，修改后需由控制面对账已有
Active 索引。
`control-plane` 当前分别装配 Elasticsearch Schema 与 Active 资源对账、时间桶维护和终态 Alert 归档任务。三项任务共享
连接和进程级监督；前两项按独立周期执行，归档按连续批量循环执行。数据进程遇到缺失 write alias 时直接失败。这些任务没有独立
启动 command。

时间桶可分别配置：

```yaml
storage:
  elasticsearch:
    # 单节点本地环境可设为 0；生产环境按节点拓扑和容灾要求设置或省略。
    number_of_replicas: 0
    refresh_interval_seconds: 5
    active_alert_refresh_interval_seconds: 5
    alert_log_translog_durability: async
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
1000 条、1 个 Worker，配置范围分别为 1～10000 和 1～64，且 Worker 数不得超过批量上限。单 Worker
用于降低归档 Bulk 与 Lifecycle 实时读写争抢；一次 sweep 内仍会连续翻页追赶积压，因此并不把整轮吞吐
限制为每 5 秒 1000 条。每个 Worker
使用 Bulk create History 和 Bulk CAS delete Active，单个 Bulk 子批最多 500 条并受 Repository 16 MiB
请求上限约束。History create 和 Active delete 使用 `refresh=false`，归档成功不代表搜索立即可见；
409 冲突通过 realtime `_mget` 核对。`archive_interval_seconds` 是一次完整 sweep 到达尾部或请求失败后的
等待时间，默认 5 秒；有后续页时批次连续执行，不等待该间隔。这避免 Active 删除在下一次 refresh 前被立即重复
扫描。单次搜索仍受 64 MiB 响应上限保护，超过时会自动缩小本轮拉取数量。

独立 `control-plane` 启动时会先按“Schema 与 Active 资源对账、时间桶对账”的顺序完成一次准备，再启动管理任务；
归档不阻塞数据面启动。
选择 Elasticsearch Repository 时三项任务自动启用；省略 `control_plane.elasticsearch` 时使用上述默认值，
显式配置该段只调整任务周期、归档批次和并发度，不改变任务所有权。

`number_of_replicas` 未配置时，Linkd 不覆盖 Elasticsearch 的模板默认值。显式配置后，`control-plane`
或 `all-in-one` 会把该值写入 Linkd 模板，之后新建的物理索引会继承该配置；已有索引不会随配置变更
被自动修改。单节点本地环境设为 `0` 可以避免因无法分配副本而长期处于 `yellow`，但这也意味着没有
副本冗余，不应直接照搬到生产环境。已有索引如需调整，应由操作者明确选择目标后通过动态 index
settings 原地修改，不需要删除索引或 reindex。

`alert_log_translog_durability` 只作用于 AlertLog 索引，支持 `request` 和 `async`，默认 `async`；Event、
Active Alert 和 Alert History 仍使用 Elasticsearch 的 `request` 默认值。`async` 会减少每次 AlertLog
写请求等待 translog `fsync` 的成本，但 Elasticsearch 或宿主异常退出时，最近一个
`index.translog.sync_interval`（Elasticsearch 默认 5 秒）内已经返回成功的 AlertLog 可能丢失。
模板负责新索引，Bucket Manager 会把当前预创建窗口内既有 AlertLog bucket 动态对账到该值。

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
7. 检查 Console Elasticsearch 拓扑后恢复上游流量。

删除操作不可恢复。本仓库不提供旧 schema 的 reindex、双读或双写逻辑，也不会在启动时替操作者执行
删除。本轮不自动执行 retention；达到 `max_buckets_per_entity` 时 Bucket Manager 会失败并要求人工处理。

## 动态 EventSource 与调度

部署前可使用 `linkd storage migrate --config <path> --timeout 4m` 检查控制面配置并执行一次性
基础存储初始化。该命令有写入副作用，不等同于只读的 `config validate`；不会自动导入来源或
重建调度历史。Helm Job 的执行、等待与清理规则见 [Helm 部署指南](helm.md#初始化-job-与等待方式)。

Helm 中每个 worker 组可以通过 `LINKD_WORKER_LABELS` 注入 JSON 字符串映射，完整替换 YAML 的
`worker.labels`；变量未设置时保留 YAML 标签，`{}` 清空标签。输入限制为 16384 字节，继续遵守
最多 32 个标签、键最多 128 字节、值最多 256 字节的校验。详见 [Helm 部署指南](helm.md)。

来源运行配置以 ES/MySQL 中的 Record/Release 为准。文件中的 `event_sources` 只供显式导入和本地模拟器使用，常驻进程不会自动导入。

```yaml
dispatch:
  deployment: default
  listen: 127.0.0.1:8090
  url: http://127.0.0.1:8090
  max_tasks: 16
worker:
  labels:
    pool: ingestion
  require_explicit_selector: false
  max_concurrency: 128
  max_inflight_bytes: 268435456
```

`LINKD_API_TOKEN` 与 `LINKD_WORKER_TOKEN` 必须显式设置且不同；也可在受保护的本地 YAML 中配置 `dispatch.api_token` 和 `dispatch.worker_token`。
`LINKD_CONTROL_PLANE_URL` 可覆盖 worker/CLI 的控制面地址。不要把 token 提交到仓库。

```bash
# 先运行控制面或 all-in-one，再在另一个终端显式导入。
linkd event-source import --config configs/linkd.local.yaml --file configs/linkd.local.yaml
```

来源新增 `scheduling.cleaner` / `scheduling.lifecycle`，各含 `replicas`（默认 all，或非负整数）与 `selector`（精确 AND 标签）。
0 停止对应角色，enabled=false 停止整个来源。Cleaner 有 Kafka partition 上限，Lifecycle 无此上限。
同一进程同源同角色最多一个 Flow；all-in-one 的两种角色允许共存。资源上限作用于整个 worker，不随来源数量无限增长。

`lifecycle.signal.stream` 现在作为来源 Stream 的基础前缀；最终键由 deployment/EventSource 稳定派生。
不要手工在前缀中加入 Release 或 worker ID。背压、Mailbox、lease 和 Stream 诊断均使用同一派生规则。

详细 API 见[动态来源设计](../design/event-source-dynamic-configuration.md)，恢复与容器关闭预算见[调度协议](../design/task-scheduling-protocol.md)。

## EventSource hooks

输出配置属于每个 EventSource，随 Record/Release 发布。旧 `lifecycle.output` 已移除，加载时拒绝；
将原 Kafka 参数放到需要输出的每个来源的 `hooks[].config` 中，再按现有导入/API/provider 发布流程生效。
空列表或省略 `hooks` 不执行输出。以下内容位于单个 EventSource 下，可自由删减或增加具名实例：

```yaml
hooks:
  - name: alert-kafka
    type: kafka
    config:
      brokers: [127.0.0.1:9092]
      topic: linkd-alerts
      client_id: linkd-alert-output
      max_message_bytes: 1048576
      security:
        protocol: plaintext
  - name: active-by-strategy
    type: active-alert-by-strategy
    config:
      redis:
        mode: standalone
        address: 127.0.0.1:6379
        database: 0
      key_prefix: linkd:active-alert-by-strategy
      timeout_milliseconds: 1000
```

实例名在来源内唯一，由 1～64 个字母、数字、下划线或连字符组成，首字符为字母或数字。
每个来源至多 16 个 hook，未知类型和错误参数在发布前拒绝。
Redis 连接参数独立于 `storage.redis`，同样支持 Sentinel 及两侧认证；密码在管理展示中脱敏。
`key_prefix` 必填，不自动添加来源 ID；`timeout_milliseconds` 为正整数毫秒，默认 1000，
可使用 100、500 等值，禁止零、负值、null、Duration 溢出及旧秒级字段。
运行时输出 Redis 不可达不阻止来源装配，每次实际调用按超时记录失败。

集合成员和失败边界以 [Lifecycle 插件行为](../modules/lifecycle.md#23-enricher-与-finalhook) 为准。
接入示例、Redis 查询和排障见 [Redis 策略活跃告警 hook](active-alert-by-strategy.md)。
Console Kafka 页面按来源和实例展示输出目标；动态管理接口中的连接凭据已脱敏，因此该模式只展示
目标声明，不使用脱敏凭据探测输出集群 metadata，也不将其标记为已验证可用。
