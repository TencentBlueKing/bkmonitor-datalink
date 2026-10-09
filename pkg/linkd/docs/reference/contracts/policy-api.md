# 告警策略配置 API v1

状态：配置发布/恢复、匹配预览、单策略状态模拟、逐策略执行观察、Worker 读取和 Lifecycle 版本上下文冻结已实现。
业务规则和配置字段以[开发方案](../../design/event-enrich-and-alarm-policies.md)为权威位置。

所有接口使用[管理 JWT](internal-token.md)，请求头为 `Internal-Token: Bearer <JWT>`。
Worker Token 不能调用这些管理接口。租户必须显式传入，不能从 EventSource 或当前登录用户猜测。
API 成功表示配置已发布，不表示全部 Worker 已加载或相关运行窗口已处理。

## 路径和请求

| 接口 | 请求 | 结果 |
| --- | --- | --- |
| `GET /api/v1/policies` | query：`bk_tenant_id`、`type`；可选 `after`、`limit`、`is_enable` | `{items: PolicyRecord[], next: string}` |
| `GET /api/v1/policies/{type}/{id}` | query：`bk_tenant_id` | 当前 `PolicyRecord`，包括待发布状态 |
| `PUT /api/v1/policies/{type}/{id}` | JSON：开发方案第 5.1 节的完整发布信封 | 本次操作对应的不可变 `PolicyRelease` |
| `DELETE /api/v1/policies/{type}/{id}` | JSON：下方删除信封 | 保留原配置的删除 Release |
| `GET /api/v1/policies/{type}/{id}/releases/{version}` | query：`bk_tenant_id` | 指定版本，不回退到最新版本 |
| `POST /api/v1/policies/preview` | JSON：下方预览信封 | 无副作用的逐 evaluation/条件组结果 |
| `POST /api/v1/policies/simulate` | JSON：下方模拟信封 | 请求私有状态的事件/时间轨迹 |
| `GET /api/v1/policies/statistics` | query：`bk_tenant_id`、`type`、`ids`、`hours` | 指定策略的执行观察 |

`spec.space_code` 只接受 canonical `bkcc__<正整数业务 ID>`；全局属性在执行时读取当前租户业务目录，
不把 0 或固定业务 ID 当成全局业务。

`type` 为 `suppression`、`shield`、`merge`。PUT 的 `id/type` 必须与路径一致；不允许用 PUT 的
`deleted` 字段绕过 DELETE。PUT/DELETE 不使用 query 参数传递租户，避免同一请求具有两份作用域。
读取请求拒绝重复或未知 query 参数。JSON 拒绝未知字段、重复键和尾随内容。

删除信封：

```json
{
  "schema_version": 1,
  "bk_tenant_id": "tenant-a",
  "expected_version": 3,
  "operation_id": "kac-sync-delete-policy-001"
}
```

首次创建的 `expected_version=0`；后续修改填写当前版本。删除要求正整数版本。
`operation_id` 为非空字符串，最多 128 字节且不含控制字符。相同身份的重试必须携带相同内容和
`expected_version`；修改请求内容必须使用新操作 ID。

分页按策略 ID 升序，`limit` 默认及上限均为 16。`after` 使用上一响应的 `next`，空 `next`
表示本轮结束。启用筛选使用已发布 Spec，尚未完成首次发布的记录按未启用处理；删除记录也按未启用处理。
筛选不改变扫描游标，即使 `items=[]`，仍须检查 `next`。不将“过滤后为空”解释成全部扫描完成。
列表不是跨请求数据库快照，并发变更通过下一轮完整扫描发现。

## 版本、重试和存储

配置身份为 `(bk_tenant_id, type, id)`，不含 EventSource。同一个策略可跨来源生效。

- `PolicyRecord`：`revision` 是已经预留的编辑版本，`published` 是已确认可读的发布版本；`spec/deleted`
  保存已发布状态，`compiled` 与该 Spec 对应；`pending` 保存正在完成的 Release。运行方只能使用 published 指向的精确 Release。
- `PolicyRelease`：`version`、`operation_id`、`request_digest`、`spec`、`compiled`、`deleted`、`created_at`。
  `compiled` 提供编译版本、摘要、时区、条件组数、目标 selector 数及归一化方案；运行对象从 Spec 编译。
- `PolicyOperation`：持久化操作摘要、目标版本和首次时间锚点，保证发布中断和重试不会改变不可变内容。

发布顺序：操作意图 create-only → Record CAS 预留 pending → Release create-only → Record CAS
推进 published。每步可部分成功，调用方可原身份重试；控制面每 5 秒扫描一页待发布记录，单轮上限
10 秒，完成遗漏发布。单个恢复错误保留 pending，并继续后续记录，下一轮继续重试。

旧操作晚到时返回其原始 Release。例如操作 A 发布 v1，操作 B 已发布 v2，重试 A 仍返回 v1。
客户端不能将此响应误当作当前最新版本；当前发布位置通过 GET Record 获取。
配置中的无意义 JSON 空白和键顺序不改变请求身份；数组顺序仍有语义。
删除发布 tombstone，保留历史版本，不清除被窗口引用的配置。

持久存储使用 Linkd Repository 选择的 ES/MySQL，按部署隔离。MySQL 使用 `linkd_policy_records`、
`linkd_policy_releases`、`linkd_policy_operations`；ES 使用带部署摘要的三个专用索引。
`linkd storage migrate` 和控制面启动均初始化这些集合，不导入或删除历史策略。

## 预算和错误

策略 Spec 输入和规范化结果上限 2 MiB；HTTP 请求上限 3 MiB；单存储对象上限 8 MiB。
完整配置每页最多 16 条，存储响应上限 128 MiB；这些是拒绝超限的硬预算，不是吞吐保证。
表达式最多 64 个条件，布尔表达式 4096 字节，条件组最多 32 组，目标 selector 最多 64 个；
静态实例每 selector 最多 2000 个、总计最多 10000 个。分组字段最多 32 个。

| HTTP 状态 | 含义 |
| --- | --- |
| 200 | 已发布，或只读查询完成 |
| 400 | 配置、作用域、版本格式或预算无效 |
| 401 | 管理 JWT 无效；Worker Token 不替代管理认证 |
| 403 | 租户、来源任务或读取结果归属校验失败 |
| 404 | 当前租户和类型中无该对象/版本 |
| 409 | 编辑版本、操作身份或不可变发布冲突 |
| 503 | 当前 API 未装配策略服务 |
| 504 | 操作超过请求期限；可能部分成功，原身份重试 |
| 500 | 存储或内部错误；不暴露驱动错误和原始配置 |

配置读取响应设置 `Cache-Control: no-store`。发布恢复作为 `policy-publication` 出现在控制面任务列表，
使用既有任务轮次/耗时/失败指标；错误日志使用固定错误码，不打印条件值或数据源响应。

## 只读匹配预览

请求必须选择一个已发布版本（id/version）或一个候选 Spec，不能两者同时提供。输入也必须且只能选择
`event_id`、`alert_id`、完整 `event` 或完整 `alert` JSON。按 ID 查询从当前租户读取已经保存的丰富结果，
预览不自动重跑 Enrich；pending Enrich 应先使用丰富预览定位原因。

下面的 `event_id` 需替换为 Event 详情中的真实 ID：

```json
{
  "bk_tenant_id": "tenant-a",
  "type": "suppression",
  "id": "policy-db-noise",
  "version": 1,
  "event_id": "<EventID>",
  "severity": "warning",
  "at": "2026-09-30T10:00:00+08:00"
}
```

省略 severity 时逐项返回 Event 的全部 evaluation；输入 Alert 只返回当前等级。省略 at 使用当前时间。
依赖屏蔽可通过 `rely=true` 选择被屏蔽条件，`origin_alert_id` 提供同租户主告警的有效字段和关系查询起点。
只有 `rely_policy` 可以设置 `is_alarm_field_referenced: true`，其 `target_value` 必须是包含
`${字段key}` 的字符串。字段必须在内置目录或 `field_mappings` 中；未提供的值替换为空字符串，
不可用的丰富字段仍使条件不可求值。每次匹配独立解析，不修改编译模板；`terms/must_not_terms`
将替换结果包装为单元素数组，不拆分字符串或展开字段数组。未启用标记的模板文本保持字面值。
需要具体模型的 CMDB 关系条件不能使用引用。引用预览需提供 `origin_alert_id`；普通字段引用不要求主实例身份。
业务范围仍受策略 space_code 约束；主目标 descriptor 不被误用于约束另一模型的依赖候选。

响应包含策略 ID/版本、编译摘要、at、`mode=matching_only` 和 `evaluations[]`。每项含 evaluated、matched、
reason、groups（逐条件结果）及可用的 group_key。`evaluated=false` 表示依赖/字段/分组不可求值，
不是普通未命中；恢复/关闭 evaluation 返回 `terminal_bypass`。响应不包含原始依赖载荷。
当前预览不读取运行窗口，也不宣称计数已达标、屏蔽关系已建立或合并已成功。它与正式纯匹配使用同一 Evaluate。
每进程最多 4 个并发预览，满载返回 429，总期限 10 秒。

## 有效字段读取

`name/content` 来自保存补丁合成的 title/content。旧清洗同名 `extra_data.object/item/meta_info/strategy_id/dimension_info`
优先使用有效视图中的值；显式空值、0、false 不触发回退。没有旧字段时，object 使用 subject_name/subject_id，
meta_info 使用有效同名丰富值或 source_event_id。
其他内置字段优先读取有效 labels，再读取有效 extra_data；`bk_biz_id` 在不存在丰富覆盖时可使用来源
`dimensions.bk_biz_id`。`source_id/source_name` 不回退到 Linkd EventSourceID。
`strategy` 对应 strategy_name，`strategy_id` 对应 monitor_template_id，`dimension_info` 对应 dimension_text，
`item` 优先 display_name 再使用 strategy_name。strategy_config_version 只读明确的同名值，不混用来源 strategy_version。
`entity_uid` 从两个明确的 canonical model_id/model_inst_id 字符串构造，不猜测 SubjectType。

level 使用本次冻结的严重程度映射；默认 critical→fatal、info→remind，native 名称使用当前合法原名。
alarm_time 保留 KAC 字段表示：将来源发生时间投影为 Asia/Shanghai 的 `YYYY-MM-DD HH:mm:ss`；
该无偏移文本与 KAC ES 字段查询一致，原 Event 时间仍以 UTC 保存。策略 timezone 只控制生效时段。

已失败/partial 的内置处理器使其输出字段不可求值，后续成功覆盖的具体字段可恢复为已知值。
缺少目标声明的自定义处理器失败时，对全部可变字段保守标记不可求值，避免负条件误命中；
skipped 处理器不产生这种失败标记。自定义字段仅使用配置显式声明的 labels/extra_data 精确路径。

## Worker 读取与 Event 上下文

- `GET /internal/policies`：query 为 task、bk_tenant_id、type、after、limit；每页最多 3 条，响应为 Record 数组，
  保留 published/spec/compiled，移除 pending。使用独立 Worker Token 与 X-Worker-ID。
- `GET /internal/policies/{type}/{id}/releases/{version}`：query 为 task、bk_tenant_id；返回精确历史版本。
- 当前任务必须属于该 Worker、角色为 lifecycle 且尚未 stopped；来源绑定租户时必须一致。多租户来源
  仍逐请求显式声明租户，返回数据逐项复核。Cleaner、停止任务和跨租户读取均被拒绝。

Worker 目录缓存最长 5 秒；每租户最多 256 个配置/16 MiB，整个缓存最多 32 个租户/32 MiB 原始配置，
每轮最多扫描 1024 条含 tombstone 的记录。超限和不完整读取不返回半份配置；过期缓存加载失败不回退旧版本。
读取授权失败不能作为普通配置依赖失败跳过。

Lifecycle 将首次 evaluated_at 和发布引用（type/id/version/digest）保存到 `processing.policy_context`，
然后才允许保存/执行计划。该上下文在 Plan 撤销、完成、重投后均保留。普通配置读取失败记录
`policy_load_failed` 后继续基础生命周期；当前存在版本引用不代表已经产生策略副作用；具体执行与验收进度见[开发方案](../../design/event-enrich-and-alarm-policies.md#111-实施进度与验收记录2026-10-07)。

防抖已在正式 Lifecycle Worker 消费冻结版本并执行；结果保存到 `processing.policy_decision.suppression`，包括活动 Alert 绕过、计数和跳过诊断。
活动 Alert 的重复、更新和升级不执行防抖或关联聚合。聚合已实现候选占位、真实主创建后登记、跨来源实时复核与 owner/代次清理。
运行结果中的 `reserved` 表示计划中的候选资格，不能单凭该字段认定登记副作用已经成功；实际登记状态由 Redis 窗口记录查询。
屏蔽/合并准入及独立控制任务已装配，完整启用策略的真实后端组合验收仍在推进，不能仅凭配置发布认定运行结果。

时间屏蔽已在 Lifecycle 与独立控制面 `shield-check` 中使用相同匹配器和冻结源视图。已建关系继续使用原 Release，
普通编辑只影响新裁决，停用/删除会在检查时提前解除；解除只同步状态，不产生处置或合并。
依赖屏蔽已实现跨来源选主、固定关系和定时解除；配置编译与只读条件预览仍不等于关系已经建立。
`alarm_tags` 为不重复的正整数 ID，单策略最多 128 个且小于 2^53；Alert 的策略标签累计上限 256，超出时跳过受影响新策略。

## Console 只读入口

Console 的 /local-api/policies 代理 GET 列表、记录、精确 Release 与 POST preview；不开放 PUT/DELETE。
输入约束及响应字段复用前后端共享 schema，返回记录和 pending 均复核租户/类型/身份。
Console 列表预算为每页 8 条，仍遵守原始扫描游标规则；不更改本契约直接 API 的 16 条上限。
页面行为见[Console 指南](../../guides/console.md#告警策略与只读匹配)。

## 隔离状态模拟

`POST /api/v1/policies/simulate` 使用相同管理 JWT，拒绝 query 参数。每次从空状态开始，仅支持
`suppression` 和 `merge`，选择一个精确 Release 或候选 `spec`（二选一）。`shield` 继续使用匹配预览。
不读取生产活动 Alert、计数或窗口，不执行 Enrich、创建时内容生成、Hook、兼容投影和动作投递。

```json
{
  "bk_tenant_id": "tenant-a",
  "type": "suppression",
  "id": "db-noise",
  "version": 2,
  "steps": [
    {"at": "2026-10-08T00:00:00Z", "event_id": "event-001"},
    {"at": "2026-10-08T00:00:10Z", "event_id": "event-002"},
    {"at": "2026-10-08T00:01:00Z"}
  ]
}
```

每步必须有 RFC3339 `at`；`event_id` 和完整 `event` 二选一，均省略表示只推进时间。
Event 必须属于请求租户且已有完成的丰富结果；读取已有 Event 时只复制事实和丰富，不复制生产
处理结果或关联。重复 Event 复用模拟内的首次结果，响应 `replayed=true`；冲突内容拒绝。
禁止以内置合并来源或 MergeOrigin 伪造内部父告警。严重等级及升级方式使用当前部署配置。

时间不递减，允许同一时刻按输入顺序处理；最多 128 步、跨度 30 天，请求 3 MiB、响应 8 MiB，
每进程最多 2 个模拟请求，总超时 10 秒。超预算整体失败，不返回截断轨迹。每步先检查到期窗口，
再处理该时刻 Event，之后再执行可提前成功的非周期裁决；不实际等待墙钟。

复用正式 Lifecycle、Suppressor、Merger、MergeJudge 与条件求值，内存状态适配器采用相同规则：
防抖按来源/指纹计数且边界闭合；聚合按配置字段跨来源分组且截止点仍有效；合并窗口为
`[start, deadline)`，按 Alert 去重并重查有效成员，非周期满足条件提前成功，周期到期裁决。
已有活动 Alert 绕过抑制、首条 Event 丰富快照、恢复/关闭清理也由正式 Lifecycle 执行。

返回 `mode=state_simulation`、配置身份/摘要和每步的 `at/event_id/replayed/outcome/decision/alerts/windows`。
`decision` 展示防抖计数、阈值、跳过原因和准入；`windows` 记录本步冻结的窗口、截止时间、成员及
`succeeded/failed`。成功只在模拟成员上建立虚拟合并标记，不创建父 Event/Alert、不模拟模板输出、
父告警人工关闭或实际处置。失败窗口按正式释放规则结束等待。所有 Alert/窗口身份仅作本次轨迹诊断。

目标与业务范围采用请求时的只读资源查询，不能据此复原历史 CMDB；虚拟控制任务按步骤推进，
不模拟真实并发争用、Redis 故障、调度延迟或多策略互相作用。该能力用于验证计数与窗口语义，
不替代真实运行链路验收。模拟与原匹配预览均不写入生产策略统计。

## 逐策略执行观察

`GET /api/v1/policies/statistics?bk_tenant_id=tenant-a&type=merge&ids=p1,p2&hours=6`。
`ids` 为逗号分隔的 1—8 个唯一策略 ID；`hours` 只允许 1、6、24。查询拒绝未知/重复参数。
按 UTC 整点读取包含当前小时的 N 个桶，因此当前桶尚不完整；响应明确给出 `from/to/hours`，
`mode=execution_observations`，每项含：

- `matched`：条件/目标求值命中次数，不等于已抑制、已屏蔽或已合并。
- `not_matched`：正常未命中次数。
- `unavailable`：条件/目标求值无法完成次数。
- `execution_skipped`：执行器记录的跳过，含配置/输入/Redis等故障；可与同次求值观察重叠。

聚合所有版本，包含重试、定时重查与依赖候选匹配；四列不可相加推断唯一 Event 总数。
写入为尽力异步采样，每个采样器最多 4 个在途、每次 50ms、无等待队列，满额丢弃。来源任务
共享采样器；关闭时先停止接收并等待有界在途退出，再关闭 Redis。统计失败不影响业务裁决。
每租户/类型/小时最多 4096 个策略，25 小时 TTL；Redis 丢失或过期不补历史。
读取最多 4 路、3 秒、24个桶及8个策略，后端错误不返回零。没有观察的零值不证明从未执行。
采样失败/丢弃必须结合 `linkd.policy.statistics.samples` 判断，细粒度指标见
[策略观测](../../design/observability.md#策略匹配窗口与统计采样)。

## 指定 Alert 快捷屏蔽

`POST /api/v1/alerts/{alert_id}/shield` 使用管理 JWT，worker token 无权调用。请求体上限 4096 字节：

```json
{
  "bk_tenant_id": "tenant-a",
  "operation_id": "quick-shield-1",
  "operator_id": "admin",
  "expected_revision": 3,
  "policy": {"id": "maintenance", "version": 1, "digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "effective_at": "2026-10-08T08:00:00Z"
}
```

策略必须属于同一租户，且为当前已发布、启用并处于有效时间内的 time_shield。policy 引用使用发布
结果的 id/version/digest，不接受草稿或任意覆盖 Alert 字段。指定活动告警立即建立 origin=manual 的绑定，
不重跑普通策略条件/目标，不创建 Event、不刷新丰富、不修改生命周期或合并关系、不生成 firing。
绑定保存 operation_id/operator_id、冻结策略版本和有效期身份；保留其他已有关系，总数仍至多 16。

执行复用 fingerprint lease 与 Alert CAS，四个并发名额、十秒总预算。成功返回
`{"alert": <当前 Alert>, "already_applied": false}`；相同最近命令重试返回 already_applied=true，
即使关系后来已到期也不重新绑定。LastShieldOperation 仅保留最近命令摘要；更早命令以原
expected_revision 拒绝再次应用。原操作 ID 对应内容变化、目标终态或版本变化返回 409。

Alert.shield、last_shield_operation 与 policy_change 输出意图同次保存；失败可能发生在 CAS 后，调用方
必须重试原命令以补齐流水/输出。定时检查无需原 Event，按冻结有效期、当前策略停用/删除解除手动绑定；
依赖失败保留。普通编辑不替换既有冻结版本，解除不补发动作，等待下一条触发 Event。快捷屏蔽不能撤回
已执行的外部副作用，已排入 Celery 的任务按处理时状态判断。

HTTP 400/401/403/404/409/429/503 分别表示参数、鉴权、租户、对象缺失、冲突、繁忙和未配置；
超时或依赖异常按接口错误返回，状态可能已保存，不能按失败响应推断操作未生效。
