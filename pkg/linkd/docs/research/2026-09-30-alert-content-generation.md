# Linkd 告警内容生成迁移调研（第一版）

> 调研日期：2026-09-30。初始快照：Linkd/alarmd `ac1331de`、bk-monitor `51c834dc6`；后续补充核对 Go alarmd `origin/master@8b4c5622` 和 Kingeye 实现。下文保留初始发现并明确修正；生产配置 Reader/Resolver/路由已接通，304 个源码模板样本及 11 条真实 Kafka 消息的本地 Alert 入库对照已通过。缺少检测事实按用户要求记录并跳过；全场景及线上端到端验证未完成。最新状态以实施草案进度章节为准。

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

策略版本绑定和生成规则见[告警内容设计](../design/alert-content-generation.md)。
