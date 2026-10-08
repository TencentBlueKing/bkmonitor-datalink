# Alert.content 在创建阶段生成的实施草案

> 状态：本轮可用事实范围的实现与验证已完成；全类型线上验收和来源切换尚未完成。配置 Reader、FactsResolver、创建前入口、content_mode、Display 内容保留、opening Event 预览以及确定性失败暂停/恢复已接入。Threshold/Ping 已完成源码模板对照；先后两次共 32 条真实 Threshold 消息通过版本绑定、描述对照、本地 Alert 入库/重投及只读预览检查，含 target、data、日志关键字、K8s 配置上下文及明确的 APM 样本。真实 Redis 上的失败暂停、管理恢复和队列继续处理验证通过。缺少稳定检测事实或真实样本的组合按用户要求记录并跳过。KAC 固定来源名的实现偏差已修复，完整 `make check` 通过；默认 source 模式继续复制来源内容。调研日期：2026-09-30；Linkd 工作树基线 `ac1331de`，alarmd 二阶段补充核对 `origin/master@8b4c5622`（含 `8c3c859f`），Kingeye `e34c323d`，bk-monitor `51c834dc6`。未切换工作树或修改 alarmd。源码证据及现状见[调研记录](../research/2026-09-30-alert-content-generation.md)。以下历史执行记录保留当时结果，以最新验证小节判断当前状态。

## 目标、边界与验收定义

- 对已配置启用的 bk-monitor/KAC 来源，**新建** Alert 的 `content` 由 Linkd 在 Enrich 准备阶段按 bk-monitor 检测事件 `description` 规则生成。以同一触发事实和同一策略版本下的 bk-monitor `Event.description` 为对照；不把通知阶段添加的“新告警”“已持续”等文字计入对照。
- 覆盖 Linkd 当前内置 Enrich 能识别的告警场景；以实际 EventSource 和真实输入清单界定可交付的具体组合。`DATA`、`LOG_METRIC`、`LOG_KEYWORD`、BaseTarget 的六个分支、UptimeCheck、Cloud 均须列入矩阵。K8s/APM 是资源和展示上下文，须与其实际检测类型交叉验收，不能视为独立的描述算法。
- `alarmd.TriggerEventV1` 输出保持原样。opening Event 的 `content` 和 `source_raw_data` 仍是来源事实。已创建 Alert 的 `content` 不允许被后续 Enrich、Lifecycle、重投或修复任务改写；现有 `ValidateAlertReplacement` 约束保持有效。
- `enrich.Result` 继续仅返回 `enrich_status/enrich`。只在构造**尚未持久化**的新 Alert 时赋一次核心 `content`。来源不采用 bk-monitor 对齐模式时继续用 `event.Content`，且必须是 EventSource 明确配置的行为。

“一致”按字节级字符串对照，包括算法组合顺序、连接词、空格、数值精度、单位、指标名和空值语义。对必须依赖已丢失的历史检测事实的组合，不能用实时查询、当前策略或解析旧中文 `content` 伪造一致性；该组合在事实补齐前不得宣称验收通过。

## 当前链路与目标链路

当前与 Event 前置丰富统一后的实现先冻结 Event 丰富，再进行策略准入；只有实际新建的 Alert 才在
`planNewAlert` 中复制 opening Event 的选中等级结果并调用内容构建器，之后保存 EventPlan。
`releaseEnricher` 为丰富和内容构建读取同一 EventSourceVersion，不用当前发布覆盖历史积压。

```text
Event → 前置 Processor Chain → 持久化 Event.enrich
    → 抑制与生命周期选择 opening Event / EventEvaluation
    → 复制选中等级的丰富快照到新 Alert
    → 只读内容事实收集与 bk-monitor description 渲染
    → 初始化新 Alert.content 一次
    → 后续策略与持久化 EventPlan → Alert CAS、日志和 Hook
```

同级别连续触发和 `update_current` 级别升级不重新生成内容，因为仍是同一个 Alert；`close_and_create` 升级会创建新 Alert，应以该新 Alert 的 opening Event 和选中级别重新生成。所有输出端读取持久化 `Alert.content`，不能把 `display.content` 的临时补丁误当成核心字段已更新。

## 实施接口与依赖位置

在 `internal/lifecycle` 消费侧定义窄接口，具体实现在 `internal/enrich`，由进程装配层显式注入：

```go
type AlertContentBuilder interface {
    BuildContent(ctx context.Context, event domain.Event, evaluation domain.EventEvaluation, opening domain.Alert) (string, error)
}
```

接口参数中的 Alert 是尚未持久化的副本，仅供读取租户、来源、级别和标题等上下文；返回值是唯一允许写入新 `Alert.content` 的结果。`planNewAlert` 构造其余字段并复制已冻结的 Event 丰富后调用 `BuildContent`，校验结果后冻结计划，不再重复执行 Enrich。不要扩展 `enrich.Input/Result` 来返回核心字段，也不要让普通 Processor 获得修改 Alert 的能力。构造函数增加必需依赖或等价的显式装配校验，避免静默使用 `event.Content` 作为 bk-monitor 来源的回退。

内容路由按 EventSource 发布配置选择 `source` 或 `bkmonitor_description`。可在 `EnrichConfig` 下增加明确的 `content_mode`，并纳入配置校验、克隆、发布摘要、动态配置和预览；已启用来源逐一配置，避免凭 `strategy_id` 或来源名称猜测。`source` 仅复制来源内容；`bkmonitor_description` 使用下述事实收集与渲染模块。配置上线前须确认 EventSource 版本与既有未处理 Event 的关系：若处理时配置可能变化，应让事件记录或路由能复用其 `EventSourceVersion`，否则禁止在同一 backlog 中直接切换模式。

`internal/enrich` 内拆成两层：

1. **FactsResolver**：将 opening Event、选中级别、触发时策略配置和检测证据整理为类型化 `DescriptionFacts`。通过既有 `Sources/Scope` 可复用租户校验、策略及指标读取，但不能复用“只取当前策略”的语义。外部 I/O 接受 `context.Context`，有超时和数量限制。
2. **Renderer**：按检测算法分派纯函数模板，输入完整事实后输出字符串和规则版本；无外部 I/O、无当前时间、无随机数。保留 bk-monitor 的空格、中文连接词、精度、单位换算、指标前后缀及多算法顺序，按黄金样本固定行为。

不要用来源 `content` 反向解析阈值或历史值。`source_raw_data` 只有在转换契约明确、字段有类型校验且具备保留期时才可作为检测事实；来源原文不参与渲染规则的事实补全。

## 数据契约前置工作

已按用户指定只读抽样 `test-bkee5` Kafka 的 `alarmd_event`：消息使用 `content/evaluations/values/labels` 格式，可由现有 `standard` Cleaner 提取，无需新增 v2 Cleaner。Go alarmd 二阶段 `linkdoutput.Converter` 已实现该标准输出，见下节版本追踪；最初只读较早的 `ac1331de`，遗漏了这一转换入口。该 topic 的正式 EventSource 发布配置及线上生产者提交仍须核对。为每个样本记录租户、来源与发布版本、策略身份、选中级别、观测值与单位、历史/预测/无数据证据、指标展示信息及保留期。样本须脱敏。

| 所需事实 | 当前可用性 | 实施要求 |
| --- | --- | --- |
| 租户、来源、opening Event、级别 | Linkd Event/Alert 已有 | 校验三者身份一致；多级别只用当前新 Alert 对应的 `EventEvaluation`。 |
| 当前观测值 | Linkd Event `values`、alarmd `observed.values` 有部分值 | 明确指标键、数值类型、空值和单位；禁止把缺失当 0。 |
| 算法与命中信息 | KAC 策略 `spec.strategy_detect_algorithms` 有配置；alarmd 检测证据只有部分命中 ordinal | 建立检测算法与 bk-monitor 模板的显式映射，确认各级别、各组组合和命中顺序可还原。 |
| 触发时策略 | `labels.strategy_version` 来自 alarmd `StrategyRefV2.SnapshotRevision`，不是 `plan_ref.strategy_revision`；现有 `CWStrategyReader` 只按租户和 ID 取最新记录 | 区分旧策略修订和 Set 拆分投影版本，核对策略 ID 的身份空间；内容仍沿 Set/Config 读取，取得可验证的触发时绑定。版本不符不能使用最新配置替代。 |
| 历史、预测、无数据、日志检测事实 | 当前 `TriggerEventV1` 与标准 Event 未完整携带 | 查找既有持久事实源，必须可由租户 + 稳定记录/计划身份定位并覆盖消费重试期；若不存在，需先在 Linkd 可读的上游持久化/转换环节补事实，保持 alarmd 输出不变。 |
| 指标名称、单位、映射、精度 | Enrich 有策略和指标资料读取能力 | 逐项确定是否依赖触发时配置；会变化的资料要快照化或有版本校验。 |

这一步的交付物是“场景 × 检测算法 × 所需事实 × 来源 × 样本”矩阵。2026-09-30 用户确认：没有的数据先记录文档并跳过。缺少稳定事实源的组合因此列为**本轮跳过**，不要求先补上游数据才能继续其他验证；这不代表运行时支持或验收通过。在 `bkmonitor_description` 中遇到这类组合仍明确 Block，不回退来源 content。后续补齐事实再恢复验收，不修改 `alarmd.TriggerEventV1`。

### 策略数据核验结论

2026-09-30 实施核验确认 `kingeye` schema 中存在 `core_v1alpha1_strategy`（488 条，租户均为 `system`）和 `core_v1alpha1_strategyset`（112 条）。按用户确认，策略内容沿 **StrategySet → StrategyConfig** 读取：StrategyConfig 对应现有 Linkd `CWStrategyReader` 读取的 `core_v1alpha1_strategy`；StrategySet 对应 `core_v1alpha1_strategyset`，通过同租户的 `monitor_template_id` 与 `spec.strategy_configs[*].id = StrategyConfig.config_id` 关联。`alarm_strategy_history` 已废弃，不作为本任务任何读取、回放或降级来源；`core_strategy_config_revision` 不是本次选定的数据源，试接的读取代码已撤掉。

上述两张表是当前声明式配置。`status.strategy_config_version`、Event `labels.strategy_version` 与 alarmd `plan_ref.strategy_revision` 属于不同字段体系，具体来源已从二阶段代码查明，见下节。已保存的 EventPlan 可固定生成后的内容；计划保存前的旧事件仍需验证 Set/Config 与触发时配置的对应关系，不能据此宣称已支持触发时配置回放。

### Go alarmd 策略版本的实际来源

本节只核对 `bkmonitor-datalink/pkg/alarmd`。当前工作树 `ac1331de` 早于标准输出实现；同一仓库已有 `origin/master@8b4c5622`，以下 alarmd 行号以该提交为准，相关代码在 `8c3c859f` 相同。通过 Git 对象只读核对，没有合并远端或使用其他工作树；CodeGraph 的当前磁盘源码不能代表这个远端快照。

```text
Redis 策略缓存文档
  文档 strategy_revision（正整数）
    → controlplane legacyStrategy.SnapshotRevision
    → EvaluationPlanV2.StrategyRef.SnapshotRevision
    → trigger 构造 TriggerEventV1.StrategyRef.Revision
    → linkdoutput.Converter 写 labels.strategy_version
```

具体证据：`controlplane/legacy_redis_source.go:33-49,85-126` 读取 Redis 文档；进程在 `cmd/alarmd/runtime_phase_two_bundle.go:154-157,399` 装配该 Reader；`controlplane/catalog.go:1334-1339,1796-1800` 读取并校验 `strategy_revision`；`trigger/evaluator_v2.go:93-100` 冻结整数引用；`linkdoutput/converter.go:251-256` 将其写入标准 labels。alarmd 不在这个入口查询 StrategyConfig 的 status 或自行加一。

另一个容易混淆的字段 `StrategyRefV2.Revision` 是字符串：`catalog.go:1780-1796` 优先取 `update_time`，缺省/零时取规范化策略摘要，最终进入 `plan_ref.strategy_revision`。它与整数 `SnapshotRevision` 同时存在，不能互换。

Kingeye `e34c323d` 代码中有两条版本生成路径：

| 路径 | Redis `strategy_revision` | 策略 ID | 证据 |
| --- | --- | --- | --- |
| 旧 Strategy 的修订投影 | `head.strategy_revision`；首次为 1，内容变化时加一 | 旧 Strategy ID | `base/domains/strategy/revision_service.py:216-255`、`runtime_cache_projector.py:504-510` |
| StrategySet 拆分投影 | `max(1, split_record.source_resource_version)`；正式 Controller 从 Set `metadata.updated_at`（缺省用 created_at）转为 Unix 微秒整数 | `projection_record.pk`，默认/override 都是拆分记录身份 | `runtime_cache_split_projector.py:360-377`、`kmc/controller/resource_handler/strategy_domain/split_record_writer.py:422-433` |

因此小整数和长整数都存在合法的代码来源。上述代码解释了两种版本形态，但没有单凭数字大小证明某条线上消息使用哪条路径；仍需对应发布文档或冻结计划作证。正常样本的 `strategy_version=4` 与 Config 的 `strategy_config_version=1` 不构成错误证明；长整数也不应被判作坏消息。当前 Kingeye 发布器 `runtime_cache_publisher.py:164-189` 已沿拆分记录发布，不能把旧修订投影当成它的当前主路径。

对实施方案的具体修正：

1. 内容读取继续用 StrategySet 与 StrategyConfig，禁止读取废弃 `alarm_strategy_history`；不新增 `core_strategy_config_revision` 回退。
2. 新投影 `labels.strategy_id` 是拆分记录主键，不能仅凭数值相同沿用 `status.bk_strategy_id` 查询。需要按租户校验投影身份绑定到 Set UID、模板 ID、Config UID；`StrategySetSplitRecord` 模型已有这些绑定字段。该映射是待验证的辅助身份端口，不把拆分记录作为新的文案配置权威。
3. Set 路径校验的是整数版本对应的发布记录及其精确 ConfigID，不校验 Config status 版本等于 labels。`source_resource_version` 对应发布时读取的 Set，不要求当前 Set 更新时间仍等于它。若核对原始时间转版本，需复制 Python 的浮点乘微秒计算语义，不能未经对照直接替换成 Go `UnixMicro()`。override 必须按其真实来源绑定核对。
4. 在一次有界一致性读取中冻结渲染依赖，比较当前 Set/Config 与该版本发布材料中的完整渲染依赖。更新时间变化不能单独证明文案变化；渲染依赖变化或无法证明相等时停止旧事件生成。若要回放其他版本，需有选定的事实保留方案，不能自动切换到其他历史表。

只读补查六个样本对应 Set 的当前更新时间为 2026-09-30 UTC，与旧样本触发时间不同；仅有当前关联不能证明触发时内容相同。

### 发布身份读取的落地步骤

以下步骤中的只读事务、default 发布身份绑定、当前 Set/Config 比较已实现；override、历史事实读取和所有检测语义验证尚未完成。输入为 opening Event 的租户、`labels.strategy_id/strategy_version/bk_biz_id`，输出为已校验的渲染快照；不将 Config 的 status 版本用于比对。

1. 使用只读、repeatable-read 事务，按 `alarm_strategy_set_split_record.bk_tenant_id + id` 读取至多一行；校验 `source_resource_version == labels.strategy_version`、发布状态和身份。SQL 必须同时带租户，不能查询全租户后由业务层筛选。记录缺失时返回明确缺失，不按数值猜测旧 Strategy ID。
2. 校验记录的 `strategy_set_uid/monitor_template_id/config_uid` 与 payload 的身份一致。使用 Set UID、租户、模板精确读取 active StrategySet，按规范化 UUID 精确选择 `strategy_configs[*].id`。对 default 与 override 分别核对绑定；没有实现 override 映射时拒绝该分支，不能选默认配置冒充覆盖配置。
3. 按发布记录的业务与投影规则选择 `resolved_strategies`，再以租户、模板、ConfigID、default 标记定位 active StrategyConfig 候选。事件业务、resolved 业务和 Config 资源所属业务是三个不同上下文，不要求相等。不得以事件业务反查或替换配置。最新真实样本证明同一 Config UUID 可以有不同业务的多份投影：最多读取 33 行并拒绝超过 32 份；仅当业务身份互异且全部渲染依赖与同一冻结 spec 相等时接受，不挑选最新或任意一份。重复业务、身份不符、依赖变化或超限仍拒绝，细节见最新核验记录。
4. 对比冻结的 `payload.strategy_config` 与当前 Set 内配置，并对比冻结 resolved spec 与当前 StrategyConfig 的**完整渲染依赖**。先逐算法列出读取字段，再做语义 JSON 比较；保留整数精度、数组顺序、缺省/null 差异及单位。不能简单对整个 spec 做 hash：目标路由字段可能不同且不参与描述；也不能只比较算法类型、名字或一个阈值，遗漏连接符、级别、单位、表达式和特殊规则上下文。
5. 对运行时编译算法、特殊事件改写和多查询表达式，补充验证冻结 `runtime_query_configs` 的检测语义与声明配置一致。通过以后才返回本次读取内的不可变快照；不得读取一次算法、稍后重新读单位。事务外的历史/预测读取必须另有同事件、同版本的稳定身份和保留约束。
6. 单次读取设置超时、最多 32 个算法、最多 64 个阈值组/组内条件，JSON 载荷上限与 Reader 并发上限。暂时性连接失败 Retry；身份、版本、歧义和渲染依赖不符使用确定错误码 Block。退出回滚并释放事务，不记录配置或完整事件。

2026-09-30 17:00 左右，使用 `test-bkee5` MySQL 只读一致性快照核验前 4 个 active/published 拆分记录，结果如下。仅输出身份、差异字段路径，没有落盘完整 payload 或凭据。

| split ID | source_resource_version | Set 内配置相等 | 当前 Config 与冻结 resolved spec 的差异 |
| --- | --- | --- | --- |
| 1 | 1790758008036099 | 是 | `targets.length` |
| 2 | 1790758008248395 | 是 | `targets.length` |
| 3 | 1790758007919376 | 是 | `targets.length` |
| 4 | 1790758007975535 | 是 | `targets[*].bk_cloud_id/bk_object_inst_id/ip` |

这是当前 4 个 default 记录的关联证明，不是线上 Kafka 事件版本匹配或所有覆盖分支验收。记录版本相较此前读取已经更新；该表是 current 发布材料，不自动提供任意历史版本。旧样本 `395/4` 等尚未获得对应的冻结绑定，不能使用这 4 行代替它们。上述差异是否可排除，须以该算法实际读取依赖和黄金样本作证。

### `alarmd_event` 实际输入核验与下一步

2026-09-30 15:07–15:12（Asia/Shanghai）对 `alarmd_event` 进行有界只读抽样：手动指定 partition/offset，不加入已有 consumer group、不提交 offset、不生产消息。topic 有 1 个 partition；按多个时点分别抽取最新最多 60 条，**不是全量扫描**。其中一批 60 条有 58 条 `extra_data.signal_type=metric`、2 条 `log`；全部 `evaluation_family=metric_algorithm`，出现 `critical/warning/info` 和 `triggered/resolved`。不同批次的场景比例会随流量变化，不能据此断言其他类型不存在。

| 内容事实 | 线上样本字段 | 本次结论 |
| --- | --- | --- |
| 初始内容 | `content` | 来源文本保留在 Event；新 Alert 由构建器另行初始化。 |
| 当前值 | `values.value`（int/float） | 标准 Cleaner 可读取；样本未携带独立单位字段，单位须从精确关联的策略/指标配置取得。 |
| 当前级别及动作 | `evaluations[*].severity/action/action_reason` | 同一消息可能有多级别；构建时只处理选中的 triggered 判定。 |
| 策略身份 | `labels.strategy_id`、`labels.strategy_version` | ID 和版本均为整数；不同时间段有 `2/3/4` 和长整数两组版本值。早期核验使用小整数样本；最新回放已按有真实发布绑定的消息验证，不按版本位数筛选。 |
| 窗口证据 | `extra_data.window.size/required/anomalies`、`anomaly_begin_time`、`event_semantic_digest` | 有触发窗口摘要；样本未发现命中算法列表、历史比较值或预测上下界。摘要不能替代检测文案需要的具体值。 |
| 专用维度 | `dimensions`、`extra_data.additional_dimensions` | 保留原始类型和缺失语义；不能从资源名称推断检测算法。 |

首次尾部抽样中，14 个策略引用能关联 Config 与 Set，但 Config 为 inactive；这批样本不再作为正常策略内容实现的主要依据。现有 `CWStrategyClient` 实际查询未过滤 active，首次探针加 active 条件时曾误报配置缺失。后续正常样本核验已找到 Config 和 Set 均 active 的完整关联，见下表。

用户指示忽略 partition `0`、offset `721407` 的消息，本方案已排除该条，不再将其用作版本约束或实现依据。重新在保留范围头部、中部、距尾部 6000/1000 条及尾部的五个窗口各读取最多 40 条，共 200 条：较早两窗口的 80 条版本为 `2/3/4`，近期三窗口的 120 条为长整数。近期长整数涉及多个策略，不是仅在已排除那一条出现；本次不判定它们异常，也不推断具体生产者或切换原因。

早期选择下面六条消息做当前关联核验。均在 `kingeye` 中按同租户 `status.bk_strategy_id` 唯一命中 StrategyConfig，Config 与关联 Set 均 active，Set 内 ConfigID 均精确匹配。它们不是后来版本绑定回放的验收样本：

| partition / offset | strategy_id | 消息版本 | 场景证据 | 指标单位 |
| --- | --- | --- | --- | --- |
| 0 / 579063 | 395 | 4 | metric，系统主机监控 | percent |
| 0 / 579068 | 482 | 3 | metric，系统主机监控 | percent |
| 0 / 579070 | 496 | 2 | metric，系统主机监控 | percentunit |
| 0 / 650645 | 370 | 3 | metric，系统主机监控 | percent |
| 0 / 650648 | 351 | 3 | log_keyword | 缺省 |
| 0 / 650649 | 489 | 2 | metric，K8s 监控 | 空字符串 |

六份当前 StrategyConfig 均为 `Threshold`，`status.strategy_config_version` 均为 `1`；这说明即使消息版本在 `2/3/4` 范围内，也不能据此假定两个版本字段属于同一体系。首选对照消息为 partition `0`、offset `579063`，event_id `2038b0928b2cad7ff7190b1039c9cab417a01dcfe59720e9387122f92014ecd3`，`occurred_at=2026-09-28T15:06:05Z`。它关联模板 `25`、ConfigID `7d52d575edea4e2ca77b1a05fccd78c6`。版本字段语义仍需代码证据；当前配置关联已验证，可以继续建立这些场景的渲染输入和测试，不能把数字大小当作迁移阻断条件。

后续按下面顺序实施：

1. 确认该版本在哪一层生成；用同租户、同 strategy ID、同版本定位实际下发配置，再核对对应 StrategyConfig 和 Set 内配置。只读验证配置 hash 或完整渲染依赖相等，不能仅以 ID 关联成功视为版本匹配。
2. 内容算法优先读已匹配的 StrategyConfig `spec.strategy_detect_algorithms/strategy_item`；Set 的 `inner_metric_info` 可提供单位、指标展示信息，必须选择同一个 `config_id`，并核对 Set 尚未派生/下发的新配置不会混入。
3. 当版本证据缺失、匹配到失效配置或 Set/Config 尚未一致时，停止该来源切换并报告明确错误。不得读取废弃历史表，也不得用当前内容掩盖无法回放的触发时事实。

已增加 [StrategySetClient](../../internal/enrich/datasources/strategy_set_client.go) 和精确 ConfigID 选择模型；租户隔离、重复模板、取消、依赖失败及当前 Set/Config 关联的只读集成验证已完成。该 Reader 本身不保证事件版本匹配，不表示内容生成已经完成。

本次验证记录：

- `go test -race ./internal/enrich/...`：通过，14 个包、402 个测试。
- `go vet ./internal/enrich/...` 与 `golangci-lint run ./internal/enrich/...`：通过，lint 为 0 issues。
- `TestStrategySetClientLiveIntegration`：在 `test-bkee5` 只读查询通过。
- 受影响文档本地链接/锚点检查及 `git diff --check`：通过。
- `make check`：未通过，首先在 fmt-check 因缺少 `console/node_modules/.bin/prettier` 停止。单独执行 `make test vet race lint` 时，普通测试阶段遇到未改动的 `internal/lifecycle/kachook.TestConvertMessageMapsAlertAndEnrichToKACAlarm` 的 identity/source 断言失败，后续全仓 vet/race/lint 未执行；该单测单独重跑仍失败。当前主机还未安装 Helm，完整门禁不能宣称通过。以上局部验证不替代内容生成的跨系统对照。

### 内容构建基础实现的当前进度

- [description](../../internal/enrich/description/doc.go) 提供纯 Facts → 文案渲染及可注入 Resolver。已有 Threshold、PingUnreachable、OsRestart、ProcPort、历史比较、预测和 NoData 模板；历史与预测输入要求冻结证据，当前并无生产 Reader 自动补齐它们。这些模板的局部测试不能代表所有来源已支持。
- 单位与数值格式按 bk-monitor 当前实现迁移。52 个已注册单位、20 组数值及分类前缀别名共 2080 个对照子用例，fixture 由真实 Python 单位实现生成。历史与预测描述目前是模板黄金用例，尚未执行真实 Python 检测器的完整描述对照。
- [Lifecycle 构建入口](../../internal/lifecycle/content.go) 在新 Alert 持久化前生成 content；已验证输入副本隔离、新建/重复触发/两类升级、普通 Enrich 失败、已存计划重放，以及构建失败时不保存计划或创建 Alert。Lifecycle 来源进程已注入 Router；默认 source 模式仍复制 Event.content，生产 bkmonitor_description 已装配真实 Resolver；遇到未验证类型会明确失败，尚未切换任何线上来源。
- Scheduler 将带永久内容错误标记的错误分类为 `consume.Block`，暂时失败继续 Retry；局部测试验证 mailbox head 保留和修复后原序消费。生产来源诊断、运维恢复入口还需完成。
- EventSource `enrich.content_mode` 接受 source/bkmonitor_description；新模式在没有普通 processors 时仍要求 MySQL。Router 要求已发布版本与 Resolver，拒绝跨租户及来源版本不符；Lifecycle 测试证明入库的是生成文本，Event.content 保持原值且计划重放不再构建。真实配置 Reader 先通过四条 current 发布记录验证，后续 11 条真实 Kafka 样本的本地入库对照见下节。
- 新模式下 Display 保留生成的 content，避免再次裁剪日志、拼接对象、转换单位或映射枚举。创建后仍只通过 enrich payload 提供展示字段；核心不可变约束未调整。
- 创建内容预览使用 `input.opening_event` 和 `input.severity`，与 Event ID/JSON 丰富预览输入互斥；多触发级别要求显式选择。只读构造临时 Alert 并返回 `candidate_content`，不读取已存 Alert、不写仓库；普通 Event 丰富预览不生成 Alert.content。Console 支持该输入并保留已发布 content_mode。预览与生产共用 Runtime.Router 装配真实 Resolver；最新 20 条真实输入的本地 Service 预览已与入库和源码 oracle 对照，线上 HTTP/Console 链路尚未验收。
- 补充验证 `go test -race ./internal/enrich/... ./internal/lifecycle ./internal/lifecycle/scheduler`、同范围 `go vet` 和 `golangci-lint run` 通过，lint 为 0 issues。完整 `make check` 的既有阻断尚未解除；未完成真实 content 入库链路验收。

本轮 `go test -race ./internal/config ./internal/enrich/... ./internal/lifecycle ./internal/lifecycle/process ./internal/lifecycle/scheduler` 通过（19 个包，2858 个测试，含单位 fixture 子用例；新增 Display 专项另行通过）；同范围 go vet 及 golangci-lint 通过，lint 为 0 issues。安装 Console 锁定依赖后，`make check` 已通过 fmt-check，普通测试仍停在原有 KAC identity/source 断言，后续全仓 vet/race/lint 尚未由该门禁执行。Console typecheck 通过；来源配置与预览两个受影响测试文件共 8 个用例通过。全量 Console 测试有 5 个未改动 App 测试因当前 Node 的 localStorage 不可用失败。文档 59 个本地链接/锚点检查通过。所有结果均不代表跨系统描述一致性已经验收。

### 生产配置 Reader 的实施与验证（2026-09-30 17:25）

[DescriptionConfigurationClient](../../internal/enrich/datasources/description_configuration.go) 已按同租户 split ID 和整数版本读取 default 发布记录，并在 5 秒超时的只读 repeatable-read 事务内校验精确 Set UID、模板、ConfigID、default 标记、启用及发布状态。Set 内配置与冻结材料做完整语义 JSON 比较；当前 Config spec 排除顶层 targets 和 `strategy_item.query_configs[*].log_theme_list`，其余字段（含未知扩展）均参与校验。这两项属于对象筛选与日志查询/丰富上下文；描述名称读取 PromQL/name/agg_method，单位及算法读取发布材料，不读取这两项。override 返回 `publication_override_revision_missing`：源码证明消息现有整数版本不能区分同一覆盖记录的修改代次，见后面的发布分支核验。不查询废弃历史表或改用默认配置。

数据型跨业务规则遵循发布投影器：逐业务 spec 必须相同，只取第一份；目标型的一份 resolved spec 可以覆盖多个业务。resolved 元数据仍须匹配发布记录业务列表。后续真实回放证明事件业务不能作为这里的配置业务过滤条件，修正及证据见下节；这不等于所有 target 分支均已验收。

运行时查询仅提取描述所需的单位及 metric_id。真实编译材料的 index_set_id 等字段与声明模型可能有不同类型，不把整个运行时查询强行解码为声明模型。Threshold 同时接受已发布的 JSON 数值与数字字符串，并拒绝 null、非数值、NaN/Inf。指标名前缀按 Kingeye Executor 的构建规则生成，单位使用该发布材料中的单位。

[Resolver](../../internal/enrich/description/resolver.go) 已接入 Runtime.Router，按选中级别生成 Threshold 和 PingUnreachable 事实。发布投影器会重写专用查询表达式；Ping 文案只依赖原始丢包率，选中级别恰有一个 PingUnreachable 算法且单查询时可用。EventExecutor 会合成查询，声明 query_configs 允许为空。无数据、历史、预测、进程和重启的生产事实不足，按用户要求跳过本轮验收；已有纯渲染模板不代表这些生产分支可用。

数值词法从 standard 原始 payload 的 values 读取，并与 Event.Values 校验一致；缺失原文或值被清洗规则修改时拒绝混用。需注意 alarmd 标准输出 Converter 将观测值解码为 float64 后再编码，可能把 `10.0` 变成 `10`。Linkd 保存的只是接入时的类型，无法由此保证上游原始类型已保留；受整数/浮点差异影响的 bk-monitor 格式分支仍须补检测证据或逐样本对照，不能宣称全部字节级一致。

验证边界：

- 真实 `test-bkee5` MySQL 的前四条 active/published default 记录已通过 `TestDescriptionConfigurationLiveIntegration`：完成同版本 Set/Config 绑定、单位及算法读取。只读，没有发布配置、写 Alert 或提交 Kafka offset。该验证没有使用旧事件 `395/4`，也不代表历史版本可回放。
- database/sql 边界测试验证真实 GORM 的租户参数、载荷/行数上限、只读 repeatable-read、事务提交/回滚、版本及配置变化、跨业务、override 拒绝和依赖错误传播。Resolver 测试验证级别选择、单位歧义、原始数值类型、缺事实与取消。
- `go test -race ./internal/config ./internal/enrich/... ./internal/lifecycle ./internal/lifecycle/process ./internal/lifecycle/scheduler` 通过（19 包，2887 个测试，含单位 fixture 子用例）；相关 go vet 通过，golangci-lint 为 0 issues；61 个本地链接/锚点及 git diff --check 通过。完整 `make check` 通过格式检查，仍停在既有 `internal/lifecycle/kachook.TestConvertMessageMapsAlertAndEnrichToKACAlarm` 的 identity/source 断言。该门禁后续步骤未执行，不宣称完整门禁通过。

### 本轮跳过的数据与后续恢复条件（2026-09-30 17:48）

下表记录用户确认的跳过项。只读抽样没有找到足以按同一事件稳定定位的事实，不断言环境里绝对不存在这些数据。

| 组合或事实 | 缺口 | 当前处理及恢复条件 |
| --- | --- | --- |
| 环比、同比、高级历史比较 | 实际比较值、周期点及多点取值；标准消息未完整保留 | 生产 Resolver 返回 detection_evidence_missing；本轮跳过，获得同版本/同事件历史事实后再验收。 |
| 预测、振幅等依赖模型输出的算法 | 触发时预测上/下界或算法中间事实 | 本轮跳过；不重跑当前模型替代旧输出。 |
| 无数据 | bk-monitor 区分 no_data_period（距离最后数据）与 anomaly_period（从首次异常 checkpoint）；当前 no_data_periods 和滚动窗口异常数不足以证明两者 | 返回 nodata_evidence_missing；本轮跳过，不用 window.anomalies 代替 anomaly_period。 |
| OsRestart、ProcPort | 前次启动值或进程/端口状态、维度及专用查询事实 | 返回明确缺事实/未验证错误；本轮跳过，不从中文 content 反解。 |
| 转换前整数/浮点类型 | alarmd float64 再编码可能丢失 10.0 的词法类型 | 已保留收到的 JSON 类型；需要转换前类型才能证明一致的样本本轮跳过。 |
| 旧策略消息对应的发布材料 | current split/Set/Config 无法证明旧版本，例如早期 395/4 样本 | 拒绝用当前版本替代；本轮跳过缺材料的旧消息，不查废弃历史表。 |
| override 触发时配置身份 | 覆盖投影复用父记录的整数版本；现有标准消息没有覆盖摘要或独立修改代次 | 返回 publication_override_revision_missing；按缺事实跳过本轮验收。后续取得同事件冻结配置或覆盖版本身份，并验证保留期后再接入；不得用当前覆盖配置或默认配置替代。 |

**其余验收边界单独保留**：target 当前发布器只产生一份 resolved 配置，已验证该读取路径；多份 target resolved 输入按无已确认发布契约拒绝，不再列为现有正常发布分支的实现缺口。正式 EventSource 的实际组合清单、Cloud 的冻结配置绑定以及完整线上链路仍未验收，不能用合成样本或局部成功代替。默认 source 不变，未执行线上来源切换。

### 发布分支核验与当前验收边界（2026-09-30 18:00）

Kingeye `e34c323d` 的 `split_record_writer.py:476-479` 明确使用 `biz_ids[:1] if is_target else biz_ids` 构造 resolved：target 可覆盖多个业务，但只生成一份配置；data 才按业务生成多份。真实 MySQL 前四条读取样本均为 target。新增边界用例验证 target 跨业务单配置成功、target 多配置拒绝、resolved 租户不符拒绝，17 个事务/绑定测试（含父测试）通过 race 检查。

override 的风险是缺少触发时身份事实。`runtime_cache_split_projector.py:266-273` 用 `override or split_record` 选择配置载荷和 ID，但 `:372` 的整数 `strategy_revision` 仍取默认父记录的 `source_resource_version`。同一 override 更新而父记录不变时，标准消息里的 strategy ID/version 无法证明触发的是修改前还是修改后的覆盖参数。当前覆盖载荷也不是 default 的完整发布结构；即使补齐当前映射，仍不能解决旧事件身份问题。恢复条件是可按租户和事件稳定定位的冻结覆盖事实；补齐后再设计读取端口和版本校验，不修改 alarmd TriggerEvent。

对 `test-bkee5`、租户 `system` 的 current active/published 拆分记录做一次只读一致性查询，上限 256 条，返回 77 条：default target 60 条、default data 17 条、override 0 条。配置包含主机、MySQL、K8s、日志和 APM 上下文；这是配置清单，不能视为这些场景都有匹配版本的触发样本。当前 active `core_v1alpha1_strategy` 的 Kind 清单只有 Strategy，未找到 StrategyCloud；不据此断言其他环境或历史数据没有 Cloud。Cloud 的冻结身份与真实样本继续列为未验证项。

新增处理器验证覆盖 BaseTarget 的 monitor-source、no-data、system-metric、basic 四条资源路径，以及 UptimeCheck、Cloud、K8s、APM。每条验证已生成 content 在资源/Display 丰富和 `view.EnrichedAlert` 读取投影后保持原文，输入 Alert 不变；9 个测试（含父测试）通过 race 检查。这里的 no-data 只验证已有文本不会被丰富覆盖，不代表无数据描述的检测事实已经补齐。DATA、LOG_METRIC、LOG_KEYWORD 的内容保留验证已有独立用例。全部属于隔离处理器验证，不扩大前述 11 条真实 Threshold 入库对照的验收范围。

完整门禁的既存冲突已核对：`docs/reference/contracts/kac-alarm-output.md`、`docs/design/kac-alarm-hook.md` 和 KAC 测试要求固定 SourceName=鲸眼监控；`internal/lifecycle/kachook/message.go:80` 实际读取丰富后的 SourceName。该代码/契约差异单独保留，未通过修改测试期望绕过，也未在本次内容迁移中更改来源名协议。后续需按已确认的 KAC 输出契约修复并重跑完整门禁；当前不宣称 `make check` 通过。

### 多业务 Config 与真实预览补充验证（2026-09-30 18:08）

扩大抽样首先暴露真实读取问题：p0/731951、split 11 在同租户、模板、Config UUID 下有两份 active default Config；split 2 有三份。它们的业务不同，排除已确认上下文字段后均与冻结 spec 相等。此前“必须只有一份 current Config”的假设过强，不能将这类正常多业务投影作为缺数据跳过。读取器现已校验最多 32 份候选的身份、不同业务及完整渲染依赖；全部相等时返回冻结 spec，用同一组冻结算法和单位生成内容。重复业务、候选不一致与超限仍拒绝。新增三类回归用例分别验证跨业务等价成功、不等价失败和候选超限；事务/绑定测试共 20 个（含父测试）通过 race 检查，真实前四条 target 配置读取再次通过。

另一条失败输入 p0/732104、split 83 返回 `configuration_config_changed`；只读差异核验定位到 `strategy_item.trigger_config.count`。当前依赖校验仍保守保留此字段，未为了通过样本而排除；该样本不算验收成功。缺历史身份、依赖变化、覆盖版本缺失与实现假设错误要分别记录，不能统一称为缺数据。

修正后重新有界抽取尾部最多 1000 条，实际检查 552 条、选择 20 条，55 条配置依赖变化被排除。选中样本仍全部为 Threshold：target/metric/Host 10 条、target/metric/K8s_Node 1 条、data/log_keyword 4 条（其中 1 条有 K8s_Workload 配置上下文）、data/metric 5 条。没有据此认定 Ping、Cloud、拨测、全部日志/APM 检测组合已通过。

20 条全部通过真实 MySQL Reader/Resolver → 本地 Lifecycle/MemoryRepository → 本地只读 Preview Service 的对照：生成并入库的 content 与相同事实的 bk-monitor 源码 oracle 一致，预览 `candidate_content/effective_alert.content` 与入库一致，原 Event.content 保留。预览使用本地明确的 EventSource 发布 fixture，不读取存量 Alert，释放调用资源；未经过线上 HTTP/Console，也未写线上 ES、发布 EventSource 或提交 Kafka offset。验证 offset 为 732360、732368、732376、732378、732379、732380、732387、732389、732397、732398、732401、732402、732430、732439、732441、732452、732479、732481、732862、732911。

本轮最新验证：相关 19 个包的 race 测试通过，共 3215 个测试（含父测试与 fixture 子用例）；同范围 go vet 通过，相关 golangci-lint 为 0 issues；80 个本地链接/锚点与 git diff --check 通过。完整门禁仍不能通过上述 KAC 来源名契约冲突。这些结果不能代替缺事实组合、Cloud 冻结身份、正式来源配置和线上输出链路的验收。

### bk-monitor 源码对照与真实事件验证（2026-09-30 17:48）

- 新增 [源码 oracle](../../internal/enrich/description/testdata/bkmonitor_oracle.py) 与 [生成脚本](../../internal/enrich/description/testdata/generate_descriptions.py)：通过 AST 加载 bk-monitor 快照里的真实 ThresholdSerializer、Threshold/Ping 检测器、组合类、Django 模板和单位函数，仅隔离 DataPoint/Anomaly 外壳及应用依赖。304 个合成样本覆盖 6 类单位、整数/浮点、6 个比较运算符、单位前缀、and/or 与多算法、Ping 边界。[Go 对照测试](../../internal/enrich/description/oracle_test.go) 通过。它验证实际源码模板，不等于启动完整 bk-monitor 服务。
- 有界读取 `alarmd_event` partition 0 尾部 1000 条，选中 11 条同当前发布版本的 Threshold 样本，涉及 split ID 81、67、40、44、43、47、48。最后一次抽样 97 条版本不符被排除；这不是全类型统计。所有 11 条通过 [真实输入测试](../../internal/enrich/assembly/content_live_test.go)：standard Cleaner → 真实 MySQL 版本绑定 Reader/Resolver → Lifecycle → 本地 MemoryRepository。创建的是最高 triggered 级别的一个 Alert，入库 content 等于同事实 bk-monitor 源码 oracle，且不同于来源 content；重投成功，Event.content 保持原值。
- 抽样使用无 group、关闭自动提交的 Kafka Consumer；线上 MySQL 只读事务。仅在本地内存仓库写 Event/Alert，临时输入文件以 0600 创建并用后删除，没有写线上 ES、发布策略、生产消息或提交 Kafka offset。测试 EventSource 为本地显式配置，无普通 processors，因此不冒充线上 EventSource、资源丰富或 Hook 端到端验收。
- 修正业务绑定：p0/730235 的 split 81、版本 1790740328889014，事件业务为 5、resolved 为 524、唯一 current default Config 资源业务为 3。Converter 的 labels 读取 Event.BusinessID；配置按租户、split ID/version、Set UID、template 和 Config UUID 定位。回归用例覆盖事件/资源/投影业务不同以及重复 default Config 拒绝。
- split 44 当前与冻结 spec 的差异为 query_configs[0].log_theme_list；它不进入当前描述 Facts，已明确排除该上下文，其他字段继续保守校验。源码模板与真实回放均通过，未通过放宽租户或 UUID 身份来规避失败。
- 当前相关包 race 测试 19 包、3200 个测试通过；相关 go vet 与 golangci-lint 通过（0 issues）；受影响文档 78 个本地链接/锚点及 git diff --check 通过。`make check` 格式检查通过，仍停在未修改的 KAC TestConvertMessageMapsAlertAndEnrichToKACAlarm：测试期待 SourceName=鲸眼监控，实际有效视图为 ignored。完整门禁没有通过，后续门禁步骤未执行；未改动该输出行为。

复现源码 fixture：在已具备 bk-monitor Python/Django/DRF 依赖的解释器下运行 `generate_descriptions.py <bk-monitor 仓库绝对路径>`，再执行 `go test ./internal/enrich/description -run TestBKMonitorSourceDescriptionOracle`。实时集成测试需显式开启；普通测试会跳过。

### 完整门禁与确定性失败恢复（2026-09-30 18:36）

KAC 文档与原测试都要求固定 `SourceName=鲸眼监控`，已修正 Hook 直接取丰富结果来源名的代码偏差。原有 13 个 KAC Hook race 测试通过，未改动断言。该修复使本次内容生成的全仓门禁可以继续执行。

完整 `make check` 已通过：格式检查、全仓普通测试、go vet、race、golangci-lint（0 issues）、Console lint/typecheck/测试/构建、两组 Helm strict lint、26 个 Chart 测试，以及 7 个 Python 和 6 个 Node 发布脚本测试。Console 为 50 个文件、308 个测试通过，5 个文件/5 个测试按既有条件跳过。测试环境使用 Go 1.26.7、Node 24.19.0、Python 3.11.15 和仓库 CI 固定的 Helm 3.17.3。执行方式是在 PATH 前置这些运行时后运行 `make check`，未修改门禁或降低测试断言。

门禁推进时的环境失败已分别定位：默认 Node 26 的实验性 localStorage 影响 jsdom，Python 3.6 不支持发布测试使用的 subprocess 参数，Helm 3.19 的 schema 错误文本与 CI 基线不同。使用上述明确版本后全门禁通过；这些失败保留为环境记录，不算业务验收失败或模板差异。

Cloud 的既有 Kind 为 `StrategyCloud`，与普通 StrategyConfig 共用 `core_v1alpha1_strategy`。描述 Reader 已接受该 Kind，继续比较完整冻结 spec；`cloud_id/cloud_type/cloud_resource_type` 参与校验。新增 Cloud 读取成功、云字段变更拒绝和未知 Kind 拒绝用例通过。当前尚未取得真实 Cloud 事件，仍跳过对应线上样本验收。

运行路径补查发现：单独 `consume.Block` 会退出当前 Runtime，但中心原先会按最多 60 秒的退避不断分配新 epoch，无法满足“确定性缺失不得无限重试”。已在 Lifecycle 任务边界标记需要修复的内容失败，Worker 将 `blocked=true` 传入确切代次的报告；中心持久化该标记并暂停同来源 Lifecycle 角色。其他来源和 Cleaner 的处理契约保持原样。标记在停止未完成时也保留，中心重启、授权到期、扩容或配置发布都不能自动解除。

恢复使用[管理接口](../reference/contracts/task-resume.md)，校验 source/version/epoch 及整个来源角色的停止握手；解除后规划新 epoch 并携带 retired consumer 列表。相同配置 PUT 会被发布服务去重，因此不能作为恢复操作。恢复不修改来源 Event、TriggerEvent、已保存 EventPlan 或 Alert.content。版本已变的 backlog 仍由 Router 拒绝，不能用新版本冒充原配置。

暂停、跨中心快照重建、时间推进/新 Release 不自动重试、停止握手、过期恢复请求、Worker 报告与管理鉴权的相关 race 测试通过（taskdispatch、管理 API、Lifecycle process/Scheduler 共 139 个测试）。

真实 Redis 验证使用隔离的临时实例，实际 Redis 为 7.4.11。显式执行 taskdispatch 的 3 个 Redis 协议集成测试和 redisstream 的 2 个集成测试，均通过 race 检查。新用例验证 stopping 阶段持久化 blocked、后续 stopped 报告不清除、中心重启保留暂停、旧 epoch 恢复拒绝，以及显式恢复后创建新 epoch 并保留退休 consumer 记录；既有 Stream 用例验证只接管退休 consumer 的 PEL、不抢健康 consumer 的消息，并验证批量 XACK。测试仅使用临时实例和独立测试键，实例已删除；未写 `test-bkee5` Redis、ES、Kafka，也未向线上 Hook 发消息。

这是组件级的真实 Redis 验证，未运行正式 EventSource 的多进程内容失败/恢复或生产切换演练。完整 Enrich 外部读取、真实 Cloud/Ping 样本及缺少检测事实的组合仍保留各自验收边界。

### 暂停恢复串联验证与 APM 样本（2026-09-30 18:44）

新增 [TestContentFailureSignalResumeIntegration](../../internal/controlplane/api/content_recovery_integration_test.go)，在隔离 Redis 7.4.11 上实际串联 Mailbox、Scheduler、consume.Runtime、redisstream.Session、taskdispatch.Agent/Controller、内容 Router/Builder、Lifecycle 与管理 HTTP API，race 检查通过。使用固定 FactsResolver 和本地 MemoryRepository，不冒充线上事实读取或完整多进程进程入口。

- 首次缺少观测值时，Signal 保留在 PEL，mailbox 队首不动，没有 EventPlan 和 Alert；任务的 blocked 报告使中心暂停来源 Lifecycle。经过超过首次自动重试间隔的多次规划，仍不创建替代任务。
- 修复固定事实后，通过管理鉴权的恢复接口校验原 source/version/epoch，中心创建新 epoch；退休 consumer 的同一条 PEL Signal 被接管，按 opening、opening 重试、later 的顺序完成处理。
- 最终 Alert 入库的是生成的 `CPU > 5.0, 当前值10%`，两条来源 Event.content 保持不变；第二条事件及终态重投不重新生成同一 Alert 的内容。Mailbox 和 PEL 清空。
- 显式 Redis 测试通过后删除临时实例，没有写线上中间件。普通 `go test` 默认跳过该测试，相关普通测试通过不代表它已执行。

第二次真实消息抽样按 `(config_type, monitor_item_type, metric_source, object_model_code)` 每组最多 3 条选择，修正此前使用不存在字段 `data_source_type` 导致的分类不清。检查 900 条、选中 12 条；529 条达到场景配额，84 条当前配置依赖变化被排除。选中的算法仍全部为 Threshold，配置算法合计 16 个。

| 配置场景 | metric_source | 配置对象模型 | 本次成功样本 |
| --- | --- | --- | --- |
| target / metric | kmc | cw-Host | 3 |
| target / metric | 空 | cw-Host | 2 |
| data / log_keyword | klc | 空 | 3 |
| data / log_keyword | klc | cw-K8s_Workload | 1 |
| data / metric | kapm | 空 | 2 |
| data / metric | 空 | 空 | 1 |

12 条均通过真实 MySQL Reader/Resolver → 本地 Lifecycle 入库/重投 → 只读 Preview Service；content 与相同事实的 bk-monitor 源码 oracle 逐字相等。partition 0 offsets 为 736079、736081、736084、736085、736100、736104、736105、736156、736177、736189、736190、736476。本次明确证明 2 条 APM 指标描述，不证明 APM namespace/资源接口的线上丰富；空 metric_source 不推断为 LOG_METRIC。本次没有取得 LOG_METRIC、Ping、Cloud 的真实样本，继续记录为未验收。此前 20 条样本的笼统 data/metric 分类也不倒推为全部 APM。

Cloud 的源码补查确认 `TargetExecutor` 按对象模型进入 is_cloud 分支，并继承 `BaseExecutor.build_items` 的 `AGG(spec.name)` 命名规则；Cloud 不另有一套 description 模板。当前 Kind 和云字段绑定有隔离测试，真实 Cloud 发布材料和事件仍缺少。上述分类、源码和本地验证不能代替正式 EventSource 发布配置及线上输出验收。

新增串联测试后重新执行完整 `make check`，全部门禁再次通过；94 个受影响本地链接/锚点和 `git diff --check` 通过。真实 Redis 测试另行显式运行；完整门禁中的普通测试依然按环境条件跳过外部集成测试，不混淆两者结果。

### 本轮交付范围与后续启用

2026-10-08 继续只读验证，实际审阅文件已保存到[真实告警输出样本](../research/samples/2026-10-08-alert-content/README.md)，并提供[机器可读 JSON](../research/samples/2026-10-08-alert-content/samples.json)。本次读取 `alarmd_event` partition 0 在快照时保留的全部 169,399 条消息，范围 `[1484421,1653820)`；选出 18 条不同策略/级别组合。13 条生成 Alert（主机 6、APM 6、K8s 节点 1），全部通过 bk-monitor 同事实源码 oracle、只读预览、终态重投和来源内容不变检查。5 条因 `publication_version_mismatch` 在计划保存前拒绝，无 Alert。日志指标、日志关键字、MySQL、Ping/重启等有部分 current 发布配置，但本次保留区间未读到其真实事件；仍未验收。该结果来自真实 Kafka + 线上只读 MySQL + 当前工作树的本地重放，Alert 仅保存至 MemoryRepository，不是线上已切换模式的 ES 输出。显式导出集成测试和 assembly 普通 race、go vet、golangci-lint 通过；本次未重新执行全仓 `make check`。

本轮完成实施步骤 0–3 中可用事实覆盖的代码、测试和操作入口。缺少历史/预测/无数据/专用检测事实、旧版本发布材料与覆盖独立修订的项目按用户确认跳过，恢复条件保留在前表；没有取得真实触发样本的 LOG_METRIC、Ping、Cloud 等场景保留未验收状态。无需为了补齐这份记录继续重复扫描没有新入口的数据。

步骤 4 的正式来源启用属于后续部署验收：取得实际 EventSource 及生产者版本，逐项核对该来源启用的组合，排空或隔离不兼容版本 backlog，发布 `bkmonitor_description`，再核对线上持久化、输出和暂停恢复。当前没有执行线上配置切换，不将源码模板、合成资源测试、本地 MemoryRepository 或隔离 Redis 的成功写成生产全类型验收。默认 source 与已存 Alert.content 的不可变规则仍生效。

## 渲染规则的实施批次

先从 bk-monitor `service/detect/strategy` 的模板与 `core/alert/adapter.py` 的 `description` 赋值点提取黄金样本，形成规则清单。顺序以检测算法及数据依赖为主轴，再与 Enrich 场景交叉，避免按资源类型复制同一模板。

1. **基础批次**：阈值、组合阈值、固定文字/目标类事件、明确的无数据描述；覆盖 DATA、BaseTarget 六分支、UptimeCheck、Cloud 中实际出现的组合。先完成数值、单位、指标名及多算法连接规则。
2. **历史批次**：简单/高级环比、同比、振幅、预测等；先交付不可变比较值或预测事实的读取，再加入模板。历史查询当前值不能替代检测时比较值。
3. **日志与专用批次**：LOG_METRIC、LOG_KEYWORD 的查询和命中描述，以及 K8s/APM 关联的实际检测算法。现有 `Display` 的旧字符串裁剪/正则逻辑不能作为核心内容生成器；依据结构化检测事实重写对应规则。

每个批次都覆盖“正常、边界、缺事实、组合算法、级别差异、单位精度、重投、策略变更”样本。允许分批合入代码，但切换 EventSource 前，**该来源实际启用的全部组合**必须完成验收，不允许只切换到一个部分规则集合。

## 失败、幂等与运行约束

- 在 `bkmonitor_description` 模式，事实缺失、版本不符、未知算法、输出超限或模板不支持均不得复制 `event.Content` 冒充成功。返回带有限定错误码的失败，且必须发生在 `EventPlan` 持久化之前；不得先创建不可修复的错误 Alert。
- 暂时性依赖失败使用现有 Lifecycle 消息重试与有界退避。确定性缺失不得无限重试：在 Lifecycle scheduler 对内容错误分类，复用 `consume.Block` 保留消息和 mailbox 顺序，并提供来源、事件 ID、规则/依赖错误码、恢复步骤的告警与人工处理入口；不记录完整 payload。需要在实施时验证 Block 的队列所有权和恢复操作，不能仅靠日志。
- 一旦 `EventPlan` 已保存，重试只能执行原计划，不能重新读取策略并改写内容。计划保存前的 CAS 冲突重算必须读取同版本事实并产生相同文本。按租户隔离所有读取和缓存；结果、模板及源事实有硬上限。
- 前置 Event Enrich 失败时，按既有规则保存失败结果；若候选仍获准新建且内容构建成功，Alert 复制该丰富状态并保存生成的核心内容。内容构建失败与普通 Enrich 失败要有不同指标和诊断。
- 预览入口应能用未持久化 Event + Alert 展示候选内容及缺失事实，不执行核心字段写入；正式流程与预览共用 Resolver/Renderer。旧 Alert 不做后台回填。

## 与现有 Display 和读取视图的关系

`internal/enrich/processors/display.go` 当前以 `alert.Content` 为输入，通过日志裁剪、阈值单位和数值映射生成 `display.content`；Kingeye 投影可将其补丁施加到读取时的临时 `$.content`。启用新模式前应逐条检查该处理是否会二次加单位、重复指标前缀或把核心新文本再改写为另一句。优先使 `display.content` 直接引用生成后的 `alert.Content`，只把对象、维度、标题等额外展示字段留在 Display；确实需要展示专用格式时必须显式命名并对外明确，与 `Alert.content` 对照测试。持久化字段的不可变校验不放松。

## 工程步骤与完成条件

| 步骤 | 主要改动 | 完成条件 |
| --- | --- | --- |
| 0. 固定输入与对照 | EventSource 清单、转换链路、脱敏真实样本、bk-monitor 黄金描述、事实来源与保留期 | 每个实际启用组合均有样本和事实来源；缺口有明确 blocked 项。 |
| 1. 固定触发时快照 | 增加版本化策略/检测事实读取端口，校验版本和租户；必要时补 Linkd 可读的上游持久化 | 重投、策略更新后仍取得同一事实；历史证据不从实时数据重算。 |
| 2. 内容构建器 | `internal/enrich` 的 Resolver、纯 Renderer、按来源路由和显式配置；Lifecycle 新建入口接入 | 新 Alert 内容只初始化一次；Event 和已存 Alert 不变；未知算法拒绝切换。 |
| 3. Display 与运行错误 | 删除/收敛重复文本处理，内容错误分类、Block/恢复观测、预览 | 核心、读取视图、Hook/输出的目标内容一致；缺事实不会产生错误 Alert。 |
| 4. 分场景启用 | 按来源发布配置切换；覆盖上述三个批次和所有已启用组合 | 对照样本逐字一致，失败和回滚演练通过；保留版本化配置可退回 `source` 处理新事件。 |

重点测试：Renderer 的表驱动黄金用例；Resolver 的租户/版本/缺字段/超时用例；Lifecycle 的新建、`close_and_create`、`update_current`、同级触发、计划持久化后重放、CAS 冲突；Domain 仍拒绝修改已存 `content`；Enrich 失败只写其自身字段；Display/查询/Hook 的输出一致性。执行 `make check`，并用同一批脱敏输入做 bk-monitor 与 Linkd 的跨系统对照。单元测试或静态检查不能代替真实数据链路验收。

## 当前待确认事项

1. `alarmd_event` 已确认使用 standard payload；仍需其正式 EventSource 发布配置、线上生产者版本，以及“已有支持类型”的真实集合。
2. 已查明 labels 整数版本与 plan_ref 字符串版本的代码来源，default 发布身份到 Set/Config 的当前版本绑定已通过真实读取与样本验证。override 独立修订、历史版本和比较事实仍缺稳定来源与保留期，按用户确认记录并跳过本轮对应验收。
3. 对 bk-monitor 自身格式化异常而输出空串的样本，产品要逐字保留空串还是按明确的新规则处理。该选择须写入黄金样本，不能隐式回退来源 `content`。

上述事实未确认前，本方案给出可实现的代码边界和推进顺序，但不能承诺所有场景已具备逐字一致的数据条件。

### 与 Event 前置丰富和策略链整合（2026-10-08）

内容构建失败时 Event 已完成的丰富结果继续保留，不保存业务计划或错误 Alert；恢复时复用丰富快照。
Display 在 `bkmonitor_description` 模式不生成 `$.content` 覆盖补丁，防止 opening Event 的来源文案
通过复制的丰富快照覆盖新 Alert 的生成内容；其余展示字段正常丰富。候选内容预览明确使用
`input.opening_event`，普通 `input.event_id/input.event` 保持多等级 Event 丰富语义，响应额外返回
`candidate_content/effective_alert/changes`，不改写 Event 有效视图或生产数据。

本次整合验收：完整 `make check` 通过（含 Go/vet/race、静态检查、Console和Helm），
真实 Redis 内容失败阻断与修复恢复用例通过；策略运行时、模拟及历史来源版本读取的相关race通过。
Console Event丰富与策略诊断浏览器回归通过。历史线上样本未重新采集，既有日期记录不代表本次线上联调。
