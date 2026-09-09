# Linkd 术语

权威字段与状态矩阵见 [`define.md`](../design/define.md)。

Linkd Console 是独立构建的运行与管理控制台，代码位于 `console/`；通过正式控制面 API 管理 EventSource，
对实体存储与基础设施执行只读查询。它不参与消息消费、确认或生命周期处理。

| 术语            | 定义                                                                                              |
| --------------- | ------------------------------------------------------------------------------------------------- |
| RawEventMessage | MQ 无关的接入信封，保存稳定 record ID、租户、来源、接收时间和原始 payload                         |
| lane            | 消息队列的确认与所有权分片；Kafka 中对应 topic partition，不表达 fingerprint 业务串行范围         |
| Mailbox         | 按租户、来源和 fingerprint 标识的 Redis 待处理 Event ID 队列                                      |
| Signal          | 只表示某个 Mailbox 需要处理的 Redis Stream 唤醒消息，不绑定单个 Event                             |
| SourceCleaner   | 由 EventSource 选择的来源解析器，只把 payload 中的来源事实解析为 EventDraft                       |
| EventDraft      | Cleaner 提取的来源事实，不含 EventFactory 独占的身份、标准化结果和原始快照                         |
| Event           | 标准化后的不可变来源事实；生命周期只允许补写 `related_alert_id`                                   |
| source_raw_data | Event 保存的完整来源 payload 的 JSON 对象快照，创建后不可修改，定位为人工追溯资料；本次丰富迁移尽量不读取它，不将其作为默认输入或缺字段兜底 |
| extra_data      | Event 中不进入核心字段的来源扩展数据，创建时确定并保持不可变；不承担丰富结果或处理工作区的职责      |
| EventProcessing | 独立于 Event JSON 的技术元数据，包含 state、outcome、reason 和 processed_at                       |
| EventSource     | 配置中的事件源定义，包含租户覆盖、Cleaner、fingerprint、Severity 和 MQ subscription               |
| fingerprint     | EventSource 按稳定 Event 字段生成的 Alert 关联键；Lifecycle 唯一关联条件为租户、来源、fingerprint |
| Severity        | 全局有序等级表；priority 越小越严重，Event/Alert 只保存 name                                      |
| 内容文案算法等级映射 | 丰富侧声明的策略内容文案算法等级到 Linkd Severity 名称的对应关系，用于选择本次告警的文案加工算法；默认 1→critical、2→warning、3→info，不由排序 priority 推断；智能算法专用图表增强不在本次范围 |
| Alert           | 从首个 triggered Event 创建的一次异常生命周期；继承字段创建后永久锁定                             |
| 告警丰富        | 依据告警特征及依赖数据生成补充信息，结果保存在 Alert.enrich，不覆盖 Event 来源事实或 Alert 核心字段 |
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
| OneModel 实例存储 | Kingeye 当前 OneModel 查询能力所使用的统一实例数据来源；本次 K8s 实例定位、集群名称及 Namespace 业务归属均对齐该来源，当前接入选择 Elasticsearch；该能力本身不等同于固定后端或已存在的跨进程接口 |
| strategy（丰富分组） | 丰富结果中的策略补充信息，包含 bk_strategy_id、monitor_template_id、strategy_config_id 三种独立身份，以及展示名称、跳转链接和鲸眼配置数据源；monitor_template_id 沿用旧 clean_strategy_id 的模板名称/策略名称回退行为 |
| source（丰富分组） | 丰富结果中的来源补充信息，保存 Alert.EventSourceID 对应的 source_id、从 alarm_collect_alarmsource 查询的 source_name，以及承载来源事件标识的 meta_info；不替代 Linkd 的 EventSourceID |
| meta_info（丰富字段） | 迁移后承载 Event.SourceEventID 中的来源事件标识，保存到 enrich.source.meta_info；旧实现使用内部转换对象 ID，本次已确认调整其取值来源 |
| metric（丰富分组） | 丰富结果中的指标补充信息，包含监控项展示名称、按原分类解释的指标名称、单位及本次告警观测数据的查询参数；指标名称不统一定义为指标 ID，多个丰富分类共用该分组 |
| 监控平台策略 | 监控平台管理和执行的策略；本次输入中的 bk_strategy_id 与 bk_strategy_history_id 指向它；鲸眼 StrategyConfig 使用独立的 UID 与配置语义 |
| 鲸眼策略配置 | 鲸眼侧与监控平台策略关联的配置；读取时先查 StrategyConfig，0 条时再查 CloudStrategyConfig，各自多匹配沿用底层结果第一条；监控平台历史记录 ID 不参与其版本选择 |
| 监控平台策略历史引用 | Event.Labels 中 bk_strategy_id 与 bk_strategy_history_id 组成的引用；两者均为正整数且必须同时存在；前者标识平台策略，后者对应 alarm_strategy_history.id，也不用于选择鲸眼 StrategyConfig 的历史版本；引用在租户和平台作用域内解释 |
| 监控平台策略快照 | 由 bk_strategy_history_id 对应历史记录 id、bk_strategy_id 对应 strategy_id 联合定位，并校验 content.bk_biz_id 与来源业务一致后取得的 alarm_strategy_history.content；与关联鲸眼 StrategyConfig 分别持有 |
| 指标查询参数 | 丰富结果中用于查询本次告警对应观测数据的参数；由明确来源的策略字段与 Event.Dimensions 构造，保留必要过滤条件和聚合语义；两份策略的读取条件与各字段取值来源分别定义，不承诺复刻完整下发配置 |
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
| 动态分组（dynamic_group_id） | 按旧模型与实例关系查询得到的分组归属，丰富结果保留旧字段名并保存查询时的分组 ID 列表；字段名为单数不表示单个分组，不表达分组成员的实时状态 |
| active          | Alert 当前仍成立                                                                                  |
| recovered       | 来源 resolved Event 使 Alert 进入的终态                                                           |
| closed          | 来源关闭、直接关闭或等级升级使 Alert 进入的终态                                                   |
| AlertLog        | 独立、确定性标识的不可变流水，记录状态操作、抑制和最终输出结果                                    |
| VersionToken    | Repository 专属 CAS 令牌，不进入领域 JSON 或外部消息                                              |
| accepted        | Event 已被生命周期接受并关联 Alert                                                                |
| suppressed      | 低等级 triggered Event 被 active 高等级 Alert 抑制，Event 不关联 Alert                            |
| orphaned        | resolved/closed 未找到 active Alert，Event 不关联 Alert                                           |
| rejected        | Event 在清洗或领域校验阶段被确定性拒绝                                                            |
| cause           | FinalHook 变更原因，包含 source event、user operation 或 system operation 的类型和稳定 ID         |

## 动态配置候选术语

来源管理术语分别用于[EventSource 动态配置](../design/event-source-dynamic-configuration.md)和
[部分配置动态化](../design/dynamic-configuration.md)，来源管理已接入实现，全局配置动态化仍为方案。两项独立管理版本与发布，
不定义跨来源与全局配置的统一 ConfigRelease。

| 术语 | 候选定义 |
| --- | --- |
| EventSourceRecord | 带管理作用域、资源版本与管理者的来源定义，仅属于 EventSource 项目 |
| EventSourceRelease | 单来源一次发布的完整不可变配置快照，不包含全局配置；与 Record 兼容 ES/MySQL 单对象操作 |
| SeverityPolicy | 部分配置动态化中的全局等级定义与默认值，独立于来源版本管理 |
| event_source_version | Event 生成/Alert 创建时实际使用的来源 Release 版本，不是执行代次 |
| desired / applied revision | 期望发布版本与运行时实际应用版本，保存成功不等于生效成功 |

## 中心调度候选术语

以下术语用于[中心化任务调度协议](../design/task-scheduling-protocol.md)。当前容灾边界为有界自停模型。

| 术语 | 候选定义 |
| --- | --- |
| TaskKey | 带 deployment、管理/租户作用域、模块与稳定资源/分片身份的互斥执行单元 |
| assignment_epoch | 同一 TaskKey 的执行代次，每次重新分配递增，不等于来源发布版本 |
| session_id | 本次 worker 进程启动的会话身份，重启不可复用 |
| StoppedConfirmed | 中心原子确认旧任务已停止，允许后续分配 |
| ForcedStopped | 授权到期及安全余量后完成必要隔离的强切决议，不伪造为 worker 的停止报告 |
| FastRestart | 满足模块停止契约后快速完成停止、确认和新分配，不跳过互斥交接 |

## 来源输出插件

- **具名 hook**：`EventSource.hooks` 中按顺序执行的插件实例，`name` 是该来源内唯一的稳定实例身份，`type` 是内置注册名，`config` 是该插件的参数。当前内置 `kafka` 与 `active-alert-by-strategy`。
- **活跃告警策略索引**：由 `active-alert-by-strategy` 将 Alert 当前状态投影到 Redis set，按租户和 `labels.strategy_id` 分组，成员为 fingerprint。它是尽力更新的输出索引，不是 Alert 权威状态。
