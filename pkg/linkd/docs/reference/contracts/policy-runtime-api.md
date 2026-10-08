# 告警策略运行态 API

状态：2026-10-05 已实现抑制当前计数/窗口/成员、合并运行态、屏蔽当前绑定及历史的只读查询。
新增屏蔽复查诊断、显式版本命令和异步执行、独立终态抑制清理明细和受控对账，以及合并裁决接续/关系检查。
读取接口不创建窗口、不重新匹配成员、不修复缓存；屏蔽复查可更新绑定并尝试状态输出，但不放行处置。

使用管理面 `Internal-Token: Bearer <JWT>`，不接受 Worker Token 替代；认证说明见
[内部 HTTP JWT 认证](internal-token.md)。所有请求明确提供 `bk_tenant_id`，响应带 `Cache-Control: no-store`。

## 抑制路由和查询范围

以下路径统一以 `/api/v1/policy-runtime/suppression` 开头，全部为 GET，`kind` 只接受 `clip` 或 `aggregation`：

| 路由 | 返回内容 | 查询参数 |
| --- | --- | --- |
| `/{kind}` | 当前租户该类状态的分页观察 | `bk_tenant_id`；可选 `policy_id`、`event_source_id`、`owner_alert_id`、`after`、`limit` |
| `/{kind}/{id}` | 一个计数或聚合窗口的当前快照 | 只接受 `bk_tenant_id` |
| `/{kind}/{id}/members` | 当前代次仍保留的 Event 身份 | 必填 `bk_tenant_id`、`epoch`；可选 `after`、`limit` |

`limit` 默认 4，有效值 1..4。防抖 `id` 为 64 位身份哈希、冒号和 64 位 CounterID，共 129 字符；聚合
`id` 是 64 位窗口哈希。调用方使用列表返回的 ID，不传入 Redis 键。精确路径段须 URL 编码。
来源过滤对防抖匹配计数来源，对聚合匹配登记主来源；它不匹配聚合全部成员来源。
所有过滤作用在同一条快照上，详情/成员不接受列表过滤条件。

列表为 `{bk_tenant_id, kind, items, next}`；成员为 `{bk_tenant_id, kind, id, epoch, observed_at_ms, items, next}`。
`next` 绑定租户、方式、过滤、精确身份和成员代次，必须原样传回；不能只看空 `items` 就停止翻页。
原始登记按物理保留期限排序后取有界页，再过滤和跳过已不存在对象。跨页不是一致性快照，新增/更新状态
可能改变位置；刷新从首页开始。列表游标身份被清理、成员换代或成员偏移已失效时返回 409，重新读取详情/首页。

快照公共字段包括 `id/bk_tenant_id/kind/policy/epoch/state/observed_at_ms/retention_ms/duration_seconds/member_count`。
时间戳均为 UTC Unix 毫秒；`retention_ms` 是物理剩余保留时间，不是下一次处置时间。

- 防抖另含 `event_source_id/fingerprint/threshold/observed_count/last_evaluated_at_ms`，可含 `owner_alert_id`。
  `observed_count` 在 Redis 观察时间的闭区间 `[observed_at_ms-duration_seconds*1000, observed_at_ms]` 内计算；合法的零值
  与缺失/损坏状态分开。`member_count` 是尚未物理移除的成员数，可能大于当前窗口计数。
  `state=retained` 只表示计数记录仍存在，不表示新的 Event 已获准处置。
- 聚合另含 `group_key/owner_alert_id/owner_event_id/owner_source_id/owner_fingerprint/started_at_ms/expires_at_ms`。
  `state=pending` 为候选占位，另含 `pending_until_ms`；`state=admitted` 表示曾登记已放行主。
  当前观察时间可能已经越过窗口或占位期限；接口不删除它，也不据此断言主 Alert 此刻仍 active。
- `epoch` 固定为该计数代次或聚合窗口的首条 Event 身份。成员项为
  `{event_id, event_source_id, fingerprint, at_ms}`；聚合成员包含候选/主自身，随后仅登记已确认抑制的 Event。
  查询不计数、不抢占、不登记成员、不延长 TTL，不补写故障丢失的索引或成员。

缺失精确对象返回 404；组成部分缺失或损坏返回不可用，不能显示为零计数。Redis 允许丢失，空列表或成员
集合不证明历史从未抑制；历史裁决以 Event 为准。终态清理按 owner/代次同步移除运行登记与当前成员，旧 owner
不能清掉新代次。独立持久清理记录使用下面的历史路由，与这些可能丢失的当前状态分开。

运行写入预算按租户、抑制方式各最多 65,536 个登记，防抖计数和单聚合窗口各最多 10,000 个保留成员。
每次写入最多清除 256 个过期登记；新登记超限按既有策略预算错误跳过并观测，不写入该次成功裁决。
已有登记继续更新，冻结操作重试继续复用原结果。成员保留期随固定窗口，后续聚合 Event 不为整个集合续期。

## 终态抑制清理历史

以下只读接口不执行 Redis 操作，也不触发处置、投影或策略重新匹配：

| 路由 | 查询参数 |
| --- | --- |
| `GET /api/v1/policy-runtime/suppression/cleanups` | 必填租户；可选 `event_source_id/fingerprint/alert_id/event_id/state/window_kind/window_id/epoch/after/limit` |
| `GET /api/v1/policy-runtime/suppression/cleanups/{id}` | 只接受必填 `bk_tenant_id`；id 为 64 位小写十六进制 |

列表返回 `{bk_tenant_id, items, next}`，精确读取返回一条 Record。每页默认和最多四条，按稳定清理 ID
扫描后过滤，不按时间倒排。过滤后的空页若有 next，应继续；游标绑定租户和全部过滤条件。
共享两并发、每次十秒；非法输入返回 400，缺失记录 404，损坏记录 503，跨作用域响应拒绝接受。
接口没有时间截断。`window_kind` 只允许 clip/aggregation；window_id 必须同时指定方式，epoch 必须同时指定窗口，
只匹配实际确认删除的明细。元信息缺失的记录只能按窗口匹配，不能按猜测的代次匹配。不提供强制清理命令。

| 字段 | 含义 |
| --- | --- |
| `id` | 对 `["suppression-cleanup:v1", tenant, trigger, alert_id, revision, event_id]` 的紧凑 JSON 做 SHA-256 |
| `cause` | 固定租户、来源、fingerprint、终态触发类型及对应实体身份 |
| `cause.trigger=alert_terminal` | 包含 AlertID、终态业务 revision 和 recovered/closed；禁止 active，无 EventID |
| `cause.trigger=event_terminal` | 包含未生成 Alert 的终态 EventID；无 AlertID/revision/status，不清理聚合主 |
| `state` | pending 为结果尚未完成；completed 只表示诊断已保存，不能推断 Redis 均成功 |
| `started_at/finished_at` | 首次意图写入和最终诊断保存时间；UTC，重投不刷新首次时间 |
| `previous_unconfirmed` | 重试曾遇到未完成记录；本轮零删除不能解释为此前没有副作用 |
| `result.clip/aggregation` | 两种清理独立确认；pending 不含 result |

每个结果只允许 `confirmed`（含 0..512 的 removed）、`unavailable`（不含 removed）、
`not_applicable`（只用于 Event-only 的 aggregation，不含 removed）。removed 是本轮 Lua 返回的
删除登记引用数，可能包括清除失效引用；不是受影响 Event 数，也不是完整历史数量。
成功且零删除表示本轮没有找到符合原 owner 条件的登记，不证明历史从未抑制。

confirmed 另包含按 ID 递增的 `windows` 明细，数量必须等于 removed；零项时可省略。
每项为 `{id, epoch}`，防抖 id 包含身份哈希和 CounterID，聚合 id 为窗口哈希。
删除时防抖元信息已经丢失的残余引用使用 `{id, missing:true}`，省略 epoch，不伪造原代次。
明细由执行删除的同一 Lua 原子捕获，不通过删除前后的分离查询推断；错误时不返回部分成功明细。
owner 来自所属 Cause，明细不复制 Event payload 或全部分组值。两个种类各最多 512 项，完整记录最多 2 MiB，
足以容纳字段上限及最坏 JSON 转义；超限不能截断为虚假的完整确认。

Lifecycle 在正式 tenant/source/fingerprint lease 内先创建 pending，再执行既有 owner 安全清理，
最后保存两类结果。记录身份不使用当前时间或随机数，同身份不同来源/指纹/终态内容冲突；
completed 重投复用原结果，不再次清理。持久化错误或取消交由原业务调用重试；Redis 普通失败记录
unavailable 和既有日志/指标，允许原终态流程继续。记录失败不会回滚已经落库的 Alert 终态。

Redis 和记录库没有跨系统事务。清理后结果保存失败、进程退出或超时会留下 pending；原调用重试时
设置 previous_unconfirmed，再执行 owner 安全清理并保存本轮结果。没有另建后台重建或无限重试任务；
Redis 数据丢失仍按已确认规则重新累计。读取 pending 不能推断清理未发生。

正式 Lifecycle Worker、人工关闭和合并终态路径已装配；每条最多 2 MiB，记录独立保存在部署隔离的
ES/MySQL `suppression_cleanups` 集合。精确 GET/CAS 为权威，ES 历史列表允许刷新延迟。
Console `/suppression-cleanups` 提供只读筛选、详情、原 Event/Alert 跳转、刷新回首页及不确定性提示。
详情按两类分别展示窗口/代次，前端每次展开最多 16 项；完整 JSON 按需展开。

## 抑制受控对账

以下路径以 `/api/v1/policy-runtime/suppression/{kind}/{id}` 开头，kind/id 与当前运行态一致。
请求历史独立保存，读取不要求当前 Redis 窗口仍存在。

| 方法与后缀 | 用途 |
| --- | --- |
| `POST /reconcile` | 严格 JSON 显式命令；无 query，最多 4096 字节 |
| `GET /requests` | 必填租户；可选 after、limit，默认和最大四条，按请求身份分页 |
| `GET /requests/{request_id}` | 必填租户，精确读取持久结果；request_id 为 64 位小写十六进制 |

```json
{
  "bk_tenant_id": "tenant-a",
  "expected_epoch": "opening-event",
  "expected_owner_alert_id": "",
  "operation_id": "check-20261005-1",
  "operator_id": "operator-a",
  "reason": "复核当前未绑定计数"
}
```

上例用于防抖。expected_epoch 必填、最长 160 字节；expected_owner_alert_id 必须显式提供字符串，
空字符串只允许防抖；聚合必须提供 owner。operation_id 为 1..128 位字母、数字、下划线或连字符，
operator_id 非空白且最多 256 字节，reason 非空白且最多 1024 字节并保留原文。拒绝未知字段和额外 JSON。
Console 使用 UUID 操作 ID，并从自身认证取操作者；本机无 Basic Auth 时为 console-local，浏览器不能伪造。

请求 ID 对紧凑 JSON `["suppression-request:v1", tenant, kind, window_id, operation_id]` 做 SHA-256。
同身份重投必须包含完全相同的 owner、代次、操作者和原因；否则 409。已完成命令复用原结果，不能重新执行。
接受的 pending 返回 202，已完成的同命令返回 200；这两种 HTTP 成功都不等于发生清理。
新命令先入持久队列，窗口是否消失/换代由后台检查，不能凭旧页面信息强制删除。

后台流程及结果：

| 观察 | outcome / reason | 行为 |
| --- | --- | --- |
| 窗口已消失 | absent / window_missing | 不重建、不声称本次删除 |
| owner 或代次变化 | superseded / window_changed | 不追随新 owner，不删除新窗口 |
| 防抖未绑定 Alert | retained / unbound_counter | 保留计数，不绕过阈值 |
| 聚合候选仍在占位期限内（含边界） | retained / candidate_pending | 允许其继续创建/绑定，不把中间状态当成 owner 缺失 |
| 活动防抖 owner 或已放行的活动聚合主 | retained / active_owner | 保留登记 |
| 原 owner 已终态、不存在，或聚合 owner 不再具备已放行主资格 | cleared / terminal_owner、owner_missing、owner_not_admitted | 在同一正式 fingerprint lease 内条件删除原 owner/代次 |
| 读取、范围、状态或删除失败 | failed / 固定安全原因 | 保留诊断，不伪造成功，不触发处置 |

除正常候选保留外，持有原 owner 的 tenant/source/fingerprint lease 后再次读取窗口，再用实时 GetAlertCurrent
复核真实 owner。租户、AlertID、来源、fingerprint、仓储版本和领域对象均须一致。
只有纯粹的 NotFound 可解释为不存在；同时包含其他错误的错误链不能触发删除。
删除原子比较 owner 和 epoch，同时移除对应登记/成员/反向引用，不删除 Event 首次裁决缓存，不调用 Hook，
不改变 Alert、Event、admission 或业务 revision。普通窗口到期仍由既有路径处理，对账不续期或重置窗口。

Request 保存完整 command、created_at、started_at、state、previous_unconfirmed、completed_at 和 result。
state 只允许 pending/completed/failed/superseded；result 包括 outcome/reason、checked_at、changed、
删除前 observed 窗口及实际读取的 owner_revision/owner_status。changed=true 只代表确认发生删除；
failed 且 changed=false 不能证明没有未确认副作用。已删除但后续 owner 租约释放失败时保留 changed=true 和失败结果。
记录时间统一移除进程内单调时钟并转 UTC，不能因持久化往返误报同一结果冲突。

执行前保存 started_at；进程退出、取消或核心记录写失败后保持 pending，原命令接续时标记此前结果未确认。
普通锁忙/名额满延后；错误链同时含释放等异常时不能按普通忙吞掉。依赖失败保存 failed，需新操作意图才能再次检查。
没有 Redis 强一致重建或通用强制重置入口。

`suppression_requests` 使用部署和租户隔离的 ES/MySQL 集合，单条最多 64 KiB；pending 标记与 payload 同次 CAS。
每租户最多 1024 pending，准入锁内有界计数。ES pending 写等待搜索可见，最终结果不等待刷新；容量在正常租约和
可见性条件下执行，不宣称故障下跨 Redis/数据库的强事务配额。历史和待办扫描分开，已完成项不反复占用执行预算。
独立 suppression-requests 每秒扫描，16 项/页、四路执行、单项 20 秒、单页 90 秒；取消保留原页游标，重读已完成项不重做删除。
管理接口共享两并发/十秒，控制请求和 owner 租约分别隔离，owner 租期至少 30 秒；未配置 Lifecycle 时拒绝新请求。
控制面任务目录与固定低基数指标展示执行情况，失败日志带租户/窗口/请求身份及安全代码，不输出原始异常或原因正文。

Console 运行态详情提供提交、请求历史和精确结果。原命令跨刷新保存在 sessionStorage，响应不确定时重试同一操作；
窗口消失后仍可读取结果。当前窗口、成员与请求共用两并发代理和最多 16 条等待，等待计入总超时，断连取消。
终态自动清理明细与显式对账结果是两种不同记录：前者在清理历史页，后者在原窗口的对账请求历史中。

## 合并路由和查询范围

以下路径统一以 `/api/v1/policy-runtime/merge` 开头，全部为 GET：

| 路由 | 返回内容 | 附加查询参数 |
| --- | --- | --- |
| `/windows` | 当前租户 Redis 窗口摘要页 | `policy_id`、`after`、`limit` |
| `/windows/{id}` | 临时窗口详情，含最多 256 个登记成员 | 无 |
| `/decisions` | 当前租户持久化裁决摘要页，包含已完成记录 | `policy_id`、`phase`、`after`、`limit` |
| `/decisions/{id}` | 裁决摘要、固定身份和执行进度 | 无 |
| `/decisions/{id}/members` | 已捕获成员的固定快照摘要页 | `after`、`limit` |
| `/decisions/{id}/members/{alert_id}` | 一个成员的完整冻结 Alert 快照 | 无 |
| `/relations` | 指定 Alert 作为父或成员参与的历史关系页 | 必填 `alert_id`；可选 `policy_id`、`after`、`limit` |
| `/relations/{id}` | 关系组详情及固定成员建联/解除进度 | 无 |

`id` 为 64 位十六进制身份；裁决及其关系共用 operation ID，窗口 ID 独立。
Alert ID 路径段应 URL 编码。重复、未知或与路由不适用的非空参数拒绝；精确详情不接受分页。
`limit` 默认 4，有效值为 1..4。查询不按历史时间截断。

列表响应统一为 `{"bk_tenant_id":"tenant-a","items":[],"next":""}`。
`next` 为空才代表本次扫描没有后续页；策略/阶段过滤或已消失窗口可产生空页并仍返回非空 `next`。
游标绑定租户、查询类型、过滤条件、Alert 或裁决身份，不能用于另一个查询；客户端应原样传回。
新增记录可能排在当前游标之前，刷新时回到首页。窗口跨页读取不是 Redis 一致性快照。

使用 Elasticsearch 时，持久化裁决、成员和关系写入不等待搜索刷新：精确 ID 详情使用实时读取，
列表可能暂时缺少刚写入的记录或显示旧进度。刷新列表后可见；不能以空列表否定精确详情或持久化结果。
按 Alert 查询的关系引用同样接受刷新延迟。这不改变策略配置发布 API 的可见性约定。

## 临时窗口

列表包含身份、`policy` 版本引用、`group_key`、`started_at`、`deadline`、`cyclic`、`revision`、
`member_count`、`committed_count`、`group_counts` 和可选 `frozen`。详情额外返回 `members`。

- `member_count` 包含尚未确认 Alert CAS 的登记；`committed_count` 只计算已确认登记。
- `group_counts[i]` 统计已提交成员首次入窗时命中的第 i 个条件组，不代表实时成员仍有效。
  正式裁决会重新读取 Alert。一个成员可命中多个组，但不能当作多个独立成员。
- `members[].main` 固定 Alert/Event/来源/fingerprint/severity 引用；`groups` 为从 0 开始的条件组编号，
  `first_at_ms` 为首次时间，`committed` 表示 Alert CAS 是否已确认。
- `frozen` 保存 Redis 冻结结果和 `operation_id`，不单独证明持久化裁决或父告警已经创建。
- 到达 `deadline` 只说明可以执行到期判定，不表示后台已执行。

登记集合沿用每租户最多 4096 个窗口的生产预算。只读 Lua 在返回前检查 Redis 类型、数量和 ID 长度，
进程在有界集合内按 ID 取页；每页最多读取四个窗口。窗口已被清理或丢失时跳过该登记但推进游标，
查询不会删除遗留登记。精确窗口缺失返回 404；**不能把它解释为合并失败、没有持久化结果或允许重新处置**。

## 持久化裁决与冻结成员

裁决列表及详情只返回必要摘要，不附带完整策略 Spec、父 Event 或成员 Alert：

| 字段 | 含义 |
| --- | --- |
| `policy` | 固定策略 ID、version、digest |
| `outcome` | `succeeded` 表示冻结时满足合并条件；`failed` 表示到期条件未满足，不表示基础设施故障 |
| `phase` | `capturing`、`prepared`、`waiting_parent`、`linking`、`ending`、`releasing`、`completed` |
| `member_count` / `wait_member_count` | 选中的固定成员数 / 需要解除等待或处理的成员数 |
| `capture_offset` / `member_offset` | 已捕获快照前缀 / 已处理等待成员前缀 |
| `parent_event_id` / `parent_alert_id` | 已准备的父 Event / 已经从仓储确认存在的父 Alert，缺失不以预计 ID 填充 |
| `reason_code` | 安全的业务原因，如 `conditions_not_met`、`parent_ended`、`members_ended` |
| `window_finished` | 业务完成后 Redis 清理提示是否已经确认，与合并结果分开 |

成员列表显示首次捕获的 Alert 身份、来源、severity、status、trigger_event_id、enrich_status 和 revision。
它不是当前 Alert 的状态。捕获尚未完成时只返回实际存在的快照，结合 `capture_offset` 判断进度。
单成员详情返回 `{bk_tenant_id, decision_id, alert}`，包含原始冻结的 Alert 和丰富结果，供模板排查；
未捕获或不属于该裁决的成员返回 404，不读取实时 Alert 作为替代。

## 父子关系

关系详情包括父身份、固定成员、`state`、`index_offset`、`end_offset`、结束原因及开始/完成时间。
`state` 有效值为 `preparing`、`ready`、`ending`、`ended`；成员建联进度为 `pending`、`linked`、`terminal`。
成员 `linked` 是建联记录，关系已经 `ended` 时不代表仍抑制该子告警。
`terminal` 表示曾观察到真实终态，不是接口把子告警改成了恢复。后台复核会逐项保存这一不可逆确认，
大关系在超时/取消后可以继续；部分成员为 terminal 不表示父已恢复，应回查父 Alert 的生命周期状态。

按 Alert 查询依赖已经建立的分页引用，关系准备期间可能尚不完整；精确关系 ID 可直接读取当前准备进度。
父人工关闭后的关系解除不改写子生命周期、不补发处置，详情中的 `end_offset` 不能当作处置成功计数。
当前生命周期和放行资格应回查对应 Alert 的 `status` 与 `admission`。
终态子 Alert 保留原合并历史摘要；关系组的 `ended` 和完整 `end_offset` 表示清理工作已经确认，
不要求把终态子中的历史关系 ID 删除。活动子才移除对应的阻断引用。

## 合并受控接续与关系检查

显式命令只作用于已有持久裁决或关系，`kind` 只允许 `decisions`、`relations`，`id` 为原记录 ID。
临时窗口不提供强制冻结、修改截止时间或重新选择成员的接口。以下路径以
`/api/v1/policy-runtime/merge/{kind}/{id}` 开头，仍使用管理 JWT，响应不缓存：

| 方法与后缀 | 含义 | 参数 |
| --- | --- | --- |
| `GET /control` | 实时控制点，包含原存储版本对应的 token | 仅必填 bk_tenant_id |
| `POST /requests` | 持久化一次接续/检查意图 | 严格 JSON，无 query，最多 4096 字节 |
| `GET /requests` | 同一裁决或关系的操作历史 | 必填租户；after、limit，默认和最大四条 |
| `GET /requests/{request_id}` | 精确读取原操作 | 仅必填租户；request_id 为 64 位小写十六进制 |

```json
{
  "bk_tenant_id": "tenant-a",
  "expected_token": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
  "operation_id": "retry-20261005-1",
  "operator_id": "operator-a",
  "reason": "接续已准备的父 Event"
}
```

expected_token 必须原样使用 `/control` 返回的 64 位小写十六进制 token，不能由列表时间或阶段推算。
operation_id 允许 1..128 位字母、数字、下划线、连字符；operator_id 非空白且最多 256 字节，reason 非空白
且最多 1024 字节并原样保存。Console 使用 UUID，并从服务端认证取操作者；浏览器不能指定 actor。
未知字段、空 token、重复 query、其他资源种类或额外 JSON 均拒绝。

控制点包含租户、kind、target_id、window_id、token、phase、updated_at、complete，以及
capture_offset/member_offset/index_offset/end_offset。前两项只用于裁决，后两项只用于关系。
裁决额外包含冻结的业务 outcome；`failed` 表示合并条件未满足，并不表示一次请求执行异常。
complete 仅在裁决 completed 且窗口清理完成，或关系 ended 时为 true。
token 是租户、资源种类、记录 ID 和真实存储版本的摘要；进度计数看起来一样，也不能复用已变化的 token。

请求 ID 为紧凑 JSON `["merge-request:v1",tenant,kind,target_id,operation_id]` 的 SHA-256。
首次入队实时确认目标及固定 window_id；同身份重投必须保留原版本、操作人和原因，即使原目标后来改变，
仍返回原请求。pending 返回 202，已完成的同命令返回 200。冲突 409，纯锁忙/容量不足 429，
未配置 Lifecycle 或后端记录损坏 503；新请求的目标不存在为 404。内部原始错误不返回浏览器。

执行顺序为：保存开始记录 → 获取与自动任务相同的窗口租约 → 实时重读并校验 token →
调用一次既有 `StepDecision` 或有界 `CheckRelation` → 同锁内重读进度 → 释放窗口租约 → 保存最终结果。
请求自身另有独立租约，避免释放窗口租约后、保存结果前由另一个请求工作线程重复接续。
副作用仍由正式 Lifecycle 的单 Alert fingerprint lease 执行，不直接改写 Alert、admission 或关系记录。

| result.outcome / reason | 含义 |
| --- | --- |
| advanced / progressed | 本次步骤前后存储版本发生变化，原业务任务可能仍有后续步骤 |
| unchanged / no_progress | 已调用正式步骤，当前没有可推进的进度，例如父 Event 尚未处理或成员仍 active |
| unchanged / already_complete | 原任务已完成，未调用业务步骤，不重启业务失败裁决 |
| superseded / target_changed | 原版本已变化，本次不调用业务步骤；不能追随最新版本修改同一命令 |
| failed / target_missing、scope_mismatch、invalid_state、dependency_failed、execution_unavailable、step_timeout | 本次执行失败；不能推断没有部分副作用，原自动任务仍按已有规则继续 |

result 保存 checked_at、step_attempted、真实 before/after 控制点。只有实际读到才保存观察；后读失败不伪造 after。
窗口租约释放失败仍保留此前确认的 before/after，并将本次记录为 failed。请求 state 分为
pending/completed/failed/superseded，completed 只代表这一次有界操作完成，不能等同于合并主已经建好。
若原裁决在 releasing，接续可按既有准入规则释放原 Alert 并产生处置；不重新抑制、屏蔽或合并。
父人工关闭后的关系检查仍只解联，最后一条关系解除也不补发子处置，下一条触发 Event 才判断。

started_at 先于执行落库。取消或最终记录保存失败留下 pending，原请求重试设置 previous_unconfirmed；
若原业务已推进，则以 superseded 结束本次接续，不回退阶段、不重渲染父 Event、不刷新已捕获成员快照。
所有时间去掉进程内单调时钟并转 UTC；时钟回拨不写入比已确认进度更早的时间。
Redis 和请求/业务文档没有跨系统事务；该接口不增加 Redis 全量重建或故障强一致恢复要求。

部署隔离的 `merge_requests` 单条最多 64 KiB，pending 标记与 payload 原子 CAS。准入租约内每租户
最多 1024 pending；相同请求在满额时仍可重投。ES pending 写等待搜索可见，最终结果和业务进度不等待刷新，
历史列表可能延迟，精确 GET 为准。历史游标绑定租户、kind、target_id，后续页不能跨资源或跨租户使用。

独立 merge-requests 每秒运行，16 项/页、四路工作线程、每项 20 秒、每页 90 秒；取消保留原页游标。
窗口执行仍与自动裁决/关系任务共享四个名额、十秒执行期限、至少 30 秒租约。管理面两并发/十秒；
Console 的运行态、成员、控制点和请求共用两路代理及最多 16 条等待。任务目录、固定低基数指标和安全日志
记录请求执行，不把租户/Alert/请求 ID 放进 Prometheus 标签。

## 屏蔽路由和查询范围

以下路径统一以 `/api/v1/policy-runtime/shield` 开头，全部为 GET，并要求 `bk_tenant_id`：

| 路由 | 返回内容 | 附加查询参数 |
| --- | --- | --- |
| `/alerts` | 当前有屏蔽绑定或待完成状态输出的 Alert 摘要页 | `policy_id`、`main_alert_id`、`binding_type`、`after`、`limit` |
| `/alerts/{id}` | 指定 Alert 当前的屏蔽、生命周期与历史准入摘要 | 无 |
| `/alerts/{id}/history` | 指定 Alert 的屏蔽/解除流水页 | `after`、`limit` |

`id` 为 Alert ID，路径段应 URL 编码；已经解除或进入终态的 Alert 仍可精确查询。
`binding_type` 有效值为 `time_shield`、`custom_shield`、`cmdb_shield`；`main_alert_id` 为固定依赖主身份。
过滤条件必须同时命中同一条绑定，匹配范围包含当前绑定以及待完成 `policy_change` 的前后绑定。
本版按策略顺序保留首次命中的屏蔽，不因查询支持绑定数组就推断所有命中策略都会建立关系。

`limit` 默认和最大均为 4；`after` 是原样回传的不透明游标，绑定租户、筛选条件和 Alert 身份。
游标最多 8192 字节，内部仓储游标最多 4096 字节；重复、未知或不适用于当前路由的非空参数拒绝。
详情不接受分页；历史不接受绑定筛选，避免把旧绑定误当作当前匹配结果。

列表从当前租户按 Alert ID 升序扫描，包含未来才到复查时间的绑定，不复用后台跨租户工作游标。
每次只扫描一页再按绑定过滤，因此空 `items` 仍可能有 `next`；客户端必须允许继续翻页。
列表不是跨页一致性快照，新插入在游标之前的记录需要回到首页读取。
屏蔽已解除且状态输出意图已完成的记录不在当前列表中；按 Alert ID 查询历史即可继续追踪。

列表响应为 `{bk_tenant_id, items, next}`，详情直接返回以下摘要：

| 字段 | 含义 |
| --- | --- |
| `bk_tenant_id` / `alert_id` / `event_source_id` / `revision` | 当前快照作用域、来源与业务版本 |
| `status` / `severity` | 真实生命周期与等级，不因解除屏蔽自动变为 recovered 或获准处置 |
| `shield.active` / `shield.bindings` / `shield.next_check_at` | 当前屏蔽、固定绑定和计划复查时间；时间到达不代表已执行 |
| `admission` | 最近一次历史放行记录；不等同于当前是否可处置 |
| `policy_change` | 可选的已保存业务变化对应的待完成状态输出意图，含 operation ID 和前后绑定 |

绑定包含策略版本、来源 Event、绑定等级和时间；依赖绑定另有固定主 Alert，时间屏蔽包含命中的时间段。
详情不附带完整 Alert/Enrich；如需原始事实，使用对应 Alert 查询。普通 Hook 仍是失败记日志，
`policy_change` 只能证明流水和状态输出尝试待完成，不能据此承诺 KAC 投影已可靠送达。

## 屏蔽变更历史

历史响应为 `{bk_tenant_id, alert_id, items, next}`。`items` 是实际 `operation_kind=shield/unshield`
的 AlertLog，按仓储的 `created_time + log_id` 顺序读取；一次扫描最多四条原始日志，再过滤操作类型。
不存在屏蔽流水的中间页可以继续翻页，`next` 为空才表示该轮到达末尾。ES 历史使用仓储原生快照游标，
过期返回参数错误，客户端重新读取首页；新日志可能需要刷新后可见。

本次实现写入的屏蔽变化日志在 `params` 中保留 `before_bindings`、`after_bindings` 的结构化 JSON 值，
无绑定时为 `null` 或空数组。定时检查还保存本次增加或移除的 `bindings`，保留的绑定不会生成一次虚假的新增屏蔽。
仅复查时间变化不写屏蔽变更日志。来源恢复/关闭、人工关闭和合并父恢复时清理绑定并补齐解除日志；
解除没有额外处置副作用。既有日志缺失这些字段时原样返回，不制造历史快照。

## 屏蔽复查与请求

以下路径以 `/api/v1/policy-runtime/shield/alerts/{id}` 开头。读取前均确认该租户的真实 Alert 存在；
`id` 为 URL 编码的 Alert ID。复查使用 Lifecycle 的原始冻结绑定、策略版本和 fingerprint lease。

| 方法/路径 | 参数与结果 |
| --- | --- |
| `GET /check` | 仅 `bk_tenant_id`；返回 `{bk_tenant_id, alert_id, check}`，尚无记录时 `check=null` |
| `GET /requests` | `bk_tenant_id`，可选 `after/limit`；默认/最大 4 条，返回 `{bk_tenant_id, alert_id, items, next}` |
| `GET /requests/{request_id}` | 仅 `bk_tenant_id`；实时读取完整请求，ID 为返回的 64 位小写十六进制值 |
| `POST /reconcile` | 无 query；最多 4096 字节严格 JSON，拒绝未知字段；pending 返回 202，已有最终结果返回 200 |

提交示例（路径中的 Alert ID 不重复放入 body）：

```json
{
  "bk_tenant_id": "tenant-a",
  "operation_id": "a38ff051-bb9e-4cec-8421-d0ba821bde50",
  "expected_revision": 2,
  "operator_id": "operator-a",
  "reason": "依赖服务恢复后复查"
}
```

`operation_id` 为 1..128 位 ASCII 字母、数字、`_` 或 `-`；作用域为租户与 Alert。
`expected_revision` 是 1..2^53-1 的业务 revision；操作者和原因须非空，分别最多 256/1024 字节。
服务端先按稳定身份查询旧请求，同一身份的命令字段必须完全一致；重复提交返回原记录和原创建时间。
新请求在准入时验证当前业务版本，变化返回 409。排队成功不代表已经检查或解除。
执行时在 Alert 租约内再次验证原版本，已变化则结束为 `superseded`，不切换到新状态执行。

请求记录为 `id/command/state/created_at` 和可选 `completed_at/result`。状态有效值：
`pending`、`completed`、`failed`、`superseded`。锁忙保持 pending；已持久化最终结果的请求不再次执行。
`failed` 是本次请求的最终检查失败，普通定时任务仍会继续；用户刷新后可使用新的 operation ID 发起新检查。
HTTP 超时、连接丢失或结果不可确认时必须重试原命令，不得自动换 operation ID。
结果保存失败时，pending 可以被重新执行；原业务 CAS、状态输出意图及版本门槛负责重复安全，
本契约不承诺故障下检查函数恰好调用一次。

检查对象为 `bk_tenant_id/alert_id/trigger/request_id/started_at/finished_at/error_code/report`。
`trigger` 是 timer、hint 或 request，分别表示定时、主终态提示和显式复查；仅 request 带 request_id。`report` 包括：

- `observed_revision/result_revision/checked_at`：本次读取与返回的业务版本及判定时间；未读取到 Alert 时版本为 0，时间可为零时间，不解释成真实业务时间。
- `outcome`：`inactive` 无须解除、`retained` 保留、`changed` 已修改、`partial` 部分条件未能检查、`failed` 检查失败、`superseded` 请求版本失效。
- `changed`：本次已确认完成业务状态 CAS；失败时 false 不承诺没有其他部分副作用。检查不推进版本；实际绑定/标签变化才推进业务 revision。
- `remaining_bindings`：已观察到的剩余数量；observed_revision=0 时无观测值，界面应显示未知。
- 可选 `decision`：完整记录最多 16 条原绑定复查和 256 条候选规则评估，共最多 272 步。`from_binding=true` 带 binding_id，结果为 retained/released/skipped；未设置或 false 表示候选规则，结果为 bound/not_matched/skipped，只有 bound 带新 binding_id。复查中的 bound 只来自时间屏蔽，依赖规则不重新选主，main_reserved/main 不允许出现在该诊断。
- 原绑定的 skipped 表示检查未完成并保留；候选的 skipped 表示未能评估并跳过候选，不能把二者都显示为“保留关系”。解除全部旧绑定后仍可能记录当前规则未命中/跳过，诊断保存不得因这些合法步骤失败。

`error_code` 仅允许 revision_changed、scope_mismatch、invalid_state、record_missing、configuration_unavailable、
alert_busy、check_timeout、check_cancelled、version_conflict、dependency_failed；不保存驱动错误原文。
最近记录按检查开始时间保护，旧的慢检查不能覆盖已保存的新检查；它不是全部定时检查历史。
诊断存储失败时最近记录可能仍旧，必须显示检查时间；手动请求的最终 result 独立保存。

请求历史按稳定 ID 排序，非按时间倒序；游标绑定租户、Alert 与 requests 查询类型。
ES 新 pending 等待搜索可见，保证准入计数；最终结果及最新诊断不等待搜索刷新，精确读取即时可见，
历史页/工作页允许短暂延迟。执行前必须实时重读，不能因旧搜索结果重复执行已完成请求。

每租户最多 1024 个 pending，请求准入四并发、十秒；租户准入与同请求执行分别有 Redis 租约。
独立 `shield-requests` 任务每秒检查，每页 16、四路、每项 20 秒、整页 90 秒；
与定时和事件提示检查共用四个实际复查名额，核心检查十秒、最近诊断保存三秒。超限/锁忙准入返回 429。
读取复用屏蔽查询的两并发/十秒预算；单条检查/请求最多 256 KiB，最近/单条响应最多 1 MiB，四条历史页及 Console 读取代理最多 2 MiB。显式请求 body 仍最多 4096 字节。

解除仍只同步状态，不创建输入 Event、不进入合并、不补发处置；下一条触发 Event 才重新判断。
Console 保留原命令直到结果明确，操作者由 Console 服务端认证注入；浏览器不能传 operator_id，
也没有 force 或跳过版本检查选项。

## 预算与错误

控制面的抑制、合并与屏蔽查询各自共享两并发额度，请求最多十秒；单次 Redis 查询整体最多三秒。
列表/摘要/关系/窗口/屏蔽历史响应最多 1 MiB，显式单成员完整合并快照最多 8 MiB。
Console 对三类运行态分别提供两并发代理，复核租户、记录身份、过滤范围及响应预算，浏览器不持有管理 JWT。
屏蔽当前状态、流水、诊断和显式请求共用同一代理，最多额外等待 16 条；等待与 HTTP 访问共用
Console 配置的总超时（上限 15 秒），断开/取消立即退出等待，满载仍返回 429。其他代理默认不排队。

400 为参数或游标不合法；401 为认证失败；403 为返回对象作用域不符；404 为当前明确身份不存在；
409 为当前游标/代次已失效；429 为查询并发已满；503 为依赖未配置或响应不可用；504 为超时。
其他存储错误返回安全的服务端失败，不能当作空集合或没有关系；不向调用方返回驱动原文、地址或凭据。

对应页面见 [Console 抑制运行态](../../guides/console.md#抑制运行态)、[合并运行态](../../guides/console.md#合并运行态)和[屏蔽运行态](../../guides/console.md#屏蔽运行态)。

Console 对复查步骤本地每页显示 16 条，区分既有绑定与候选规则；新检查到达后回到步骤首页，不额外触发检查。
