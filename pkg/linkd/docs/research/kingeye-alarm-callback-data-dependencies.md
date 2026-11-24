# Kingeye 告警回调数据生成外部依赖盘点

## 调研信息

- 记录日期：2026-08-31
- 调研对象：Kingeye 告警回调数据生成链路的历史源码快照
- 主要符号：`BasicPushAlarmData`、`BasicPushAlarmData.clean_alarm_data()`、
  `DataPushAlarmData.clean_alarm_data()`
- 记录来源：对 Kingeye 告警回调数据结构生成逻辑的源码盘点
- 用途：为 Linkd 后续告警丰富、字段归属、外部依赖收敛和失败语义设计提供输入

本文记录的是 Kingeye/KMC 旧回调链路在历史源码快照中的行为，不是 Linkd 当前实现、目标字段
契约或必须兼容的历史协议。归档时已核对主要符号仍存在，但没有逐项执行外部服务或运行时验证；
涉及 Linkd 的结论仍需结合当前[核心数据模型](../design/define.md)、
[Lifecycle](../modules/lifecycle.md) 设计和真实用例重新确认。

## 结论摘要

`BasicPushAlarmData.clean_alarm_data()` 的清洗和丰富过程真正依赖六类外部数据源：Kingeye/KMC
本地库、Meta HTTP、OneModel/CMDB 投影、Redis/Django cache、监控平台告警 ES，以及仅
`DataPushAlarmData` 使用的 KAPM HTTP。

`AlarmEvent.event_message`、`strategy_detail` 和维度展示等事件本体不计为外部数据，但构造
`BasicPushAlarmData` 时会按 `alarm_id` 重新查询完整事件，并预拉对象模型、业务、集群、模块、
云区域和实例拓扑等映射。也就是说，外部调用不是只在逐字段 `clean_*` 时发生；对象一旦构造，
即使最终没有推送，也可能已经访问多个外部源。

从后续设计角度看，当前链路把来源事实、展示字段、资源拓扑、策略元数据和权限标签一起组装成
一份推送结构。Linkd 不应直接继承这种字段归属，而应逐项确认哪些属于不可变事件事实、哪些属于
可追溯的丰富结果、哪些只是下游展示或权限投影。

## 1. Kingeye/KMC 本地库

| 模型或能力 | 读取内容 | 主要影响字段或行为 |
|---|---|---|
| `AlarmEvent` | 按 `alarm_id` 重新查询完整事件 | 整条清洗的底稿 |
| `StrategyConfigRelateInst` → `StrategyItem` / `StrategyConfig` | `field_name`、`data_source`、`table_id`、`field_tag`、`agg_method`、`agg_interval`、`query_configs`、`is_default` | `item`、`metric_name`、`unit`、`aggregate_func`、`time_interval`、策略 URL、`data_source` |
| `MonitorMetricLibrary` | 指标中文名、单位、`object_model_code`、tag | `item`、`unit`；覆盖 `object_model_code`，进而改变 `model_id`、`model_name` 和维度忽略规则 |
| `MonitorMetric` | 拨测或进程指标的 `description`、`unit` | `item`（`uptimecheck*` / `process.*`）、`unit` |
| `MonitorTemplate` + `strategyconfig_set` | 模板名、`config_type`、`object_model_code`、`data_source` | `strategy_id`、`data_source`，包括云平台的 `云平台监控-{云名}` |
| `CollectConfigRelateInst` | `remote_collecting_host`、`bk_inst_id` | 业务拓扑补偿；决定是否远程采集，以及使用采集实例还是告警实例查询 CMDB |
| `CloudAlarmStrategy` | `cloud_strategy_id` → `alarm_strategy_id` | 云对象策略 URL 的 `item_id` |
| `CloudPlugin` | `cloud_object_model_code_map_by_resource_type()` | `result_table_id` 解析、`data_source` 云前缀、是否使用云策略 URL |

当前源码中的 `is_public_cloud()` 恒为 `False`，因此会进入非公有云路径并查询
`MonitorMetricLibrary`。这个行为属于源实现快照，不应直接转化为 Linkd 的云类型规则。

## 2. Meta HTTP

Meta 请求由内部元数据服务提供。

| 调用 | 接口能力 | 用途 |
|---|---|---|
| `MetaApiSearch.search_obj_model({})` | 对象模型查询 | 拉取全量对象模型的 `object_model_id`、名称、`bk_cmdb_obj_id`、`host_related_field`、`object_model_group_id`，用于 `bk_obj_id`、`model_name`、策略 URL 的 `classId` 和主机关联字段 |
| `get_object_model_mapping()` | 同一对象模型能力 | 把维度中的 `cw_object_model_id` 映射为 `object_model_code` |
| `MetaApiSearch.get_metadata_settings()` | 元数据设置查询 | 获取 `source_name`；失败时回落为默认监控产品名 |

`search_obj_model` 带 120 秒缓存；对象模型 mapping 还会写入 Redis，具体见缓存部分。

## 3. OneModel / CMDB 投影

业务、集群和模块名称映射在 `BasicPushAlarmData.__init__()` 中就会拉取，入口经过
`bk_cc_helper`，底层使用 OneModel。云区域是例外，直接访问 CMDB API。

| 调用 | 底层能力 | 用途 |
|---|---|---|
| `biz_id_name_map()` | `onemodel.search_businesses` | `bk_biz_name`；K8s/APM 覆盖业务名时也使用 |
| `set_id_name_map()` | 按业务扫描 set 实例 | `bk_set_name` |
| `module_id_name_map()` | 按业务扫描 module 实例 | `bk_module_name` |
| `search_inst_cw_biz_id()` | `onemodel.search_instances`，读取实例业务字段配置 | 实例自身的业务属性；主机和服务实例会跳过 |
| `_search_host_ids()` | `onemodel.find_related_host_ids` | 非主机模型根据 `host_related_field` 查找关联主机 |
| `_find_host_biz_relations()` | `onemodel.find_host_biz_relations` | 从主机获取业务、集群和模块关系；清洗代码读取 `bk_biz_id`、`bk_set_id`、`bk_module_id` |
| `search_k8s_instance_document()` | `onemodel.search_tenant_entity_documents` | K8s cluster、namespace、pod 的 `bk_biz_id`、`cluster_name`、`pod_ip` |
| `ActiveAlarmService.build_k8s_inst_id()` | 同上 | 只读取 `cw_object_model_inst_id`，用于 `model_inst_id` |
| `search_cloud_area_msg()` | CMDB 云区域查询 | `bk_cloud_name`；该调用不经过 OneModel |

业务、集群和模块映射使用包装缓存，TTL 为 600 秒；云区域缓存 TTL 为 120 秒。
`cloud_id != 0` 时直接跳过拓扑查询，topology 相关字段保持为空。

## 4. Redis / Django cache

| Key 或缓存 | 内容 | 主要影响字段或行为 |
|---|---|---|
| 对象模型映射缓存 | `object_model_code → {object_model_id}`，TTL 300 秒 | 把维度中的 `cw_object_model_id` 转换为模型 code |
| 动态实例分组缓存 | Redis hash；field 为 `bk_inst_id`，value 为 `{"group_ids": [...]}` | `dynamic_group_id` |
| CMDB / Meta 包装缓存 | 上述 CMDB 和 Meta 查询结果 | 避免每次完整扫描外部数据 |

动态分组读取没有命中时返回空列表。该字段反映的是 Meta 侧预先写入的投影，不是事件本体事实。

## 5. 监控平台告警 ES

`clean_close_reason()` 优先使用事件中的 `event_message.event_description`。事件已经恢复或关闭且
该描述不存在时，才访问配置的监控平台告警日志 ES：

- 索引：按 `end_time` 日期分区的告警日志只读索引；
- 条件：`op_type ∈ {RECOVER, CLOSE}` 且 `alert_id = alarm_id`；
- 结果：读取 `_source.description`；
- 重试：最多 5 次，每次等待 1 秒。

这里访问的是监控平台告警流转日志，不是 Kingeye 自己的 ES。该同步补查会把关闭原因生成和外部
日志索引的可用性、延迟及日期分区绑定在一起。

## 6. KAPM HTTP

该依赖只出现在 `DataPushAlarmData`。当 `result_table_id` 包含 `bkapm` 时：

1. 调用 `KapmApis.monitor_app(namespace=apm_app_name)`；
2. 请求内部应用元数据查询接口；
3. 接口是模糊查询，代码再按 `name` 精确匹配应用；
4. 读取应用 `id` 和 `alias`；
5. 写出 `apm_app_id`、`apm_app_name`、`apm_app_alias`，并重写 `cw_labels`。

应用名来自 `data_source` 中的应用标识，或者从 APM metric result table ID 中解析。

## 7. 影响结果的配置

这些配置不是业务数据源，但会改变清洗结果：

| 配置能力 | 影响 |
|---|---|
| Web SaaS 地址 | `field_extra_info.strategy_name.url` |
| 告警内容格式化开关 | 告警内容和关闭原因的小数位，属于定制行为 |
| 实例业务字段名 | 实例业务归属读取 |
| 告警日志 ES 连接 | 关闭原因补查 |

## 8. 按输出字段观察依赖

### 8.1 主要来自事件本体

以下字段几乎只依赖事件本体和本地转换：

- `alarm_id`（生成新 UUID）、`source_id`、`event_id`、`alarm_time`；
- `content`、`action`、`level`、`object`、`strategy_name`；
- `meta_info`、`where_condition`、`metric_unique_id`；
- `close_time`、`anomaly_begin_time`、`metric_query_params`、`bk_cloud_id`；
- `bk_service_id`（恒空）、`strategy_info`（恒空）、`Namespace`（恒空）。

这里的“几乎只依赖事件本体”不代表这些字段应原样进入 Linkd `AlertEvent`。例如新 UUID 是当前
推送结构的生成行为，不能作为 Linkd 稳定事件身份的设计依据。

### 8.2 必须或可能查询外部源

| 字段 | 外部来源 |
|---|---|
| `source_name` | Meta settings |
| `bk_biz_id/name`、`bk_set_*`、`bk_module_*` | OneModel 业务/实例/主机关系和名称映射；失败时回退 `alarm_dimension_display` |
| `bk_cloud_name` | CMDB 云区域 API |
| `bk_obj_id`、`model_name` | Meta 对象模型 |
| `item`、`unit`、`metric_name` | 策略关联表和指标库 |
| `data_source`、`strategy_id` | `MonitorTemplate` / `CloudPlugin` |
| `dynamic_group_id` | Meta 预写入的 Redis 投影 |
| `close_reason` | 事件 JSON；缺失时查询监控平台 ES |
| `cw_labels` | 已清洗出的 biz、set、model；K8s/APM 分支可能整表覆盖 |
| K8s `cluster_name`、`pod_ip`、`namespace` 等 | OneModel K8s 实例文档 |
| APM `apm_app_*` | KAPM `monitor_app` |

## 9. 调用时机

`BasicPushAlarmData.__init__()` 在逐字段清洗前就会访问：

1. Django cache / Meta，取得对象模型 mapping；
2. OneModel，取得全量业务、集群和模块名称；
3. CMDB API，取得全量云区域；
4. Meta，取得全量对象模型；
5. OneModel，取得当前实例拓扑（`get_biz_topo_message()`）；
6. Kingeye/KMC 本地库，取得策略项和指标库记录。

随后 `clean_alarm_data()` 按 `Alarm` 注解逐字段调用 `clean_*`，并追加
`metric_query_params`、`dynamic_group_id`、`cw_labels` 和 K8s 标签。
`DataPushAlarmData` 再叠加 APM 与云平台字段。

这种调用方式意味着“构造清洗器”和“真正需要某个字段”没有隔离。后续设计如果希望降低依赖和
尾延迟，应区分批量预取、按需查询、共享快照和允许缺失的派生字段，而不是简单把现有调用逐个
迁移到 Go。

## 10. 源码快照中的已知问题

### 10.1 K8s 查询使用了未初始化的租户字段

`fetch_k8s_resource_info()` 使用 `self.bk_tenant_id`，但当前
`BasicPushAlarmData.__init__()` 没有为该属性赋值。K8s 打标时会触发 `AttributeError`，异常被
捕获后返回空字典，因而 cluster、namespace 和 pod 的丰富结果可能为空。
`ActiveAlarmService.build_k8s_inst_id()` 自身会回退到 `get_tenant_id()`，所以两个 K8s 查询入口
的租户行为并不一致。

### 10.2 主机拓扑返回值与消费字段可能不一致

当前 `find_host_biz_relations` 实现主要返回 `bk_host_id` 和 `bk_biz_id`，而清洗代码还会读取
`bk_set_id` 和 `bk_module_id`。因此业务名仍可能补齐，但集群和模块名称经常为空。

这两点只作为源实现风险记录。是否仍存在、应在 Kingeye 修复还是应由 Linkd 采用不同契约，
需要针对各自当前代码和目标用例另行确认。

## 11. 对 Linkd 设计的输入

### 11.1 字段分层

后续字段设计至少应区分：

1. 来源事件事实：来源系统明确陈述且需要原样保留的字段；
2. 标准化结果：由确定性映射产生、可从事件重建的字段；
3. 资源丰富结果：OneModel/CMDB 等外部快照产生的派生字段；
4. 策略元数据：指标库、策略和模板等配置快照；
5. 展示与权限投影：名称、URL、`cw_labels`、动态分组等面向特定消费者的字段；
6. 补偿查询结果：关闭原因等依赖异步外部日志的字段。

这些层次不应继续被无差别地原地写回 `AlertEvent`。丰富结果需要记录数据源、查询键、执行状态
和必要的版本或快照身份，才能解释历史结果并支持重建。

### 11.2 优先收敛的外部依赖

如果目标是减少外部依赖或统一到 OneModel，优先评估两处成本最高的行为：

- 为每个清洗对象预拉全量业务、集群和模块映射；
- 绕过 OneModel 直接全量查询云区域。

但“改成只走 OneModel”不能作为先验目标。指标、策略、用户/权限、动态分组、APM 应用和告警
关闭日志是否属于 OneModel 的权威边界，需要按数据所有权逐项确认。

### 11.3 失败与性能语义

新设计需要显式回答：

- 哪些字段缺失必须拒绝事件，哪些只使丰富结果变为 `partial` 或 `failed`；
- 外部查询是同步阻塞主链路、异步补全，还是使用有版本的本地投影；
- 全量映射、单实例查询和关联遍历分别采用什么批量、缓存和并发上限；
- 缓存 miss、数据不存在、权限不足、超时和上游错误如何区分；
- 所有查询、缓存键和派生结果怎样按 `bk_tenant_id` 隔离；
- 下游消费的是特定 Event 的丰富快照，还是可变化的 Alert 当前投影。

## 12. 未决问题

- 当前回调结构中的每个字段，最终应归属于 `AlertEvent`、Event Enrichment、Alert 投影还是通知
  视图；
- `source_name`、策略 URL、`cw_labels`、`dynamic_group_id` 是否仍是 Linkd 核心领域字段；
- 业务、集群、模块和云区域是否能由一次租户级快照或批量接口提供；
- 关闭原因是否应由来源事件直接携带，或作为异步补偿结果独立落库；
- K8s、APM 和云平台字段应由统一资源丰富端口处理，还是由来源插件负责标准化；
- 指标库和策略配置需要保存哪些不可变 revision，才能重放历史丰富结果。
