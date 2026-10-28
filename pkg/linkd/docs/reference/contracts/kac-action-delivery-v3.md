# KAC 动作投递 V3

状态：2026-10-14 对应 Linkd 与 Kingeye 配套实现。普通链路保存 Linkd Alert 后发布轻量消息；控制面完成投影后独立发送动作。

## 消息与动作资格

Redis Streams 通知包含 `bk_tenant_id`、`alert_id` 和可选 `action`，不保存完整 Alert。
动作资格沿用 Lifecycle 的策略裁决：新建、允许的触发及终态可携带动作；普通屏蔽解除、合并解联仅同步状态。
同批重复通知合并投影，动作逐条保留。动作 ID 由原租户、Alert、业务版本、动作和原因生成稳定 UUID；重投不使用当前时间。
业务 CAS 和审计仍由 Lifecycle 负责。消息发布失败只记日志与指标，不回滚已保存的 Alert，也不创建入队确认或补偿记录。

## HTTP 请求

向 `plugins.kac.action_endpoint` 发送 POST，保留 `Internal-Token: Bearer <JWT>`、HTTP 参数校验及租户作用域。
KAC 路由为 `/kac/internal/linkd/action/`，部署前缀由实际部署确定。请求上限 16KiB。

```json
{
  "schema_version": "linkd.kac-action.v3",
  "bk_tenant_id": "tenant-a",
  "linkd_alert_id": "alert-a",
  "action_id": "fb2a1efb-2da9-54bc-a522-722f3d0e949c",
  "action": "firing",
  "cause": {"type": "source_event", "id": "event-a"}
}
```

`action` 支持 firing/resolved/close。`cause.type` 支持 source_event/user_operation/system_operation。
终态附带原业务实际使用的 `end_operation`（存在时）、`severity`、`end_type`，供 KAC 终态审计消费。
HTTP 字段不变，Celery 消息的 `alarm_id` 直接等于 `linkd_alert_id`；KAC Worker 仅按租户和原生 ID 读取文档并执行处置。
旧 UUID 消息不兼容，切换前暂停 Linkd 投影和动作投递并完成在途请求及旧 Celery 动作；请求不包含 `alert`、版本确认、目标、内容摘要或回执证明。

KAC 同步投递 Celery，成功返回 HTTP 200：

```json
{"outcome":"queued","task_id":"celery-task-id"}
```

Linkd 只按 HTTP 成败记录结果，不回读版本或核对回执。成功只表示 KAC 接受投递，不表示通知、自愈或工单已完成。
KAC 业务处理继续读取当前 ES 告警，租户显式传给 Celery。重复请求可产生不同 task_id；不提供队列层 exactly-once 保证。

## 并发、失败与重启

投影和动作使用独立并发；默认均为 64，动作缓冲 10,000 条。发送前按批次集中 refresh 涉及的物理索引，随后并发调用 KAC。
投影线程只投递到内存动作队列，不等待动作接口。队列满、refresh 或发送失败均记录明确阶段/租户/Alert/动作日志及失败指标，结束当前消息，不建立失败任务。
ES `_bulk` 部分失败只结束失败项，成功项仍可进入动作发送。普通投影、动作调用有 10 秒超时。

收到停止信号后停止读 Stream 和启动新工作，已开始的调用使用独立超时上下文，运行器等待其结束。
已完成消息批量 XACK/XDEL；未开始的通道、刷新队列和发送队列消息留在同一 consumer 的 PEL 中，重启先恢复这些消息，再消费新增消息。
这只使用原 Redis Stream，不维护第二份任务状态；强制杀进程或确认响应丢失仍可能重复执行。
HTTP 服务退出预算 30 秒；部署须给足 terminationGracePeriodSeconds（Helm 当前默认 60 秒）。沿用单活动控制面，不能并行运行两套消费者。

## Console 运行观测

Console `/kac-output` 和 Kingeye Admin 的 KAC 输出入口展示吞吐、耗时、积压和最早消息等待。
指标为 `linkd.kac.items`、`linkd.kac.duration`、`linkd.kac.queue.messages`、`linkd.kac.queue.oldest_age`。
阶段及结果标签为固定低基数值，业务身份只记录日志。指标按进程查询，不携带租户/Alert 条件；无时序不等于零。
旧任务查询、快照、人工恢复 API 及页面均已删除。旧任务存储不迁移、不自动删除，两端使用 V3 配套部署。

投影字段所有权见[兼容存储](kac-alert-projection-v1.md)，主动关闭见[关闭接口](alert-close.md)。
