# Alert 主动关闭 API

状态：2026-10-08。接口复用 Lifecycle 的 `CloseAlert`，支持显式人工和系统关闭。

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

| 状态码 | 含义 |
| --- | --- |
| 400 | 字段、字节长度、时间或 JSON 格式无效 |
| 401 / 403 | 管理鉴权失败或来源租户不匹配 |
| 404 | 告警或来源不存在 |
| 409 | 告警已进入其他终态、CAS 冲突未收敛或来源无发布配置 |
| 429 | 四个独立关闭执行名额已满，或同 fingerprint 正在被 Worker 处理 |
| 502 | 依赖失败或执行结果不确定，可能已保存终态；使用原命令重试 |
| 503 | 未配置 Lifecycle、Repository 或 Redis |

请求预算十秒；超时不承诺回滚。成功保存后读取列表或流水仍可能受 ES refresh 延迟影响。
入口与 Lifecycle Worker 共用按租户、来源和 fingerprint 隔离的 Redis lease；请求取消后仍有界释放。
来源发布配置可能在两次请求间变化；重试保持同一操作身份，但按请求时的发布配置装配 Hook。
此边界不提供“恰好一次”外部投递保证，下游继续使用稳定消息身份去重。

系统调用示例：将 operator_kind 设为 system，operation_source 设为 self_heal，operator_id 设为稳定的
后台组件身份；operation_id 来自本次工单/自愈操作，超时重试不得重新生成。KAC 生命周期和屏蔽/合并
清理由 Linkd 推进，KAC 不再直接修改兼容告警的生命周期字段。

KAC 收到 close 动作后只执行后续处置，不再次据此生成关闭命令，避免终结操作回流。
