# Enrich 对鲸眼监控策略的读取需求

本文汇总当前 Linkd Enrich 对鲸眼监控策略的依赖。实现依据见 [`internal/enrich/`](../../internal/enrich/)。

## 当前读取方式

Event 在策略裁决前完成 Enrich。`labels.strategy_id`、`labels.strategy_version`、`labels.bk_biz_id` 均须为正整数。
`CWStrategyReader` 使用显式租户和 `labels.strategy_id` 查询 Kingeye MySQL
`alarm_strategy_set_split_record.id`，核对 `source_resource_version == labels.strategy_version`，
并要求记录启用、active、published。默认和覆盖各用自身的拆分主键，不能替换为旧策略 ID 或覆盖的父 ID。

一次有界 SELECT 同时取得版本、状态和 payload；4 MiB 载荷在 SQL 端限长，读取超时 5 秒，
每条记录最多 32 个 resolved 配置和业务身份。单 Event 的各 Processor、各 evaluation 复用读取结果。
不读取 `core_v1alpha1_strategy`、`core_v1alpha1_strategyset`、策略历史表或 Redis 策略缓存，也没有兼容回退。
模板名称仍从按租户隔离的 `home_application_monitortemplate` 读取，仅用于展示。

发布材料的 `resolved_strategies` 是当前编译 DTO，不要求携带旧 Resource 的 kind/api_version；
普通与云字段在适配边界转换为既有的 `CWStrategy` 丰富视图。
DATA 多业务材料先验证各份完整 spec 相等，再按 Kingeye 投影规则使用首项；target 仅接受一个 resolved 项。
配置身份、租户、模板、default 标记和业务名册必须完整一致，不能按事件业务猜选配置。

## 所需策略数据

| 数据 | Enrich 用途 |
| --- | --- |
| `kind`、`spec.config_type`、`spec.monitor_item_type`、`spec.metric_source` | 区分目标类、DATA、日志、云等场景 |
| `bk_biz_id` | 校验告警业务；跨业务时查询业务空间是否为全局业务 |
| `object_model_code` | 选择资源模型和定位 OneModel 实例 |
| `name`、`spec.name`、`spec.alarm_alias`、`spec.data_source` | 策略名称、展示标题、数据来源 |
| `is_default`、`monitor_template_id`、`config_id` | 生成策略标识及详情 URL |
| `spec.strategy_item.expression`、`spec.strategy_item.functions`、`spec.strategy_item.agg_method`、`spec.strategy_item.agg_interval`、`spec.strategy_item.agg_condition`、`spec.strategy_item.agg_dimension`、`spec.strategy_item.query_configs` | 生成指标、维度、过滤条件及 `metric_query_params` |

完整丰富还会读取独立的 MetricLibrary、业务空间、OneModel 实例、模型与拓扑、采集/拨测配置及告警源；这些数据由各自 Reader 提供。

## 版本约束

版本不匹配明确失败，不使用最新版本替代。SplitRecord 是可更新当前态，不是历史快照存储；
记录缺失或已更新时，未保存结果的旧 Event 不能保证可重现。已经持久化的 Event 丰富结果和 EventPlan 继续复用。

普通丰富允许读取当前已发布的覆盖子记录；覆盖编辑复用父记录版本，因此不承诺覆盖历史重现。
`bkmonitor_description` 仍拒绝覆盖并返回 `publication_override_revision_missing`，直到上游提供可区分覆盖修订的稳定身份。
默认文案直接使用同版本 payload 的 `strategy_config`、`resolved_strategies` 和 `runtime_query_configs`，
不再比较当前 Set/Config 表；单位和算法来自同一条发布材料。版本读取及文案限制见
[告警内容生成](alert-content-generation.md)。
