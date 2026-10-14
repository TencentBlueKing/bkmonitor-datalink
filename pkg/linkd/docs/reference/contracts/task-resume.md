# 确定性任务失败的显式恢复

状态：本次实施新增的管理契约，尚未线上发布。中心与 Worker 使用同一版本。

> 当前错误处理更新：普通消息错误按[运行容量与失败隔离](../../design/runtime-capacity-and-recovery.md)
> 记录失败后移出 Mailbox，不再默认暂停整个来源。本文暂停/恢复流程用于历史 blocked 任务。
> 创建和只读内容预览共享原文兜底白名单，当前范围见下方内容边界。

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

Lifecycle 的普通丰富与内容构建均按 Event 固定的来源版本读取历史 Release，不把积压交给最新配置。内容配置 Reader 只解析运行时查询中实际使用的 `unit`、`metric_id`；未使用的 `index_set_id` 等扩展字段可为空字符串，不因声明模型的类型约束阻断内容构建。实际使用字段、算法、租户和版本绑定仍严格校验。

历史丰富与当前版本共用 `lifecycle.concurrency` 的消费 Worker 预算，不另设固定 4 并发上限；每次历史调用仍独立打开并关闭所需资源，不改变租户、发布版本绑定或同告警 mailbox 顺序。

策略发布材料的确切版本已不可得（`publication_missing`），或无数据文案返回
`nodata_identity_invalid`、`nodata_configuration_invalid`、`nodata_evidence_missing`、
`nodata_evidence_invalid` 时，Lifecycle 沿用该 Event 自带的非空原始 `content`，
通过 WARN 日志记录租户、来源、Event ID、具体失败错误码和 `source_fallback`；
不读取最新策略来替代历史版本，不解析原文补算无数据事实。
只读 opening Event 内容预览使用同一白名单。降级内容仍经过 Alert 校验，并随
EventPlan 固定，后续重试或配置变化不重算已有内容。

原始内容为空/空白或超限、发布材料存在歧义、历史来源 Release 缺失、来源/租户身份或
模板绑定校验失败、其他检测事实缺失及依赖故障均不在此降级范围，仍按当前消息失败策略处理。
无数据文案内部的标记/等级冲突可兜底，不放宽事件的来源与租户身份校验。
恢复接口不改写这些数据。相同来源配置的 PUT 会被发布服务去重，不能用它替代本接口。

## 已落库计划的幂等比较

EventPlan 可保留原始业务 JSON，而 Alert 读取可能已将 JSON 规范化。恢复时，`extra_data` 与 `enrich` 按规范 JSON 比较，键顺序与无意义空白不构成身份冲突；数值、数组顺序及实际内容仍须一致。租户、来源、告警身份、revision、目标绑定和其他业务字段保持严格比较，既有投影 ACK 等完成元数据继续不参与业务快照比较。比较不修改冻结计划或已落库告警，也不把规范化重新放回每次 Event 的处理路径。
