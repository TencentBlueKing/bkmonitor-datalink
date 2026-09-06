# Lifecycle 吞吐优化汇总

日期：2026-09-06。

本文汇总本轮围绕 Alert 归档、Redis Stream、Cleaner、Lifecycle 与 Elasticsearch 请求链路完成的优化、验证结果和配置结论。它是阶段性技术总结，不是生产容量承诺；完整实验数据见[吞吐压测报告](benchmarks/2026-09-04-lifecycle-elasticsearch-throughput.md)，当前执行顺序与一致性边界见[Lifecycle 执行链路审查](2026-09-06-lifecycle-execution-flow.md)。

## 1. 目标与最终结果

本轮目标是让既定告警模型能够持续处理目标 1000 Events/s，同时避免用盲目扩大并发掩盖请求粒度、串行依赖和运行时竞争问题。

最终采用三进程运行，并将本地 Lifecycle 并发从 512 收敛到 256；读写批次参数继续由并发自动派生。相同进程和已有索引连续完成两轮十分钟验证：

| 指标 | 第一轮 | 同进程重复运行 |
| --- | ---: | ---: |
| 实际输入均速 | 981.7/s | 981.0/s |
| Lifecycle 均速 | 980.7/s | 980.8/s |
| 最后两分钟 Lifecycle | 997.8/s | 999.5/s |
| 平均写批量 / 执行耗时 | 103.65 / 5.80ms | 104.05 / 5.98ms |
| 平均读批量 / 执行耗时 | 59.60 / 5.15ms | 59.63 / 5.22ms |
| Repository failure / retry / ES write rejected | 0 / 0 / 0 | 0 / 0 / 0 |

两轮停止输入后，Kafka、Signal 未读与 PEL、Mailbox、ES 未处理 Event 和终态归档积压均归零。十分钟均速包含生成器启动爬坡，因此结果应表述为“能够承接本轮实际输入并在末段稳定接近 1000/s”，不能写成生产环境长期保证精确 1000/s。

## 2. 已完成的代码优化

### 2.1 Alert 归档与 Redis Stream 裁剪

- History create 与 Active CAS delete 使用 `refresh=false`，移除每批固定等待 refresh 的延迟。
- History create 冲突通过 realtime 读取核对内容，继续保持确定性 ID、create-only 和逐项失败隔离。
- 归档按 sweep 连续翻页；到达尾部后才等待下一周期，避免未刷新搜索视图反复扫描相同文档。
- Redis Stream Manager 在一轮内按安全边界连续裁剪，每批后重新检查所有 Group/PEL，并受单轮总量预算限制。
- 默认对账周期收敛到 10 秒；裁剪不会跨过任何 Group 的未读或 Pending entry。

结果是归档不再受约 5 秒的固定批次等待限制，Stream 可以围绕软上限收敛。这两项解决后台任务能力不足，但没有单独解决 Lifecycle 降速。

### 2.2 Lifecycle 四类 ES 写入跨 Event 合批

共享写入器覆盖：

- Event 结果 CAS；
- Alert CAS；
- Alert create；
- AlertLog create。

不同 Event 已就绪的操作可以合入同一物理请求；同一 Event 的依赖步骤仍按顺序等待，不提前执行日志、Event 终态、输出或 ACK。CAS 继续核对租户、身份、预期版本和合法状态转换，并使用 `if_seq_no`/`if_primary_term`；create 冲突继续 realtime 核对内容。429、5xx、404 与版本冲突按项返回，不重放其他成功项。

### 2.3 ES realtime 读取合批

Lifecycle 的 realtime 点读使用独立读队列：达到操作数或字节上限立即发送，未满批最多等待 10ms。读写共享有界物理请求槽位和总调用预算，但不共用收集期限。

本轮最终配置平均约 60 项/读批，避免过去平均每批约 1～2 项、每秒数百次物理读取的情况。

### 2.4 ES 客户端编码与响应处理去重

- 写操作在切批时完成一次编码，并在真正发送时复用。
- Event、Alert、AlertLog 的规范化路径避免对同一动态 JSON 重复进行完全相同的校验。
- Bulk 子项状态、单请求视图和原始 JSON 在第一次解析后保留，分发逻辑不再重复解析。
- 逻辑 Bulk 响应直接复用已经验证的子项，避免再次扫描并重新编码整个子响应集合。

这些修改减少了明确的重复工作，但完整压测表明，单独完成响应复用并没有消除后程降速；它属于必要的成本优化，不是唯一根因修复。

### 2.5 Cleaner Mailbox 批量入队

Cleaner 原来是批次之间并发、批次内部逐条调用 Redis。现在 Mailbox 与必要的 Signal 创建使用有界 Lua 批量操作：

- Mailbox 从空变为非空时才创建 Signal；
- 同一 Mailbox 保持顺序；
- 逐项返回 added/coalesced 结果；
- 遇错停止，只确认已经安全完成的连续前缀；
- 不用普通 pipeline 假装获得中途失败原子性。

该优化减少了批次槽位被串行 Redis 往返长期占用的时间。

### 2.6 Redis Stream 批量确认

Lifecycle 将已经完成的 Signal 使用 Redis 多条 XACK 有界确认。Redis Stream 不要求像 Kafka 一样按 offset 连续提交，但实现仍只确认已经安全完成的条目。

这消除了“处理已经完成，但逐条确认占住大量 inflight，导致新 Handler 无法进入”的明确限速点。批量确认不改变 Mailbox 队首 ACK 与 Signal XACK 的先后边界。

### 2.7 Cleaner 空转与 Kafka 分区尾部恢复

- 修复 Cleaner 在等待批次完成或等待槽位时反复被过期定时器唤醒的问题。隔离测试中，单消息等待期间的快照次数从 356,234 次降至 5 次。
- Kafka `fetch_max_wait_milliseconds` 默认设为 100ms，避免暂停分区恢复后被其他空分区的长 fetch 拖住约 5 秒。

这两项分别解决无效 CPU/GC 开销和单分区尾部恢复缓慢，不应与 ES 写入性能混为一个问题。

### 2.8 可观测性与 DevTools

新增或拆分了以下观测维度：

- 物理批次数、操作数、字节数、实际平均批量和触发原因；
- 每项进入队列到发送前的等待；
- 批次执行、连接获取、请求发送、首字节、响应体读取、整体解析和逐项映射耗时；
- 同一 Bulk 的 ES `took` 与客户端完整执行时间；
- 批量确认大小、耗时、待确认数量和 Handler 在途状态；
- Go runnable 调度延迟、分配速率、GC CPU 与暂停；
- Cleaner 各步骤、Redis/ES/Kafka 状态以及归档和裁剪效果。

DevTools 按“吞吐与积压 → 调度与在途 → ES 请求阶段 → 运行时与基础设施”组织图表，使降速时可以先确定积压环节，再区分服务端执行和客户端等待。

## 3. 配置与部署收敛

### 3.1 代码默认值与本轮验证值

项目默认 Lifecycle 并发仍为 32；256 是本轮本地资源和负载模型验证出的运行值，不是通用默认值。

| 配置 | 项目默认 | 本轮验证值 |
| --- | ---: | ---: |
| Lifecycle concurrency | 32 | 256 |
| 最大写操作数 | `min(128, concurrency/2)` | 128 |
| 写收集期限 | 与最大操作数同值（ms） | 128ms |
| 读收集期限 | `min(10ms, 写期限)` | 10ms |
| 共享物理批次并发 | `min(32, concurrency)` | 32 |
| 单批字节预算 | 4 MiB | 4 MiB |
| Signal inflight | `max(4 × concurrency, receive batch)` | 1024 |
| Lifecycle HTTP 每 ES host 上限 | `clamp(concurrency + 4, 4, 1024)` | 260 |

只需要配置 Lifecycle concurrency 和字节预算；操作数、收集期限和物理并发由代码推导，避免用户把应用并发、ES 连接和写批次配置成互相矛盾的组合。

### 3.2 本轮测试拓扑

- Control Plane、Cleaner、Lifecycle 分为三个进程，各自暴露 metrics。
- ES 7.17.7：单节点，4 CPU / 4 GiB，heap 1 GiB，两个主分片、零副本，数据使用独立 ext4 volume。
- Event、Active、History 使用 request durability；AlertLog 使用 async durability。
- 所有测试索引 refresh interval 为 5s。
- Redis 7.2.16：1 CPU / 1 GiB，自动 RDB、AOF 关闭。

拆进程本身并没有消除降速；它的价值是隔离 Go 堆、调度器和指标，使瓶颈归因更准确。零副本、AlertLog async、AOF 关闭和本地资源规格均是压测条件，不是生产可靠性建议。

## 4. 瓶颈结论

### 4.1 不是已证明的 ES 写盘饱和

降速窗口中曾出现客户端写批次从约 9ms 增长到 30ms 以上，但同批 ES `took` 通常只从约 4～5ms 增长到约 5～6ms；ES write queue、rejection、连接等待和批次执行槽位等待也没有持续堆积。因此不能把完整增量解释为 fsync、SSD 或 ES Bulk 线程池饱和。

### 4.2 主要放大点在客户端并发与依赖链

Lifecycle 一个 Event 包含多步串行读取、CAS、日志、输出和确认。并发从 32 增大到 512 时，写批次也自动增大到 256 项/256ms；大量 Handler 同时停在不同依赖点，并不能形成 32 个持续满载的物理 Bulk，却会扩大在途对象、响应处理和 Go 调度压力。

在 Redis 恢复后，将并发从 512 降到 256，同时让写批次派生为 128 项；当时收集期限为 128ms，分配量仍约 620 MiB/s，但末段指标明显改善：

- 写批次执行约 26.5ms → 7.2ms；
- 逐项响应处理约 7.3ms → 1.0ms；
- 写操作排队约 46.0ms → 14.7ms；
- runnable 调度 P99 约 64ms → 10ms；
- Lifecycle GC 暂停占比约 5% → 1%。

与此同时平均写批量仍约 104 项，没有退化成单条请求。因此关键不是取消合批，而是用更少的在途工作维持足够批量，让每个依赖步骤更快完成。

### 4.3 Redis OOM 是独立故障

原 Redis 512 MiB 在自动 RDB fork 时达到 cgroup 上限：先导致后台保存失败和写入保护，随后主进程被 OOM 杀死。该事故会触发 Cleaner/Lifecycle 失败和重启，相关窗口不能作为参数优劣证据。

恢复时保留旧数据和最后成功 RDB，并重放本轮有界 Kafka 范围，通过现有幂等路径补齐状态；确认所有积压归零后，将 Redis 扩到 1 GiB并加入内存、RDB 和 cgroup OOM 采样。后续两轮没有再次 OOM。

## 5. 验证与边界

已完成：

- `make check`；
- Elasticsearch 7.17.7 集成测试；
- Redis Mailbox 批量入队和 Stream 批量确认集成测试；
- Elasticsearch/MySQL 双后端 all-in-one E2E；
- 两轮相同 1000 Events/s、十分钟验证，第二轮不重启角色且沿用已有索引；
- 停止输入后的 Kafka、Signal、Mailbox、未处理 Event、归档 backlog 和 Stream 收敛核对。

仍需保留的边界：

- 当前生成器每秒集中发布一次，结果包含这种突发形态，但不覆盖所有真实流量分布。
- 两轮只验证十分钟，不等于长期生产容量或配额。
- 当前高基数模型不能代替单个热点 fingerprint/Mailbox 验证。
- CAS 前仍存在可进一步评估的快照回读和完整对象处理；本轮没有以牺牲身份、租户、版本或状态转换校验换取速度。
- 副本、AOF、translog durability、refresh SLO 和跨节点网络必须按生产可靠性重新验证。

本轮结论不是“ES 没有成本”，而是：在当前资源和模型下，合理的客户端并发与批次组合比继续扩大写并发更重要；最终配置通过控制在途规模、保持有效 Bulk 和缩短依赖完成时间，达到了本次 1000 Events/s 验证目标。

后续空索引复验通过 pprof 进一步定位到重复动态 JSON 规范化：Cleaner 的 Mapper 与 Event create、
Lifecycle 的 Event realtime 读取与终态 CAS 会在同一可信调用链重复解码和复制 payload。保留外部
输入与 ES 文档边界校验、移除内部重复后，空索引十分钟验证的末两分钟 Lifecycle 提高到约
1043/s，调度 P99 从约 50ms 回落到约 9ms，Signal 短暂积压可在输入期自行追赶。详细数据见
[压测报告 4.28](benchmarks/2026-09-04-lifecycle-elasticsearch-throughput.md#428-pprof-定位与重复规范化优化)。

随后 Lifecycle Event 点读排除 `source_raw_data`，终态 CAS 改为只更新关联字段和 processing，
并缓存消费指标的固定 AttributeSet。第三组空索引十分钟内 Kafka lag、Signal 未读始终为零，
Lifecycle 与 Cleaner 最终均完成 588,855 条；Lifecycle 末段分配进一步从约 499MiB/s 降至
346MiB/s，GC 暂停占比从 4.42% 降至 1.32%。关键 pprof 图和 top 摘要已归档到
[诊断制品目录](profiles/2026-09-07-lifecycle-gc/README.md)，不包含原始 trace。
