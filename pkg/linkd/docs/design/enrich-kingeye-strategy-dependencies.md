# Enrich 对鲸眼监控策略的读取需求

本文汇总当前 Linkd Enrich 对鲸眼监控策略的依赖。实现依据见 [`internal/lifecycle/enrich/`](../../internal/lifecycle/enrich/)。

## 当前读取方式

新 Alert 创建或等级升级时，Lifecycle 同步执行 Enrich。输入 Alert 的 `labels.strategy_id`、`labels.strategy_version`、`labels.bk_biz_id` 均须为正整数。`CWStrategyReader` 按 `bk_tenant_id + status.bk_strategy_id` 从 Kingeye MySQL `core_v1alpha1_strategy` 读取最新 active 策略；单次 Enrich 中各 Processor 复用读取结果。

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

当前 `strategy_version` 只用于输入校验和结果输出，策略查询仍读取最新 active 记录。延迟消息或历史重放可能使用更新后的策略内容。若要求按事件版本复现丰富结果，需要先确认版本身份、快照保留及读取契约；当前没有策略快照 API。
