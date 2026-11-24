# Alert.content 在创建阶段生成的实施草案

> 2026-10-17 统一降级确认：合法 Event 的任何文案生成错误、panic、空白或非法输出均不能
> 导致事件被丢弃或阻止应创建的告警。优先逐字保留来源 content；来源为空/空白则使用
> “告警已触发，详细内容暂不可用。”。创建与预览共用实现，分别记录 WARN 或返回降级诊断。
> 事件身份检查与 Worker 取消控制不被吞掉，已保存 Event/Alert 不回填。
> 下方错误白名单、缺文案即拒绝及历史暂停流程不再是当前文案处理契约；以
> [任务恢复与内容边界](../reference/contracts/task-resume.md#操作顺序与内容边界) 为准。

> 告警字段遵循 alarmd 生产者的标准事件契约。
> ProcPort 已读取结构化进程/端口维度，OsRestart 使用 triggered 结论和当前 uptime；
> 两者不再一律按专用查询改写拒绝。历史算法保留上游的条件模板：有完整冻结条件时
> 只陈述条件和当前值，不补造历史比较值；缺少条件时统一降级。自定义级别缺少数字算法级别映射、进程事实缺失/非法时也保留
> 非空来源内容并记录具体原因。创建、重投和预览由原始 JSON 示例回归覆盖；
> 这属于本地验证，不扩大历史线上验收范围。完整边界见
> [任务恢复与内容边界](../reference/contracts/task-resume.md#操作顺序与内容边界)。

> 2026-10-19 执行历史对齐，沿用既有文案容错：`aggregate_method_missing`、`item_name_missing`、
> `detection_evidence_missing` 属于展示事实缺失，创建与预览允许采用来源原文并记录诊断。
> 普通丰富与文案统一按租户、`strategy_id` 和 `strategy_revision` 精确查询
> `alarm_strategy_execution_revision`；事件 `strategy_version` 对应历史表 `strategy_revision`。
> 使用同一快照的 `runtime_config.metadata/spec` 与 `items`，
> 不再依赖会被覆盖的 SplitRecord 当前态或 `publish_status`。历史缺失按
> `strategy_revision_missing` 保留来源文案；歧义、损坏、身份不符仍明确失败。
> 下方以 SplitRecord 当前态代替历史的描述属于历史实现，当前契约以
> [任务恢复与内容边界](../reference/contracts/task-resume.md#操作顺序与内容边界) 为准。

> 2026-10-17 持续关联更新：同级触发和 `update_current` 升级默认直接使用本次 `Event.content`，
> 与时间字段和 `revision` 在原有一次 Alert CAS 中保存；不新增内容查询，不重跑创建构建器。
> 空字符串原样覆盖，恢复、关闭和低等级抑制保留已有内容。新建 Alert 仍按 `content_mode` 生成初始内容。
> 首次 Enrich 快照保持不变；本文历史执行记录中的“已存 content 不可变”已由此规则替代。

> 2026-10-14 无数据接入更新：用户确认直接使用 alarmd 原始 `extra_data.no_data_periods`
> 生成无数据周期描述，省略无法证明的旧延时文字，保持 alarmd 输出不变。
> Resolver 已接入标记/family 一致性、同版本无数据配置启用/等级校验和周期数校验；
> 上述四类无数据文案错误发生且 Event 自带非空 content 时，创建和只读预览
> 均采用原文兜底，记录具体失败原因；其他错误继续沿原边界处理。
> 下方“无数据本轮跳过”及全量逐字一致要求对此分支属于历史边界。
> 当前契约见[标准事件的无数据事实](../reference/contracts/standard-event.md#无数据描述的检测事实)，
> 实际验证见[2026-10-14 无数据接入](../research/samples/2026-10-14-nodata-content/README.md)。

> 2026-10-13 错误处理策略更新：普通内容/逻辑错误与 panic 记录消息失败后移出 Mailbox，不再以这类消息错误暂停整个来源；消息错误默认无重试白名单，显式可重试最多三次。本文下方的“确定性失败阻断/修复后自动重放”属于历史行为，当前策略见[运行容量与失败隔离](runtime-capacity-and-recovery.md)。既有 blocked 任务的受控恢复入口保留。

> 状态：本轮可用事实范围的实现与验证已完成；全类型线上验收和来源切换尚未完成。配置 Reader、FactsResolver、创建前入口、content_mode、Display 内容保留、opening Event 预览以及确定性失败暂停/恢复已接入。Threshold/Ping 已完成源码模板对照；先后两次共 32 条真实 Threshold 消息通过版本绑定、描述对照、本地 Alert 入库/重投及只读预览检查，含 target、data、日志关键字、K8s 配置上下文及明确的 APM 样本。真实 Redis 上的失败暂停、管理恢复和队列继续处理验证通过。缺少稳定检测事实或真实样本的组合按用户要求记录并跳过。KAC 固定来源名的实现偏差已修复，完整 `make check` 通过；默认 source 模式继续复制来源内容。调研日期：2026-09-30；Linkd 工作树基线 `ac1331de`，alarmd 二阶段补充核对 `origin/master@8b4c5622`（含 `8c3c859f`），Kingeye 实现，bk-monitor `51c834dc6`。未切换工作树或修改 alarmd。源码证据及现状见[调研记录](../research/2026-09-30-alert-content-generation.md)。以下历史执行记录保留当时结果，以最新验证小节判断当前状态。

> 2026-10-12 已确认的历史积压处理例外：确切策略发布版本在当前发布表中已不可得时，内容 Reader 返回 `publication_missing`，Lifecycle 可使用该 Event 自带的非空原始 content，并记录 `source_fallback` WARN 诊断。不得借用最新策略生成历史文案。降级内容继续校验并冻结进 EventPlan；发布歧义、身份/模板错误、来源 Release 缺失与外部依赖故障不降级。无数据的四类文案错误按上方 2026-10-14 边界允许原文兜底，其他事实错误按上方 2026-10-17 白名单处理。运行契约见 [任务恢复与内容边界](../reference/contracts/task-resume.md#操作顺序与内容边界)。


## 当前策略读取边界（2026-10-19）

策略历史材料来自 `alarm_strategy_execution_revision`，同版本唯一记录才是触发时的策略快照。
`runtime_config` 提供声明式 metadata/spec，`items` 提供同版执行查询与算法；
读取校验、统一降级和运行限制统一见[任务恢复与内容边界](../reference/contracts/task-resume.md#操作顺序与内容边界)。
2026-10-19 对齐 Kingeye 10/17 的 Reader 变更：执行快照位于根部，管理业务列为 `strategy_bk_biz_id`。
默认与覆盖均使用自身 ID 和执行修订；`record_revision`、来源水位与缓存生成号不参与执行历史定位。
当前 Reader 修复从 Linkd `b6c21fe0` 重放到 `cc0c7150`，保留统一文案降级和检测场景；
自定义文案、控制面故障重试及后续触发的内容更新沿用上游规则，读取过程不改写原始 Event。
SplitRecord 是可变当前态，不能提供历史保证；本轮移除以它的 `source_resource_version` 与
`publish_status` 决定丰富有效性的路径。下方旧实现和调研记录不代表当前 Reader。

## 目标、边界与验收定义

- 对已配置启用的 bk-monitor/KAC 来源，新建及持续触发时 Alert 的 `content` 由 Linkd 在 Enrich 准备阶段按 bk-monitor 检测事件 `description` 规则生成。以同一触发事实和同一策略版本下的 bk-monitor `Event.description` 为对照；不把通知阶段添加的“新告警”“已持续”等文字计入对照。
- 覆盖 Linkd 当前内置 Enrich 能识别的告警场景；以实际 EventSource 和真实输入清单界定可交付的具体组合。`DATA`、`LOG_METRIC`、`LOG_KEYWORD`、BaseTarget 的六个分支、UptimeCheck、Cloud 均须列入矩阵。K8s/APM 是资源和展示上下文，须与其实际检测类型交叉验收，不能视为独立的描述算法。
- `alarmd.TriggerEventV1` 输出保持原样。opening Event 的 `content` 和 `source_raw_data` 仍是来源事实。已创建 Alert 的 `content` 在后续同级触发或原地升级时按 content_mode 使用本次 Event 的事实生成，与时间和版本一次写入；标题、维度与首次丰富快照继续保留。
- `enrich.Result` 继续仅返回 `enrich_status/enrich`。构造**尚未持久化**的新 Alert 时按 `content_mode` 生成初始 `content`；持续更新在 `source` 模式使用本次来源内容，在 `bkmonitor_description` 模式使用本次 Event 及其精确策略历史版本重新渲染，沿用统一降级规则。

普通指标的“一致”按字节级字符串对照，包括算法组合顺序、连接词、空格、数值精度、单位、指标名和空值语义。无数据按 2026-10-14 确认的来源周期描述验收，不还原旧延时文字。对仍必须依赖已丢失的历史检测事实的组合，不能用实时查询、当前策略或解析旧中文 `content` 伪造一致性；该组合在事实补齐前不得宣称验收通过。

## 当前链路与目标链路

当前实现先冻结 Event 丰富，再进行策略准入；实际新建的 Alert 在 `planNewAlert` 中复制
opening Event 的选中等级结果并调用内容构建器。持续触发在描述模式下使用本次 Event 的
临时候选身份和选中等级丰富结果调用同一构建器，只保存内容，不替换已有首次丰富快照。
丰富和内容构建统一复用当前任务加载的来源配置；积压 Event 不再按 EventSourceVersion 回读历史 Release。Event 的接入版本保持原样，丰富结果另外记录实际执行的 enrich_source_version；已保存的结果不重算。

```text
Event → 前置 Processor Chain → 持久化 Event.enrich
    → 抑制与生命周期选择 opening Event / EventEvaluation
    → 复制选中等级的丰富快照到新 Alert
    → 只读内容事实收集与 bk-monitor description 渲染
    → 初始化新 Alert.content 一次
    → 后续策略与持久化 EventPlan → Alert CAS、日志和 Hook
```

同级别连续触发和 `update_current` 级别升级按 `content_mode` 选择本次来源原文或重新渲染的描述，与生命周期时间和 `revision` 合并在同一次 Alert CAS 中保存；`close_and_create` 升级会创建新 Alert，应以该新 Alert 的 opening Event 和选中级别重新生成。`display.content` 的临时补丁仍属于首次丰富快照，不代表持久化核心字段。

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

## 数据契约与验证

Standard Cleaner 接收 `content`、`evaluations`、`values` 和 `labels`。渲染时使用事件携带的策略身份和发布版本，按租户读取对应配置，核对算法、单位与检测事实。缺少事实时沿用来源内容，避免根据当前配置重写历史告警。验证方法见[阈值描述回放](../research/samples/2026-10-08-alert-content/README.md)。

## 渲染规则的实施批次

先从 bk-monitor `service/detect/strategy` 的模板与 `core/alert/adapter.py` 的 `description` 赋值点提取黄金样本，形成规则清单。顺序以检测算法及数据依赖为主轴，再与 Enrich 场景交叉，避免按资源类型复制同一模板。

1. **基础批次**：阈值、组合阈值、固定文字/目标类事件、明确的无数据描述；覆盖 DATA、BaseTarget 六分支、UptimeCheck、Cloud 中实际出现的组合。先完成数值、单位、指标名及多算法连接规则。
2. **历史批次**：简单/高级环比、同比、振幅、预测等；先交付不可变比较值或预测事实的读取，再加入模板。历史查询当前值不能替代检测时比较值。
3. **日志与专用批次**：LOG_METRIC、LOG_KEYWORD 的查询和命中描述，以及 K8s/APM 关联的实际检测算法。现有 `Display` 的旧字符串裁剪/正则逻辑不能作为核心内容生成器；依据结构化检测事实重写对应规则。

每个批次都覆盖“正常、边界、缺事实、组合算法、级别差异、单位精度、重投、策略变更”样本。允许分批合入代码，但切换 EventSource 前，**该来源实际启用的全部组合**必须完成验收，不允许只切换到一个部分规则集合。

## 失败、幂等与运行约束

- 所有 content_mode 共用[统一降级](../reference/contracts/task-resume.md#操作顺序与内容边界)：构建错误、panic、空白或非法输出优先使用来源原文，原文为空则使用固定文案。合法事件不因文案失败移出队列或缺少应创建的 Alert；降级不表示事实已补齐。
- 事件身份校验与整个处理 Context 的取消在构建器外执行。取消时不冻结内容也不确认队首，按所有权控制协议保留重试。只有文案依赖返回错误而处理 Context 有效时不再做事件级重试，直接降级。存储与准入等其他错误继续按既有策略处理。
- 一旦 `EventPlan` 已保存，重试只能执行原计划，不能重新读取策略并改写内容。计划保存前的 CAS 冲突重算必须读取同版本事实并产生相同文本。按租户隔离所有读取和缓存；结果、模板及源事实有硬上限。
- 前置 Event Enrich 失败时，按既有规则保存失败结果；若候选仍获准新建且内容构建成功，Alert 复制该丰富状态并保存生成的核心内容。内容构建失败与普通 Enrich 失败要有不同指标和诊断。
- 预览入口应能用未持久化 Event + Alert 展示候选内容及缺失事实，不执行核心字段写入；正式流程与预览共用 Resolver/Renderer。旧 Alert 不做后台回填。

## 与现有 Display 和读取视图的关系

`internal/enrich/processors/display.go` 当前以 `alert.Content` 为输入，通过日志裁剪、阈值单位和数值映射生成 `display.content`；Kingeye 投影可将其补丁施加到读取时的临时 `$.content`。启用新模式前应逐条检查该处理是否会二次加单位、重复指标前缀或把核心新文本再改写为另一句。优先使 `display.content` 直接引用生成后的 `alert.Content`，只把对象、维度、标题等额外展示字段留在 Display；确实需要展示专用格式时必须显式命名并对外明确，与 `Alert.content` 对照测试。本次仅允许持久化 content 随触发更新，其他创建字段和首次丰富快照的不可变约束保持不变；已有 content 展示补丁仍可能覆盖读取视图中的核心内容。

## 工程步骤与完成条件

| 步骤 | 主要改动 | 完成条件 |
| --- | --- | --- |
| 0. 固定输入与对照 | EventSource 清单、转换链路、脱敏真实样本、bk-monitor 黄金描述、事实来源与保留期 | 每个实际启用组合均有样本和事实来源；缺口有明确 blocked 项。 |
| 1. 固定触发时快照 | 增加版本化策略/检测事实读取端口，校验版本和租户；必要时补 Linkd 可读的上游持久化 | 重投、策略更新后仍取得同一事实；历史证据不从实时数据重算。 |
| 2. 内容构建器 | `internal/enrich` 的 Resolver、纯 Renderer、按来源路由和显式配置；Lifecycle 新建入口接入 | 新 Alert 内容按模式初始化，后续触发直接复制 Event.content；Event 不变；未知算法拒绝切换。 |
| 3. Display 与运行错误 | 删除/收敛重复文本处理，内容错误分类、Block/恢复观测、预览 | 核心、读取视图、Hook/输出的目标内容一致；缺事实不会产生错误 Alert。 |
| 4. 分场景启用 | 按来源发布配置切换；覆盖上述三个批次和所有已启用组合 | 对照样本逐字一致，失败和回滚演练通过；保留版本化配置可退回 `source` 处理新事件。 |

重点测试：Renderer 的表驱动黄金用例；Resolver 的租户/版本/缺字段/超时用例；Lifecycle 的新建、`close_and_create`、`update_current`、同级触发、重投、CAS 冲突；内容与时间、版本单次更新，失败前后不部分写入，持续触发不重复调用内容构建器；Domain 允许活动告警更新 content 并保留其余创建字段；Enrich 失败只写其自身字段；Display/查询/Hook 的输出边界。执行 `make check`，并用同一批脱敏输入做 bk-monitor 与 Linkd 的跨系统对照。单元测试或静态检查不能代替真实数据链路验收。

## 当前待确认事项

1. `alarmd_event` 已确认使用 standard payload；仍需其正式 EventSource 发布配置、线上生产者版本，以及“已有支持类型”的真实集合。
2. 已查明 labels 整数版本与 plan_ref 字符串版本的代码来源；当前默认文案只读取同版本 SplitRecord 发布材料，早期 Set/Config 的真实验证仅作历史证据。override 独立修订、历史版本和比较事实仍缺稳定来源与保留期，按用户确认记录并跳过本轮对应验收。
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
