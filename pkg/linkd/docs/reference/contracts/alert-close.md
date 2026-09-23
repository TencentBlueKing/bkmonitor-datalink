# Alert 主动关闭 API

状态：当前 v1 实现，2026-09-23。接口复用 Lifecycle 的 `CloseAlert`，只允许显式人工关闭。

`POST /api/v1/alerts/{alert_id}/close` 使用控制面管理 Bearer token；worker token 无权调用。
Console 代理为 `POST /local-api/alerts/{alert_id}/close`，浏览器无需也不能获取管理 token。

```json
{
  "bk_tenant_id": "tenant-a",
  "operation_id": "581e3d13-c28b-45be-b06e-bc3f21d233cc",
  "operator_id": "operator-a",
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
| `reason` | 必填、最长 256 UTF-8 字节 |
| `effective_at` | 显式操作时间，RFC3339；重试保持原值，不能超过服务端当前时间五分钟 |

请求体上限 4096 字节，拒绝未知字段。仅关闭 active Alert；相同命令的终态重试可以修复近期缓存并补齐
流水和输出，不能修改既有终态原因或时间。当前命令一致性依赖既有 CloseAlert 的结束类型、原因和结束时间检查，
操作 ID 用于流水和 Hook 身份；调用方必须保持全部命令字段不变。接口不是跨多告警的事务。

成功返回 `200` 和 `{ "alert": <关闭后的 Alert>, "already_closed": false }`；重复完成返回 `already_closed: true`。
响应为 `Cache-Control: no-store`。操作方固定为 `user`，不接受关闭以外动作；不创建 Event、不会更改 latest_event_id。
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
