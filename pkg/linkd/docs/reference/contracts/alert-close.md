# Alert 主动关闭 API

状态：2026-10-29。本次批量路径为本地实现，尚未部署验证。接口复用 Lifecycle 的 `CloseAlert`，支持显式人工、系统关闭和按条件批量关闭。

`POST /api/v1/alerts/{alert_id}/close` 使用控制面管理 JWT（`Internal-Token: Bearer <JWT>`）；worker token 无权调用。
Console 代理为 `POST /local-api/alerts/{alert_id}/close`，浏览器无需也不能获取管理 JWT。

```json
{
  "bk_tenant_id": "tenant-a",
  "operation_id": "581e3d13-c28b-45be-b06e-bc3f21d233cc",
  "operator_id": "operator-a",
  "operator_kind": "user",
  "operation_source": "manual",
  "reason": "人工确认结束",
  "effective_at": "2026-09-23T04:00:00Z"
}
```

| 字段 | 约束 |
| --- | --- |
| `alert_id` | URL 中的稳定 Alert ID，最长 160 字节 |
| `bk_tenant_id` | 必填、最长 64 字节；只读取此租户的目标告警 |
| `operation_id` | 本次用户操作的稳定幂等标识，最长 128 字节；Console 每次明确操作生成一次 UUID，重试复用 |
| `operator_id` | 必填、最长 256 字节；Console 从服务端认证身份填充，浏览器请求不得包含此字段 |
| `operator_kind` | user/system；省略为 user。Console 固定使用用户操作，系统调用显式传 system |
| `operation_source` | manual、auto_policy、work_order、self_heal、strategy_change、system；manual 只允许 user，自动来源只允许 system，work_order 可为两者。省略按操作者取 manual/system，内部配置关闭取 strategy_change |
| `reason` | 必填、最长 256 UTF-8 字节 |
| `effective_at` | 显式操作时间，RFC3339；重试保持原值，不能超过服务端当前时间五分钟 |

请求体上限 4096 字节，拒绝未知字段。仅关闭 active Alert；相同命令的终态重试可以修复近期缓存并补齐
流水和输出，不能修改既有终态原因或时间。Alert.end_operation 固定 id/source/operator_kind/operator_id/config_digest；重试同时校验这份操作身份及
结束类型、原因和结束时间。同 ID 改内容、换 ID/操作者重复终结均返回冲突；原命令重试补齐部分完成的输出。接口不是跨多告警的事务。自愈完成要求关闭仍为 closed，不冒充来源恢复。

成功返回 `200` 和 `{ "alert": <关闭后的 Alert>, "already_closed": false }`；重复完成返回 `already_closed: true`。
响应为 `Cache-Control: no-store`。结束类型按操作者保存 user/system，不接受关闭以外动作；不创建 Event、不会更改 latest_event_id。
执行当前已发布来源的全部 FinalHook（不重新丰富），普通 Hook 失败记录在 push 流水中，不回滚已保存状态。
启用 `plugins.kac` 时，管理入口和集成入口在返回 200 前共用控制面的投影写入器和执行通道。
主动关闭优先于尚未执行的普通批次，直接写入 `refresh=true` 后返回，不回读水位或创建确认记录。
同步写入失败返回 502，Linkd 关闭事实保留；使用原命令重试可再次执行同步投影。
详见[KAC 兼容投影](kac-alert-projection-v1.md)。同步成功不代表 KAC 后续处置完成。
未启用插件时不执行 KAC 写入。

| 状态码 | 含义 |
| --- | --- |
| 400 | 字段、字节长度、时间或 JSON 格式无效 |
| 401 / 403 | 管理鉴权失败或来源租户不匹配 |
| 404 | 告警或来源不存在 |
| 409 | 告警已进入其他终态、CAS 冲突未收敛或来源无发布配置 |
| 429 | 对应入口的独立执行名额已满，或同 fingerprint 正在被 Worker 处理 |
| 502 | 依赖失败或执行结果不确定，可能已保存终态；使用原命令重试 |
| 503 | 未配置 Lifecycle、Repository 或 Redis |

请求预算十秒，包含同步 KAC 投影；超时不承诺回滚。Linkd 列表或流水仍可能受自身 ES refresh 延迟影响。
入口与 Lifecycle Worker 共用按租户、来源和 fingerprint 隔离的 Redis lease；请求取消后仍有界释放。
来源发布配置可能在两次请求间变化；重试保持同一操作身份，但按请求时的发布配置装配 Hook。
此边界不提供“恰好一次”外部投递保证，下游继续使用稳定消息身份去重。

系统调用示例：将 operator_kind 设为 system，operation_source 设为 self_heal，operator_id 设为稳定的
后台组件身份；operation_id 来自本次工单/自愈操作，超时重试不得重新生成。KAC 生命周期和屏蔽/合并
清理由 Linkd 推进，KAC 不再直接修改兼容告警的生命周期字段。

KAC 收到 close 动作后只执行后续处置，不再次据此生成关闭命令，避免终结操作回流。

## 按 filter 批量关闭

`POST /api/v1/alerts/close` 使用相同管理 JWT。`filter` 使用
[通用检索的 Elasticsearch JSON Query DSL](alert-search.md)，必填且必须为对象。
服务端额外限制 `bk_tenant_id` 和 `status=active`；不能通过 DSL 扩大租户范围或关闭其他终态。
当前仅支持 Elasticsearch Repository，MySQL 返回 `501`。

```json
{
  "bk_tenant_id": "tenant-a",
  "filter": {
    "bool": {
      "filter": [
        {"term": {"event_source_id": "built_in_bk"}},
        {"term": {"labels.strategy_id": "123"}}
      ]
    }
  },
  "operation_id": "close-strategy-123-20261026",
  "operator_id": "operator-a",
  "operator_kind": "user",
  "operation_source": "manual",
  "reason": "人工确认结束",
  "effective_at": "2026-10-26T04:00:00Z",
  "limit": 100,
  "dry_run": false
}
```

操作字段与单条关闭完全相同。一次批量操作在所有目标上使用同一个 `operation_id`、操作者、原因和
`effective_at`；既有关闭日志身份已包含 Alert ID，不额外生成随机身份或操作摘要。
可先设置 `dry_run=true` 只查询本批目标，此时可省略所有关闭操作字段，也不要求关闭器已配置。
显式使用 `filter={"match_all":{}}` 选择该租户全部可搜索的活动候选。

每次最多枚举 `limit` 个目标，省略使用 `control_plane.close.batch_size`（默认 1000），允许 1–5000，按 Alert ID 升序分页。
候选搜索只返回 Alert ID，不返回完整正文和丰富数据；普通检索接口仍保持 1–500 的分页预算。
非空 `next` 传入下次请求的 `after` 后继续下一批，其他条件和操作字段保持不变。
一次请求不自动扫描全部历史，也不创建后台任务。

成功枚举后返回 `200` 和逐项结果，例如：

```json
{
  "items": [
    {"alert_id": "alert-a", "status": 200},
    {"alert_id": "alert-b", "status": 429, "error_code": "busy", "message": "alert is being processed; retry the same operation"}
  ],
  "next": "alert-b",
  "dry_run": false,
  "closed": 1,
  "failed": 1
}
```

`status` 是单条关闭的 HTTP 状态语义。`closed` 统计成功关闭及原命令幂等补齐的数量；
相同命令已关闭时该项带 `already_closed=true`。`failed` 包括执行失败与未开始执行的项。
`dry_run=true` 的每项 `status=200` 仅表示候选，`closed/failed` 均为 0。
空匹配返回空 `items`、空 `next` 和零计数。

批量入口显式提交整个候选批次，不循环调用单条 `CloseAlert`，处理方式参照合并成员批次：

1. 候选只取 ID；批读租户、Alert ID、来源、来源版本和 fingerprint 五个身份字段，同 fingerprint 只取得一次 lease。每个来源发布记录及所需发布版本每批读取一次。
2. 持有全部已取得的 lease 后，按租户及来源发布配置分组批读关闭所需状态，不读取正文、标题或 Enrich；无数据联动只读取必要原始事实。仅仍启用旧 KAC Kafka Hook 的来源才按需使用完整快照；通用 `type: kafka` 已移除并忽略。
3. 使用 ES Bulk update 逐项携带版本条件，只替换关闭涉及的 12 个状态及派生字段，不回写正文或丰富数据；只对版本冲突项批量重读和重试，最多沿用既有 CAS 尝试次数。
4. 对成功项 pipeline 更新近期终态缓存；主动关闭缓存仅保留用于 active lookup 负缓存与修复的状态，不携带正文、Enrich 或扩展数据。随后执行既有清理和 FinalHook，流水按存储批次写入。
5. 释放 lease 后，对成功项窄批读并 Bulk 同步投影 KAC，仅写 5 个终态字段；KAC 文档不存在时才补读该项完整快照创建。保留逐项错误及 `refresh=true` 的成功语义。

控制面复用存储、Redis、来源 Hook 和终态清理 Journal，不为每条关闭打开连接或检查存储 schema。
批大小、批次并发及成员并发分别配置，完全独立于单条关闭、查询和 Lifecycle Worker：
`control_plane.close.batch_size` 默认 1000（最大 5000）；`concurrency` 默认 8（在途筛选关闭请求）；
`member_concurrency` 默认 32（每来源子批的成员准备、前置调用、索引清理和 Hook）。
请求 `limit` 大于配置批大小时连续处理多个批次，仍共享请求截止时间；不按成员并发数截短 Bulk。
较大状态、绑定或关系仍可能触发既有存储字节预算，配置 5000 不代表所有载荷都保证在十秒内完成。
外部无数据关闭和自定义 Hook 仍按现有接口逐项调用，不承诺这些外部接口已经支持批量协议。
过滤仅在候选读取时求值，不保证条件在执行关闭时仍未变化；锁内继续检查实时生命周期与操作身份。
它不是多告警事务，部分成功不会回滚。

查询阶段短暂使用通用检索的并发槽及正文预算，返回候选后立即释放；关闭使用独立名额。查询阶段使用 `dispatch.alert_search.timeout_seconds`，
全部关闭共享管理 API 十秒总预算。请求在批次开始前已经取消时，每项返回 `status=504` 和
`error_code=not_attempted`；批次开始后的取消传播到读写、前置调用及输出，不再逐条消耗完整超时。
失败项可能已经保存终态，返回原关闭器的结果不确定语义；取消后仍有界释放已取得的租约。

**失败项和未执行项必须使用原操作字段，按其 `alert_id` 调用单条关闭接口重试。**
`next` 只用于继续枚举本批之后的候选，不代表失败项已经处理完成。
不能只重新执行 active filter：CAS 成功但输出失败的告警已经不再匹配 active，仍需要按 ID 补齐输出。
请求丢失响应时，筛选重试也不保证重新找到全部原目标；需要可靠追踪的调用方应先用 dry-run 保存本批 ID，
再按这些 ID 调用单条关闭。多次请求不持有固定快照，也不冻结批次成员。

鉴权、DSL、容量和搜索阶段错误使用[检索接口状态码](alert-search.md#预算与错误)，并且不会执行任何关闭。
缺少关闭器返回 `503`；缺少操作字段等返回 `400`。一旦开始逐条执行，顶层 `200` 只表示返回本批结果，
不能据此判断全部成功。

## alarmd 无数据关闭联动

配置 `resources.alarmd.no_data_close_endpoint` 后，主动关闭先通知 alarmd 停止当前无数据过程的追踪；
地址未配置或为空时完全跳过联动，沿用原关闭行为。仅同时满足以下条件的原始 Alert 参与：

- `event_source_id` 精确等于配置的 alarmd 渠道 ID（缺省 `built_in_bk`）；
- `dimensions.__NO_DATA_DIMENSION__` 按既有真值规则有效；
- `extra_data.evaluation_family` 等于 `no_data`。

人工、自动关闭策略、工单、自愈及系统超时关闭参与；来源恢复、来源关闭、等级切换和策略变更不参与。
其他渠道即使携带相同标记也不调用，不按标题、内容或丰富后的展示字段推断。

在现有 fingerprint lease 内、终态及操作身份判断之后、关闭 CAS 之前，向配置地址发送一次 POST。
请求为 `{bk_tenant_id, strategy_id, source_alert_id}`，分别来自原始 Alert 的 `bk_tenant_id`、
`labels.strategy_id`、`source_alert_id`；不使用 KAC 监控模板 ID 或 Linkd Alert ID。
该接口为 alarmd control 角色的 `/internal/v1/no-data-closures`，仅在 `http.internal_listen` 上提供，
无需 Token。请求服从关闭总预算，单次默认 2000 毫秒，不额外重试。

必要身份缺失或策略 ID 无效返回 400；网络、超时或非 200 响应返回 502，当前 Alert 不推进关闭，
KAC 继续复用原命令重试。一次关闭尝试中的 CAS 重试不重复通知。同一命令已保存终态时只补齐输出，
不再通知 alarmd。alarmd 已记录但 Linkd 尚未保存关闭时，原命令重试可能再次通知；这是非原子调用，
alarmd 按接收时间记录关闭，不提供恰好一次保证。

这里不物理删除历史记录，也不拦截已进入 Kafka 的积压事件。数据恢复后再次缺失仍可重新告警。
配置和发布要求见[配置指南](../../guides/configuration.md#alarmd-无数据关闭联动)。

## KAC 集成入口

KAC 使用 `BKAPP_KAC_LINKD_API_URL` 调用 `POST /api/v1/integrations/kac/alerts/{alert_id}/close`。
此入口当前不校验 token；`bk_tenant_id` 只限定读写范围，不构成调用方身份验证。部署时须将控制面限制在受信内部网络。

请求字段与管理入口相同。接口同步执行单个 Alert 的状态转换；返回 200 时，`alert.status` 为 `closed`，
`alert.close_operation_id` 是已保存的 `Alert.end_operation.id`，须与请求的 `operation_id` 一致。
响应中的 `alert` 只包含 `bk_tenant_id`、`alert_id`、`status`、`close_operation_id`，不返回告警正文或扩展数据。
`already_closed=true` 表示同一命令此前已生效，并在本次补齐缓存、Hook、流水及启用的 KAC 投影。

`GET /api/v1/integrations/kac/alerts/{alert_id}?bk_tenant_id={tenant_id}` 返回同样的最小快照，
供 KAC 在 409 或不确定结果后核对关闭身份。只有状态和操作 ID 都匹配，才可认定本命令已关闭 Linkd 目标；GET 快照不证明 KAC 投影完成，
关闭请求返回 502 后仍须复用原命令重试 POST，以取得同步投影确认。
KAC 在超时、429 或 5xx 后复用原命令重试；接口每次只关闭一条 Alert。

集成入口错误响应为 `{ "error_code": "...", "message": "..." }`：400 `invalid_command`、
403 `tenant_mismatch`、404 `not_found`、409 `invalid_transition` / `version_conflict` / `source_unpublished`、
429 `busy`、502 `result_uncertain`、503 `unavailable`。
