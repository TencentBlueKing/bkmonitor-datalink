# KAC 告警兼容投影

状态：2026-10-14 对应当前实现。文件名中的 v1 保留稳定 alarm_id 的身份域；当前写入不再使用持久投影任务、快照、摘要或水位。

## 身份与消息

一个租户的一个 Linkd Alert 生命周期对应一条 KAC 文档。alarm_id 为 `linkd-` 加 UUIDv5：
namespace 使用 URL，name 为紧凑 JSON 数组 `["linkd:kac-alert-projection:v1", bk_tenant_id, alert_id]`，保持 Go JSON 转义规则。
身份不随告警版本、动作或 rollover 改变，ES `_id` 使用相同值。
主流程保存 Alert 后向部署专属 Redis Stream 发布租户、Alert ID 和必要动作信息，不做同步 KAC HTTP 调用。

## 批量写入

控制面按租户与 Alert 固定分配到 64 个通道，同一告警串行，不同告警并发。
每个通道最多收集 500 条、等待 10ms；批内重复投影合并，动作独立保留。
按租户批量读取最新 Alert，ES 使用 realtime `_mget`，不依赖搜索 refresh。
新建执行既有 KAC 结构转换；更新通过 `_bulk` 只写等级、动作、生命周期、关闭信息、合并关系和策略标签。
每次 Bulk 默认不超过 4MiB；单条超过预算记失败。只执行当前消息，无补偿扫描或投影 CAS。

## 索引与字段所有权

沿用原 KAC alias、mapping、模板、ILM 和 rollover。首次索引准备成功后才消费队列。
按稳定 ID 批量查询实际物理索引，内存位置缓存上限 100,000 项，达到上限清空后重新定位；不维护位置元数据索引。
rollover 后继续更新原物理索引，不能总向当前 write alias upsert。

| 字段 | 行为 |
| --- | --- |
| 身份、来源、内容、丰富数据、storage_time 和处置默认值 | 新建时转换，后续普通同步不重置 |
| level、action、source_alarm_status、close_reason、close_time | 从最新 Alert 更新 |
| associate_alarm_id、associate_count | 同步当前合并关系及解联 |
| status | 屏蔽、合并及终态按业务转换；普通 active 保留 KAC 处置状态 |
| linkd_disposal_status | KAC 文档内的业务字段；屏蔽/合并前保存原处置状态，解除后恢复 |
| tag_info | 追加合并，保留已有标签 |
| conductor、工单、通知、自愈和 field_extra_info | 更新保留 KAC 已写入内容 |

不创建同步元数据索引，不回读版本、校验内容摘要或记录 Receipt。

## 主动关闭

主动关闭保存 Linkd 终态后，使用同一个写入器与通道优先取得执行机会，等待正在执行的普通批次结束，先于尚未开始的普通批次执行。
在通道内重新读取最新事实，直接 Bulk `refresh=true`，成功后返回；后续普通批次同样重新读取，避免旧队列消息写回旧状态。
写入失败返回 502，保留 Linkd 关闭事实，原命令重试重新执行同步写入。没有独立确认记录或回读水位。

## 失败与观测

普通执行失败记录明确日志及指标并结束当前消息，不阻塞主流程。部分 Bulk 失败按单项处理。
动作发送在成功投影后独立刷新和并发执行；优雅退出及队列恢复见[动作 V3](kac-action-delivery-v3.md)。
旧投影任务 API、快照、人工恢复、水位及页面已移除；旧存储不自动删除。
实现入口为 `internal/kaccompat/runtime.go`、`writer.go` 与 `internal/alertqueue/queue.go`。
