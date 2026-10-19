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
新建执行既有 KAC 结构转换；更新通过 `_bulk` 只写最新内容、等级、动作、生命周期、关闭信息、合并关系和策略标签。
每次 Bulk 默认不超过 4MiB；单条超过预算记失败。只执行当前消息，无补偿扫描或投影 CAS。

## 索引与字段所有权

沿用原 KAC alias、mapping、模板、ILM 和 rollover。首次索引准备成功后才消费队列。
按稳定 ID 批量查询实际物理索引，内存位置缓存上限 100,000 项，达到上限清空后重新定位；不维护位置元数据索引。
rollover 后继续更新原物理索引，不能总向当前 write alias upsert。

| 字段 | 行为 |
| --- | --- |
| 身份、来源、丰富数据、storage_time 和处置默认值 | 新建时转换，后续普通同步不重置 |
| content、level、action、source_alarm_status、close_reason、close_time | 从最新 Alert 更新；content 包含持续触发更新及显式空串 |
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

## 策略关联事实与详情导航（2026-10-15）

新 `strategy` 丰富结果不含 URL。KAC 输出适配把冻结事实放入
`field_extra_info.strategy_name.strategy_context`：

**2026-10-16 本地实现：** KAC 输出已使用下表明确名称，尚未部署。
Linkd 原始 labels 和 enrich 内部保留 `strategy_id/strategy_version`，该命名承接冻结执行身份，不用于 KAC 查询时重建参数。

| 字段 | 类型与语义 |
| --- | --- |
| `bk_tenant_id` | Alert 或恢复 Event 的租户，不能由来源全局配置覆盖 |
| `strategy_set_split_record_id` | 目标名称，来自 alarmd `strategy_id`，对应历史 Reader 的 `strategy_id`；不是监控模板 ID |
| `strategy_set_split_record_version` | 目标名称，来自 alarmd `strategy_version`，对应历史 Reader 的 `strategy_revision`；与 ID 联合唯一 |
| `monitor_template_id` | 监控模板 ID，对应既有 KAC 顶层 `strategy_id` |
| `strategy_config_id` | 原有规则规范化后的配置 UID，保留监控项抽屉定位 |
| `strategy_kind` | `Strategy` / `StrategyCloud` |
| `is_default` | bool，缺失与 false 不同；不推断缺失值，云策略仅凭类别即可选路，普通策略缺失时不生成导航 |
| `strategy_instance` | 非默认实例策略关联，包含字符串 `object_model_code`、`bk_obj_id`、`bk_inst_id`、`object_model_group_id`；实例 ID 不转数字 |

`field_extra_info` 按展示字段组织，`strategy_name` 是已有“监控策略名称”字段，原本就保存 URL；
导航迁移在同一字段下增加内部 `strategy_context`，不新增展示字段或 KAC 顶层列。
2026-10-19 撤回指标查询参数迁移：Linkd Metric Processor 继续为新内置及外部事件生成兼容查询参数，
随同次事件丰富结果冻结，并在新建 KAC 文档时编码为 `metric_query_params` JSON 字符串。
KAC 独立指标及邮件继续来源插件协议；导航 `strategy_context` 不触发另一条查询分支。
Linkd 的准确执行历史读取和文案降级保留；不新增 KAC 历史重建、结构化取数入口或事实查询接口。
历史已有参数不清理、ES 和 Event 不回填，不修改执行历史快照。完整边界在 Kingeye
`docs/specs/kac-linkd-integration.md` 第 8.6 节维护。

`strategy_name` 和 `data_source` 继续由 Linkd 生成或透传并投影，KAC 消费已保存结果；
两者不再属于后续展示迁移项，职责依据见 [strategy 输出](../../design/enrich.md#61-strategy)。

KAC 共用详情组装层按以上事实选择路由，读取当前 `KINGEYE_WEB_SAAS_MODULE_URL`，
并处理观测融合参数。Linkd 不含前端路由常量，不查询产品页面开关。
新告警不依赖反解析 URL，恢复详情使用引用的恢复 Event 事实。
恢复业务字段投影同样生成 `strategy_context`，只使用该恢复 Event 已冻结并生效的丰富结果；
不借用 opening Alert 的策略版本、不重跑丰富；当次冻结的查询参数继续透传，缺失时不在读取入口补造。

历史 Alert 丰富结果中的 `url` 继续解码、重放；来源自定义 `field_extra_info` 覆盖规则保留。
KAC 对历史内置告警只解析已知管理/实例路由，保留 ID 并换用当前基础地址；
外部自定义链接原样承接。必要事实缺失时展示名称。详情转换不写 ES、不查询最新策略，
既有投影更新保留 `field_extra_info` 的约定不变，因此无需批量修复历史告警。
`strategy` Processor 不读取或校验 `web_saas_module_url`，该键是否存在及其取值均不影响关联事实输出；新来源发布不再带此配置。

部署先升级支持新旧两种输入的 KAC，再升级 Linkd；本契约和本地回归不代表线上已部署或完成页面验收。
