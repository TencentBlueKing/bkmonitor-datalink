# Linkd 术语

本文下方既有术语描述当前实现。新增策略能力及 Event Enrich 的计划术语见文末
[计划引入的术语](#计划引入的术语)，不能据此认定代码已经支持。

自定义丰富使用 **Enrich Patch（丰富补丁）** 记录 Event 可丰富字段的已求值赋值；来源事实不改写。Alert 复制 opening Event 的选中等级快照。
**有效告警视图** 是原始 Alert 按 Processor 顺序应用补丁后的临时读取结果，不单独持久化。
**丰富规则** 属于一个 Processor；**操作** 是规则内按顺序执行的提取、替换或赋值。详见
[自定义丰富规格](../design/custom-enrichment.md)。

权威字段与状态矩阵见 [`define.md`](../design/define.md)。

Linkd Console 是独立构建的运行与管理控制台，代码位于 `console/`；通过正式控制面 API 管理 EventSource，
对实体存储与基础设施执行只读查询。它不参与消息消费、确认或生命周期处理。

| 术语            | 定义                                                                                              |
| --------------- | ------------------------------------------------------------------------------------------------- |
| Internal-Token | Kingeye 兼容的内部 HTTP JWT 认证头，认证调用身份；不替代租户授权或业务幂等，详见 [协议](contracts/internal-token.md) |
| RawEventMessage | MQ 无关的接入信封，保存稳定 record ID、租户、来源、接收时间和原始 payload                         |
| lane            | 消息队列的确认与所有权分片；Kafka 中对应 topic partition，不表达 fingerprint 业务串行范围         |
| Mailbox         | 按租户、来源和 fingerprint 标识的 Redis 待处理 Event ID 队列                                      |
| Signal          | 只表示某个 Mailbox 需要处理的 Redis Stream 唤醒消息，不绑定单个 Event                             |
| SourceCleaner   | 由 EventSource 选择的来源解析器，只把 payload 中的来源事实解析为 EventDraft                       |
| EventDraft      | Cleaner 提取的来源事实，不含 EventFactory 独占的身份、标准化结果和原始快照                         |
| Event           | 标准化后的不可变来源事实；生命周期只允许补写 `related_alert_ids`                                   |
| source_raw_data | Event 保存的完整来源 payload 的 JSON 对象快照，创建后不可修改，定位为人工追溯资料；本次丰富迁移尽量不读取它，不将其作为默认输入或缺字段兜底 |
| extra_data      | Event 中不进入核心字段的来源扩展数据，创建时确定并保持不可变；不承担丰富结果或处理工作区的职责      |
| EventProcessing | 独立于 Event JSON 的技术元数据，包含 state、outcome、reason 和 processed_at                       |
| EventSource     | 配置中的事件源定义，包含租户覆盖、Cleaner、fingerprint、Severity 和 MQ subscription               |
| fingerprint     | EventSource 按稳定 Event 字段生成的 Alert 关联键；Lifecycle 唯一关联条件为租户、来源、fingerprint |
| Severity        | 全局有序等级表；priority 越小越严重，Event/Alert 只保存 name                                      |
| 内容文案算法等级映射 | 丰富侧声明的策略内容文案算法等级到 Linkd Severity 名称的对应关系，用于选择本次告警的文案加工算法；默认 1→critical、2→warning、3→info，不由排序 priority 推断；智能算法专用图表增强不在本次范围 |
| Alert           | 从首个 triggered Event 创建的一次异常生命周期；继承字段创建后永久锁定                             |
| test（丰富处理器） | 通过进程内 datasource 模拟随机延迟、调用超时和概率故障的负载处理器；成功后写入固定 JSON 字段，调用进入统一数据源指标 |
| 告警丰富        | 依据告警特征及依赖数据生成补充信息，结果按 evaluation 保存在 Event.enrich；Alert 复制 opening Event 结果，不覆盖来源事实 |
| 丰富分类        | 用于选择一个告警丰富主处理路径的分类，各分类可有内部特殊分支并共用依赖数据；不等同于 EventSource、告警等级或生命周期状态 |
| partial（丰富状态） | 告警丰富局部失败，但保留了已获得的有效补充信息；不阻断 Alert 创建，也不表示后续会自动补齐       |
| 丰富诊断        | 随丰富结果保存的结构化失败说明；code 表达失败原因，groups 表达实际受影响的输出分组，两者不直接决定整体丰富状态，不承担重试或补丰富职责 |
| display         | 丰富结果中的展示分组，保存加工后的告警名称、内容、对象展示文本、结构化维度展示及其摘要，不替代 Event/Alert 的来源事实 |
| 维度展示条目 | enrich.display.dimensions 中按旧分类规则生成的单项展示信息，保留 name、value，以及可独立缺省的 key、real_key、real_value；缺少维度原始信息时不以展示内容补造 |
| 对象展示文本 | 按旧分类规则使用实例展示名、地址或节点等信息加工得到的文本；不作为模型实例标识或 Event/Alert 的主体身份 |
| 日志关联信息 | 来源提供的日志补充内容；通过 Event.ExtraData.log_related_info 输入，沿用旧日志关联内容的加工和输出语义，不作为对象定位或指标观测维度 |
| log（丰富分组） | 丰富结果中的日志专用信息，包含日志主题、检索语句和关联信息；日志指标与日志关键字共用适用字段，加工后的告警文案仍属于展示分组 |
| apm（丰富分组） | 丰富结果中的 APM 专用信息，包含应用标识、名称、别名，以及服务、实例、接口和对端名称；派生模型与实例标识仍属于资源分组 |
| k8s（丰富分组） | 丰富结果中的 K8s 专用信息，包含集群、命名空间、服务、工作负载、Pod、容器和节点上下文；集群标识保留 bcs_cluster_id 命名，资源所属业务与派生模型实例标识仍属于资源分组 |
| 公共资源配置 | Linkd 顶层 `resources` 中的第三方连接与凭据；丰富/调试使用只读连接，可靠投递使用独立租户凭据；启动时加载，不进入 EventSource 发布快照 |
| OneModel 实例存储 | Kingeye 当前统一实例来源；可选 Doris 通用实例/关系表；Elasticsearch 逻辑入口为 `kingeye_all_instance` alias，实例根身份为 `bk_tenant_id/model_id/model_inst_id/entity_uid`，来源原始属性位于 `attributes`，可检索动态属性位于 nested `attribute_values` |
| strategy（丰富分组） | 丰富结果中的策略补充信息，包含 bk_strategy_id、monitor_template_id、strategy_config_id 三种独立身份，以及展示名称、跳转链接和鲸眼配置数据源；monitor_template_id 沿用旧 clean_strategy_id 的模板名称/策略名称回退行为 |
| source（丰富分组） | 丰富结果中的来源补充信息：按租户和 Event.EventSourceID（KAC 的 linkd_source_id）回查 alarm_collect_alarmsource，source_id 保存该表的 id，source_name 保存名称，meta_info 承载来源事件标识；不替代 Linkd 的 EventSourceID |
| meta_info（丰富字段） | 迁移后承载 Event.SourceEventID 中的来源事件标识，保存到 enrich.source.meta_info；旧实现使用内部转换对象 ID，本次已确认调整其取值来源 |
| metric（丰富分组） | 丰富结果中的指标补充信息，包含监控项展示名称、按原分类解释的指标名称、单位及本次告警观测数据的查询参数；指标名称不统一定义为指标 ID，多个丰富分类共用该分组 |
| 来源策略身份 | Event.Labels 中 `strategy_id` 与 `strategy_version`；新发布链中前者为 SplitRecord 主键，后者为 `source_resource_version`。两个字段均参与运行时读取校验，不能用旧策略 ID 或 Config status 版本替代。 |
| 鲸眼策略配置 | Linkd 从 Kingeye MySQL `alarm_strategy_set_split_record` 的同版本发布材料构造的只读丰富视图；分类、展示和指标查询复用 `resolved_strategies[*].spec`，不读取旧配置表。 |
| StrategySetSplitRecord（策略拆分发布当前态） | Kingeye 表 `alarm_strategy_set_split_record`；默认与覆盖分别使用自身主键作为运行时策略 ID，覆盖通过 `parent_id` 关联默认记录。payload 保存编译后的配置与检测材料；它是当前态，不保证历史版本保留。 |
| StrategySet（声明式策略集合） | Kingeye 的上游声明输入；Linkd 使用其已编译到 SplitRecord 的材料，不直接查询当前 Set 表。 |
| StrategyConfig（声明式策略配置） | 旧声明式配置资源及其写入支路；Linkd 不再读取 `core_v1alpha1_strategy`，也不以该支路写入成功作为策略可读条件。 |
| 全局业务 | `metadata_space` 中租户、`space_type_id = bkcc` 和业务 ID 对应且 `is_global = 1` 的业务空间；该业务下的策略可匹配同租户任意来源业务 |
| 鲸眼声明式策略查询投影 | SplitRecord 的 `resolved_strategies[*].spec.strategy_item` 中的聚合、表达式和 `query_configs`；供丰富处理器在单 Event 内共享。 |
| 指标查询参数 | 丰富结果中用于查询本次告警对应观测数据的参数；当前由鲸眼声明式策略 `spec.strategy_item.query_configs` 与 Event.Dimensions 构造，不回查蓝鲸策略当前表或历史表 |
| 维度条件文本（where_condition） | 按旧过滤和拼接规则从工作维度生成的条件文本，保存到 enrich.metric.where_condition；生成过程不修改 Event 的来源维度 |
| 首次异常点时间（anomaly_begin_time） | 上游提供的本次告警对应首次异常点时间，从 Event.ExtraData.anomaly_begin_time 读取；仅接受字符串并原样保存到 enrich.metric.anomaly_begin_time，空字符串也保留，缺失时省略 |
| 来源业务 | Event.Labels.bk_biz_id 表示的业务上下文；`built_in_bk` 来源要求该标签为数字 Scalar 正整数，替代旧回调 F05–F07 的业务输入，用于对应的来源业务和查询消费位置 |
| 业务观测维度 | Event.Dimensions.bk_biz_id 表示的指标观测值，仅在指标需要该维度时参与查询过滤或分组，不替代来源业务 |
| 资源所属业务 | 从拨测任务、CMDB 等依赖取得的资源业务归属，供资源展示和拓扑逻辑使用并保存到 enrich.resource，不覆盖来源业务标签 |
| CMDB 模型实例 ID（bk_inst_id） | 指定 CMDB 对象模型下的实例标识；本次丰富的来源输入位于 Event.Dimensions.bk_inst_id，必须结合模型解释，不是跨模型通用身份 |
| 主机 ID（bk_host_id） | 主机对象模型 HOST_OBJECT_MODEL_CODE 的实例标识；本次丰富从 Event.Dimensions.bk_host_id 读取，保留主机模型的专用语义 |
| 目标主机 ID（bk_target_host_id） | 来源提供的目标主机标识，位于 Event.Dimensions.bk_target_host_id；可能对应远程采集下发的主机，不能预先等同于告警对象的 bk_inst_id 或 bk_host_id；具体来源语义仍按场景取证 |
| resource（丰富分组） | 丰富结果中的资源上下文，分别保留 CMDB 与 Meta 的模型实例信息，包含业务、CMDB 业务集群、模块和云区域的标识与名称，以及动态分组和派生范围标签，不改写 Event/Alert 的核心主体或标签 |
| cw_labels       | 旧告警规则生成的业务或资源范围字符串标签列表；迁移后作为资源丰富信息保存，不等同于 Event/Alert.labels 或授权结果 |
| 动态分组（dynamic_group_id） | 按租户和 canonical `model_id`、`model_inst_id` 查询 Kingeye 预写入的 Redis 关系投影；丰富结果保留旧字段名，将整数分组 ID 保存为十进制字符串数组。字段名为单数不表示单个分组，也不表达分组成员的实时状态 |
| active          | Alert 当前仍成立                                                                                  |
| values | Event 本次观测的有限数字对象，不参与 fingerprint，不包含单位等元信息 |
| evaluations | Event 中按标准 severity 唯一的判定列表，动作是 triggered/resolved/closed；顺序无语义 |
| severity_upgrade_policy | 全局升级策略：update_current 保留 Alert 身份更新级别；close_and_create 关闭旧 Alert 后新建 |
| EventPlan | EventProcessing 内先于副作用保存的裁决计划，冻结升级策略、Alert 目标快照和逐级结果；完成后删除 |
| related_alert_ids | Event 最终关联的有界 Alert ID 列表，最多包含旧、新两条 Alert |
| recovered       | 与活动 Alert 同级的 resolved 判定使其进入的终态                                                           |
| closed          | 同级来源关闭、直接关闭或 close_and_create 等级升级使 Alert 进入的终态                                                   |
| AlertLog        | 独立、确定性标识的不可变流水，记录状态操作、抑制和最终输出结果                                    |
| VersionToken    | Repository 专属 CAS 令牌；可进入内部处理计划以恢复原版本 CAS，不进入领域 Event/Alert JSON 或外部消息                                              |
| accepted        | 事件级表示至少产生一项 Alert 变更；判定级表示该级别被接受并关联 Alert                                                                |
| suppressed      | 判定级表示低等级触发被抑制或旧级别终结被升级替代；事件级表示无 Alert 变更但存在抑制，关联实施抑制的 Alert                            |
| orphaned        | 判定级表示 resolved/closed 未匹配同级活动 Alert；整个事件 orphaned 时关联列表为空                                           |
| rejected        | Event 被确定性拒绝；Lifecycle 遇到未知标准等级时持久化该终态并跳过                                                            |
| cause           | FinalHook 变更原因，包含 source event、user operation 或 system operation 的类型和稳定 ID         |

## 动态配置术语

来源管理术语分别用于[EventSource 动态配置](../design/event-source-dynamic-configuration.md)和
[部分配置动态化](../design/dynamic-configuration.md)，来源管理与选定等级配置同步均已接入实现。两项独立管理，
不定义跨来源与全局配置的统一 ConfigRelease。

| 术语 | 定义 |
| --- | --- |
| EventSourceRecord | 带管理作用域、资源版本与管理者的来源定义，仅属于 EventSource 项目 |
| EventSourceRelease | 单来源一次发布的完整不可变配置快照，不包含全局配置；与 Record 兼容 ES/MySQL 单对象操作 |
| SeverityPolicy | deployment 内当前等级定义与默认值，支持动态来源同步，独立于来源版本管理 |
| event_source_version | Event 生成/Alert 创建时实际使用的来源 Release 版本，不是执行代次 |
| desired / applied revision | 期望发布版本与运行时实际应用版本，保存成功不等于生效成功 |

## 中心调度候选术语

以下术语用于[中心化任务调度协议](../design/task-scheduling-protocol.md)。当前容灾边界为有界自停模型。

| 术语 | 定义 |
| --- | --- |
| TaskKey | 带 deployment、管理/租户作用域、模块与稳定资源/分片身份的互斥执行单元 |
| assignment_epoch | 同一 TaskKey 的执行代次，每次重新分配递增，不等于来源发布版本 |
| session_id | 本次 worker 进程启动的会话身份，重启不可复用 |
| StoppedConfirmed | 中心原子确认旧任务已停止，允许后续分配 |
| ForcedStopped | 授权到期及安全余量后完成必要隔离的强切决议，不伪造为 worker 的停止报告 |
| FastRestart | 满足模块停止契约后快速完成停止、确认和新分配，不跳过互斥交接 |

## 来源输出插件

- **具名 hook**：`EventSource.hooks` 中按顺序执行的插件实例，`name` 是该来源内唯一的稳定实例身份，`type` 是内置注册名，`config` 是该插件的参数。当前内置 `kafka` 与 `active-alert-by-strategy`。
- **活跃告警策略索引**：由控制面统一将已落库 Active Alert 的 fingerprint 并集投影到 Redis Set，按租户和 `labels.strategy_id` 分组；Hook 仅提交刷新提示，周期校准恢复遗漏。它是可重建、允许传播延迟的查询缓存，不是 Alert 权威状态。
- **策略索引变更通知**：集合成员实际新增或移除时，自动向 `<key_prefix>:changes` 发布的 Redis Pub/Sub 提示，只携带 `bk_tenant_id` 和 `strategy_id`；消费者收到提示后重新读取策略索引集合，不作为心跳或历史事件日志。

动态配置的最后有效快照用于上游异常和进程重启恢复，不是事件历史配置版本。

## 计划引入的术语

以下术语用于[Event 丰富与三类策略开发方案](../design/event-enrich-and-alarm-policies.md)，尚未对应完整实现。
Event 丰富模型、冻结 CAS、逐等级主流程和预览已落地；策略配置发布和公共条件编译已实现，三类策略运行时仍在开发。

| 术语 | 计划含义 |
| --- | --- |
| PolicyRecord | 当前策略已发布版本及待发布快照；租户与策略类型共同隔离 |
| PolicyRelease | 单个策略版本的不可变配置与编译摘要，不按 EventSource 拆分 |
| AlertAdmission | 最近一次实际放行的等级、时间与推动原因；同级重复不再次处置，被屏蔽升级不会借用旧等级的放行记录 |
| AlertMerge | 与生命周期独立的合并角色、等待窗口及关系摘要；未参与时为 nil，原始成员等待或已合并时阻止处置，合并主在关系就绪后才可放行 |
| MergeOrigin | 内置合并 Event 的只读裁决来源，保存操作/策略/窗口、成员摘要及冻结策略标签；只允许 builtin_alarm_merge 来源，外部 Cleaner 无权生成 |
| 合并执行记录（merge.Decision） | Redis 冻结后持久化的业务裁决与单调阶段；成员快照分行保存，父 Event 先冻结再投递，计算出的父 ID 不等于真实父 |
| 合并控制点（merge.ControlPoint） | 精确持久裁决/关系的版本摘要；token 绑定真实存储版本，区分一次操作完成与整个业务任务完成，不复制成员或父 Event 内容 |
| 合并显式请求（merge.RetryRequest） | 固定原控制点版本、操作者及原因的持久命令；在正式窗口租约中接续裁决或检查关系，不重置冻结业务结果；结果不确定时沿原操作身份重试 |
| MergeWait | Alert 持久化的首次入窗引用，包含冻结策略、首次 Event/等级、条件组及原截止时间；Redis 窗口丢失也不能延长它 |
| AlertShield | 独立屏蔽状态及有界绑定、复查时间；终态清空，解除本身不生成处置资格 |
| 屏蔽复查记录（ShieldCheck） | 独立于 Alert 的最近检查事实，区分保留、变更、部分检查失败和请求版本失效；不推进业务 revision，不替代当前绑定 |
| 屏蔽复查请求（ShieldCheckRequest） | 带稳定 operation_id、业务版本、操作者和原因的持久化检查命令；复用正常复查逻辑，不强制解除、不补发处置 |

| AlertMergeChange | 合并释放、建联、父就绪、解除或父恢复与 Alert CAS 同时保存的待完成输出/流水意图；固定前后状态、窗口/关系、稳定操作身份和处置资格，完成前不允许后续业务覆盖 |
| MergeWorkCursor | 控制面内部的租户/Alert/窗口三级游标；每页按窗口或待完成意图计数，避免同一 Alert 的多个等待相互饿死 |
| AlertPolicyChange | 控制面屏蔽状态 CAS 同时保存的待完成状态输出与流水意图；下一轮或新 Event 可按稳定身份补齐 |
| PolicyDecision | EventPlan 中冻结、完成后保存在 EventProcessing 的策略裁决；记录活动 Alert 绕过、逐等级计数、抑制与故障跳过，不替代 Event/Alert 状态 |
| 抑制运行快照（SuppressionWindow） | 对 Redis 当前防抖计数或聚合占位的一次只读观察，带观察时间、策略版本、代次和剩余保留期；不替代 Event 的历史裁决或 Alert 当前处置资格 |
| 抑制保留成员（SuppressionMember） | 当前 Redis 仍保存的 Event 身份与首次处理时间；聚合集合包含候选/主 Event 和已确认抑制的 Event，允许随缓存丢失或到期，不是持久化事件历史 |
| PolicyContext | EventProcessing 中已 CAS 保存的首次策略时间、发布版本引用与配置读取跳过原因；临时 Plan 撤销或完成后仍保留 |
| PolicyOperation | 策略同步操作的持久化摘要、目标版本和首次时间锚点，供幂等发布重试 |
| Event Enrich | 在事件策略裁决前丰富 Event，保存结果和诊断；不覆盖来源事实 |
| 有效事件视图 | Event 来源字段按有序丰富补丁计算出的读取视图，供条件匹配和后续投影使用 |
| 事件抑制 | 阻止本次触发 Event 形成新的有效触发动作；结果属于 Event，不是 Alert 的生命周期状态 |
| 策略状态模拟 | 在请求私有内存中，以多条已丰富 Event 和虚拟时间执行单个策略的计数、生命周期及合并窗口裁决；不读取生产运行状态，不创建实际父告警或执行输出 |
| 策略执行观察 | 按租户、策略类型、策略 ID 和 UTC 小时桶聚合的尽力采样；包含所有版本、重试、重查及候选匹配次数，不是唯一 Event 数或持久审计 |
| 防抖抑制（clip） | 仅对尚无活动 Alert 的同一租户、来源和 fingerprint 按首次裁决处理时间滑动计数，达阈值的当前 Event 放行；重投去重，Redis 丢失允许重新累计 |
| 关联聚合抑制（aggregation） | 仅对尚无活动 Alert 的候选，按策略配置字段将 Event 关联到主告警；未选来源字段时可以跨 EventSource |
| 终态抑制清理记录（SuppressionCleanup） | 按终态 Alert 版本或未生成 Alert 的终态 Event 固定身份，独立保存清理意图及两类确认结果；Lua 原子返回被删窗口/代次，元信息丢失显式标记；计数为登记引用数，Redis 失败不伪装为零 |
| 抑制受控对账（SuppressionCheck） | 固定窗口 ID、owner、代次与操作意图，在正式 owner fingerprint lease 内复核真实资格；保留未绑定计数和有效候选，条件删除失效登记，不重建窗口或触发处置；请求与结果独立持久化 |
| 屏蔽关系（ShieldBinding） | 告警由于有效时间或依赖条件暂不进入后续处理的关联；固定建立时的策略和主告警，独立于 Alert.status；origin=manual 为指定告警快捷绑定，定时检查不重跑普通匹配条件 |
| DependencyMain | Redis 原子登记的跨来源待处理主引用；保存 Alert 身份和冻结 Event/等级，Alert CAS 成功后才能绑定，但不代表处置已经放行 |
| 解除屏蔽 | 结束有效屏蔽关系并同步状态；本次已确认不主动触发处置，等待下一条触发 Event |
| 合并窗口（MergeWindow） | Redis 中按租户、策略版本与分组收集成员的固定半开窗口；成员以 Alert 去重，实际落库后才确认，冻结仅代表待持久化裁决 |
| 合并裁决（MergeDecision） | 基于冻结策略和成员作出的成功合并或失败释放决定；操作 ID 仅由租户与固定窗口确定，Redis 丢失后仍可直接查找，一窗不允许两个不同裁决 |
| 合并关系（MergeRelation） | 持久化的固定父子关系组，经历 preparing/ready/ending/ended；保存有界成员与建立、解除进度，不替代成员真实生命周期状态 |
| 解除合并关系 | 父告警终结后移除该关系及对应等待；保留活动子告警的 status/admission，最后一条关系解除也不补发处置，等待下一条触发 Event |
| 合并主告警指纹 | 由租户、合并策略 ID 和排序去重后的子 AlertID 集合确定；子 Event 重复上报不改变逻辑身份 |
| 合并模板（MergeTemplate） | 基于冻结成员有效视图编译和渲染的字段配置；复杂变量采用稳定 JSON，合并集合排序去重后用 ### 连接，完整结果在父 Event 入队前保存 |
| 内部合并 Event | 控制面根据裁决、模板和成员快照创建并持久化的系统事件，经 Mailbox/Lifecycle 创建主 Alert；不是外部消息的伪装 |
| 内置合并 EventSource | 保留来源 builtin_alarm_merge，具有独立 Release、Enrich 和 Hook；默认空 Enrich，不启动外部 Cleaner，也不再次参与合并 |
| 触发准入 | 本次触发是否已通过相应策略阶段；不能仅凭 Alert.status=active 推断 |
| 状态同步 Hook | 输出 Event 裁决或 Alert/关系变化，供外部查询展示使用 |
| 处置触发 Hook | 只对本次获准动作执行的输出，不因纯粹的屏蔽关系解除而触发 |
| opening Event | 实际创建 Alert 的首条有效触发 Event；防抖前置事件不一定是 opening Event；Alert 展示与丰富快照固定取自该 Event |
| KAC alarm_event 兼容层 | 一个 Linkd Alert 生命周期对应的一条稳定 KAC 兼容文档；已确认由 Linkd 直接维护原索引和告警状态，KAC 查询并修改处置字段，不按每条 Event 新建；由[全局插件](../design/kac-compatibility-plugin.md)直接维护 |
| KAC 全局兼容插件 | 内置插件，一个部署级开关统一启用 alarm_event 维护及后续处置通知，覆盖所有来源和租户，不配置逐来源/逐租户目标；数据、任务及幂等身份仍按租户隔离 |
| 投影版本与同步水位 | Alert 业务 revision 从 1 递增；每个目标独立保存 required_revision/synced_revision，同步 ACK 自身不推进业务 revision/update_at |
| ProjectionTargetState | Alert 中的有界目标引用与同步水位，最多 16 项；source_version 记录 opening 来源版本而不解析路由；全局 KAC 插件固定使用目标 kac 并启用动作，不在 Alert 存放 endpoint 或凭据 |
| ProjectionWorkCursor | 内部补扫的租户/Alert/目标游标，每页最多 16 个待确认目标；覆盖活动、终态和历史索引，不设最近时间过滤 |
| ProjectionTask | 按租户/Alert/目标/业务版本固定的持久投递任务；保存业务快照、来源发布引用、有界尝试进度及远端确认，不保存连接凭据 |
| ProjectionReceipt | Linkd 兼容存储适配器确认的文档身份、应用版本、内容摘要、生命周期和搜索可见性；单次 HTTP 200 不等于该确认 |
| ActionDelivery | 对本次已获准动作保存的独立持久任务，冻结原 Alert/原因/目标发布；按同 Alert/目标版本顺序执行，不从最新状态补造历史动作 |
| 动作入队意图（AlertActionIntent） | Alert.action_pending，与获准业务变更同次 CAS 保存的原版本、动作、原因和目标发布；全部动作任务持久且排序可见后清除。意图存在时禁止推进下一业务版本，投影 ACK 仍可更新 |
| 动作受理确认（ActionReceipt） | 接收端对动作身份和完整请求摘要的持久幂等受理或明确过期跳过；受理成功不表示通知/工单执行完成，也不用于推进投影同步水位 |
| 动作顺序屏障（unsettled） | 尚未成功受理或确认过期跳过的动作，包含 failed；较新动作不能绕过前序失败，较新终态可见后才允许旧 firing 跳过 |
| Projection RetryCommand / RetryRecord | 对既有失败任务的完整人工恢复命令及首次受理时间；固定租户、任务、CAS 版本、操作 ID、操作者和原因，只保存最近一条记录，重投不重置预算 |
| delivered | 投影任务已持久化远端确认，尚需推进本地 Alert 水位；重试只补本地 ACK |
| succeeded | 投影任务已完成远端确认和本地水位处理；不表示对应的处置动作已经执行 |
| 策略跳过 | 本次策略因依赖或协调失败未执行，记录诊断、指标和日志后继续；不冒充正常未命中，也不吞掉核心持久化或租户校验错误 |

## 屏蔽事件提示

依赖主 Alert 结束后发送的尽力而为唤醒信号，只用于加速查询和复查其当前子绑定。提示按部署和租户隔离，
不代表解除命令、不创建处置资格，也不是需要恢复的业务事实；遗漏、重复、断连或队列满由持久化定时扫描补偿。

## 蓝鲸调用配置

`BluekingConfig` 是 Linkd 部署级蓝鲸应用与 APIGW 配置。其多租户开关只控制调用用户解析：单租户
使用 admin，多租户查询当前租户的 bk_admin；请求租户仍来自现有业务上下文，不改变数据隔离。
租户管理员成功缓存由控制面/Lifecycle 运行时持有，来源发布与 Event/Alert 不保存调用凭据。

- Celery 投递确认：KAC 返回原动作身份、请求摘要及本次 task_id，只确认任务已投递；重试可能重复投递，不代表持久去重受理或处置完成。
- 结束操作（AlertEndOperation）：主动关闭时保存的操作 ID、来源及操作者；与结束原因/时间共同约束幂等重试。
