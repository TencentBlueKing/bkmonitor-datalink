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
Linkd 原始 labels 和 enrich 内部保留 `strategy_id/strategy_version`，KAC 查询兼容可信旧短名。

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
指标查询迁移使用用户确认的不可变 `alarm_strategy_revision`，历史接入缺口由另一项修复处理；
KAC 经 `kingeye.base.strategy.get_strategy_revision` 按上述两字段读取定义，不能以 split 当前态替代历史。
当前四参数接口临时传告警租户和告警业务 ID，后续接口移除两项时再精简；不新增管理业务关联字段。
当前 Reader 的附加过滤仍须验证实际命中，缺失不试查其他业务或 current。Linkd 保留真实执行引用、
有类型的事件维度、时间及业务，不为本次迁移另存完整查询定义。KAC 适配历史查询、事件范围和产品
取数入口，只在内存生成最终参数；历史/外部已有参数沿原协议承接，无 ES 回填。
新 `built_in_bk` 事件的 Metric Processor 不再生成参数；非内置兼容输出、已有 enrich 历史和匹配/检索字段保留。
KAC 先检查告警自身的旧参数：已有合法非空 `metric_query_params/metric_group_params` 时直接沿原插件取数，
不调用 `metric-facts` 或历史 Reader，即使同时存在 `strategy_context` 也不强制切换。
空对象/数组及其字符串占位不算有效旧参数；没有旧参数且具有可信新内置引用时才进入历史组装路径。
历史版本可读性验收针对该新路径，不能把已有旧参数告警的 Reader 未命中视为取数回退或 ES 回填要求。

该迁移成立的完整输入是同版历史定义、准确 opening/恢复事件事实与本次查询选项。Linkd 继续保留
Alert/Event、来源、租户及等级/动作关联，执行 ID/version、有类型维度、事件业务和时间锚点；
多个历史 item 时须能由可信事实唯一定位。KAC 缺少事实时只补有界只读响应，不要求新增顶层字段
或复制查询定义。既有检索/权限/匹配/模板字段不随参数生成停止而删除。
KAC 共用查询层承接组装，独立取数和内置来源入口均需接入；KMC 公共取数面还须承接结构化计划的
实际执行，保留旧扁平协议。当前旧入口拒绝非空 expression，不直接消费 `query_configs`，不能以
透传参数代替执行验证。历史物理定位、聚合周期、维度及过滤、函数和多项表达式都需验证后再切换；
窗口当前已经由 KAC 选择。历史导航上下文的存在不强制切换查询，合法旧参数与外部自定义分组继续
承接；已明确选择新路径后的读取/权限/执行错误不得用旧参数掩盖。权威改造范围和验收在 Kingeye
`docs/specs/kac-linkd-integration.md` 第 8.6 节维护；本地各查询类型实现不表示所有扩展或线上联调已验收。

`strategy_name` 和 `data_source` 继续由 Linkd 生成或透传并投影，KAC 消费已保存结果；
两者不再属于后续展示迁移项，职责依据见 [strategy 输出](../../design/enrich.md#61-strategy)。

KAC 共用详情组装层按以上事实选择路由，读取当前 `KINGEYE_WEB_SAAS_MODULE_URL`，
并处理观测融合参数。Linkd 不含前端路由常量，不查询产品页面开关。
新告警不依赖反解析 URL，恢复详情使用引用的恢复 Event 事实。
恢复业务字段投影同样生成 `strategy_context`，只使用该恢复 Event 已冻结并生效的丰富结果；
不借用 opening Alert 的策略版本，不重跑丰富，也不为新内置恢复事件补造旧查询参数。

历史 Alert 丰富结果中的 `url` 继续解码、重放；来源自定义 `field_extra_info` 覆盖规则保留。
KAC 对历史内置告警只解析已知管理/实例路由，保留 ID 并换用当前基础地址；
外部自定义链接原样承接。必要事实缺失时展示名称。详情转换不写 ES、不查询最新策略，
既有投影更新保留 `field_extra_info` 的约定不变，因此无需批量修复历史告警。
`strategy` Processor 不读取或校验 `web_saas_module_url`，该键是否存在及其取值均不影响关联事实输出；新来源发布不再带此配置。

部署先升级支持新旧两种输入的 KAC，再升级 Linkd；本契约和本地回归不代表线上已部署或完成页面验收。

## 内置指标查询事件事实（2026-10-16）

内部 JWT 保护的 `POST /api/v1/alerts/{id}/metric-facts` 复用已有事实 Reader 与有界并发，
不查询策略或重跑 enrich。请求包含 `bk_tenant_id`、`severity`；缺省读取 Alert 的
`TriggerEventID`，不读取 `LatestEventID`。可选 `event_id` 仅接受同告警已关联、同等级且
动作是恢复/来源关闭的 Event；KAC 从已保存流转引用选择该值，不接受浏览器直接指定。

返回 `bk_tenant_id/alert_id/event_id/event_source_id/severity`、`strategy_id/strategy_version`、
`bk_biz_id`、有类型 `dimensions`、`occurred_at`，若原事件 labels 含合法 `item_id` 则一并返回。
只接受 `built_in_bk`，准确核对租户和 Event/Alert 关系；不返回正文、原始 payload 或完整查询定义。
请求上限2 KiB、响应上限1 MiB、执行预算5秒，与已有丰富事实读取共享两个并发名额。
非法请求400、关联缺失404、执行引用无效422、繁忙429、依赖未配置503，响应预算/编码/取消失败502。
现有数据保留期不自动延长；已有有效旧参数的请求在调用本接口前即选择旧路径。
无旧参数并已进入新查询路径后，无法定位准确事件事实或历史定义则明确不可用，不改用当前策略兜底。

KAC 公共历史读取仍临时传告警租户/业务，结构化计划只在本次调用内存生成；KMC 使用独立公共入口
执行原生时序、物理表日志/事件、多项算术和已承接函数，保留日志/事件字段与维度适配。
普通时序的 `query_string: "*"` 可承接；日志关键词与行计数按物理表协议执行。
旧告警入口并未执行无物理表的原始 PromQL 或仅索引集日志；新入口复用公共 PromQL 原语
及索引集查询补齐。PromQL 原文不改写，结果按准确事件标签匹配；日志统一公共执行器，
保留毫秒/日志条件词表、冻结物理定位和索引集脱敏。作用域/时间字段冲突、截断明确失败；
图表 PromQL 与外部自定义入口保持原协议。未支持扩展仍明确报错。
已有合法 `metric_query_params/metric_group_params` 继续走旧插件协议（空 `{}`/`[]` 是占位符），
不因字段键同名而进入新执行入口，不改写历史 Alert/Event、KAC ES 或 Release。
