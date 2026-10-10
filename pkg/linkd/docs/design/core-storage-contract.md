# 核心存储契约

领域字段以 [`define.md`](define.md) 为准。本文件只定义 Repository 的逻辑操作和故障边界。

## Repository 端口

`store.Repository` 组合四个窄接口：

- `EventStore`：Event 批量幂等创建、读取、丰富结果冻结 CAS 和处理结果 CAS；
- `AlertStore`：Alert 创建、读取、active 关联查询和快照 CAS；
- `AlertLogStore`：不可变流水批量追加和时间线分页；
- `QueryStore`：按 Event 查询其处理结果和已关联 Alert。

调用方只依赖这些逻辑端口，不解析 MySQL version、Elasticsearch index/seq_no 或物理路由。

## 对象与版本

- `StoredEvent = Event + EventProcessing + VersionToken`。processing 不进入 Event JSON，状态为
  `unprocessed | accepted | suppressed | orphaned | rejected`。
- `StoredAlert = Alert + VersionToken`。Alert.revision 是可对外解释的单调业务版本，不能用 VersionToken 排序。
- `VersionToken` 对调用方不透明，只能从一次读取原样交给同一对象的 CAS；MySQL 内部使用 version，
  Elasticsearch 使用物理 `_index/_id/_seq_no/_primary_term`。
- Event、Alert 和 AlertLog 是独立对象，Repository 不承诺跨对象事务。

## Event

- `CreateEvent` 只接受 `related_alert_ids` 为空、丰富为 pending 的新 Event；相同身份和内容为幂等，内容不同返回
  `ErrIdentityConflict`。
- `CreateEvents` 按输入顺序返回逐项创建、幂等、冲突或暂时失败结果；请求级错误表示无法可靠解释
  整批响应。`CreateEvent` 委托 batch-of-one，避免维护两套语义。
- Elasticsearch 使用 `_bulk?refresh=false` 的逐项 `create`；成功表示主分片已确认写入，不保证搜索立即
  可见。409 使用 realtime GET 回读并校验内容，429/5xx 保持可重试。MySQL 首版通过统一批量接口逐项
  写入，不宣称多行 SQL 性能。
- `CompareAndSetEventEnrichment` 在计划保存前通过同一 Event 版本冻结全部 evaluation 的结果、完成时间和配置摘要；
  完成后相同结果可幂等核对，修改结果被拒绝。来源事实、processing 和关联保持不变；过期版本返回冲突。
- `CompareAndSetEventResult` 在一次 CAS 中同时写 processing 和 `related_alert_ids`。accepted 必须关联 Alert；
  suppressed 可无关联，orphaned 与 rejected 必须保持为空。
- `ListEventsByAlert` 按 related_alert_ids 成员关系过滤，再以 received_at + event_id 稳定分页；ES 使用 Alert 生命周期边界
  缩小 Event 桶范围，跨度过大时回退到 read alias。
- Elasticsearch 的 Event 创建和结果 CAS 均使用 `refresh=false`：成功只表示主分片已经确认写请求，不
  保证 Event 或新的 processing 状态已经能被 `_search` 查询到。单 Event `GetEvent` 根据稳定 EventID
  路由到写 alias 并使用 realtime GET，使 Cleaner 幂等核对和 Lifecycle 处理不依赖 refresh；列表和统计
  查询仍接受 refresh 延迟。

`CompareAndSetEventResult` 也接受 unprocessed + plan 的计划 CAS；计划先保存才允许执行副作用。
终态结果必须与已有计划一致，且不得残留 `plan`。ES 的 Lifecycle 投影 CAS 使用固定脚本整体
替换 `processing`，保留 `_seq_no/_primary_term` 条件；撤销计划也清除旧对象，不能用对象局部合并
代替替换。非法处理快照返回 `ErrInvalidEventProcessing`。ES evaluations 使用 nested，values、plan、policy_context 和 policy_decision 只保留原始对象、不建立动态索引。
MySQL related_alert_ids 使用 JSON 数组，查询通过 JSON_CONTAINS 判断成员关系并使用租户/接收时间索引。
这是内部 schema 的直接调整，不提供存量单动作事件或旧 related_alert_id 字段的迁移。

## Alert

Elasticsearch 存量 Alert JSON 完全缺少 `revision`，且没有投影目标、待执行动作、
策略或合并变更意图及对应工作标记时，读取按业务初始版本 `1` 兼容。该版本只描述升级后
的快照基线，不推断历史变更次数，不自动绑定投影目标或补发动作。读取不改 ES 文档；
后续正常 CAS/归档写入保存版本，仍使用原 `_seq_no/_primary_term` 保护并发。
显式 `revision: 0/null`、负数、越界值或带新版版本状态但缺版本的损坏文档仍拒绝读取。
兼容限定在 ES 存储解码边界，不放宽领域对象和外部投影协议的版本校验。

- 同一 `(bk_tenant_id, event_source_id, fingerprint)` 同时最多一个 active Alert。MySQL 用唯一键约束；
  Elasticsearch 依赖 lifecycle lease、active 查询和覆盖 refresh 窗口的 Redis Recent Alert 缓存保证正常处理路径中的
  唯一性，不额外维护全局 fingerprint 唯一索引。
- Elasticsearch Active 与 History 文档都使用租户与 `alert_id` 摘要作为 `_id`。归档过渡期间同一
  Alert 可以同时存在于两个索引，业务内容必须一致；仅投影确认水位不同时，由归档 CAS 将各目标较新的确认合入 History 后条件删除 Active。不同 Alert 即使 fingerprint 相同也使用不同 `_id`。
- Alert CAS 必须保留所有继承字段和创建锚点，允许推进生命周期字段及当前 severity；终态 Alert 不可修改业务事实，但可完成输出意图及推进投影 ACK。
- 新建 Alert 必须为 active/revision=1，不能预置已同步水位。业务 CAS 在读取版本边界计算 current.revision+1，并原子推进全部已绑定目标的 required_revision；调用方可以提交读到的 revision 或已冻结的下一版，不能任意指定版本。
- 元数据 CAS 保持 revision/update_at；ACK 不改变绑定或 required_revision，按目标单调确认。CAS 冲突不推进业务版本。
- `FindAlertEndedByEvent` 只匹配 `latest_event_id` 等于目标 Event 且 `end_type` 为 source 或
  severity_upgrade 的终态 Alert，保留为只读查询能力，Lifecycle 重试改用持久化计划与 Alert 实时读取。
- Lifecycle 专用 Alert create/CAS 使用 `refresh=false`，写成功后必须先更新 Recent Alert 缓存再推进
  Hook、日志和 Event；普通 Repository 契约仍可使用 `refresh=wait_for` 保证独立调用后的搜索可见性。
- CAS 冲突修复按 VersionToken 指向的物理文档执行 realtime GET，不依赖 `_search` refresh。

## AlertLog 与查询

- `AppendAlertLogs` 按输入顺序返回逐项结果，不承诺整批事务；`AppendAlertLog` 委托 batch-of-one。
  相同 log_id 和内容为幂等，内容不同返回 `ErrIdentityConflict`；
- Elasticsearch 使用 `_bulk?refresh=false` 追加 AlertLog；409 使用 realtime GET 核对内容。Bulk
  成功不保证日志已经能被 `_search` 或 PIT 查询到，调用方必须接受 refresh interval 内的短暂缺口；
- AlertLog 索引的 translog durability 默认使用 `async`，降低逐 Event AlertLog 写入的同步成本；进程、
  宿主或存储故障时可能丢失最近一个 translog sync interval 内已确认的日志。Event、Active Alert 和
  Alert History 保持 `request` durability；
- `ListAlertLogs` 只读取指定租户和 Alert，按 `created_time + log_id` 稳定升序分页；
- 默认分页 100，最大 500；cursor 绑定对象类型、租户、父 Alert 和物理读目标，不能跨查询复用；
- `QueryAlertByEvent` 总是返回 Event；Event accepted/suppressed 且 related_alert_ids 存在时返回关联 Alert 列表；
- 查询组合多个对象时不承诺事务快照。

## 物理资源

- MySQL：`linkd_events`、`linkd_alerts`、`linkd_alert_logs`。
- Elasticsearch 稳定读 alias 为 `<prefix>-events`、`<prefix>-alerts`、`<prefix>-alerts-active`、
  `<prefix>-alert-history`、`<prefix>-alert-logs`。Event、AlertHistory、AlertLog 物理索引按 UTC 时间桶创建，
  Active Alert 使用单一热索引；Active 和非 Active 时间桶的 `refresh_interval` 分别配置，默认均为 5 秒；
  AlertLog 的 translog durability 可独立配置，默认 `async`；
  时间桶默认 7 天。
- 数据进程只通过 `require_alias=true` 写入 Bucket Manager 创建的 per-bucket write alias。
  `control-plane` 分别装配 Schema 与 Active 资源对账、时间桶维护和遗留终态 Alert 归档任务；三者共享连接，
  前两项使用独立周期，归档使用连续批量循环，且没有各自的常驻 command。缺失目标时数据写入和归档失败，
  不在调用路径自行建索引。
- ES `_source` 直接保存完整 Event、Alert 或 AlertLog 领域字段，不使用通用 `payload` 包装。EventProcessing
  作为 Event 根字段旁的 `processing` object 保存，Lifecycle 查询使用 `processing.state`。
  Alert 另存派生查询标记 merge_work，仅用于内部工作扫描；它不是领域状态，不进入 Alert 输出信封。
- ES mapping 为 strict；dimensions/labels 使用 flattened。`source_raw_data`、`extra_data`、`enrich` 和
  AlertLog `params` 显式存在但 `enabled=false`，展示文本显式存在但不建立倒排索引或 doc values。
- Event Enrich 的四个新增 mapping 字段由索引对账在核对归属与 schema 后增量补齐，不删除现有数据，已有字段类型冲突仍报错。
- Bucket Manager 同时核对读 alias 下已存在的历史桶，按物理名称、桶边界与 managed metadata 验证归属，再增量补齐字段；不受近期预创建窗口限制，也不为升级创建中间历史空桶。历史桶数量仍受每类硬上限约束。
- 主动关闭和快捷屏蔽的 `end_operation`、`last_shield_operation` 与策略字段一起增量维护，覆盖已有 Active 和 History 索引；只增加 opaque object mapping，不回填历史操作身份。
- 当前 ES schema version 为 3；索引 mapping `_meta` 记录实体、role、桶周期与起止时间。配置不匹配时
  对账任务失败，不自动迁移或删除已有数据。
- Alert 与 AlertHistory 的 `_source` 和 mapping 完全一致。Lifecycle 的终态 CAS 只更新 Active，成功后
  不等待物理搬迁；归档任务持续扫描终态文档，由有界 Worker 先 Bulk create-only 写 History，再只对已确认
  存在于 History 的项目按 Active 原版本 Bulk delete。单项失败保留 Active 并在后续扫描重试，不回滚
  已成立的逻辑终态，也不终止其他归档项或数据面进程。
  带有 merge_change/policy_change 待输出意图的终态 Alert 暂不归档，防止 Active 工作扫描失去补偿入口；
  搜索排除与单项归档校验均执行该规则，元数据清理完成后恢复普通归档流程。
- Event、Alert 和 AlertLog 使用租户与对象 ID 摘要作为 `_id`；Alert CAS 使用
  `_seq_no/_primary_term`。
- 本轮不自动删除旧桶；每类物理桶有硬上限。历史回放必须先用 `linkd storage prepare --from --to`
  显式准备目标范围。

## 屏蔽与控制面工作

Alert 的 `shield`、`admission`、`policy_tags` 及 `policy_change` 与业务快照一起保存。控制面扫描端口
`ShieldWorkStore.ListShieldWork` 是内部全局工作枚举，每行返回明确的租户、Alert 和版本；执行端必须按该租户
实时重读并取得与 Lifecycle 相同的 fingerprint lease。该端口不作为无租户管理查询 API 暴露。

扫描同时发现活动屏蔽和已解除但输出意图未完成的记录，按租户/AlertID 分页；未到复查时间的行仍推进游标。
新插入到游标之前的工作由下一轮发现，不承诺扫描期间的跨对象事务快照。Redis 提示丢失不影响持久化扫描。
ES 在已验证归属的活动及归档索引追加策略 mapping；MySQL 追加 `policy_work` 标记和组合索引，写入与 Alert CAS 同步。
当前首次引入该标记时，旧记录没有屏蔽/待输出状态，默认 0；不会清理已有业务数据。

管理面使用独立 `ShieldAlertReader.ListShieldAlerts`：明确租户、按 Alert ID 升序、单页最多 16 条，
包含当前屏蔽及待完成输出意图，不按复查截止时间过滤。API 在此基础上收紧为每页四条并筛选固定绑定；
已解除历史通过租户内 `ListAlertLogs` 读取。Memory/ES/MySQL 均实现该端口，读取不修改工作标记或关系。
ES 查询只读 Active 副本；终态 `policy_change` 完成前归档器保留 Active，完成后由历史 Alert 和日志支持精确查询。

提示检查另使用 `ShieldDependencyReader.ListShieldDependents(tenant, main, after, limit)`，按同租户当前主
枚举最多 16 个子 Alert；跨 EventSource，不受 next_check_at 限制。时间屏蔽、已解除但待输出的历史
绑定不属于当前子关系；定时工作端口仍负责它们。返回值校验租户、顺序、版本与完整绑定，执行前再取得
子告警 fingerprint lease 实时重读，提示不能直接改状态。

ES 从 Active 查询 `shield.active` 与派生 keyword 数组 `shield_main_alert_ids`；后者为当前主 ID 的排序
去重集合，随普通/合批 Alert 创建与 CAS 编码一同写入。mapping 仅追加，不回填已有文档；缺少加速索引
的旧记录仍由定时扫描发现，后续写入会生成索引。MySQL 先按租户和 policy_work 缩小候选，再对同次 CAS
保存的 payload 执行 JSON 主绑定过滤，不维护另一个异步关系副本；数据库内过滤成本仍受该租户工作集合
大小影响，接口只承诺返回页和查询时间有界，不宣称主 ID 已有独立物理索引。Memory 使用相同当前绑定语义。

控制面状态 CAS 保存 `policy_change`，清除该意图不改变业务 `update_at`。这保证输出尝试和流水可以补齐；
现有普通 Hook 仍是失败记日志。P6 已具备持久化同步水位及补扫端口，任务单步生产/投递/重试已实现，目标配置和生产自动调度仍待完成，不能将普通 Hook 输出等同于可靠投影。

屏蔽最近诊断与手动复查请求使用独立 `shield_checks`、`shield_requests` 集合，不写入 Alert 业务快照，
也不引起 KAC 投影水位推进。请求的 pending 工作标记与 payload 原子保存，按租户/工作标记/身份建索引；
读取历史与有界 pending 扫描分离。屏蔽检查/请求每条最多 256 KiB，以容纳 16 个绑定和 256 个候选步骤；其他控制请求保持各自原预算。每租户最多 1024 pending，在准入租约内计数。
ES 创建 pending 等待搜索可见，完成和最新诊断不等待刷新；精确 GET/CAS 是状态依据。
MySQL 使用部署 namespace 隔离，ES 使用部署专属集合；全部键和记录同时保留租户与 Alert 范围。
完整身份、状态、重试及有效值见[屏蔽复查契约](../reference/contracts/policy-runtime-api.md#屏蔽复查与请求)。

## 终态抑制清理记录

`suppression_cleanups` 独立于 Event/Alert 业务存储与 Redis 运行态，记录终态清理意图和逐种类确认。
每条最多 2 MiB；按部署隔离集合、按租户前缀及稳定终态身份保存，create-only 与后续 CAS 不分离。
不推进 Alert revision、投影水位或处置资格，也不保存业务 payload、凭据及原始错误。

两类确认各包含最多 512 项窗口 ID/代次，数量与删除登记数一致，由执行删除的同一 Lua 原子捕获。
防抖元信息已丢失的残余引用显式标记 missing，不推算原代次；Redis 失败不保存部分明细为完整确认。
2 MiB 预算包含所有字段上限及最坏 JSON 转义，超限拒绝保存，不能静默截断。

正式 Worker 复用一份有界文档连接；低频人工关闭/合并终态处理器在实际清理时才打开并关闭连接，
普通屏蔽检查不初始化此依赖。清理前先保存 pending，结果确认后 completed；全流程受调用方
fingerprint lease 与十秒上下文约束。ES 不等待搜索刷新，精确 GET/CAS 推进，历史列表允许延迟。

核心记录失败返回原业务流程重试；已经落库的终态不会回滚。Redis 普通失败保存 unavailable 后继续，
不把失败计为零，也不为这类记录另建自动重建任务。pending 被原调用接续时标记此前结果未确认，
不能根据重试的零删除推断首次没有副作用。完整字段与查询范围见
[清理历史契约](../reference/contracts/policy-runtime-api.md#终态抑制清理历史)。

## 抑制显式对账请求

`suppression_requests` 保存租户、窗口、原 owner/代次、operation、认证操作者、原因及最终检查结果，
单条最多 64 KiB；与终态自动清理记录分开。相同命令重用稳定请求身份，不允许换 owner/代次后重投。
记录时间去除进程内单调时钟并统一为 UTC，避免存储往返后误报同一结果冲突。

pending 工作标记和 payload 在 ES/MySQL 同次 CAS 写入；待办扫描只读 pending，历史按租户和窗口分页。
工作计数/扫描复用屏蔽请求的有界存储实现，但集合必须在固定白名单中，两类请求不共享业务状态。
每租户准入锁内限制 1024 项待办；ES pending 写等待搜索可见，最终结果不等待刷新。
这不是 Redis/数据库之间的强事务配额，正常运行仍依赖租约和搜索可见性条件。

后台使用正式 Lifecycle 的 tenant/source/fingerprint lease 重读窗口及实时 Alert，条件删除原 owner/代次；
有效聚合候选占位、未绑定防抖和有效活动 owner 保留。started_at 先于执行写入，取消或结果写失败保留
pending，接续时保留 previous_unconfirmed。最终记录不再执行，依赖失败保存 failed，重新检查需新命令。
对账不修改 Alert/Event、业务 revision 或处置资格。路由、错误分类和执行预算见
[抑制受控对账](../reference/contracts/policy-runtime-api.md#抑制受控对账)。

## 合并控制面工作

`MergeWorkStore.ListMergeWork` 按租户、AlertID、窗口 ID 分页，单页最多 16 个窗口/意图；窗口 ID 为空
表示先处理 merge_change。读取包含游标所在 Alert，按排序后的窗口继续展开，不能用“处理了一个 Alert”
越过其中剩余窗口。满页返回最后一项游标，最后一次请求允许空页；新插入到游标之前的工作下轮发现。
没有最近天数或“必须有新 Event”的限制，核心读取错误不返回部分成功页。

ES 的 merge_work 布尔字段和 MySQL 的 merge_work 标记及组合索引，由 pending 引用或 merge_change
派生，与 Alert 同次写入/CAS 更新；Memory 使用相同领域判断。首次引入前不存在正式合并工作记录，
仅追加 schema，不自动改写业务数据。终态意图完成清理后标记自动消失。

上述扫描和丢失等待释放用例已接入独立合并控制任务。丢失释放前必须直接
查询租户/窗口对应的持久化裁决；found 或存储错误都阻止按丢失窗口释放。调用方须按窗口串行执行
裁决创建、关系变更和丢失释放，再由成员操作取得自身 fingerprint lease。

裁决 progress.capture_offset 保存已确认快照的前缀；progress.window_finished 只在业务完成后的 Redis
提示清理成功后设置。业务 completed 但提示清理尚未确认的裁决仍出现在工作扫描中，不能因瞬时 Redis
错误永久占用每租户的提示容量。两者都不改变已冻结策略、成员集合或父 Event。

ES 的 `merge_decisions`、`merge_members`、`merge_relations` 和 `merge_relation_refs` 写入使用
`refresh=false`：成功代表写入已确认，按固定身份的实时 GET/CAS 可以继续推进；列表与后台扫描等待索引
刷新，不能以暂时搜索不到认定业务不存在。扫描到末页后重新从头发现新工作。策略配置、发布版本和
配置发布操作仍使用 `refresh=wait_for`，保留发布后的搜索可见性约定。

关系复核按实时 Alert 逐项保存不可逆的 `terminal` 确认，每步最多 16 项，同一次任务最多 16 步且仍受
10 秒期限限制。超时、取消或读取失败后保留已确认项，下轮跳过这些项；错误/缺失不能作为终态证明。
全部选中成员确认后，Lifecycle 在父 fingerprint lease 内重读关系并执行父恢复 CAS；人工关闭父优先保留
原有终态原因。结束后的解除仍每步处理一个等待身份，不恢复子 Alert，也不补发子处置。


## 合并显式请求存储

`merge_requests` 单条最多 64 KiB，复用固定请求集合白名单的 payload/work 原子 CAS 和 pending 索引；
每租户准入锁内最多 1024 pending。与自动任务共用原 Journal、Executor、窗口租约和四个执行名额，
不能由请求重置原业务 outcome 或阶段。控制点 token 绑定精确记录的真实存储版本；开始后业务变化与
最终诊断不构成跨文档事务，未确认结果用 previous_unconfirmed 保留。
正常接续仍按原稳定 Event/Alert/关系身份幂等；已完成命令只读原结果。ES pending 写等待搜索可见，
最终结果不等待刷新，历史页与精确读取分开解释。完整语义见
[合并受控接续与关系检查](../reference/contracts/policy-runtime-api.md#合并受控接续与关系检查)。

## 投影待办与归档

投影任务存储新增按 namespace/租户/work 的有界计数索引；生产和人工恢复共用租户准入租约，
最多 1024 条未完成任务，失败/成功历史保留且不占执行队列预算。满载不清除 Alert 未确认水位。
自动运行器通过全局 KAC 插件装配，直接维护兼容 ES 文档并可靠通知处置；完整约束见
[投影自动运行器](../reference/contracts/kac-alert-projection-v1.md#自动运行器)。


ProjectionWorkStore.ListProjectionWork 是控制面内部跨租户枚举端口，游标按租户/AlertID/target_id
排序，每页限制 1..16 个目标，同行剩余目标继续翻页。条目带明确租户和物理版本，执行前须按租户实时
重读当前 Alert；它不是无租户的管理查询 API。扫描没有最近时间或 active 限制；新插入游标前的工作
由下一轮发现，不提供跨对象事务快照。

ES/MySQL 与 Alert 同一次写入派生 projection_work 标记；ES 使用覆盖 Active 和所有 History 的别名，
MySQL 使用标记和租户/AlertID 组合索引。ES 每页最多读取 2*(limit+1) 个物理命中，归档过渡最多允许
两个副本；超预算、非法记录、超时或分片失败均返回错误，不能当作已完整扫描。普通目标水位留在
JSON 中，ES 不为任意 target_id 动态创建 mapping。

终态即使投影未确认也可以归档；历史副本继续参与补扫和 ACK CAS。History create 成功而 Active
条件删除失败时，重试先核对业务快照、绑定和 required_revision，再合并每个目标的较高 synced_revision。
合并不推进业务版本；Active 在此后又收到 ACK 会使条件删除失败，下一轮继续收敛。业务差异仍报身份
冲突，不自动覆盖。

查询跨索引重复副本时，只返回确认水位覆盖另一副本的真实文档与其原始 CAS token，不拼接虚拟文档；
两边确认互不覆盖时等待归档收敛并报告冲突。补扫结果不代表远端已应用或已可搜索；任务单步用例已实现独立确认与重试；生产控制循环和真实 KAC 接收端仍待接入。

## 独立投影任务存储

projection.Store 与核心 Repository 分开，只承诺单个任务的原子条件写，不承诺任务与 Alert 事务。
ES 使用部署隔离的 projection 索引，MySQL 使用 linkd_projection_tasks 及 deployment namespace。
请求和目标引用在首次创建后不可变；业务 ID 固定为租户/Alert/目标/revision 摘要，排队时间不进入身份。

payload 和 work 在同次写入内保存，状态转换前以当前版本验证，重复创建不能重置进度。
delivered 仍属于待办，succeeded/failed 退出自动待办；失败记录保留供租户管理查询及人工恢复。
查询页大小为 1..16，支持当前租户全量状态或内部跨租户 work-only 枚举，部分 ES 查询结果返回错误。

MySQL payload 使用 LONGBLOB 保存规范请求的原始字节，避免数据库 JSON 重排破坏已冻结的内容摘要。
ES 记录相同的规范 JSON 字节，往返与重开通过真实后端契约测试。单文档上限 1 MiB + 16 KiB，
ES 单次响应上限 32 MiB，覆盖最多 16 个完整任务的读取预算。没有自动 TTL 或失败历史回收。

远端成功确认先落任务 Receipt，再 CAS 逐目标 Alert 水位，最后将任务标记 succeeded。
三步之间允许重试：已保存 Receipt 时不重发外部请求；本地 ACK 已生效但任务尚未完成时，重复 ACK
无副作用。具体状态和协议见 [KAC Alert 投影 V1](../reference/contracts/kac-alert-projection-v1.md)。

## 独立动作任务存储

ActionDelivery 与纯状态投影分开保存，使用 ES 部署专属 action 索引或 MySQL linkd_action_deliveries。
业务身份为租户/Alert/目标/revision 摘要；请求、原来源 Release 和首次创建时间不可变。单次 CAS 同时
保存 payload、work、unsettled、Alert/目标/revision 索引，读取时核对索引与载荷一致。它只保证单任务
的原子条件写。Lifecycle 另以 Alert.action_pending 和业务同次 CAS 保存原动作意图：提交先保证意图持久，
全部任务持久且排序可见后才清除意图，不宣称 Alert 与任务存储具有跨文档事务。

自动待办为 pending/waiting_projection/sending/retry，每租户最多 1024 条；failed 保留历史并退出自动
预算，但仍是 unsettled 顺序屏障。最早未结清查询限定租户/Alert/目标，按业务 revision 取一项，执行前
在与投影共用的目标租约内实时重读。只有 succeeded/skipped 解除屏障；后续任务不能越过前序失败。
人工恢复和生产共用租户准入锁，原请求或原恢复命令在满额时仍可重投。

ES 写入等待刷新；响应未知后的重复 Record 即使实时 GET 命中，也必须由 ConfirmVisible 确认已进入
排序索引再承认入队。尚不可见则返回 ErrBusy，生产者保留业务意图并背压，不能直接继续更高版本。
列表每页最多 16 条，最早未结清只读一条；部分查询、超时、分片失败和非法派生索引都返回错误。
单任务最多 1 MiB + 4096 字节请求再加 16 KiB 元数据，ES 响应最多 32 MiB，没有自动历史过期。

发送前 CAS 保存 sending 和尝试期限。HTTP 受理确认先验证后保存 succeeded/skipped；不修改 Alert
投影水位。结果保存失败后仍保留 sending，后续按同一 action_id/request_hash 重投，由接收端持久去重。
较新终态可见允许旧 firing 跳过，但保留此前结果未知标记，不倒推此前从未受理。完整请求、状态转换、
人工恢复及验证范围见 [KAC 动作投递 V2](../reference/contracts/kac-action-delivery-v2.md)。

## Alert 动作入队待办

`action_pending` 最多保存 16 个显式动作目标的来源发布、原 revision/action/cause，不复制完整 Alert。
仓储禁止有意图的 Alert 推进下一业务版本，当前业务字段就是不可变动作快照；投影 ACK 可继续推进。
所有任务入队后以原 CAS 清除意图，revision/update_at 不变；允许清除确认与投影 ACK 竞争后重读补齐。
新建/业务替换校验要求获准动作与意图同时存在，元数据白名单不能更改目标开关、原因或原快照。

Memory/ES/MySQL 实现 `ActionWorkStore.ListActionWork`，每页 1..16 个 Alert，按租户/AlertID 严格递增，
满页返回最后一项游标，扫描到末页后从头发现新工作。每项带真实存储版本，执行前须在 fingerprint lease
内实时重读。该端口为内部跨租户工作发现，不是无租户管理接口。异常载荷、错误身份、部分 ES 查询或
缺失 hits 返回错误，不能当作空页前进。

ES `action_work` 和 MySQL 同名列/组合索引与 payload 原子写入，新增 mapping/索引不重写历史业务数据。
ES 扫描 Active 索引，终态意图未清除时自动归档候选和直接归档均拒绝搬迁；确认入队后恢复归档，
独立 ActionDelivery 继续保存冻结快照和失败记录。独立动作运行器已利用该端口在正式 fingerprint lease
内补扫，双后端验证无新 Event 时的入队和重开恢复；不从扫描快照直接构造动作。
循环预算与游标规则见[动作自动运行器](../reference/contracts/kac-action-delivery-v2.md#自动补扫与发送运行器)。
正式来源绑定和生产进程的周期任务装配仍待完成。
