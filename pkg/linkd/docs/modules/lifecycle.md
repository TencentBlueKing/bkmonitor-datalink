# Lifecycle

Alert 业务保存后发布租户、Alert ID 和必要动作信息，普通路径不等待 KAC 投影或动作接口。
动作资格沿用原策略裁决；解除屏蔽和解联只同步状态。发布失败记录日志和指标，业务事实仍保留。
控制面按[动作 V3](../reference/contracts/kac-action-delivery-v3.md)执行批量投影与独立发送。

源恢复、关闭及系统关闭先读取业务事实，不以旧活动告警重新取得依赖主资格为前提；终态 CAS 后清理候选索引，
清理失败的重放仍补偿当前终态。人工屏蔽使用控制面共享的完整策略目录与目标读取能力。
已有屏蔽绑定的停用判断必须基于完整发布目录，缓存淘汰触发重载，重载失败保留绑定；定时复查的候选筛选与正式求值共用等级映射。
合并等待扫描按 Redis 到期查询预算分块；到期成员和丢失提示保留逐项补偿，同一窗口一批只做一次正常裁决。
成员读取/旧意图补齐及成员补偿分别使用 3 秒子上下文，超时仍报告失败；整个作业预算耗尽后，下一完整扫描轮换同窗口起始成员，避免固定前缀长期阻塞后续补偿。窗口整体裁决继续使用原 10 秒作业预算。
Relation 已 ended 而 Decision 未完成时直接补齐裁决，不重新解联或改写结束事实。

每个 Elasticsearch Lifecycle runtime 默认使用独立合批器，将不同 Mailbox 已就绪的 Event result CAS、Alert
CAS/create 和 AlertLog create 合并发送。现有校验、缓存修复和错误映射由 Repository 继续负责；
调用方收到对应 item 成功后才进入下一步，不能提前输出或 ACK。已有快照的 CAS 不再前置读取；
独立 ID+版本接口与冲突核对仍使用 realtime `_mget`。快照更新路径首次读取完整 Event，以保留丰富提交的
完整文档预算检查，后续提交沿用返回快照与新版本；终态 CAS 使用局部
Bulk update，只修改 `related_alert_ids` 和 `processing`。公共 Event 查询仍返回完整文档。
写批次参数自动匹配 Lifecycle 并发，不允许独立手调：
单批上限为并发的一半（向下取整、最少 1、最多 128），读写最多收集 10ms（单项不等待），
执行名额跟随 Lifecycle 并发，不再独立封顶为 32；默认并发 256 对应 128 项、10ms、256 个执行名额，字节预算默认 4 MiB。
聚合名额已满时直接执行原 CAS 请求；读写使用独立收集队列，达到操作数或字节上限立即发送，
未满则在各自首项期限到达时发送。10ms 只限制主动收集，不包含 ES 执行与响应时间。
实际批次大小还受调用方并发与写入依赖限制。完整配置见[配置指南](../guides/configuration.md)。

控制面进程内的合并成员使用显式批次，不使用上述自动合批器：锁前按批读取身份、锁内按批读取成员与活动父，
提交 Bulk 条件更新后执行各成员输出，批量写日志，再 Bulk 局部清除完成意图。完成 token 的 ES 缓存按来源使用显式 Redis pipeline；失败成员重试时即使业务已完成，也继续补齐缓存。每个块为各成员取得 fingerprint 租约，准备/输出任务使用
全局执行名额；租约持有等待不占工作名额，因此批次大小与工作并发相互独立。
内存批次池允许同组多个批次在本进程并行执行，成功成员直接回报，失败项留在内存待办；每批只读取本批成员与固定关系摘要。
组协调在内存汇总直接返回的批次结果，不以最早失败成员阻塞其他批次。
组级进度只局部提交变化字段，成员批次不争用同一个 Relation/Decision CAS；解除完成后直接收尾。
MySQL 单项快照 CAS 使用直接条件 UPDATE；控制面多项使用短 SQL 事务批读版本与 UPDATE JOIN，日志多行 INSERT；已形成的多行日志批直接提交，不重新进入单项自动收集器。MySQL 未启用 Recent Alert Cache，完成阶段不为空缓存动作等待执行名额。
MergeChange/PolicyChange 确认保留版本条件，只清除相应字段，不重写业务版本、时间或完整载荷。
批次诊断使用 `linkd.merge.batch.phase_duration`：`batch.*` 是每批墙钟，`member.*` 是并行成员的独立样本，不能相加；
`http.*` 拆分客户端请求，`es.bulk_took` 记录 ES 返回值，`sql.*` 拆分短事务。
`linkd.merge.pool.waits` / `wait_duration` 是共享连接池累计值的 gauge，跨采样取差值，不重复相加。
启用 debug 日志可获得每个实际成员批的起止及阶段耗时；容量测试保存这些记录，计算真实执行区间、峰值和有效批次并发，避免一秒状态轮询造成误判。
详细故障边界与容量验收见[策略扩展方案第 12 节](../design/policy-runtime-scaling.md#12-cas-与底层批处理改造2026-10-16)。

Console 的单 Event 耗时分布默认显示完整处理时间；右上角“忽略合批等待”只扣除主动合批等待，
保留存储排队，并同时显示执行、合批等待、存储排队与完整总耗时。点击阶段可下钻子步骤。
子步骤使用同一次 Event 的独占计时，包含零样本、失败、重放及尝试内重试；所有子步骤之和等于父阶段。
Enrich 分别计时内置 processor 的 Match/Process、链编排/补丁编码与丰富结果保存；自定义名称归为 custom，
不把动态配置 ID 放入指标。Event 写入只统计最终结果提交；告警执行区分
读取、创建/CAS、动作投递、策略绑定、缓存与流水保存。父子计时均将实际执行、合批等待、存储排队拆开。
新 `linkd.lifecycle.event.phase.step.duration` 以固定 `linkd.step` 和 `linkd.wait_kind`
（execution、batch_wait、storage_queue）维度记录；子步骤均值的分母为 Event 尝试次数，不是 processor 调用次数。
明细按自身已采集尝试样本求均值；有子项数据就展示，不用父子加总或覆盖率校验阻断显示。
各累计序列经 Prometheus 独立计算 increase，窗口边界外推和计时边界可产生小幅差异；
升级前的历史尝试没有明细，子项不因此被稀释或隐藏。缺失子项显示暂无数据，不冒充零。
上方当前指标使用“指标计算窗口”的最新值，下方分布使用完整所选时间范围；页面显式标出两种窗口。
执行时间仍包含网络/下游 I/O、编解码及业务处理，不等同于 CPU 时间。
合批等待按每次逻辑调用实际进入收集窗口的时刻记账；取得调用容量、队列调度及执行槽位的等待
归入存储排队，两者不重叠。一个 Bulk 调用拆成多个物理批次时，等待只按该调用经历的区间记一次，
不按文档数累加。失败和取消也记录返回之前的等待；记录在原调用 goroutine 完成后归入当时的独占环节。
原 `linkd.lifecycle.event.phase.duration` 继续保留完整耗时；新增 `phase.execution_duration` 与
`phase.wait_duration`（`linkd.wait_kind=batch_wait|storage_queue`）每次尝试为每个环节记录零或实际样本，
保持相同分母。不能用物理批次均值直接扣减 Event 均值；缺少新指标的旧 Worker 显示数据不完整。

Lifecycle 负责把已经持久化的 unprocessed Event 裁决为 Alert 创建、推进、终结、等级升级、抑制或
孤儿结果，并写回 EventProcessing。它不接收原始 MQ payload，也不负责 Cleaner 的 Event 创建和 ACK。

## 1. 核心概念

### 1.1 关联键

Lifecycle 查找 active Alert 的唯一条件是：

```text
(bk_tenant_id, event_source_id, fingerprint)
```

`source_alert_id` 只是 Event 来源事实，只有 EventSource 明确选择它生成 fingerprint 时才间接参与
关联，Lifecycle 不提供绕过 fingerprint 的查询旁路。

### 1.2 MailboxID

```text
MailboxID = CorrelationKey(bk_tenant_id, event_source_id, fingerprint)
```

MailboxID 同时是 Redis Mailbox 身份、Lifecycle Signal 的 `Message.ID/OrderKey` 和分布式 lease identity。
同一 Mailbox 中只保存属于同一租户、来源和 fingerprint 的 Event ID。

### 1.3 Mailbox 与 Signal

Redis Mailbox 只使用 `<key_prefix>:<mailbox_id>:events` List，按成功入队顺序保存 Event ID。它不再维护
去重 Set、scheduled marker 或全局精确计数；单 Mailbox 默认不设置持久积压上限（max_pending=0）。

Signal schema v1 只携带租户、来源、fingerprint、MailboxID 和入队时间，不绑定单个 Event。入队 Lua 先
读取 `LLEN` 并检查单箱上限；原长度为零时先 `XADD` Signal，再 `RPUSH` Event ID，非空时只追加 List。
脚本运行时错误不会回滚先前命令，因此这个顺序把失败方向限制为最多产生一次空唤醒，不会形成无 Signal
的非空 Mailbox。

Mailbox 允许相同 Event ID 出现多次。Event 业务去重由 Repository 的稳定身份负责；Lifecycle 逐条处理，
已经终态的 Event 由 `ProcessEvent` 直接返回保存结果。

### 1.4 Lease

lease 保护同一 Mailbox 的跨进程串行处理。RedisLocker 使用 `SET NX PX` 获取随机 token，并通过
compare-token Lua 续租和释放；旧 owner 不能误续租或删除新 owner 的 lease。

### 1.5 Recent Alert 缓存

Elasticsearch 后端使用所有 Lifecycle 实例共享的 Redis Recent Alert 缓存跨越搜索 refresh 窗口。缓存
从 Mailbox `key_prefix` 派生 namespace，分别按 MailboxID 保存最近的 current Alert、按租户和 EventID
保存最近的 terminal Alert。TTL 等于 Active Alert `refresh_interval + 5s`，默认为 10 秒。value 包含
完整 Alert 快照和存储 `VersionToken`，但它不是
Alert 事实源，也不会保存全部 active Alert。

首次裁决先查 current 缓存；命中 active 时直接使用，命中 terminal 时视为“当前无 active”。
重投读取当前策略和真实 Alert 状态重新判断，不再恢复 processing.plan。
只有 current 缓存明确不存在时才查询 Elasticsearch；缓存错误、损坏或身份不一致均保留队首重试。
ended 条目仍由缓存接口维护，但不是当前 Processor 的恢复入口。MySQL 后端不启用该缓存。

## 2. 关键抽象

### 2.1 Scheduler Handler

```go
type EventReader interface {
    GetEvent(ctx context.Context, tenantID, eventID string) (store.StoredEvent, error)
}

type Mailbox interface {
    Peek(ctx context.Context, mailboxID string) (string, error)
    AckHead(ctx context.Context, mailboxID, eventID string) error
}

type EventProcessor interface {
    ProcessEvent(ctx context.Context, stored store.StoredEvent) (ProcessResult, error)
}

type Locker interface {
    Acquire(ctx context.Context, mailboxID string) (Lease, error)
    Renew(ctx context.Context, lease Lease) error
    Release(ctx context.Context, lease Lease) error
}
```

Handler 只负责编排 Mailbox、lease 和 Processor，不包含 Alert 状态机。Handler 从 Repository 读取并校验
Mailbox 队首 Event 后把同一 `StoredEvent` 快照交给 Processor；Processor 仅在并发冲突重裁决时重新读取，
避免正常路径重复 realtime GET。Elasticsearch 与 MySQL 快照更新首次读取完整 Event，
丰富提交和最终结果提交复用写入返回的新快照；EventPlan 只保留在当前调用内，不持久化。
快照不存入全局缓存，处理结束后由 Go GC 回收无引用对象。

Repository 在读取边界恢复默认值、UTC 时间和计划中的空集合。Processor 和 Enrich Scope 直接使用
该快照，不重复规范化来源 JSON 或校验已经冻结的丰富载荷。新丰富结果由提交接口检查，非法结果在
没有写入副作用的 `ErrInvalidArgument` 后降级保存；版本冲突和 I/O 故障仍向外返回重试。
计划与关联更新只检查本次变更和身份、状态关系，深拷贝仅用于隔离可变对象。

### 2.2 Processor

`lifecycle.Processor` 的依赖是窄能力：

- `store.Repository`：Event/Alert/AlertLog 读写和单对象 CAS；
- `RecentAlertCache`：Elasticsearch 最近 refresh 窗口内 Alert 写入和终态恢复锚点；
- `AlertIDGenerator`：由 opening Event 生成稳定 Alert ID；
- `SeverityTable`：比较 incoming Event 与 active Alert 等级；
- `EventEnricher`：生成处理计划前同步丰富当前 Event；
- `PolicySnapshotter`：在策略副作用前固定处理时间和精确配置版本；
- `NewAlertSuppressor`：仅在本次入口未关联活动 Alert 时执行新告警门槛，返回计划诊断并负责计数 owner 绑定/终态清理；
- `ProjectionTargetSelector`：新 Alert 按 opening Event 精确来源版本与租户选定可靠出口；
- `ActionRecorder`：业务 CAS 后可靠保存已冻结的动作，失败传播并阻止覆盖原意图；
- `FinalHook`：Alert 真实变化后的普通外部输出；
- `Clock`：服务端状态时间；
- `Logger`：记录降级和失败上下文。

Processor 最多进行 3 次 CAS 循环。冲突后重新读取 Event；当前调用可复用内存裁决，第一项尚未生效且被并发操作推进时重新裁决。跨调用重投读取当前策略，不恢复已保存计划，不用旧 replacement 覆盖并发状态。

当前防抖已接入正式 Worker：按冻结时间在 Redis 统计不同 EventID，未达到阈值的 Event 完成处理但不创建 Alert。
所有活动 Alert（包括后续屏蔽/合并状态）都绕过防抖与关联聚合，同级、低等级、升级以及同 Event 终态/触发组合继续沿用生命周期裁决。
候选按严重程度依次判断；高等级未达阈值不覆盖低等级独立判定。选中一个放行等级后，其余未判定的触发仍按等级抑制处理。
抑制计数和跳过原因在内存裁决中计算，随最终结果保存到 `EventProcessing.policy_decision`；未终态重投允许重新判断。

有明确主告警的汇总抑制还生成 `suppress` AlertLog，绑定冻结裁决中的真实主 AlertID；流水保存来源 EventID、等级、策略版本及窗口引用。日志身份由租户、事件、主告警、等级与策略版本确定，先在本次内存裁决中构造，全部流水写入成功后才完成 Event CAS。跨调用重投允许重新裁决，不承诺补齐本次缺失流水；流水继续使用既有稳定身份。防抖未达门槛时没有真实 Alert，继续仅保存 Event 诊断，不伪造 AlertID，也不在后续告警产生后追补这些事件。

屏蔽/解除屏蔽和合并等待、建联、释放、结束均属于独立策略流转。合并控制面日志保留操作、窗口和关系身份；合并成员全部结束导致系统恢复时保留系统操作类型，不描述为告警源恢复。不修改既有不可变流水的参数与身份。

新策略记录保存准确策略版本与当次执行事实。屏蔽绑定直接保存 `periods`（实际命中的 UTC 时段）和操作人；合并等待、关系与抑制裁决不保存策略名称、字段展示名、时区等静态副本。静态说明由 KAC 查询准确不可变 Release，其 `origin` 关联发布时完整 KAC 快照。合并控制日志明确保留 `change_kind`、`merge_role`、主告警身份/当时标题、成员数量及确认的结束/释放原因，不复制完整成员列表。释放失败裁决、缓存丢失及落选成员分别标识 `conditions_not_met`、`window_missing`、`member_not_selected`，不重新推测原因。

新日志使用 `display_version=2` 与 `policy-flow-log-v2` 身份命名空间；控制面屏蔽意图固定 `log_version=2`，合并意图固定 `log_params`。控制面已有冻结流水意图沿用原 payload 和 LogID；Lifecycle 不再保存 EventPlan 或回填历史数据。ES 仅追加必要映射，不重写历史文档。对外读取契约见[策略流转业务事实](../reference/contracts/policy-flow-records.md)。

无 Alert 的单独恢复/关闭会清理未绑定计数；实际告警终态和人工关闭按 owner 清理，旧告警不能删除新代次。
关联聚合也已接入：跨来源候选先抢占 pending 窗口，真实主 Alert 创建后登记，其他来源实时复核活动性并原子确认关联。
聚合候选不会提前抑制其他 Event；较后策略抑制时释放占位，旧 owner 清理不影响新窗口。多等级可以关联不同主，整体上限为 32。
Redis 失败或有界争抢超时记录跳过和低基数指标，取消/身份/CAS 等错误仍使 Event 重试。
时间、自定义依赖、CMDB 依赖屏蔽及合并准入均已接入；屏蔽候选只绑定/清理自身计数并撤销聚合占位，不能登记为抑制主。
屏蔽、合并和处置资格/投影任务的进度见[开发方案](../design/event-enrich-and-alarm-policies.md#111-实施进度与验收记录2026-10-07)。

### 2.3 屏蔽、放行与控制面检查

`ShieldEvaluator` 使用本次 Event 的有效视图做新匹配；已建立关系保留原 SourceEventID/severity 和主告警，
每次复查使用当前发布目录中的最新策略，不加载绑定时的历史 Release。绑定中的版本、时段和原因保留建立时的历史事实。
策略顺序保持为首个匹配后停止；修改有效期、目标、主/子条件会影响下次复查，停用、删除或屏蔽类型变化会解除原绑定。
手动绑定指定了具体告警，只复查最新策略的启用状态、类型和有效期，不重新匹配目标与条件或比较旧时段标识。
发布目录沿用现有缓存刷新机制，读取失败保留绑定等待重查。自定义依赖
与 CMDB 依赖按活动主优先、无主首次原子登记及固定关系处理。主目标集合仅限制主告警，子告警按业务、
rely_policy、时间及实际关系匹配；主恢复/关闭通过提示和定时兜底解除，不主动给子告警补发处置。

`admission` 记录最近实际放行的等级。已放行同级触发保存 `duplicate_trigger`，只推进最近事件和状态；
warning 升级为 critical 若被屏蔽，保留 warning 的放行记录，解除后下一条 critical 仍能获得处置资格。
来源恢复/关闭照常进入终态并清空屏蔽。

控制面 `shield-check` 独立扫描 ES/MySQL 工作标记，每页 16 条、最多 4 路、单项 10 秒，循环间隔 5 秒。
它复用来源 Hook、租户检查及 fingerprint lease；检查失败保留关系/待输出意图，不能把读取失败当作条件不匹配。
解除或换绑先 CAS 保存状态及 `policy_change`，再尝试状态输出、写稳定流水，最后仅清除意图。无新 Event 时不会设置 admission 或进入合并。

合并失败释放已实现独立用例：只移除指定 pending 窗口，保留其他等待和成功关系；符合当前状态与等级资格才放行。
`merge_change` 与状态 CAS 一起保存；新 Event、直接关闭和屏蔽检查先完成它，流水/输出中断按原 cause 重试。
逐成员裁决进度和完整边界见[失败释放设计](../design/event-enrich-and-alarm-policies.md#84-失败释放与生命周期联动)。

合并成功时，成员在自己的 fingerprint lease 内复核真实父并记录关系，全部成员确认后才开放父处置。
父被人工关闭后，只解除对应关系和等待；即使移除了活动成员的最后一条关系，也不设置 admission 或调用
action Hook，下一条触发 Event 再判断。多父共享成员时保留其他关系，已终态成员保留历史摘要。
全部真实成员终结才将内置父恢复为 recovered/system/merge_members_ended；已被人工关闭的父不改写原结束原因。
建立、解除和恢复均保存稳定的 merge_change 意图，纯元数据清理不再次推进 UpdateAt。Worker 已装配合并
入窗与确认；独立控制面的 merge-judge/merge-decisions/merge-relations 分别负责窗口、持久化步骤与终态，
merge-batches 在内存拆批，按空闲名额启动 goroutine，收齐直接返回的结果即推进关系和父准入。
不跨进程分发，也不在 Redis/ES/MySQL 保存批次待办或逐批进度；重启从持久关系重建，允许幂等重放。
反向引用使用固定身份幂等批写，无写后读回；成员 Alert 与组级关系保持原 CAS，冻结快照保留首次值。
合并主只经过内置来源自己的 Lifecycle，不再入窗；关系未就绪时仍阻止处置。

Hook 内部角色区分 `state/action`：通用 Kafka 快照与策略索引属于状态输出，现行 KAC 消息属于处置触发。
动作只在本次 `EventPlan.mutations[].action_ready` 为真，或曾放行告警进入终态时发送；定时解除仅调用状态 Hook。
KAC 投影默认在 Alert 业务写入后同步尝试，失败进入一次后台补偿；动作仍使用独立持久任务及投影确认 Gate。已有可靠动作目标的 Alert 跳过旧 KAC Hook，通用状态输出仍保留。

### 2.4 Enricher 与 FinalHook

```go
type EventEnricher interface {
    Enrich(ctx context.Context, input enrich.Input) (enrich.Result, error)
}

type FinalHook interface {
    Execute(ctx context.Context, input FinalHookInput) (FinalHookResult, error)
}
```

Enricher 对每条不同 Event 在裁决前同步执行；逐 evaluation 保存结果，共享本次 Event 的查询缓存。
结果专用 CAS 一旦成功，处理计划失败或重投均复用该结果。error、panic、非法状态/数据降级为 failed，父取消停止处理。
恢复/关闭和低等级被抑制 Event 也保存丰富结果。创建 Alert 时直接复制 opening Event 对应等级的 data；
后续 Event 不刷新既有 Alert 的丰富快照。`trigger_event_id` 是稳定的首次丰富来源，无需再增加重复的 enrich_event_id。

输入为 `enrich.Input{Event}`；Scope 返回隔离的 Event 与显式 evaluation，未构造虚拟 Alert。
运行时统一复用当前任务加载的来源配置及连接，积压 Event 不再回读接入时的历史 Release，也不创建临时运行时。
`event_source_version` 保留接入版本；`enrich_source_version` 记录实际执行丰富的来源版本，供已保存结果的展示使用。来源发布通过任务切换生效，已保存丰富结果继续复用。

`Alert.enrich` 固定只包含 `processors` 顶层 key；每个 processors 元素是以稳定处理器名
为唯一 key 的 envelope，包含 status、value 和可选 diagnostics。`Alert.enrich_status` 是总状态的
唯一持久化字段，领域校验根据 Processor 状态重新聚合并要求一致。

`test` 处理器通过注入的进程内 datasource 模拟顺序调用、随机延迟、调用超时和概率故障；
成功后返回固定字段副本，失败遵循同一 Processor envelope。调用进入统一 datasource 指标，
不依赖外部数据库。参数与范围见[配置指南](../guides/configuration.md)。

当前 `strategy → resource → display → metric → source` 链已经提供 MySQL 和 Elasticsearch 数据源装配，
由进程顶层 resources 和来源 Release 的实际 Processor 依赖选择连接；不回查监控平台策略历史表。
单元测试中的 mock 不表示目标业务环境已经联调，接入示例见[主机告警丰富](../guides/host-alert-enrich-example.md)。

FinalHook 在 Alert 真实变化后按当前任务 Release 的 `EventSource.hooks` 顺序执行。
每个实例接收独立快照，各自产生成功或失败 push AlertLog，实例名参与流水幂等身份和指标。
单个插件 error、独立超时、panic 或非法结果不阻止后续插件；父上下文取消则停止处理。
流水仍与本次状态操作一起提交；失败流水写入成功不回滚已经完成的 Alert 状态。
空列表不输出，同类型允许多个实例；每个来源至多 16 个实例，名称必须唯一。

插件客户端归来源任务所有，发布切换排空旧任务后关闭旧客户端，部分装配失败释放已创建客户端。
Kafka 插件保持 [Kafka Alert V1](../reference/contracts/alert-output.md) 协议及 message ID 不变。
KAC 插件把相同 Alert 快照转换为 KAC `alarm_collect_topic` 扁平消息；`event_id` 使用
`linkd-` 加 AlertID 维持生命周期关联，`alarm_id` 使用 `linkd-` 加稳定 UUID 兼容 KAC URL 路由。
同一 Alert 快照重试生成相同 `alarm_id`，不同更新时间或 outcome 生成不同记录身份。等级、
`Asia/Shanghai` 时区和 `source_name=鲸眼监控` 固定在适配器内。
实现与配置见 [KAC Alarm Hook](../design/kac-alarm-hook.md) 和[来源 hooks](../guides/configuration.md#eventsource-hooks)。

来源的 `active_alert_limits` 使用内部 Redis 维护活跃集合。Lifecycle 在真实创建或终结后尽力更新，
持续触发不写集合；等级轮换接替旧成员，只有成员变化才发配置的通知。缓存失败不改变主流程结果，
由控制面任务在已有 fingerprint lease 下按实时状态修复。配置及故障边界见
[活跃告警缓存与软限流](../guides/active-alert-by-strategy.md)。

## 3. Signal 处理流程

Lifecycle 进程使用 Redis Streams Session 和通用 `consume.Runtime` 消费 Signal：

```text
Redis Signal
  → 校验 Signal 与 transport metadata
  → Acquire(MailboxID)
  → 循环读取并处理 Mailbox 队首
  → Mailbox 为空
  → Complete / XACK Signal
```

具体顺序：

1. 解码严格 Signal schema，校验 MessageID、TenantID、OrderKey 与 payload 一致；
2. 只尝试一次 lease；锁忙返回 `Defer(lock_retry_delay)`，立即释放 worker；
3. 持锁后启动定时续租，续租失败取消当前处理；
4. `Peek` 队首 Event ID；
5. 读取 StoredEvent，并校验其 EventSource/fingerprint 与 Signal 一致；
6. 调用 `Processor.ProcessEvent`；
7. 只有 Processor 成功后才以 Event ID 校验并 `LPOP` 队首；
8. 重复直到 Mailbox 为空或达到 `max_drain_events`；
9. Mailbox 为空时直接 Complete Signal；
10. 达到排空上限但仍非空时释放 lease 并返回 `Defer(0)`，让 Signal 回到本地公平队列；
11. 最终释放 lease；释放失败只记录警告，不能误删新 owner 的锁。

Worker 直连集群内 Service，仅可识别的传输故障（连接拒绝/重置/中断、网络不可达、暂时性 DNS 故障、HTTP 传输 EOF、连接或请求超时）保留当前 Mailbox 队首及 Signal，不执行 AckHead/XACK。这类等待不消耗普通三次重试预算；每次让出 lease 后按现有延时重试，取消和长期失联仍由 Worker 心跳授权及停止流程接管。

HTTP 响应不单凭状态码判定控制面不可用：500/502/503/504、限流、鉴权等响应均按原有普通错误语义处理，不进入上述等待。完整响应的 JSON 解析错误、证书/地址错误、快照内容无效和本地缺少任务身份也不能标记为控制面故障。404 与有效空结果保留各自业务语义；HTTP 错误响应不伪装成“查询不存在”。传输读取与 JSON 解析分开，完整收到的 `{` 是响应格式错误，未读满 HTTP 声明长度是传输中断。

该边界覆盖策略目录、精确策略版本及策略依赖快照；Enrich/正文生成复用当前任务配置，不再按事件查询历史来源配置；其他数据源的错误处理不因此扩大重试范围。Worker 发布目录有效期为 60 秒，约每 5 秒后台刷新，控制面本地目录在发布时立即失效。资源快照过期本身不证明控制面故障；没有已有刷新错误时先共享刷新一次，再按真实结果裁决。真实网络故障不完成事件，响应中的配置/数据错误保留既有不可求值处理，避免某个配置长期卡住事件。

默认单次最多排空 128 条后主动让出。普通 ProcessEvent 错误（含逻辑、JSON 和 panic）记录拒绝诊断后移出该 Event，继续排空后续项。当前不预置错误重试白名单；显式可重试标记最多尝试三次，耗尽同样只结束当前 Event。Event 的 processing 使用 rejected/processing_failed 和固定 reason_code；不再保存失败执行计划，终态重投直接返回已有结果。诊断存储不可用时保留结构化日志，不因诊断写入再次阻塞消息。
任务上下文取消、lease 撤销和 Mailbox ACK 未确认时保留队首；动作排序索引尚不可见属于协议等待，释放 lease 后延后 Signal。租户待办配额已取消。
Processor 必须幂等。最后一次 `LPOP` 前发生的新入队会由当前 Handler 继续读取；`LPOP` 后的空到非空
入队会创建新的 Stream ID，确认旧 Signal 不会删除新 Signal。处理期间已创建的新 Signal 可能成为冗余
空唤醒，可以安全完成。

## 4. Event 状态裁决

同一 `(bk_tenant_id, event_source_id, fingerprint)` 仍最多一个 active Alert；Event 的多个判定共享
一个 Mailbox。先验证所有级别，再从 triggered 判定选最高级别，不按数组顺序逐项执行。

`severity: __ALL__` 是 alarmd 的关闭专用标记，只允许 `closed`，不参与级别排序。
它优先匹配当前活动 Alert，保留原级别并按来源时间、原因关闭；即使该 Alert 的级别已从动态
配置删除，也按此次来源关闭处理。不存在活动 Alert 时为 orphaned。若同时有普通 triggered，
先关闭旧生命周期，再创建最高触发级别的新 Alert，不应用旧生命周期的升级策略。

其余普通级别遵循以下规则：

1. 最高 triggered 高于当前 active 级别时，优先应用全局 `lifecycle.severity_upgrade_policy`；
   同一事件中旧级别的 resolved/closed 被升级替代，记录 suppressed/evaluation_superseded。
2. 没有更高级别触发时，只允许与 active 同级的 resolved/closed 结束它。
3. 当前不存在 active（包括刚刚恢复/关闭）时，最高 triggered 创建新 Alert。
4. 当前 active 与最高 triggered 同级时推进生命周期，并按来源的 `content_mode` 更新 `Alert.content`；更低的 triggered 被抑制。
5. 未出现的级别不隐含恢复；历史被抑制的低级别不会自动激活，必须由本次或后续事件明确触发。

| 当前 active | 本次判定 | 结果 |
| --- | --- | --- |
| 无 | critical 与 warning triggered | 创建 critical，抑制 warning |
| warning | critical triggered + warning resolved/closed | 按全局策略升级，旧级别终结判定被替代 |
| critical | critical resolved + warning triggered | 恢复 critical，创建 warning |
| critical | critical triggered + warning resolved | 更新 critical 的内容和生命周期，warning 恢复为 orphaned |
| critical | 只有 warning triggered | 抑制 warning，critical 继续活动 |
| 任意级别 | __ALL__ closed | 关闭当前 Alert，保留原级别 |
| warning | __ALL__ closed + critical triggered | 来源关闭 warning，再创建 critical，与升级策略无关 |

同级持续触发的 `content`、`latest_event_id`、`last_occurred_at`、`update_at` 与 `revision`
在原有一次 Alert CAS 中一起保存，不增加独立写请求。
`source` 模式原样采用本次 Event 的值（包括空字符串）；`bkmonitor_description` 模式用
本次 Event 的判定、检测事实及精确策略历史版本重新渲染，沿用新建时的文案失败降级和取消规则。
渲染仅使用临时候选身份和本次丰富结果，
标题、维度、标签、创建锚点和首次丰富快照保持不变。低等级被抑制、恢复和关闭不改写内容。

策略只有两个取值，默认 `close_and_create`：

- `update_current`：原 Alert 保持 active 和 alert_id，在同一次 CAS 中更新 severity/content/latest_event_id/last_occurred_at/update_at/revision；
  content 与同级持续触发一样按 content_mode 生成，保留 begin_at/create_at/trigger_event_id、来源版本、维度和首次丰富结果。追加包含 from_severity、
  to_severity 的 severity_change 流水，只输出一个 active 快照。
- `close_and_create`：旧 Alert 进入 closed/severity_upgrade，按本次 Event 创建新的高等级 Alert，复制本次 Event 的丰富结果；
  先输出旧告警 closed，再输出新告警 active。一个 Event 关联旧、新两条 Alert。

两种策略都不执行原地降级。当前高级别恢复且低级别再次触发时，结束旧 Alert 并创建新 Alert。
`last_occurred_at` 仍取本次被接受 Event 的 occurred_at；`update_at` 严格单调。

## 5. 内存裁决与副作用

2026-10-14 取消执行计划和策略版本快照落盘。策略与裁决集合仅在当前调用内使用；进程中断后的重投按当时策略与真实告警状态重新判断。
允许本次事件/输出丢失，不承诺补齐历史副作用，但不能留下阻塞后续触发、恢复或关闭的身份冲突。
写入响应不确定时复用 realtime GET 和 Recent Alert 缓存修复；保留稳定 ID、单对象 CAS 与租户隔离。
本节替代本文件其他段落关于冻结 EventPlan 重放的历史说明。


```text
读取 Event / active Alert，完整裁决全部级别
  → 冻结 Event Enrich，构造稳定 Alert ID、目标快照与流水
  → 按计划执行最多两项 Alert CAS/create
  → 每项写入后更新 Recent Alert 缓存
  → 按旧告警、新告警顺序执行 FinalHook
  → 一次批量追加操作与输出流水
  → Event CAS 提交逐级结果及 related_alert_ids
  → Mailbox 出队
```

纯抑制保存最终裁决和稳定流水，不持久化执行计划，不输出 Alert 快照。来源事实（包含 values 和 evaluations）不被处理结果覆盖。
EventProcessing.state/outcome 是整个事件的摘要；每个级别的结果保存在 processing.evaluations。

### 5.1 Elasticsearch 终态归档

Lifecycle 的终态提交点是 Active Alert CAS 成功，不包含物理归档。Active 文档 `_id` 只由
`bk_tenant_id + alert_id` 生成，因此已经终结但尚未归档的旧 Alert 不会占用新 Alert 的文档身份，
等级升级可以在同一次裁决中直接创建新的 active Alert。

Control Plane 的 Alert Archiver 使用稳定游标连续扫描 Active 索引中的终态文档，将每批数据拆给有界 Worker。
Worker 先 Bulk create-only 写入对应 History 时间桶，再只对 History 已确认成功或内容一致的幂等副本按 Active
原 `_seq_no/_primary_term` 执行 Bulk delete。单项失败保留 Active 并在下一轮扫描重试，不阻塞当前轮后续
Alert，也不让 Lifecycle 重试已经成功的终态 CAS。归档过渡期间 Active 与 History 中允许存在内容完全一致的
同一 Alert，逻辑读取将其折叠为一个对象。History create 和 Active delete 都使用 `refresh=false`，
Active 删除的 409 若 realtime 确认版本变化，只记录归档并发冲突并留待下一轮，不清除新版本、
不立即循环重试、不将任务标记异常；批次满足 `Scanned = Archived + Conflicts + Failed`，冲突不能冒充已归档。
History 内容冲突、读取/写入失败仍保留失败语义。

409 幂等冲突使用 realtime `_mget` 核对；搜索可见性由 Elasticsearch refresh 最终提供，不属于归档提交点。

## 6. 幂等与部分成功恢复

上述步骤不是一个事务。Event 终态直接返回；未完成重投读取当前策略和真实 Alert 状态重新裁决。
Alert 创建仍使用 opening Event 的稳定 ID 和 create-only，业务更新保留原有 CAS。创建已提交但响应丢失时，
失败路径按同一 Alert ID 回读真实状态；同一源事件已生效或后续控制命令已推进告警时，不能用旧快照覆盖。
告警写入后先发布缓存，再执行绑定、Hook、流水与 Event 最终结果，不增加新的持久化补偿状态。

同一次裁决产生的 AlertLog 在一次 Bulk 中提交。只有全部日志项成功后才执行独立的 Event CAS，避免
Bulk 部分成功时 Event 已经终态而日志无法由现有重试路径补齐。Alert 与 Event/AlertLog 位于不同
索引，Bulk 不提供跨文档事务。

Elasticsearch Lifecycle 专用的 Alert create/CAS、AlertLog Bulk 和最终 Event CAS 都使用
`refresh=false`。Alert 写入成功后必须先更新 Redis Recent Alert 缓存，缓存失败时不能继续 FinalHook、
AlertLog 或 Event CAS。Active 索引 `refresh_interval` 由
`storage.elasticsearch.active_alert_refresh_interval_seconds` 配置，默认 5 秒；缓存 TTL 在此基础上
再增加 5 秒安全余量，默认 10 秒。缓存过期后搜索结果应已经可见；CAS
冲突通过物理文档 realtime GET 回读并修复缓存。重复 Mailbox 引用仍通过 Event realtime GET 立即看到
终态；若其他部分成功窗口发生重投，仍允许 FinalHook 重复，下游必须按稳定 Kafka message ID 去重。

Elasticsearch 不使用 Active 文档 `_id` 额外防御跨进程 fingerprint 重复；正常处理路径依赖 Mailbox
lease 串行化同一 fingerprint。物理归档由 Alert Archiver 异步完成，不属于 Event 的处理成功条件。

如果副作用完成但 Event CAS 前崩溃，Event 仍在 Mailbox 队首；重投重新裁决。允许本次输出或流水缺失，
但同 fingerprint 后续合法触发、恢复和关闭应继续工作，无需人工清理旧计划。

重复 Mailbox 引用在 Event 已终态后不会再次执行领域副作用；这不等价于外部 exactly-once。若
FinalHook 已完成但 Event 终态 CAS 尚未成功，恢复仍可能再次调用 FinalHook，下游继续按稳定
Kafka `message_id` 去重。

## 7. 直接关闭

`CloseAlert` 是内部应用用例，不通过 Event 或 Mailbox：

```go
type CloseAlertCommand struct {
    OperationID  string
    BKTenantID   string
    AlertID      string
    OperatorKind user | system
    OperatorID   string
    Reason       string
    EffectiveAt  time.Time
}
```

它只允许关闭 active Alert，不创建 Event，不修改 latest_event_id。CAS 成功后以 operation ID 作为
cause 执行 FinalHook，再把 close 与 push AlertLog 一次批量写入。相同命令可补齐流水和输出；不同
命令不能改写已有终态。

控制面已提供受管理 JWT 保护的 [HTTP 主动关闭入口](../reference/contracts/alert-close.md)，Console 通过
该入口调用现有用例；当前仍没有 CLI 或 Kafka 直接关闭入口。独立关闭请求最多四个并发、每次十秒，
使用当前已发布来源的 Hook 配置；不重新丰富。CAS 成功后缓存失败的重试也会修复近期缓存，再补齐输出和流水。
启用全局 KAC 插件时，控制面关闭入口还会在同一请求预算内同步推进持久投影并确认搜索可见性及 ACK；
未确认返回 502，原命令重试可继续补齐。具体保证与失败恢复规则统一见上述 HTTP 契约。

## 8. 恢复与数据安全边界

- Signal 未 XACK 时，Redis Streams PEL/XAUTOCLAIM 负责重新投递；
- XAUTOCLAIM 完成一轮空扫描后，按 Signal 的 `read_block_milliseconds` 间隔再检查，默认一秒；
  未结束的扫描游标、已发现的待恢复消息和失败重试继续处理，已退休 consumer 的定向接管不受此间隔影响；
- Signal XACK 只清理 PEL 引用，不删除 Stream entry；配置 Redis 与 Lifecycle 后，控制面默认执行安全裁剪，
  在软长度上限触发时只裁剪所有 Consumer Group 都已经确认的连续前缀；
  可通过 `control_plane.redis_stream.enabled: false` 显式关闭，预算说明见[配置指南](../guides/configuration.md)；
- Event 从 Mailbox 移除前崩溃，队首仍可重放；
- lease 锁忙只 Defer，不消耗普通 Retry 次数；
- Cleaner 只有 Event 持久化和 Mailbox 入队都成功后才确认上游 MQ；
- 不再扫描 unprocessed Event 补发 Signal；
- 上游 MQ 已确认后，如果 Redis Mailbox 整体丢失，无法从 offset 精确恢复；
- 部署必须为 Redis 配置满足该风险级别的持久化、复制和备份。

当前没有运行中的 Event Replayer。未来若增加恢复任务，应只扫描超过 Elasticsearch refresh 安全窗口
仍为 `unprocessed` 的 Event，并重新交给 Processor 收敛；AlertLog 不承担当前状态，且 orphaned、
rejected 等结果不一定生成日志，不能单独作为“Event 已处理”的判据。

配置中的 Signal、Mailbox、lease、Retry 和并发预算见[配置指南](../guides/configuration.md)。


## 动态等级变更

等级来源、恢复与分发由[动态配置](../design/dynamic-configuration.md)管理，Lifecycle 只读取进程内快照。
未知等级的活动 Alert 在处理到同一 fingerprint 时作为 system 操作关闭（显式 `__ALL__/closed` 按来源关闭）；对应清理意图保存在 EventPlan，
用于关闭后日志/Hook 失败时幂等补齐。Event 包含任何未知等级则整条标记 `rejected/unknown_severity`，
不执行其余 evaluation 的业务操作，持久化完成后正常确认 Mailbox，不修改已经终态的 Event 或 Alert。
`__ALL__/closed` 不属于未知等级，无需动态等级表包含该标记。

### 延后容量与积压背压（2026-10-13）

Signal 已接管后，锁忙、动作积压水位和分批处理返回 Defer，仍保留原 Receipt，不能提前 XACK。
运行时延后容量覆盖全部在途消息；在途预算满后暂停接收，等待既有消息完成。动作入队使用租户积压软水位，
移除跨 ES 写入及刷新等待的租户准入锁，保留 fingerprint 处理租约、任务幂等、排序可见性确认和目标投递租约。

### 当前容量策略

任务内默认最多接管 256 条 Signal；达到未确认消息/字节软水位后暂停领取，确认后恢复。处理 goroutine 按实际消息懒启动。默认不使用租户动作待办数、Mailbox 深度或 Signal 持久积压作为准入条件。Lifecycle 副本省略时每个可调度 Worker 一个，可通过来源 scheduling.lifecycle.replicas 显式设上限。具体配置与保留的一致性边界见[运行容量与恢复](../design/runtime-capacity-and-recovery.md)。

### KAC 队列与同步关闭

普通更新交给控制面队列投影；主动关闭保存终态后直接写 KAC 并 refresh，失败返回 502，允许原命令重试。
不再保存 ActionPending、投影水位或输出任务；生命周期自身保留业务 CAS 和审计，执行计划只在内存中存在。
