# Alert Enrich 现行设计与 KAC 迁移基线

状态：现行设计与迁移状态的唯一权威文档

适用代码：`internal/lifecycle/enrich`、`internal/lifecycle/process`、`internal/config`、`internal/telemetry`
事实基线：当前工作区代码、测试与 `go test ./...` 结果

本文统一描述 Linkd Alert Enrich 的现行契约、实现边界、KAC 行为迁移状态和后续顺序。代码与本文发生差异时，以代码和测试判断当前已实现行为，并同步修正文档。

## 1. 定位与总体状态

Enrich 在 Lifecycle 创建新 Alert 时同步执行，只读取已经 Normalize 的 `domain.Alert`，将补充信息写入：

```text
Alert.EnrichStatus
Alert.Enrich
```

Alert 的标题、内容、标签、维度、主体、来源和生命周期字段保持来源事实，不由 Enrich 改写。等级升级产生的新 Alert 会重新执行 Enrich；同等级更新、恢复和关闭沿用已有结果。

当前可配置生产链由 EventSource 按需组合，核心链为：

```text
strategy → resource → display → metric → source
```

日志、Cloud、K8s、APM 可分别插入 `log`、`cloud_resource`、`k8s`、`apm` Processor。

当前总体成熟度（按代码、测试和当前数据源装配判断）：

| 范围 | 完成度 | 判断 |
| --- | ---: | --- |
| Enrich 运行框架 | 约 90% | Router、配置校验、链式执行、Scope 隔离、状态聚合、diagnostics、生命周期接入、类型化 Context 和 Observation 已完成 |
| BaseTarget / KAC `BASE_COLLECT` | 约 90% | 六个二级分支、生产 Reader、请求内缓存、业务/云区域/标签、派生与 KAC fixture、主要失败矩阵已完成；真实边界样本继续补充 |
| DATA | 约 90%～95% | 已接入 Standard Kafka → Lifecycle → Strategy/Resource/Display/Metric/Source 主流程；普通时序、system 主机、uptimecheck、hardware_、多模型、枚举、衍生指标、PromQL、函数和查询翻译已完成；真实样例继续补充 |
| LOG_METRIC / LOG_KEYWORD | 约 75%～85% | 分类、日志 Processor、`log_theme_logtheme` MySQL Reader、接口入参租户查询、主题回退、关联信息、内容裁剪、主流程装配和派生测试已完成；真实数据库命中与专用查询契约继续验证 |
| Cloud / K8s / APM | Cloud 约 65%～75%，K8s 约 75%～85%，APM 约 65%～75% | 三类 Processor、路由注册、派生身份和主要失败矩阵已完成；CloudResource 与 K8s OneModel Reader 已接入生产装配，APM Application Reader和剩余真实回放继续推进 |
| 生产环境闭环 | 约 50%～55% | BaseTarget 的主要 MySQL、OneModel、关系边和业务拓扑协议已完成第一轮核验；日志、Cloud、K8s、APM 的外部适配器待接入 |

这些百分比用于安排迁移顺序，不属于发布验收指标。

## 2. 已确认边界

### 2.1 输入与副作用

- Enricher 输入固定为 `lifecycle.EnrichInput{Alert}`。
- Scope 保存 `Alert.Clone()`，`Scope.Alert()` 继续返回隔离副本。
- Processor 只执行外部只读查询和确定性转换。
- Processor 不写回 Alert，也不把查询响应塞入 `ExtraData`。
- `SourceRawData` 用于人工追溯，Enrich 不将其作为默认输入或缺字段兜底。
- Enrich 可因消息重投、CAS 冲突或进程恢复重复执行，因此实现必须幂等且没有外部业务写入。

### 2.2 失败语义

- 父 Context 取消立即终止处理。
- 单个 Processor 的 error 或 panic 转换为该 Processor 的 failed envelope，后续 Processor 继续执行。
- 可预期输入错误和依赖故障通过 `ProcessorResult` 与 diagnostics 表达。
- 场景已有核心身份但可选名称、拓扑或标签依赖失败：Processor 返回 `partial`，保留已确认字段。
- 场景核心身份缺失、输入非法、实例未命中或响应身份不匹配：Resource 返回 `failed`，保留模型代码、来源业务和展示回退，禁止生成不可信实例标签。
- Chain 最终状态按现有聚合规则计算：所有适用 Processor 成功为 `succeeded`，所有适用 Processor 失败为 `failed`，其余组合为 `partial`；因此单个 Resource failed 且其他 Processor succeeded 时，Alert 最终状态为 `partial`。
- Enrich failed 不阻断 Alert 创建；Lifecycle 会持久化合法失败 payload。
- 当前不提供查询重试、Enrich 重试、后台补丰富或已创建 Alert 的重丰富接口。

### 2.3 与 KAC 的目标差异

KAC Converter/Cleaner 直接构造旧 Alarm 字段。Linkd 将对应业务含义放入 `Alert.Enrich`，同时保留原 Alert 字段。迁移验收比较字段含义、资源身份、展示文本、指标信息和依赖结果；旧对象布局、线程池、Kafka 推送和生命周期实现不作为复制目标。

## 3. 运行架构

```text
Lifecycle 构造并 Normalize 新 Alert
  → Router 按 Alert.EventSourceID 选择不可变 Chain
  → Scope 保存 Alert 深拷贝和 DataSource Readers
  → 按配置顺序执行 Processor
  → 每个 Processor 返回 status/value/diagnostics
  → Chain 聚合 EnrichStatus 并编码 Payload
  → Lifecycle 校验、降级并写回 Alert.EnrichStatus / Alert.Enrich
  → Repository 创建 Alert
  → FinalHook 输出完整 Alert 快照
```

主要代码位置：

| 关注点 | 文件 |
| --- | --- |
| Lifecycle 端口与调用保护 | `internal/lifecycle/enrich.go`、`internal/lifecycle/enrich_observer.go` |
| Chain、隔离和状态聚合 | `internal/lifecycle/enrich/enricher.go`、`status.go` |
| Payload 和 diagnostics | `internal/lifecycle/enrich/result.go` |
| Scope 与 Reader 接口 | `internal/lifecycle/enrich/scope.go` |
| EventSource 路由与注册 | `internal/lifecycle/enrich/assembly/router.go` |
| Processor | `internal/lifecycle/enrich/processors/` |
| Collect 场景 | `internal/lifecycle/enrich/collect/` |
| Uptime 场景 | `internal/lifecycle/enrich/uptime/` |
| 外部读取适配器 | `internal/lifecycle/enrich/datasources/` |
| 生产装配 | `internal/lifecycle/process/enrich.go` |
| Observation | `internal/telemetry/enrich.go`、`enrich_datasource.go` |

## 4. 配置与装配

Enrich 配置属于 EventSource Release：

```yaml
enrich:
  datasources:
    mysql:
      address: mysql.example.com:3306
      database: kingeye
      username: reader
      password: "..."
    elasticsearch:
      addresses:
        - http://onemodel.example.com:9200
      api_key: "..."
  processors:
    - type: strategy
      config:
        web_saas_module_url: https://kingeye.example.com/
    - type: resource
    - type: display
    - type: metric
    - type: source
```

约束：

- Processor 出现即启用，列表顺序决定执行和输出顺序。
- 同一类型最多出现一次。
- 空列表使用 Noop，结果为 succeeded 且 `processors` 为空。
- 未注册 Processor、缺少必需物理连接或非法配置使 Release 校验失败。
- `strategy`、`resource` 当前要求 MySQL 与 Elasticsearch；`display`、`metric`、`source` 要求 MySQL。
- 凭据只进入 DataSource 配置，redaction 后输出。
- `configs/linkd.pm2.yaml` 当前示例使用空链；生产启用需要在 EventSource Release 中显式配置处理器和数据源。

当前 Router 注册：

```text
strategy
resource
display
metric
log
cloud_resource
k8s
apm
source
```

`log`、`cloud_resource`、`k8s`、`apm` 已完成注册和派生测试；生产链是否启用仍由 EventSource 的 Processor 列表和已装配 Reader 决定。当前生产装配已提供 MySQL、OneModel、LogTheme、CloudResource 和 K8s Reader；APM Application Reader仍待接入。

## 5. Payload 契约

`Alert.Enrich` 顶层固定为：

```json
{
  "processors": [
    {
      "resource": {
        "status": "partial",
        "value": {},
        "diagnostics": [
          {
            "code": "dependency_invalid",
            "dependency": "onemodel"
          }
        ]
      }
    }
  ]
}
```

每个数组元素只有一个 Processor key。Envelope 字段：

| 字段 | 类型 | 规则 |
| --- | --- | --- |
| `status` | `succeeded/partial/failed/skipped` | Processor 状态 |
| `value` | object | 无输出时使用 `{}` |
| `diagnostics` | array，可省略 | 稳定、脱敏的输入或依赖诊断 |

总状态算法：

- 空链：`succeeded`；
- 全部 skipped：`skipped`；
- 排除 skipped 后全部 succeeded：`succeeded`；
- 排除 skipped 后全部 failed：`failed`；
- 其余组合：`partial`。

Diagnostics 原因码：

| code | 含义 |
| --- | --- |
| `missing_field` | 场景必需输入缺失 |
| `invalid_field` | 输入类型、范围或格式非法 |
| `dependency_invalid` | 未取得可用依赖结果 |
| `classification_failed` | 已取得分类输入，但无法选择支持场景 |

Diagnostics 不保存 SQL、URL、凭据、完整请求、响应或原始异常文本。

BaseTarget 降级矩阵：

| 情况 | Resource Processor | 保留内容 |
| --- | --- | --- |
| 模型元数据未命中或身份不匹配，实例已确认 | `partial + dependency_invalid=onemodel` | 实例身份和已确认的资源字段，模型名称留空 |
| 实例／租户／模型响应身份不匹配 | `failed + dependency_invalid=onemodel` | 模型代码、已确认模型上下文和来源业务；不生成实例标签 |
| 实例未命中 | `failed + dependency_invalid=onemodel` | 已确认模型上下文、来源业务、Display 回退 |
| 关联主机未命中 | `partial + dependency_invalid=collect_topology` | 已取得实例、业务和已有云区域字段 |
| 拓扑祖先非法或缺失 | `partial + dependency_invalid=collect_topology` | 已取得实例和业务字段 |
| 可选名称／云区域名称未命中 | `partial` | 对应 ID 与其他资源字段 |
| Context 取消 | Chain 立即返回取消错误 | 不生成伪造 Processor 结果 |

## 6. Processor 输出

### 6.1 strategy

适用场景：需要策略元数据的链路。Strategy 是日志、Cloud、K8s、APM 等场景读取策略配置的共同入口；缺少策略时由具体 Processor 返回对应 dependency diagnostic。

```text
strategy_id
strategy_version
monitor_template_id
strategy_config_id
strategy_name
url
data_source
```

当前策略身份来自 `labels.strategy_id + labels.strategy_version + labels.bk_biz_id`。鲸眼声明式策略从 Kingeye MySQL 读取，并校验租户、策略身份和业务边界。默认策略、云策略与实例策略生成不同 URL。
`strategy_name` 沿用旧 KAC 展示语义：普通策略使用 `monitor_template.name + "-" + spec.alias_name`，PromQL 策略只使用 `monitor_template.name`；没有 `monitor_template_id` 的策略才回退 `spec.name` 或声明式资源名。

### 6.2 resource

适用场景：BaseTarget、DATA、Cloud/APM 等需要资源身份的链路。Resource 只在取得可信模型和实例身份后生成实例级标签；场景级 Processor 可在 Resource 之外补充专用资源投影。

```text
bk_obj_id
bk_inst_id
model_id
model_inst_id
model_name
bk_biz_id
bk_biz_name
bk_set_id
bk_set_name
bk_module_id
bk_module_name
bk_cloud_id
bk_cloud_name
cloud_plat_id
dynamic_group_id
cw_labels
```

当前 `dynamic_group_id` 的代码类型为 `[]string`，值为空数组。后续接入动态分组前需先确认最终类型；旧迁移设计曾记录 `[]int64`，该差异仍未收敛。

### 6.3 display

适用场景：所有需要统一标题、内容、对象和维度展示的链路。日志、Cloud、K8s、APM 可以在 Display 前后提供场景字段，Display 本身继续保持 Alert 原始字段只读。

```text
title
content
object
dimensions
dimension_text
```

结构化 dimensions 保留场景生成顺序；`dimension_text` 使用副本排序和格式化。当前大部分场景的 content 直接继承 `Alert.Content`。

### 6.4 metric

适用场景：DATA、日志和其他带指标查询的链路。Metric 读取策略和 MetricLibrary 投影；Cloud/K8s/APM 的专用指标仍以真实契约为准。

```text
display_name
metric_name
unit
result_table_id
metric_unique_id
aggregate_func
time_interval
where_condition
metric_query_params
anomaly_begin_time
```

Metric 已支持 StrategyItem 投影、单/多 query config、部分衍生指标和查询参数翻译。`anomaly_begin_time` 从 `Alert.ExtraData` 的同名可选字段读取。

### 6.5 source

适用场景：需要告警源元数据的链路。Source 查询告警源名称失败时保留稳定来源 ID，并按公共失败语义处理。

```text
source_id
source_name
meta_info
```

`source_id` 来自 `Alert.EventSourceID`，`source_name` 按租户和来源 ID 查询 Kingeye，`meta_info` 来自 `Alert.SourceEventID`。

### 6.6 已注册但生产适配未闭环的分组

`log`、`cloud_resource`、`k8s`、`apm` 已有 Processor、类型化输出、Router 注册和派生测试。LogTheme、CloudResource 和 K8s 已接入生产 Reader；APM Application Reader仍待接入。

## 7. DataSource 现状

### 7.1 已装配

| Reader | 后端 | 状态 |
| --- | --- | --- |
| `CWStrategyReader` | Kingeye MySQL | 已实现并装配 |
| `BusinessReader` | Kingeye MySQL | 已实现并装配 |
| `MetricReader` | Kingeye MySQL | 已实现并装配 |
| `AlarmSourceReader` | Kingeye MySQL | 已实现并装配 |
| `CollectConfigReader` | Kingeye MySQL | 已实现并装配 |
| `UptimeReader` | Kingeye MySQL | 已实现并装配 |
| `UptimeNodeReader` | Kingeye MySQL | 已实现并装配 |
| `OneModelReader.FindInstance` | Elasticsearch `kingeye_all_instance` | 已实现并装配 |
| `ModelReader` | Kingeye MySQL `object_model_v2` | 已实现并装配 |
| `CollectTopologyReader` | Elasticsearch `kingeye_topo` 与 CMDB 业务拓扑索引 | 已实现并装配 |

所有查询应显式携带 `bk_tenant_id`，区分 found、not found、查询错误和非法响应，并传播 Context。

### 7.2 接口存在，生产能力待补

| 能力 | 当前事实 | 影响 |
| --- | --- | --- |
| DynamicGroup Redis | 缺失 | `dynamic_group_id` 固定为空数组 |
| LogTheme 生产 Reader | 已装配 | MySQL Reader 查询 `log_theme_logtheme`，使用调用方传入的 `bk_tenant_id`，只投影 `bk_tenant_id`、`log_theme_id`、`log_theme_name`；生产装配接入 `enrich.Sources.LogTheme` | |
| CloudResource | 已装配 | MySQL Reader 已接入 `enrich.Sources.CloudResource`，按接口入参租户和 `cloud_id + type + instanceid` 查询并关联云平台名称；当前 CloudResource 表为空 |
| CloudPlugin / SysSetting | 已移出当前 Cloud 主链 | 当前 Cloud/VMWARE 契约不依赖 CloudPlugin、SysSetting，不阻塞资源丰富 |
| K8s Reader | 已装配 | `OneModelK8sReader` 复用 `OneModelClient` 查询 `kingeye_all_instance`，按租户、模型和 K8s 属性过滤并复核实例身份；不直接读取 `kmc_k8s_*` |
| APM application API | 缺失 | APM 应用候选查询无法接入真实 Application 服务 |

### 7.3 OneModel 实例协议

`event_sources[].enrich.datasources.elasticsearch.index_prefix` 配置 CMDB 业务拓扑索引前缀，默认 `bk_monitor_base_`；实例 alias `kingeye_all_instance` 与投影边 `kingeye_topo` 使用固定契约名。

当前 `OneModelClient` 固定查询：

```text
kingeye_all_instance
```

查询约束：

- 根字段：`bk_tenant_id`、`model_id`、`model_inst_id`；
- 来源属性：nested `attribute_values` 的 keyword/long/double/boolean/datetime/ip 类型槽；
- 响应复核：租户、模型、实例和 `entity_uid=model_id|model_inst_id`；
- 来源属性从 `attributes` 读取，与根字段合并时根字段优先。

### 7.4 模型与拓扑协议

真实 Kingeye MySQL 与 OneModel Elasticsearch 第一轮核验已经完成：

- `kingeye.object_model_v2` 具备 Reader 所需字段，`bk_tenant_id + model_id` 使用联合唯一索引；
- `kingeye_all_instance` 的租户、模型、实例、`entity_uid` 及 nested 类型槽符合当前 Reader；
- 主机 IP 使用 keyword 槽，云区域使用 long 槽，组合查询可唯一命中代表性主机；
- `kingeye_topo` 中 MySQL、Redis 到主机的关系边可以定位唯一关联主机；
- CMDB 业务拓扑节点与 membership 的身份、业务范围和祖先路径符合 Reader；
- `cw-Business` 是对象模型分组代码，业务对象模型代码为 `cw-biz`；拓扑业务节点通过 `bk_obj_id=biz + model_id=cw-biz` 识别；
- 同一主机允许多条 membership。BaseTarget 按祖先节点 `sort_order` 与路径 ID 建立稳定顺序，并投影首条路径的单值 set/module；membership 上限为 64，祖先节点上限为 256。

服务实例 `cw_biz_id` 继续按 KAC 旧逻辑使用 long 属性槽。当前环境缺少服务实例样本，该类型契约列为保留验证项。双租户隔离与更多反向关系样本同样进入后续环境验证。

## 8. Scope 与请求内复用

Scope 当前按稳定查询键复用：

- CW Strategy、Business、AlarmSource；
- OneModel Instance；
- Model、MetricLibrary、CollectConfig、UptimeTask/Node；
- RelatedHost 与 HostTopology；
- Collect/Uptime/BaseTarget 场景结果。

`InstanceQuery.AttributeFilters` 排序后编码，字段顺序不影响缓存键；父 Context 已取消的查询结果不写缓存。`Scope.Scenario` 也不缓存 `context.Canceled` / `context.DeadlineExceeded` 错误，同一 Scope 后续调用能够重新解析。

`collect.Enrich` 与 `uptime.Enrich` 使用 `Scope.Scenario`，使 Resource 与 Display 共享同一次解析和外部查询结果。当前不提供跨 Alert TTL 缓存、预热或刷新机制。

## 9. 场景覆盖与 KAC 差距

KAC 当前可达主分类：

```text
BASE_COLLECT
DATA
LOG_METRIC
LOG_KEYWORD
UPTIME_CHECK
VMWARE
```

`ACCESS_OBJECT` 有旧实现但缺少当前分类入口证据，继续排除。

### 9.1 BaseTarget 分类与场景边界

BaseTarget 使用统一分类入口，当前优先级为：

```text
UPTIME_CHECK
→ MONITOR_SOURCE
→ COLLECT_TASK
→ NO_DATA
→ SYSTEM_METRIC
→ BASIC
```

Kingeye 原始 `event.tags` 与 Linkd 标准事件属于两个协议层。已通过本地 `RawEventMessage → StandardCleaner → Event → Lifecycle Alert → Enrich` 测试验证：当上游把 `__NO_DATA_DIMENSION__=true` 写入标准 `dimensions` 时，标记传至 `Alert.Dimensions` 并命中 NoData；原始 `event.tags` 单独携带该标记时，StandardCleaner 仅在 `SourceRawData` 留存原文，Enrich 仍按后续 BaseTarget 规则分类。真实上游的 tags → 标准 dimensions 映射契约和端到端样本仍待确认。

触发条件：

| 分支 | 条件 |
| --- | --- |
| UptimeCheck | 非 DATA 且策略 `object_model_code == cw-web_service` |
| MonitorSource | `bk_inst_id` 有值，或 `obj_model_id + obj_model_inst_id` 同时有值 |
| CollectTask | `bk_collect_config_id` 有值 |
| NoData | `__NO_DATA_DIMENSION__` 为真值；`model_id` 是模型代码且必须与策略 `object_model_code` 一致，`model_inst_id` 是资源定位所需实例 ID |
| SystemMetric | 策略 `object_model_code == cw-Host`，按主机 ID 或 IP + 云区域定位 |
| Basic | 其余 BaseTarget；保留模型和来源业务并跳过实例查询 |

Resource 与 Display 共享同一个场景解析结果。MonitorSource、NoData、SystemMetric、Basic 进入 `basetarget.Enrich`，CollectTask 和 UptimeCheck 分别进入 `collect.Enrich` 与 `uptime.Enrich`。新增信息只写入 Processor Value，Alert 来源字段保持只读。

### 9.2 覆盖矩阵

`__NO_DATA_DIMENSION__` 是 NoData 分类标记，`model_id + model_inst_id` 只承担资源定位。`basic_inst_no_data_report` 的 KAC 样例名称描述告警来源语义，其维度缺少 NoData 标记，按 KAC 与 Linkd 分类规则进入 SystemMetric。NoData 另有携带标记和规范身份字段的独立脱敏 fixture。

| 场景 | 当前完成度 | 已实现 | 主要差距 |
| --- | ---: | --- | --- |
| `BASE_COLLECT/MONITOR_SOURCE` | 90% | 独立 Resolver、两类身份、模型/实例/拓扑、Resource/Display、`cw_labels`、五 Processor payload、KAC `base_collect_monitor_source_01` 脱敏差异 fixture | 服务实例真实样本 |
| `BASE_COLLECT/COLLECT_TASK` | 90% | CollectConfig、实例定位、模型/拓扑生产 Reader、OneModel 执行主机云区域回填、Resource/Display、`cw_labels`、KAC 远程 MySQL 脱敏 fixture | 动态分组、其他采集类型样本 |
| `BASE_COLLECT/NO_DATA` | 90% | `__NO_DATA_DIMENSION__` 分类、`model_id + model_inst_id` 规范身份、策略模型一致性校验、实例/拓扑、通用 KAC title/content/object、五 Processor payload 与失败矩阵 | 真实来源样本复核 |
| `BASE_COLLECT/SYSTEM_METRIC` | 90% | 主机 ID/地址定位、实例唯一性、模型/拓扑、多 membership 稳定首路径、Resource/Display、`cw_labels`、两份 KAC 脱敏差异 fixture | 组合 `event.target` 差异样本 |
| BaseTarget `UPTIME_CHECK` | 85%～90% | Task/Node 双 ID、协议维度、删除任务回退、OneModel 业务名称与可信目标主机云区域名称、业务/任务 `cw_labels`、两份 KAC 脱敏 fixture | 非主机目标的云区域名称、真实外部样本 |
| 顶层 `UPTIME_CHECK` | 65%～70% | 分类和共享 Uptime 模块 | RawEvent 闭环与入口差异验证 |
| `BASE_COLLECT/BASIC` | 90% | 跳过实例查询、模型/来源业务、Resource/Display、业务 `cw_labels`、派生 KAC 通用 Cleaner 五 Processor fixture、模型未命中/错误边界 | 真实可达 KAC 样本 |
| `DATA` | 90%～95% | Standard Kafka → Lifecycle → Strategy/Resource/Display/Metric/Source 主流程、`basic_data` KAC 对照、system 主机与 uptimecheck、普通／hardware_／多模型 canonical OneModel 身份、枚举／衍生／多指标／PromQL／函数指标、特殊查询翻译、内容算法和派生失败矩阵 | 真实普通及多模型 KAC 样例、双租户与真实外部失败样本 |
| `LOG_METRIC` | 75%～85% | 分类、标题、`log` Processor、`log_theme_logtheme` Reader、接口入参租户查询、主题/查询字段、空资源、日志标签、Router 注册、内容裁剪和 KAC 样例字段对照 | 真实数据库记录命中、专用查询参数细节和完整 JSON fixture |
| `LOG_KEYWORD` | 75%～85% | 分类、标题、`log` Processor、`log_theme_logtheme` Reader、接口入参租户查询、查询语句、关联信息、空资源、日志标签、命中/无数据裁剪和 KAC 样例字段对照 | 真实数据库记录命中、专用 URL/查询细节和完整 JSON fixture |
| `VMWARE/Cloud` | 65%～75% | Cloud 类型、期望 Kafka 输入、旧 KAC 表结构与读取字段、策略读取、CloudResource MySQL Reader、复合身份、资源投影、业务标签、响应身份校验、Router 注册和主流程验证；已用 Navicat 转发连接验证空数据查询 | 真实 CloudResource 记录和完整 fixture | |
| K8s 横切能力 | 75%～85% | 输出模型、Processor、Router/配置注册、`OneModelK8sReader` 生产装配、`kingeye_all_instance` 实际数据核验、Cluster/Namespace/Service/Workload/Pod/Container/Node 身份映射、Workload-Pod 特殊映射、实例/业务/集群名称和 `cw_labels` 投影、响应身份校验、Context 输出和完整 Processor 链测试 | 真实 Kafka Event 回放、Cluster/Namespace 多业务优先级、PV/PVC 接入和完整 JSON fixture |
| APM 横切能力 | 65%～75% | 输出模型、APM 表判断、`apm` Processor、应用名推导、ApplicationReader、精确匹配、租户校验、别名、application/service/service-instance 身份、Resource 联动、维度、业务/应用标签、Reader 错误/重复匹配矩阵和完整链路测试 | 真实 Application Client、重复应用最终产品决策和完整 JSON fixture |

### 9.3 Collect

生产调用链：

```text
Resource ─┐
          ├─ collect.Enrich
Display ──┘
```

当前链路：

```text
Alert.Dimensions.bk_collect_config_id
→ CollectConfigReader
→ bk_object_code + bk_inst_id
→ OneModelReader.FindInstance
→ resource + display
```

已实现：

- 租户隔离；
- 字符串和数字采集任务身份；
- 删除状态过滤；
- `model_inst_id` 根字段查询；
- 服务实例通过 `cw_biz_id` long 属性槽增加业务条件；
- Resource/Display 场景复用；
- 基础 `cw_labels`；
- 主机实例从 OneModel `attributes.bk_cloud_id/bk_cloud_name` 投影云区域；
- 需要拓扑的远程或非主机采集，可由相同云区域 ID 的关联主机补齐名称；
- 非主机资源已有业务并跳过拓扑时，按告警 `bk_host_id/bk_target_host_id + bk_target_cloud_id`，或 `bk_target_ip + bk_target_cloud_id` 查询 OneModel `cw-Host`，补齐同云区域名称；
- Alert 不可变、未命中和依赖故障测试。

生产限制：

- 云区域名称全部来自 OneModel 资源实例或执行主机实例；不引入 CMDB API。身份不完整、主机未命中、查询错误或区域 ID 冲突时保持空名称；
- 动态分组进入独立后续能力；
- CollectConfig namespace 和前导零身份规则保留真实样本确认；
- 服务实例 `cw_biz_id` 属性槽保留真实样本确认。

### 9.4 Uptime

生产调用链：

```text
Resource ─┐
          ├─ uptime.Enrich
Display ──┘
```

当前链路：

```text
Alert.Dimensions.task_id
→ UptimeTaskReader
→ 数据库 task_id 查询
→ 记录主键 id 作为 model_inst_id
→ UptimeNodeReader(node_id=plat_id:ip)
→ resource + display
```

已实现：

- Task/Node 租户隔离；
- Uptime 双 ID 语义；
- HTTP、TCP、UDP、ICMP 专用目标维度；
- 业务名称通过任务 `bk_biz_id`（任务缺失时用 Alert 来源业务）读取同租户 OneModel `cw-biz` 实例；
- `bk_target_ip/target_host + bk_target_cloud_id` 同时提供可信 IP 和云区域时，读取同租户 OneModel `cw-Host`，校验 IP/区域后补充云区域名称；云区域 `0` 保留；
- `cw_labels` 使用有效业务 ID，以及已定位任务的 `cw-web_service|<数据库任务主键>`；删除任务时只输出业务范围标签；
- 两份 KAC 拨测脱敏 fixture 覆盖有任务和缺失任务的五 Processor payload；
- 删除任务 `<task_id>（该拨测任务已被删除）` 回退；
- Resource/Display 场景复用和 Alert 不可变测试。

生产限制：

- 非主机目标或仅有云区域 ID 的拨测无法从主机实例取得云区域名称，保留 ID 和原维度；
- DATA uptimecheck 由 DATA 场景迁移阶段接入；
- `task_id=0` 输入语义保留真实样本确认；
- 完整 RawEvent 到 Lifecycle 的入口差异验证继续补充。

### 9.5 DATA

当前已覆盖普通时序 `cw-Others`、system 主机与 uptimecheck 的基础资源定位：

- `MetricReader` 投影 `object_model_code`，用于 DATA 对象分类；
- MetricLibrary 同步读取 `value_mapping`，DATA 单指标且无函数时将匹配数值追加枚举名称，多指标、函数和衍生指标保留原始内容；
- 策略无 `object_model_code` 时，按指标库模型回退；
- 无实例身份的 `cw-Others` 告警保留模型代码、模型名称、来源业务和 `cw_labels`；
- 业务名称可从同租户 OneModel `cw-biz` 实例读取；
- Display 使用空 object，保留 Alert 内容和指标标题；
- KAC `basic_data` 转换 fixture 验证完整五 Processor payload；
- system 主机按 `bk_target_host_id` 优先，随后以 `bk_target_ip + bk_target_cloud_id` 查询 OneModel `cw-Host`；
- uptimecheck 结果表复用 `uptime.Enrich`，按 `task_id` 定位任务数据库主键；
- 普通 OneModel 单模型在标准 Alert 明确提供 `model_id + model_inst_id` 且模型与 MetricLibrary 一致时查询 OneModel；`model_inst_id` 按不透明字符串原值使用，实例成功才生成实例标签；
- `data_ordinary_onemodel` 是依据统一实例身份规格构造的派生 fixture，真实普通实例 KAC 样例仍待取证；
- 上游缺少显式 canonical 模型／实例身份时保留模型和业务并返回 Resource partial；身份冲突或查询未命中时 Resource failed。

当前仍缺：

- system 主机和 uptimecheck 的身份规则来自 Kingeye Converter 源码，目前由合成五 Processor 测试保护；现有 KAC `source_data` 只有 `basic_data` 一份 DATA 原始样例；
- hardware_ 与多模型结果表已经收敛为 canonical `model_id + model_inst_id` 定位；旧数值模型主键字段只作迁移诊断，真实样例仍待补充；
- Cloud、APM、K8s 的专用资源定位进入各自场景；
- 枚举指标 `value_mapping` 已从 MetricLibrary 读取，并在 DATA 单指标、无函数场景中为展示 content 的数值追加 `(mapped_value)`；多指标、函数和衍生指标保持原始内容；
- 当前值、阈值、算法等级和单位生成展示内容已支持静态阈值场景：算法级别的 `algorithmUnit` 优先，`NONE` 清空单位，MetricLibrary `bytes/percent/percentunit` 使用 KAC 映射；枚举映射继续按精确数值处理；
- 环比、同比和无数据内容已按 Kingeye 的算法分支建立确定性转换：环比/同比只处理可定位的数值片段，无数据文案保留原文；
- custom event、alert、FTA 的 Metric 查询投影已覆盖字段、结果表和事件过滤器翻译；
- PromQL 查询保留原始 PromQL，并按 Kingeye 的冒号段规则提取结果表与指标字段；
- 函数指标保留策略级和 query 级 `functions`，Metric name 为空，并关闭函数场景的枚举映射；
- 派生失败矩阵覆盖策略未命中／错误、MetricLibrary 未命中／错误、模型未命中、实例依赖错误、非法 canonical 身份、混合 Processor 最终 partial 和 Context 取消；
- 普通时序、多指标、衍生指标、custom event、alert、FTA、日志查询等 fixture。


### 9.6 LOG_METRIC / LOG_KEYWORD

已完成第一轮日志纵切：

- 新增 `log` Processor 和 Router 注册；
- 从策略 `source_config` 读取 `log_theme_id/log_theme_name/query_string`；有 `LogThemeReader` 时按租户读取主题名称并复核主题 ID，Reader 未配置时保留策略名称回退；
- 日志指标使用查询语句作为 metric 展示名，日志关键字为空查询时回退 `--`；
- 日志场景使用空 Resource 语义，Display 使用空 object；
- 关键字从 `Alert.ExtraData.log_related_info` 读取关联信息；
- 日志 Display 内容先拼接 `Alert.SubjectName`，再执行日志内容裁剪：日志指标移除“关联信息”尾部；日志关键字命中内容生成“匹配到【query】关键字次数 ...”，无数据内容生成“【query】关键字 ...”；`Alert.SubjectName` 为空时保持原内容形态；
- 输出日志主题、查询语句、关联信息和 `cw_labels`；
- 已读取 KAC `log_metric_data.json` 与 `log_keyword.json` 的真实输入/清洗输出，补充同字段语义的完整 Processor 链测试；
- 派生输入测试覆盖日志 Processor 的正常、关键字空查询和主题缺失边界。

仍需：

- `LogThemeReader` 已接入 MySQL 主流程，按接口入参租户与 `log_theme_id` 查询 `log_theme_logtheme`，投影主题 ID和名称；本次使用 system 租户进行验证；Strategy 仍沿用当前 `CWStrategyReader` 逻辑；真实记录命中待数据库连接恢复后验证；
- 专用日志查询参数、URL 和真实 KAC fixture。

### 9.7 Cloud、K8s、APM

三类专用 Processor 已完成第一轮代码接入、Router 注册、类型化输出和派生测试。生产装配现状以第 7 节为准：LogTheme、CloudResource 和 K8s 已进入 MySQL/OneModel Sources，APM Application Reader仍保持可注入边界。

APM 第一轮纵切已完成：

- 新增 `APMApplicationReader`，按租户和应用名称读取候选应用；
- 精确匹配应用名称后投影应用 ID、别名和业务；
- 生成 application/service/service-instance 模型身份与 `cw_labels`；

- 新增 `apm` Processor 与 Router/配置注册；
- 识别 `bkapm` 结果表；
- 应用名称按 `data_source=应用-<name>` 或 `_bkapm_metric_<name>` 推导；
- application 身份为 `<app_id>`，service 身份为 `<app_id>|<service>`，service-instance 身份为 `<app_id>|<service>|<instance>`；
- 投影 application/service/instance/span/net_peer 字段；
- 缺少应用 ID、应用未命中、非精确匹配或租户不匹配返回 partial；真实 Application Client 待接入，Reader 缺失时保留派生应用名称和维度投影；
- Resource Processor 已按 APM 维度生成 application/service/service-instance 资源身份，并补齐业务/应用标签；
- 已覆盖 ApplicationReader 未命中、非精确匹配、租户不匹配、Reader 错误、缺少应用 ID、重复精确匹配和完整 Processor 链测试；
- 真实 APM 数据按当前迁移决策跳过，真实 Application Client 保留待环境接入；
- 已补完整 Processor 链测试、application/service/service-instance 三种身份 payload、应用非精确匹配、租户不匹配、Reader 错误和重复精确匹配矩阵；重复匹配当前沿用 Reader 返回顺序的第一条精确结果。

Cloud 第一轮纵切已完成：

- 新增 `CloudResourceReader` 和 `models.CloudResource` 最小投影；
- 新增 `cloud_resource` Processor 与 Router/配置注册；
- 使用 `cloud_id + type + instanceid` 复合身份查询云资源；
- 每次查询显式携带租户，并校验响应租户、云平台、资源类型和实例 ID；
- 投影云资源名称、云区域、对象模型、模型实例、业务和 `cw_labels`；
- `CloudResourceReader` 已有 MySQL 生产实现，按调用方租户、`cloud_id`、`type`、`instanceid` 查询 `cloud_application_cloudresource` 并关联 `cloud_application_cloud`；当前数据库 CloudResource 为空，成功资源投影尚未获得真实记录；
- 已覆盖成功路径和响应身份不匹配失败路径。

Cloud 主链已完成收敛：业务 ID统一使用 Kafka `labels.bk_biz_id`，CloudResource MySQL Reader 已接入主流程。CloudPlugin、SysSetting 和额外 CloudResource 业务 ID来源已从当前 Cloud/VMWARE 主链移除；后续补充真实 CloudResource 记录和完整 fixture。

### 9.8 K8s

K8s 已完成 OneModel Reader 主流程接入，复用统一实例索引 `kingeye_all_instance`；不直接读取 `kmc_k8s_*` 专用索引。

已完成：

- `k8s` Processor、Router/配置注册和 `OneModelK8sReader` 生产装配；
- K8s Reader 返回真实实例后，投影 `model_id`、`model_inst_id`、`cluster_name`、`bk_biz_id`、`bk_biz_name` 和确定的 `cw_labels`；
- 业务选择对齐旧 KAC：Namespace `bk_biz_id` 优先，Cluster `bk_biz_id` 回退，随后使用 `bk_biz_ids` 第一个值，最后回退 Kafka `labels.bk_biz_id`；
- `OneModelK8sReader` 通过 `OneModelClient` 按租户、对象模型和类型化属性过滤；
- Cluster、Namespace、Service、Workload、Pod、Container、Node 的身份维度映射；
- Workload `workload_kind=Pod` 改查 `cw-K8s_Pod` 的旧 KAC 特殊规则；
- Reader 响应租户、模型、实例和 `entity_uid` 复核；
- `EnrichContext.K8s` 输出；
- 缺字段、Reader unavailable、响应身份异常和完整 Processor 链测试；
- 本机 ES `127.0.0.1:19200` 实际核验：`kingeye_all_instance` 共 4807 条 system 租户 K8s 文档；Cluster、Namespace、Service、Workload、Pod、Container、Node、PV、PVC 均有数据；
- 已验证 Cluster `BCS-K8S-00000` 的真实 OneModel 文档和 `model_inst_id`。

当前仍需：

- 真实 Kafka Event → Lifecycle → K8s Processor 回放；
- 真实 Kafka Event 回放；
- 更完整的名称/`cw_labels` 对照和真实 Kafka 回放；
- PV/PVC 是否进入当前 Linkd K8s 输出范围；
- 完整 K8s JSON fixture；
- 双租户和真实失败响应。

当前 K8s 真实数据已可用，后续工作聚焦真实回放和输出契约收口。

## 10. 跨场景公共缺口

当前缺口分为“代码边界已存在、生产适配待补”和“契约尚未确认”两类。派生 fixture 与 Reader 契约测试用于固定确定性行为，不代表真实环境验证。

1. **真实协议验证**：BaseTarget 的 ModelReader、OneModel 实例、投影边和 CMDB 拓扑已完成真实环境第一轮核验；K8s `kingeye_all_instance` 已完成 system 租户模型分布和代表性 Cluster 文档核验；服务实例、双租户和更多反向关系样本保留后续验证。
2. **动态分组**：Redis Reader、租户连接、key 查询、类型与失败语义作为独立后续能力。
3. **`cw_labels`**：BaseTarget 六个分支和 APM 已覆盖业务与确定资源身份；日志已输出业务/主题标签，Cloud 已输出确定资源标签；K8s Reader 已获得真实业务字段，完整标签投影和优先级仍待 Kafka 回放确认。
4. **Display 内容**：BaseTarget/DATA 已覆盖主要阈值、枚举、算法和日志内容分支；其他专用场景仍需要真实模板或产品规则确认。
5. **fixture**：BaseTarget、Collect、Uptime、DATA 已有 KAC 脱敏或派生 fixture；日志、Cloud、K8s、APM 已有 Processor 级和链路级派生测试，完整 JSON fixture 与真实外部样例继续补充。所有派生 fixture 均标记为迁移推导依据。
6. **名称补齐**：Collect 和 Uptime 的业务/云区域名称使用 OneModel 已知身份补齐；非主机目标的云区域名称及真实环境契约保留验证。

## 11. Observation

Enrich Observation 分为三层：

```text
Lifecycle 总体
├── Processor
└── DataSource
```

### 11.1 总体层

记录：

- attempts；
- duration；
- inflight；
- payload size；
- EventSource；
- chain kind；
- 最终 status 和 outcome。

总体层位于 Lifecycle 调用保护外侧，因此状态和 payload 大小对应最终写入 Alert 的值。

### 11.2 Processor 层

记录每个 Processor 的：

- status；
- outcome；
- duration；
- diagnostic code 与 dependency。

Processor Observation 顺序与配置顺序一致。指标不使用 `Diagnostic.Fields`。

### 11.3 DataSource 层

当前包装：

```text
CWStrategy
Business
MetricLibrary
Model
AlarmSource
OneModel
CollectConfig
CollectTopology
UptimeTask/UptimeNode
```

Outcome：

```text
found
not_found
invalid_response
failed
canceled
```


### 11.4 指标

```text
linkd.enrich.attempts
linkd.enrich.attempt.duration
linkd.enrich.inflight
linkd.enrich.payload.size
linkd.enrich.processor.attempts
linkd.enrich.processor.duration
linkd.enrich.processor.diagnostics
linkd.enrich.datasource.operations
linkd.enrich.datasource.duration
```

指标标签保持低基数，不包含租户、Event/Alert ID、fingerprint、策略 ID、模型实例 ID、完整错误、SQL、URL、payload 或凭据。

## 12. 验证状态

已具备：

- Chain 顺序、重复 Processor、空链和未知来源测试；
- Processor error/panic、非法状态、Context 取消和状态聚合测试；
- Alert 深拷贝与不可变测试；
- Strategy、Business、Metric、AlarmSource、CollectConfig、Uptime Task/Node、OneModel 实例 Reader 测试；
- BaseTarget 六个二级分支的显式分类和独立场景链路；
- 四个通用 BaseTarget 分支的场景模块、完整五 Processor 本地 payload、缓存和 DataSource 契约测试；
- DATA `basic_data` 原始 KAC 样例转换 fixture 验证 `MetricLibrary.object_model_code` 模型回退、`cw-Others` 空实例和业务标签；`data_ordinary_onemodel` 为基于统一实例规格的派生五 Processor fixture；
- NoData `__NO_DATA_DIMENSION__` 分类与 `model_id + model_inst_id` 规范身份 fixture；标准 Cleaner 对已进入 `dimensions` 的标记有保留测试，分类测试覆盖标记真值、假值、缺失及分支优先级，Processor 测试覆盖模型冲突、缺字段、非法实例、实例未命中和模型依赖失败；
- 模型／实例／租户响应身份不匹配、关联主机未命中／身份非法、拓扑未命中，以及查询期间 Context 取消与取消后同 Scope 重试的 BaseTarget 回归测试；
- 标准事件 `dimensions` 中 NoData 标记到 Alert.Enrich 的本地全链路测试，以及原始 `event.tags` 单独携带标记时的分类边界测试；
- 生产 ModelReader、OneModel InstanceReader、关联主机与拓扑 Reader 装配；
- `cw-biz` 业务模型、主机地址查询、代表性关系边和多 membership 稳定首路径的真实环境核验；
- 主机 MonitorSource RawEvent → Lifecycle → Alert.Enrich 测试；
- `go test ./...`、`go vet ./...`、golangci-lint 和目标包 race 在当前工作区通过；完整 `make check` 的 Helm 检查受本机缺少 `helm` 命令阻断。

BaseTarget 二级分支收口实施已完成。以下事项列为**迁移尾声待定验证项**；专用场景当前继续基于派生输入收口，真实外部适配器按契约确认结果接入：

- 真实可达的 Basic KAC 样本、其他采集类型及剩余失败边界；
- 真实来源 `event.tags` 到 Linkd 标准事件 `dimensions` 的上游映射契约和端到端样本；
- 服务实例 `cw_biz_id` 类型的真实样本；
- 双租户隔离和更多反向关系样本；
- 全部失败边界的真实环境验证；
- 非主机拨测目标的云区域名称和 Uptime 真实外部样本；

## 13. 已确认排除项

| KAC 能力 | Linkd 决定 |
| --- | --- |
| ACCESS_OBJECT | 当前分类入口缺少可达证据，排除 |
| 旧终态时间格式化 | 使用 Linkd Lifecycle |
| 关闭原因 ES 补查 | 排除 |
| 恢复、关闭重新 Enrich | 沿用已保存结果 |
| 智能算法专用图表增强 | 排除 |
| 旧告警 ID | 使用 Linkd 稳定 Alert ID |
| 旧线程池和 Kafka 推送 | 使用 Linkd Runtime 与 FinalHook |
| 旧监控原始回调 Cleaner | 当前从有效 Standard Event 开始 |
| `bk_service_id`、大写 `Namespace` 空占位 | 排除 |
| MonitorMetric 表 | 使用 MonitorMetricLibrary |
| KAC 隐式租户和已知参数错位 | 使用 Linkd 显式契约 |

## 14. 后续迁移顺序

### 当前已完成里程碑：Enrich 框架、BaseTarget、DATA 与专用 Processor 第一轮

已完成：

1. Enrich Router、配置校验、Chain、Scope、状态聚合、生命周期接入、类型化 Context 和 Observation；
2. BaseTarget 六个二级分支及 Collect/Uptime 场景的生产 Reader、请求内缓存、业务/云区域/标签和主要失败矩阵；
3. DATA 普通时序、system 主机、uptimecheck、hardware_、多模型、枚举、衍生指标、PromQL、函数和查询翻译；
4. LOG_METRIC/LOG_KEYWORD、Cloud、K8s、APM 的 Processor、Router 注册、输出模型、派生身份和主要链路测试；
5. KAC 脱敏样例、派生 fixture、完整 Processor 链测试、跨场景租户/身份/取消/失败语义测试；
6. BaseTarget 相关 MySQL、OneModel、拓扑和业务协议的第一轮真实环境核验。

### 已完成里程碑：BaseTarget 二级分支收口

已完成：

1. 六个二级分支的显式优先级和独立场景入口；
2. Scope 按稳定查询键缓存；
3. ModelReader、InstanceReader、关联主机与主机拓扑 Reader 的生产装配和 Observation；
4. MonitorSource、NoData、SystemMetric、Basic 的 Resource/Display 与 `cw_labels`，其中 NoData 使用 `__NO_DATA_DIMENSION__` 分类、`model_id + model_inst_id` 定位资源，并完成通用 KAC 展示行为迁移；
5. Collect/Uptime 场景回归、四个通用分支的完整五 Processor 本地 payload、六份 KAC 原始脱敏 fixture及 NoData/Basic 两份派生 fixture；
6. `object_model_v2`、统一实例、代表性投影边和 CMDB 业务拓扑的真实环境第一轮核验；
7. `cw-biz` 业务模型识别与多 membership 稳定首路径投影。

保留项统一记录在第 12 节，不影响本次实施设计收口。

### BaseTarget 已完成细节

已验证的降级行为：模型元数据异常保留合法实例并返回 Resource partial；核心实例身份不匹配或未命中返回 Resource failed、禁止生成实例标签；关联主机或拓扑缺失保留实例并返回 Resource partial；Context 取消中止 Enrich 链，同一 Scope 中的取消结果不会污染后续重试。单个 Resource failed、其余 Processor succeeded 时最终 Alert.EnrichStatus 依现有聚合算法为 partial。标准事件内的 NoData 标记已通过本地全链路验证；真实 Kingeye tags 到标准事件的上游映射仍待确认。

Basic fixture 已完成：

1. 现有 KAC source_data 没有可达 Basic 样例；所有 BaseTarget 样例均进入 MonitorSource、CollectTask 或 SystemMetric；
2. 已基于 KAC 通用 Cleaner 契约建立 `basic_model_context` 派生 fixture，覆盖模型上下文、来源业务标签、SubjectName object 回退和完整五 Processor payload；
3. 已增加 Basic 模型未命中／Reader 错误、MonitorSource 实例未命中和 SystemMetric 拓扑非法响应测试。

Uptime 业务名称、云区域名称和 `cw_labels` 已完成：

1. 任务业务 ID 优先，删除任务回退 Alert 来源业务；业务名称读取同租户 OneModel `cw-biz` 实例；
2. 仅在目标地址是可信 IP 且同时有 `bk_target_cloud_id` 时查询 OneModel `cw-Host`，复核 IP/区域后补名称，云区域 `0` 保留；
3. 已定位任务时输出业务和 `cw-web_service|<任务数据库主键>` 标签，删除任务只输出业务标签；
4. Reader 未命中、错误或身份冲突时保留 ID 与已有结果，不把可选名称故障升级为核心资源失败；
5. 两份 KAC 拨测脱敏 fixture 覆盖有任务与删除任务的五 Processor payload。

Collect 云区域名称已完成：

1. 主机采集从 OneModel 资源实例投影 `bk_cloud_id/bk_cloud_name`；
2. 远程／拓扑路径从相同云区域 ID 的关联主机补齐名称；
3. 非主机无需拓扑时，按告警主机 ID + 云区域，或 IP + 云区域查询 OneModel `cw-Host` 补齐名称；
4. 资源实例自身值保持优先，区域 ID 冲突时保持空名称；
5. 全程复用现有 OneModel Reader、租户隔离、身份复核、Scope 缓存和 Observation，不增加 CMDB API；
6. 已覆盖 ID／地址查询、字符串云区域 0、资源值优先、身份冲突、名称缺失、完整远程拓扑路径，以及 KAC `base_collect_collect_task_01` 五 Processor payload。

NoData 迁移已完成：

1. `__NO_DATA_DIMENSION__` 的真值决定 NoData 分类；`model_id`（模型代码）与 `model_inst_id`（实例 ID）只负责资源定位，普通资源身份不会触发 NoData；
2. 旧 `cw_object_model_*` 字段不再参与分类和查询；
3. `model_id` 必须与策略 `object_model_code` 一致；
4. NoData 沿用 KAC 通用展示规则：content 保留 Alert 原文，object 使用实例展示名并回退 SubjectName，title 使用 `AlarmAlias` 或“对象发生了监控项告警”；
5. 控制维度 `__NO_DATA_DIMENSION__` 不参与 Metric 查询筛选与 `where_condition`；标准 Cleaner 可保留标准输入 `dimensions` 中的标记，真实上游 tag 映射仍待验证；
6. 已增加规范身份的完整五 Processor fixture，以及普通身份、缺字段、非法身份、模型冲突、实例未命中和依赖失败测试。

### DATA 已完成纵切：普通时序、system 主机、uptimecheck、hardware_、多模型身份、枚举与查询语义

已完成：

1. `basic_data` KAC 样例转换 fixture，保留 `cw-Others`、空实例和业务标签语义；
2. MetricLibrary 数据源读取 `object_model_code`，并用于 DATA 资源模型回退；
3. DATA Resource 保留模型、业务和标签，不伪造实例身份；
4. system 主机使用目标主机 ID 或 IP+云区域定位 OneModel；uptimecheck 复用 `task_id` 任务定位；
5. 普通 OneModel 仅按显式 `model_id + model_inst_id` 原值查询，已覆盖成功、模型冲突、非法数字、未命中和响应身份不匹配；
6. `data_ordinary_onemodel` 派生 fixture 验证完整五 Processor payload，真实普通 DATA 样例仍待取证；
7. `hardware_` 与多模型结果表统一要求 canonical `model_id + model_inst_id`，MetricLibrary 按模型精确投影，OneModel 按实例原值查询；
8. 旧 `object_model_id + obj_model_inst_id`、`cw_object_model_id + cw_object_model_inst_id` 只保留为待迁移输入诊断字段，不恢复 Meta API 或数值主键推断；
9. MetricLibrary 读取 `value_mapping`，DATA 单指标无函数时对告警内容中的匹配数值追加枚举名称；多指标、函数、衍生指标和无法精确匹配的内容保持原值。
10. 衍生指标按 `field_tag=derived_metric`、策略 `field_name` 和无结果表约束读取元数据；Metric 输出保留表达式和 `field_tag`。
11. 多指标输出保留全部 query configs、别名、表达式和各自查询条件；Metric name 为空，Metric unique ID 沿用首个 query 的物理字段。
12. DATA 静态阈值展示优先使用告警等级对应算法的 `algorithmUnit`；`NONE` 表示清除单位；算法未覆盖时使用 MetricLibrary 单位映射。阈值和当前值支持数值单位追加。
13. 环比／同比内容按 Kingeye 分支识别可定位数值并进行枚举映射，无数据周期文案保留来源文本；custom event、alert、FTA 查询类型使用对应字段和结果表翻译。
14. PromQL 查询保留原始表达式，并按 `segment:...:metric` 约定投影结果表与指标字段；真实 PromQL 形态仍待样例补充。
15. 函数指标保留策略级和 query 级函数配置；Metric name 为空，函数和多指标场景关闭枚举映射，函数计算结果由上游查询链提供。
16. DATA 派生失败矩阵覆盖策略、MetricLibrary、模型、实例、身份校验和 Context 取消；单个 Resource failed 与其他 Processor 成功时最终状态为 partial，Alert 输入保持不变。

下一阶段：**在无真实场景数据的约束下，先补齐日志、Cloud、K8s、APM 的派生 fixture、跨场景失败矩阵和生产装配边界；真实外部 Reader 按契约确认结果接入。**


后续 DATA 子项：

1. 真实普通及多模型 DATA 样例和外部失败样本；
2. 更多查询类型和完整算法文本对照；
3. DATA uptimecheck 复用现有 `uptime.Enrich`；
4. 完成 DATA 后，日志、Cloud、K8s、APM 进入并行的派生收口阶段；真实外部 Reader 依赖契约确认。

### P3：LOG_METRIC / LOG_KEYWORD

代码与主流程迁移已完成：`LogThemeReader` 已接入 MySQL Runtime，按接口入参租户查询 `log_theme_logtheme`，只投影 `log_theme_id` 和 `log_theme_name`；本次使用 system 租户完成真实主题记录读取；日志 Processor、主题回退、关联信息、内容模板和 Router 注册已有保护。专用 query 参数/URL 和完整 fixture继续列为验证项。

### P4：Cloud

Cloud/VMWARE 主链已完成第一轮收敛：CloudResource MySQL Reader 已接入，业务标签使用 Kafka `labels.bk_biz_id`；CloudPlugin、SysSetting 和额外业务 ID来源不属于当前主链。后续补充真实 CloudResource 记录和完整 fixture。

### P5：K8s

K8s OneModel Reader 已接入主流程，并已用本机 ES system 租户真实数据核验 `kingeye_all_instance` 的模型和实例身份；业务优先级、业务名称和 `cw_labels` 已按旧 KAC 规则接入。后续补充真实 Kafka Event 回放、PV/PVC 范围确认和完整 fixture。

### P6：APM

第一轮派生迁移已完成。后续接入真实 Application Client，确认重复应用产品决策和完整 JSON fixture；application/service/service-instance 的派生身份、Resource 联动和失败矩阵已由测试保护。

### 迁移尾声待定：跨场景真实契约与历史边界验证

专用 Processor 的派生迁移已经完成第一轮。以下事项需要真实样本或外部契约后再确认验收口径和接入顺序：

1. 真实上游 NoData `event.tags → dimensions` 映射契约与端到端样本；
2. 双租户、关联主机恢复及其余外部失败样本；
3. 其他采集类型 KAC fixture；
4. 动态分组 Redis 独立任务。

## 15. 每个场景的完成标准

每新增或收口一个场景，至少具备：

1. 明确的 Alert 输入字段和类型；
2. 类型化分类结果和 Processor 适用条件；
3. 真实 DataSource Adapter 或明确复用的现有 Adapter；
4. 每次外部查询显式携带 `bk_tenant_id`；
5. 正常、边界、未命中、非法响应、依赖失败和取消测试；
6. 与受影响分组一致的 partial/failed diagnostics；
7. 类型化完整 Value；
8. RawEvent 或 Alert fixture 到最终 payload 的业务测试；
9. Alert 原有字段保持不变；
10. Observation 标签保持低基数；
11. 配置、模块和示例文档同步更新。

## 16. 后续方向：共享读模型与 Enrich v2

当前生产实现直接读取 Kingeye MySQL 和 OneModel ES。跨 Alert 缓存、Namespace、规则 DSL、可配置提取和版本化共享读模型均未实现。

后续若需要降低 Linkd 对旧存储的耦合，可采用“上游单写、Linkd 只读”的版本化物化读模型：

```text
Kingeye / OneModel / 其他来源
→ enrich-cache-publisher
→ 版本化共享读模型
→ Linkd EnrichmentCatalog View
```

该方向需要独立确认：

- 数据集与 schema version；
- tenant + generation 键空间；
- manifest/readiness/staleness；
- 增量发布、全量校准和删除墓碑；
- 单次 Enrich 固定 generation View；
- miss、not ready、stale、unavailable 和 incompatible 的语义；
- Linkd 禁止隐式回源。

在上述契约、发布者和运行责任确认前，当前 Reader 接口与直接只读连接继续作为生产基线。Enrich v2 的 Namespace、MatchRule、ExtractRule 和 endpoint Provider 属于候选产品方向，不构成当前实现要求。

## 17. 相关文档

- 使用与字段示例：[主机推送告警 Enrich 示例](../guides/host-alert-enrich-example.md)
- 输入字段取证归档：[alarm_callback 输入字段盘点](../research/alarm-callback-input-field-inventory.md)
- 外部数据依赖取证归档：[Kingeye alarm_callback 数据依赖](../research/kingeye-alarm-callback-data-dependencies.md)
- 实际数据准备清单：[Enrich 实际数据准备清单](../research/enrich-real-data-preparation.md)

本文是 Enrich 设计、迁移状态、Observation 和后续方向的唯一现行入口。研究文档只保留带版本的证据，不覆盖本文和代码。
