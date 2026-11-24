# Enrich 的 item 补全规则

核对日期：2026-11-01。代码基线：主仓 `feat/linkd-dev`，`b6fa6420f75fc53b702071e61ca5c8721615c962`。

本文按当前代码说明 `item` 从哪里来、什么条件下补全、各类告警如何取值。
这里的“规则”指当前实现，不新增产品要求，也不代表目标环境已经部署或历史告警已经更新。
Enrich 整体流程见[丰富设计](enrich.md)。
监控项名称如何参与告警名称，以及待实现的跨策略类型规则，见 [title 生成规则](enrich-title.md)。

## 1. item 最终取什么值

正式 KAC 输出按以下顺序取值：

1. 有效视图中的 `extra_data.item` **存在**：直接使用它，包括空字符串、`0`、`false` 和 `null`。
2. `extra_data.item` 不存在：使用非空的 `display_name`。
3. `display_name` 为空：使用 `strategy_name`。
4. 都没有：输出 `item = ""`，不会改用 `metric_name`、对象实例名称或告警标题。

第 2、3 步的字段均先取有效视图的 `labels`，同名标签不存在时才取 `extra_data`。
内置 `metric` 通常生成 `labels.display_name`，内置 `strategy` 通常生成 `labels.strategy_name`。

这里有两种不同判断：`extra_data.item` 看字段是否存在；`display_name` 看字符串是否非空。
`display_name = ""` 会转用 `strategy_name`，`extra_data.item = ""` 则保留空串。
代码不对这两个默认名称执行去除空白处理，只有空字符串才触发名称回退。

| 字段 | 当前用途和来源 |
| --- | --- |
| `item` | KAC 的文本字段，按上述顺序生成 |
| `display_name` | `metric` 的展示名称，由 `cleanItem` 计算；也可由来源或自定义规则提供 |
| `metric_name` | 指标字段或按场景计算的指标名称，不作为 `item` 缺失时的备用值 |
| `strategy_name` | `strategy` 生成的策略展示名称，是 `item` 的最后一个默认值 |
| `object` | KAC 的对象展示文本，默认取 `subject_name`，为空再取 `subject_id`；可被 `extra_data.object` 覆盖 |
| `name` | KAC 告警标题，取有效视图的 `title` |
| `model_id / model_inst_id` | 对象模型和实例标识，不直接用作 `item` |

例如，主机 CPU 告警可以同时有 `object = "host-101"`、`item = "CPU使用率"`、
`metric_name = "usage"`。三个字段各有用途，不能按对象实例名称推导 `item`。

代码依据：[KAC 字段转换](../../internal/lifecycle/kachook/message.go)、
[输出字段类型](../../internal/enrich/kingeye/alarm.go)。

## 2. 从 Event 到 KAC 的处理过程

```text
EventSource.enrich.processors 指定步骤和顺序
  → strategy 查询策略，生成 strategy_name
  → metric 查询策略和指标库，cleanItem 生成 display_name
  → 成功或部分成功步骤的补丁按顺序应用到有效视图
  → Event 按 severity 保存 Enrich 结果
  → 创建 Alert 时复制 opening Event 对应 severity 的 Enrich
  → KAC 读取 Alert 的有效视图，按第 1 节生成 item
```

`strategy` 和 `metric` 各自读取策略，不要求先运行 `strategy` 才能执行 `metric`。
但没有配置 `strategy` 时，不会仅因为 `metric` 读过策略，就自动生成 `strategy_name`。
没有配置 `metric` 时，也不会自动执行 `cleanItem`。

内置处理器保存的是 `value.display_name` 以及对应的 `$.labels.display_name` 补丁，
通常不会直接写 `extra_data.item`。读取时优先使用已保存的 `patches`；历史结果只有 `value` 时，
再按相同字段映射生成补丁。只有 `succeeded / partial` 的补丁生效，`failed / skipped` 不生效。

补丁按 `enrich.processors` 的顺序应用，后面的同路径赋值覆盖前面的值。
这决定来源字段、自定义规则和内置 `metric` 同时写 `labels.display_name` 时最终保留哪个值。

代码依据：[Router](../../internal/enrich/assembly/router.go)、
[Chain](../../internal/enrich/enricher.go)、[内置字段映射](../../internal/enrich/kingeye/projection.go)、
[有效视图](../../internal/enrich/view/view.go)、[创建 Alert](../../internal/lifecycle/plan.go)。

## 3. 策略和指标库怎么查询

### 3.1 策略：按 Event 指定的执行版本读取

输入字段：

- `Event.bk_tenant_id`：非空且不能带首尾空白。
- `Event.labels.strategy_id / strategy_version`：JSON 数值类型的正整数，字符串不通过这一步校验。

查询和处理：

1. 读取 Kingeye MySQL 表 `alarm_strategy_execution_revision`，条件为
   `bk_tenant_id = Event.bk_tenant_id`、`strategy_id = labels.strategy_id`、
   `strategy_revision = labels.strategy_version`。
2. 必须唯一命中，并通过快照字节数、摘要、schema 和身份绑定校验。
   不使用当前最新策略替代缺失的历史版本，不用 Event 的 `bk_biz_id` 过滤策略管理业务。
3. 从 `snapshot_json.runtime_config.spec` 读取 `name`、`strategy_item`、`alias_name` 等字段；
   `runtime_config.metadata.labels.object_model_code` 供模型条件使用。
   dataset、指标引用和衍生类型的绑定只使用同一快照的根部 `items`。
4. 按同租户及 `runtime_config.metadata.labels.monitor_template_id` 查询
   `home_application_monitortemplate.id`，取当前表中的 `name` 作为模板名称。
   模板缺失、名称为空或查询报错也会使策略读取失败。

输出：供 `cleanItem` 使用的策略视图。`spec.name` 来自历史快照；模板名称来自读取时的数据库，
两者的数据时间不同，不能把整个读取结果都理解为历史名称。

`StrategyItemProjection` 还要求 `spec.strategy_item.query_configs` 至少一条。
查询缺失 `result_table_id / metric_field` 时分别取 `spec.table_id / field_name`。
Metric 用投影中的首条查询计算名称；带 `metric_ref_id` 的其他查询仍会参与指标引用校验。

代码依据：[输入校验](../../internal/enrich/input.go)、
[执行快照读取](../../internal/enrich/datasources/strategy_revision.go)、
[策略视图及模板名称](../../internal/enrich/datasources/cw_strategy_client.go)、
[查询投影](../../internal/enrich/models/strategy.go)。

### 3.2 指标库：查询名称不等于一定使用名称

`metricDefinitionQuery` 将首查询的 `metric_ref_id / result_table_id / metric_field` 和
策略的 `object_model_code / field_tag / field_name` 转成查询条件。
`Scope` 强制使用原始 Event 的 `bk_tenant_id`，不接受其他租户值。

| 输入情况 | MySQL 查询条件 |
| --- | --- |
| `metric_ref_id > 0` | 表 `metric` 的 `bk_tenant_id` 和 `id` 精确匹配；不再按表名、模型或空间过滤，也不在未命中时改查其他指标 |
| 没有 ID，普通指标 | `kind = native`、`physical_field = metric_field`，且 `result_table_id` 或 `data_label` 等于查询表名 |
| 没有 ID，`field_tag = derived_metric` 且非 `StrategyCloud` | `kind = derived`、`metric_name = spec.field_name` |
| 没有 ID，指定了 `object_model_code` | 额外要求 `metric_object` 中存在同租户、同指标 ID、同 `model_id` 的关系 |
| 没有 ID，需要空间 | 有效 `labels.bk_biz_id > 0` 时使用 `bkcc__<bk_biz_id>`，否则使用该执行快照根部 `space_uid`；同时允许平台空间 `*`，没有可信空间则失败 |

每次最多取两条候选：零条是未命中，一条继续校验，多条是歧义。
查询不按 `enabled / ref_status` 过滤；已经触发的告警仍可读取停用指标的定义。

字段映射是：表 `metric.display_name` → `MetricMetadata.FieldCNName`。
只有 `cleanItem` 选用该值时，它才成为最终 `metric.value.display_name`。
因此不能看到指标库有中文名称，就断定 `item` 一定使用它。

显式引用的 `native / predefined` 指标还必须提供有效的物理字段及结果表或 `data_label`；
`derived` 必须是没有物理主引用的计算配方。类型或引用无效时，Metric 失败。
指标行的身份、`metric_name / metricset_code`、JSON 元数据和模型关系也要通过校验。

PromQL 无法确定完整指标位置时，不查询指标库；没有 ID 的 PromQL 反查出现歧义时，
保留策略名称并记为 `partial + dependency_invalid(metric_catalog)`。
显式 ID 的失败、数据库错误和非法响应仍是失败，不按“可选补充”忽略。
同一次 Enrich 中相同指标查询会复用结果。

代码依据：[指标定位和 cleanItem](../../internal/enrich/processors/metric.go)、
[租户、空间及查询复用](../../internal/enrich/scope.go)、
[指标库 SQL 与响应校验](../../internal/enrich/datasources/metric_client.go)。

## 4. cleanItem 的实际选择顺序

当前 `Metric.Process` 先读策略及查询投影、解析指标引用、补单位，再调用 `cleanItem`。
`cleanItem` 按下面的顺序返回：

1. **首查询是 PromQL**：指标库命中且 `FieldCNName` 非空时取它，否则取 `spec.name`。
   PromQL 的判断是 `query_config_type` 忽略大小写和首尾空白后等于 `prometheus`，
   或 `promql` 去除首尾空白后非空。
2. **不是 PromQL，且 `strategy_item.query_configs` 非空，或 `object_model_code` 为空**：取 `spec.name`。
3. 继续往下才有系统事件、指标库中文名、`metric_field` 三种返回方式。

**当前正常 Metric 入口必须先通过 `StrategyItemProjection` 的非空查询校验，因此非 PromQL
会在第 2 步返回。第 3 步虽然保留在函数中，当前正常入口不会走到。**
其中系统事件分支原本返回 `projection.Name`（优先 `spec.item_name`，其次 `spec.name`），
普通分支原本返回指标库中文名或 `metric_field`；这些不能写成当前普通告警已经采用的规则。

当前名称取值的主要区分是首查询是否为 PromQL，不能仅按主机、K8s、APM 等资源类型判断。
另外，`cleanItem` 即使能够使用 `spec.name`，前面的策略、指标引用或单位查询失败，
仍会让整个 Metric 失败，不会单独保存这个名称作为成功补丁。

代码依据：[Metric.Process / cleanItem](../../internal/enrich/processors/metric.go)、
[StrategyItemProjection / IsPromQL](../../internal/enrich/models/strategy.go)。

## 5. 各类告警的输入与输出

本节都以处理器已配置、输入通过校验为前提。每节的“输出”指 `metric.value.display_name`；
最终 `item` 仍服从第 1 节的 `extra_data.item` 覆盖及 `strategy_name` 回退。
任一场景首查询是 PromQL 时，优先按第 5.6 节处理。

### 5.1 普通目标

- **输入字段**：`spec.name`、`spec.strategy_item.query_configs` 及其指标引用。
- **处理规则**：非 PromQL 时取 `spec.name`，不按 `model_id / model_inst_id` 查询实例名称来填它。
- **输出字段**：`display_name = spec.name`。对象查询结果用于 `object / model_inst_id` 等其他字段。

### 5.2 主机指标和系统事件

- **输入字段**：策略的 `spec.name` 和首查询；主机维度用于资源定位。
- **处理规则**：非 PromQL 时取 `spec.name`。系统事件也先命中第 4 节的非空查询分支，
  不会因表名为 `system.event` 就自动改用 `spec.item_name`。
- **输出字段**：`display_name = spec.name`；不使用主机名或 IP 替代它。

### 5.3 采集任务

- **输入字段**：策略名称和查询；`bk_collect_config_id` 由资源步骤使用。
- **处理规则**：采集配置和实例查询不决定 `cleanItem` 的返回值，非 PromQL 时仍取 `spec.name`。
- **输出字段**：`display_name = spec.name`，不是采集配置名称或实例展示名。

### 5.4 拨测

- **输入字段**：策略名称和查询；`task_id` 由拨测资源步骤使用。
- **处理规则**：目标类拨测和 DATA uptimecheck 都没有独立 `item` 分支。
- **输出字段**：非 PromQL 为 `spec.name`，不取拨测任务或节点名称。

### 5.5 普通 DATA、多查询和函数

- **输入字段**：`spec.name`，查询列表、表达式和函数配置。
- **处理规则**：非 PromQL 时取 `spec.name`，不把多条查询的指标名称自动拼接成展示名称。
- **输出字段**：`display_name = spec.name`。有函数时 `metric_name` 可能为空，
  不影响 `display_name` 的选择；无函数的多查询通常以首查询字段作为兼容 `metric_name`。

### 5.6 PromQL

- **输入字段**：首查询的 `query_config_type / promql / metric_ref_id / result_table_id / metric_field`，以及 `spec.name`。
- **处理规则**：有指标 ID 就按 ID 查；无 ID 时，已有表名和字段优先，缺失部分尝试从唯一序列名推导。
  序列名按冒号分段：至少三段，末段是字段，中间各段以点连接为表名，首段不作为表名。
  不能确定唯一序列或缺少完整位置时，放弃指标库补充。
- **输出字段**：命中非空指标库名称时用它，否则用 `spec.name`。
  多序列表达式如 `a / b` 在没有其他完整定位字段时保留 `spec.name`，不任取一个指标名称。

### 5.7 衍生指标

- **输入字段**：`field_tag = derived_metric`、`field_name`、`spec.name` 和同一执行快照的查询材料。
- **处理规则**：普通非 PromQL 路径仍取 `spec.name`，不因为指标库存在衍生指标名称就改用它。
  带指标 ID 时需先通过第 3.2 节的类型和引用校验。
- **输出字段**：`display_name = spec.name`；`metric_name` 可取父定义的 `field_name`。
  已删除指标唯一标识生成，不再仅因首执行查询位置缺失或非法让 Metric 失败；父身份、目录和声明校验仍保留。

### 5.8 无数据告警

- **输入字段**：`dimensions.__NO_DATA_DIMENSION__`、相同策略快照和查询。
- **处理规则**：无数据标记不增加单独的 `item` 命名分支，仍按 PromQL 或非 PromQL 规则取值。
- **输出字段**：与同一策略的普通告警采用相同名称规则；不会自动在 `item` 加“无数据”。

### 5.9 K8s

- **输入字段**：策略名称和查询；K8s 维度供资源与展示步骤使用。
- **处理规则**：非 PromQL 取 `spec.name`，PromQL 按第 5.6 节。
- **输出字段**：`display_name` 不直接取 `pod_name / container_name / workload_name / cluster_name`。
  即使这些字段已补全，也不会自动成为 `item`。

### 5.10 APM

- **输入字段**：策略名称和查询；应用、服务、实例等维度由其他步骤使用。
- **处理规则**：APM 没有独立的 `cleanItem` 分支，仍按通用规则。
- **输出字段**：非 PromQL 为 `spec.name`，不是 `apm_app_name / apm_service_name / apm_interface_name`。

### 5.11 云资源

- **输入字段**：`StrategyCloud` 的策略名称、查询及云资源维度。
- **处理规则**：非 PromQL 取 `spec.name`，不使用云资源表中的实例名称替换它。
- **输出字段**：`display_name = spec.name`；资源的 `model_name / model_inst_id` 单独处理。

### 5.12 日志指标

- **输入字段**：`monitor_item_type = log`、`spec.name / alias_name` 和日志配置。
- **处理规则**：Metric 的非 PromQL `display_name` 取 `spec.name`。
  `metric_name` 则优先 `alias_name`，其次 `source_config.query_string`，都为空时取 `--`。
- **输出字段**：`display_name` 与 `metric_name` 可以不同。
  Display 生成标题时可以使用 `alias_name / alarm_alias`，这不等于 KAC 的 `item` 也改成该值。

### 5.13 日志关键字

- **输入字段**：`monitor_item_type = log_keyword`、`spec.name` 和 `source_config.query_string`。
- **处理规则**：非 PromQL `display_name` 仍取 `spec.name`，不直接取检索语句；单位为空。
  没有指标 ID、物理表或衍生定义引用时可不查指标库，显式引用失败仍会使 Metric 失败。
- **输出字段**：`display_name = spec.name`。标题中的关键字文本不能作为 `item` 的取值依据。

### 5.14 外部来源与自定义丰富

- **输入字段**：来源清洗后的 `extra_data.item / labels.display_name / extra_data.display_name`，或 `fields / cmdb` 规则的赋值补丁。
- **处理规则**：这些值按第 1、2 节的优先级和步骤顺序参与输出。外部来源如果也配置内置 `metric`，
  仍须提供有效 `strategy_id / strategy_version` 并通过相同策略读取，不因是外部来源而绕过校验。
- **输出字段**：需要明确指定最终 `item` 时，赋值目标是 `$.extra_data.item`；
  只写 `$.labels.item` 不能按正式转换规则覆盖默认 `item`。
  `item` 是固定协议字段，不能放进 `__kac_custom_fields` 作为自定义字段覆盖。

代码依据：[Metric](../../internal/enrich/processors/metric.go)、
[Display 标题](../../internal/enrich/processors/display.go)、
[自定义规则执行](../../internal/enrich/custom/run.go)、
[固定字段保护](../../internal/enrich/kingeye/alarm.go)。

## 6. strategy_name 怎么补最后一个默认值

Metric 的 `display_name` 为空或没有生效时，正式输出尝试使用有效 `strategy_name`。
`Strategy.Process` 按下面顺序生成它：

1. 有模板名称，且原始 `spec.strategy_item.query_configs[0].promql` 非空：只取模板名称。
2. 有模板名称且有 `spec.alias_name`：`模板名称 + "-" + alias_name`。
3. 有模板名称且没有别名：只取模板名称。
4. 没有模板名称：取 `spec.name`，仍为空再取 `runtime_config.metadata.name`。

这里第 1 步只判断 `promql != ""`，与 Metric 的 `IsPromQL()` 判断不同：
只有 `query_config_type = prometheus` 但没有 `promql` 时，Strategy 不会因此跳过别名。

生产 `CWStrategyClient` 要求模板查询成功且名称非空，因此第 4 步是函数保留的后备行为，
不能把“模板查不到”描述成生产读取会继续使用 `spec.name`。
策略依赖失败时，Strategy 可以保留策略 ID/版本并返回 `partial`，但不会凭空生成名称。

代码依据：[Strategy.Process / legacyKACStrategyName](../../internal/enrich/processors/strategy.go)、
[生产策略 Reader](../../internal/enrich/datasources/cw_strategy_client.go)。

## 7. 失败、空值和自定义覆盖

| 情况 | 对 item 的影响 |
| --- | --- |
| 未配置 `metric` | 不生成内置 `display_name`；仍可使用来源、自定义规则或 `strategy_name` |
| 策略身份缺失或非法 | Metric 为 `failed`，记录 `missing_field / invalid_field`；其名称补丁不生效 |
| 策略不存在、快照非法或模板查询失败 | Metric 为 `failed + dependency_invalid(kingeye_strategy)`；再检查来源字段和 Strategy 是否有可用名称 |
| 显式指标引用未命中、引用无效、单位读取失败或数据库报错 | Metric 通常为 `failed + dependency_invalid(metric_catalog)`；即使 `spec.name` 非空，也不会单独应用 Metric 名称 |
| 普通无 ID 查询合法未命中 | 不等同于查询报错；名称仍按 `cleanItem` 的当前分支选择 |
| 无 ID 的 PromQL 可选反查多义 | 保留 `spec.name`，Chain 记 `partial + dependency_invalid(metric_catalog)`，名称补丁仍生效 |
| `extra_data.item` 存在且为 `"" / 0 / false / null` | 正式 KAC 文本分别为 `"" / "0" / "false" / ""`，不改用默认名称 |
| `extra_data.item` 为对象或数组 | 正式 KAC 转换返回类型错误，不把 JSON 拼成文本，也不改用默认名称 |
| Metric 的步骤状态为 `partial` | 已生成的合法名称补丁仍生效；不能仅凭整个 Enrich 是 partial 就断定 item 没有补全 |

例如，自定义 `fields` 用 `set $.extra_data.item = "自定义监控项"`，后续 Metric 即使生成
`labels.display_name = "CPU使用率"`，正式 `item` 仍是“自定义监控项”。
如果自定义规则改的是 `labels.display_name`，则与内置 Metric 按步骤顺序覆盖同一路径。

代码依据：[Metric 失败结果](../../internal/enrich/processors/metric.go)、
[步骤应用条件](../../internal/enrich/enricher.go)、
[KAC 覆盖](../../internal/lifecycle/kachook/message.go)、
[sourceText 类型校验](../../internal/lifecycle/kachook/debug.go)。

## 8. 为什么新 Event 已补全，旧 Alert 的 item 仍没变

每条不同 Event 在策略裁决前执行 Enrich，按 `severity` 冻结结果；重投已经冻结的 Event 不重新计算。
创建 Alert 时，复制真正创建它的 opening Event 对应等级的 Enrich、来源字段和展示快照。
持续触发可以更新内容和时间，但不会自动用新 Event 的 Enrich 替换 `Alert.Enrich`。

因此，指标库名称、模板名称或策略后来变化，新 Event 的预览可能变化，已有 Alert 的 `item`
仍从旧快照生成。`content` 后续重新生成也不能证明 `item` 已刷新。

配置 `reuse_on_active` 时，部分步骤还可复用同租户、同来源、同 fingerprint 的活动 Alert
相同快照等级下的 `succeeded` 结果；`partial / failed` 不复用，预览也不使用这项复用。
检查新 Event 时应同时看 `reused_from_alert_id`，不能只看处理时间。

代码依据：[Event 结果冻结](../../internal/lifecycle/enrich.go)、
[Alert 创建快照](../../internal/lifecycle/plan.go)、
[持续触发内容更新](../../internal/lifecycle/content.go)、[步骤复用](../../internal/enrich/reuse.go)。

## 9. 策略、合并模板和预览如何读取 item

- **策略条件**：`FactView.Field("item")` 先读有效 `extra_data.item`，不存在再取
  `display_name`，为空再取 `strategy_name`；两个名称均按 `labels → extra_data` 读取。
  策略按实际字段类型匹配，不执行正式 KAC 出口的文本转换。
- **合并模板**：读取 `${item}` 使用上述字段视图；配置合并输出字段 `item` 时，
  写入父 Event 的 `extra_data.display_name`。父来源后续的同名标签或 `extra_data.item`
  仍会按第 1 节影响最终输出。
- **Event 丰富记录窗口**：`eventWindowAlarm` 展开有效字段并补 `item`，优先保留已有值，
  缺失时使用 `display_name / strategy_name`。这是该 Event、该等级的结果，不能代替 Alert 快照。
  它会先展开 `labels`，因此只有 `labels.item` 的来源也可能在窗口中显示该值；
  正式 KAC 出口仍按第 1 节，只用 `extra_data.item` 覆盖默认名称。
- **接入调试**：`DebugFields` 只转换清洗后已有字段，不执行 Enrich，也不会调用 `cleanItem`
  或补出默认 `item`。不能把接入调试结果等同于正式丰富后的结果。

代码依据：[策略字段视图](../../internal/policy/view.go)、
[合并模板字段映射](../../internal/policy/merge_template.go)、
[Event 丰富记录窗口](../../internal/controlplane/api/event_enrich_window.go)、
[接入调试字段](../../internal/lifecycle/kachook/debug.go)。

## 10. 排查时核对哪些字段

1. **确认对象和版本**：分别取得 Alert、`trigger_event_id` 对应的 Event、`latest_event_id`
   对应的 Event；记录 `bk_tenant_id / event_source_id / strategy_id / strategy_version`。
2. **看来源字段**：`extra_data.item` 是否存在，值和 JSON 类型是什么。
3. **看对应等级的步骤**：在 `enrich.evaluations[]` 中选择 `severity`，
   在该条目的 `data.processors` 中按名称检查 `metric / strategy / fields / cmdb` 的状态、诊断、值和补丁。
   Alert 保存的是选中等级的 `enrich.processors`，没有外层 evaluations。
4. **重建有效值**：按步骤顺序核对 `labels.display_name / extra_data.display_name`、
   `labels.strategy_name / extra_data.strategy_name` 和 `extra_data.item`，区分字段不存在与空串。
5. **需要查库时**：按同租户、准确策略版本查 `runtime_config.spec.name` 和首查询，
   按第 3 节查指标库及模板名称；不拿当前策略替代历史材料。
6. **比较三处结果**：Event 当前结果、Alert opening Event 快照、KAC 实际存储。
   本地转换或预览成功，只能证明本次计算结果，不能证明兼容写入已完成。

## 11. 本次验证范围

本次只修改本文、术语表和文档索引，未修改业务代码，未提交或推送。
结论来自上述代码；以下已有测试在本次运行通过，均不依赖线上服务：

```bash
go test ./internal/enrich/processors -run '^(TestPromQLPublishedSnapshotEnrichChain|TestPromQLWithoutCatalogKeepsDeclaredIdentity|TestPromQLCatalogAmbiguityFallback|TestPromQLMixedSeriesEnrich|TestExternalMultiQueryMetricNameKeepsLegacyFirstField|TestMetricCatalogQueryLocation|TestStrategyUsesLegacyKACDisplayName|TestStrategyUsesOnlyTemplateNameForPromQL)$' -count=1
go test ./internal/lifecycle/kachook -run '^(TestConvertMessageMapsAlertAndEnrichToKACAlarm|TestK8sFrozenAlertCompatibilityProjection|TestK8sCompatibilityProjectsEffectiveFields)$' -count=1
go test ./internal/policy -run '^TestLegacyCleanedFieldsUsePresenceAndEffectivePatches$' -count=1
go test ./internal/lifecycle -run '^TestEveryEventEnrichedAndOpeningSnapshotFrozen$' -count=1
```

执行时以上命令均经 `rtk proxy` 调用。它们覆盖 PromQL 命中、未命中、歧义和错误，
多查询、指标引用校验、策略名称、KAC 字段转换、策略字段优先级及 opening Event 快照。
未逐项新增或运行所有空值、自定义覆盖和告警场景的测试；这些部分按代码静态核对。
本文 14 个场景的输入、处理和输出结构检查通过；4 篇受影响文档共 119 个本地链接及锚点、
行尾空白检查和 `git diff --check` 均通过。
本次为文档改动，不运行全量 `make check`。
未查询目标环境、未重放或修改线上 Event/Alert，也未验证部署或实际 KAC 写入。
