# Enrich 的 model_id、model_inst_id 与 OneModel 关联规则

本文回答：各种资源从哪里取得 `model_id`、`model_inst_id`，如何查询 OneModel，查询失败时保留什么，以及与已提出原则的差距。

代码基线：2026-11-01，主仓 `feat/linkd-dev`，`b6fa6420f75fc53b702071e61ca5c8721615c962`。
本文描述该版本的实现，不表示已部署或已通过线上验收；后续修改相关行为时应同步本文。
2026-11-04 更新通用 `resource` 的 CMDB 实例查询和 `bk_inst_id` 输出规则；其余章节仍按上述基线描述。
Enrich 的执行、补丁和保存规则见[丰富设计](enrich.md)，自定义规则的完整配置见[自定义丰富规格](custom-enrichment.md)。

## 1. 先区分这些字段

| 字段 | 含义与来源 | 使用原则 |
| --- | --- | --- |
| `bk_tenant_id` | Event 所属租户 | 模型、实例和关联查询都必须带此租户，不能只按实例 ID 查询 |
| `model_id` | OneModel 模型代码，例如 `cw-Host`、`cw-K8s_Pod` | 对应 `object_model_v2.model_id` 和实例根字段 `model_id` |
| `model_inst_id` | 该模型下的 OneModel 实例 ID，字符串 | 与 `bk_tenant_id`、`model_id` 一起定位实例；不能一概限制成整数 |
| `entity_uid` | 实例根字段，值为 `model_id + "\|" + model_inst_id` | Reader 校验它与模型、实例 ID 一致；租户仍须单独过滤 |
| `object_model_id` | `object_model_v2` 的数字主键 | 不是告警的 `model_id`，不能拿来查实例根字段 `model_id` |
| `object_model_code` | 策略投影中的模型代码 | 可为监控场景提供模型，但不能与模型表数字主键混淆 |
| `model_name` | 模型名称 | 通常来自 `object_model_v2.model_name`；云资源分支有例外，见第 5.7 节 |
| `bk_obj_id` | CMDB 对象代码，例如 `host` | 通过同租户 `object_model_v2.bk_cmdb_obj_id` 映射到 `model_id` |
| `bk_inst_id` | CMDB 实例 ID | CMDB 同步实例以此源主键的字符串作为 `model_inst_id`；须先确认同租户模型映射并查到实例，不能将此规则推广到所有 OneModel 来源 |
| `bk_host_id`、`bk_host_name` | 关联主机的信息 | 非主机告警也可有这两个字段，不意味着告警对象变成主机 |

代码中有一个容易误读的命名：`enrich.Model.ModelID` 保存模型表的 `object_model_id`，
`enrich.Model.ModelCode` 才保存输出的 `model_id`。本文统一使用实际数据字段名说明。

依据：[模型 Reader](../../internal/enrich/datasources/model_client.go)、[实例 Reader](../../internal/onemodel/onemodel_client.go)、[主机补充](../../internal/enrich/processors/resource_host.go)。

## 2. 原则与当前落实程度

引用讨论中提出的原则如下。它们是本次对照的目标，不能把整张表都读成已经实现的统一契约。

| 原则 | 当前代码 | 尚未统一的地方 |
| --- | --- | --- |
| 有 `model_id` 和 `model_inst_id` 时，只用这两个字段确定告警对象 | 通用 `resource` 优先解析这两个字段；监控普通目标、DATA 已有多项优先与冲突校验 | `resource` 还校验 Subject、CMDB 字段；监控仍受策略和指标库约束；K8s Processor 仍按维度查询；拨测、云资源仍走专用路径 |
| 未提供这两个字段时，只有主机、K8s 从维度补模型和实例 | 主机、K8s 均已支持 | 监控普通目标仍接受旧字段，采集任务、拨测、云资源也会产生 `model_inst_id`；这些是现状，不是原则允许的新例外 |
| K8s 使用数据维度所能确定的最细对象 | DATA 已按聚合后维度选择；支持 PV/PVC，并优先识别被监控的存储资源 | 实例策略保留策略模型限制；独立 `k8s` 的 `workload_kind=Pod` 判断仍早于 Container；不能笼统宣称所有路径都选最细对象 |
| APM 按数据维度确定模型和名称，去掉 CMDB 实例 ID，使用最细对象 | 已支持应用、服务、服务实例；接口可使用经 OneModel 验证的已有字段 | 策略/指标库仍可限定模型；无应用 ID 时不输出模型；`monitor-resource` 仍将应用 ID 写入 `bk_inst_id`；仅有 `span_name` 不会生成接口实例 |
| CMDB 集群、模块、云区域可关闭，且仅主机丰富 | 已有 `enrich.cmdb_set_module_enabled`，默认 `false`；监控非主机已清空云区域输出 | 开关只控制额外的集群/模块拓扑查询；实例自带字段仍可保留，非主机仍可经关联主机补集群/模块；云区域没有独立开关，专用云资源路径另有输出 |

需要统一时，应先确定对象，再补名称、业务、主机和拓扑。后续字段不能把已确定的对象换成采集主机、策略模型或更粗的对象。
但“已有字段与其他声明冲突时忽略后者还是报错”“是否取消专用分支产生实例 ID”仍需作出具体决策；本文不以文档修改代替代码变更。

## 3. 哪些 Processor 会确定对象

EventSource 的 `enrich.processors` 决定执行哪些步骤及顺序；不是每条告警都执行以下所有逻辑。

| Processor | 当前职责 |
| --- | --- |
| `resource` | 通用资源解析。使用已有字段、Subject 或 CMDB 字段查同租户模型和实例，不依赖监控策略 |
| `monitor-resource` | 监控场景解析。结合策略、指标库、采集配置、主机/K8s/APM 等规则确定对象，并补业务、主机及动态分组 |
| `k8s` | 按模型和 K8s 维度查询实例，补集群、命名空间、业务等信息，也可输出 `model_id`、`model_inst_id` |
| `apm` | 查询应用，确定应用/服务/服务实例或验证已有接口实例，输出 APM 字段与模型、实例 ID |
| `cloud_resource` | 查云资源表，输出其中的模型代码及资源表主键 |
| `cmdb`、`fields` | 按配置查询 OneModel；带 `identity` 的规则可写模型、实例及 CMDB 字段 |
| `cmdb-access` | 按最终字段补业务名、权限标签和动态分组，不负责查验主对象 |
| `strategy`、`log`、`display`、`metric`、`source` | 分别提供策略、日志、展示、指标和来源信息；不应把其名称或展示字段当成实例已命中的证据 |

还有一个实际发布配置的例外：`built_in_bk` 的已发布来源若同时有 `strategy`、旧 `resource`，且没有 `monitor-resource`，
Router 会让该 `resource` 执行监控逻辑。仅看保存的 Processor 名称不足以判断行为。

丰富结果按等级保存为 `Event.enrich.evaluations[].data.processors[]` 中的有序 `patches`。
内置输出的标量通常写到 `labels`，对象/数组写到 `extra_data`；例如模型和实例 ID 通常在有效视图的 `labels` 中。
原始 `Event.dimensions` 不因此改写。读取结果应使用应用补丁后的视图，不能只检查原始维度。

依据：[Router](../../internal/enrich/assembly/router.go)、[补丁映射](../../internal/enrich/kingeye/projection.go)。

## 4. OneModel 的共同查询规则

### 4.1 查询模型定义

- 输入：`bk_tenant_id`、模型代码。
- 查询：`object_model_v2.bk_tenant_id = bk_tenant_id AND model_id = 模型代码`。
- 输出：`model_id`、`model_name`、`bk_cmdb_obj_id`、`host_related_field` 和展示配置。
- 若输入是 CMDB 对象代码，改查 `bk_cmdb_obj_id`，必须只命中一个模型；不按 `host` 等名称拼接 `cw-*`。

### 4.2 查询实例

- 有 `model_inst_id`：按 `bk_tenant_id + model_id + model_inst_id` 精确查询。
- 只有资源定位维度：按 `bk_tenant_id + model_id + 类型明确的属性条件` 查询。
- 返回零条：未命中；返回多条：查询有歧义，不能任选第一条作为内置单实例查询的结果。
- 返回前校验 `bk_tenant_id`、`model_id`、非空字符串 `model_inst_id`、`entity_uid` 和对象类型的 `attributes`；按实例 ID 查询时还要求返回的 ID 与请求一致。
- 超时、部分查询失败、错误响应不能解释成“实例不存在”。

后端由 `resources.onemodel.backend` 选择：Elasticsearch 使用 `kingeye_all_instance`，Doris 默认使用 `kingeye_instance`。
Doris 故障不会自动回退旧 ES 实例数据。CMDB 主线拓扑仍使用独立 ES 数据，不能据实例后端推断拓扑后端。

ES 属性查询使用 nested `attribute_values`，同时限定 `field_name` 和类型槽；例如主机地址使用
`bk_host_innerip` 的 `keyword_values`、`bk_cloud_id` 的 `long_values`。不能把两个属性的条件拼成不相关的数组命中。
Doris 将相同条件转换为类型明确的属性查询。

依据：[模型 Reader](../../internal/enrich/datasources/model_client.go)、[单实例查询及响应校验](../../internal/onemodel/onemodel_client.go)、[后端装配](../../internal/onemodel/assembly/runtime.go)、[Doris](../../internal/onemodel/doris.go)。

## 5. 各类告警如何得到对象实例

### 5.1 通用来源：已有 model_id 和 model_inst_id

**输入字段**

- 当前等级有效视图中的 `labels`、`extra_data`、`dimensions`，每处可以提供 `model_id` 和 `model_inst_id`。
- 两个字段必须来自同一处，不能把 `labels.model_id` 与 `dimensions.model_inst_id` 拼在一起。

**查询规则**

- `resource` 按上述顺序收集这两字段，再处理 Subject 和 CMDB 字段。
- 两字段必须是非空、无首尾空白和控制字符的字符串；模型最长 128 字节，实例 ID 最长 1024 字节。
- 查模型定义，再按 `model_id + model_inst_id` 查 OneModel 实例；实例 ID 可以是 `host|101` 这样的字符串。
- 多处声明、Subject、旧 CMDB 字段只要存在，都需要合法并定位到同一模型、实例。低优先级声明失败或冲突不会被忽略，也不会改选另一个对象。
- 未提供上述任何字段或 Subject 声明时 `skipped`，不从 IP、采集任务或策略推断资源；已提供但非法则报错。

**输出字段**

- `model_id`、`model_inst_id`、`model_name`；有 CMDB 映射时补 `bk_obj_id`，已命中的 `model_inst_id` 为规范正整数字符串时反投影 `bk_inst_id`，不依赖实例 attributes 中的旧身份字段。
- `subject_name` 为空时可使用实例的 `display_name`。
- 不在此步骤自动补监控拓扑、主机信息或动态分组。

依据：[resourceIdentities、resolveResourceIdentity](../../internal/enrich/processors/resource.go)。

### 5.2 通用来源：Subject 或 bk_obj_id、bk_inst_id

**输入字段**

- `subject_system=onemodel`：`subject_type` 是模型代码，`subject_id` 是 OneModel 实例 ID。
- `subject_system=cmdb`：`subject_type` 是 CMDB 对象代码，`subject_id` 是 CMDB 实例 ID。
- 或在同一处 `labels`、`extra_data`、`dimensions` 提供 `bk_obj_id`、`bk_inst_id`。

**查询规则**

- OneModel Subject 直接按模型代码和实例 ID 查询。
- CMDB Subject/字段先通过同租户 `bk_cmdb_obj_id` 映射模型，再按实例顶层 `model_inst_id` 查询；CMDB ID 必须是正整数，并规范为字符串。
- Kingeye 的 CMDB 同步将源主键写入 `model_inst_id`：主机、业务、集群、模块分别取 `bk_host_id`、`bk_biz_id`、`bk_set_id`、`bk_module_id`，普通对象取 `bk_inst_id`。规范 attributes 排除 `bk_inst_id`，不再按此属性查找或验证实例。
- 查询响应的租户、模型、实例 ID 和 `entity_uid` 必须与请求身份一致；实例不存在时仅保留已确认模型，不能复制请求 ID 冒充查询命中。

**输出字段**

- 与第 5.1 节一致。`model_inst_id` 取 OneModel 返回值，不能直接复制输入的 CMDB ID 冒充实例命中。
- 同时有 `model_id`、`model_inst_id` 时仍须通过一致性校验。这里与“只认这两个字段”的目标还有差距。

依据：[CMDB 映射和规范实例查询](../../internal/enrich/processors/resource.go)。

### 5.3 监控目标：普通对象、CMDB 服务实例、无数据

**输入字段**

- `labels.strategy_id`、`labels.strategy_version`，按 Event 的 `bk_tenant_id` 精确读策略执行快照，取得 `object_model_code`。
- 普通目标分支主要读取原始 `dimensions` 中的 `model_id`、`model_inst_id`。
- 未提供这两个字段时，普通目标还接受 `bk_inst_id`，或 `obj_model_id`、`obj_model_inst_id`。
- `__NO_DATA_DIMENSION__` 只决定无数据分支，不能充当对象实例 ID。

**查询规则**

- 已出现 `model_id` 或 `model_inst_id` 中任一个，就校验两字段是否完整、模型是否与策略一致；失败不回退采集任务或旧字段。
- 该分支仍要求 `model_inst_id` 为正整数，字符串形式也必须是规范十进制；与通用 `resource`、普通 DATA 的字符串规则不同。
- `cw-CCServiceInstance`、`cw-ServiceInstance` 还要求有效业务，查询增加 `attributes.cw_biz_id` 的 long 条件。
- 非主机无数据通常仍要求 `model_id`、`model_inst_id`。已知 K8s 模型在两字段都未提供时可改用第 6 节的维度查询。
- `basic` 分支只查模型定义，不查实例。

**输出字段**

- 命中后取 OneModel 的 `model_id`、`model_inst_id`，补模型名称、CMDB 字段及业务等信息。
- 只确认模型时可以保留 `model_id`、`model_name`、`bk_obj_id`；不能把输入或采集配置中的 ID 直接作为已命中的实例发布。

依据：[分类顺序](../../internal/enrich/rules/rules.go)、[BaseTarget 查询](../../internal/enrich/basetarget/resolver.go)、[资源投影](../../internal/enrich/basetarget/resource.go)。

### 5.4 主机：已有字段、主机 ID、Subject、地址

**输入字段**

- 模型为 `cw-Host`；已有 `dimensions.model_id`、`dimensions.model_inst_id` 时优先校验它们。
- 两字段均未提供时，主机 system/no-data 路径依次使用 `bk_inst_id`、`bk_target_host_id`、`bk_host_id`。
- 再使用 `subject_system=cmdb + subject_type=host + subject_id`。
- 最后使用 `bk_target_ip`（别名 `ip`）与 `bk_target_cloud_id`（别名 `bk_cloud_id`）。

**查询规则**

- 主机 ID、主机 Subject：查 `model_id=cw-Host + model_inst_id=主机 ID`。
- 地址：查 `model_id=cw-Host`，属性 `bk_host_innerip=输入地址`、`bk_cloud_id=输入云区域`，同时限定租户。
- 云区域 `0` 有效。主字段和别名同时提供但值不同则报错，不能默认选择一个。
- 已有 `model_id`、`model_inst_id` 残缺、非法或查询失败时，不改用远程执行主机的 Subject、IP 或主机 ID。

**输出字段**

- `model_inst_id` 取已验证的 OneModel 主机实例；补 `bk_host_id`、`bk_host_name`、业务及允许的拓扑字段。
- 非主机告警有地址时也可独立查询 `bk_host_id`、`bk_host_name`，但不能据此改写该告警的 `model_id`、`model_inst_id`。

依据：[主机定位顺序与地址条件](../../internal/enrich/basetarget/host_identity.go)、[远程主机优先规则](../../internal/enrich/basetarget/resolver.go)、[独立主机信息](../../internal/enrich/processors/resource_host.go)。

### 5.5 采集任务

**输入字段**

- `dimensions.bk_collect_config_id`，表示平台采集任务 ID。

**查询规则**

- 查 `core_v1alpha1_collect`：同租户、`active=true`、`status.bk_collect_task_id` 与输入一致，排除 `deleting/deleted`。
- 必须唯一命中；任务 ID `"0"` 不关联配置。`collect_enable` 不作为过滤条件，停用后的在途告警仍可定位。
- 取 `bk_object_code` 作为模型代码、`bk_inst_id` 作为实例 ID，再查 OneModel。
- CMDB 服务实例可追加 `final_biz_id → attributes.cw_biz_id`。
- 远程采集的告警对象仍是配置指定的被采实例；关联主机只补上下文，不能替换对象。

**输出字段**

- OneModel 命中才输出 `model_inst_id`；配置或模型可独立确认时保留模型及业务信息。
- 该路径仍会在事件没有 `model_id`、`model_inst_id` 时生成实例关联，与第 2 节目标有差距。

依据：[采集配置查询](../../internal/enrich/datasources/collect_config_client.go)、[Collect](../../internal/enrich/collect/resolver.go)。

### 5.6 普通 DATA / PromQL

**输入字段**

- 策略的全部 `query_configs`、`object_model_code`，以及当前有效视图中的 `model_id`、`model_inst_id`。
- DATA 会检查 `labels`、`extra_data`、`dimensions` 中的这两个字段：同处成对，多处一致。

**查询规则**

- 通常先取策略 `object_model_code`，再取事件 `model_id`；指标库可以补充模型并限定适用范围。
- 对全部输入的非空适用模型集合求交集；集合冲突不能任选第一条。显式 `metric_ref_id` 未命中仍是依赖失败。
- 指标库合法记录未关联模型，不一概视为失败；没有模型可选时可能 `skipped`。
- K8s DATA 有专门处理，见第 6 节。APM、拨测会进入专用分支。
- 主机走第 5.4 节。其他普通对象需要 `model_id`、非空字符串 `model_inst_id`，按这两字段查询 OneModel。
- PromQL 允许没有指标库命中；没有模型时还可由明确的 CMDB host Subject 确认 `cw-Host`。仍无法确定时保留业务和缺字段诊断。

**输出字段**

- 命中实例后输出其模型、实例 ID 及可确认属性；只有模型时保留模型上下文。
- 普通 DATA 的字符串 `model_inst_id` 不等于 BaseTarget 已统一接受字符串。
- 已有两字段仍可能被策略、指标库校验拒绝，尚未实现全路径“只认这两个字段”。

依据：[processDataResource](../../internal/enrich/processors/monitor_resource.go)、[全部输入、模型集合及字段校验](../../internal/enrich/processors/metric_resource.go)。

### 5.7 云资源

**输入字段**

- Cloud 策略及有效业务；`dimensions.cloud_id`、`target_type`、`instanceid`。

**查询规则**

- 查 `cloud_application_cloudresource`：`bk_tenant_id`、`cloud_id`、`type=target_type`、`instanceid=输入值`。
- 关联 `cloud_application_cloud` 获取云平台名称，并校验两表租户一致。
- 此步骤没有再按输出的模型、实例 ID 查询 OneModel 实例，也没有统一优先采用事件已有的两字段。

**输出字段**

- `model_id ← resource.object_model_code`。
- `model_inst_id ← resource.id`，不是输入的 `instanceid`。
- 当前 `model_name ← resource.name`，实际取资源名称；不要把它解释成已经查询模型表的模型名称。
- 当前还输出 `bk_cloud_id`、`bk_cloud_name`、`cloud_plat_id`；这是专用分支，与监控非主机清空云区域的行为不同。

依据：[Cloud Processor](../../internal/enrich/processors/cloud.go)、[云资源 SQL 映射](../../internal/enrich/datasources/cloud_resource_client.go)。

### 5.8 拨测

**输入字段**

- `dimensions.task_id`；可选 `node_id`、`bk_target_ip`/`target_host`、`bk_target_cloud_id`。

**查询规则**

- 目标类拨测使用策略模型；DATA 的 `uptimecheck*` 专用分支使用 `cw-web_service`。
- 按同租户、`task_id`、`is_deleted=false` 查询 `home_application_uptimechecktask`，要求唯一命中。
- 模型名称查模型表。`node_id` 按 `plat_id:ip` 查询拨测节点，只用于展示。
- 目标地址还可用于查询主机云区域；最终监控非主机资源输出会清空云区域，见第 8 节。

**输出字段**

- `model_inst_id ← home_application_uptimechecktask.id`，不是输入 `task_id`。
- 此 ID 来自拨测任务表，未通过 OneModel 主实例查询确认；已有 `model_id`、`model_inst_id` 也没有统一绕过拨测专用路径。

依据：[Uptime Resolver](../../internal/enrich/uptime/resolver.go)、[任务表](../../internal/enrich/datasources/uptime_client.go)、[投影](../../internal/enrich/uptime/resource.go)。

### 5.9 日志指标、日志关键字

- 输入：策略分类、日志主题等信息。
- `monitor-resource` 在日志场景只提供业务等上下文，不主动查主对象；`log` 补日志主题与展示信息。
- 不会仅凭日志主题生成 OneModel `model_inst_id`。需要对象关联时，取决于来源配置的 `resource` 或 CMDB 规则。

依据：[监控分派](../../internal/enrich/processors/monitor_resource.go)、[Log](../../internal/enrich/processors/log.go)。

## 6. K8s：模型、维度与实例 ID

### 6.1 先决定模型，再查询实例

- `k8s` 优先读取有效视图中已有的 `model_id`，多处非空值必须一致；没有模型才从原始维度判断。
- 从维度判断的顺序为：PVC、PV、`workload_kind=Pod + workload_name`，然后 Container、Pod、Workload、Service、Node、Namespace、Cluster。
- PV/PVC 的 `pod_name`、`namespace` 可能属于采集 Pod，不能让它们把存储资源改成 Pod。PV 的命名空间上下文会被清空。
- DATA 已确认属于 K8s 时，按结果维度重新判断，允许 Container 指标聚合为 Namespace；完整的事件两字段优先于 K8s 策略模型。
- DATA 对独立 Pod 另有修正：存在 `container_name` 时选 Container；独立 `k8s` 的原始推断顺序仍未完全统一。
- 选择模型后缺少必需维度就报错，不自动退成更粗对象。

以下每个查询都隐含 `bk_tenant_id + model_id`。表述中的 `attributes.*` 是 OneModel 属性，ES 实际使用第 4 节的类型槽。

### 6.2 Cluster：cw-K8s_Cluster

- 输入：`bcs_cluster_id`。
- 查询：`attributes.cluster_id = bcs_cluster_id`。
- 输出：命中的 `model_inst_id`；DATA 未命中时可使用 `bcs_cluster_id`。

### 6.3 Namespace：cw-K8s_Namespace

- 输入：`bcs_cluster_id`、`namespace`。
- 查询：`attributes.cluster_id = bcs_cluster_id`、`attributes.namespace = namespace`。
- 输出：命中的 `model_inst_id`；DATA 未命中时可使用 `bcs_cluster_id|namespace`。

### 6.4 Service：cw-K8s_Service

- 输入：`bcs_cluster_id`、`namespace`、`service`。
- 查询：`attributes.cluster_id`、`attributes.namespace`、`attributes.name = service`。
- 输出：命中的 `model_inst_id`；DATA 未命中时可使用 `bcs_cluster_id|namespace|service`。

### 6.5 Workload：cw-K8s_Workload

- 输入：`bcs_cluster_id`、`namespace`、`workload_kind`、`workload_name`。
- 查询：`attributes.cluster_id`、`attributes.namespace`、`attributes.type = workload_kind`、`attributes.name = workload_name`。
- 输出：命中的 `model_inst_id`；DATA 未命中时可使用 `bcs_cluster_id|namespace|workload_kind|workload_name`。

### 6.6 Pod：cw-K8s_Pod

- 输入：通常为 `bcs_cluster_id`、`namespace`、`pod_name`。
- 查询：`attributes.cluster_id`、`attributes.namespace`、`attributes.name = pod_name`。
- `workload_kind=Pod` 时，Reader 改用 `workload_name` 查询 `attributes.name`，生成 ID 时也使用该值。
- 输出：命中的 `model_inst_id`；DATA 未命中时可使用 `bcs_cluster_id|namespace|pod_name`，独立 Pod 使用 `bcs_cluster_id|namespace|workload_name`。
- 当前前置必需字段检查仍要求 `pod_name`；不能仅据 Reader 支持 `workload_name` 就宣称仅有该字段也能通过完整链。

### 6.7 Container：cw-K8s_Container

- 输入：`bcs_cluster_id`、`namespace`、`pod_name`、`container_name`。
- 查询：`attributes.cluster_id`、`attributes.namespace`、`attributes.pod_name = pod_name`、`attributes.name = container_name`。
- 输出：命中的 `model_inst_id`；DATA 未命中时可使用 `bcs_cluster_id|namespace|pod_name|container_name`。

### 6.8 Node：cw-K8s_Node

- 输入：`bcs_cluster_id`、`node`。
- 查询：`attributes.cluster_id`、`attributes.name = node`。
- 输出：命中的 `model_inst_id`；DATA 未命中时可使用 `bcs_cluster_id|node`。

### 6.9 PV：cw-K8s_PersistentVolume

- 输入：`bcs_cluster_id`、`persistentvolume`。
- 查询：`attributes.cluster_id`、`attributes.name = persistentvolume`，不加命名空间条件。
- 输出：命中的 `model_inst_id`；DATA 未命中时可使用 `bcs_cluster_id|persistentvolume`。

### 6.10 PVC：cw-K8s_PersistentVolumeClaim

- 输入：`bcs_cluster_id`、`namespace`、`persistentvolumeclaim`。
- 查询：`attributes.cluster_id`、`attributes.namespace`、`attributes.name = persistentvolumeclaim`。
- 输出：命中的 `model_inst_id`；DATA 未命中时可使用 `bcs_cluster_id|namespace|persistentvolumeclaim`。

### 6.11 已有 model_inst_id、未命中与查询失败

- `monitor-resource` 的 DATA 已有完整两字段时，可直接按模型和实例 ID 查询；后续 `k8s` 仍要求相应维度，并按属性重新查询。
- `k8s` 查询命中后会核对有效视图中已有的 `model_id`、`model_inst_id`；不一致报 `identity_conflict`，不能覆盖成另一实例。
- 只有 **DATA 策略、查询成功但零命中** 才按以上维度顺序用 `|` 拼接 `model_inst_id`，保留 `partial + dependency_invalid`。
- 生成前校验必需字段，拒绝字段值自带 `|`、非法文本或超长 ID；已有两字段与生成结果冲突也不能覆盖。
- 查询报错、响应租户/模型不符，以及实例策略未命中，都不采用这种处理。按维度生成 ID 不表示 OneModel 已经存在该实例。
- 命中的实例 ID 始终以响应为准，不强制用维度重建。
- 查询主实例之外，还可独立查询 Namespace、Cluster 补业务和集群名；PV 不查 Namespace。上下文查询成功不表示主实例已命中。

依据：[模型选择与一致性](../../internal/enrich/processors/k8s.go)、[DATA 模型选择](../../internal/enrich/processors/metric_resource.go)、[维度到属性](../../internal/enrich/datasources/k8s_reader.go)、[未命中时生成 ID](../../internal/enrich/processors/k8s_identity.go)。

## 7. APM：应用、服务、服务实例、接口

### 7.1 先查询应用

**输入字段**

- 有效正整数业务；`dimensions` 或 `extra_data.additional_dimensions` 中的 `app_name`、`service_name`、`bk_instance_id`、`span_name`。
- 两处维度出现同名键就拒绝合并，即使值相同也不静默选取。
- 缺少 `app_name` 时可从策略 `data_source=应用-...` 或结果表名 `_bkapm_metric_...` 提取；旧 `dimensions.apm_app_id` 仍可作为回退。

**查询规则**

- 先确认输入业务是否全局业务，再查同租户 `kapm_namespace` 的 `namespace=app_name OR normalized_name=app_name`，排除已删除记录。
- 普通业务增加 `bk_biz_id` 条件；全局业务在租户内查，应用重名不能任选一个，资源业务取命中的应用记录。
- 应用 ID 来自 `namespace_id`，不是直接使用输入的应用名称。

依据：[应用解析](../../internal/enrich/processors/apm.go)、[APM 应用表](../../internal/enrich/datasources/apm_application_client.go)。

### 7.2 各模型的 model_inst_id

以下 `namespace_id` 均指第 7.1 节取得的应用 ID；既有回退也可能提供该 ID。

| model_id | 所需字段 | model_inst_id |
| --- | --- | --- |
| `cw-application` | 应用 ID | `namespace_id` 的字符串形式 |
| `cw-service` | 应用 ID、`service_name` | `namespace_id\|service_name` |
| `cw-service_instance` | 应用 ID、`service_name`、`bk_instance_id` | `namespace_id\|service_name\|bk_instance_id` |
| `cw-interface` | 应用 ID、已有 `model_inst_id`，及可选服务范围 | 使用已有 `model_inst_id` 查 OneModel，并核对 `attributes.app_id`、已提供的 `service_name` |

- 模型优先由策略、事件字段和指标库约束确定；都未确定时，才按服务实例 → 服务 → 应用判断。
- APM 与非 APM 输入混用、不同 `apm_application.*` 来源混用、模型不在指标适用集合或已有实例 ID 与计算值不同，都报告冲突。
- 应用、服务、服务实例的 ID 主要按上述规则生成，不逐个查询 OneModel 实例。接口是当前明确需要已有实例查询验证的路径。
- 仅有 `span_name` 不足以拼接口 ID；接口还涉及 `category`、`span_kind`，当前代码没有补全这组规则。

### 7.3 输出及目标差距

- `monitor-resource` 补 `model_id`、`model_inst_id`、模型表中的 `model_name`；模型名称查询失败可保留已有两字段并报 `partial`。
- `apm` 输出 `apm_app_id`、`apm_app_name`、`apm_service_name`、`apm_instance_name`、`apm_interface_name` 等，也输出模型、实例 ID。
- 当前 `monitor-resource` 仍输出 `bk_inst_id=应用 ID`；`apm` 本身不输出这个字段，执行它也不会自动清除前序值。
- 没有应用 ID 时，当前模型和实例 ID 都为空；还没有做到仅凭维度独立补模型名称。
- 策略或指标库选了应用模型时，不会因有服务维度就必然改成服务模型，因此尚未全路径满足“最细粒度”。

依据：[apmResourceIdentity](../../internal/enrich/processors/metric_resource.go)、[APM Processor](../../internal/enrich/processors/apm.go)、[processAPMResource](../../internal/enrich/processors/monitor_resource.go)。

## 8. 业务、主机、CMDB 集群、模块和云区域

- `bk_biz_id`、`bk_biz_name` 是资源所属业务；策略管理业务不要求与资源业务相等。只有实际依赖业务查询的 APM、CMDB 服务实例等分支才强制验证业务。
- 实例已有 `bk_biz_id` 时直接使用；`bk_biz_ids` 只有一个元素时可以取唯一业务。非空多业务数组表示已有归属，不能随意选其中一个，也不因此当作“缺业务”找关联主机。
- 监控主机通常查主机拓扑；非主机缺业务、采集任务远程采集等场景仍可沿模型的 `host_related_field` 查关联主机，再补业务和拓扑。对象的模型、实例 ID 保持原值。
- 关联主机通过同租户 `kingeye_topo` 的 `cmdb_fact` 关系查询，再读取 `cw-Host`；主线业务拓扑使用带 `index_prefix` 的 `cmdb_biz_topo_host_membership`、`cmdb_biz_topo_node`。

### 8.1 cmdb_set_module_enabled 到底控制什么

```yaml
enrich:
  cmdb_set_module_enabled: false
  processors:
    - type: strategy
    - type: monitor-resource
```

- 默认 `false`，控制监控场景额外查询、补充 `bk_set_id/name`、`bk_module_id/name`。
- 关闭时，已验证主机实例有唯一业务就复用该业务；否则仍可查 membership 确认业务，不额外读祖先集群/模块节点。
- 开启时，监控投影稳定选择一条路径，输出单个集群、模块；自定义 CMDB 拓扑可取同业务下全部集群/模块。
- 这不是字段清除开关：实例自带的 `bk_set_*`、`bk_module_*` 仍可保留；非主机经关联主机补拓扑也未统一限制。
- 自定义 CMDB 规则和显式拓扑操作有独立配置，不受这个监控开关统一控制。

### 8.2 云区域和主机地址条件

- 监控实例投影和关联主机云区域补充只对 `model_id=cw-Host` 生效。
- `monitor-resource` 最终确认是非主机时，显式输出空 `bk_cloud_id`、`bk_cloud_name`，遮蔽来源已有的云区域结果。
- 该处理不删除原始维度，也不取消主机地址查询中的 `bk_cloud_id` 条件；不同云区域的同 IP 仍须区分。
- 当前没有独立的云区域输出开关；`cmdb_set_module_enabled` 不控制它。
- `cloud_resource` 仍按自己的映射输出云平台信息，后续自定义规则也可能改字段。不能把监控步骤的清空规则扩大成全链保证。

依据：[实例属性投影](../../internal/enrich/resource_values.go)、[Scope.HostTopology](../../internal/enrich/scope.go)、[关系与拓扑读取](../../internal/onemodel/collect_topology_client.go)、[监控输出](../../internal/enrich/processors/monitor_resource.go)、[配置定义](../../internal/config/event_source.go)。

## 9. 自定义 CMDB / fields 规则

**输入与查询**

- 规则指定 `lookup.model_id`；可按 `lookup.where` 查询属性。
- 带 `identity` 的规则先读取当前模型字段决定是否适用，再依次读取 `model_inst_id`、`bk_inst_id`、`cw_object_model_inst_id`。
- 读到实例值时，当前会直接作为 OneModel 根字段 `model_inst_id` 查询；这里的旧字段处理与第 5.2 节 CMDB 属性映射不同。
- 没有实例值时才使用 `lookup.where`。查询始终在 Event 租户内；`expect=first` 明确取稳定首条，不应与内置 `FindInstance` 的唯一命中要求混淆。

**输出字段**

- 带 `identity` 的规则成功后可写 `bk_obj_id`、`model_id`、`model_name`、`bk_inst_id`、`model_inst_id`，默认写入 `labels`，也可配置目标路径。
- `model_name` 可来自规则配置；`bk_inst_id` 优先用对应 CMDB 属性，没有时当前还会用 `model_inst_id`。
- 后续赋值和拓扑操作按规则继续，不能仅据前面已有两字段就认定后续规则不会写它们。
- `cmdb-access` 根据最终两字段补权限标签和动态分组；生成了标签不等于重新验证过 OneModel 主实例。

依据：[自定义查询](../../internal/enrich/custom/run.go)、[模型匹配、字段绑定与拓扑](../../internal/enrich/custom/identity.go)、[CMDBAccess](../../internal/enrich/processors/cmdb_access.go)。

## 10. 查询结果如何判断，哪些场景仍需确认

### 10.1 不要只看 model_inst_id 是否非空

| 观察到的结果 | 能说明什么 |
| --- | --- |
| 只有 `model_id`、`model_name` | 模型上下文已取得，不代表实例已找到 |
| 通用 `resource` 成功输出两字段 | 已按同租户模型和实例查询验证；仍应看后续步骤有没有改字段 |
| K8s DATA 的两字段，加 `partial/dependency_invalid` | 可能只是查询零命中后按维度生成 ID，不能当作 OneModel 实例已存在 |
| APM 应用/服务/服务实例的两字段 | 可由应用 ID 和维度生成，不代表逐个读取了 OneModel 实例 |
| 拨测、云资源的两字段 | 来自对应业务表，不代表 OneModel 主实例已查验 |
| `bk_host_id`、集群名或业务名有值 | 可能只是上下文补充成功，不代表主对象已定位 |

### 10.2 失败和后续处理

- 字段缺失、非法、冲突分别看 `missing_field`、`invalid_field`、`identity_conflict`；依赖故障或不合格结果看 `dependency_invalid` 及 `dependency`。
- 核心实例失败时，不把输入值直接包装成查询结果；可独立成立的模型、业务、展示信息仍可保留。
- 监控分支已确认模型名称或 CMDB 映射时可返回 `partial`，否则必要实例缺失可能 `failed`；不能仅按最终 Event 状态倒推某一步成功。
- 单个 Processor 失败不阻断后续 Processor；后续仍可能发布其他信息。父 Context 取消则立即终止。
- 已完成 Event 的结果固定；当前没有后台自动补丰富。OneModel 后续同步到实例，不会自动修复历史 Event 或已创建 Alert 的快照。
- 若启用 `reuse_on_active`，指定步骤还可能复用活跃 Alert 的成功快照；这不能当作本次又查询了一次 OneModel。

### 10.3 后续统一前需要明确的决定

1. 已有完整 `model_id`、`model_inst_id` 时，策略、指标库、Subject、旧 CMDB 字段冲突是否一律只作诊断，还是仍拒绝该步骤。
2. 各分支是否统一接受字符串实例 ID，并取消 BaseTarget 的正整数限制；CMDB 原生 ID 的正整数要求应另行保留。
3. 采集任务、拨测、云资源没有这两个字段时，是否停止生成对象实例，或明确列出获准例外。
4. K8s 是否统一先按已有两字段查询，维度只补上下文；以及 DATA 零命中时按维度生成 ID 是否继续保留。
5. APM 如何解除策略模型对最细粒度的限制、无应用 ID 时如何输出模型、何时清除 `bk_inst_id`，以及接口缺少维度时的行为。
6. 集群、模块、云区域的开关是只停止额外查询，还是同时清除最终输出；如何覆盖实例自带属性、专用 Processor 和自定义规则。

这些是当前实现与第 2 节原则之间的具体收敛点。本文未修改运行行为，也未把未决定的处理方式写成已有支持。

## 11. 本次核验范围

本次按代码逐项核对，并运行现有普通测试：

```bash
go test ./internal/enrich/processors ./internal/enrich/basetarget \
  ./internal/enrich/collect ./internal/enrich/uptime \
  ./internal/enrich/datasources ./internal/enrich/custom ./internal/onemodel
```

重点证据包括：[通用来源字段](../../internal/enrich/processors/resource_subject_test.go)、[远程主机](../../internal/enrich/processors/remote_host_test.go)、
[K8s DATA 聚合与未命中](../../internal/enrich/processors/data_k8s_identity_test.go)、[PV/PVC](../../internal/enrich/processors/k8s_volume_test.go)、
[APM 来源和接口](../../internal/enrich/processors/apm_metric_source_test.go)、[实例响应校验](../../internal/onemodel/onemodel_client_test.go)。
以上 7 个包的 1324 项现有普通测试通过。本次仅修改文档，未运行完整 `make check`、线上依赖查询或 KAC 页面验收。
现有测试不替代线上实例命中和实际发布配置的验证。
