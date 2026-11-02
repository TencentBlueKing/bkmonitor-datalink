# KAC 告警的 metric_name、metric_unique_id 和 item

核对日期：2026-11-01。本文记录当前代码行为，不代表目标环境已部署或历史告警已补齐。

- Linkd：主仓 `feat/linkd-dev`，`b6fa6420f75fc53b702071e61ca5c8721615c962`。
- Kingeye：本地 `develop/5.3.0`，`2c1e000b2f0b0049314b32826cb498afbe3a5133`；用于核对 KAC 字段名和消费方式。
- 规则主要来自 [Metric](../../internal/enrich/processors/metric.go)、[指标库查询](../../internal/enrich/datasources/metric_client.go)和 [KAC 字段转换](../../internal/lifecycle/kachook/message.go)。

阅读入口：[字段含义](#1-两个字段分别是什么) · [补全流程](#2-linkd-从哪里补全) ·
[各类场景](#3-各类场景的输入与输出) · [歧义与失败](#4-歧义缺失和失败怎么处理) ·
[当前差异](#5-当前实现中需要保留的区别) · [排查步骤](#6-看到异常值时怎么查)。

## 1. 两个字段分别是什么

### 1.1 KAC 展示名与代码字段

| KAC 展示名 | KAC 字段 | Linkd Metric 输出 | 含义 |
| --- | --- | --- | --- |
| 告警指标ID | `metric_name` | `MetricValues.MetricName` | 按场景生成的指标字段名、衍生指标定义名或日志文本；允许为空 |
| 指标唯一标识 | `metric_unique_id` | `MetricValues.MetricUniqueID` | 通常是 `result_table_id + "." + metric_field`；衍生指标另取首条执行查询 |
| 告警指标 | `item` | `MetricValues.DisplayName` | 给人看的监控项名称；KAC 转换时为空则取 `strategy_name` |

例如普通 CPU 告警可以同时有：

```json
{
  "item": "CPU使用率",
  "metric_name": "usage",
  "result_table_id": "system.cpu_summary",
  "metric_unique_id": "system.cpu_summary.usage"
}
```

`metric_name` 的页面名称虽然带“ID”，代码并没有要求它是主键。
`metric_unique_id` 也没有包含租户、业务、对象实例、策略、聚合方法、表达式或过滤条件。
同一指标在不同实例、不同策略、不同租户中可以得到相同字符串，不能单独用它确定一条告警或一个指标库行。
拼接也没有转义表名或字段名中的点，不能把这个字符串当作可无歧义反解的结构化身份。

KAC 名称依据：Kingeye
[`enrich_field_defaults.py`](https://code.cwoa.net/rd-fy22-canway-kingeye/kingeye/-/blob/2c1e000b2f0b0049314b32826cb498afbe3a5133/src/kingeye/kac/alarm_collect/enrich_field_defaults.py)。
两个字段默认均为 `is_display=False`；实际显示还受租户字段配置影响，本文没有查询环境配置。

### 1.2 不要与这些字段混用

| 字段所在位置 | 字段 | 含义 |
| --- | --- | --- |
| Kingeye 指标库 `metric` 表 | `id`，领域/API 中常称 `metric_id` | 指标库的数值主键 |
| 策略 `query_configs[]` | `metric_ref_id` | 指向指标库主键，Linkd 用它精确查指标库 |
| 策略 `query_configs[]` | `metric_id` | 检测面的字符串标识；当前 Metric 不直接拿它填这两个 KAC 字段 |
| 指标库 `metric` 表 | `metric_name` | 指标定义名；可能与物理字段 `physical_field` 不同 |
| 策略 `query_configs[]` | `metric_field` | 当前查询字段；经绑定后可能是物理字段，也可能是衍生父指标定义名 |
| 指标库 `metric` 表 | `metricset_code` | 指标定义来源，不是上述两个 KAC 字段 |

尤其是 `metric.metric_name` 与 KAC `metric_name`：两处同名，但不保证同值。
普通 `native/predefined` 显式引用会使用 `physical_field`；衍生指标会使用父定义名；日志会使用日志配置文本。

代码依据：[查询模型](../../internal/enrich/models/strategy.go)、[指标查询与输出模型](../../internal/enrich/models/output.go)。

## 2. Linkd 从哪里补全

### 2.1 先定位本次告警的策略执行快照

输入是 Event 的 `bk_tenant_id`、`labels.strategy_id`、`labels.strategy_version`。
按这三个字段读取 `alarm_strategy_execution_revision`：

- `strategy_id` 对应策略拆分发布记录自身的 ID。
- `strategy_version` 对应 `strategy_revision`。
- 缺失或非法时 Metric 失败，不读当前最新策略兜底。

同一份 `snapshot_json` 中有两份不同用途的材料：

| 位置 | Metric 用途 |
| --- | --- |
| `runtime_config.spec.strategy_item.query_configs` | 声明查询投影；普通场景最终使用其第一条查询 |
| 根部 `items[0].query_configs[0]` | `FirstExecutionQuery`；只用于衍生指标的 `metric_unique_id` |

声明投影中，查询缺 `result_table_id` 时先取 `spec.table_id`，缺 `metric_field` 时先取 `spec.field_name`。
后面的“查询已有字段”均指完成这一步及必要绑定后的值。

代码依据：[策略快照读取](../../internal/enrich/datasources/strategy_revision.go)、
[声明投影](../../internal/enrich/models/strategy.go)、[首条执行查询](../../internal/enrich/datasources/strategy_derived_binding.go)。

### 2.2 再解析指标库引用或 dataset 绑定

有 `metric_ref_id > 0` 时：

1. 只用 Event 租户和 `metric.id` 点查。
2. 不以查询中旧的表名、字段名、业务或模型改变所查的指标；查不到不改按旧表名反查。
3. `native/predefined` 取指标库主引用；`derived` 取父定义，具体见第 3 节。
4. 逐条检查声明中的显式引用，后续查询的引用错误也可能使整个 Metric 失败。

没有 `metric_ref_id` 时，指标库反查用于单位、名称等补充：

- 租户固定来自 Event。
- 优先用有效 `labels.bk_biz_id > 0` 构造 `space_uid = bkcc__<bk_biz_id>`；否则取同一执行快照根部 `space_uid`。都没有时不能发起反查。
- 查询同时允许该 `space_uid` 与平台层 `*`，两者不设优先级。
- 普通指标按 `kind=native`、`physical_field`、`result_table_id` 或 `data_label` 查。
- 旧式 `field_tag=derived_metric` 按 `kind=derived + metric_name` 查。
- 指定策略模型时，通过同租户 `metric_object` 限定适用对象；不跨模型任选一行。
- 要求最多命中一行；普通反查不自动选择 `predefined`。

反查到元数据，不意味着会用其主引用重写原查询。
“显式引用解析物理位置”和“无引用反查名称、单位”是两种不同代码路径。

声明缺少引用时，只有同一执行快照能以唯一 `alias` 和一致字段证明对应关系，
才允许从执行查询补 `metric_ref_id`；不能按数组位置猜。`dataset` 另见第 3.7 节。

代码依据：[Scope 作用域与可选反查](../../internal/enrich/scope.go)、
[SQL 与唯一性检查](../../internal/enrich/datasources/metric_client.go)、
[执行快照引用绑定](../../internal/enrich/datasources/strategy_metric_binding.go)。

### 2.3 生成并保存字段，再投影到 KAC

```text
Event + 对应版本的策略执行快照 + 本次读取的指标库元数据
  → MetricValues.metric_name / metric_unique_id
  → Enrich Patch 写入 labels
  → 创建 Alert 时复制 opening Event 对应等级的 Enrich 结果
  → 按处理器顺序合成有效 Alert
  → KAC alarm_event.metric_name / metric_unique_id
```

KAC 投影读取有效视图：同名字段优先取 `labels`，其次 `extra_data`。
成功或部分成功的补丁按处理器顺序应用，后面的同路径赋值可覆盖前面结果；失败、跳过的处理器不应用补丁。
因此来源已有字段、是否配置 Metric、自定义字段处理器及其顺序都会影响最终值。
Metric 成功生成的空字符串也会写入 `labels`，不会因其为空再退回 `extra_data`。

策略来自不可变执行快照；指标库通过 SQL 读取本次可见行，并非按告警版本读取历史指标定义。
结果保存之后，普通 KAC 投影重放已有结果，不重新查指标库。
修改当前策略或指标库不等于历史 Alert 自动补齐。

若 `metric` 步骤显式配置 `reuse_on_active=true`，同租户、来源、`fingerprint` 和活跃 Alert
快照等级下，可以直接沿用该步骤的 `succeeded` 结果，本次不重新执行 Metric。
`partial/failed` 不复用，预览不复用；缺少快照等级或 Event 含 `__ALL__` 判定时也不复用。
因此本节的查询规则描述实际执行 Metric 时的行为，不能认为每条 Event 都会再次查依赖。

代码依据：[内置结果转补丁](../../internal/enrich/kingeye/projection.go)、
[创建 Alert](../../internal/lifecycle/plan.go)、[有效视图](../../internal/enrich/view/view.go)、
[活跃告警结果复用](../../internal/enrich/reuse.go)、
[兼容输出](../../internal/lifecycle/kachook/compatibility.go)、[KAC 写入](../../internal/kaccompat/writer.go)。

## 3. 各类场景的输入与输出

以下均以 Metric 已配置、策略和必要依赖读取成功为前提。
场景顺序沿用 `cleanMetricName`：日志优先，其次非云衍生指标，再是 PromQL，最后普通查询。

### 3.1 普通物理指标，没有 metric_ref_id

- 输入：首条声明查询的 `result_table_id`、`metric_field`；缺省先用第 2.1 节投影规则补。
- `metric_name`：没有函数配置时取 `metric_field`。
- `metric_unique_id`：表和字段都非空才拼 `表.字段`，否则为空。
- 例：`system.cpu_summary + usage` → `usage`、`system.cpu_summary.usage`。

指标库查无记录并不必然失败：完整物理查询仍可能生成这两个字段，单位或中文名按各自回退规则处理。
但缺少完整反查条件、数据库错误或普通反查多义会使 Metric 失败，不能一律视为“没有元数据”。

### 3.2 native / predefined，有 metric_ref_id

- 输入：租户、`metric_ref_id`。
- 查询到 `metric.kind=native/predefined` 后，`metric_field` 使用 `physical_field`。
- `result_table_id` 优先使用指标库 `result_table_id`；为空才用 `data_label`。
- 无函数时 `metric_name = physical_field`，`metric_unique_id = 选定位置 + "." + physical_field`。

例如定义名 `logical_cpu`、物理字段 `cpu_usage`、物理表 `system.cpu_detail`，输出是
`cpu_usage` 和 `system.cpu_detail.cpu_usage`，不会输出 `logical_cpu`。

查不到、类型未知、`physical_field` 为空，或 `result_table_id/data_label` 都为空，均使 Metric 失败。
未绑定主引用的 `predefined` 不用定义名拼一个标识兜底。

### 3.3 一个监控项包含多条 query_configs 或表达式

- 普通非 PromQL、非衍生、非日志场景，仍以首条声明查询生成这两个字段。
- `A+B` 不会被编码进 `metric_unique_id`，也不会把多条查询拼成列表。
- 调换首条查询会改变输出，即使数学表达式含义没有变化。
- 例如第一条 `system.cpu_summary.usage`、第二条 `system.cpu_summary.idle`，无函数时输出
  `metric_name=usage`、`metric_unique_id=system.cpu_summary.usage`。

普通分支只要监控项、投影或任意一条查询的 `functions` 非空，`metric_name` 就留空。
`null`、`[]`、空字符串按无函数处理。`metric_unique_id` 没有同样的函数判断，仍可保留首条查询的 `表.字段`。
所以“告警指标ID 为空、指标唯一标识有值”可以是当前预期行为。

### 3.4 衍生指标 derived

适用条件是非 `StrategyCloud` 且 `field_tag=derived_metric`。
这个标记可以来自声明、同一执行快照中的父指标绑定证明，或解析单条指标库 `kind=derived` 引用后的读取视图。

- `metric_name`：父指标的 `spec.field_name`；显式指标库引用解析成功时更新为父定义 `metric.metric_name`。
- `metric_unique_id`：同一历史执行快照的 `items[0].query_configs[0]`，按 `result_table_id + "." + metric_field` 生成。
- 首条执行查询为 PromQL 时，仍先按第 3.5 节规则解析其位置。
- **允许执行查询的表名为空，保留前导点**；这是此分支对普通拼接规则的明确例外。

例如父指标 `container_memory_resource_limits_ratio` 使用 RSS 和 limit 两个输入：

```json
{
  "metric_name": "container_memory_resource_limits_ratio",
  "result_table_id": "",
  "metric_unique_id": ".container_memory_rss"
}
```

这两个字段分别指向父定义名和首个执行输入，不能按“同一个指标的短名与全名”解释。
换一个首输入，`metric_unique_id` 就可能变化；不同父公式也可能共享同一首输入。

首条执行查询缺失、非法或无法确定字段时 Metric 失败，不跳过它选第二条，也不改用父名称或当前配方。
显式 `derived` 定义若仍带 `result_table_id/data_label/physical_field` 物理主引用，同样失败。
`StrategyCloud` 不进入这个特殊分支，仍按其查询处理。

### 3.5 PromQL

`query_config_type=prometheus` 或非空 `promql` 都会被识别为 PromQL。

- 查询已有 `result_table_id/metric_field` 时保留；只补缺失部分，不重新验证已有值与表达式是否一致。
- 缺失部分尝试从唯一序列名提取：至少三个冒号段，丢掉首段，最后一段作字段，中间段用点连接为表。
- 例：`bkmonitor:system:cpu_summary:usage` → 表 `system.cpu_summary`、字段 `usage`。
- `metric_name` 取解析后的字段；`metric_unique_id` 仍要求表与字段完整。

| 表达式情况（未提供表和字段） | 结果 |
| --- | --- |
| `sum(rate(bkmonitor:system:cpu_summary:usage[5m]))` | 能识别单一序列，输出 `usage`、`system.cpu_summary.usage` |
| 同一序列乘常数、不同标签下重复使用同一序列名 | 仍按同一序列名识别，不区分计算或标签差异 |
| 两个不同序列，如 `usage + idle` 的两个完整序列名 | 不能选一个，表和字段留空 |
| 普通 `process_cpu_seconds_total`，没有冒号分段 | 不能由这套规则推导表和字段 |
| 仅 `{__name__="..."}`、无名选择器、带注释或结构不完整 | 保守放弃推导 |

这是一套有限的身份提取规则，不是完整 PromQL 语义分析。
它位于普通函数判断之前，能识别单序列的 `rate(...)` 不会仅因为有函数就清空 `metric_name`。

没有显式 `metric_ref_id` 时，指标库仅作可选补充：无法定位单指标可以跳过查询；
反查多义可以保留已确定字段并记为 `partial`。数据库故障、非法响应、取消，以及显式引用错误不会被这个规则吞掉。

### 3.6 日志指标与日志关键字

`monitor_item_type=log/log_keyword` 时，`metric_name` 依次取：

1. `spec.alias_name`。
2. `spec.source_config.query_string`。
3. 固定值 `--`。

这里的 `metric_name` 可以是别名或日志检索文本，不要求是物理字段名。
`metric_unique_id` 没有日志专用算法，仍来自首条查询的表和字段；无表时通常为空。
例如别名 `错误日志数`、查询字段 `count`、无表，得到 `metric_name=错误日志数`、`metric_unique_id=""`。

没有显式指标库引用、也没有表的普通日志查询，不因无法查指标库而失败。
显式提供 `metric_ref_id` 后，其引用错误仍会使 Metric 失败。

### 3.7 dataset

声明的 `dataset_code/metric_field` 可以是语义数据集和语义字段，不能直接当物理位置拼接。
Linkd 先在同一执行快照内绑定声明查询与编译查询：

- `alias` 必须唯一，`dataset_code` 与 `dataset_semantic.metric_field` 必须对应声明。
- 校验维度、条件和值一致，目前只接受无需改名的维度与条件映射。
- 要求有效的 `bk_monitor/time_series` 编译查询、非空物理表和字段，且没有额外 `filter_dict`。
- 缺少证明、重复别名、来源无效或绑定冲突，拒绝该策略读取，不取数组第一条凑位置。

绑定后，声明读取视图中的表和字段替换为证明过的物理位置，再按普通、多查询和函数规则输出。
例如语义字段 `cpu_usage` 绑定物理字段 `usage`，无函数时 `metric_name=usage`。
保留的 `functions` 非空时，仍按第 3.3 节留空。

代码依据：[dataset 绑定](../../internal/enrich/datasources/strategy_dataset_binding.go)。

### 3.8 主机、K8s、APM、拨测、云资源和无数据

这些资源场景没有各自独立的 `metric_unique_id` 生成公式。
它们按实际查询属于第 3.1～3.7 节哪一类处理，不把 `model_id/model_inst_id`、应用、集群或云资源 ID 拼进去。

`dimensions.__NO_DATA_DIMENSION__` 不切换上述指标身份规则。
无数据专用逻辑会从取数条件中移除规范 `model_id/model_inst_id`，不据此改写指标字段。
指标库没有关联对象，不必然使 Metric 失败；对象定位是否成功应单独看 Resource 结果。

### 3.9 外部来源、未配置 Metric 和合并主告警

- Metric 的 `Match` 始终为真，已配置时不按来源名跳过；外部来源也需要有效策略身份及对应执行快照。
- 未配置 Metric 时，不会自动执行上述补全；KAC 可以继续投影来源或自定义规则保存的有效字段。
- 内置合并 Event 默认不执行 Metric，也不自动继承某个子告警的 `metric_unique_id`；要看合并模板和来源丰富配置是否显式赋值。

不能把“外部告警有 metric_name”反推为“Linkd 已识别出一个指标库主键”。

代码依据：[Metric 入口](../../internal/enrich/processors/metric.go)、[合并 Event 构造](../../internal/merge/renderer.go)。

## 4. 歧义、缺失和失败怎么处理

| 情况 | 当前处理 |
| --- | --- |
| 显式指标库 ID 与旧表名、字段或模型不一致 | 按同租户 ID 读取；普通物理引用用指标库主引用，模型合法性由资源分支另行处理 |
| 显式 ID 查不到 | Metric 失败，不回退旧物理查询 |
| 无 ID，业务空间与 `*` 各命中一行 | 多义，不优先业务行或平台行 |
| 无 ID，同一物理位置或衍生定义名命中多行 | 返回 `ErrAmbiguousMetricLocator`；不按最小 ID 选一行 |
| 无 ID，PromQL 可选反查多义 | 放弃指标库补充，保留已有字段，Chain 标记该步骤 `partial` 并保留 `metric_catalog` 诊断 |
| 普通查询反查多义、数据库失败、指标库响应非法 | Metric 失败；不统一降级为空元数据 |
| 指标库一行适用多个模型 | 仍是一条指标定义，不等同于多行指标歧义；不选首个模型作唯一对象 |
| `result_table_id` 或 `metric_field` 缺失 | 普通拼接返回空；是否能到达这一步还受前面的指标库查询校验约束 |
| 衍生首条执行查询表为空、字段非空 | 保留 `.字段` |
| 衍生首条执行查询字段无法确定 | Metric 失败 |
| PromQL 多序列且没有显式位置 | 不挑首序列，不推导表和字段；允许留空 |
| Metric 失败，来源已有同名字段 | 失败补丁不应用，来源或其他成功步骤的值仍可能被投影；不能仅凭 KAC 有值判定 Metric 成功 |
| Metric 成功生成空字符串 | 该空字符串参与补丁覆盖，不恢复旧值 |
| 指标当前 `enabled/ref_status` 不允许取数 | SQL 不据此过滤；这里补告警信息，不判断当前取数资格 |

`Metric=failed` 与整个 Enrich 的 `failed/partial` 不是同一层状态。
其他处理器仍可成功；新 Alert 可以保留其他丰富结果。投影时只跳过失败步骤，不能把字段缺失理解为告警未创建。

## 5. 当前实现中需要保留的区别

### 5.1 result_table_id 与 data_label 的优先顺序尚未一致

显式 `native/predefined` 引用中，当前 Linkd 是：

```text
result_table_id 非空 → 用 result_table_id
否则 → 用 data_label
```

Kingeye 当前指标库列表和对象指标候选的 `metric_unique_id` 则优先用 `data_label`。
两者都有值且不相等时，例如 `result_table_id=system.cpu`、`data_label=plugin.cpu`、`physical_field=usage`：

- Linkd 可以输出 `system.cpu.usage`。
- Kingeye 指标库读侧可以输出 `plugin.cpu.usage`。

因此，不能声称当前已保证两侧字符串总相等。
Kingeye 的指标级别反查可以按两种位置查候选，但这不等于所有消费方都能消除字符串差异。
本文记录现状，不在文档任务中修改任一侧规则。

Kingeye 依据：
[`internal_queries.py`](https://code.cwoa.net/rd-fy22-canway-kingeye/kingeye/-/blob/2c1e000b2f0b0049314b32826cb498afbe3a5133/src/kingeye/kmc/kmc/metric/service/internal_queries.py)、
[`metric_catalog.py`](https://code.cwoa.net/rd-fy22-canway-kingeye/kingeye/-/blob/2c1e000b2f0b0049314b32826cb498afbe3a5133/src/kingeye/kmc/operations/metric_catalog.py)。

### 5.2 两个字段不能完整表达取数语义

多查询、函数、PromQL 和衍生父公式都可能只留下一个字段或一个输入位置。
相同 `metric_unique_id` 不保证表达式、聚合、过滤、单位、父指标或资源对象相同。

KAC 已有代码会按 `metric_unique_id.keyword` 聚合告警；这说明它参与按指标归组，不能据此推导它是全局主键。
例如两个不同衍生父指标共用第一条输入时，可以进入同一个字符串分组。
依据：Kingeye
[`instance_statistics.py`](https://code.cwoa.net/rd-fy22-canway-kingeye/kingeye/-/blob/2c1e000b2f0b0049314b32826cb498afbe3a5133/src/kingeye/kac/alarm_api/instance_statistics.py)。

当前 [Metric 实现](../../internal/enrich/processors/metric.go)已不再为新结果生成 `metric_query_params`，
取数由消费侧组织；历史记录可能仍含此字段，当前 KAC 转换也不输出它，见
[历史参数投影测试](../../internal/lifecycle/kachook/message_test.go)。[既有 Enrich 文档](enrich.md)中“继续生成兼容查询参数”的描述
与本基线代码存在差异，不能用它解释新告警。这里的两个文本字段也不能替代完整查询配置。

## 6. 看到异常值时怎么查

1. 先确认字段 key：`metric_name`、`metric_unique_id`，以及作为展示名称的 `item`，不要只看中文标题。
2. 确认告警是否来自 Linkd，以及实际运行版本；本文 commit 只证明本地代码基线。
3. 找到 Alert 的 `trigger_event_id` 和 `enrich_severity`，查看对应 opening Event 的租户、策略 ID 与版本。
4. 查看已冻结的 `metric` 步骤状态、诊断和补丁；确认是未配置、失败、部分成功、成功留空、沿用活跃结果，还是被后续规则覆盖。
5. 对照同版本快照中的声明查询与根部执行查询；衍生指标重点看 `items[0].query_configs[0]`，不要拿当前策略对照。
6. 有 `metric_ref_id` 就查同租户 `metric.id`；无 ID 则核对空间、`*`、物理位置和模型限定是否产生多行。
7. 最后检查有效 `labels/extra_data` 与 KAC `alarm_event`。只重建 KAC 投影会重放已有结果，不等于重新运行 Metric。

## 7. 验证范围与代码入口

本次只新增说明与索引，不修改 Go 行为、提交代码或部署环境。

已执行并通过：

```bash
go test ./internal/enrich/... ./internal/lifecycle/kachook ./internal/kaccompat
```

上述包测试包含普通测试及其默认跳过机制；本次没有开启依赖外部服务的集成测试，没有做在线告警重放、
KAC 页面验收或历史数据回填。文档本地链接、锚点和 `git diff --check` 另行检查。
文档改动未运行完整 `make check`。

| 代码或测试 | 核对内容 |
| --- | --- |
| [metric.go](../../internal/enrich/processors/metric.go) | `Process`、`cleanMetricName`、`metricIdentity`、`metricUniqueID`、PromQL 规则 |
| [metric_query_test.go](../../internal/enrich/processors/metric_query_test.go) | 显式引用、物理字段、别名、缺失引用 |
| [multi_metric_name_test.go](../../internal/enrich/processors/multi_metric_name_test.go) | 多查询取首条、函数使普通指标名留空 |
| [derived_metric_identity_test.go](../../internal/enrich/processors/derived_metric_identity_test.go) | 衍生父名称、首个执行输入、前导点、非法首查询 |
| [metric_catalog_kinds_test.go](../../internal/enrich/processors/metric_catalog_kinds_test.go) | native/predefined/derived 的主引用规则 |
| [business_scope_test.go](../../internal/enrich/processors/business_scope_test.go) | 显式指标库引用不要求来源业务、无旧标记的 derived |
| [promql_test.go](../../internal/enrich/processors/promql_test.go) | PromQL 单序列提取、多序列拒绝和可选反查多义 |
| [log_test.go](../../internal/enrich/processors/log_test.go) | 日志无引用时跳过指标库、显式引用不掩盖依赖失败 |
| [metric_client_test.go](../../internal/enrich/datasources/metric_client_test.go) | 租户、主键优先、位置和空间反查、响应校验 |
| [strategy_dataset_binding_test.go](../../internal/enrich/datasources/strategy_dataset_binding_test.go) | dataset 绑定证明与歧义拒绝 |
