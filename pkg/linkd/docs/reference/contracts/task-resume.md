# 确定性任务失败的显式恢复

状态：本次实施新增的管理契约，尚未线上发布。中心与 Worker 使用同一版本。

## 暂停与诊断

`bkmonitor_description` 内容构建出现确定性错误时，Lifecycle 保留 mailbox 队首，Signal 不 XACK，且不保存该 Event 的处理计划或创建错误 Alert。消费 Runtime 退出后，Worker 的确切任务代次报告 `blocked=true`。中心将标记存入协调快照，停止同来源、同角色的其他副本，并禁止自动重新分配该来源角色。中心或 Worker 重启、时间推进、扩容和新 Release 都不会自动解除标记。暂时性连接失败继续走原有有界重试和调度退避。

管理 JWT 鉴权的 `GET /api/v1/runtime` 返回 `tasks[*].blocked`、`source/role/epoch/version/phase`，对应 status 的 reason 为 `task requires repair and explicit resume`。Worker 日志 reason_code 为 `task_requires_repair`；Scheduler 日志另含租户、来源、Event ID 和内容错误码，不记录完整输入或凭据。授权到期时仍保留 blocked 标记；停止未完成时不允许提前恢复。

## 恢复接口

```http
POST /api/v1/event-sources/{source_id}/tasks/{task_id}/resume
Internal-Token: <管理 JWT>
Content-Type: application/json

{"expected_source_version":7,"expected_epoch":42}
```

- 沿用来源管理的 JWT 权限边界；Worker Token 无权调用。
- 路径必须与协调快照中的来源和 task ID 精确相符，版本与 epoch 都为正数并匹配。
- 该来源角色的全部任务均须为 `stopped`，确保旧消费所有权已释放。
- 中心使用 Leader 身份与完整旧状态的 Redis CAS 解除确切任务的 blocked 和 RetryAfter。不会确认 Signal、删除 mailbox、修改 Event、生成 Alert 或重写 Release。
- 返回 `204` 表示解除已提交。规划器随后创建新 epoch，并携带 retired consumer 记录接管 PEL；不复活旧 epoch。
- 同一 stopped 代次的重复恢复幂等。已分配后续代次时，旧请求返回 `409`，不能解除后续失败。
- 未找到同来源任务返回 `404`；代次、版本或停止握手不符返回 `409`；请求不合法返回 `400`；管理认证失败返回 `401`。

多个失败任务各自持有 blocked 标记，需要逐一恢复；只要仍有一个标记，该来源角色就保持暂停。

## 操作顺序与内容边界

1. 从 runtime 和 Scheduler 日志定位失败 Event、错误码与任务代次。
2. 修复读取/模板实现或补齐稳定检测事实，并用 opening Event 的只读预览验证。修复依赖不意味着可以用最新策略替代触发时策略。
3. 保持与失败 Event 一致的来源 Release，完成 Worker 停止握手后调用恢复接口。
4. 核对新 epoch、PEL/mailbox 顺序以及最终 Alert.content。失败 Event 创建成功前一直保留；已经保存的 EventPlan 和 Alert.content 不重新生成。

来源版本已切换、历史配置或检测事实仍不可得时，当前 Router 会再次阻止内容生成。恢复接口不提供历史 Release 路由或字段改写功能；这些缺口见[内容生成方案](../../design/alert-content-generation.md#本轮跳过的数据与后续恢复条件2026-09-30-1748)。相同来源配置的 PUT 会被发布服务去重，不能用它替代本接口。
