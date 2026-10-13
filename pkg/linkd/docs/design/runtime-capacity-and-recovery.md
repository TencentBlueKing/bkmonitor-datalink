# 运行并发、容量与故障恢复

2026-10-13；本轮审查基线为 `41d0825c` 及 test 恢复补丁。用户明确要求单进程可纵向扩容，默认放开无依据的限制。本文件记录本轮目标与逐项验收；未完成项不能视为上线能力。

## 设计边界

1. 持久待办数量不等于进程内存使用。动作和投影不能因同租户已有 1024 个待办拒绝写入；取消仅为计数准入而存在的租户锁与本地四路闸门。
2. 调度任务数量、来源声明并发/内存之和默认不限制。显式配置才启用静态准入预算；实际每来源的在途消息/字节仍有可配置的内存边界。闲置来源声明的最大值不是实际占用量。
3. 后台执行并发应从有效 CPU 推导默认值并可配置。不得在服务、运行器、连接池叠加彼此无关的四路/八路限制；入队与发送分开调度。
4. 普通消息处理错误（逻辑异常、JSON 编解码、panic）记录诊断并确认；当前不预置 HTTP 状态或网络错误白名单；只有适配器显式标记、且已确认可安全重放的错误退避重试，总共最多尝试三次。耗尽后结束该消息，不能把 EventSource/Worker 标为异常或 pending。Mailbox 内失败 Event 必须先移出队头，不能只确认 Signal。进程取消、所有权丢失、确认结果未知属于控制协议，不能冒充业务失败后直接跳过。历史结果不确定动作不自动重放。
5. Elasticsearch CAS 与持久化确认不依赖 refresh；动作首次入队的排序可见性必须确认，普通进度写不等待 refresh。投影写后实时读取，扫描允许下一轮发现。
6. 所有权代次、fingerprint/Mailbox lease、同 Alert/目标投递锁、幂等身份、CAS、Kafka 连续 offset 不得移除。故障隔离必须遵守各 Broker 的确认语义，不以伪造成功确认换吞吐，普通失败消息按用户确认的终结策略处理。
7. 内存、请求载荷、单次扫描、超时、取消与安全裁剪属于资源或协议边界；不把它们与持久积压配额混为一谈。每个保留限制必须说明作用域、调整入口及满载行为。

## 验收检查点

- 全量检索生产代码，按机制归并重试、锁、容量和错误传播；逐项记录处理决定与剩余约束。
- 用自包含测试验证多来源调度、同租户并发入队、三次重试边界及普通错误确认、CAS 竞争、顺序与取消；持续故障不得消耗其他对象的入队配额。
- 运行完整 `make check`；静态检查、模拟并发测试与真实环境吞吐分别报告，不互相替代。

## 审查口径

初筛覆盖 `internal/` 565 个非测试 Go 文件。关键词候选包含锁保护的普通 map、API 查询预算和业务热路径，不能把匹配行数当作问题数。详细机制清单见下表；初筛候选用于范围覆盖，不作为缺陷计数。

用户进一步收敛后的核心限制：Cleaner 实例数不超过 Kafka 分区数；Cleaner 和 Lifecycle 各任务以未确认消息计数软背压；Lifecycle 数量可设上限，省略时每个可调度 Worker 一个。持久 Mailbox 数量、Signal 积压和租户任务总量默认均不作为准入条件。

## 按机制归并的审查清单

以下 **45 类机制**按作用域归并，不按相同检查出现的行数重复计数。初筛的 692 个候选位置分布在 171 个文件，包含普通 mutex、测试/预览预算及正常取消，不能解释成 692 个缺陷。1–28 为本轮取消、调整或补齐的处理链路机制；29–45 是核对后保留或收敛的协议、资源和管理边界。

| # | 机制与代码入口 | 审查结果及当前行为 |
| --- | --- | --- |
| 1 | `config.DispatchConfig.MaxTasks` | 默认 16 改为 0；0 不启用任务数准入，删除最大 256 的校验 |
| 2 | `config.WorkerConfig.MaxConcurrency` | 默认 128 的来源并发预留取消；0 不限制，显式正数仍可主动限制 |
| 3 | `config.WorkerConfig.MaxInflightBytes` | 默认 256 MiB 的来源最大值求和预留取消；任务内真实在途字节仍控制领取 |
| 4 | `taskdispatch.Controller.Beat` | 删除注册 Worker 最多 256、心跳最多 256 个任务报告的数量限制；保留认证、身份和报文大小校验 |
| 5 | `taskdispatch.Controller` 来源目录扫描 | 删除累计 10000 个来源即拒绝整轮的限制；继续按页读取 |
| 6 | `taskdispatch.Reconcile` 退休记录 | 删除超过 1000 条便不启动的门槛；旧 owner 排空后精确回收，Kafka 不保留 Redis 历史 |
| 7 | `taskdispatch.Agent.Run` 来源准备 | 固定 Release 异步读取，慢配置请求不阻塞心跳；只在准备成功后启动任务 |
| 8 | `cleaner.Runtime.workerLoop` / `consume.Runtime.workerLoop` | 工作 goroutine 懒启动；空闲来源不预建整池 goroutine |
| 9 | `config.CleanerRuntimeConfig` 默认 worker 与批次并发 | 不再叠加固定 8 个 worker、2 个 lane 批次的默认门槛；默认跟随未确认消息预算，可显式降低 |
| 10 | `config.LifecycleConfig` 默认并发 | 默认 256，与默认未确认 Signal 数对齐；不是整个进程共享 32 个名额 |
| 11 | Cleaner/Lifecycle 资源配置校验 | 删除并发 1024、批次数 64、在途 4096、固定 64/256 MiB 等无上下文常量封顶；保留正数、大小关系与乘法溢出校验 |
| 12 | `lifecycle.mailbox.Config.MaxPendingPerMailbox` | 默认 128 改为 0，不限制持久 Mailbox 深度；显式正数仍可主动开启 |
| 13 | `cleaner.SignalBackpressureChecker` | 不再默认以 Signal lag+pending 的倍数阻止 Cleaner 领取；0 直接放行，不查询 Redis 准入 |
| 14 | `consume.New` 延后集合 | 覆盖全部已接管消息；消除 256 个在途 Signal 对 64 个延后位置的容量失配 |
| 15 | `consume.Runtime` 消息重试/Block | 默认普通错误确认；显式标记最多三次；耗尽不会 failLane 或令整个来源退出 |
| 16 | `cleaner.Runtime` 纯计算失败 | 普通错误与 panic 只终结该消息，后续连续确认前缀继续推进 |
| 17 | `cleaner.Runtime` Event/Mailbox 批次失败 | 按具体条目保存次数与结果；普通错误丢弃，显式可重试最多三次；Broker 确认错误另行处理 |
| 18 | `lifecycle.scheduler.Handler` 队头失败 | 对实际 Event 分类、重读检查点；失败后记录终态并 AckHead，再继续下一条，不能只 XACK Signal |
| 19 | `processingerror.Guard` 异常边界 | Cleaner 单条/批次、Lifecycle Event、消费客户端、任务入口及动作/投影页面执行捕获 panic；日志不回显 panic payload |
| 20 | `actiondelivery.Recorder` / `Retrier` | 删除固定四路入队闸门和 1024/512 租户软水位；已持久化任务数量不再决定其他 Alert 是否可入队 |
| 21 | `projection.Service` / `Retrier` | 删除租户准入 Redis 锁、1024 待办配额及独立四路服务闸门；保留任务 create-only/CAS |
| 22 | 动作/投影 Runner 的同租户串行 | 补建按具体 Alert 独立，删除整租户串行；发送仍按同 Alert/目标串行 |
| 23 | Runner 两个循环共用四个名额 | 补建与发送各自按配置执行，慢发送不挤占补建名额 |
| 24 | Runner 固定 16 条/四路 | `control_plane.delivery` 配置并发与页大小，默认按有效 CPU 推导；管理 HTTP 查询仍有自己的分页预算 |
| 25 | 共享任务/业务 HTTP、MySQL 连接池 | 去掉四路/八路和从静态任务数推导的额外连接上限，0 为不限；空闲连接回收仍有预算 |
| 26 | ES 动作/投影每次 Put 等待 refresh | 动作首次入队保留排序可见性等待；普通进度 CAS、投影写不等待刷新，执行前实时读 |
| 27 | Lifecycle ES 合批器满载 | 移除独立的 32 批次并发封顶，执行名额跟随 Lifecycle 并发；读写默认收集最多 10ms，单项批次不等待、满批提前发送；聚合名额满载回退为原同步 CAS 请求，直接请求仍受 Close/取消管理 |
| 28 | 大页扫描与响应字节上限叠加 | 明确超限时按原游标缩页重读，最小一条后返回实际错误；不以固定大页反复卡住扫描 |
| 29 | Cleaner 实例数与 Kafka 分区数 | 保留：同来源实例数量不超过 Topic 分区；元数据未知不猜分区数 |
| 30 | Lifecycle 来源副本数 | 保留：省略 replicas 时每个可调度 Worker 一个；显式数字作为上限，0 停止该角色 |
| 31 | 两阶段任务内未确认消息计数 | 保留：Cleaner 默认 512，Lifecycle 默认 256；确认释放计数，达到软水位暂停领取 |
| 32 | Kafka lane FIFO、连续 offset、revoke/lost | 保留：失败消息可以按新策略结束，但不能越过未完成消息伪造成功提交；一个 lane 的业务错误不再退出整个来源 |
| 33 | Mailbox/fingerprint lease 与续租 | 保留：按租户、来源、fingerprint 保护同一告警状态；忙是等待，不是业务重试失败 |
| 34 | 同 Alert/目标投递锁、unsettled 顺序屏障 | 保留：仅影响同一业务对象；历史结果不确定动作不被本次改造自动重放，也不能假装外部受理成功 |
| 35 | Event/Alert/任务 CAS、幂等键、状态转换 | 保留：移除配额不能允许双创建、覆盖新版本、越租户或重新生成动作身份 |
| 36 | 任务授权 watchdog、停止握手、清理期限 | 保留：失去授权必须停止旧 owner；不是普通消息失败策略，不能为继续吞吐让旧 owner 越权执行 |
| 37 | Broker Confirm、Mailbox ACK、已发送结果确认 | 保留：结果未知不能释放成“已确认”；这里只重试确认协议，不重新执行业务；基础设施不可用期间对应来源可能暂停领取 |
| 38 | 动作/投影持久任务自动尝试 | 从八次收敛到三次；已有 Sender 的显式协议失败分类继续受该上限约束。任务自身 failed 与 EventSource/Worker 的异常不是同一状态；失败历史不占租户入队配额 |
| 39 | `policy.redisstate` 抑制/合并/屏蔽原子脚本 | 保留窗口/owner/epoch 一致性和集合/脚本执行预算；这是规则运算及 Redis 单次载荷边界，不是来源数量准入 |
| 40 | `activeindex`、动态配置、来源 Provider | 保留定时发现、重读/回补和原子替换；活跃策略提示队列溢出由周期发现补偿，不作为消息处理配额 |
| 41 | `enrich`、OneModel、JSONPath/规则表达式 | 保留单 Event 的查询/结果/表达式与载荷预算，防止输入组合放大；触发普通错误按消息终结策略处理，不把来源退休 |
| 42 | 预览、Console、管理 API 的并发/分页/响应预算 | 保留：保护交互查询，不占 Cleaner/Lifecycle 的执行名额；不能将管理接口四路限制误认为进程仅四路处理 |
| 43 | 人工合并接续、抑制清理、屏蔽检查的请求日志 | 保留独立管理队列及命令级预算、CAS/操作身份。它们不由普通 Event 入队；管理请求仍可能返回 busy，属于明确剩余边界 |
| 44 | Control Plane 合并/屏蔽维护的页面与执行预算 | 保留现有后台维护预算；逐行失败推进扫描，不取消 Worker。与动作/投影 Runner 已拆开，不宣称本轮完成所有后台维护任务的吞吐调优 |
| 45 | 单请求字节、单项文档、批次、超时与 goroutine/map mutex | 保留内存/协议边界和本地数据竞争保护；Go map 的短临界区不是远端业务串行锁，不能机械删除 |

## 当前可复制的配置边界

```yaml
dispatch:
  max_tasks: 0
worker:
  max_concurrency: 0
  max_inflight_bytes: 0
cleaner:
  max_inflight_messages: 512
  # worker_count/max_concurrent_batches 省略时跟随未确认消息预算。
lifecycle:
  concurrency: 256
  signal:
    max_inflight_messages: 256
  mailbox:
    max_pending: 0
    backpressure:
      high_watermark: 0
      low_watermark: 0
control_plane:
  delivery:
    concurrency: 0
    batch_size: 0
```

来源的 `scheduling.lifecycle.replicas` 省略或 `all` 即每个匹配 Worker 一个，可设正数上限。Cleaner 另受真实 Kafka 分区数约束。旧部署 YAML 中显式写入的 16/128/256MiB、Mailbox 128、Signal 高低水位仍会被视为主动配置；发布时需同步检查有效配置，单独换二进制不会抹掉这些显式值。

## 失败记录与不可混淆的边界

- 消息逻辑失败、JSON/内容错误、panic：结构化日志包含租户、来源、Event/Message/位置及固定原因；已存在 Event 尽力保存 `processing.state=rejected`、`outcome=processing_failed` 与 `reason_code`。诊断存储不可用时日志仍保留，不能为写诊断无限阻塞消息。
- 当前消息错误的默认可重试白名单为空；HTTP 502/429、错误文本或超时不会自动纳入。扩展必须显式标记并验证可安全重放，总尝试三次。测试中的可重试错误标记不代表生产白名单。
- Mailbox 失败终态可保留原 Plan 作为部分副作用诊断。终结当前 Event 不撤销已经创建的 Alert、已持久动作意图或已经受理的通知；已有可靠动作/投影仍沿各自协议收尾。
- 进程取消、所有权丢失与 ACK/确认结果未知不等于消息逻辑失败。此时保留未确认数据；依赖一直不可用时对应来源的领取会暂停，不保证无条件继续吞吐。
- 数据库存储对象损坏、跨租户身份矛盾、非法分页与基础设施启动失败仍报错；这类协议问题没有按普通原始消息静默跳过。

## 验证证据

新增回归覆盖：单 Worker 调度 1000 个来源（1000 Cleaner + 1000 Lifecycle）；慢配置读取时心跳继续；历史 1200 个退休 owner 不阻止重启；普通失败、显式可重试失败、panic 与健康消息混合；同租户并发入队超过原 1024 限制；任务首次入队/进度更新的 refresh 边界；载荷超限缩页；取消与原任务 CAS。完整 `make check` 已通过，包含 Go 单测、vet、race、golangci-lint（0 issues）、Console 测试/构建、Helm 与发布脚本检查。独立本机 Redis 的 redisstream/mailbox/taskdispatch 集成及 race 通过；退休记录清理保留新 owner 的未确认消息，也不接管健康 owner。该 Redis 由测试独立启动并销毁，没有访问 test 或生产基础设施。单 Worker 的 1000 来源用例验证调度容量与规则，不代表完成该规模的真实业务吞吐压测。本轮代码尚未部署。
