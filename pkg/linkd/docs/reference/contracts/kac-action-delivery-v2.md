# KAC 动作投递 V2

2026-10-08：V2 将回执收敛为 Celery 投递确认，替代原 V1 持久受理要求；不接受 V1 回执。
全局 `plugins.kac` 同时启用兼容 ES 写入和可靠动作投递。
已实现内部请求、持久任务、投影 Gate、按序入队、有界重试、管理 API 与 Console 运维入口。
KAC 动作接收端仍须在实际应用中接入，本地协议模拟不等于真实 KAC 联调。

全局连接和鉴权见[配置指南](../../guides/configuration.md#kac-全局插件配置)。
本契约与[稳定 Alert 投影 V1](kac-alert-projection-v1.md)配合使用，旧 [KAC Alarm 输出](kac-alarm-output.md)
仍保持自己的协议。动作请求只发送到显式实现 `linkd.kac-action.v2` 的独立 HTTP 入口，不能投递到旧 pipeline。
Alert 已保存任一可靠动作绑定时，旧 KAC Hook 在运行时跳过输出；来源后来重新配置旧 Hook、
可靠投递失败均不触发旧通道回退。普通状态 Hook 继续输出。
全局插件对所有来源及租户启用同一目标 kac，不再提供逐来源的仅投影配置。

## 动作与快照

一个 ActionDelivery 保存一条已经获准的动作，不从最新 Alert 扫描合成历史动作，也不合并掉中间处置。
当前构造器接收原 Alert 快照、目标及原 cause，直接按已确认事实生成：

| action | 必需事实 |
| --- | --- |
| firing | active；未屏蔽、未被合并等待/关系阻塞；admission 等级与当前等级相同；cause 与本次 admission 完全相同；admitted_at 等于本次 update_at；source_event 原因还须等于 latest_event_id |
| resolved | recovered，且本生命周期曾获准处置 |
| close | closed，且本生命周期曾获准处置 |

因此 active、定时解除屏蔽、父关闭后的解联本身都不是新触发资格。此前已放行告警的终态仍独立投递。
动作在 Linkd 内冻结原 Alert 业务快照，延迟发送和重试不刷新它，以保持原因、身份和请求摘要稳定。
KAC 按 alarm_id 读取处理时的 ES 数据进行匹配、快照和通知，不要求逐版本重放原快照；例如 warning 动作
排队期间升级为 critical，处理时可以使用 critical 数据。原请求只用于动作语义和追踪，不得写回并覆盖当前投影。
KAC 处理 firing 时仍检查当前结束、屏蔽和合并状态，不让延迟任务重新激活终态；不再执行 Linkd 已承担的三类策略。

`alert` 与投影 V1 共用规范业务 JSON，排除 projection、policy_change、merge_change、action_pending 和 shield.next_check_at。
单个业务快照最多 1 MiB，动作信封最多 1 MiB + 4096 字节。原请求必须由构造器形成；不接受保存往返会改变
字节表示的非规范 RawMessage。数字和复杂丰富值沿用稳定 JSON，不使用 Python 文本表示。

## 请求身份与确认

使用 POST、JSON Content-Type 和 `Internal-Token: Bearer <token>`。URL 来自部署级 plugins.kac.action_endpoint，
租户通过请求体 `bk_tenant_id` 显式传递，不发送 APIGW 专用的 `X-Bk-Tenant-Id`；
只接受 HTTP/HTTPS，不允许 userinfo/query/fragment，不跟随重定向。凭据仅在执行时解析，不写入任务。

| 请求字段 | 约束 |
| --- | --- |
| schema_version | 固定 linkd.kac-action.v2 |
| action_id | 下述稳定动作摘要，64 位小写十六进制 |
| bk_tenant_id / target_id | 明确租户和目的端，各为 1..64 位字母、数字、下划线或连字符 |
| linkd_alert_id / alarm_id | 原生命周期及投影 V1 生成的固定兼容身份，不能按动作创建另一条 alarm_event |
| linkd_revision | 原动作业务版本，1..2^53-1，同时是最低要求的可见投影版本 |
| action | firing/resolved/close，不接受其他拼写 |
| cause | type 为 source_event/user_operation/system_operation；id 为原 Event/操作身份，非空且最多 160 字节 |
| content_hash / alert | 原业务快照摘要与完整快照，不含投递尝试元数据 |

动作 ID 为以下紧凑 JSON 数组的 SHA-256；revision 使用 JSON 整数：

```text
["linkd:action:v1", tenant, AlertID, target_id, revision, action, cause.type, cause.id]
```

任务 ID 则为 `["linkd:action-task:v1", tenant, AlertID, target_id, revision]` 的 SHA-256。
一个 Alert/目标/版本最多一条动作任务；同一版本的不同原因或内容必须冲突，不能另建新任务绕过幂等。
原请求的完整摘要为 Linkd 发送的规范 JSON 请求字节的 SHA-256，包含 action、cause 和全部快照。
接收端可直接对原始请求字节计算 request_hash，不能用另一种语言默认 JSON 重编码后的文本替代。

HTTP 200 还必须返回以下明确确认，响应最多 64 KiB，拒绝未知字段、额外 JSON 和非法类型：

| 确认字段 | 含义与校验 |
| --- | --- |
| schema_version、bk_tenant_id、target_id、linkd_alert_id、alarm_id、action_id | 与原请求完全一致 |
| request_hash | 原完整动作请求摘要，不只是 Alert 的 content_hash |
| outcome | 固定 queued，表示本次调用已投递 Celery |
| task_id | 非空、1..256 位字母/数字/下划线/连字符；本次 Celery 任务 ID |

KAC 必须在实际投递成功后返回确认；投递失败返回非成功响应，不把仅构造 task_id 当作完成投递。
不要求持久受理中间表、固定 acceptance_id 或重复返回原 receipt；同一 action_id 和 request_hash
重试可以得到不同 task_id。回执不再包含 applied_revision/applied_status/search_visible，这些证明由
Linkd 自身的投影 Gate 管理。不能用动作回执推进投影水位。

**queued 只证明已向 Celery 投递，不证明通知、工单或自动处置完成，也不保证业务副作用恰好一次。**
投递后响应丢失、本地保存确认失败，都可能引发相同动作重复入队。Linkd 保存最近一次确认的 task_id；
不会查询 Celery 执行结果或据此补发。收到确认之后的执行失败、任务重试和日志归 KAC 管理。


## 投影门槛与原版本顺序

Service 在同一个 Alert/目标租约内读取最早 unsettled 动作、实时重读它，再检查 Gate。
Gate 使用同一租户/Alert/全局目标的持久确认，校验业务溯源、摘要、版本及搜索可见性。本地已有较新终态时，至少等待该终态投影可见，再让旧 firing 跳过，
不能只复用旧活动版本水位。`ProjectionGate` 已接入正式控制面；目标由正式 Worker 根据全局插件自动绑定，opening 来源版本只作业务溯源。

- Gate 未就绪：保存 waiting_projection，下一秒再查，不消耗发送/失败尝试次数。
- Gate 已确认较新终态：保存 skipped/superseded_by_terminal，不发送旧 firing。
- Gate 确认可投递：先持久化发送尝试，再解析原目标并发送同一冻结请求。
- Gate 依赖异常：计入有界失败重试；非法确认永久失败，不能按“未命中”或空队列继续。

`ProjectionGate` 通过窄读取端口，在五秒总预算内最多执行两次实时 Alert 读取和一次精确投影任务读取：

1. 校验业务仓储返回的租户、Alert、来源、业务版本、目标绑定和非空存储版本。目标的 SourceVersion
   必须与原动作一致；不借用后来来源发布、其他目标或其他租户的同步水位。
2. ACK 未达到原动作版本时等待；本地已经终态时，还必须达到该终态的当前版本。按 ACK 的原请求版本
   计算 `projection.TaskID`，不按回执声明的更高远端版本定位任务，不扫描或挑选另一条看似可用的确认。
3. 校验完整持久任务、存储版本、原业务来源版本/全局目标和搜索可见回执。已保存回执但尚未标记 succeeded
   的 delivered 任务，在本地 ACK 已推进后也可作为证明；裸水位、缺失/损坏证明不能放行。
4. 相同原动作版本要求业务摘要一致；证明或回执对应当前 Alert 版本时，再与当前真实业务摘要比较。
   ACK 和待输出元数据不进入业务摘要。远端更高版本的既有协议语义保持不变，不推升本地 ACK。
5. 返回前再次实时读取 Alert；业务版本或相关水位已变化时返回 ErrBusy，留给下轮检查。
   存储和取消错误保留错误链，错来源发布/作用域/回执按非法证明处理。

Gate 不更新绑定、ACK 或动作，不读取来源配置，也不请求 KAC 状态接收端。
控制面使用部署级全局出口；来源发布不再影响路由。共享 ES 位置应与 KAC 一致，公共插件配置变更后重启进程。
两次读取与业务 CAS 之间并非强事务，KAC 仍须在任务处理时拒绝旧触发复活较新终态。
尚有动作依赖的投影确认记录必须可读取，不能只因任务进入 succeeded 就删除证明；目前任务仓储保留这些记录。

投影与动作共用 `projection.TargetLockKey` 和相同部署的 Redis Locker，单目标串行，其他告警独立推进。
投影可能在请求途中或 Celery 排队期间更新，因此 KAC 任务处理时仍须
复核当前文档，拒绝旧 firing 重新激活较新终态，不再次执行 Linkd 已经承担的抑制/屏蔽/合并。

任务查询的排序依据是业务 revision，不是哈希 ID 或创建时间。failed 保留 unsettled=true：前序故障不能
让后续同目标动作乱序执行。较新任务被调度时，Service 可能先推进该目标较早的任务，返回实际处理项。
前序 failed 可在较新终态可见后安全跳过；否则保持屏障，等待修复后恢复，其他 Alert/目标不受其影响。

此顺序只覆盖已经持久化的动作。Lifecycle 已通过下述原子意图补齐生产边界；独立调用 Record 的其他
生产者仍必须持久保留原动作并按版本入队，任务库不能修复根本没有保存过的业务意图。

同 Alert 的顺序只约束 Linkd HTTP 投递，不保证 Celery 的实际执行/完成顺序；KAC 处理时读取当前 ES 状态。

## Lifecycle 原子动作意图

正式 Worker 用 `WithInitialProjectionTargets` 注入全局目标 kac，动作开关固定为 true，
同时通过 `WithActionRecorder` 注入可靠入队端口。无需读取来源出口配置，也没有逐租户凭据映射。
冻结计划重投保留原目标和原动作身份；本版不设计首次启用回填或旧目标迁移。

`actiondelivery.Recorder` 独立提供 Record/RecordAction，仅依赖任务写入、待办预算、排序可见性和
同部署准入租约；不持有投影 Gate、HTTP Sender、全局出口解析器或 KAC Token。完整 Service 复用同一
Recorder，与发送共享四个执行名额，重投/容量/原快照冲突的行为保持一致。
Worker 在进程内共享一个 Recorder 和任务连接；人工关闭、屏蔽及合并控制共用的处理器只在真正入队
时短暂打开连接，并传播持久化或关闭错误。两种入口均使用 OpenExisting，不创建任务表或索引，初始化
仍归控制面负责。即使部署已停用自动投递，已有 Alert 的获准动作仍可靠入队，不能因缺接收端凭据丢弃。

| action_pending 字段 | 约束 |
| --- | --- |
| revision / action | 原业务版本和 firing/resolved/close，与当前 Alert 一致 |
| cause_type / cause_id | 原 Event 或用户/系统操作身份，补扫不得重造 |
| targets | 所有明确启用动作的目标到 source_version 的映射，最多 16 项；不含 endpoint/凭据 |

Event 计划在策略准入后冻结意图；创建/升级/来源终态、直接关闭、合并释放/父就绪/父恢复均将其与
Alert 放行或终态写进同一次创建/CAS。重复触发、定时解除屏蔽、父关闭后的子解联不生成新动作意图。
业务 CAS 前检查实际动作协议及快照预算，拒绝把无法编码的载荷提交成永久待办。

```text
获准业务变更 + action_pending（同次 CAS）
  → 按原目标逐项幂等 Record，并确认每项已进入排序查询
  → 清除 action_pending（只更新元数据）
  → 允许推进下一业务版本
```

意图存在时，仓储拒绝下一业务版本，当前 Alert 就是原动作快照，不额外嵌套一份完整 Alert。
投影 ACK、复查时间和其他已完成输出标记仍可更新；这些元数据不进入动作摘要。清除意图不改变
revision/update_at、admission 或投影水位。ACK 并发造成清除 CAS 冲突时，重读并重投原任务后再确认。
事件计划的幂等比较忽略已经清除的意图，因此补扫先完成入队不会让原 Event 重试变成身份冲突。

全部目标共享十秒入队预算。部分成功、队列满、取消、写响应未知或清除意图失败均保留可重试边界：
已存在任务不改首次时间/内容，未确认目标继续入队。错误向输入或控制任务传播，不按普通 Hook 错误
吞掉；上一个动作无法入队时，同 Alert 后续升级或关闭也会背压。入队成功不等待远端投影/处置完成。

`FinishActionDelivery` 在调用方持有的 tenant/source/fingerprint lease 内重读并补齐原意图，不重新执行
Enrich 或策略。Memory/ES/MySQL 提供 ActionWorkStore，每页最多 16 个 Alert，按租户/AlertID 扫描，
与 payload 同次写入 action_work 标记。ES 在终态意图清除前暂缓归档；任务入队后可归档，后续投递依靠
独立任务继续。意图缺失只表示当前没有入队待办，不能解释为远端已经受理。
正式控制面已装配下述 action-enqueue 周期补扫与 action-delivery 发送循环，由全局插件统一启用。

## 持久状态、重试与不确定性

任务固定 request、source_id、source_version、created_at。source_version 从原快照的绑定目标读取，
required_revision 必须等于该快照 revision；创建动作不要求同步已完成。记录保存业务来源版本，不保存出口路由或凭据。
任务最大为请求预算再加 16 KiB；所有尝试、确认和最近恢复信息有界，不在任务内追加无限错误历史。

| progress.state | 含义 |
| --- | --- |
| pending | 新动作或人工恢复后的待办 |
| waiting_projection | 等待必需投影可见，保留原动作 |
| sending | 已持久预留一次尝试及 30 秒期限，不证明 HTTP 已实际发出 |
| retry | 临时失败后的有界退避 |
| succeeded | 已取得 Celery 投递确认，后续不再发送；不等于业务处置完成 |
| skipped | 本次因更高终态停止发送，保留原因及证明 |
| failed | 永久失败或自动预算耗尽，保留历史和顺序屏障 |

每个 Service 最多四项执行，单项十秒总预算包含排队；租约至少 30 秒，释放最多两秒且不受原取消影响。
每周期最多八次发送/失败尝试，退避为 1、2、4、8、16、32、60 秒。普通等待投影不消耗次数。
任务 attempt 与 total_attempts 分别记录本周期和累计次数；generation 初始为 1，只在人工恢复时递增。

网络响应未知、KAC 已投递但本地结果保存失败或 sending 中途退出时，等待原期限后重投相同动作身份
和请求内容。相同动作可能重复投递；不再要求接收端持久去重账本，也不承诺 exactly-once。
previous_unconfirmed 保留早先发送结果不确定；随后旧动作变为 skipped，只说明本次不再发送，不能据此
认定此前从未产生处置。已知目标解析失败和明确未授权不会伪造成已受理；其他不符合确认契约的响应保守记录不确定性。

失败分类只允许 projection_pending、projection_unavailable、target_unavailable、transport_failed、
remote_unauthorized、identity_conflict、remote_unavailable、remote_rejected、response_too_large、
response_invalid、visibility_pending、attempt_interrupted、superseded_by_terminal；等待和跳过代码不充当普通失败代码。
不保存远端错误正文、URL 或凭据。锁忙/预算满可延后；混合租约释放、存储或取消错误不能按普通忙吞掉。
Service 返回 nil error 也可能表示已保存 retry/failed/skipped，调用方必须检查任务状态。

独立 Retrier 只恢复 failed，要求完整租户、任务、当前 CAS token、operation_id、操作者和非空白原因。
它保留原请求、发布引用、首次时间及累计次数；同一个最近命令重投不重置预算，内容改变返回冲突。
仅保存最近一条恢复记录，不宣称完整人工操作审计；该用例已接入下述正式管理 API。

## 自动补扫与发送运行器

`actiondelivery.Runner` 包含独立的 action-enqueue 与 action-delivery 循环，构造不启动后台工作。
前者每五秒发现一次持久意图，后者每秒发现一次自动待办；首次立即扫描，成功满页连续向后推进。
每页最多 16 项、扫描最多五秒、页面执行上下文九十秒，单项最多二十秒（包含等待共享名额）。
两个循环共用四个执行名额；底层生产/发送用例仍执行自己的十秒及有界清理约束。
取消后等待已启动用例释放租约，页面上下文期限不等于进程可以跳过清理的强制退出时间。

补扫在一页内按租户串行，同 Alert/目标的发送串行，其他组最多四路。稳定游标只表示工作发现位置，
不能决定业务发送顺序；实际发送仍由 Service 读取最早未结清版本。单项失败不阻塞后面扫描项，
扫描失败保留游标；取消或排队超时只推进真正执行过的连续前缀，后面已完成组可以幂等重读。
未到 due_at/lease_until 的任务延后；failed 历史不参与 work-only 扫描，但仍可阻塞后序同目标动作。

`actiondelivery/producer.Producer` 只接收租户和 Alert ID，先精确读取，再取得与正式 Worker 相同的
`Lifecycle.ForSource` / `CorrelationKey(tenant, source, fingerprint)` Redis 租约并重读。
租约至少三十秒；十秒预算含排队、读取和入队，释放使用独立两秒上下文并保留释放错误。
锁内核对来源、指纹、租户和业务版本，只补齐原意图，不重新丰富、匹配策略或读取最新 Release。
意图已被其他实例清除时返回当前事实；成功只保证任务入队，不证明KAC 已投递。
装配方负责提供正确的 RecentAlertCache、ActionRecorder 及客户端生命周期。

两阶段分别报告 enqueued、queued、skipped、waiting_projection、blocked、retrying、deferred、
capacity、failed、unstarted。这些是每页互斥的**观察结果**，同一任务可被重复观察，不是唯一动作数、
HTTP 请求数或处置执行数。日志仅保留固定阶段/原因和最多四条经过验证的业务定位样本，
不记录请求快照、URL、凭据或依赖错误正文。指标口径见[动作运行器观测](../../design/observability.md#动作运行器观测)。

运行器和租约适配已在真实 ES/MySQL、Redis 与实际 Lifecycle 上组合验证。正式控制面使用全局出口解析器和上述 ProjectionGate，并将四个阶段的真实存活/页面结果写入任务目录及指标。
启用条件与资源释放见[控制面投递装配](../../../internal/controlplane/process/delivery.go)；全局插件统一覆盖来源与租户。Worker/直接关闭/屏蔽/合并入口已接入仅入队 Recorder，本运行器负责补齐
中断后留下的持久意图；不重新丰富、裁决策略、修改绑定或重建 Redis 策略状态。

## 管理查询与人工恢复

以下路由使用管理 JWT，认证头为 `Internal-Token: Bearer <JWT>`；不是 Worker Token。
正式控制面打开同一部署的 action 任务存储，恢复与动作生产共用同一 Redis 租户准入租约。
查询不产生投递或 Alert 变更，POST 只恢复 failed 任务，不在 HTTP 请求内调用接收端。

| 路由 | 行为 |
| --- | --- |
| GET /api/v1/action-deliveries | 显式租户的有界任务摘要分页 |
| GET /api/v1/action-deliveries/{id} | 实时读取指定任务摘要、原动作/原因、投影依据、受理确认和最近恢复 |
| GET /api/v1/action-deliveries/{id}/snapshot | 单独读取原完整动作请求与 request_hash，不加载最新 Alert 替换它 |
| GET /api/v1/action-deliveries/{id}/order | 观察同租户/Alert/目标最早未结清任务，再实时重读该任务；不表示允许发送 |
| POST /api/v1/action-deliveries/{id}/retry | 根据原 CAS 和完整稳定命令恢复一轮尝试 |

GET 必须提供 bk_tenant_id。列表可选 alert_id、target_id、source_id、state、action、after、limit；
action 只允许 firing/resolved/close，state 与 progress.state 相同，limit 为 1..4，默认 4。
底层按稳定任务 ID 扫描，再对本页筛选，空 items 不代表结束：next 非空时继续。
游标绑定租户及全部筛选条件，不能换租户/动作/状态复用；重复或未知 query 参数拒绝。

摘要不包含 Alert 完整快照、endpoint 或凭据；显式 snapshot 返回 bk_tenant_id/id/revision/request_hash/request。
order 返回 bk_tenant_id/id/alert_id/target_id/observed_at/head，head 是同作用域任务摘要或 null。
排序索引滞后时，实时队首可能已结清，此时返回 null 并由后续查询重新发现，不无界追查下一条。
null 不证明整个目标已完成，也不等于投影已可见；读取/部分查询异常不能伪装成空队列。

恢复请求正文最多 4096 字节，仅接受以下字段：

```json
{
  "bk_tenant_id": "tenant-a",
  "expected_version": "task-cas-token",
  "operation_id": "retry-20261006-001",
  "operator_id": "operator-a",
  "reason": "依赖恢复后重试原动作"
}
```

expected_version 必须替换为 GET 返回的真实 task_version；operation_id 使用 1..128 位字母/数字/下划线/连字符，Console 使用 UUID。
原版本或状态已变化返回 409；同一个最近命令原样重投返回已有结果，修改原因/操作者/版本返回冲突。
确认有自动待办时返回 202，否则返回 200；两者都必须检查完整任务进度，不等于动作已受理或处置已完成。
succeeded/skipped 不可恢复，不允许借重试更换目标、cause、action 或冻结快照。

所有调用共享两个并发名额和十秒期限，满额返回 429。普通摘要/队首页使用 1 MiB 响应预算，
冻结请求使用任务大小预算，恢复结果最多 64 KiB；有效管理请求的响应设置 no-store。纯锁忙/租户队列满可以延后，
混有租约释放或存储错误不能降级为普通忙、不存在或 CAS 冲突。错误响应不带底层地址/正文。

Console 的同源代理固定上述路径，校验作用域、原摘要和状态关系，两个执行名额加最多 16 个排队请求。
操作者取服务端认证身份，浏览器不提交 operator_id。请求未知时保留同一租户/任务的原 CAS、UUID
和原因到 sessionStorage，刷新后重试同一命令；成功核对最近恢复记录后清除。页面分开显示动作入队、
投影可见依据、Celery 投递确认、当前排序障碍与此前结果未确认，不能把本地跳过解释为此前从未处置。

## 存储、可见性与预算

MySQL 使用部署/租户隔离的 linkd_action_deliveries，ES 使用部署专属 action 索引。
单次 CAS 同时写 payload、work、unsettled、Alert/目标/revision 索引；条件写验证全部冻结内容和合法阶段转换。
精确查询不跨租户，管理列表必须指定租户；内部 work-only 可跨租户有界扫描，每页最多 16 项。
最早 unsettled 查询只读取同目标一项，含 failed；搜索超时、分片失败、载荷/派生索引矛盾均拒绝接受。

每租户最多 1024 个自动待办，pending/waiting_projection/sending/retry 计入预算，failed/succeeded/skipped
历史不计入自动队列。Record 与人工恢复共用租户准入锁，满额仍可重投原任务或原恢复命令。
达到上限必须由生产者背压并保留原业务意图，不允许像普通 Hook 一样仅写失败日志后完成业务输入。

ES 写入等待搜索可见性；重复 Record 即使实时 GET 命中，也必须确认该任务已进入排序索引，才允许
生产者确认入队并继续较新版本。ConfirmVisible 不强制全索引刷新，不无限等待；尚不可见返回 ErrBusy。
这覆盖写入响应丢失后的顺序边界，不宣称 Redis/ES/MySQL 间存在跨系统事务。

## Console 运行观测

`GET /local-api/action-metrics` 是 Console 的只读 Prometheus 查询入口，沿用 Console 的路径前缀及登录认证，
不代理任意 PromQL 或地址，也不运行投递。参数固定为 from/to（RFC3339 UTC）、step（1..3600 秒）、
calculation_window_seconds（15..3600，默认 60）及可选 instance（最长 512 字符）。不接受租户、来源、
Alert/任务 ID 或其他参数；范围必须正向、至多七天且不超过 Console.query.maxRangeSeconds，最多 481 个采样点。
页面默认一小时、计算窗口一分钟；按需展开，手动刷新，收起取消读取。

每个 Console 实例的动作与投影指标入口共用最多两个请求，无后台排队；单请求最多四个并发固定查询、总期限取配置超时与
十秒的较小值，浏览器断开也取消。八个面板逐项降级，允许部分可用；一次 Prometheus 响应最多 1 MiB、
64 条时序、每条 481 点。超限、警告表示结果不完整、非法类型/标签/值/时间顺序均使该面板不可用。
原错误正文、endpoint 和鉴权信息不进入浏览器。所有读取返回 Cache-Control: no-store，容量拒绝为 429。

查询按 job/instance/linkd_task 保留进程作用域，固定结果另带 linkd_outcome。运行器水位按同进程累计，
页面数量、年龄和观察时间取同进程阶段值，不求跨进程队列总数；观察距今以查询采样时间减观察时间，
负值提示时钟偏差。计数使用计算窗口内 rate，P95 由页面耗时直方图计算；不补 vector(0)。
NaN/正负无穷转为断点，范围结尾缺失的系列补空点，该空点同样计入 481 点预算；不把历史最后一个样本当作当前值。
浏览器校验返回区间、步长和八个固定面板身份；失败不继续显示上次图表。

任务详情另提供结构化日志定位字段和同租户 Alert 操作流水跳转；进程日志留在原日志出口，尚无
Console 进程日志搜索后端。发送日志在返回已验证的前序任务时使用实际推进的任务 ID；任务尚未返回
可信身份的失败按输入任务定位。入队补扫没有任务 ID；样本缺失不能当作没有失败。
指标定义以[动作运行器观测](../../design/observability.md#动作运行器观测)为准。

## 验证范围与剩余接线

已实现源代码位于 [actiondelivery](../../../internal/actiondelivery/protocol.go)、
[单步用例](../../../internal/actiondelivery/service.go)及[任务仓储](../../../internal/actiondelivery/storage/store.go)。

- 单元/race 覆盖准入、原始快照/身份、投影等待、错误确认、顺序屏障、八次预算、人工恢复、并发/取消、
  结果保存失败后重投、不确定历史与旧触发跳过、千级待办限额、原命令在满额时复用。
- 真实 ES/MySQL 契约验证重开、CAS、版本排序、失败屏障、队列可见性及租户隔离；只使用测试专属资源。
- 真实业务仓储、真实投影任务/ACK、Redis 租约和模拟 HTTP 接收端组合验证：投影未可见不发送；受理后
  本地保存失败，重启按原动作重投，允许新的 Celery task_id；较新终态同步后旧触发不再发送。接收端为测试夹具，未部署 KAC。

- 实际 Lifecycle 与真实 ES/MySQL 动作任务、Redis 租约验证触发/人工关闭入队响应丢失后按原意图恢复；
  单元覆盖多目标部分入队、升级/轮转/来源恢复/关闭、合并父就绪/恢复/解联、ACK 竞争和超预算预检。
- 真实 ES 普通/批量仓储及 MySQL 验证 pending CAS、跨租户分页、终态发现；ES 真实归档验证未入队动作
  暂缓归档，清除后正常归档并保留尚未确认的投影水位。
- 管理 API 单元/race 验证 JWT、租户/游标/动作筛选、冻结正文分离、队首实时重读、取消释放名额和
  复合错误；Console 组件/代理和 Chrome 验证未知恢复结果的原命令重投、等待投影、本地跳过与此前不确定性。
- 真实 Console/控制面/ES/MySQL 验证任务读取、原请求、队首、失败恢复及刷新后持久记录；失败动作任务
  由测试在隔离集合中创建，不代表正式来源生产器已装配。验证未额外产生原 KAC Kafka action。

- 自动循环的真实双后端测试运行两个动作实例和一个投影实例：无新 Event 补扫、不可见投影等待、
  管理 API 恢复后自动发送、远端受理但本地保存中断后的重开后按原动作重投、旧触发跳过及人工关闭投递。
  每后端四次动作 HTTP 请求、两个唯一动作身份；其中一次请求为明确未授权，另一次为相同动作重投。
  测试在停止旧动作循环后推进注入时钟，越过发送预留期限；没有改变真实 Redis TTL。
- 上述单步组合和自动循环已使用正式 ProjectionGate，不再使用测试专属投影确认算法；来源目标解析
  与 HTTP 接收端仍是模拟。单元/race 另覆盖确认缺失、错来源发布/作用域、同版本内容冲突、二次读取
  期间业务/ACK 变化、存储故障、取消和并发读取。该证据不代表控制面已启动可靠投递。
- Prometheus 实际抓取验证实例计数、取消归零、固定结果分类、页面年龄/观察时间及业务身份不进入标签；
  单元/race 覆盖有界并发、取消前缀、扫描失败、非法/跨作用域结果与有界日志。

- Console 组件/只读代理与 Chrome 验证按需查询、时间/进程筛选、取消、无数据不补零、日志定位及无写入；
  图表从有数据切换到无数据保留真实提示。真实 Prometheus 3.14.0 验证八条固定 PromQL 可以执行，
  该环境当前没有动作时序；有数据的页面使用明确的测试夹具，不冒充生产运行数据验证。
  `console/src/server/action-metrics.integration.test.ts` 需要显式设置 `LINKD_TEST_PROMETHEUS_URL`，
  只读取既有 Prometheus，不修改抓取配置、规则或时序。

- 正式进程的本地真实时序现已通过 ES/MySQL、临时 Prometheus 3.14.0 和 Chrome 联合验收；不模拟
  浏览器接口，核对七个有数据动作面板及未知接收结果面板的真实缺失、进程/范围筛选、任务受理确认和
  零业务写入。截图同时覆盖深浅主题与窄窗口；该证据不等于真实 KAC 或生产业务接入。
  复现方法见 [Console 真实时序](../../../tests/e2e/allinone/README.md#console-可靠投递的真实时序)。

生命周期目标绑定及屏蔽/合并组合使用正式 Worker 回归；实际 KAC 接收端仍待后续接入。
现行 Kafka Hook 保持原来的普通失败语义，本协议不能作为它已经可靠的证据。
