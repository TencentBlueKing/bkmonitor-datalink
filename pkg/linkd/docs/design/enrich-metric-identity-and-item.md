# KAC 告警的 metric_name 与 item

核对日期：2026-11-05。本轮基于 Linkd `b09207a7a1d9856bd420112e7c8e9b85cc949659` 实施
指标唯一标识移除；下文描述修改后的源码契约，不代表目标环境已经部署。
Kingeye 第一阶段业务读取已在 MR !16721 改用准确执行事实及目录 ID；KAC 字段声明的删除与本轮配套。

规则来自 [Metric](../../internal/enrich/processors/metric.go)、
[目录查询](../../internal/enrich/datasources/metric_client.go)和
[KAC 转换](../../internal/lifecycle/kachook/message.go)。

## 1. 两个字段分别是什么

| KAC 展示名 | KAC 字段 | Linkd Metric 输出 | 含义 |
| --- | --- | --- | --- |
| 告警指标ID | `metric_name` | `MetricValues.MetricName` | 查询物理字段、衍生父定义名或日志文本，按场景生成，允许为空 |
| 告警指标 | `item` | `MetricValues.DisplayName` | 给人看的监控项名称；KAC 转换为空时取 `strategy_name` |

例如普通 CPU 告警：

```json
{
  "item": "CPU使用率",
  "metric_name": "usage",
  "result_table_id": "system.cpu_summary"
}
```

`metric_name` 的页面名称虽然带“ID”，代码不要求它是主键，也不要求它总是物理字段。
目录正式主键是 `metric.metric_id`（表列名 `id`）；策略用 `metric_ref_id` 引用该主键。
检测面的字符串 `query_configs[].metric_id`、目录定义名 `metric.metric_name`、物理字段
`physical_field` 与这两个 KAC 展示字段各有用途，不能互换。

### 已删除的字段

`metric_unique_id` 不再由 Metric 生成，不在 `MetricValues`、KAC `AlarmMessage`、新建 mapping
或内置策略目录中声明。也不生成一个同义字符串字段替代它。原先专为衍生指标兼容标识保留的
`FirstExecutionQuery`、首查询提取和缺失失败分支已删除。

这不清理历史文档或既有 mapping，不重写来源原文，也不禁止用户显式注册同名自定义字段。
没有自定义注册或规则时，系统的新输出不会带该字段；有明确自定义字段时仍按通用目录和权限处理。
外部告警不要求必须具备指标，不能为了删除字段新增必填协议。

## 2. Linkd 从哪里补全

### 2.1 准确策略执行版本

输入是 Event 的 `bk_tenant_id`、`labels.strategy_id`、`labels.strategy_version`，读取同租户
`alarm_strategy_execution_revision` 的准确版本；缺失、非法或读取失败时不以当前最新策略兜底。

`runtime_config.spec.strategy_item.query_configs` 提供声明查询投影。表或字段缺省时分别先用
`spec.table_id` / `spec.field_name`。普通分支使用绑定后的首条声明查询，不把多查询扩成多个展示指标。

根部执行查询仍用于已有的引用绑定、衍生类型确认或描述等用途；它不再向 Metric 提供专用于已删字段
的首查询位置。`bindRevisionDerivedKind()` 校验同一版本的 `parent_metric_id` / `derived` 父身份，
只确认类型，不用成员 ID、表达式或过滤条件覆盖父声明。

代码：[策略读取](../../internal/enrich/datasources/strategy_revision.go)、
[声明投影](../../internal/enrich/models/strategy.go)、
[衍生类型确认](../../internal/enrich/datasources/strategy_derived_binding.go)。

### 2.2 目录与位置

显式 `metric_ref_id > 0` 时按 Event 租户和主键点查，不因声明里的旧表、字段、业务或模型改变 ID。
未命中不反查其他行。`native/predefined` 读取有效主引用，`derived` 读取父定义；后续查询引用失败
同样可能使整个 Metric 失败。

无 ID 的反查用于名称、单位等补充：优先用有效 Event 业务构造 `bkcc__<业务ID>`，否则使用准确执行
快照的 `space_uid`；都没有时拒绝反查。查询同时可见该空间及 `*`，不任意优先其中一行。
普通指标按 `kind=native`、物理字段及表/别名定位；旧式衍生声明按定义名定位。对象约束通过同租户
`metric_object` 集合核验，要求至多一行，不跨模型任选，也不自动挑选 `predefined`。

只有同一执行快照的唯一 alias 和一致字段能够证明对应关系时，声明才可补入执行查询的目录引用；
不按数组位置猜。无 ID 的元数据反查不等于用当前目录重写发布查询。

代码：[Scope](../../internal/enrich/scope.go)、[SQL 查询](../../internal/enrich/datasources/metric_client.go)、
[引用绑定](../../internal/enrich/datasources/strategy_metric_binding.go)。

### 2.3 输出与 KAC 投影

```text
Event + 准确策略版本 + 本次目录元数据
  → MetricValues.metric_name / display_name / result_table_id / unit 等
  → Enrich patch 写入 labels
  → 创建 Alert 时复制 opening Event 对应等级的 Enrich 结果
  → 依处理器顺序合成有效 Alert
  → KAC metric_name / item / result_table_id 等
```

有效视图先取 labels，再取 extra_data。成功或部分成功的补丁按顺序应用，后续赋值可以覆盖前序结果；
失败或跳过步骤不应用补丁。成功生成的空字符串仍参与覆盖，不恢复来源原值。

策略是准确不可变执行版本；目录是本次 SQL 可见行。普通 KAC 投影重放已保存结果，不重新运行 Metric，
修改当前策略或目录不会自动修复历史 Alert。`metric_query_params` 也不再由 Metric 生成，取数由消费侧组织。

显式 `reuse_on_active=true` 只复用同租户、来源、fingerprint、活跃 Alert 快照等级下的 succeeded 结果；
partial/failed、预览、缺快照等级及 `__ALL__` 不复用。以上查询规则描述实际执行 Metric 时的行为。

代码：[结果投影](../../internal/enrich/kingeye/projection.go)、[有效视图](../../internal/enrich/view/view.go)、
[复用](../../internal/enrich/reuse.go)、[KAC 兼容转换](../../internal/lifecycle/kachook/compatibility.go)、
[全局 KAC 写入](../../internal/kaccompat/writer.go)。

## 3. 各类场景的输入与输出

以下以 Metric 已配置、准确策略及必要依赖可读为前提。`cleanMetricName` 的顺序为日志、非云衍生、
PromQL、普通查询；`item` 另见 [item 规则](enrich-item.md)。

### 3.1 普通物理指标，无目录 ID

没有函数时 `metric_name` 取首条声明查询的 `metric_field`，例如 `usage`；表输出到 `result_table_id`。
反查没有记录不必然失败，但定位不完整、数据库错误或普通反查多义不能当作正常无元数据。

### 3.2 native / predefined，有目录 ID

`metric_field` 取主引用 `physical_field`，表优先 `result_table_id`，为空才用 `data_label`。
无函数时指标名取物理字段，不取逻辑定义名。例如定义名 `logical_cpu`、物理字段 `cpu_usage`，
输出 `metric_name=cpu_usage`。未命中、类型未知、字段空或表/别名都空时失败，不用定义名猜位置。

### 3.3 多查询、表达式和函数

普通分支仍选择首条声明查询，不把 `A+B` 编码为身份或多个告警。交换首查询可能改变展示输出。
只要监控项、投影或任意查询的 `functions` 非空，普通 `metric_name` 留空；null、空列表及空字符串
按无函数处理。不能用这一展示值反推完整查询。

### 3.4 衍生指标

非 StrategyCloud 且 `field_tag=derived_metric` 时，`metric_name` 取父定义名；标记可来自声明、同版本
执行成员的父身份校验，或解析单目录 derived 引用后的读取视图。显式目录成功时使用父目录定义名和单位。

```json
{
  "metric_name": "container_memory_resource_limits_ratio",
  "result_table_id": ""
}
```

Metric 不再要求首执行查询提供物理表或字段。缺失、空表、非法首位置不再仅因为已删除字段而让
Metric 失败；仍须通过现有准确版本、父身份、目录类型和声明校验。带物理主引用的 derived 定义仍失败。
StrategyCloud 不进入此分支。

### 3.5 PromQL

`query_config_type=prometheus` 或非空 promql 识别为 PromQL。已有表/字段优先，只补缺失部分；
从唯一冒号序列提取时至少三段，最后一段作字段，中间段用点连接为表，首段忽略。
`bkmonitor:system:cpu_summary:usage` 得到表 `system.cpu_summary`、字段 `usage`。

单序列函数、乘常数、重复同名序列可识别；不同序列不能任选；普通无冒号序列、无名选择器、注释或
结构不完整时不猜位置。该分支在普通函数留空判断之前，因此 rate 包装单序列不会单独清空指标名。
无显式 ID 的可选反查多义保留已有值并标 partial；数据库故障、非法响应、取消和显式引用错误不吞掉。

### 3.6 日志指标与关键字

日志的 `metric_name` 依次取 `spec.alias_name`、`spec.source_config.query_string`、`--`，可以是展示文本。
无显式目录引用且无表时可跳过目录；显式引用错误仍失败。不新增指标唯一标识或强制目录身份。

### 3.7 dataset

这是 Linkd 当前保留的执行快照绑定读取。alias、dataset_code、dataset_semantic.metric_field、维度及
条件必须一致，只接受有效 bk_monitor/time_series 编译查询、完整物理位置且没有额外 filter_dict。
证明不足、歧义或无效来源拒绝读取，不任选首条。绑定只修改读取视图，不改原快照、表达式或函数；
无函数时输出物理字段，有函数时仍执行普通留空规则。
代码：[绑定校验](../../internal/enrich/datasources/strategy_dataset_binding.go)。

### 3.8 主机、K8s、APM、拨测、云资源与无数据

仍按实际查询类型执行上述规则，不将模型、实例、应用、集群或云资源 ID 拼成指标身份。
`dimensions.__NO_DATA_DIMENSION__` 不改变指标名规则；资源定位失败应与 Metric 分开判断。

### 3.9 外部来源、未配置 Metric 与合并告警

已配置 Metric 时 Match 不按来源跳过，仍要求有效策略身份及准确版本。未配置时不自动补指标，
外部无指标告警可以正常存在。内置合并 Event 默认不执行 Metric，也不自动继承子告警指标字段；
显式模板或自定义规则按原契约处理。

## 4. 歧义、缺失和失败怎么处理

显式 ID 未命中不回退；无 ID 反查多行不选最小 ID；PromQL 可选多义与普通反查失败分别处理。
目录的 enabled/ref_status 不在元数据读取中充当当前取数资格判断。多模型适用集合不等于多条指标定义。

已删除字段的首查询位置不再是依赖，其他父引用、目录、版本、取消和声明约束不放宽。
Metric failed 与整个 Enrich failed/partial 不同；其他处理器可成功，缺字段不代表 Alert 未创建。

## 5. 当前实现中需要保留的区别

### 5.1 表与别名

Linkd 显式原生/预定义指标的表优先物理 result_table_id，空时才用 data_label；Kingeye 目录读侧
在原匹配位置上可以别名优先。移除字段不统一两侧优先级，也不因 metric_id 一样而扩大关联。
Kingeye 各业务用自己的已确认分组、多义、启停和排除规则匹配准确事实。

### 5.2 展示不是取数身份

metric_name、item 和 result_table_id 都不能完整表达多查询、函数、聚合、过滤和衍生父公式。
正式目录引用、准确执行历史和消费侧查询各自保留，不创建替代旧拼接字符串的新持久化身份。

## 6. 看到异常值时怎么查

1. 先确认 metric_name 或 item 的实际 key，不把页面上的“ID”当目录主键。
2. 核对实际 Linkd/Kingeye 版本；本地修改不代表已部署，旧进程和在途消息仍可能带旧字段。
3. 从 trigger_event_id、enrich_severity 定位准确 opening Event、租户及策略执行版本。
4. 检查冻结 Metric 的状态、诊断和补丁，区分未配置、失败、partial、成功留空、复用及后续覆盖。
5. 核对同版本声明、父身份和目录引用，不为已删除字段单独要求首执行查询位置。
6. 有目录 ID 时同租户点查，无 ID 时核对空间、物理位置与模型是否多义。
7. 比较有效 labels/extra_data 与 KAC 新文档；重新投影不等于重跑丰富，不修历史数据。

## 7. 验证范围与代码入口

本轮修改同时更新生产代码、样本与回归。定向测试已经覆盖 Metric、策略读取和 KAC 兼容转换；
2026-11-05 完整 `make check` 已通过（普通测试、vet、race、静态分析及项目其他门禁）；
不代表目标环境部署、历史回填或实际 ES 集成服务验收。

- [衍生指标回归](../../internal/enrich/processors/derived_metric_identity_test.go)：无需专用首查询位置，父定义输出和输入不可变。
- [准确版本读取回归](../../internal/enrich/datasources/strategy_execution_query_test.go)：不提取无关首位置，声明和快照保留。
- [真实客户端 Bulk/模板回归](../../internal/kaccompat/removed_metric_field_test.go)：本地 HTTP 服务捕获真实模板、mapping 和 Bulk 请求，验证字段缺失及其余指标事实。
- [普通目录回归](../../internal/enrich/processors/metric_query_test.go)：原生/预定义位置、单位及缺失引用。
- [PromQL 回归](../../internal/enrich/processors/promql_test.go)：单序列、复杂表达式、多义与失败。
- [元数据查询回归](../../internal/enrich/datasources/metric_client_test.go)：租户、主键和反查唯一性。
