# Lifecycle 与 Elasticsearch 吞吐压测

## 1. 摘要

本报告记录 2026-09-04 在单机开发环境中，对以下性能改造进行的基线、阶梯升压和临界档长稳测试：

- Elasticsearch Repository 使用按运行角色隔离并限制容量的 HTTP 连接池；
- Lifecycle 正常路径复用 Scheduler 已读取的 `StoredEvent`，只执行一次 Event realtime GET；
- Elasticsearch Event 单条和批量创建使用 `refresh=false`。

本轮已确认当前完整 all-in-one 链路可持续处理约 **440 Events/s**，600 Events/s 可以短时达到，
但运行约 3.5 分钟后会因 Alert Archiver 持续满载而发生 Signal 积压。800 Events/s 明确超过当前容量。
由于 460 Events/s 精细档按人工指令提前终止，本轮只把 440 Events/s 作为已验证上界，不把
440～600 Events/s 之间未经完成的点写成确定结论。

本报告是带环境边界的实验记录，不是生产 SLA。生产部署应按 300～350 Events/s/实例保留
20%～30% 余量，并先处理 Alert Archiver 的 `refresh=wait_for` 和 Redis Stream 裁剪能力不足。

## 2. 时间与源码基线

### 2.1 压测时间

所有时间均为北京时间 `Asia/Shanghai`（UTC+8）。

| 阶段 | 开始时间 | 结束时间 | 用途 |
| --- | --- | --- | --- |
| 低负载 30 分钟基线 | 2026-09-04 15:11:40 | 2026-09-04 15:42:05 | 验证正常路径 Event GET、写入延迟、连接复用和长期错误率 |
| 第一轮阶梯升压 | 2026-09-04 16:18:22 | 2026-09-04 16:29:44 | 约 200、400、600、800 Events/s |
| 680 Events/s 候选档 | 2026-09-04 16:30:32 | 2026-09-04 16:31:43 | 受前一档超载后的 ES/归档状态影响，结果不采信 |
| 干净环境 600 Events/s 长稳 | 2026-09-04 16:33:07 | 2026-09-04 16:37:45 | 验证短时 600 Events/s 能否长期保持 |
| 干净环境 440 Events/s 长稳 | 2026-09-04 16:39:15 | 2026-09-04 16:45:29 | 验证实时链路、归档和裁剪的完整稳态 |
| 460 Events/s 候选档 | 2026-09-04 16:46:57 | 2026-09-04 16:47:08 | 人工提前终止，结果不采信 |

### 2.2 源码与二进制身份

压测发生时，性能改造尚在工作区中，因此必须区分二进制内嵌的 Git 基线和压测后形成的提交：

| 项目 | 值 |
| --- | --- |
| 压测二进制内嵌 VCS revision | `b418d7916a89831302d5f42eefa6ad067ba778d7` |
| 压测二进制 VCS 状态 | `vcs.modified=true` |
| 压测二进制构建时间 | 2026-09-04 15:10:56 +0800 |
| 压测二进制 SHA-256 | `111dc5980bf9c8f3e4821ab3b6d2d5ae075d8ece8eec768f0eb5a15e7ac96061` |
| 性能改造落库提交 | `e9541f18d6299561c6ef9b51ce08ce4e4ffd8755` |
| 提交摘要 | `refactor(storage): 优化 ES 写入与生命周期读取` |
| 后续 HEAD | `2fc25238617417643a749f0c80b276c10536e7f6` |

`e9541f1` 同时包含连接池、单次 Event GET 和 Event `refresh=false` 三项被测改造。后续
`2fc2523` 只增加 DevTools 指标计算窗口，不改变本轮被测 Go 数据路径。

## 3. 测试平台

### 3.1 宿主机

| 项目 | 配置 |
| --- | --- |
| 操作系统 | macOS 26.6.2，Build 25G83 |
| 架构 | arm64 |
| CPU | Apple M4 Pro，14 个逻辑核心 |
| 内存 | 48 GiB |
| 容器运行时 | Apple Container |
| Go | 1.26.7 |
| Node.js | 24.18.0 |
| PM2 | 7.0.3 |

宿主机是共享开发机，不是独占生产节点。压测可用于比较代码路径、识别瓶颈和给出单节点容量下界，
不能直接外推为生产集群吞吐。

### 3.2 基础组件

| 组件 | 版本 | 容器资源 | 压测用途 |
| --- | --- | --- | --- |
| Elasticsearch | 7.17.7，Lucene 8.11.1，JVM 19 | 2 vCPU / 2 GiB | Event、Alert、AlertLog 和归档存储 |
| Redis | 7.2.16 | 1 vCPU / 512 MiB | Mailbox、Signal、lock 和 Recent Alert Cache |
| Kafka | 3.9.1 | 2 vCPU / 1 GiB | 两路 EventSource 输入和 Alert 输出 |
| Prometheus | 3.14.0 | 4 vCPU / 1 GiB | Linkd 指标采集和区间查询 |
| MySQL | 8.4.10 | 2 vCPU / 1 GiB | 未进入本轮吞吐链路，仅用于此前双后端 E2E |

Elasticsearch 为单节点，Linkd 索引 `number_of_replicas=0`。Redis 使用 `noeviction`，未设置
`maxmemory`，AOF 关闭。Kafka 测试 topic 均为 3 partitions、单副本并使用 plaintext。

## 4. 被测配置

权威 YAML 为 [`configs/linkd.pm2.yaml`](../../../configs/linkd.pm2.yaml)。本节只列出影响吞吐、延迟、
积压和恢复能力的参数。

### 4.1 Cleaner

| 配置 | 值 |
| --- | ---: |
| 全局 `worker_count` | 8 |
| 全局 `max_batch_messages` | 128 |
| `batch_wait_milliseconds` | 20 |
| 全局 `max_concurrent_batches` | 2 |
| `max_inflight_messages` | 512 |
| `max_inflight_bytes` | 16 MiB |
| `process_timeout_seconds` | 30 |
| `retry_max_attempts` | 3 |
| `retry_max_elapsed_seconds` | 120 |

| EventSource | Worker | Batch | Concurrent batches | Kafka partitions |
| --- | ---: | ---: | ---: | ---: |
| `standard-infra` | 4 | 64 | 1 | 3 |
| `standard-service` | 8 | 128 | 2 | 3 |

Cleaner Elasticsearch 每地址连接预算为 `1 + 2 + 2 = 5`：两个启用来源的有效
`max_concurrent_batches` 之和再加 2 个余量。

### 4.2 Lifecycle

| 配置 | 值 |
| --- | ---: |
| `concurrency` | 8 |
| `process_timeout_seconds` | 30 |
| `retry_max_attempts` | 3 |
| `retry_max_elapsed_seconds` | 120 |
| Signal `max_batch_messages` | 128 |
| Mailbox `max_pending` | 128 |
| Mailbox `max_drain_events` | 512 |
| 背压 high / low watermark | 100,000 / 80,000 |
| 背压缓存 / 查询超时 | 3 秒 / 1 秒 |
| Fingerprint lock TTL / renew | 60 秒 / 20 秒 |
| Elasticsearch 每地址连接预算 | 12 |

Elasticsearch Active Alert `refresh_interval=5s`，Recent Alert Cache TTL 为 `5s + 5s = 10s`。
Event 和 Lifecycle Alert 写入使用 `refresh=false`；Lifecycle 无冲突正常路径只执行一次 Event realtime GET。

### 4.3 Control Plane

| 配置 | 值 |
| --- | ---: |
| Alert archive interval | 30 秒 |
| Alert archive batch size | 1,000 |
| Alert archive worker count | 4 |
| Control Plane Elasticsearch 每地址连接预算 | 8 |
| Stream reconcile interval | 60 秒 |
| Stream operation timeout | 10 秒 |
| Stream max entries | 100,000 |
| Stream trim batch size | 10,000 |

Alert Archiver 当前 History bulk create 和 Active bulk delete 都使用 `refresh=wait_for`，见
[`archive.go`](../../../internal/store/elasticsearch/archive.go)。Redis Stream Manager 每轮最多执行一次
受 `trim_batch_size` 限制的安全裁剪，见
[`redisstream/manager.go`](../../../internal/controlplane/redisstream/manager.go)。

## 5. 流量模型与测试方法

### 5.1 Event 生成模型

两个 `linkd-eventgen` 分别向 `standard-infra` 和 `standard-service` 写入，新增告警速率约按 1:2 分配：

| 参数 | 值 |
| --- | ---: |
| 周期 | 1 秒 |
| 平均生命周期 | 20 个周期 |
| 重复投递比例 | 20% |
| fingerprint | 每个生成告警唯一 |
| Kafka ACK | all ISR，同步有界分块发送 |

稳定后，每个新告警大致产生一次 triggered 和一次 resolved，再叠加 20% 完全相同的重复 delivery：

```text
Events/s ≈ new_alerts_per_minute × 2 × 1.2 ÷ 60
         ≈ new_alerts_per_minute × 0.04
```

因此 10,000 个新告警/min 在本模型下约等于 400 Events/s。这个换算只适用于本次生命周期和重复率，
生产容量必须使用真实 triggered、updated、resolved、closed 和重复投递比例重新计算。

### 5.2 干净环境步骤

临界档测试前执行以下隔离步骤：

1. 停止 Linkd 和生成器，不再接收新输入；
2. 删除精确匹配 `linkd-pm2-*` 的 Elasticsearch 数据索引；
3. 删除精确匹配 `linkd:pm2:*` 的 Redis key；
4. 删除并按 3 partitions、单副本重建三个压测 Kafka topic；
5. 重启 Elasticsearch 并等待集群 green；
6. 启动 all-in-one，确认 Linkd 索引文档数为 0、Signal `pending=0 && lag=0`；
7. 启动带独立 run ID 的生成器；
8. 每档结束后停止输入，并等待 Kafka 与 Signal 排空。

### 5.3 观测和判定

数据来自 Prometheus、Redis `XINFO`、Kafka consumer group、Elasticsearch node stats 和 PM2。

一个档位只有同时满足以下条件才视为可持续：

- Lifecycle process rate 长期不低于 Cleaner mailbox enqueue rate；
- Kafka lag 和 Signal lag 不持续增长；
- Repository failure、retry 和 ES write rejected 为 0；
- Alert Archiver 能把 terminal Alert 从 Active 索引完整迁移到 History；
- 测试停止后 Kafka、Signal 和 terminal Alert backlog 能排空。

短时间达到目标速率但随后 lag 发散，只记录为短时峰值，不视为可持续容量。

## 6. 低负载 30 分钟基线

输入为 infra 600、service 1,200 个新告警/min，总计约 72 Events/s。

| 指标 | 结果 |
| --- | ---: |
| Lifecycle 成功处理 | 130,705 |
| Event realtime GET | 130,705 |
| Event GET / processed | 1.000 |
| Event create batch 平均 | 9.48 ms |
| Event create batch P95 / P99 | 44.09 / 49.51 ms |
| Lifecycle Processor 平均 | 8.60 ms |
| Lifecycle P50 / P95 / P99 / P99.9 | 7.43 / 26.33 / 47.62 / 196.10 ms |
| 完整 Handler 每 Event 平均成本 | 11.48 ms |
| Repository failure / CAS conflict / retry items | 0 / 0 / 0 |

Event create batch 相对改造前 1.2008 秒基线下降约 99.2%。Event GET 与 processed 完全相等，说明
无冲突正常路径已经消除 Processor 内的第二次 Event GET。

Elasticsearch HTTP 连接统计：

| 时点 | `current_open` | `total_opened` |
| --- | ---: | ---: |
| 开始 | 6 | 9 |
| 结束 | 15 | 63 |

30 分钟只新增 54 个连接，运行中 `current_open` 基本稳定在 15～16，连接数不再随请求数线性增长。

## 7. 第一轮阶梯升压

| 新告警/min | 理论 Events/s | 实测吞吐 | 主要观测 | 判定 |
| ---: | ---: | ---: | --- | --- |
| 5,000 | 200 | 约 199.1/s | Signal `0/0`；ES CPU 22%；write rejected 0 | 稳定 |
| 10,000 | 400 | 约 397.5/s | Signal 无持续积压；ES CPU 40%；write rejected 0 | 稳定 |
| 15,000 | 600 | 短时约 598.2/s | 短窗口无持续 lag，需长稳复核 | 仅证明短时可达 |
| 20,000 | 800 | 输入 796.5/s，Lifecycle 降至 497.1/s | Signal lag 45 秒内由 1,256 增至 12,025 | 明确超载 |

### 7.1 约 200 Events/s

- Cleaner 与 Lifecycle 都约为 199.1/s；
- Signal `pending=0 && lag=0`；
- Elasticsearch CPU 约 22%，write queue/rejected 为 0/0；
- Lifecycle P99 约 47.7 ms；
- Event create batch P99 约 73.9 ms。

### 7.2 约 400 Events/s

- Cleaner 与 Lifecycle 都约为 397.5/s；
- Kafka 只出现批次级瞬时 lag，Signal 无持续积压；
- Elasticsearch CPU 约 40%，write queue/rejected 为 0/0；
- Lifecycle P95/P99 为 16.0/24.5 ms；
- Event create batch P99 为 78.2 ms。

### 7.3 约 600 Events/s 短窗口

- Cleaner 与 Lifecycle 短时都约为 598.2/s；
- Lifecycle P95/P99/P99.9 为 21.9/24.9/66.4 ms；
- Event create batch P99 为 96.6 ms；
- Elasticsearch CPU 约 56%，write rejected 为 0。

该阶段不足以覆盖 Archiver 的长期影响，因此不能据此认定 600 Events/s 可持续。

### 7.4 约 800 Events/s

- Cleaner 输入达到 796.5/s；
- Lifecycle 下降到 497.1/s；
- Signal lag 在约 45 秒内从 1,256 增至 12,025；
- Kafka 曾出现约 585 条 lag；
- Lifecycle P95/P99/P99.9 为 35.2/66.8/103.3 ms；
- Event create batch P99 为 242.8 ms；
- Elasticsearch CPU 约 57%，write rejected 仍为 0；
- 停止输入后约 30 秒排空 backlog。

CPU 未耗尽且 ES 没有 rejected，但吞吐已经发散，说明只看 ES CPU 和 thread-pool rejected 会漏掉
应用并发等待、refresh 等待和后台任务竞争。

## 8. 干净环境 600 Events/s 长稳

初始约 3 分钟可维持 597～605 Events/s，随后开始持续发散：

| 观测阶段 | Cleaner | Lifecycle | Signal pending/lag | ES CPU / queue / rejected |
| --- | ---: | ---: | ---: | ---: |
| 初始稳态 | 590.5/s | 590.7/s | 127/149 | 52% / 1 / 0 |
| 临界稳态 | 604.9/s | 604.9/s | 128/147 | 56% / 4 / 0 |
| lag 开始扩大 | 602.6/s | 602.6/s | 128/288 | 56% / 0 / 0 |
| 吞吐下降 | 604.2/s | 534.1/s | 128/1,429 | 50% / 0 / 0 |
| 持续下降 | 593.5/s | 508.2/s | 128/3,202 | 46% / 0 / 0 |
| 停止输入前 | — | — | 128/4,518 | — |

停止输入后约 10 秒排空 4,518 条 Signal backlog。该阶段确认 600 Events/s 只是短时峰值。

同期 Alert Archiver：

| 指标 | 结果 |
| --- | ---: |
| 已归档 Alert | 55,540 |
| 成功批次 | 57 |
| 批次平均耗时 | 约 4.95 秒 |
| 最近批次 | 持续 1,000 条满批 |
| 累计任务耗时 | 约 282 秒 |

Archiver 在约 5 分钟窗口中几乎持续运行。批次耗时接近 5 秒 Active Alert refresh interval，和
History create、Active delete 的 `refresh=wait_for` 行为一致。当前归档吞吐约为：

```text
1,000 alerts / 5 seconds ≈ 200 Alerts/s
```

600 Events/s 流量对应约 250 个新告警及最终终结 Alert/s，超过归档能力。Archiver 连续满批占用 ES
后，Lifecycle 吞吐从约 600/s 降到约 500/s，Signal lag 随即发散。

同期 Redis Stream Manager：

- 运行 6 次，总耗时约 10.4 ms；
- 裁剪 20,000 条；
- `entries_added=131,452`；
- Stream 长度仍为 111,452；
- Stream 内存约 64.3 MiB。

裁剪任务本身不是 CPU 或延迟瓶颈，但当前裁剪量不足以抵消 Signal 新增速度。

## 9. 干净环境 440 Events/s 长稳

该阶段持续 6 分 14 秒，覆盖多轮归档与两轮实际 Stream 裁剪。

### 9.1 实时链路

| 指标 | 结果 |
| --- | ---: |
| Lifecycle 成功处理 | 162,962 |
| 全程平均 | 约 435.7 Events/s |
| 稳态窗口 | 436～443 Events/s |
| Lifecycle 平均 | 7.08 ms |
| Lifecycle P95 | 20.52 ms |
| Lifecycle P99 | 37.86 ms |
| Lifecycle P99.9 | 76.67 ms |
| Repository failure | 0 |
| Retry items | 0 |
| ES CPU | 约 45%～52% |
| ES write queue | 0～4 |
| ES write rejected | 0 |
| 结束时 Kafka lag | 0 |
| 结束时 Signal pending/lag | 0/0 |

### 9.2 Alert Archiver

| 指标 | 结果 |
| --- | ---: |
| 已归档 Alert | 65,980 |
| 成功批次 | 77 |
| 批次平均耗时 | 4.92 秒 |
| 结束时 Active Alert | 3,625 |
| Active 中 terminal Alert | 0 |

结束时 Active 索引中只剩真正 active 的 Alert，说明 440 Events/s 下归档仍能追平，但已经接近
约 200 Alerts/s 的归档能力上限，不能把该点直接当作无余量的生产配额。

### 9.3 Redis Stream 裁剪

- 已裁剪 20,000 条；
- 最终 Stream 长度 113,649；
- 推算累计新增 Signal 约 133,649；
- 平均 Signal 新增约 357/s；
- Stream Manager 累计耗时约 35 ms；
- 裁剪发生时没有观察到 Lifecycle 延迟或吞吐突降。

当前理论最大裁剪速度只有：

```text
10,000 / 60 seconds ≈ 166.7 entries/s
```

因此实时链路虽然能处理 440 Events/s，Redis Stream 仍会长期增长。`max_entries=100000` 是触发阈值，
不是严格长度上限。

## 10. 未采信样本

### 10.1 680 Events/s 候选档

输入达到约 631.6/s 时，Lifecycle 为 486.3/s，Signal lag 在约 40 秒内达到 3,014。该档紧随
800 Events/s 超载测试，受到 ES merge、连续归档和遗留 Active 数据影响，没有作为独立容量结论。

### 10.2 460 Events/s 候选档

该档只运行约 11 秒即被人工提前终止，没有进入恢复流和 Archiver 稳态，不作为有效样本。

## 11. 结论

### 11.1 当前容量边界

在本报告的平台、配置和流量模型下：

- 已验证可持续吞吐：约 **440 Events/s**；
- 已验证不可持续吞吐：约 **600 Events/s**；
- 短时峰值：约 **600 Events/s**；
- 800 Events/s 会快速积压；
- 440～600 Events/s 之间的精确拐点尚未完成二分验证；
- 当前首要限制不是 Cleaner 或 HTTP 连接池，而是 Alert Archiver 的 5 秒 refresh wait；
- Redis Stream Manager 不消耗明显算力，但 166.7 entries/s 的裁剪能力无法维持高吞吐下的有界 Stream。

### 11.2 容量规划值

不建议直接使用 440 Events/s 作为生产限额。当前单实例建议：

| 目的 | 建议值 |
| --- | ---: |
| 已验证实验上界 | 440 Events/s |
| 临时入口硬保护线 | 400 Events/s |
| 预留 20%～30% 余量的部署容量 | 300～350 Events/s |

按本次流量模型，400 Events/s 约等于 10,000 个新告警/min。真实部署必须根据实际生命周期事件比例
重新换算，不能只用“新告警数”代替 Event 总量。

## 12. 优化建议

### 12.1 P0：移除 Alert Archiver 的 refresh wait

将 History bulk create 和 Active bulk delete 从 `refresh=wait_for` 改为 `refresh=false`。归档已有确定性
文档 ID、create-only、冲突核对、Active 条件删除和逐项失败隔离，不应把搜索可见性等待放在归档完成路径。

改造应覆盖：

- 未 refresh 时的重复归档幂等；
- History create 409 的 realtime 核对；
- Active 条件删除后的重复扫描收敛；
- History 搜索使用有界可见性等待；
- 归档部分成功、进程退出和重新扫描场景。

只增加 `archive_worker_count` 或连接数不能消除每批约 5 秒的 refresh 等待。

### 12.2 P0：提高 Redis Stream 追赶能力

优先把 Manager 改成在超过 `max_entries` 时连续裁剪多个安全批次，直到满足以下任一条件：

- Stream 回落到目标长度；
- 达到单轮耗时预算；
- 达到单轮最大删除量；
- 无法得到所有 Consumer Group 的安全边界。

若暂时只调整 YAML，使用：

```text
trim_batch_size >= peak_signal_rate × reconcile_interval × safety_factor
```

440 Events/s 档实测约 357 Signals/s。按 1.5 倍余量，候选配置为：

```yaml
control_plane:
  redis_stream:
    reconcile_interval_seconds: 30
    operation_timeout_seconds: 10
    max_entries: 100000
    trim_batch_size: 20000
```

该配置的理论裁剪能力约为 667 entries/s。另一种候选是 10 秒间隔、10,000 条批次，理论能力约
1,000 entries/s。两种配置都需要重新验证 Redis CPU、命令延迟、安全边界和多 Consumer Group 行为。
单独提高 `max_entries` 只能扩大缓冲，不能解决长期增长。

### 12.3 P1：隔离实时链路与归档链路

生产环境优先使用分角色部署，不让 Cleaner、Lifecycle 和 Control Plane 长期共享同一个 all-in-one
进程。Archiver 应拥有独立的进程资源、连接池、限速和故障域。

即使共享同一 Elasticsearch 集群，也建议为 Archiver 增加：

- 每轮最大连续批次数；
- 满批之间的最小间隔；
- 基于 ES write queue、CPU 或实时链路 lag 的动态限速；
- 最大 duty cycle；
- terminal backlog 与归档 ETA 指标。

### 12.4 P1：归档优化后再提高 Lifecycle 并发

当前 Lifecycle concurrency 为 8。现在直接提高到 12 或 16 会在 Archiver 持续满载时增加 ES 竞争。
推荐顺序：

1. 移除 Archiver refresh wait；
2. 提高 Stream trim 能力；
3. 重新运行 30 分钟 400～600 Events/s 长稳；
4. 再比较 Lifecycle concurrency 8、12、16；
5. 依据 P99、Signal lag、ES queue/rejected 和 terminal backlog 选择并发。

Repository 的连接预算会随 Lifecycle concurrency 自动增加 4，不需要新增 YAML 连接池配置。

### 12.5 P2：进一步减少 Active Alert 搜索

低负载 30 分钟基线中 Recent Alert Cache 的 current 命中率约为 19.4%。在归档和裁剪问题解决后，
可以评估 Persistent ActiveHead，减少 `FindActiveAlert`、fingerprint search 和 Active 索引 refresh
可见性依赖。

### 12.6 P2：按恢复时间设计背压

当前 high/low watermark 为 100,000/80,000。600 Events/s 超载时 Signal lag 已在几十秒内增至数千，
但距离 high watermark 很远。建议用目标恢复时间推导阈值：

```text
high_watermark ≈ 可接受恢复秒数 × 实际 drain rate
```

例如希望停止输入后 60 秒内排空，按 500 Events/s drain rate 计算，high watermark 可从约 30,000
开始验证。背压阈值控制的是未处理 Signal，不能替代已 ACK Stream 的裁剪治理。

## 13. 后续部署指导

### 13.1 上线前置条件

1. 从干净 commit 构建并记录制品 SHA-256；
2. 完成 `make check`、Elasticsearch 7.17.7 和 MySQL 8.4.10 双后端 E2E；
3. 实施或至少配置化缓解 Archiver refresh wait；
4. 保证 Stream trim rate 高于峰值 Signal append rate；
5. 完成不少于 30 分钟的目标流量稳态测试；
6. 验证 Kafka、Signal、Mailbox 和 terminal Alert backlog 均能排空；
7. 验证 Redis 使用 `noeviction` 并设置明确容量告警。

### 13.2 推荐启动顺序

分角色部署时建议：

1. 执行 `storage prepare`，准备 Elasticsearch template、index 和 alias；
2. 启动 Control Plane 并完成 schema、Active index refresh interval 和 bucket 对账；
3. 启动 Lifecycle，确认 Redis Signal Consumer Group 存在；
4. 启动 Cleaner；
5. 最后恢复 Kafka 上游流量。

Mailbox 和 Recent Alert Cache 协议发生不兼容变化时，不应混合滚动。升级或回滚前应停止上游并确认：

- 两个 Kafka group lag 为 0；
- Signal `pending=0 && lag=0`；
- 不存在非空 Mailbox；
- Archiver 没有 terminal backlog。

### 13.3 灰度阶梯

单实例建议按以下流量逐级灰度：

```text
150 → 250 → 300 → 350 Events/s
```

每档至少持续 15 分钟，目标档至少持续 30 分钟。只有同时满足以下条件才继续升压：

- Lifecycle process rate 不低于 Cleaner enqueue rate 的 95%；
- Kafka 和 Signal lag 不持续增长；
- Repository failure、retry 和 ES write rejected 为 0；
- Archiver 不持续满批，Active 索引中 terminal Alert 为 0；
- Stream trim rate 不低于 Signal append rate；
- Elasticsearch CPU 建议长期低于 70%；
- Lifecycle P99 满足部署环境的业务 SLO。

### 13.4 必备监控

- Cleaner mailbox enqueue rate 与 Lifecycle process rate 差值；
- Signal lag 的绝对值和增长率；
- Signal pending 是否长期保持一个完整 read batch；
- Archiver `last_batch_items == archive_batch_size` 的连续次数；
- Archiver archived rate、duty cycle 和 terminal backlog；
- Stream `entries_above_max`、append rate、trim rate和内存增长率；
- Elasticsearch HTTP `current_open`/`total_opened`、write queue/rejected、merge 和 refresh；
- Repository failure、retry、CAS conflict；
- Event GET / Lifecycle processed 比例。

## 14. 后续验证矩阵

完成 P0 优化后，建议使用相同数据模型执行：

| 变量 | 候选值 |
| --- | --- |
| Lifecycle concurrency | 8、12、16 |
| Archiver refresh | `wait_for`、`false` |
| Archive batch size | 1,000、2,000 |
| Archive worker count | 4、8 |
| Stream reconcile/trim | 60s/10k、30s/20k、10s/10k |
| 目标 Events/s | 400、500、600、800 |
| 持续时间 | 每档 15 分钟，目标档 30～60 分钟 |

最终容量必须同时满足实时处理、归档追平和 Stream 有界三项条件，不能只以瞬时 Lifecycle throughput
作为验收结果。
