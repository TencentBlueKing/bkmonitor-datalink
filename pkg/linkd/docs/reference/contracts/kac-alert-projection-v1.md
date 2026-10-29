# KAC 告警兼容投影

状态：2026-10-28 对应当前实现。文件名沿用既有路径；当前写入不使用持久投影任务、快照、摘要或水位。

## 身份与消息

一个租户的一个 Linkd Alert 生命周期对应一条原生 ID KAC 文档。
当前告警 ID 为 `alert-<14 位 UTC 时间>-<13 位小写 Base32 摘要>`，事件 ID 为 `event-<14 位 UTC 时间>-<13 位小写 Base32 摘要>`，均为 34 字符；租户与来源取独立字段。
`alarm_id` 与 ES `_id` 直接等于 Linkd `alert_id`，`event_id` 固定使用 `trigger_event_id`。
不兼容旧 `linkd-<UUID>`：旧文档脱离关联，不更新、不迁移、不删除，用户后续在 KAC 手动关闭。
持续中的 Linkd 告警下次投影按原生 ID 创建新文档，可以与旧记录并存；处置信息不从旧记录继承。
新文档沿用处置默认值，动作仍取自原生命周期资格，不主动重放历史动作。
主流程保存 Alert 后向部署专属 Redis Stream 发布租户、Alert ID 和必要动作信息，不做同步 KAC HTTP 调用。

## K8s 固定字段（2026-10-28）

创建 KAC 文档时，从 opening Alert 冻结的有效 labels / extra_data 投影
`bcs_cluster_id`、`cluster_name`、小写 `namespace`、`service`、`workload_kind`、
`workload_name`、`pod_name`、`container_name`。遵循 labels 优先、空字符串不发送的规则，
不依赖自定义字段目录。大写 `Namespace` 保持旧协议语义，不用于 K8s 命名空间。
K8s 的 `cw_labels` 与其他场景一样承接有效丰富结果。

本地回放不更新远端既有文档。后续普通同步仍遵守丰富字段只在新建时写入的所有权，
修复代码不会自动回填已有 KAC 文档；回填、重建或共享环境重放必须另行明确授权。

## 批量写入

控制面按租户与 Alert 固定分配到 64 个通道，同一告警串行，不同告警并发。
每个通道最多收集 500 条、等待 10ms；批内重复投影合并，动作独立保留。
按租户批量读取最新 Alert，ES 使用 realtime `_mget`，不依赖搜索 refresh。
新建执行既有 KAC 结构转换；更新通过 `_bulk` 只写最新内容、等级、动作、生命周期、关闭信息、合并关系和策略标签。
每次 Bulk 默认不超过 4MiB；单条超过预算记失败。只执行当前消息，无补偿扫描或投影 CAS。

## 索引与字段所有权

沿用原 KAC alias、mapping、模板、ILM 和 rollover。首次索引准备成功后才消费队列。
仅按原生 ID 定位物理索引，多租户候选在一次搜索中分别限定租户；查询失败直接返回错误。
位置缓存按租户和原生 ID 保存物理索引，上限 100,000 项，达到上限清空后重新定位。
rollover 后更新原物理索引，不向当前 write alias 重复创建。
父子引用直接使用原生 ID，不查父文档；尚未投影的父告警随后落库使用同一 ID。
旧 Kafka Hook 输出契约保持不变，不新增位置元数据索引。

| 字段 | 行为 |
| --- | --- |
| 身份、来源、丰富数据、storage_time 和处置默认值 | 新建时转换，后续普通同步不重置 |
| fingerprint | 写入器直接取 Alert.Fingerprint，新建与后续同步均写入，不依赖自定义字段目录 |
| content、level、action、source_alarm_status、close_reason、close_time | 从最新 Alert 更新；content 包含持续触发更新及显式空串 |
| associate_alarm_id、associate_count | 同步当前合并关系及解联 |
| status | 屏蔽、合并及终态按业务转换；普通 active 保留 KAC 处置状态 |
| linkd_disposal_status | KAC 文档内的业务字段；屏蔽/合并前保存原处置状态，解除后恢复 |
| tag_info | 追加合并，保留已有标签 |
| conductor、工单、通知、自愈和 field_extra_info | 更新保留 KAC 已写入内容 |

不创建同步元数据索引，不回读版本、校验内容摘要或记录 Receipt。

指纹在既有索引及模板中声明为 `keyword`，用于 KAC 原样展示和精确搜索。
仅原生 ID 文档随正常投影补齐指纹；旧 UUID 文档保持原样，不新增回填任务。

## 主动关闭

主动关闭保存 Linkd 终态后，使用同一个写入器与通道优先取得执行机会，等待正在执行的普通批次结束，先于尚未开始的普通批次执行。
关闭状态按 Alert 冻结的 `EndOperation.Source` 区分：`auto_policy` 为 `auto_closed`、`self_heal` 为 `autoexecute_closed`、`work_order` 为 `autoorder_closed`。手动关闭仍为 `closed`，系统关闭仍为 `system_closed`；无主动关闭命令的来源关闭，以及来源等级升级结束旧告警（`EndType=severity_upgrade`），均为 `source_closed`，恢复保持 `restored`。此映射同时用于新建及后续更新；流转日志保留等级升级的具体原因。
在通道内重新读取最新事实，直接 Bulk `refresh=true`，成功后返回；后续普通批次同样重新读取，避免旧队列消息写回旧状态。
2026-10-29 本地批量关闭实现通过 `ProjectBatchNow` 按固定通道顺序取得本批涉及的通道，避免批次交叉等待；
现存目标只批读关闭状态，Bulk 仅更新 `status/action/source_alarm_status/close_reason/close_time` 五个字段，
不读取或回写 Enrich、正文、等级和既有处置数据。KAC 文档不存在时，仅为缺失项补读完整快照创建；
写入器仍按现有字节预算切片。缺失目标、逐项失败保持输入位置，
不为每条关闭单独执行投影；取消时释放已取得的通道。该路径尚未部署验证。
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

告警 ID 切换前暂停 Linkd 投影和动作投递，等待在途请求与旧 KAC Celery 动作完成。
现有 KAC 引用字段足够，无需扩容；升级全部 Web、Worker、管理入口及 Linkd，再恢复投影和动作。
V3 HTTP 字段不变；新 Celery 消息与文档均使用原生 ID，不兼容旧 UUID 消息。
不需要为了发现旧文档刷新索引。旧 KAC 手动关闭只修改本地记录，不向 Linkd 发命令。
新 ID 写入后不直接降级到旧 UUID 写入器。不迁移历史数据。
