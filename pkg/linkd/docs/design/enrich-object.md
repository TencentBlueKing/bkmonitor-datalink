# Enrich 的 object 补全规则

核对日期：2026-11-02。代码基线：Linkd 主仓 `feat/linkd-dev`，`b6fa6420f75fc53b702071e61ca5c8721615c962`。

本文回答：告警对象 `object` 从哪里来、不同告警怎样补名称、查询失败时显示什么。
规则来自当前代码，不代表目标环境已部署，也不表示已有告警会自动重新补全。
对象模型和实例的完整定位规则另见 [model_id、model_inst_id 与 OneModel](enrich-resource-identity.md)。

## 1. object 最终取什么值

正式 KAC 转换先合成 Alert 的有效视图，再按以下顺序生成 `object`：

1. `extra_data.object` **存在**：直接使用该值，包括空字符串、`0`、`false` 和 `null`。
2. `extra_data.object` 不存在：使用非空的 `subject_name`。
3. `subject_name` 为空：使用 `subject_id`。
4. 两者都为空：输出空字符串；不会再拿 `model_inst_id`、`bk_host_name` 或告警标题填充。

这里“存在”和“非空”不同：`extra_data.object = ""` 会保留空串，
`subject_name = ""` 才会转用 `subject_id`。名称不去除首尾空白，空白字符串也算非空。
`labels.object` 不参与正式出口的这项覆盖。

| 字段 | 含义及关系 |
| --- | --- |
| `object` | KAC 告警对象的展示文本，按上述顺序生成 |
| `subject_name` | Linkd 的对象展示名称；Display 或通用 Resource 可以通过补丁补全 |
| `subject_id` | 来源 Subject 的标识；仅作为默认展示的最后一个值，不等于所有场景的 OneModel 实例 ID |
| `display.value.object` | Display 计算的对象名称，映射为 `$.subject_name` 补丁 |
| `model_id / model_inst_id` | OneModel 模型、实例标识，决定对象关联，不直接决定对象名称 |
| `model_name` | 通常是模型名称；专用云资源步骤将资源表的 `name` 放在这里，见第 4.10 节 |
| `bk_host_name` | 关联主机的真实主机名；单独补出该字段不会自动改变 `object` |
| `item / name` | 分别是监控项展示文本、告警标题，与 `object` 分开处理 |

例如，MySQL 告警可以是 `model_id = cw-MySQL`、`object = "订单数据库"`，
同时带有关联主机的 `bk_host_name`。补关联主机不应被理解为对象名称已经改成主机名。

依据：[KAC 转换](../../internal/lifecycle/kachook/message.go)、
[关联主机补充](../../internal/enrich/processors/resource_host.go)。

## 2. Enrich 在哪里补 object

### 2.1 通用 resource

`resource` 不依赖监控策略。它读取当前等级工作对象中的资源声明，查同租户模型和实例。
只有所有声明通过校验、实例读取成功，才进行名称补全：

- 当前工作对象的 `subject_name` 非空：保留它。
- `subject_name` 为空，且实例**根字段** `display_name` 是非空字符串：写 `$.subject_name` 补丁。
- 根字段没有有效 `display_name`：不补名称，不改读 `attributes.bk_host_name` 或其他属性。

它并不采用监控 BaseTarget 的多个名称字段顺序。来源只需要通用资源查询时，不要求同时配置 `display`。

### 2.2 监控 display

`display` 依赖准确的策略执行快照及非空的 `strategy_item.query_configs`，
按第 4 节选择对象名称，返回 `value.object`，再映射为 `$.subject_name` 补丁。

- 非空名称：生成补丁，可以覆盖原来的 `subject_name`。
- 内置 `value.object = ""`：不生成清空补丁，保留原名称。
- 步骤 `succeeded / partial`：合法补丁生效。
- 步骤 `failed / skipped`：补丁不生效，即使计算中曾查到一个名称也不会单独应用它。

Display 使用 `scope.Facts()` 中的原始 Event 作为 `subject_name` 和维度来源，
同时读取前面步骤设置的 `Resource.Values`。不能假定前置自定义补丁改过的 `subject_name`
会被它重新当成原始来源名称。最终仍按 Processor 顺序应用同一路径的补丁。

### 2.3 Processor 的配置和顺序

`EventSource.enrich.processors` 决定实际运行哪些步骤。监控链通常先由 `monitor-resource`
设置资源上下文，再由 `display` 补名称；只运行 `apm / k8s / cloud_resource` 不等于运行了对象名称补全。
这些专用步骤主要输出资源和扩展字段，Display 才使用其中可用的信息计算 `object`。

已发布 `built_in_bk` 有一项例外：配置含 `strategy + resource` 且不含 `monitor-resource` 时，
Router 让旧名称 `resource` 执行监控逻辑。不能只凭步骤叫 `resource` 就认定采用第 2.1 节规则。

依据：[通用 Resource](../../internal/enrich/processors/resource.go)、
[Display](../../internal/enrich/processors/display.go)、[原始 Facts](../../internal/enrich/scope.go)、
[补丁映射](../../internal/enrich/kingeye/projection.go)、[Router](../../internal/enrich/assembly/router.go)。

## 3. 查询和名称选择的共同规则

### 3.1 查询必须落到同租户的正确对象

- 策略：按 Event 的 `bk_tenant_id + labels.strategy_id + labels.strategy_version` 读取
  `alarm_strategy_execution_revision`，其中版本匹配 `strategy_revision`。不以当前最新策略替代历史版本。
  两个标签必须是 JSON 数值类型的正整数；策略快照及同租户模板名称读取失败都会影响 Display。
- 模型：按 `object_model_v2.bk_tenant_id + model_id` 查询。通用 Resource 接收 CMDB 对象代码时，
  按同租户 `bk_cmdb_obj_id` 找唯一模型，不按对象名称猜模型代码。
- 实例：按 `bk_tenant_id + model_id + model_inst_id`，或同租户、同模型下的类型明确的属性条件查询。
  返回多条不能任选一条，返回后复核租户、模型、实例 ID 及 `entity_uid`。
- 实例名称与属性：OneModel 根字段在 `Instance.Fields`，来源属性在 `Instance.Attributes`。
  各名称选择函数读取的位置和空值行为不同，见下表。

OneModel 后端可配置为 Elasticsearch 或 Doris；以下的根字段、属性条件描述共同查询含义，
不表示全部环境都查询同一套 Elasticsearch 索引。

依据：[策略读取](../../internal/enrich/datasources/cw_strategy_client.go)、
[模型读取](../../internal/enrich/datasources/model_client.go)、
[实例读取和校验](../../internal/onemodel/onemodel_client.go)、
[OneModel 后端装配](../../internal/onemodel/assembly/runtime.go)。

### 3.2 实例名称不是所有分支都用同一顺序

| 路径 | 读取位置及顺序 | 无有效名称时 |
| --- | --- | --- |
| 通用 `resource` | 仅根字段 `display_name`，要求非空字符串 | 不写名称补丁 |
| BaseTarget：普通目标、主机、无数据 | 合并根字段与 `attributes`，同名根字段优先；按 `bk_inst_display_name → bk_inst_name → display_name → bk_host_name → bk_host_innerip` 取第一个存在且非 null 的值 | 该值转成文本后若为空，直接回到来源 `subject_name` |
| Collect | 仅根字段，按 `bk_inst_display_name → bk_inst_name` 取第一个存在且非 null 的值 | 保留来源 `subject_name` |
| DATA 非主机实例 | 合并根字段与 `attributes`，同名根字段优先；按 `bk_inst_display_name → bk_inst_name → display_name` 取第一个非空字符串 | 使用 `cleanDisplayObject` 的场景规则 |

BaseTarget 和 Collect 使用 `FirstField`：前面的字段存在但为 `""` 时，
不会继续寻找后面的名称。例如 `bk_inst_display_name = ""`、`bk_host_name = "host-1"`，
BaseTarget 会保留来源名称，不自动取 `host-1`。
DATA 的 `FirstStringField` 则会跳过空字符串和非字符串。

Collect 也不会因为 `attributes` 中有主机名，就自动使用它；这是当前读取位置的差异。
这些是实现现状，不能概括成统一的“优先取实例展示名”。

依据：[BaseTarget 名称](../../internal/enrich/basetarget/display.go)、
[Collect 名称](../../internal/enrich/collect/display.go)、
[DATA 名称](../../internal/enrich/processors/display.go)、[字段读取函数](../../internal/enrich/rules/rules.go)。

## 4. 各类告警如何补全

以下“输出”指内置步骤计算的名称；最终 KAC `object` 仍服从第 1 节的 `extra_data.object` 覆盖。

### 4.1 普通目标：MonitorSource

**输入字段**：策略 `object_model_code`，`dimensions.model_id / model_inst_id`；旧路径还接受
`dimensions.bk_inst_id` 或 `obj_model_id / obj_model_inst_id`，以及来源 `subject_name`。

**查询与处理**：

- 提供 `model_id / model_inst_id` 任一个字段时，必须完整、合法且模型与策略一致。
  BaseTarget 的实例 ID 要求正整数或规范十进制字符串；不完整或非法时不改用其他字段找对象。
- 两者均未提供时，先尝试 `bk_inst_id`，再使用旧的对象模型、实例字段。
- 查询同租户 OneModel 实例。CMDB 服务实例还要求有效业务，并加 `attributes.cw_biz_id` 条件。
- 成功后按第 3.2 节 BaseTarget 顺序读名称；实例缺失、错误或身份不符时使用来源名称。

**输出字段**：`display.value.object → subject_name`；没有可用实例名称并不代表可以把 `model_inst_id`
直接写成名称。最终来源名称也为空时，KAC 才使用 `subject_id`。

依据：[BaseTarget 查询](../../internal/enrich/basetarget/resolver.go)。

### 4.2 主机指标与远程采集

**输入字段**：主机模型 `cw-Host`，实例字段、主机维度、Subject 和来源名称。

**查询与处理**：

1. 有 `dimensions.model_id / model_inst_id` 时只按这对字段验证和定位，避免选成采集执行主机。
2. 没有这对字段时，原生主机路径依次取 `bk_inst_id → bk_target_host_id → bk_host_id`。
3. 其次可取 `subject_system = cmdb + subject_type = host` 的 `subject_id`。
4. 最后使用 `bk_target_ip`（别名 `ip`）和 `bk_target_cloud_id`（别名 `bk_cloud_id`），
   查询主机属性 `bk_host_innerip / bk_cloud_id`；云区域 `0` 有效，别名冲突会被拒绝。
5. 实例成功时用 BaseTarget 名称；不能取得名称时，Display 的初始备用值是来源 `subject_name`，
   再取 `dimensions.ip`，最后取 `dimensions.bk_target_ip`。

**输出字段**：选中实例的名称可以覆盖来源名称；例如原始 `subject_name` 是采集主机，
而 `model_inst_id` 指向被采主机，成功补全后使用被采实例名称。
实例查不到时保留来源名称或 IP，不因此证明已验证了被采实例。

主机类型由策略模型，或 DATA 前序资源上下文确定。非主机告警即使通过
`resourceHostContext` 补出了 `bk_host_name`，也不会直接改写对象名称。

依据：[原生主机查询](../../internal/enrich/basetarget/host_identity.go)、
[主机备用名称](../../internal/enrich/processors/display.go)、
[独立主机补充](../../internal/enrich/processors/resource_host.go)。

### 4.3 无数据

**输入字段**：`dimensions.__NO_DATA_DIMENSION__`，对象字段、策略模型及来源 `subject_name`。

**查询与处理**：无数据标记选择场景，不生成单独的对象名称。
普通目标仍按 `model_id / model_inst_id` 查实例，主机可使用第 4.2 节的原生主机路径。
名称按 BaseTarget 或 DATA 的既有规则产生。

**输出字段**：成功时使用相应实例名称；失败时保留来源名称或该场景备用值。
不在 `object` 上自动添加“无数据”。非 DATA 的 K8s 无数据，即使资源步骤已按 K8s 维度取得实例，
Display 仍走目标类逻辑，不能据此断定会采用 DATA 的实例名称读取路径。

依据：[分类](../../internal/enrich/rules/rules.go)、
[无数据与主机解析](../../internal/enrich/basetarget/resolver.go)、[Display](../../internal/enrich/processors/display.go)。

### 4.4 采集任务：CollectTask

**输入字段**：`dimensions.bk_collect_config_id`、`bk_tenant_id`、来源 `subject_name`。

**查询与处理**：

- 查询 `core_v1alpha1_collect`：租户相等、`status.bk_collect_task_id` 等于输入、`active = true`，
  且 `status.delete_status` 不是 `deleting / deleted`，要求唯一命中。
- 用配置的 `bk_object_code` 作为 `model_id`，`bk_inst_id` 转字符串作为 `model_inst_id` 查 OneModel。
  服务实例可额外使用配置的 `final_biz_id` 限定 `attributes.cw_biz_id`。
- 按第 3.2 节 Collect 的根字段顺序读取名称。远程采集关联主机和拓扑不替换这里的目标实例。

**输出字段**：目标实例的 `bk_inst_display_name / bk_inst_name`，不可用时保留来源名称。
不使用采集配置名称、`final_inst_id` 或关联主机名作为对象名称。

依据：[采集配置 SQL](../../internal/enrich/datasources/collect_config_client.go)、
[Collect 解析](../../internal/enrich/collect/resolver.go)、[Collect 名称](../../internal/enrich/collect/display.go)。

### 4.5 普通 DATA、PromQL、多查询和衍生指标

**输入字段**：前序步骤的 `Resource.Values.model_id / model_inst_id`、策略、维度及来源 `subject_name`。

**查询与处理**：

- 资源模型为主机：复用 BaseTarget 的主机解析及名称，见第 4.2 节。
- 非主机且模型、实例 ID 均非空：按同租户和这两个字段查实例，
  按第 3.2 节 DATA 顺序取第一个非空字符串名称。
- 未命中、名称不可用或普通读取报错：转用 `cleanDisplayObject`；Context 取消则返回错误。
- `cleanDisplayObject` 通常使用来源名称；策略模型为 `cw-Host / cw-K8s_Node` 时有主机或 Node 备用值。
- APM 和拨测在这一步之后还可能覆盖名称，见第 4.8、4.9 节。

**输出字段**：已确认实例名称或场景备用值。PromQL、多查询和衍生指标没有单独的对象命名公式，
不会把指标表达式、`spec.name` 或首指标名称直接当成 `object`。
前序资源没有补出模型、实例 ID 时，Display 不会仅凭任意维度自动查询所有模型找名称。

依据：[dataResourceDisplay / cleanDisplayObject](../../internal/enrich/processors/display.go)、
[监控资源上下文](../../internal/enrich/processors/monitor_resource.go)。

### 4.6 K8s Node

**输入字段**：策略模型 `cw-K8s_Node`，来源 `subject_name`、`dimensions.node / instance`。

**查询与处理**：实例名称按普通目标或 DATA 路径选择；没有可用实例名称时，Node 备用顺序为
`subject_name → node → instance`。使用 `instance` 时按第一个冒号切分，取冒号前的文本。
这里是直接字符串切分，不是完整的主机地址解析。

**输出字段**：实例名称或上述 Node 文本。Basic 分支返回空名称时不会清除已经得到的 `node`。
例如 `instance = 10.0.0.1:9100` 可得到 `10.0.0.1`。

依据：[Node 名称](../../internal/enrich/processors/display.go)。

### 4.7 其他 K8s：Cluster、Namespace、Service、Workload、Pod、Container、PV、PVC

**输入字段**：资源步骤确定的 `model_id / model_inst_id`，以及该资源所需的 K8s 维度。

**查询与处理**：资源步骤先按模型和维度确定实例；例如 Pod 使用集群、命名空间、Pod 名，
PVC 使用集群、命名空间、声明名。DATA 的 Display 再按第 4.5 节读取实例展示名称。
完整的维度到属性映射见[资源关联规则](enrich-resource-identity.md)。

**输出字段**：命中实例的名称。没有可用实例名称时通常保留来源 `subject_name`，
不会统一拼接 `namespace/pod_name/container_name` 作为对象文本。
即使资源步骤在 OneModel 未命中时用维度生成了稳定 `model_inst_id`，也不等于取得了实例名称。
独立 `k8s` Processor 输出的 `cluster_name / pod_name` 等字段不会自动映射成 `subject_name`。

依据：[K8s Reader](../../internal/enrich/datasources/k8s_reader.go)、
[K8s Processor](../../internal/enrich/processors/k8s.go)、[DATA 名称](../../internal/enrich/processors/display.go)。

### 4.8 APM

**输入字段**：首查询的 `metricset_code / result_table_id`，合并后的 `service_name` 维度，来源名称。
维度由 `dimensions` 和 `extra_data.additional_dimensions` 合并，重名字段会被拒绝。

**查询与处理**：Display 的首查询满足 `IsAPMQuery` 时，即来源编码是 `apm / apm_application.<标识>`，
或表名含 `bkapm`，按 APM 展示。DATA 在通用实例名称选择之后会直接把候选名称设为 `service_name`。
因此不能把实例查到的名称一定排在服务名之前。未被该判断识别时仍按通用场景处理。

**输出字段**：非空 `service_name` 写入 `subject_name`；服务名为空时内置空补丁不清除来源名称，
最后仍可回到 `subject_id`。不会自动使用 `apm_app_alias`、`bk_instance_id` 或 `span_name`
作为更细的对象展示名称。APM 模型/实例可以定位到服务实例或接口，`object` 仍可能只显示服务名。

APM 资源解析还会判断指标库的实际来源和多个查询是否一致；这与 Display 直接检查首查询的规则
不是同一个判断。不能仅凭 `model_id` 已是 APM 模型就断定 Display 一定取服务名。

依据：[APM 展示判断](../../internal/enrich/rules/rules.go)、
[Display](../../internal/enrich/processors/display.go)、[APM 资源](../../internal/enrich/processors/metric_resource.go)。

### 4.9 拨测

**输入字段**：`dimensions.task_id`、租户；目标类模型为 `cw-web_service`，
或 DATA 首查询的结果表以 `uptimecheck` 开头。

**查询与处理**：查询 `home_application_uptimechecktask`，条件为同租户、
`task_id = 输入 task_id`、`is_deleted = false`，要求唯一匹配且响应身份有效。

**输出字段**：任务有效且 `name` 非空时使用任务名。否则生成
`task_id原文 + "（该拨测任务已被删除）"`；任务字段缺失或文本为空时使用 `0` 作为这段原文。
例如任务 `10079` 查询未命中，候选名称为 `10079（该拨测任务已被删除）`。

这段提示也可能出现在查询报错、输入无效或任务名为空时，不能仅凭文案认定任务确实被删除。
`node_id` 可用于补拨测节点的展示维度，节点名不拼入 `object`。
DATA 的拨测候选名称直接替换前面取得的对象名称，不保留来源名称作为优先值。

依据：[拨测任务 SQL](../../internal/enrich/datasources/uptime_client.go)、
[拨测名称](../../internal/enrich/uptime/display.go)、[Display](../../internal/enrich/processors/display.go)。

### 4.10 云资源

**输入字段**：`cloud_id / target_type / instanceid`、租户、前序云资源上下文和来源 `subject_name`。

**查询与处理**：

- `cloud_resource` 查询 `cloud_application_cloudresource` 的同租户、`cloud_id / type / instanceid`，
  并关联同租户 `cloud_application_cloud`。
- 资源表 `name → Resource.Values.model_name`，云平台表 `name → bk_cloud_name`。
- `cleanDisplayObject` 先取 `model_name`，缺失时用 `bk_inst_id` 文本；有云平台名则组成
  `资源名称(云平台名称)`，平台名缺失时尝试 `cloud_plat_id`。
- 随后的 BaseTarget 调用在当前 Cloud 分类下返回来源 `subject_name`；它非空时又会覆盖上述候选。

**输出字段**：当前完整 Display 路径中，来源 `subject_name` 非空时使用来源名称；
为空时才有机会使用云资源名称及平台组合。不能只看 `cleanDisplayObject` 就认定所有云告警都显示组合名。
没有运行 `cloud_resource` 或对应上下文缺失时，也不能保证能从实例 ID 自动生成资源名称。

依据：[云资源 SQL](../../internal/enrich/datasources/cloud_resource_client.go)、
[云资源字段](../../internal/enrich/processors/cloud.go)、[Display 调用顺序](../../internal/enrich/processors/display.go)。

### 4.11 日志指标

**输入字段**：DATA 策略的 `metric_source = klc`、来源 `subject_name`、日志配置。

**查询与处理**：`cleanDisplayObject` 对日志先返回空值；后续通用目标处理保留来源名称。
日志主题查询可用于标题和日志扩展字段，但没有将查询到的 `log_theme_name` 写成对象名称。

**输出字段**：有来源名称时保留它；没有时最终使用 `subject_id`。
日志主题出现在标题里，不表示 `object` 已补成该主题名。

依据：[日志分类](../../internal/enrich/rules/rules.go)、[Display](../../internal/enrich/processors/display.go)。

### 4.12 日志关键字

**输入字段**：DATA 策略的 `monitor_item_type = log_keyword`、来源 `subject_name` 和检索配置。

**查询与处理**：对象名称与日志指标相同，保留来源名称。
检索语句、`log_theme_name` 和 `log_relate_info` 用于标题、内容或扩展信息，不自动填入对象名称。

**输出字段**：来源名称或最终的 `subject_id`，不是检索语句或关键字。

依据：[Display](../../internal/enrich/processors/display.go)、[Log](../../internal/enrich/processors/log.go)。

### 4.13 Basic、外部来源和自定义规则

**输入字段**：来源 Subject、已有资源声明和 `fields / cmdb` 规则。

**查询与处理**：

- Basic 不查询实例名称，通常保留来源 `subject_name`；Node 模型仍有第 4.6 节的初始备用值。
- 外部来源可使用第 2.1 节的通用 `resource`，无需提供监控策略。
- 自定义规则可明确赋值 `$.subject_name`，或赋值 `$.extra_data.object`。
  后者在最终 KAC 转换时优先，不会因后续 Display 改写 `subject_name` 而失效。
- 仅运行自定义 CMDB 查询、补出模型和实例 ID，不等于已配置了名称赋值。

**输出字段**：最终生效的 `subject_name` 或 `extra_data.object`。
规则显式将 `subject_name` 设为空串时，KAC 转用 `subject_id`；
显式将 `extra_data.object` 设为空串时，KAC 保留空串。

依据：[通用 Resource](../../internal/enrich/processors/resource.go)、
[自定义规则](../../internal/enrich/custom/run.go)、[允许的补丁字段](../../internal/domain/enrich_patch.go)。

## 5. 空值、失败与“补全成功”的判断

| 情况 | 实际行为 |
| --- | --- |
| `extra_data.object` 为 `"" / 0 / false / null` | 正式 KAC 文本分别为 `"" / "0" / "false" / ""`，不触发默认名称回退 |
| `extra_data.object` 为对象或数组 | 正式 KAC 转换报类型错误，不把 JSON 拼成对象名称 |
| 内置 Display 返回空 `object` | 不生成清空 `subject_name` 的补丁 |
| 实例未命中或名称字段为空 | 可保留来源名称或使用场景备用值；不能据非空 object 认定实例已命中 |
| 模型/实例补全成功 | 不保证名称字段也存在，不保证 `object` 与 `model_inst_id` 表达相同粒度 |
| Resource 为 partial，Display 为 succeeded | 可能只是 Display 成功保留了来源名称；两个状态不能互相替代 |
| Display 中普通实例读取失败 | 某些路径直接使用备用名称，未必把 Display 标为 partial；应结合资源步骤诊断和依赖日志判断 |
| Display 的其他计算失败 | 策略、指标库、维度解析等失败可使整个 Display 为 failed，已经算到的名称不会单独生效 |
| 后续 `fields` 再写 `subject_name` | 按步骤顺序覆盖前面的 Display 名称，前提是该规则的补丁有效 |

Display 同时生成标题、维度和可能的内容。不能因为对象查询本身不需要指标库，
就认为 `metric_catalog` 错误与对象补全无关：后续指标或维度计算失败仍可能使整个 Display 不生效。
`enrich.content_mode = bkmonitor_description` 让 Display 保留核心内容，不会跳过对象名称计算。

依据：[Display 完整流程](../../internal/enrich/processors/display.go)、
[步骤状态与补丁](../../internal/enrich/enricher.go)、[KAC 文本类型](../../internal/lifecycle/kachook/debug.go)。

## 6. 为什么最新 Event 名称与 Alert 的 object 不同

每条不同 Event 在策略裁决前执行并冻结 Enrich，按 `severity` 保存结果。
重投已经冻结的 Event 不重新查询实例名称。
创建 Alert 时复制 opening Event 对应等级的来源字段及 Enrich；后来的 Event 不会自动替换 `Alert.Enrich`。

因此，来源名称、OneModel 实例名称或规则后来变化，新 Event 可显示新名称，
已有 Alert 的对象文本仍可能来自 opening Event。持续触发更新 `content` 也不表示对象文本刷新。
配置 `reuse_on_active` 时，选定步骤还可沿用同一活动 Alert、同快照等级下的成功结果；预览不使用该复用。

策略字段 `FactView.Field("object")` 同样优先读有效 `extra_data.object`，否则取非空 `subject_name`，
再取 `subject_id`，但保留字段原类型，不执行 KAC 的最终文本转换。
合并模板的 `object` 输出目标是 `subject_name`，仍受 `extra_data.object` 的最终覆盖。

Event 丰富记录窗口展示当前 Event 的结果；它还会展开 `labels.object`，与正式出口的覆盖规则存在差别。
接入调试 `DebugFields` 只转换已有字段，不执行 Display 或实例名称查询，不能代替正式 Enrich。

依据：[冻结 Event](../../internal/lifecycle/enrich.go)、[创建 Alert](../../internal/lifecycle/plan.go)、
[步骤复用](../../internal/enrich/reuse.go)、[策略读取](../../internal/policy/view.go)、
[合并模板](../../internal/policy/merge_template.go)、
[Event 窗口](../../internal/controlplane/api/event_enrich_window.go)、
[接入调试](../../internal/lifecycle/kachook/debug.go)。

## 7. 排查时依次看什么

1. 查 Alert 的 `trigger_event_id / latest_event_id / enrich_severity`，区分 opening Event 与最新 Event。
2. 检查有效 `extra_data.object` 是否存在；若存在，先核对它的值、JSON 类型和赋值步骤。
3. 检查原始 `subject_name / subject_id`，再检查对应等级的 Enrich：
   Event 是 `enrich.evaluations[].data.processors`，Alert 是 `enrich.processors`。
4. 根据来源配置区分通用 `resource`、`monitor-resource` 或旧发布配置中的监控 `resource`，
   检查 `display / fields / cmdb` 的状态、诊断及 `$.subject_name / $.extra_data.object` 补丁。
5. 按场景核对实例查询输入和返回身份；名称按第 3.2 节检查根字段与 `attributes`，
   不只看某个属性有值就认定当前分支会读取它。
6. 最后比较 Event 有效视图、Alert 快照和 KAC 实际存储。预览成功只证明本次计算，
   不能证明历史 Alert 已更新或兼容写入已完成。

## 8. 本次验证范围

本次只新增本文、补充术语和文档索引，未修改业务代码，未提交或推送。
相关已有定向测试通过，命令如下；执行时均经 `rtk proxy` 调用：

```bash
go test ./internal/enrich/processors -run '^(TestResourceSubjectWithoutStrategy|TestBaseTargetBranchesFlowThroughResourceAndDisplay|TestNoDataFiveProcessorFailures|TestRemoteHostCanonicalIdentity|TestDisplayKeepsK8sNodeNameAcrossBasicTarget|TestDisplayCreatedContentAcrossResourceScenes|TestDATAOrdinaryModelUsesCanonicalIdentity|TestDATAUptimeUsesTaskID|TestPromQLPublishedSnapshotEnrichChain|TestLogMetricAndKeywordProjection)$' -count=1
go test ./internal/enrich/basetarget ./internal/enrich/collect ./internal/enrich/uptime -run '^(TestResolverAndProjectors|TestBaseTargetBranches|TestNoDataIdentityContract)$' -count=1
go test ./internal/lifecycle/kachook ./internal/policy ./internal/lifecycle -run '^(TestConvertMessageMapsAlertAndEnrichToKACAlarm|TestK8sCompatibilityProjectsEffectiveFields|TestLegacyCleanedFieldsUsePresenceAndEffectivePatches|TestEveryEventEnrichedAndOpeningSnapshotFrozen)$' -count=1
```

这些测试覆盖部分资源、名称、失败和快照路径，不代表本文每个场景及空值组合均新增或执行了专项测试。
云资源候选名称的覆盖顺序、名称字段为空时的读取差异等内容按代码静态核对。
本文 13 个场景的输入、查询和输出结构检查通过；4 篇受影响文档共 146 个本地链接及锚点、
行尾空白检查和 `git diff --check` 均通过。
文档改动不运行全量 `make check`；未查询目标环境、未重放线上 Event/Alert，也未验证部署和实际 KAC 写入。
