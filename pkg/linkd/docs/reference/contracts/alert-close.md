# Alert 主动关闭 API

状态：2026-10-14。接口复用 Lifecycle 的 `CloseAlert`，支持显式人工和系统关闭。

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
启用 `plugins.kac` 时，管理入口与 KAC 集成入口均在返回 200 前同步推进本次关闭版本的
[KAC 持久投影任务](kac-alert-projection-v1.md#主流程优先与一次补偿)，确认兼容文档可搜索并完成本地水位 ACK。
同步执行与后台共用稳定任务身份、目标租约和重试预算，不等待五秒补扫或一秒投递周期。
投影未就绪、锁忙、退避、失败或超时返回 502；关闭事实不回滚，原命令重试补齐投影，后台仍推进未完成待办。
失败任务遵循一次后台补偿及显式人工恢复规则，不因重复关闭重新生成任务或重置重试预算。
未启用插件时不执行 KAC 投影；同步投影成功不代表 KAC 后续处置已完成。

| 状态码 | 含义 |
| --- | --- |
| 400 | 字段、字节长度、时间或 JSON 格式无效 |
| 401 / 403 | 管理鉴权失败或来源租户不匹配 |
| 404 | 告警或来源不存在 |
| 409 | 告警已进入其他终态、CAS 冲突未收敛或来源无发布配置 |
| 429 | 四个独立关闭执行名额已满，或同 fingerprint 正在被 Worker 处理 |
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
