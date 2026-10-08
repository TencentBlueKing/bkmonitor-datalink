# Linkd 告警内容生成迁移调研（第一版）

> 调研日期：2026-09-30。初始快照：Linkd/alarmd `ac1331de`、bk-monitor `51c834dc6`；后续补充核对 Go alarmd `origin/master@8b4c5622` 和 Kingeye `e34c323d`。下文保留初始发现并明确修正；生产配置 Reader/Resolver/路由已接通，304 个源码模板样本及 11 条真实 Kafka 消息的本地 Alert 入库对照已通过。缺少检测事实按用户要求记录并跳过；全场景及线上端到端验证未完成。最新状态以实施草案进度章节为准。

## 目标和结论

目标是让 Linkd 在新 Alert 创建时，根据 bk-monitor 生成 Event `description` 的规则，生成 `Alert.content`；范围为当前 Linkd 内置 Enrich 已覆盖的告警场景。alarmd `TriggerEvent` 不增加内容字段。通知渠道拼出的“新告警”“已持续”等最终文案不在本次对齐范围。

**可行的结构位置是 Lifecycle 创建新 Alert 时的 Enrich 准备阶段。** 内容由一个只读、确定性的构建过程计算，随后作为新 Alert 的初始 `content` 一次性写入。已存 Alert 的 `content` 仍不可变，后续 Enrich 运行及其结果只写 `enrich_status/enrich`，不覆盖任何已有核心字段。opening Event 的 `content` 和 `source_raw_data` 继续保留来源事实，不能被生成过程改写。

**现有字段不足以承诺所有场景与 bk-monitor 逐字一致。** 部分算法文案依赖触发时策略快照、历史比较值或专用检测事实；当前 alarmd 输出和 Linkd 标准 Event 并未完整携带这些材料。不修改 alarmd 输出的前提下，必须先核实其他可按稳定身份读取的事实来源，才能为每个场景给出一致性承诺。

## 代码证据

| 链路 | 当前事实 | 源码位置 |
| --- | --- | --- |
| bk-monitor 内容源 | 检测器用 `desc_tpl`、检测上下文及数值/单位生成 `anomaly_message`；组合逻辑拼接指标名、多算法结果和当前值。`MonitorEventAdapter.adapt` 将选中级别的消息写入 Event `description`。 | `bkmonitor/alarm_backends/service/detect/strategy/__init__.py:108-189`，`core/alert/adapter.py:126-183`；阈值样本见 `tests/service/detect/test_threshold.py:81-94` |
| bk-monitor 后续使用 | Alert builder 从 Event 创建或更新 Alert；通知上下文再单独叠加持续时间或恢复原因。Linkd 若保持不可变内容，应只对齐新 Alert 的 opening Event 描述。 | `bkmonitor/alarm_backends/service/alert/builder/processor.py:345-400`，`core/context/alarm.py:598-615` |
| alarmd 输出 | `TriggerEventV1` 含 `plan_ref`、观测值、维度、等级判定和检测证据，没有 `content/description`。输入策略 IR 虽有 `legacy_json_b64`，该完整快照未进入 TriggerEvent。 | `pkg/alarmd/contract/v2_model.go:345-446`，`contract/v2_outputs.go:20-67`，`contract/strategy_ir.go:27-40` |
| Linkd 接入 | `standard` Cleaner 将 payload 的 `content`、`evaluations`、`values` 转为 Event 来源事实；创建 Alert 时目前直接复制 Event `content`。未见当前 Linkd 直接解码 alarmd `TriggerEventV1` 的正式 Cleaner。 | [raw_event.go](../../internal/cleaner/raw_event.go)，[plan.go](../../internal/lifecycle/plan.go)，[standard-event.md](../reference/contracts/standard-event.md) |
| Linkd Enrich | 当前已有 `internal/enrich`：按 EventSource 路由 Processor Chain，策略、资源、展示、指标及专用日志/Cloud/K8s/APM 处理器均已注册。`Display.Process` 当前从 `alert.Content` 出发，生成展示内容，尚不是独立的原始告警描述构建器。 | [router.go](../../internal/enrich/assembly/router.go)，[display.go](../../internal/enrich/processors/display.go)，[enrich.md](../design/enrich.md) |
| 写入语义 | `enrich.Result` 只含 Status/Data，Lifecycle 只写 `Alert.EnrichStatus/Enrich`。补丁可生成读取时的临时展示视图；`ValidateAlertReplacement` 禁止已创建 Alert 的 `content` 被 CAS 改写。 | [types.go](../../internal/enrich/types.go)，[enrich.go](../../internal/lifecycle/enrich.go)，[view.go](../../internal/enrich/view/view.go)，[alert.go](../../internal/domain/alert.go) |

当前[模型定义](../design/define.md)和[Enrich 设计](../design/enrich.md)把 `Alert.content` 描述为 opening Event 继承事实。这与本次“Linkd 生成初始内容”的目标有差异：实施时应只调整**创建来源**的描述，保留创建后的不可变校验，以及“Enrich 结果不覆盖已有数据”的约束。现有 `display.content` 补丁只作用于有效视图，不应被误认为已修改持久化 `Alert.content`。

## 场景覆盖和数据缺口

当前[Enrich 设计](../design/enrich.md)与分类代码覆盖 BaseTarget 六种分支、DATA、LOG_METRIC、LOG_KEYWORD、UptimeCheck、Cloud，以及 K8s/APM 专用丰富。这里的“覆盖”指代码中有分类或 Processor；真实来源启用、外部依赖命中和端到端回放仍需逐项核验。

| 场景 | 重建 bk-monitor 描述所需的主要事实 | 当前判断 |
| --- | --- | --- |
| DATA 阈值与多条件 | 触发时策略算法/连接符、指标展示名、当前值、单位、精度、选中级别 | Linkd Event 有 `values/evaluations`，Enrich 可读当前策略和指标资料；若策略已变更或算法命中细节缺失，仍不能保证逐字一致。 |
| DATA 环比、同比、预测与无数据 | 上述资料加历史比较点、预测输出或无数据周期/延迟事实 | bk-monitor 文案会写入这些具体值；当前 TriggerEvent 只有部分检测证据，Linkd Event 的数值快照也不能凭空还原历史事实。 |
| BaseTarget、UptimeCheck、Cloud | 场景分类、策略/对象及该类型实际检测结果 | 现有处理器主要补资源和展示信息；仍需按真实输入逐类确认描述所用检测事实。 |
| LOG_METRIC、LOG_KEYWORD、K8s、APM | 日志查询/命中、指标或服务身份及对应检测结果 | 现有 Display 有基于来源文本的裁剪与拼接；要避免复用来源 `content`，需补结构化事实及专用黄金样本。K8s/APM 处理器本身不等于内容生成器。 |

以上是待验证矩阵，不把 alarmd 的完整 `TriggerEventV1` 直接等同于 Linkd 标准 Event，也不从旧中文文案反向解析检测结果。单凭当前策略 ID 查询最新配置，会造成触发时与 Enrich 时策略版本漂移；查询实时指标代替当次历史比较值也不能满足回放确定性。

## 建议方案与验收

1. **先固定事实清单。** 从已启用的 EventSource 和脱敏真实消息确认实际覆盖场景，以及 alarmd 事件进入 `standard` Event 的转换位置。逐场景列出所需配置、算法命中与历史事实、稳定身份及可读取位置；以 bk-monitor 同批事件的 `description` 和现有单测作为黄金样本。
2. **新增创建前内容构建入口。** 在 `internal/enrich` 内提供只读内容构建模块，由 Lifecycle 在新 Alert 构造阶段传入 opening Event（含 `values/evaluations`）和待创建 Alert 的租户、级别、策略身份等信息。优先复用现有 Scope/DataSource 读取能力，但要求按触发时版本取得策略/指标资料；构建结果用于初始化**新** `Alert.content`，不修改 Event，不经 `enrich.Result` 或补丁覆盖 Alert 核心字段。
3. **保留现有 Enrich 契约。** 初始化后照常运行 Processor Chain，只写 `enrich_status/enrich`。检查 `display.content` 与新核心内容是否一致；必要时让 Display 使用同一构建结果或仅做展示投影，避免读视图和持久化 Alert 出现两套互相矛盾的文本。更新模型和 Enrich 文档中“始终从 Event 复制 content”的一句话，但不放松不可变规则。
4. **按事实可用性分批对齐。** 对无法取得触发时快照或历史判定事实的场景，先标记为未达成逐字一致，不能把旧 `content` 原样复制后报告迁移成功。明确缺失事实时的诊断和告警创建策略；当前 Enrich 失败会创建 Alert 且没有后台补丰富，不能把暂时性读取失败当成自动可恢复。
5. **验证。** 对每个实际启用的类型运行 bk-monitor/Linkd 同输入对照，覆盖正常、边界、缺字段、依赖失败、跨租户、重投及策略版本变化；断言新 Alert 的核心 `content`、读取视图和输出一致，且旧 Alert 的 `content` 不能被 Enrich 或生命周期更新改写。

## 未决事实

- 当前生产使用的 alarmd→Linkd 转换位置和 EventSource 配置，及真实启用的告警类型清单；本地代码不足以直接证明两者已按 `TriggerEventV1` 连通。
- 触发时策略快照、历史/预测/无数据判定事实是否已有 Linkd 可读取且有保留期保证的存储。若没有，保持 alarmd 输出不变时，相关文案无法证明全量一致。
- 内容构建依赖失败时，是阻止新 Alert 创建并重试，还是创建带明确降级状态的 Alert；当前 Enrich 降级语义不能自动补算已创建内容。

初始调研未改动 Go 实现，未执行跨系统事件回放；后续只读查询实现与核验见下节。可行性判断仅针对代码中的构建位置和已观察到的数据边界。

## 2026-09-30 实施核验补充

- 已只读核验 `test-bkee5` 环境，`kingeye` schema 中有 488 条 StrategyConfig、112 条 StrategySet；StrategyConfig 的租户均为 `system`。本次扫描全部 488 条配置得到 473 个 `Threshold` 算法条目，其余配置无算法条目；这仅描述该环境当前配置，不缩小 Linkd 支持场景的验收范围。
- 用户明确 `alarm_strategy_history` 已废弃。后续只沿 StrategySet / StrategyConfig 关系核验策略内容；`core_strategy_config_revision` 为 Kingeye 的不可变声明式配置快照表，但不是本次选定的数据来源，试接代码已撤掉。
- 代码关系：Kingeye `StrategyConfig.kind = Strategy`，对应 Linkd `internal/enrich/datasources/cw_strategy_client.go` 中的 `core_v1alpha1_strategy`。StrategySet 对应 `core_v1alpha1_strategyset`；其内嵌配置 `id` 被写入派生 StrategyConfig 的 `config_id`，模板身份写入 `monitor_template_id`。本次新增 StrategySet Reader，按此关系增加只读查询和精确配置选择；不能把它与配置修订快照表混用。`TestStrategySetClientLiveIntegration` 已使用指定环境验证 Set/Config 关联及不存在租户无法读取，普通单元测试不会隐式连接线上。
- ES 告警 Event v4 索引有数据，另取得 500 条 Enrich preview 原始告警。抽查原始字段结构以旧 KAC 告警字段为主，尚未从该样本证明 alarmd TriggerEvent 转换、历史比较值或预测证据的持久读取链路。只检查字段结构与计数，未将完整 payload 写入仓库。
- 初次实施核验时仅实现 StrategySet Reader。后续已增加纯内容渲染器、数值/单位规则与 Lifecycle 创建前的可注入构建入口；生产 Resolver/路由尚未接通。单位转换使用真实 bk-monitor Python 实现导出的 fixture 验证；完整描述的跨系统对照和真实来源启用仍未完成。

### 用户指定 Kafka topic 的补充结论

- 已只读抽样 `test-bkee5` 的 `alarmd_event`，手动指定分区与 offset，`group_id=None`、`enable_auto_commit=False`，没有生产消息或提交消费进度。topic 有 1 个分区；多个时点分别读取最多 60 条，未全量扫描。
- 线上消息包含 `bk_tenant_id/event_id/alert_id/title/content/values/evaluations/dimensions/labels/extra_data`；部分消息含 subject。它符合当前 `StandardCleaner` 的字段结构，与本地 alarmd v2 `TriggerEventV1` 不同。初始调研中“没有 content”“无法直接接 standard”的结论仅适用于本地 v2 契约，不能代表该线上 topic。
- `values.value` 为数字；`labels.strategy_id/strategy_version` 为数字；`evaluations` 有多级别；扩展字段含 `evaluation_family=metric_algorithm`、`signal_type=metric|log`、窗口摘要和部分首次异常时间。样本未发现单位、历史比较点、预测上下界或命中算法列表。没有采样到不等于整个 topic 永远没有。
- 一批 60 条中去重后的 14 个策略引用，在 `kingeye` schema 均能按租户和 `status.bk_strategy_id` 找到 StrategyConfig，再按模板和 ConfigID 精确关联 Set。对应 Config 均 inactive，Set 均 active。现有 Linkd Reader 没有过滤 active；初始探针的 active 条件造成了误报缺失，已纠正。不能由低位 ID 或关联成功推断这是当前下发策略。
- Event 的 `strategy_version` 长整数与关联配置的 `strategy_config_version=1`、`updated_at` Unix 微秒值均不相等。不能复用新修订表或废弃历史表处理该差异；需要取得部署的策略读取/版本生成入口后验证当前 Set/Config 是否与该触发版本一致。完整字段路径、实施顺序与限制见[实施草案](../design/alert-content-generation.md#alarmd_event-实际输入核验与下一步)。

### 排除指定消息后的重新抽样

用户要求忽略 partition `0` / offset `721407`，已将该条排除，不再作为主要实施样本。后续只读抽样覆盖保留范围头部、中部和三个近期窗口，共 200 条，均未提交 offset。较早的 80 条使用 `2/3/4` 版本，近期 120 条使用长整数，后者涉及多个策略，不能将整个 topic 描述成单一版本形态，也不能由整数位数判定消息异常。

从较早消息选取六个不同策略核验：`395/4`、`482/3`、`496/2`、`370/3`、`351/3`、`489/2`。它们均唯一命中同租户 StrategyConfig，Config 与关联 StrategySet 均 active，Set 内 ConfigID 均精确匹配。场景包含系统主机指标、日志关键字和 K8s 指标，单位包含 percent、percentunit、空串和缺省；六份配置当前均为 Threshold。消息版本与当前 `status.strategy_config_version=1` 仍不同，因此版本字段语义独立于数字大小，不能直接互换。

后续优先使用 partition `0` / offset `579063`：event_id `2038b0928b2cad7ff7190b1039c9cab417a01dcfe59720e9387122f92014ecd3`，`labels.strategy_id=395`、`labels.strategy_version=4`，事件时间 `2026-09-28T15:06:05Z`。完整六条定位与关联信息见实施草案。此次只验证输入形态和当前配置关系，尚未证明触发时版本等价或完成文案对照。

### Go alarmd 二阶段源码补查：修正版本与转换入口

用户确认本次应查 `bkmonitor-datalink/pkg/alarmd`。此前仅以 `ac1331de` 工作树源码判断输出结构，遗漏了同仓库 `origin/master` 已合入的二阶段实现。补查以 `8b4c5622` 为快照，相关转换代码在 `8c3c859f` 已存在；没有切换当前 Linkd 工作树，没有修改 alarmd 输出。

- `linkdoutput.Converter` 已将内部 TriggerEvent 转成线上观察到的 standard 字段，并由 Kafka sink 输出。因此初始表格中“没有 content / 没有 standard 转换”仅描述早期 v2 结构，不代表新版 Go alarmd。
- 正式进程装配 `controlplane.LegacyRedisStrategySource`，读取策略缓存文档。`catalog.go` 将文档 `strategy_revision` 放入整数 `StrategyRefV2.SnapshotRevision`，触发器复制为 `TriggerEventV1.StrategyRef.Revision`，Converter 写入 `labels.strategy_version`。alarmd 不在这里读取 Config status 或生成递增号。
- `plan_ref.strategy_revision` 是另一条字符串字段，来自策略 `update_time`（缺省/零时是规范化摘要）。之前“核对这两个版本是否相等”的方向不准确，应该核对 labels 与实际下发文档的整数修订，以及下发文档绑定的 Set/Config。
- Kingeye 旧修订投影使用 `head.strategy_revision`，首次 1、变化加一；Set 拆分投影使用 `source_resource_version`，正式 Controller 取 Set 更新时间的 Unix 微秒。当前发布器沿拆分记录发布。两种版本形态都有代码来源，不按位数判错；尚未逐条证明既有消息来自哪条历史路径。
- Set 投影输出 ID 为 `projection_record.pk`。旧样本能按 Config `status.bk_strategy_id` 关联，并不证明新投影 ID 也适用同一查询；后续需核对同租户的 Set UID / Config UID 绑定。内容权威继续使用用户选定的 StrategySet 与 StrategyConfig，不读取 `alarm_strategy_history`，不引入配置修订表回退。

完整链路、源码行号、身份端口及版本校验的实施修正集中在[实施草案](../design/alert-content-generation.md#go-alarmd-策略版本的实际来源)。

### 发布绑定的进一步只读验证

对同环境前 4 个 active/published `alarm_strategy_set_split_record` default 记录按租户读取，均精确命中同 Set UID、模板、ConfigID；冻结 `payload.strategy_config` 与当前 Set 对应配置完整相等。冻结 resolved spec 与当前唯一 active StrategyConfig 的差异均位于 targets 字段，路径和版本值记录在[实施草案](../design/alert-content-generation.md#发布身份读取的落地步骤)。此次查询使用只读一致性事务，不读取历史表，不提交线上变更。

此前与本轮读取的记录版本已发生变化，说明 current 发布材料不能自动回答任意旧 Kafka 消息的版本。此前读到当前 Set 更新时间晚于 split 来源版本，也不能单独证明描述发生变化。生产校验应按逐算法的完整渲染依赖验证；尚未确认哪些 targets 字段可排除，也没有完成旧版本和 override 的实际关联验收。

后续已接入真实配置 Reader/Resolver：default 发布身份沿租户、Set UID、模板、Config UUID 精确绑定；事件业务、resolved 业务和资源业务不要求相等。新增源码 oracle 对照与真实 Kafka → 本地 Alert 检查均已通过。2026-09-30 用户确认缺失数据记录后跳过，无数据/历史/预测等未验收项及恢复条件统一记录在[实施草案](../design/alert-content-generation.md#本轮跳过的数据与后续恢复条件2026-09-30-1748)。这不代表线上来源已切换，也不代替完整门禁。

18:00 补查发布器证明 target 正常路径只生成一份 resolved 配置，已完成跨业务单配置及异常输入的边界验证。override 虽有独立 ID，整数版本仍复用父记录，当前标准消息不能区分覆盖修改代次，因此按缺少冻结身份事实记录并跳过；不是通过默认配置替代。77 条有界 current 配置清单、Cloud 样本边界、各资源路径的内容保留测试以及 KAC 门禁代码/契约冲突集中记录在[发布分支核验](../design/alert-content-generation.md#发布分支核验与当前验收边界2026-09-30-1800)。

18:08 扩大抽样发现同 Config UUID 的多业务 current 投影，修正仅允许一行的读取假设，并保留重复业务/依赖不等价/超限拒绝。最新 20 条真实 Threshold 样本通过版本绑定、源码描述、本地入库与只读预览对照；配置依赖变化的失败样本单独记录，详情见[补充验证](../design/alert-content-generation.md#多业务-config-与真实预览补充验证2026-09-30-1808)。

18:36 修复已确认的 KAC 固定来源名实现偏差，完整 `make check` 通过。补齐 Cloud Kind 白名单和冻结云字段校验，并修复“Runtime Block 后中心自动重启”的运行缺口：新增持久暂停与确切代次的显式恢复接口。工具版本、测试结果和实际验证边界见[最新验证](../design/alert-content-generation.md#完整门禁与确定性失败恢复2026-09-30-1836)。缺少稳定检测事实与真实场景样本的项目继续按用户要求记录并跳过，未发布线上来源配置。

18:44 在真实隔离 Redis 上完成失败 Signal、持久暂停、管理恢复、新 epoch 接管和按原队列顺序生成 Alert.content 的串联测试。再次按正确的 metric_source 字段有界抽样，12 条 Threshold 样本全部通过源码描述、本地入库和只读预览对照，其中 2 条明确为 kapm。LOG_METRIC、Ping、Cloud 的真实样本仍未取得；详情和本轮完成范围见[补充验收](../design/alert-content-generation.md#暂停恢复串联验证与-apm-样本2026-09-30-1844)。步骤 4 的线上来源切换与生产验收单独保留，未执行。
