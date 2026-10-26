# Redis 请求、命令与存储容量评估

## 1. 基线与结论

- 评估日期：2026-10-17，时间均为北京时间。
- 源码基线：`c8de40b1ca36ab0904c610ac3d8dad0ce97f4a67`，分支 `feat/linkd-dev`。
- test 运行版本：`0.1.0-test.20261017.redis-qps.1`；Control Plane、Cleaner、Lifecycle 已核对运行提交。
- 场景：Kafka 接入、Elasticsearch Repository、Redis 调度/策略状态、KAC 全局插件及策略活跃索引 Hook。
- Redis：共享 standalone 实例，7.2.7；主状态 DB 0，当前策略活跃索引目标 DB 8。其他服务也使用此实例。
- 本文是带版本、负载和采样时间的容量评估，不是固定性能承诺，也不修改运行配置、保留时间或清理历史数据。

主要结论：

1. 单 Event 没有跨场景通用的 Redis 常数。普通持续触发的 Lifecycle 核心路径约 **10～11 条客户端命令、23～27 条服务端命令**，具体假设见第 3 节；丰富、Signal 消费、策略执行及异步统计另计。
2. 本次 30 秒现场窗口，Lifecycle 进程摊销为 **18.09 条客户端命令、41.72 条服务端命令/Event**。这是包含轮询和异步统计的混合负载观察，不能作为所有环境的常数。
3. 依赖主索引的一个 Lua 请求仍随相关规则数增加内部命令；策略统计每次实际求值都会尝试写 Redis，并非固定频率采样。
4. 控制面中，依赖索引回填、屏蔽复查、策略活跃集合发布、合并成员建联/解除，均存在随告警数或关系成员数增加的 Redis 工作量。
5. 内存不能只按活动告警数估算：Signal 保留记录、48 小时的合并入窗去重记录、30 天的终态依赖水位，都可能成为主要容量项。
6. 当前 21 个 Linkd Signal Stream 的 `MEMORY USAGE` 合计约 **1.03 GiB**；其中旧前缀、无法映射到当前来源的 Stream 约 **920.41 MiB**，其 lag/pending 为零。这是已消费历史保留，不是待处理积压。

## 2. 统计口径

本文使用三个不同量：

| 符号 | 含义 | 示例 |
|---|---|---|
| R | 客户端提交给 Redis 的命令数，即逻辑请求数 | 一次 `EVAL` 算 1；Pipeline 内 32 个 `EVAL` 算 32 |
| C | Redis 服务端执行的命令数，含 Lua 的 `redis.call/pcall` | `EVAL` 内执行 `GET`、`DEL`，合计 3 |
| W | 应用可见的命令批次往返数 | 32 个 `EVAL` 放在一个 Pipeline，通常 W=1；不能据此说 R=1 |

W 不是 TCP 包数：分包、协议握手、重连、失败重试会改变实际网络流量。本文的源码计数默认连接已建立、脚本缓存已热、没有失败重试；`NOSCRIPT` 回退会额外增加客户端命令。

Console 的操作速率来自实例级 `INFO stats.instantaneous_ops_per_sec`，不是所选来源、DB 或 Event 的处理速率。源码见 `console/src/server/redis.ts`（当时源码，独立 Console 已移除）。Redis 服务端的执行计数包含 Lua 内部命令，参见 [Redis 7.2.7 server.c](https://github.com/redis/redis/blob/7.2.7/src/server.c#L3394)。

**C 相同不代表成本相同。** `HGETALL` 读取 10 万个成员仍是一条命令，但 CPU、返回字节和事件循环占用与一个单字段 `HGET` 差别很大。容量规划必须同时记录 R、C、字节数、脚本耗时、客户端等待和积压。

### 2.1 本次在线采样

2026-10-17 21:47:54.957～21:48:24.971，约 30 秒。通过短时 `MONITOR` 按 Pod IP 和 Lua 所属客户端归类，只聚合命令名称和固定 key 类别，不保留告警正文、完整命令参数或凭据。采样结束已关闭 MONITOR。

Lifecycle 成功执行 Mailbox `LPOP` 181 次，即观察到 181 条 Event 出队；窗口边界可能跨过正在处理的 Event，分母包含业务拒绝等正常终结结果，不等同于新建 Alert 数。

| Lifecycle 类别 | R | C | C/Event |
|---|---:|---:|---:|
| 依赖主索引 | 179 | 1,236 | 6.83 |
| Mailbox 读取、确认 | 542 | 904 | 4.99 |
| fingerprint lease | 361 | 723 | 3.99 |
| Recent Alert | 446 | 446 | 2.46 |
| Signal 消费及接管检查 | 917 | 917 | 5.07 |
| 其他策略脚本、统计、Hook、KAC 入队及事务控制 | 830 | 3,326 | 18.38 |
| **合计** | **3,275** | **7,552** | **41.72** |

该窗口约 6.03 Event/s，Lifecycle 为 R≈109.16/s、C≈251.72/s；Cleaner 为 R≈2.57/s、C≈20.57/s；控制面为 R≈32.37/s、C≈45.50/s。全实例约 1,395 C/s，包含 IAM、BCMS、Plugind 等其他应用。

依赖更新样本包含 84 次活动态更新和 95 次资格清理；活动态更新分别涉及 1 或 2 条规则。不能把这个混合均值套到“每次都新建、每次匹配几十条规则”的场景。

Signal 读/接管在空闲来源上也会发生。本窗口有 427 次 `XREADGROUP` 和 395 次 `XAUTOCLAIM`，因此低吞吐时摊到每条 Event 的固定成本明显变大。不能直接用截图的五分钟 Event 速率除另一个短窗口的 Redis QPS。

## 3. 单 Event 主流程计数

### 3.1 普通持续触发的 Lifecycle 核心路径

以下取一个已存在、已放行的 active Alert，一条 Event 只修改一个 Alert；无等级升级、失败、竞争、额外控制变更；一个 Mailbox 本轮只有一条 Event；启用 Recent Alert、KAC 插件和一个策略活跃索引 Hook。

令 D 为本次依赖主资格更新中的**纯字段不匹配规则数**，前一个 revision 也只有这些不匹配标记，没有旧的正向候选。此路径不执行新告警的 clip/aggregation 准入；“持续触发”与“新建告警”不可混算。

| 操作 | R | C | 说明 |
|---|---:|---:|---|
| 获取 lease | 1 | 1 | `SET NX` |
| 读取队首、结束时确认空队列 | 2 | 2 | 两次 `LINDEX` |
| 确认 Event 出队 | 1 | 3 | `EVAL + LINDEX + LPOP` |
| 释放 lease | 1 | 3 | `EVAL + GET + DEL` |
| Recent Alert 查询 | 1 | 1 | `GET` |
| 未命中后 ES 回源并回填 | 0～1 | 0～1 | 回填一个 `SET`；ES 读取不计入 Redis |
| 保存新 Alert 快照后刷新缓存 | 1 | 1 | `SET` |
| 依赖主索引更新 | 1 | D=0 时 6；D≥1 时 7+D | 见第 3.2 节 |
| 策略活跃索引 Hook 提示 | 1 | 通常 5 | 已在 pending：`EVAL + ZSCORE + TIME + HSET + ZADD`；首次入队通常再加 `ZCARD/HLEN` 两条 |
| KAC 轻量变更入队 | 1 | 1 | `XADD`，不是完整 Alert |
| **合计** | **10～11** | **D=0：23～24；D=1：25～26；D=2：26～27** | 不含下述增量项 |

证据：[Scheduler](../../internal/lifecycle/scheduler/handler.go)、[Mailbox](../../internal/lifecycle/mailbox/store.go)、[lease](../../internal/lifecycle/scheduler/lock.go)、[缓存查询及索引更新](../../internal/lifecycle/processor.go)、[保存后缓存](../../internal/lifecycle/plan.go)、[Hook](../../internal/lifecycle/strategyhook/hook.go)、[KAC 入队](../../internal/alertqueue/queue.go)。

需要额外加入：

- **Signal 消费**：每批一次 `XREADGROUP`，确认可合批；还有按来源的空轮询、超时接管和退休 consumer 接管。没有固定的“每条 Event 两次”常数。
- **Mailbox 合并处理**：同一 lease 内处理多条 Event 时，获取/释放 lease 和最终空队列检查可以摊薄；达到 `max_drain_events` 时会让出调度，不能无限摊薄。test 配置为 4，源码缺省为 128。
- **lease 续租**：长任务每次额外 R=1、成功 C=3，通常为 `EVAL + GET + PEXPIRE`；缺省续租间隔 20 秒。
- **丰富读取**：展示缓存每个 hash 字段通常 `HSTRLEN + HGET`，R=C=2；String 读取为 `GETRANGE`，R=C=1。动态分组未命中为一次 `HSTRLEN`，有值再加一次 `HGET`。取决于真实读取的实例、字段及 Processor，不能按 Event 一概写死。见 [展示缓存](../../internal/enrich/datasources/display_client.go)、[动态分组缓存](../../internal/enrich/datasources/dynamic_group_client.go)。
- **状态分支**：恢复/关闭可能写两份终态缓存、清理抑制/依赖状态并发送提示；等级升级可能一次操作两个 Alert。被提前抑制的 Event 则不会走完表中全部保存和输出步骤。
- **重试**：普通临时错误最多三次，部分成功重放、CAS 冲突和控制面准备等待还会改变调用组合。不能把正常路径数字当最大值。

### 3.2 依赖主索引的规则相关成本

一次 `ApplyDependencyCandidates` 通常 R=1、W=1，但内部命令随规则数增长。

在新 revision、旧 owner 有成员/不匹配标记、无过期状态恢复的常见路径中：

```text
C_dependency ≈ 7 + D_local_false + 2 × D_external_false + 4 × D_true + P_old
```

- `D_local_false`：不读取外部事实就确定不匹配的规则。
- `D_external_false`：读取依赖快照后确定不匹配的规则。
- `D_true`：本次合资格的规则。
- `P_old`：需要从旧有序集合移除的正向成员数。
- 首次没有旧 owner 字段可删除时少一条命令；从终态清理状态恢复时可能多一条 `PERSIST`。公式不覆盖失败分支。

例如两条纯字段不匹配规则，本次修复后是 **1 个客户端 EVAL + 8 条 Lua 内部命令 = 9 C**；修复前为 19 C。源码和真实 Redis 回归见 [索引脚本](../../internal/policy/redisstate/dependency_index.go)、[命令预算回归](../../internal/policy/redisstate/dependency_index_test.go)。

这里的 D 是候选规则数，不是全部策略数。不过 [候选索引](../../internal/policy/candidate.go) 将主条件和子条件合并覆盖，正则、否定、引用等条件也可能进入保守兜底，因此宽泛规则增多时 D 仍可能接近全部依赖屏蔽规则数。**没有匹配规则不等于没有 Redis 调用**：旧成员撤销、revision 和后台回填保护仍需维护。

### 3.3 其他策略的增量成本

以下按实际执行的规则/方案计数，不按配置目录总数计数；正常脚本缓存已热，不含额外统计、失败修复和并发竞争。

| 分支 | 典型额外 R | 典型额外 C | 增长因素 |
|---|---:|---:|---|
| 一次被接纳的策略统计写入 | 1 | 5；该小时首次登记通常 7 | 实际条件求值/故障记录次数 |
| 一个 clip 防抖方案正常计数 | 1 | 约 31～35 | 命中的方案数；过期清理/反向索引满额分支另计 |
| clip 放行后首次绑定 owner | 1 | 7 | 实际建立 Alert 的防抖方案数 |
| aggregation 找到有效 owner 并确认抑制 | 2 | 18 | `Claim` 7 C + `Confirm` 11 C；真实 owner 读取在 ES/MySQL |
| 查询依赖主候选 | 1 | 空索引 4，有主 6 | 实际执行的依赖规则数 |
| 新加入一个正常合并窗口并确认 | 3 | 通常 22～23 | `Join` 约 15 C、读取 meta 1 C、首次 `Commit` 6～7 C |
| 普通时间屏蔽的纯条件计算 | 0 | 0 | 计算本身不访问 Redis；统计、已有绑定、后续状态变化另计 |

这些数字**不能无条件叠加到核心表**：例如聚合抑制后可能不创建 Alert；已放行 active 的持续触发会绕过新告警准入，已有合并等待也会跳过重复入窗。

还有两个尾部放大因素：

- [aggregation](../../internal/policy/runtime/aggregation.go) 的占位竞争最多循环 64 次、每次等待 25ms；每轮仍可能发 `Claim`。2 R 是正常 owner 命中分支，不是竞争时上限。
- [依赖主查询](../../internal/policy/runtime/dependency.go) 最多处理 64 轮失效候选；删除旧候选、补齐主 revision 会额外访问 Redis。正常路径不遍历全量活动 Alert，但失效候选修复不能忽略。

**策略统计不是固定 QPS 采样器。** [Observations](../../internal/policy/runtime/observations.go) 每次实际求值尝试异步写一次，最多四个在途、50ms 截止，满额丢弃；没有“每秒只写四次”的限制。[统计 Lua](../../internal/policy/redisstate/statistics.go) 的稳定路径为 `EVALSHA + TYPE + HEXISTS + HINCRBY + EXPIRE`。若每 Event 实际接纳 20 次统计，会单独增加约 20 R、100 C。

### 3.4 Cleaner 和异步 KAC 消费

- Cleaner 将一批持久化 Event 入 Mailbox：一批 R=1；每条 Event 通常 `LLEN + RPUSH`，原 Mailbox 为空时再 `XADD` Signal。因此 B 条入队、其中 Z 次空转非空，**C=1+2B+Z**。批次大小受操作数和字节预算约束，不是任意大。见 [入队 Lua](../../internal/lifecycle/mailbox/store.go)。
- KAC 消费面：每批读取一个 `XREADGROUP`；每批确认 Pipeline 中有 `XACK + XDEL`，所以确认 **R=C=2、W≈1**。另外每秒 `XLEN + XRANGE COUNT 1` 做队列观测。见 [KAC Runtime](../../internal/kaccompat/runtime.go)。
- KAC 的 `XREADGROUP BLOCK` 为 100ms，空闲时也会产生读取轮询；它属于控制面背景成本，不能除以新建 Alert 数后当作固定单条业务成本。
- 理想化例子：D=1、缓存 miss、每批仅一条、无丰富/统计、不含额外轮询和接管扫描，核心 Lifecycle 11 R/26 C，加 Cleaner 1 R/4 C、Signal 读取确认 2 R/2 C、KAC 读取确认 3 R/3 C，约 **17 R/35 C**。真实合批会降低摊销值，空轮询/丰富/策略又会增加它；此例不是在线平均值。

## 4. 控制面随告警规模增长的工作

### 4.1 依赖主索引回填：活动 Alert × 依赖规则

[索引管理器](../../internal/controlplane/process/dependency_index.go) 在启动、新规则、等级配置变化及真实依赖快照变化时回填；还有变化后的补扫。每分钟的常规检查只查询 Ready，**不是无条件每分钟扫描所有活动 Alert**。

[回填实现](../../internal/policy/runtime/dependency_index.go) 每页 32 条 Alert，一页一个 Pipeline，但每个 Alert 独立 EVAL。回填传入当前全部规则，再筛依赖屏蔽种类；不是普通 Event 的候选规则集合。

设 A 为本轮扫描的活动 Alert 数，D 为依赖规则数，D_ready 为本轮需标记 Ready 的规则数：

```text
R_backfill ≈ A + D + D_ready + O(1)
W_backfill ≈ ceil(A/32) + D + D_ready + O(1)
C_backfill = Σ C_index_update(alert, D) + Σ C_top(rule) + D_ready + O(1)
```

同 revision 已有纯字段 false 标记的重复回填：约 **5+2D C/Alert**；同 revision 各规则均有有效正向成员时约 **5+7D C/Alert**。首次构建、新 revision、清理态的成本不同，不能混用这些公式。

例如 A=10,000、D=10：一次已有负向标记的完整回填约 10,000 R、313 个写 Pipeline、250,000 C，加少量 Ready/命名空间操作。若真实依赖变化造成每分钟一次，单此项需求约 **167 R/s、4,167 C/s**；同样规模的正向成员回填可达约 750,000 C/轮。执行速度受四个后台槽、存储读取和实际延迟限制。

### 4.2 屏蔽定时复查与主变化提示：到期屏蔽 Alert / 受影响子告警

[定时复查](../../internal/controlplane/process/shield.go) 查询持久化的到期工作，每页 16 条、四路执行，满页继续处理，扫描到尾部才等待五秒。因此不是固定“每五秒最多 16 条”。[主变化提示](../../internal/controlplane/process/shield_hints.go) 则对受影响子告警执行同一复查入口。

一个仍处于屏蔽状态、没有实际资格变化、只推进 `NextCheckAt` 的典型复查：

- 获取/释放 fingerprint lease：2 R、4 C。
- `getAlertCurrent` 和 CAS 成功后各刷新一次依赖资格；已清理、只剩终态水位时，各 1 R、5 C。
- 刷新 Recent Alert：1 R、1 C。

因此基线约 **5 R、15 C/条复查**，再加绑定求值、依赖主选择、统计以及真正解除/变更的输出。见 [复查流程](../../internal/lifecycle/shield_check.go)、[控制入口](../../internal/lifecycle/process/shield.go)、[读取与 CAS 观察](../../internal/lifecycle/processor.go)。这条调度字段更新路径仍有重复资格维护成本。

若 A_s=10,000 条屏蔽 Alert 都要求每五秒复查，基线需求就是 **10,000 R/s、30,000 C/s**。四路执行不能保证满足这个需求：一轮耗时至少约 `A_s × 平均单项耗时 / 4`，容量不足时表现为复查延迟，而不是自动维持五秒周期。

### 4.3 策略活跃集合维护：策略数及每策略的活动成员数

此处指 `active-alert-indexes` 对 `alarmd:open_alerts` 的维护，与依赖屏蔽主索引是两套不同数据。

test 的实际配置为：每目标一秒检查 pending、每轮最多 16 个策略、每 60 秒完整发现、操作超时十秒、最多 100,000 行/32MiB。任务目录显示的五秒是目标发现外层周期，不是每个目标的 pending 轮询周期。

[发布过程](../../internal/activeindex/redis.go) 每次都 `SCARD` 并用 `SSCAN COUNT 256` 读取完整旧集合后比较；有变化时用每批 256 个成员构建临时集合，再原子发布。

令 q 为实际 SSCAN 页数，b=`ceil(新成员数/256)`，本次提示 token 未被并发更新、成功释放租约：

| 单策略一次刷新 | R（通常 W 也相同） | C |
|---|---:|---:|
| 成员不变 | 5+q | 16+q |
| 成员变化且新集合非空 | 5+q+b | 20+q+4b |

另加每目标 pending 查询：1 R，C=3+本轮返回策略数；以及每 60 秒发现的 `SCAN`、每策略 Enqueue 和一次健康状态写入。

`SSCAN COUNT` 只是工作量提示，q 不是保证等于 `ceil(N/256)`。按 q≈391 粗估一个 10 万成员的策略：不变也约 **396 R/407 C**，变化时约 **787 R/1,975 C**。如果频繁 Event 持续触发刷新，这项成本远高于仅每分钟校准；实际刷新频率还受处理耗时和批量预算约束。

另有 [Discover](../../internal/activeindex/redis.go) 的 `SCAN MATCH`：扫描成本受所在 DB 总 key 数影响，不能把 MATCH 理解为服务端索引定位。正式成员的全量读取还带来 O(N) 返回字节、排序和比较成本。

### 4.4 合并调度与成员执行：窗口数和成员总数

- [等待窗口筛选](../../internal/controlplane/process/merge_schedule.go) 跨页去重窗口，使用 [ZMScore](../../internal/policy/redisstate/merge.go) 每次最多 16 个 ID。一次扫描 R=C 为 `Σ ceil(每页每租户首次出现窗口数/16)`；不是严格全局 `ceil(W/16)`，因为有分页/租户边界。
- 完整读取一个已有窗口为一次 EVALSHA，通常 4 C（`TYPE/HGET/HGETALL` 加外层），但返回量和 Lua 遍历随窗口成员 M 增长。不能用“只有四条命令”判断它便宜。
- [成员建联/解除](../../internal/lifecycle/process/merge_batch_execution.go) 虽然批量读写 ES，仍逐成员取得 fingerprint lease、维护依赖索引、输出及更新缓存。
- 一次发生变更的 active 成员操作，忽略重试/旧意图恢复，含一个策略 Hook 和 KAC 入队：约 **7 R、12+C_dependency C**。其中 lease 2 R/4 C、两次缓存写 2 R/2 C、Hook 1 R/5 C、KAC 入队 1 R/1 C、依赖更新 1 R。最终缓存写可 Pipeline，减少 W，不减少 R/C。
- 例如清理已无 owner 的成员资格 C_dependency=5，则约 17 C/成员；首次移除一个 namespace 的旧反向索引至少再增加约 3 C，正向成员移除另计。1000 个成员不是一笔固定 Redis 成本，而是数千 R、数万 C 加窗口调度和父告警处理。

不存在以 Redis 保存 `MemberPool` 短时批次的额外队列；不能把已经移除的跨进程 merge-worker 模型计入当前容量。

### 4.5 KAC 消费、Stream 维护与其他任务

| 任务 | 增长因素 | Redis 量级 |
|---|---|---|
| KAC delivery | 告警变更/动作流量与实际批次 | 每读取批次 1 R/C；每确认批次 2 R/C、约 1 W；队列观测约 2 R/C 每秒 |
| Redis Stream Manager | 来源数、Consumer Group 数、待裁剪记录 | 存在的 Stream 每次检查 4 R/C：EXISTS、XINFO STREAM、MEMORY USAGE、XINFO GROUPS；每个有 PEL 的 Group 再加一次 XPENDING |
| 安全裁剪 | 已确认历史长度超过软上限 | 每批 XTRIM 之后重新检查，约 `1+4+有PEL的Group数` R/C；test 每来源每轮最多裁剪 100,000 条、每命令预算 10,000 |
| 调度协调/心跳 | 来源任务、分区、副本与 Worker 数 | 共享状态 GET/CAS 和中心租约；不按 Alert 总数增长，但状态 JSON 字节数会增长 |
| 抑制/屏蔽/合并管理请求 | 实际管理请求及请求选中的窗口/成员数 | 请求级 lease，再加目标级检查；不是每轮自动对所有历史 Alert 执行 |
| Schema、Bucket、Alert Archiver、KAC index maintenance | ES 索引/归档工作量 | 主要访问 ES；不能把其扫描 Alert 的次数直接换算为 Redis 请求 |

Stream 管理器每轮最多选择 16 个来源，缺省/当前轮询十秒；来源更多时要把轮转时间计入保留峰值。其定位方式是当前来源目录和当前 Signal 前缀，**不会自动发现任意旧前缀的历史 Stream**。

## 5. Redis 存储清单与增长因素

下表“大小”先给数据结构/公式；没有实测的条目不给固定字节保证。K 为实例/分组数，λ 为每秒实际发生相应动作的速率，T 为保留秒数。

| 数据族 | Redis 类型与内容 | 数量/大小主要取决于 | 保留与清理 |
|---|---|---|---|
| dispatch state、snapshot、leader | String，任务/Worker JSON、中心 token | 来源、任务、分区、Worker、交接记录；不随 Alert 历史条数直接增长 | state/snapshot 持久；leader 60 秒租约 |
| Signal | Stream，来源、租户、fingerprint、Mailbox 等调度信号 | 保留 Signal 数、PEL、consumer 数；不保存完整 Event | XACK 不删 entry；当前软阈值 100,000/来源，仅安全裁剪已确认前缀；无整体 TTL |
| Mailbox | List，Event ID | 待处理 Event 数、不同 fingerprint 队列数 | AckHead LPOP，空 List 自然消失；没有 TTL，积压持续存在 |
| fingerprint / 控制操作 lease | String token | 并发正在处理的不同所有权对象 | Worker 默认 TTL 60 秒；显式释放；控制操作至少 30 秒 |
| Recent Alert current / ended | String，完整 StoredAlert JSON | TTL 内不同关联键数和终结 Event 数 × Alert 字节数 | active refresh interval +5 秒；缺省 10 秒；单条编码上限 2MiB；终态通常有 current、ended 两份 |
| 依赖主候选正向索引 | 每规则版本/配置摘要的 ZSET，成员为排序时间+Alert ID | 合资格的 `(Alert,规则版本,配置摘要)` 关系数 | 活动期间无 TTL；失去资格/终态删除成员 |
| 依赖 owner-ns | 每 Alert/配置摘要的 Hash，revision、正向成员、纯字段 false 标记 | 被观察 Alert 数 × 配置 namespace 数 × 规则成员/负向标记数 | 活动期间持久，终态清理；无匹配也可能有 owner |
| 依赖 owner-ns:times | Hash，规则版本对应外部快照时间 floor | 当前仍存活 Alert 曾涉及的外部依赖版本数，不仅当前规则数 | 随 Alert 资格清理；当前更新不遍历/清除历史 floor |
| 依赖全局 owner / 清理水位 | Hash，namespace 反向引用、revision、cleared | 活动引用，加近 30 天被清理的不同 Alert ID | 最近一次 clear 后 TTL 30 天；终态、屏蔽或合并失去主资格均可能写入；重复复查会续期 |
| 依赖 ready / retired | String，规则 Ready、退役配置摘要 | 策略发布版本数、配置摘要数 | 无 TTL；retired 保留旧成员以支持受控重建，不能假定有后台自动 GC |
| 待放行依赖主 reservation / owner | String 候选 JSON、Set 反向引用 | 正在建立的主、涉及的规则数 | pending 30 秒；登记与反向引用约 30 天，终态清理 |
| clip | ZSET Event 成员、Hash 元信息、Set 反向引用、String op 去重、ZSET 管理索引 | 活跃 `(租户,来源,fingerprint,规则版本)` 数、窗口 Event 数、λ_clip×T | 普通计数和 op 的 T≈策略窗口+60 秒；反向/共享索引取较长 TTL |
| aggregation | String owner/op、ZSET 窗口成员、Set owner 反向索引 | 活跃策略分组数、窗口成员数、λ_确认抑制×T | pending 物理保留 90 秒、占位租期 30 秒；登记后约窗口+60 秒；成员跟随窗口 TTL |
| merge-window / merge-group / merge-due | Hash 元信息+成员 JSON、String 当前组窗口、ZSET due | 活跃/近期窗口数、成员总数、条件组、冻结结果 | 入窗刷新 48 小时 TTL；Finish 清 due 提示，不立即删除整个窗口结果 |
| merge-event | 每规则版本/组/Event/Alert 的 String 首次入窗结果 | **实际新入窗速率 × 48 小时**，还乘实际匹配入窗规则数 | 48 小时；不是只保留到合并完成 |
| policy observations | 每租户/类别/小时一个 Hash，每策略五个字段 | 有观察的策略数、租户/类别数、约 25～26 个小时桶 | 25 小时 TTL，每小时桶最多 4096 策略；不按 Event 保留一条记录 |
| KAC AlertChange | Stream，Alert 身份和可选动作原因 | 未处理/未确认通知数 × 通知大小 | XACK+XDEL；没有业务积压硬上限或整体 TTL；不保存完整 Alert 正文 |
| active-by-strategy 正式集合 | Set，KAC 兼容告警 ID | 每租户/策略的活动成员数之和 | 非空集合持久，刷新为空时 UNLINK |
| active-index pending/token/status/staging | ZSET/Hash/Set/String | 待刷新策略数、刷新并发、正在构建的完整新集合 | staging TTL 120 秒；发布覆盖期间新旧集合会同时占内存；空策略状态保留 24 小时 |
| Display / Dynamic Group 读取缓存 | 外部系统的 String/Hash | CMDB 实例、展示字段、分组关系 | Linkd 为只读消费者，不应把其写入/保留策略归为 Linkd 自有缓存 |

数据所有权参考：[调度器](../../internal/taskdispatch/controller.go)、[策略状态](../../internal/policy/redisstate)、[Recent Alert](../../internal/lifecycle/recentalert/store.go)、[合并状态](../../internal/policy/redisstate/merge.go)、[活跃集合](../../internal/activeindex/redis.go)。普通 Event、完整历史 Alert/AlertLog、策略文档、合并 Decision/Relation 的权威持久化在 ES/MySQL；但不能因此认为 Redis 里的所有状态丢失都无影响，调度历史缺失会阻止启动，抑制窗口和恢复水位也有业务语义。

## 6. test 内存样本

### 6.1 实例与分类样本

21:51:18～21:51:21 对 DB 0、DB 8 做了一轮有界 SCAN；分别完成 111、1 次扫描。只保留 Linkd 前缀及 DB 8 的 `alarmd:open_alerts`。每个数据族最多 12 个均匀蓄水池样本，读取 TYPE、MEMORY USAGE SAMPLES 5、PTTL 和长度，不读取业务值。SCAN 不是原子快照，短 TTL key 可能在两次读取间消失。

共享实例 `used_memory=2,669,480,640 B`，约 **2.49 GiB**；RSS 约 **2.69 GiB**；client buffer/connection 相关内存约 **25.9 MiB**；maxmemory 8 GiB，noeviction。DB 0 共约 55,904 key，DB 8 共 127 key。以上不是 Linkd 独占资源。

| 数据族 | 观察到 key 数 | 单 key MEMORY USAGE 样本 | 解释 |
|---|---:|---:|---|
| 当前部署依赖全局 owner/水位 | 23,921 | 448～784 B，均值约 560 B | 含持久活动引用和 30 天清理水位；抽样外推约 12.8MiB，不是精确总量 |
| 当前部署 owner-ns | 1,438 | 848～1,200 B，均值约 965 B | 样本为 2～3 个 Hash 字段，说明即便只有少量规则也有固定 key/字段开销 |
| 初次分类中的其他策略 String | 346 | 样本约 1,320 B | TTL 样本约 42～48 小时；初次分类未单独区分 merge-event，定向复核见下文 |
| merge-window | 16 | 1,608～115,112 B | 样本 Hash 为 2～261 个字段；按成员数增长 |
| observations | 78 | 样本约 552 B | 样本每桶五个字段 |
| Recent current | 16 | 活样本约 2.65～7.15KiB | 快速过期，扫描后只有十个样本仍存在 |
| Recent ended | 11 | 活样本约 2.63KiB | 九个活样本，不应外推为稳定 key 数 |
| dispatch | 3 | 120～49,272 B | state/snapshot 内容随任务规模增加 |
| DB 8 正式策略集合 | 34 | 120～65,728 B | 大样本为 993 个成员，约 66 B/成员；小集合固定开销不可忽略 |
| DB 8 active-index 元数据 | 38 | 抽到的状态 Hash 约 280 B | 不能将状态样本尺寸套到 staging Set |

依赖候选 ZSET、Ready、times 等也已观察到，但样本很小，不足以代表多规则、多配置版本规模。没有在此窗口观察到 clip 等 key，不代表该功能不会消耗 Redis。

22:06:21 对当前部署的 `:merge-event:` 前缀再次定向完成 SCAN，观察到 **411 个 key**；12 个样本的 MEMORY USAGE 均为 **1,320 B**，剩余 TTL 约 42～48 小时。这次复核验证了第 7 节算例的单条尺寸；计数与 21:51 的 346 不属于同一快照，不能直接比较增长率。

### 6.2 Signal Stream 全量 key 复核

由于 Stream 分布偏斜，不能用 12 个样本均值乘 key 数。21:54:09 对全部 21 个 Signal Stream 单独读取 MEMORY USAGE、XLEN、XINFO GROUPS：

| 范围 | entries | MEMORY USAGE | lag / pending |
|---|---:|---:|---|
| 最大旧前缀 Stream，无法映射当前来源 | 1,880,928 | 964,885,603 B，约 920.19MiB | 0 / 0 |
| 当前 test 来源 | 93,967 | 48,504,699 B，约 46.26MiB | 0 / 0 |
| 当前前缀、已停用 alarmd 来源 | 90,770 | 46,695,350 B，约 44.53MiB | 0 / 0 |
| 当前 built_in_bk 来源 | 93,240 | 45,729,504 B，约 43.61MiB | 0 / 0 |
| **21 个 Stream 合计** | **2,159,755** | **1,106,310,344 B，约 1.03GiB** | 当时均无待处理积压 |

全部旧前缀合计约 920.41MiB；当前前缀合计约 134.65MiB。常规大 Stream 样本约 **490～516 B/entry**。因此当前 100,000 条软保留阈值可以先按 **每个有历史流量来源约 47～50MiB** 做初步预算，再叠加 PEL/consumer 和流量峰值。

test Stream 还记录了 977 个 consumer；consumer 数与历史任务代次/重启有关，不等于当前进程数。只看 lag=0 或队列“空闲”会漏算这些已确认记录与元数据。

本次仅识别，未删除旧 Stream、consumer 或策略水位。当前管理器不扫描任意旧前缀，因此历史遗留应单独核对来源迁移和保留需求，不能计成新部署的必要稳态基线。

## 7. 可复用的容量模型

### 7.1 请求、命令与带宽

对新建、持续触发、恢复/关闭、抑制、合并分别统计流量 λ_i 和各自 R_i/C_i：

```text
R_total/s = Σ(λ_i × R_i) + Signal固定轮询 + 控制面任务R/s + 管理查询R/s
C_total/s = Σ(λ_i × C_i) + Signal固定轮询 + 控制面任务C/s + 管理查询C/s
网络字节/s = Σ(各调用速率 × 实际请求/响应字节) + Lua脚本文本 + 握手/重试
```

不能仅将历史样本 `41.72 C/Event × 新EPS` 当最终预算。该样本含约 14 个来源的轮询；将 EPS 提高会摊薄固定轮询，却可能增加匹配规则、新建窗口、Hook 突发和后台成员执行。

作为起始假设，1000 Event/s、15 R/Event、40 C/Event 对应约 15,000 R/s、40,000 C/s，**尚未加控制面和突发余量**。这些只是工作负载输入，需在目标实例上压测，不能由 Redis 通用 benchmark 推断可承载性。

Recent Alert 如果平均 4KiB、每 Event 写两次，1000 Event/s 的缓存写入正文就约 7.8MiB/s，另有 key、协议和读取响应。依赖更新目前使用 EVAL 而非只发送 SHA，也应计入脚本文本带宽。

### 7.2 内存

```text
M_data ≈ M_signal + M_mailbox + M_recent + M_dependency
         + M_suppression + M_merge + M_active_index + M_dispatch + M_statistics + M_kac_queue

M_signal ≈ Σ(各Stream保留entry数 × 实测bytes/entry) + PEL/consumer开销
M_mailbox ≈ 积压Event ID数 × List成员成本 + 不同Mailbox的key开销
M_recent ≈ TTL内不同current键数 × Alert缓存大小 + TTL内不同ended键数 × 终态缓存大小
M_merge_event ≈ 新入窗操作速率 × 172800 × 每条操作缓存大小
M_dependency_cleared ≈ 每秒新清理Alert数 × 2592000 × 清理水位大小
```

关键算例：

- 新入窗操作 **10 次/s**，按当前样本 **1,320 B/merge-event**、48 小时保留：约 **2.12GiB**，还没算窗口成员。这里是实际入窗操作速率，不能用所有重复触发 Event 的 EPS 替代。
- 每秒终结并清理 **1 个不同 Alert**，按 **512 B/水位**、30 天保留：约 **1.24GiB**；10 个/s 约 12.36GiB。未匹配依赖主规则的 Alert 也可能走清理并留下水位。
- 尚未终结但处于屏蔽/合并状态的告警，复查可能不断续期 clear 水位；应额外计入这类仍在维护的 key，不能按创建时间满 30 天就假定消失。
- 20 个来源各保留 100,000 Signal，按 500 B/entry：约 **0.93GiB**，即使全部 lag=0。
- Recent Alert 用 10 秒 TTL、每秒 1000 个不同关联键、4KiB/条：current 基线约 **39MiB**；相同 fingerprint 高频覆盖不会每次增加一个 key，终态第二份缓存另计。
- 依赖索引必须按 `(Alert,规则版本,配置摘要)` 关系数和负向标记数估算，不能只乘活动 Alert 数。旧 namespace、无 TTL 的 Ready/retired、仍存活 Alert 的历史 source-time floor 都有额外维度。
- 策略集合发布期间同时持有旧 Set 和 staging Set，成员变化高峰应计入临时双份成员内存。

实例预算还要加入连接/输出缓冲、复制/AOF 缓冲、allocator/RSS 碎片，以及持久化时的实际峰值；`MEMORY USAGE` 各 key 之和不等于 RSS。可先保留明确的工程余量，再以目标实例负载验证，不把某个固定倍率当 Redis 保证。

### 7.3 连接与慢请求

- Hook 每来源任务默认最多 16 个连接；控制面另有维护连接池，不能把 16 当成 Linkd 总连接数。
- [Redis client 装配](../../internal/redisclient/client.go) 对显式 PoolSize 同时设置 MaxActiveConns；未设置的客户端沿用 go-redis 9.21.0 默认，PoolSize 为 `10×GOMAXPROCS`，MaxActiveConns=0 不构成硬上限。
- 来源任务、实例、丰富 Redis 目标和租户会增加客户端/连接池数量。连接数量与 QPS 是两个预算，慢请求也会占住连接。
- 估算必要在途连接可从 `请求速率 × 平均往返秒数` 起算，再考虑突发和分池；扩大连接池不能减少命令量，也不能代替降低大 Lua/大结果的服务时间。
- 当前 Redis 同时承载队列、锁、抑制状态和恢复水位，不能把所有 key 当可随意淘汰的缓存。当前 noeviction 配置下，内存不足会体现为写入失败，因此要同时观察内存与队列/窗口增长。

## 8. 后续评估步骤与待改进项

每次发布或扩容前至少记录：

1. 提交、Redis/客户端版本、存储后端、来源/Worker 数、关键预算和保留配置。
2. 按 Event 结果分类的 EPS、新 Alert 速率、终结速率、真实入窗速率、相关策略数、被接纳的统计写入数。
3. R/C/W 的正常、突发、后台重建三个窗口；避免混用窗口和来源范围。
4. 活动/到期屏蔽 Alert 数、依赖规则数、待处理窗口/成员数、各活跃策略集合大小。
5. 每个大 Stream 的 XLEN、lag、pending、consumer、MEMORY USAGE；分开当前前缀、停用来源、旧前缀。
6. 对 key 数据族做有界 SCAN、去重、分层/蓄水池抽样，记录 TTL 与长度。大 Stream/大集合应逐 key 核对，不能用小样本均值外推总量。
7. 客户端连接、池等待、网络字节、Redis CPU/慢脚本、RSS、持久化配置及积压回落时间。

当前仍需关注的改进方向，本文不视为已经实施：

- 减少无资格变化的依赖索引重写，特别是屏蔽复查的读取/CAS 双重观察；必须保留提交后失败补偿。
- 评估逐次条件求值写统计的成本，四并发并未限制长期写入速率。
- 评估依赖水位 30 天、merge-event 48 小时与实际恢复语义是否匹配，未确认前不直接缩短或删除。
- 给旧前缀 Stream、历史 consumer、退役 namespace/规则版本明确的运维清理流程；当前自动维护不覆盖所有历史遗留。
- 对大策略集合反复全量 SSCAN/发布、大依赖回填、大规模到期屏蔽进行独立容量测试。
- 为依赖索引、策略统计及控制面单任务补充请求数、Lua 命令/耗时、返回字节观测；当前不能仅凭阶段 P95 分离每个 Redis 子步骤。

## 9. 验证边界与外部资料

- 源码计数覆盖本基线的典型成功分支；所有公式都需结合条件分支、真实命中率及批次大小使用。
- 在线采样是 test 的短窗口，期间仍有测试流量和来源配置变化，不是固定负载的性能压测。
- 只抽查了当前主 Redis DB 0、策略 Hook DB 8；没有清点所有外部丰富 Redis 实例。
- 此文没有进行大规模容量压测，没有宣称所有高延迟已经解决；发布后 21:43 的一分钟窗口 Lifecycle P95 仍约 4.61 秒。
- `MEMORY USAGE SAMPLES 5` 是 Redis 给出的内存估计，SCAN 允许重复且不是静态快照。本文做了 key 去重，保留采样时点和范围。
- 本次文档新增不改变任何业务行为；运行数据、凭据和完整告警正文未写入仓库。

外部资料：

- [Redis MEMORY USAGE](https://redis.io/docs/latest/commands/memory-usage/)：计入 key/value 管理开销，嵌套结构按样本估算。
- [Redis SCAN](https://redis.io/docs/latest/commands/scan/)：COUNT 是提示，遍历期间可重复返回，MATCH 不构成按前缀直接定位的索引。
- [Redis XDEL](https://redis.io/docs/latest/commands/xdel/)：删除条目与底层 Stream 节点释放并非一一对应，批量节点回收影响实际内存。
