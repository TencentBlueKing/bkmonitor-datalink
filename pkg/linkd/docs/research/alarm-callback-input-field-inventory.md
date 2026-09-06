# alarm_callback 回调输入字段盘点

日期：2026-09-02。状态：源码输入盘点；用户后续映射方案见迁移计划第 5.19 节，完整输入 schema 尚未冻结。

关联：[迁移执行计划](../design/alarm-callback-enrichment-migration.md)。

## 1. 本轮约束与证据范围

用户明确要求先列出旧回调实际被读取的字段，再决定这些信息在 Linkd 中从哪里读取。
`SourceRawData` 定位为人工追溯资料，本次丰富尽量不读取它；不将其设为默认输入或字段缺失时的
自动兜底。撤回“固定从 SourceRawData 根对象读取旧回调”的建议。

下文所有路径都描述**旧回调**，不是 `Event.SourceRawData` 路径，也不是对 Event 新字段的命名建议。
本盘点保留旧代码的输入路径；用户后续已指定的 Event 字段来源和待收敛问题统一记录在
[迁移计划第 5.19 节](../design/alarm-callback-enrichment-migration.md#519-用户输入映射方案与落地评估待收敛)，
不在旧路径表中混入新路径。旧默认值、回退和失败行为仅作为取证，不自动成为 Linkd 契约。
用户进一步明确可决定上游推送数据的字段落点，因此旧路径不是新输入设计的兼容性边界；
字段按 Linkd 职责重新分配的建议见迁移计划第 5.19.5 节。
策略输入已区分为两份数据。最新决策为完整 bk_strategy_id/bk_strategy_history_id 标签引用触发监控平台策略历史内容
与鲸眼 StrategyConfig 两份读取，任一缺失按 partial 处理，见
[迁移设计第 5.23 节](../design/alarm-callback-enrichment-migration.md#523-已确认完整平台策略引用触发两份策略读取)。
输入标签已确认是 Event.Labels.bk_strategy_id 与 Event.Labels.bk_strategy_history_id，均为正整数；
已启用本套丰富的来源必须同时提供两者。缺失返回 failed 与 missing_field；字符串、布尔值、非整数、
0 或负数返回 failed 与 invalid_field。两类输入错误都在依赖查询前结束，详见迁移设计第 5.23.4–5.23.5 节。
引用合法后的策略记录缺失及超时、连接失败、权限拒绝、响应无法解析均按 partial 处理；
上述依赖问题的诊断码已统一为 dependency_invalid，暂不细分故障类型，见
[迁移设计第 5.9.1 节](../design/alarm-callback-enrichment-migration.md#591-已确认诊断原因码与受影响分组)。
bk_strategy_history_id 已进一步明确为 alarm_strategy_history.id，不是旧内容中的固定 version="v2"。
以下旧路径表保留原回调字段名，不把新标签名写回旧实现证据。
下列 S 字段保留旧消费来源；用户已确认重叠字段沿用旧来源，见迁移设计第 5.23.3 节。
第 11 节仅证明鲸眼侧覆盖能力，不构成替换旧平台读取的依据。

证据基线：

- Kingeye：`5597beae82d42b7c732bf4cc10f7cffeaaf44672`，本次核对时工作区干净。
- Linkd：`9b698332c0f994563d6800940dc6be6763933feb`；保留已有文档改动、脚本及工具。
- 跟踪 `entry → processors → converter → cleaner`，以及实际调用的图表参数构造、内容转换、
  维度转义和 K8s 身份查询函数。没有访问业务服务或运行 Python/Go 业务测试。
- 检查 `kac/tests/alarm_callback/source_data/` 的 14 个文件、14 条 `origin_alarms` 测试输入。
  它们是仓库测试夹具，不能当作新取得的生产样本或完整可达性证明。
- 分类盘点覆盖基础监控、基于数据、日志指标、日志关键字、拨测、云平台和接入对象；
  APM/K8s 为条件分支。`ACCESS_OBJECT` 处理类存在，当前分类器与样本均无可达证据，后续已确认
  从本次实现范围排除，并在本文保留源码读取意图和排除依据。

## 2. 路径与读表约定

- `R`：传入旧 `AlarmDataHandler` 的单条原始回调，尚未经过 Processor 修改。
- `I`：`R.strategy.items[0]`。多数转换只看第一个监控项；图表参数处理其全部 query_configs。
- `Q`：`I.query_configs[j]`；表中“首条”表示 `j=0`，不是把所有查询都压成一条。
- `D`：旧 `BaseProcessor.adapt_alarm_data` 生成的工作维度字典，不是原始回调字段。

`D` 的来源需要展开，不能要求上游提供 `custom_dimensions_map`：

1. 读取 `R.dimensions[]` 中含 `value` 的条目，以 `key.replace("tags.", "")` 为键保存 `value`。
   这是字符串替换；重复或替换后碰撞的键由后出现条目覆盖。
2. 非空 `R.event.bk_host_id`、`R.event.bk_topo_node` 覆盖同名维度。
3. 缺少 `bk_biz_id` 键且 `R.event.bk_biz_id` 非空时补业务维度；旧代码同时修改 tags、聚合维度和
   回调 dimensions 列表。这里记录旧写入，不允许 Linkd 丰富回写 Event。
4. 只要 `ip`、`bk_cloud_id` 键存在，就分别覆盖 `bk_target_ip`、`bk_target_cloud_id`。
5. Converter 去掉 `bk_topo_node`；BasicDataConverter 还去掉 `bk_host_id`。之后把 `D` 写回旧
   `event.dimensions`，BasicData 的实例定位再读取这份已经被替换的字典。

因此，`R.event.dimensions` 与 `R.dimensions[]` **不能直接当作两份平级输入让 Linkd 自动回退**。
按当前调用顺序，实例定位读到的主要是从外层 dimensions 加工的 `D`。证据：E02、E04、E05。

## 3. 跨分类的回调输入

以下为需要用户决定承载位置的信息。输入缺失的影响描述该信息服务于什么功能，不冻结新的
`failed/partial` 细分原因码。

| 编号 | 旧读取路径 | 信息与旧类型证据 | 用途及缺失影响 | 读取证据 |
| --- | --- | --- | --- | --- |
| F01 | `R.strategy.id` | 后端监控策略 ID；14 个样本均为整数 | 分类前查询 StrategyConfig / CloudStrategyConfig；缺失不能按现有规则查关联策略。不是模板 ID、配置 UID 或 Linkd condition_key | E01 `classify_alarm` |
| F02 | `R.event.description` | 告警内容；14 个样本均为字符串 | 数值/枚举/单位与聚合函数文案加工，日志内容裁剪；缺失不能重现旧 display.content | E04 `clean_event`；E10 `clean_content`；E11 `get_alarm_content` |
| F03 | `R.event.severity` | 来源等级；14 个样本均为整数 | 内容算法选择；旧等级还参与按等级读取首次异常点时间。迁移时等级表示如何对应需单独决定 | E04 `clean_event/algorithm_map`；E09 `clean_anomaly_begin_time` |
| F04 | `R.severity` | 外层来源等级；14 个样本均为整数 | 旧图表参数按算法 level 筛选智能检测配置；本次已排除全部智能算法专用图表增强，因此该独立读取点不再形成 Linkd 输入需求 | E12 `get_graph_panel` |
| F05 | `R.event.bk_biz_id` | 事件业务 ID；14 个样本均为整数 | 基础/日志业务上下文、业务维度补充、图表查询的业务作用域；不是租户 ID | E02 `adapt_alarm_data`；E04 `format_event_dict`；E12 |
| F06 | `R.bk_biz_id` | 外层业务 ID；3 个原始样本存在，均为整数 | DATA/日志 Processor 在非真值时用策略元数据的业务 ID 补入。DATA 的 APM 分支消费该值；日志最终基础转换另读 F05，不能合并为一个已确认优先级 | E05；E07；E08 |
| F07 | `R.strategy.bk_biz_id` | 策略快照业务 ID；14 个样本均为整数 | DATA 的 APM 分支在 F06 仍无有效值时回退读取；正常流程 F06 可能已经从外查元数据补齐 | E05 `clean_object_inst_by_dimension` |
| F08 | `R.dimensions[].key`、`.value` | 名称/值；样本值含字符串、整数及 null | 形成 D，支持分支选择、对象定位、展示、where 条件及图表过滤；需要保留任意业务维度，不仅是下文列出的内置键 | E02；E03；E04；E12 |
| F09 | `R.event.target` | 主机目标字符串，旧代码按 `IP\|云区域` 拆分；7 个样本存在 | 基础监控系统指标优先用它发起主机查询；格式不满足两段时退回 D 的 IP/云区域 | E03 `SystemMetricMixin.preprocess_alarms` |
| F10 | `R.event.bk_host_id` | 主机 ID；7 个样本存在，均为整数 | 写入 D；BaseConverter 在实例查询未给 ID 且模型为主机时回退使用 | E02；E04 `format_event_dict` |
| F11 | `R.log_related_info` | 日志关联信息；14 个样本均为字符串，存在不代表非空 | 日志关键字转换覆盖 event.related_info，输出关联信息并参与内容加工；缺失回退空字符串 | E08 `LogCollectConverter.__init__`；E10 |

`R.dimensions[].display_key/display_value` 虽然出现在样本中，当前迁移路径未读取它们来生成
最终维度展示；Converter 使用维度 key/value 与指标库、Meta 等依赖重新生成展示条目。
因此它们暂不列为必须提供的丰富输入。用户后续已将 F03/F04 合为 Event.Severity、F05–F07
合为 Labels.bk_biz_id；表中的分处读取是旧事实。算法等级的显式映射已确认，见
[迁移设计第 5.22 节](../design/alarm-callback-enrichment-migration.md#522-已确认策略算法等级显式映射到-eventseverity)。
业务 ID 按来源、观测维度与资源归属分别使用的决定见
[迁移设计第 5.24 节](../design/alarm-callback-enrichment-migration.md#524-已确认业务-id-按来源业务观测维度与资源归属分别使用)。
新输入的 Labels.bk_biz_id 已确认为必填数字 Scalar 正整数；缺失返回 missing_field + failed，
字符串、布尔值、小数、0 或负数返回 invalid_field + failed，且不查询依赖，详见
[第 5.24.1 节](../design/alarm-callback-enrichment-migration.md#5241-已确认来源业务标签使用数字-scalar-正整数)。
这是迁移后的契约，不改变上表记录的旧整数样本事实。F08 空值及其他字段类型仍见映射评估。
F11 已确认改从 Event.ExtraData["log_related_info"] 读取，仅接受 JSON 字符串。缺失沿用旧空字符串；
错误 JSON 类型按 log 分组记录 invalid_field + partial，display.content 继续基于 Event.Content 加工，见
[迁移设计第 5.25 节](../design/alarm-callback-enrichment-migration.md#525-已确认日志关联信息从-extradata-读取)。
表中的 R.log_related_info 仍是旧读取证据，不代表新输入位置。
F09 已确认改用 Event.Dimensions.bk_target_ip 与 bk_target_cloud_id 两个独立维度；
主机定位不再解析组合 target，拨测 target 保留原语义，见
[迁移设计第 5.26 节](../design/alarm-callback-enrichment-migration.md#526-已确认主机-ip-与云区域使用独立维度)。
旧路径表和 D 的构造步骤仍保留原代码证据，不作为新输入的别名回退规则。
B01/F10/B06 三种实例标识已确认保留各自语义，统一从 Event.Dimensions 读取，按
bk_inst_id → bk_host_id → bk_target_host_id 回退；适用链的三项均缺失时按 partial，见
[迁移设计第 5.27 节](../design/alarm-callback-enrichment-migration.md#527-已确认保留三种实例标识及其维度回退顺序)。
已确认仅在 HOST_OBJECT_MODEL_CODE 下使用完整三段回退；非主机保留原有实例定位逻辑，
不将后两个主机字段当作该模型的实例 ID，也不套用三项缺失的 partial。
主机三项 ID 缺失时仍继续可用的 IP/云区域查询；查得资源进入丰富结果，整体保持 partial
及来源 ID 缺失诊断，不回填 Event。查询返回多个主机候选时沿用旧逻辑使用底层结果第一条，见
[迁移设计第 5.27.1 节](../design/alarm-callback-enrichment-migration.md#5271-已确认三个-id-缺失时继续-ip-查询并保持-partial)。
地址维度的取值、缺省和转换已确认按旧分支处理，不增加统一成对必填门槛；基础分支保留
空 IP、云区域 -1 的缺省，DATA 分支缺云区域时的转换失败不解释为支持只按 IP 检索，见
[迁移设计第 5.27.2 节](../design/alarm-callback-enrichment-migration.md#5272-已确认地址查询输入沿用旧分支处理)。
旧代码的其他分支差异继续保留在下表中。

## 4. 策略快照的具体输入字段

除了 F01，旧逻辑还实际读取下面的策略快照信息。用户后续明确不必受旧输入格式约束，
并区分鲸眼配置与监控平台策略。第 5.23 节最新决定完整引用下两份均读；S01–S18 的旧来源
及鲸眼覆盖能力分别保留为取证信息，不能用后者自动替换旧来源或省略平台读取。
不能把旧字段路径当作不可改变的来源要求，也不能把鲸眼原始配置与平台展开结果无验证地视为等价。

| 编号 | 旧读取路径 | 用途、使用范围与旧缺省 | 读取证据 |
| --- | --- | --- | --- |
| S01 | `I.name` | 监控项名称回退；图表标题在其为空时用 expression | E09 `clean_item`；E12 |
| S02 | `I.expression` | 多查询表达式；图表参数缺省空字符串 | E12 |
| S03 | `I.functions[]` | 监控项函数链，原结构传入图表查询参数，缺省空列表 | E12 |
| S04 | `I.query_configs[]` 的条数与顺序、`R.strategy.items[]` 的条数 | 首条决定部分指标/维度与类型；图表遍历第一个监控项的全部查询；清洗指标名有监控项数量判断 | E04；E05；E09 `clean_metric_name`；E12 |
| S05 | `Q.data_source_label`、`Q.data_type_label` | 选择查询翻译器、Prometheus 分支和图表查询类型，通常直接索引；首条也影响指标名加工 | E09；E12 |
| S06 | `Q.metric_field` | 指标字段名；首条用于指标库/单位/唯一标识/维度查询，全部查询用于图表。部分路径可从 promql 派生，部分直接索引 | E04；E05；E06；E09；E12 |
| S07 | `Q.metric_id` | 指标完整标识；DATA/云平台由首条解析结果表；图表副标题读取全部查询的该值 | E05；E06；E09 `clean_result_table_id`；E12 |
| S08 | `Q.result_table_id` | 图表查询表及系统指标维度别名判断；缺省空字符串。不等同于 Converter 写入的 strategy.result_table_id | E12 |
| S09 | `Q.promql` | DATA 的结果表/指标解析，缺 agg_dimension 时解析 `by(...)`；图表查询表达式。不是所有路径都支持缺 metric_field 回退 | E04 `__init__`；E05；E09；E12 |
| S10 | `Q.agg_dimension[]` | 首条约束维度展示；各查询约束图表 group_by/维度过滤；含 `le`、`bk_task_index_*` 的特殊处理 | E04；E05；E06；E12 |
| S11 | `Q.agg_condition[].key/method/value/condition` | 图表过滤条件，与当前告警维度合并；value 可以是多值，condition 表示 and/or | E12 `create_where_with_dimensions/load_agg_condition_instance` |
| S12 | `Q.agg_method`、`Q.agg_interval`、`Q.alias` | 图表聚合方法、周期和查询别名；旧图表缺省 COUNT/60/A。外层丰富的聚合字段另从外查 strategy_item 获取 | E12；E09 `clean_aggregate_func/clean_time_interval` |
| S13 | 首条 `Q.unit` | 派生指标单位查不到时的策略快照回退值，缺省空字符串 | E09 `get_query_config_unit/clean_unit` |
| S14 | `Q.query_string`、`Q.index_set_id` | 日志查询语句/索引集传入图表参数；部分日志翻译器直接用 query_string 作 metric_field。这不是日志 Cleaner 从外查 source_config 读取的语句 | E12 `translate_bk_log_search_log_metric/get_graph_panel` |
| S15 | `Q.custom_event_name` | custom/event 翻译为 filter_dict.event_name，直接读取；也进入输出查询配置 | E12 `translate_custom_event_metric` |
| S16 | `Q.bkmonitor_strategy_id`、`Q.alert_name` | 分别用于 bk_monitor/alert、bk_fta 查询的指标字段翻译；只对对应来源类型有意义 | E12 `translate_bk_monitor_alert_metric/get_graph_panel` |
| S17 | `Q.time_field`、`Q.extend_fields`、`Q.functions[]` | 图表查询时间字段、扩展参数及函数链；分别缺省 null/空对象/空列表；旧逻辑透传，内部结构未在此逐叶解释 | E12 |
| S18 | `I.algorithms[].level/type/config.visual_type`、`Q.intelligent_detect.result_table_id` | 旧图表智能检测增强输入；本次已确认整体排除，不迁移额外指标、预测/上下界/分数或离群 cluster 条件 | E12 |

各条件查询类型只记录读取点，不表示主分类与查询类型的笛卡尔积都可达。旧 Converter 返回
图表的首个 target.data，因此只出现在图表标题/副标题或第二个 target 的信息不等于已经进入
旧最终输出；S01/S07 本身还有名称/指标解析用途，不能因此整体删除。

## 5. 各分类的对象定位与专用维度

以下 `D.xxx` 均须按第 2 节追溯到原始维度，不是要求新增名为 D 的 Event 字段。
除特别说明外，类型保持来源值；旧代码中的 `int()`、`str()` 是转换行为，不是统一 ID 类型契约。

### 5.1 基础监控

| 编号 | 旧维度或输入 | 作用与组合要求 | 缺失/边界影响与证据 |
| --- | --- | --- | --- |
| B01 | `D.bk_inst_id` | 监控来源分支；配外查 object_model_code 查实例 | 真值才匹配，并转 int；缺失时检查 B02。E03 |
| B02 | `D.obj_model_id` + `D.obj_model_inst_id` | 监控来源的另一组合；模型 code 与实例 ID，用于硬件等模型展示 | 两者真值才匹配；obj_model_id 不能直接与数字 Meta 模型 ID 混用。E03；E04 |
| B03 | `D.bk_collect_config_id` | 采集任务 → 外查 CollectConfig → 模型/实例 → 展示名 | 数字字符串转 int，非数字保留字符串；不能一律限定整数。E03 |
| B04 | `D.cw_object_model_id` + `D.cw_object_model_inst_id` | 无数据实例查找和通用模型/实例展示；前者是 Meta 模型 ID | 分支仅检查模型 ID 真值，随后直接读取实例 ID；缺一不能完整定位。E03；E04 |
| B05 | `D.ip`、`D.bk_target_ip`；`D.bk_cloud_id`、`D.bk_target_cloud_id` | 主机 IP/云区域、对象展示回退、云区域展示；前一别名覆盖后一名称，F09 可优先用于查询 | 不把 IP 当租户或跨云全局身份；样本云区域有整数和字符串。E02；E03；E09 |
| B06 | `D.bk_target_host_id` | BaseConverter 缺查询实例 ID、缺 F10 时的主机 ID 回退；DATA 内置主机定位也优先用它 | 样本含字符串/null，不等同于 D.bk_host_id。E04；E05 |
| B07 | `D.bk_biz_id` | 业务展示、拓扑回退、范围标签的间接来源 | 旧流程可由 F05 补入，也可能从资源拓扑查询得到；新输入职责已另行确认，见迁移设计第 5.24 节。E02；E09 |
| B08 | 其他 `D.<维度名>` | 按策略聚合维度与指标维度定义生成展示、where 条件和图表过滤 | 是开放维度集合。样本有 device_name、disk_name、hostname、ifIndex、ifDescr、data_id；process 维度名还涉及 pid/process_name。不能只保留固定定位键。E04；E05；E12 |
| B09 | `D.bk_obj_id` + `D.bk_inst_id` | bk_monitor/log 查询翻译把模型与实例换成 `bk_<模型>_id` 过滤字段 | 条件类型专用，不是额外主分类；两个键均存在才转换。E12 |

旧基础分支顺序是监控来源 → 采集任务 → 无数据 → 系统指标 → 拨测，首个命中返回。
F09 查询优先使用 event.target，但 SystemMetricMixin 保存匹配键仍使用 D 中 IP/云区域；两者不一致
时可能查询得到却匹配不回实例。这里只暴露旧规则风险，不把该行为迁为验收规则。

### 5.2 基于数据（DATA）

复用 F、S、B 的相关输入，额外需要核对：

| 编号 | 旧维度或输入 | 用途与条件 | 证据 |
| --- | --- | --- | --- |
| D01 | `D.cloud_id` + `D.instanceid` + `D.type` | 结果表被识别为云指标时，三个真值齐全才查询云资源；type 是资源类型 | E05 `clean_object_inst_by_dimension` |
| D02 | `D.object_model_id` | 多模型关联且结果表以 hardware_ 开头时，改用该键查 Meta 模型；拼写与 B02.obj_model_id 不同 | E05 |
| D03 | `D.obj_model_inst_id` | hardware_ 单模型实例分支用此值；多模型分支存在把它赋给模型局部变量而非实例变量的代码，不应宣称两条分支等价 | E05 `clean_object_inst_by_dimension/get_object_model_inst_id` |
| D04 | `D.cw_object_model_id/cw_object_model_inst_id`、`D.bk_target_host_id/bk_target_ip/bk_target_cloud_id`、`D.task_id` | 分别服务多模型/普通对象、system 主机和拨测对象定位；按结果表及指标库模型关系选择 | E05 |
| D05 | F06、F07，以及 `D.bk_biz_id` | APM 优先使用外层/策略业务；其他资源查不到业务时可退回维度业务 | E05 `clean_biz_and_alarm_obj` |

旧 APM 的应用名称来自外查策略/模板形成的 data_source 或 S07/S09 派生的结果表，不是默认读取
回调中的 apm_app_id。迁移时 data_source 统一读取 StrategyConfig.spec.data_source，见
[迁移设计第 5.16.4 节](../design/alarm-callback-enrichment-migration.md#5164-已确认所有分类的数据源统一读取鲸眼策略配置)。
其专用原始维度列在第 6 节。

### 5.3 日志指标、日志关键字

| 编号 | 旧输入 | 用途与边界 | 证据 |
| --- | --- | --- | --- |
| L01 | F01 及相关 S 字段 | 找策略、指标/查询参数；主题 ID、主题名称、检索语句另从依赖取得，见第 7 节 | E07；E08；E10 |
| L02 | F02 | 日志指标裁掉 `,关联信息` 后缀；关键字按“无数据”或命中次数加工内容 | E10 `clean_content` |
| L03 | F11 | 关键字关联信息输出；通用 related_info 会被此字段覆盖，不能自动互为回退 | E08 `LogCollectConverter.__init__`；E10 |
| L04 | F08 的任意维度 | 日志关键字逐条展示；名称由指标维度定义映射，未映射时保留 key；不要求固定 keyword 字段 | E08 `get_dimensions_display` |
| L05 | F05、F06 | 业务读取存在 Processor 与 BaseConverter 的两层差异，按第 3 节分别保留，尚未合并 | E07；E08；E04 |

日志关键字当前从告警内容中解析次数/比较关系，没有读独立的 `hit_count` 或匹配原文列表字段。
不能依据“命中内容”的功能名称虚构这两个必需输入。

### 5.4 拨测

| 编号 | 旧维度 | 用途及旧空值语义 | 证据 |
| --- | --- | --- | --- |
| U01 | `D.task_id` | 查任务、对象名及任务业务；缺省 0，预处理转 int；有/无 task_id 都有样本 | E03 `UptimeCheckMixin`；E13 |
| U02 | `D.node_id` | 节点名/节点维度，旧节点映射使用 `plat_id:ip`；无对应名时展示原值。通用 dimension_escape 另查 CollectNode | E13；E11 `dimension_escape` |
| U03 | `D.url` | 目标 URL 展示，缺失不产生条目 | E13；E05 |
| U04 | `D.target_host` + `D.target_port` | TCP/UDP 等目标地址与端口展示，各自有值才加入 | E13；E05 |
| U05 | `D.target` + `D.target_type` | 新式目标地址/地址类型展示；不要与 F09.event.target 混同 | E13；E05 |
| U06 | `D.bk_target_cloud_id` | 任务命中后的云区域展示及基础清洗云区域字段；0 的真假判断不能直接照搬 | E13；E09 |

DATA 中 uptimecheck 结果表也消费这组字段，且按任务协议选择部分目标展示；主分类拨测的
UptimeCheckConverter 则独立追加有值的目标条目。并非完全相同的显示流程。

### 5.5 云平台

| 编号 | 旧维度/输入 | 用途及缺失影响 | 证据 |
| --- | --- | --- | --- |
| C01 | `D.cloud_id` + `D.instanceid` | VmwareConverter 直接索引，联合查询 Cloud / CloudResource，生成资源名称与平台名称 | E06 |
| C02 | `D.type` | DATA 云指标定位三元组的第三项、通用维度转义；VmwareConverter 直接路径不要求它作联合查询条件 | E05；E11 |
| C03 | S06、S07、S10 及指标定义选中的 `D.<key>` | 解析指标/结果表，查询云维度名称；cloud_id/instanceid 展示替换为名称，保留原值 | E06 |

平台类型、资源模型、单/多指标中文名称与表达式主要取自外查 CloudStrategyConfig/指标库；
不能把 cloud_type、cloud_resource_type、资源名称一律加入“回调必须携带”清单。

### 5.6 接入对象（本次排除）

| 编号 | 旧路径 | 用途与边界 | 证据 |
| --- | --- | --- | --- |
| A01 | `R.latest_anomaly_record.origin_alarm.data.dimensions[unique_id]` | unique_id 由外查模型 ar_dimensionality 决定；用值向模型所属服务查 display_name；查不到时回退原始值 | E14 `preprocess_alarms` |
| A02 | `D[unique_id]` | AccessObjectConverter 从用于展示的工作维度中去掉唯一维度，不是从这里取得 A01 的查询值 | E14 |

服务来源也由模型元数据决定；当前服务映射只有 kapm_saas。分类入口注册了处理类，当前分类器
没有选中 ACCESS_OBJECT 的分支，14 个样本也没有 A01 路径。用户已确认从本次实现范围排除；
本节只保留旧代码的读取意图和入口不可达证据，不形成 Linkd 输入或验收契约。

## 6. 横跨分类的 APM/K8s 维度

### APM

| 编号 | 旧维度 | 用途与限制 | 证据 |
| --- | --- | --- | --- |
| P01 | `D.service_name` | 服务展示文本；适用维度条目还生成 apm_service_name | E15 `apm_field_add` |
| P02 | `D.bk_instance_id` | 通过展示条目的 real_key/real_value 生成 apm_instance_name，参与应用/服务/实例关系 | E09 `apm_dimension_key_map`；E15 |
| P03 | `D.span_name`、`D.net_peer_name` | 同上生成接口名/对端名称；这些是 APM 字段，不能与 CMDB 实例 ID 混同 | E09；E15 |

P02/P03 经维度展示条目传递，受聚合维度及名称映射规则影响；不能宣称 D 中有值就必定输出。
应用 ID 与别名来自 APM 查询匹配结果；应用名称由 data_source 或结果表派生，再用于精确匹配。
派生标签由业务与应用 ID 构造；这些信息不列为本阶段需要上游填入的原始字段。

### K8s

| 编号 | 模型/用途 | 旧维度组合 | 证据 |
| --- | --- | --- | --- |
| K01 | 集群定位、资源业务和标签 | `D.bcs_cluster_id` | E09 `k8s_field_add`；E16 |
| K02 | Namespace | K01 + `D.namespace` | E09；E16 |
| K03 | Node | K01 + `D.node`；对象展示还可从 `D.instance` 按冒号前半段回退 | E09 `clean_object`；E16 |
| K04 | Service | K01 + `D.namespace` + `D.service` | E16 |
| K05 | Workload | K01 + `D.namespace` + `D.workload_kind` + `D.workload_name`；kind=Pod 时公共函数改查 Pod | E11 `build_k8s_inst_id`；E16 |
| K06 | Pod | K01 + `D.namespace` + `D.pod_name` | E16 |
| K07 | Container | K01 + `D.namespace` + `D.pod_name` + `D.container_name` | E16 |
| K08 | PersistentVolume | K01 + `D.persistentvolume` | E16 |
| K09 | PersistentVolumeClaim | K01 + `D.namespace` + `D.persistentvolumeclaim` | E16 |

以上是当前身份注册表与调用意图的输入组合。`k8s_field_add` 还对 namespace/service/workload/
pod/container/node 字段进行条件追加；集群名称、业务归属来自依赖，不能从同名输入猜测。

**当前源码冲突**：KAC `BaseConverter.format_event_dict` 向 `build_k8s_inst_id` 传 `es_client=`，
但它经 utils 导入的当前公共函数签名为 `(dimension_info, object_model_code, bk_tenant_id=None)`；
`BaseClear.clean_model_inst_id` 则把 ES_CLIENT 作为第三个位置参数传入，落在租户参数位置。
公共函数当前使用 cmdb_query，不能概括为 KAC 这两个调用均已完成 ES 查询迁移。
本轮不修复 Kingeye；用户后续已确认 Linkd 的三类 K8s 查询统一对齐 OneModel 实例存储，
见迁移设计第 5.30 节；后续已明确使用 Elasticsearch，具体读取规则见第 5.30.1 节。

2026-09-03 补充核对：公共 utils 中的 cmdb_query 是 kingeye.base.onemodel 的进程内别名，
不能据该名字认定存在可供 Linkd 直接调用的同名 HTTP 接口。
[search_tenant_entity_documents](../../../../../kingeye/src/kingeye/base/domains/onemodel/instances.py)
（184）通过 search_tenant_entities 调用 fabric Reader；
[Reader 装配](../../../../../kingeye/src/kingeye/base/domains/onemodel/fabric_storage.py)（42）由配置选择实现，
仓库的 [InstanceStorageFabricReader](../../../../../kingeye/src/kingeye/base/candidacy/infras/instance_storage/onemodel.py)
（367）使用统一实例存储 SDK。这与 KAC k8s_field_add 直接使用 ResourceIndex 查询 ES 的路径
应分别记录；当前只核对源码，未验证实际部署配置、存储后端或接口可用性。输出字段已在
[迁移设计第 5.29 节](../design/alarm-callback-enrichment-migration.md#529-已确认k8s-专用输出归入-enrichk8s保留-bcs_cluster_id)
确认；查询来源已按
[迁移设计第 5.30 节](../design/alarm-callback-enrichment-migration.md#530-已确认k8s-查询统一对齐当前-onemodel-实例存储)
统一为当前 OneModel 实例存储，Go 侧对接其 Elasticsearch 后端。

已有调用示例位于
[common/alarm_callback/basic_push_alarm_data.py](../../../../../kingeye/src/kingeye/common/alarm_callback/basic_push_alarm_data.py)：
k8s_field_add（523）分别按 cluster_id 查询集群、按 cluster_id + namespace 查询 Namespace，
两次均显式传入 self.bk_tenant_id，随后优先采用 Namespace 业务、回退集群业务，并读取集群名称；
clean_model_inst_id（912）调用 build_k8s_inst_id 时显式传入 event_data.bk_tenant_id。
公共 [utils.py](../../../../../kingeye/src/kingeye/common/alarm_callback/utils.py) 的
search_k8s_instance_document（588）将模型与过滤字段组织为 EntityQuery，实际调用
onemodel.search_tenant_entity_documents；build_k8s_inst_id（534）使用同一查询入口读取
cw_object_model_inst_id。这些是当前 common 路径的源码示例，不代表 KAC 的旧调用点已同步；
用户在核对示例后明确确认 Linkd 的 K8s 查询对齐当前 OneModel 实例存储。

当前 [SDK 装配](../../../../../kingeye/src/kingeye/base/candidacy/infras/instance_storage/runtime.py) 的
build_instance_storage_sdk（252）支持 Elasticsearch 与 Doris，由显式 read_backend 参数或
BKAPP_INSTANCE_STORAGE_READ_BACKEND 配置选择，源码默认 elasticsearch。该默认值不能
证明待接入部署使用哪个后端；用户已明确本次使用 ES。该事实来自用户确认，本次未读取
运行环境配置或验证真实存储连接。

进一步核对 ES 读取路径：runtime.resolve_elasticsearch_instance_write_target 虽名称包含
write，但当前适配器读写均使用该目标解析器；带模型过滤的 K8s 查询读取相应 kingeye_k8s_*
目标，而不是无条件使用 kingeye_all_instance。模型路由、扁平字段和租户过滤证据收敛在
[迁移设计第 5.30.1 节](../design/alarm-callback-enrichment-migration.md#5301-已确认本次接入-onemodel-的-elasticsearch-读后端)。

2026-09-03，经用户指出后重新核对：不能仅因标准 bk_biz_ids 是列表，就认定旧 bk_biz_id
无法继续读取或必须修改消费规则。search_k8s_instance_document（588）查询 page_size=1，
返回 dict(page.items[0]) 或空字典，回调取得的是单个实例文档。

字段保留链路如下：

- [ES _instance_record](../../../../../kingeye/src/kingeye/base/candidacy/infras/instance_storage/elasticsearch.py)
  （423）将未列入保留/标准字段的键保存为 attributes；bk_biz_id 不在这些排除字段中。
- [OneModel _instance_document](../../../../../kingeye/src/kingeye/base/candidacy/infras/instance_storage/onemodel.py)
  （329）展开 record.attributes，并另行提供标准 bk_biz_ids 列表。
- [document_to_entity](../../../../../kingeye/src/kingeye/base/domains/onemodel/_shared.py)（216）的属性
  排除项也不包含 bk_biz_id；[entity_to_document](../../../../../kingeye/src/kingeye/base/domains/onemodel/instances.py)
  （117）再展开 entity.attributes，因此原有 bk_biz_id 会保留到最终返回文档。

当前 common k8s_field_add（560）仍可按 namespace_info.get("bk_biz_id") or
cluster_info.get("bk_biz_id") 消费。该证据不保证每条真实记录均有此属性，但足以撤回
“接入 OneModel 必须改为 bk_biz_ids 列表”的推导；沿用旧消费规则见
[迁移设计第 5.30.2 节](../design/alarm-callback-enrichment-migration.md#5302-取证澄清单实例文档保留-bk_biz_id沿用旧消费规则)。

## 7. 不应要求回调提供的查询结果与中间字段

| 信息 | 旧实际来源 | 说明 |
| --- | --- | --- |
| config_type、monitor_item_type、metric_source、object_model_code | 分类前查询的 StrategyConfig 元数据/spec；云模型由 CloudStrategyConfig/插件映射确定 | 不从回调顶层取同名字段来替代分类查询 |
| strategy_config / cloud_strategy_config、config_id、monitor_template_id | 外查配置及其 metadata.labels | 不等同于 F01 后端策略 ID |
| strategy_item、alarm_alias、alias_name、field_tag、table_id、field_name | 外查策略配置 | 与 S 中的平台字段可能重叠；已确认按各消费位置沿用旧来源，不统一覆盖 |
| log_theme_id / log_theme_name / 日志 query_string | 策略 spec.source_config；主题名还查主题映射并回退配置名 | Processor 把结果写回 alarm_info 不会使其成为原始输入 |
| instance_detail_info、identification、unique_id | Processor 查询/组合结果、模型 ar_dimensionality | 独立调用上下文，不向 Event 回写 |
| custom_dimensions_map、event_message.dimensions | 第 2 节 D 的加工和传递 | 原始输入要展开成 F08/F10/F05 及别名，不复制旧工作字典 |
| strategy_detail.result_table_id / metric_field | Base/Data Converter 从配置或查询快照派生后写入 | 与 S08/S06 不必相同；不是新增的必需顶层输入 |
| strategy_detail.monitor_template_id | 下游有可选读取并回退 event_data.strategy_id；该字段未在 14 个原始策略样本中出现 | 后者已由外查配置标签注入，不要求上游为回退分支补字段 |
| 业务/集群/模块名称、模型名称、实例显示名、云区域名 | CMDB、Meta、实例/拓扑查询和名称映射 | 业务 ID 仍有多处回调输入，不能连同名称一并排除 |
| APM 应用信息、K8s 集群/namespace 业务、云平台/资源详情 | APM、K8s 查询、Cloud/CloudResource | 缺失应依迁移的局部失败契约处理，不能用回调同名字段伪造查询成功 |
| dynamic_group_id、cw_labels、strategy URL | Redis 投影读取、已定位资源/业务派生、配置及 Web 基础地址 | 属于输出或依赖结果，不是回调必填值；动态分组读取键已确认对齐当前写入端，见[迁移设计第 5.16.5 节](../design/alarm-callback-enrichment-migration.md#5165-已确认动态分组读取键对齐当前写入规则) |
| source_id、source_name | 旧来源 ID 为固定值 built_in_bk；旧名称从 KMC_NAME 配置读取并默认“鲸眼监控” | 已确认保留 ID 原逻辑，名称本次默认“鲸眼监控”并在实现处留 TODO，后续读取配置；不新增回调输入，见[迁移设计第 5.33 节](../design/alarm-callback-enrichment-migration.md#533-已确认保留旧来源标识与名称名称配置读取暂留-todo) |
| alarm_dimension_display、metric_query_params | Converter/图表参数构造结果 | 下文 S/F/D 才是要盘点的输入；不要求来源预先构造完整结果 |

## 8. 读取过但不直接列为本次必需输入的字段

| 旧路径/行为 | 本次处理与原因 |
| --- | --- |
| `R.id`、`R.event.id`；`R.begin_time/create_time`；`R.status` | 旧身份/时间/状态装配及日志定位。Linkd 已有 Event 与生命周期，不因迁移旧 AlarmEvent 构造函数而重复要求输入或覆盖核心事实 |
| `clean_meta_info()` | 返回旧 event_data.id 的字符串；[BaseProcessor.process_alarms](../../../../../kingeye/src/kingeye/kac/alarm_callback/processors/base.py)（162–180）按当前时间生成批次 ID 池并赋给转换对象，该值不是回调中的业务 ID。已确认保留 meta_info 并改从 Event.SourceEventID 取值，见[迁移设计第 5.34 节](../design/alarm-callback-enrichment-migration.md#534-已确认meta_info-从-eventsourceeventid-取值) |
| `R.end_time`、`R.event.end_time`、`R.description` | 旧终态时间/原因分支；已明确排除恢复/关闭丰富，不加入本次输入需求 |
| `clean_bk_service_id()`、`clean_Namespace()` | 两项方法固定返回空字符串，没有来源读取需求；已确认不迁移这两个空占位输出，实际 K8s namespace 继续保留，见[迁移设计第 5.31 节](../design/alarm-callback-enrichment-migration.md#531-已确认不迁移-bk_service_id-与-namespace-空占位字段) |
| `R.event.tags[].key/value` 中 `__NO_DATA_DIMENSION__` | 旧接入过滤判断；过滤已排除。日志无数据文案另由 F02 识别，不据此要求迁移整份 tags |
| `R.event.bk_topo_node`、`D.bk_host_id` | 旧适配过程写入后又被部分 Converter/where 流程移除；保留处理证据，不把拓扑名称/关系误认为由它直接提供。F10 主机 ID 的真实用途另列 |
| `R.related_info` | 通用 Converter 写入 event.related_info；通用内容函数只读 description，日志关键字又用 F11 覆盖。当前跟踪未见它对本次丰富输出的独立必要性 |
| `R.dimensions[].display_key/display_value` | 样本存在，但当前维度展示从 key/value 与依赖重新生成，见第 3 节 |
| `R.event.anomaly[旧等级].anomaly_time` | 旧 BaseClear 读取并输出 anomaly_begin_time，14 个原始样本均缺少 anomaly。已确认改为可选输入 Event.ExtraData.anomaly_begin_time，保存到 enrich.metric.anomaly_begin_time；缺失时省略，不回退 Event.OccurredAt，见[迁移设计第 5.32 节](../design/alarm-callback-enrichment-migration.md#532-已确认首次异常点时间作为可选补充信息) |
| `R.strategy.item.name/metric`、`R.strategy.item_list[0].metric_field` | Cleaner 的公有云/缺配置兼容分支有读取；指定 VmwareConverter 主路径却要求 items/query_configs，未取得能贯通这些形态的样本。暂列条件证据，不宣称可达或擅自删除能力 |
| `Q.raw_query_config` | 图表函数仅在 use_raw_query_config=true 时读取；当前 Converter 调用未开启，故不作为本次直接输入 |
| `R.alert_name/dedupe_md5` | 图表的无策略计数回退使用；当前分类前已要求成功查到策略，且 BaseConverter 先读取 strategy.items，不宣称此回退在迁移主链路可达 |
| `alert.event.extra_info.origin_alarm.data.values.cluster` | 旧图表离群算法分支读取；本次随全部智能算法专用图表增强一并排除，不新增 Event 输入位置 |

## 9. 样本与下一步取值决策

14 个样本都包含 F01/F02/F03/F04/F05/F07；F06 仅 3 个有原始值，F09/F10 各 7 个。
`related_info/log_related_info` 各 14 个出现，均为字符串，不代表有业务内容。样本中的维度值有
字符串、整数和 null，不能据此把全部标识、空值或所有维度冻结成单一类型。

来源目录中没有接入对象、APM/K8s 专用样本；`ACCESS_OBJECT` 又缺少分类入口可达证据，已确认从
本次实现范围排除。已有 APM 模型字段单测属于更局部的行为证据。

后续用户已给出 F01–F11 及各分类维度映射方向，统一见迁移计划第 5.19 节；本盘点继续保留
旧实现证据，不把用户的新映射反向描述为旧 KAC 行为。上游可控后，不要求兼容旧外壳；
最新按完整平台引用触发两份策略读取，任一缺失按 partial；字段消费来源与读取条件分别设计。
不要求上游携带全量策略，也不自动回退 SourceRawData。

## 10. 源码索引

以下链接指向同级 Kingeye checkout，需该仓库同时存在；对应快照见第 1 节。符号与行号用于核对，
不表示全部函数均运行验证。

| 编号 | 文件与主要符号 |
| --- | --- |
| E01 | [entry.py](../../../../../kingeye/src/kingeye/kac/alarm_callback/entry.py)：StrategyCollectTypeMatcher，AlarmDataHandler.classify_alarm（142）、_search_configs_by_bk_strategy_ids（207） |
| E02 | [processors/base.py](../../../../../kingeye/src/kingeye/kac/alarm_callback/processors/base.py)：process、adapt_alarm_data（289）、filter_no_data_alarms |
| E03 | [processors/basic_event.py](../../../../../kingeye/src/kingeye/kac/alarm_callback/processors/basic_event.py)：AlarmTypeMatcher，MonitorSource/CollectTask/NoData/SystemMetric/UptimeCheckMixin |
| E04 | [converter/base.py](../../../../../kingeye/src/kingeye/kac/alarm_callback/converter/base.py)：__init__（33）、clean_event（345）、get_dimensions_display（357）、get_obj_model_dim_display（442）、format_event_dict（542）、get_metric_query_params（628） |
| E05 | [converter/basic_data.py](../../../../../kingeye/src/kingeye/kac/alarm_callback/converter/basic_data.py)：get_dimensions_display、format_event_dict、clean_object_inst_by_dimension（340）、get_object_model_inst_id（478）；[processors/basic_data.py](../../../../../kingeye/src/kingeye/kac/alarm_callback/processors/basic_data.py)：adapt_alarm_data |
| E06 | [converter/vmware.py](../../../../../kingeye/src/kingeye/kac/alarm_callback/converter/vmware.py)：get_table_id/get_dimensions_display/format_event_dict；[cleaner/private_cloud.py](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/private_cloud.py)：clean_item |
| E07 | [processors/log_metric.py](../../../../../kingeye/src/kingeye/kac/alarm_callback/processors/log_metric.py)：adapt_alarm_data/preprocess_alarms |
| E08 | [processors/log_keyword.py](../../../../../kingeye/src/kingeye/kac/alarm_callback/processors/log_keyword.py)：adapt_alarm_data；[converter/log_event.py](../../../../../kingeye/src/kingeye/kac/alarm_callback/converter/log_event.py)：LogCollectConverter |
| E09 | [cleaner/base.py](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/base.py)：get_biz_topo_message、k8s_field_add（383）、clean_object、clean_item、clean_metric_name、clean_dimension_info、clean_model_inst_id、clean_anomaly_begin_time、clean_unit |
| E10 | [cleaner/log_metric.py](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/log_metric.py)：clean_content；[cleaner/log_keyword.py](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/log_keyword.py)：clean_content（191）、clean_log_relate_info |
| E11 | [common/alarm_callback/utils.py](../../../../../kingeye/src/kingeye/common/alarm_callback/utils.py)：build_k8s_inst_id（534）、get_alarm_content（608）、dimension_escape（651）；由 [kac utils](../../../../../kingeye/src/kingeye/kac/alarm_callback/utils.py) 导入 |
| E12 | [handle_alert_info.py](../../../../../kingeye/src/kingeye/kac/alarm_callback/handle_alert_info.py)：HandleAlertToGraphPanel 翻译器、create_where_with_dimensions（523）、get_dimensions（581）、get_graph_panel（609） |
| E13 | [converter/uptime_check.py](../../../../../kingeye/src/kingeye/kac/alarm_callback/converter/uptime_check.py)：UptimeCheckConverter；[processors/uptime_check.py](../../../../../kingeye/src/kingeye/kac/alarm_callback/processors/uptime_check.py)：plenty_alarm_message |
| E14 | [processors/access_object.py](../../../../../kingeye/src/kingeye/kac/alarm_callback/processors/access_object.py)：preprocess_alarms（11）；[converter/access_object.py](../../../../../kingeye/src/kingeye/kac/alarm_callback/converter/access_object.py)：get_dimensions_display |
| E15 | [cleaner/basic_data.py](../../../../../kingeye/src/kingeye/kac/alarm_callback/cleaner/basic_data.py)：apm_field_add（59）、cloud_field_add（116） |
| E16 | [onemodel/kubernetes.py](../../../../../kingeye/src/kingeye/base/domains/onemodel/kubernetes.py)：KUBERNETES_IDENTITY_DIMENSIONS（30）；[kubernetes_constant.py](../../../../../kingeye/src/kingeye/common/constant/kubernetes_constant.py)：K8S_DIMENSION_NAME（1291） |
| E17 | [测试样本目录](../../../../../kingeye/src/kingeye/kac/tests/alarm_callback/source_data/) 与 [conftest.py](../../../../../kingeye/src/kingeye/kac/tests/alarm_callback/conftest.py)：origin_alarms 输入、外部依赖 mock；[test_apm_model_fields.py](../../../../../kingeye/src/kingeye/kac/tests/alarm_callback/test_apm_model_fields.py)：APM 局部字段测试 |

## 11. 鲸眼配置对策略输入的覆盖情况

2026-09-02，补充核对同一 Kingeye 源码基线。监控平台策略与鲸眼 StrategyConfig 是两份数据，
通过 bk_strategy_id 关联；version 只标识监控平台策略。
2026-09-03，用户改为完整引用下两份均读、任一缺失按 partial，替代此前鲸眼足够就不查平台的
规则；随后确认重叠字段沿用旧消费来源。以下只记录静态覆盖能力和转换差异，不构成将旧平台
字段改为鲸眼字段的迁移要求，也不代表已通过逐分类回归。

表中 `SC` 为鲸眼 StrategyConfig，`SI = SC.spec.strategy_item`，`QC = SI.query_configs[*]`。
QC 与旧 S 表里的监控平台 Q 不是同一个结构；尤其 SI.query_configs 为空在既有指标服务中可表示
单指标，不能按旧平台数组结构直接判定缺字段。直接配置、可推导值与其他指标服务结果分别注明。

| 旧编号 | 所需信息 | 鲸眼侧已找到的来源/转换 | 来源判断与剩余核对 |
| --- | --- | --- | --- |
| S01 | 监控项名称 | SC.spec.name/alias_name；BaseExecutor.build_items 会构造 `agg_method(name)`，PromQL 下发另以 promql 替换名称 | 鲸眼有相关数据，但显示别名与平台 item.name 不完全相同；各消费位置按旧来源取值 |
| S02 | 表达式 | SI.expression | 可直接取得；单/多指标表达式与查询别名需保持一致 |
| S03 | 函数链 | SI.functions，逐查询可有 QC.functions | 有鲸眼来源；类型定义还允许字符串，必须核对具体格式，不能任意字符串转列表 |
| S04 | 监控项/查询数量和顺序 | SI.query_configs，单指标还用 SC.spec.table_id/field_name；BaseExecutor.build_items 当前构造一个监控项 | 可按鲸眼配置形成所需逻辑查询列表，但不能把旧 items 数量判断原封不动套用到 QC 数量 |
| S05 | data_source_label/data_type_label | DataExecutor/TargetExecutor 按 query_config_type、metric_source、结果表与指标元数据选择；日志/APM 有独立构造规则 | 可推导候选，部分依赖指标元数据；不是把 spec.metric_source 改名就等价，不能据此跳过已确认的平台读取 |
| S06 | metric_field | 普通单指标用 SC.spec.field_name，多查询用 QC.metric_field；日志智能指标会拼接聚合方法前缀，平台告警组合有专用转换 | 多数已有配置来源；要区分原字段与执行字段，特殊分支核对转换后再使用 |
| S07 | metric_id | 普通查询由 data_source_label/result_table_id/metric_field 组合；日志构造 index_set 形式；指标组合另生成 alert 形式 | 可按类别推导，不假定一条拼接公式覆盖所有指标；只需要表和字段时不必先拼旧 metric_id 再解析 |
| S08 | result_table_id | SC.spec.table_id 或 QC.result_table_id；日志部分路径使用空表名，dataset/平台侧转换可能展开或改写表 | 普通路径有鲸眼来源，展开后执行表是否必要按查询用途核对，不将原始表和平台运行表混同 |
| S09 | PromQL | SI.query_configs[0].promql，query_config_type 标识 Prometheus | 有鲸眼来源；继续需要表名/指标名时按明确规则派生，字段缺口与第 5.23 节的读取触发条件分别处理 |
| S10 | 聚合维度 | SI.agg_dimension 或 QC.agg_dimension；TargetExecutor 会按目标类型再追加维度 | 基础配置可取；已确认以本次告警观测范围为目标，保留聚合语义；具体维度选择与转换仍需验证 |
| S11 | 聚合条件 | SI.agg_condition 或 QC.agg_condition；硬件/采集任务/拨测/K8s 下发会加入目标条件 | 鲸眼有原始条件，但旧图表 agg_condition 仍取平台指定版本；合并沿用第 11.3 节旧行为并保留 OR 问题，Go 实现仍待验证 |
| S12 | 聚合方法/周期/别名 | SI 或逐查询的 agg_method/agg_interval/alias；周期可能带单位，经 time_converter 转换 | 有鲸眼来源；SI 级与 QC 级适用范围及时间单位必须明确，不能直接把 `60s` 当数字字段 |
| S13 | 单位 | 原清洗已经查 MonitorMetricLibrary/MonitorMetric，APM 下发还会查 APM 指标；BaseExecutor.get_unit 会考虑函数类别 | 不全在 SC 内，有既定指标依赖及旧平台单位回退；是否消费回退值按字段规则判断，两份策略读取按第 5.23 节执行 |
| S14 | 日志语句/索引集 | SC.spec.source_config.query_string/index_set_id；LogExecutor 有明确查询构造 | 鲸眼存在对应配置，旧消费来源与默认值仍需区分；日志智能指标的语义单列验证 |
| S15 | custom_event_name | TargetExecutor 的多指标分支由 QC.metric_field 设置；其他来源类型需继续核对 | 已有特定分支的推导证据，不能泛化为所有 custom/event；未知分支登记字段缺口，读取条件按第 5.23 节执行 |
| S16 | bkmonitor_strategy_id/alert_name | 监控项组合的 QC.bkmonitor_strategy_id，TargetExecutor 据此设置平台查询字段 | 对已检查的 metric_group 分支有鲸眼来源；不要与当前告警关联的 bk_strategy_id 混同 |
| S17 | time_field/extend_fields/query functions | 日志从 source_config.log_theme_list[0].time_field 或 source_config.time_field；其他已检查分支使用常量 time；函数来自 QC/SI | 时间字段和函数有类别来源；任意 extend_fields 不在 SC 显式定义中。其内部是否真的被丰富消费、能否从鲸眼配置取得仍需逐项核对 |
| S18 | 检测算法、可视类型、智能检测执行表 | 鲸眼与平台存在相关数据 | 本次已确认排除全部智能算法专用图表增强；本行只保留来源取证，不形成迁移输入或验收要求 |

分类元数据、alarm_alias、模板/模型关联和日志主题信息本来就在鲸眼配置中，见第 7 节。
两份都读不代表上述鲸眼字段改从监控平台取得，读取完整性与字段来源须分别验收。

2026-09-03，查询用途已由用户确认为“查询本次告警对应的观测数据”，来源原则与范围示例见
[迁移设计第 5.21 节](../design/alarm-callback-enrichment-migration.md#521-已确认指标查询参数定位本次告警的观测数据)。
本表的旧源码事实保持不变，S10/S11 的目标设计判断按该决策收窄；转换实现仍未验证。

### 11.1 不能直接宣称等价的转换

1. **下发不是简单字段复制**：TargetExecutor.format_kwargs_base_target 会追加目标相关条件和
   维度；DataExecutor 会转换周期、数据来源并处理 dataset；LogExecutor 会改写部分指标字段。
   字段来自原配置还是下发结果，按确定的来源规则处理；执行查询仍须核对有效查询语义。
2. **平台可能进一步转换**：base.domains.strategy.converter 中存在结果表、维度改写和
   origin_config 还原逻辑；不能用生成前的配置冒充某一版最终执行结果。完整引用下两份均读，
   也不表示所有字段都改用平台值。
3. **版本与时点不要混用**：当前 SC 内容不等于平台 version 对应的快照。最新规则要求两份读取，
   平台采用指定版本，鲸眼采用关联配置；同一字段两份均有值时已确认沿用旧消费来源。
4. **字段不足不是所有错误的统称**：字段缺失、策略记录不存在与查询故障必须区分。两份都读后
   仍要分别记录结果，不能将超时或权限失败描述为记录不存在，或默认为另一份可替代。
   用户已确认策略查询故障返回 partial，具体原因仍区分，见迁移设计第 5.23.6 节。

后续验收按最新规则区分：完整引用下即使鲸眼字段足够也读取平台；两份都在正确租户与作用域内
查询；指定平台版本或关联鲸眼配置任一缺失返回 partial 并保留有效结果。具体测试在实现阶段
落实，本轮没有执行这些测试。

### 11.2 补充源码索引

| 证据 | 文件与用途 |
| --- | --- |
| E18 | [StrategyConfig 定义](../../../../../kingeye/src/kingeye/base/candidacy/models/declaratives/v1alpha1/strategy.py)：StrategyItemSpec、BaseStrategySpec、StrategyDetectAlgorithmSpec、StrategyStatus/Labels |
| E19 | [BaseExecutor](../../../../../kingeye/src/kingeye/kmc/controller/executor/strategy_task/base_executor.py)：get_unit、build_items、init_kwargs_algorithms；这里只取转换证据，不在丰富中运行下发器 |
| E20 | [DataExecutor](../../../../../kingeye/src/kingeye/kmc/controller/executor/strategy_task/data_executor/__init__.py)：init_kwargs_query_configs、get_data_source_label、update_promql_kwargs |
| E21 | [TargetExecutor](../../../../../kingeye/src/kingeye/kmc/controller/executor/strategy_task/target_executor/__init__.py)：init_kwargs_query_configs、format_kwargs_base_target |
| E22 | [LogExecutor](../../../../../kingeye/src/kingeye/kmc/controller/executor/strategy_task/data_executor/log_executor.py)：日志 query_config 与 time_field 来源 |
| E23 | [ApmExecutor](../../../../../kingeye/src/kingeye/kmc/controller/executor/strategy_task/data_executor/apm_executor.py)：查询字段转换、APM 指标单位依赖 |
| E24 | [StrategyMetricService](../../../../../kingeye/src/kingeye/common/alarm_callback/strategy_metric_service.py)：single_metric_info、multiple_metric、get_metric_info；空 query_configs 与单指标的关系 |
| E25 | [策略转换](../../../../../kingeye/src/kingeye/base/domains/strategy/converter.py)：结果表/维度转换与 origin_config；该证据不能替代指定监控平台版本表的读取协议 |


### 11.3 旧指标查询条件的实际合并行为

2026-09-03，核对 Kingeye 5597beae82d42b7c732bf4cc10f7cffeaaf44672；旧仓库工作区干净。
实际调用由 E04 的 BaseConverter.get_metric_query_params（628）导入 E12 的
kingeye.kac.alarm_callback.handle_alert_info.HandleAlertToGraphPanel，最终返回首个 target.data。
仓库还有 common 等同名副本，本节以实际 KAC 导入路径取证。

普通非 Prometheus 查询在 get_graph_panel（693–726）中先从告警维度筛出本查询
agg_dimension 中的字段，再调用 create_where_with_dimensions（523–578）合并 agg_condition。
此前 get_dimensions（581–606）还会移除 tags.、忽略 bk_host_id 与非真值，系统指标补 IP/云区域
键映射；这些属于旧行为记录，不据此覆盖已确认的新输入职责和维度缺失处理。

合并函数先把告警维度转换为 eq 条件，再按 or 将策略条件分为多个 AND 组：

- 主循环内，组条件匹配当前维度时，移除该组中与告警维度同键的条件，再追加告警维度等值条件；
  不匹配时保留原条件并追加告警维度。组内条件引用的字段不在维度中时，旧匹配器默认该条件匹配。
- 循环末尾残留组只有匹配时才追加；没有生成任何条件时回退到告警维度等值条件。
- 没有“识别查询范围冲突、省略查询并标记 partial”的分支。不能把旧方法概括为一律覆盖同键，
  也不能据其表面组装流程声称所有 OR 场景都保持交集语义。

为避免加载 Django 与应用服务，使用 AST 提取原文件的类、函数和静态映射，在内存中直接调用
原 create_where_with_dimensions。以下五个样例验证了纯条件函数输出；没有调用指标查询服务，
不代表完整告警链路或实际查询结果通过验证。

表中 A/B/C 为普通维度 disk 的示意值，IN 表示旧 eq 的多值条件；每次调用复制输入以隔离样例。

| 输入策略条件 | 告警维度 | 实际生成的条件 |
| --- | --- | --- |
| disk IN {A, B} | disk=A | disk=A |
| disk IN {A, B} | disk=C | disk IN {A, B} AND disk=C，仍返回参数 |
| 无条件 | disk=A | disk=A |
| disk=A OR disk=B | disk=B | disk=A OR disk=B OR disk=B |
| disk=A OR disk=B | disk=C | disk=A AND disk=C；末尾未匹配的 B 组未追加 |

第四例暴露了对象共享问题：default_condition 的字典被多个条件组复用，后续给组首条件设置
condition=or 时，也改写了前一组已追加的同一字典。因此告警维度为 B 时，生成条件仍包含 A。
这是原纯函数样例可复现的范围扩大；本次没有修复 Kingeye。

2026-09-03，用户确认 OR 问题保留并在迁移实现中增加代码注释，其余条件合并行为依旧迁移。
具体要求见[迁移设计第 5.21.4 节](../design/alarm-callback-enrichment-migration.md#5214-已确认沿用旧条件合并保留并注释-or-问题)。
此前提出的“条件取交集，冲突则 partial”不采用；已经确认的“必要定位维度缺失且无法补齐时
partial”仍以迁移设计第 5.21.3 节为准。上表作为后续输出对照基线，不代表 Go 实现已完成。

## 12. 监控平台策略版本读取的取证与缺口

2026-09-03，继续按本文源码基线核对平台版本读取。仅检查本地源码与测试夹具，未访问监控平台
数据库、部署配置或业务接口。随后用户确认 alarm_strategy_v2 与 alarm_strategy_history
就是所指的监控平台策略读取目标，见
[迁移设计第 5.23.7 节](../design/alarm-callback-enrichment-migration.md#5237-已确认监控平台策略读取目标表)；
用户进一步明确 bk_strategy_history_id 为历史表主键 id，并确认按 id 与 strategy_id 联合定位，
直接读取 content 作为平台策略结果。主表没有 history_id 是用户认定事实，不再继续查表验证。

| 本地证据 | 能确认的事实 | 不能据此推断的内容 |
| --- | --- | --- |
| 14 条 origin_alarms 的 strategy.version | 均为字符串 "v2"；本次仅统计该字段 | 不是某个修订版本号的生产样本，也不决定 Linkd 标签的数据类型 |
| Strategy.version 与 to_dict | 类属性固定为 "v2"，序列化为 version | 不能用这一固定值区分同一策略的多次修改 |
| StrategyModel | 映射 alarm_strategy_v2，所查看的模型未声明修订 version 字段 | 不能据此直接设计 WHERE id = ? AND version = ? |
| StrategyHistoryModel | 映射 alarm_strategy_history，含 strategy_id、create_time、content、operate、status 等；初始化迁移声明 id 为 BigAutoField 主键 | 用户已确认主键对应版本并直接读取 content，暂不考虑操作状态，不新增 status 筛选 |

源码证据：[策略模型](../../../../../kingeye/src/kingeye/base/domains/strategy/models.py) 第 16–21、263–371 行；
[策略对象与历史保存](../../../../../kingeye/src/kingeye/base/domains/strategy/strategy.py) 第 1733、1875、2041 行。
模型管理器可在 use_old_model 配置下使用旧监控数据库连接；当前未核对部署选项，不能仅凭
类位于 Kingeye 仓库就断言这些表运行在鲸眼自有库。用户已确认逻辑读取目标，运行环境的连接
和租户隔离条件仍须落实。

另查 Strategy.save：第 2041 行在保存当前配置及子配置之前将 self.to_dict() 写入历史 content；
创建成功后会补 history.strategy_id，并在第 2136 行标记 status=True。不能据表名或成功状态
直接认定每条历史内容都包含最终下发后的完整数据；对应字段完整性须随消费矩阵验证。

既有主键证据见[初始化迁移](../../../../../kingeye/src/kingeye/base/domains/strategy/migrations/0001_initial.py)
第 143–157 行。用户已纠正此前关于主表 history_id 的推测，认定主表没有该列；不再保留
“部署库待核实”事项，也不增加该列。此结论来自用户明确认定，不表述为本轮查库验证结果。

平台内容读取已确定为：历史 id = bk_strategy_history_id 且 strategy_id = bk_strategy_id，
取匹配记录的 content，并校验 content.bk_biz_id 与 Event.Labels.bk_biz_id 一致；业务缺失、类型无法解释
或值不一致时，该平台历史结果不可用，按 partial 处理。历史表自身没有 bk_tenant_id，数据库连接的租户
选择仍由 Linkd 依赖装配保证。不改读当前主表配置，也不与鲸眼 StrategyConfig 合并。固定 "v2"、
操作时间或最新版不能代替历史主键。用户已确认暂不考虑操作状态，不因 status=false 拒绝
匹配记录或生成额外降级诊断。后续继续明确内容字段缺口与数据库连接隔离语义。
