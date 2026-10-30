# Event Enrich 现行设计与 KAC 迁移基线

> Event Enrich 主流程已实现。三类策略、可靠投影的开发范围及验收进度见
> [开发方案](event-enrich-and-alarm-policies.md)。

状态：内置丰富场景与 KAC 迁移基线；自定义规则、补丁、预览和配置转换以[自定义丰富开发规格](custom-enrichment.md)为准。

适用代码：`internal/enrich`、`internal/lifecycle/process`、`internal/config`、`internal/telemetry`
事实基线：当前工作区代码与针对性普通、竞态回归；不代表完整 make check 或线上验收

本文统一描述 Linkd Event Enrich 的现行契约、实现边界、KAC 行为迁移状态和后续顺序。代码与本文发生差异时，以代码和测试判断当前已实现行为，并同步修正文档。

## 1. 定位与总体状态

Enrich 在 Lifecycle 为每条不同 Event 同步执行，输入为 `domain.Event`。来源事实不被覆盖，结果保存为：

```text
Event.enrich_status / enriched_at / enrich_config_digest / enrich_source_version
Event.enrich.evaluations[].{severity,status,data}
data.processors[] = 有序处理器信封
```

每个 evaluation 有独立文案、补丁和诊断，同一 Event 共享只读查询缓存；不同 Event 不共享查询缓存。
Processor 可显式复用活跃 Alert 已保存的成功结果，见 [2.5](#25-活跃告警丰富复用)。
结果通过专用 CAS 完成后固定，包括 partial/failed；已完成处理的重投不重新丰富。
创建 Alert 时复制 opening Event 对应等级的 data，`trigger_event_id` 同时标识丰富来源，`enrich_severity` 固定记录快照等级。
后续触发、低等级抑制、原地升级和终态 Event 仍各自保存完整丰富结果，默认执行全部步骤，也可按配置复用部分步骤；不刷新该 Alert 的来源/展示/丰富快照。
close_and_create 新 Alert 使用本次 Event 的新快照。

当前可配置生产链由 EventSource 按需组合，核心链为：

```text
strategy → monitor-resource → display → metric → source
```

日志、Cloud、K8s、APM 可分别插入 `log`、`cloud_resource`、`k8s`、`apm` Processor。

当前总体成熟度（按代码、测试和当前数据源装配判断）：

| 范围 | 完成度 | 判断 |
| --- | ---: | --- |
| Enrich 运行框架 | 约 90% | Router、配置校验、链式执行、Scope 隔离、状态聚合、diagnostics、生命周期接入、类型化 Context 和 Observation 已完成 |
| BaseTarget / KAC `BASE_COLLECT` | 约 90% | 六个二级分支、生产 Reader、请求内缓存、业务/云区域/标签、派生与 KAC fixture、主要失败矩阵已完成；真实边界样本继续补充 |
| DATA | 约 90%～95% | 已接入 Standard Kafka → Lifecycle → Strategy/Resource/Display/Metric/Source 主流程；普通时序、system 主机、uptimecheck、hardware_、多模型、枚举、衍生指标、PromQL、函数和查询翻译已完成；真实样例继续补充 |
| LOG_METRIC / LOG_KEYWORD | 约 75%～85% | 分类、日志 Processor、`log_theme_logtheme` MySQL Reader、接口入参租户查询、主题回退、关联信息、内容裁剪、主流程装配和派生测试已完成；真实数据库命中与专用查询契约继续验证 |
| Cloud / K8s / APM | Cloud 约 65%～75%，K8s 约 75%～85%，APM 约 75%～85% | 三类 Processor、路由注册、派生身份和主要失败矩阵已完成；CloudResource、K8s OneModel 与 APM Application Reader 已接入生产装配，剩余真实回放继续推进 |
| 生产环境闭环 | 约 55%～60% | BaseTarget 的主要 MySQL、OneModel、关系边和业务拓扑协议已完成第一轮核验；日志、Cloud、K8s、APM 的主要外部适配器已接入，真实命中和端到端回放仍待验证 |

这些百分比用于安排迁移顺序，不属于发布验收指标。

## 2. 已确认边界

### 2.1 输入与副作用

- Enricher 输入固定为 `enrich.Input{Event}`。
- 每次 Enrich 拥有一份只读原始 Event，各等级 Scope 共享该事实；每个等级只建立一个 `domain.EnrichEvaluation` 工作对象。`Scope.Event()` 保留隔离快照接口，受控内置步骤通过 `Facts()` 只读访问。
- Processor 只执行外部只读查询和确定性转换。
- Processor 不覆盖原始 Event 或已存储 Alert；运行时补丁只修改本等级工作对象的白名单字段，结果仍以 patches 保存。
- `SourceRawData` 用于人工追溯，Enrich 不将其作为默认输入或缺字段兜底。
- Enrich 在结果提交前可因 CAS 冲突或进程恢复重复执行，已提交结果重投直接复用，因此实现必须幂等且没有外部业务写入。

### 2.2 失败语义

- 父 Context 取消立即终止处理。
- 单个 Processor 的 error 或 panic 转换为该 Processor 的 failed envelope，后续 Processor 继续执行。
- 可预期输入错误和依赖故障通过 `ProcessorResult` 与 diagnostics 表达。
- 场景已有核心身份但可选名称、拓扑或标签依赖失败：Processor 返回 `partial`，保留已确认字段。
- 场景核心身份缺失、输入非法、实例未命中或响应身份不匹配：已有已验证模型名称或 CMDB 映射时 Resource 返回 `partial`，发布独立成立的模型、业务和展示补偿；没有可用上下文时返回 `failed`。两种情况都保留诊断，禁止生成未验证的实例绑定与实例标签。
- Chain 最终状态按现有聚合规则计算：所有适用 Processor 成功为 `succeeded`，所有适用 Processor 失败为 `failed`，其余组合为 `partial`；因此单个 Resource failed 且其他 Processor succeeded 时，该 evaluation 状态为 `partial`；Event 再汇总全部 evaluation。
- Enrich failed 不阻断 Alert 创建；Lifecycle 会持久化合法失败 payload。
- 当前不提供查询重试、Enrich 重试、后台补丰富或已创建 Alert 的重丰富接口。

### 2.3 与 KAC 的目标差异

KAC Converter/Cleaner 直接构造旧 Alarm 字段。Linkd 将对应业务含义放入 `Alert.Enrich`，同时保留原 Alert 字段。迁移验收比较字段含义、资源身份、展示文本、指标信息和依赖结果；旧对象布局、线程池、Kafka 推送和生命周期实现不作为复制目标。

### 2.4 产品派生逻辑的后续归属（2026-10-16）

2026-10-27 恢复指标参数读侧迁移；2026-10-29 调整为所有告警源均停止生成指标查询参数，部署仍须满足下述验收前提。
所有告警源的 Metric 丰富结果均不再生成 `metric_query_params`。内置来源由 KAC 根据准确 Event、
原始维度和策略执行引用组装查询。Linkd 保留指标身份、名称、单位、资源身份和冻结文案等处理侧事实。
外部来源的具体取数继续由来源插件承接。Linkd 不再读取、编码或输出历史指标查询参数；不清理、不回填存储中的旧数据。

部署必须先完成 Kingeye MR !15890 的 KAC 兼容支持和真实曲线验收，再部署停止参数生成的 Linkd。
KAC 输出省略 `metric_query_params`，由新版读侧组装查询；不新增 `metric-facts` 接口，不改写执行历史。
KAC 按历史 `agg_dimension` 与原始 Event 实际维度的交集生成分组和过滤，不补造缺失 `target`。
当前原生 `alarm_id=alert-*`、`event_id=event-*` 的关联承接及无参数真实取数尚未通过验收，不能据代码提交认定已可切换部署。
策略 URL 仍由 KAC 从 `strategy_context` 组装；APM/K8s/日志专属丰富、文案降级和其他纯展示收敛范围保持既有约定。
具体输入及验收合同见 [KAC 投影](../reference/contracts/kac-alert-projection-v1.md)和 Kingeye
`docs/specs/kac-linkd-integration.md` 第 8.6 节；本地实现不表示已部署。

### 2.5 活跃告警丰富复用

2026-10-27：`enrich.processors[].reuse_on_active` 默认 `false`。显式开启表示接受该步骤的匹配条件和输出
在同一告警生命周期、同一快照等级内固定，跳过该步骤的 Match、外部查询和 Process。

- Lifecycle 在 fingerprint lease 内提前执行原有活跃 Alert 查询，结果（包括未命中）直接传给 Enrich 和计划准备。
  Redis/存储故障沿用原有失败语义；不会把查询失败解释成未命中。
- 复用按 `bk_tenant_id/event_source_id/fingerprint` 隔离，并且只适用于 `Alert.enrich_severity` 对应的 evaluation。
  原地升级只改变当前 severity，不改变快照等级；其他等级正常执行。
  同条 Event 全等级关闭后再触发，或当前等级被移除而系统关闭时，新生命周期不沿用旧快照。
- 只有指定 Processor 的 `succeeded` 结果可复用；缺少步骤、快照等级或成功结果时正常执行。
  `partial/failed/skipped` 不作为后续新 Event 的复用来源。缺少元信息的现有 Alert 不补查 opening Event、不回填。
  Elasticsearch 的 `enrich_severity` 映射由既有索引对账追加，MySQL 沿用 Alert JSON payload，不新增表或回填任务。
- 在当前链的原位置应用保存的补丁，并恢复内置处理器的类型化上下文，保证后续字段、展示和指标步骤能够读取它。
  原始 Event、已有 Alert 和其他等级的结果保持独立。
- 当前 Event 保存完整的逐等级处理器结果。复用步骤仍是 `succeeded`，通过 `reused_from_alert_id` 标识来源 Alert，
  规则 Trace 沿用原始执行记录；不能把历史耗时解释为本次真实执行耗时。步骤指标以 `outcome=reused` 区分。
- 步骤保持启用复用时，修改规则不会自动刷新活跃 Alert 快照；新生命周期使用新配置。关闭复用后，下一条新 Event
  正常执行。已经冻结结果的同一 Event 重投始终沿用已保存事实，包括之前的失败或复用结果。
- 调试模拟不查活跃 Alert，始终执行当前配置；历史事实查询保留复用来源。复用规则用原规则 ID 标识，
  避免把当前发布中的规则名称冒充原执行名称，也不为展示增加历史配置查询。

资源、CMDB 属性可按业务需要启用；这同时意味着接受拓扑、动态分组等结果在本生命周期内固定。
依赖本次数值、发生时间、动作或文案的规则应保持默认执行；例如 Metric 包含当次异常开始时间，Display 会加工当前内容。
该优化减少重复依赖查询和计算，不减少 Event 丰富结果的持久化次数，也不新增独立缓存或后台刷新任务。

## 3. 运行架构

```text
Repository 读取并恢复标准 Event，Lifecycle 直接使用
  → 在 fingerprint lease 内一次查询活跃 Alert，供 Enrich 和计划共用
  → 按 EventSourceID 复用当前任务的丰富配置和运行时，积压事件不回读历史来源版本
  → 保留 EventSourceVersion 作为接入事实，记录 enrich_source_version 作为实际丰富配置版本
  → 逐 evaluation 执行或复用 Processor，各等级共享请求缓存
  → 保存完整 Event 丰富结果 CAS
  → 冻结生命周期计划并创建/推进 Alert
  → 新 Alert 复制 opening Event 的选中等级结果
  → FinalHook 输出 Alert 快照
```

原始 Event 在整次 Enrich 入口隔离一次；规则查询直接使用工作对象的 Go 视图，动态 JSON 在输入边界展开，不在步骤间反复构造完整 Event。操作/Processor 使用局部撤销记录，成功后释放旧值。新结果在输出协议边界规范化一次。
Lifecycle 将冻结对象绑定到实际保存的等级 payload，策略读取直接复用；恢复时仅从已保存补丁惰性构建一次。缓存只属于本次处理，按事件、租户、来源版本、配置摘要和 payload 检查；不入 JSON、不跨消息复用，Normalize/Restore 清除引用。
新丰富结果在提交边界检查等级覆盖、状态和载荷预算；已经冻结的结果在后续计划、关联更新中直接复用。
内容生成仅检查新 content 的限制，不为检查一个字段而重新校验整份 opening Alert。

主要代码位置：

| 关注点 | 文件 |
| --- | --- |
| Lifecycle 端口与调用保护 | `internal/lifecycle/enrich.go`、`internal/lifecycle/enrich_observer.go` |
| Chain、隔离和状态聚合 | `internal/enrich/enricher.go`、`status.go` |
| Payload 和 diagnostics | `internal/enrich/result.go` |
| Scope 与 Reader 接口 | `internal/enrich/scope.go` |
| EventSource 路由与注册 | `internal/enrich/assembly/router.go` |
| Processor | `internal/enrich/processors/` |
| Collect 场景 | `internal/enrich/collect/` |
| Uptime 场景 | `internal/enrich/uptime/` |
| 外部读取适配器 | `internal/enrich/datasources/` |
| 生产装配 | `internal/lifecycle/process/enrich.go` |
| Observation | `internal/telemetry/enrich.go`、`enrich_datasource.go` |

## 4. 配置与装配

以下 `enrich` 片段属于 EventSource Release：

```yaml
enrich:
  processors:
    - type: strategy
    - type: monitor-resource
    - type: display
    - type: metric
    - type: source
```

约束：

- Processor 出现即启用，列表顺序决定执行和输出顺序。
- 同一类型最多出现一次。
- 空列表使用 Noop，结果为 succeeded 且 `processors` 为空。
- 未注册 Processor 或非法配置使 Release 校验失败。
- `strategy`、`monitor-resource` 当前要求 MySQL 与 Elasticsearch；`display`、`metric`、`source` 要求 MySQL。
- `configs/linkd.pm2.yaml` 当前示例使用空链；生产启用需要在 EventSource Release 中配置处理器。

当前 Router 注册：

```text
strategy
monitor-resource
display
metric
log
cloud_resource
k8s
apm
source
```

`log`、`cloud_resource`、`k8s`、`apm` 已完成注册和派生测试；生产链是否启用仍由 EventSource 的 Processor 列表决定。当前生产装配已提供 MySQL、OneModel、LogTheme、CloudResource、K8s 和 APM Application Reader。

## 5. Payload 契约

每个 `Event.enrich.evaluations[].data`，以及复制到 `Alert.enrich` 的快照，固定为：

```json
{
  "processors": [
    {
      "monitor-resource": {
        "status": "partial",
        "patches": [],
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
| `patches` | array | 已求值的有序 JSONPath 补丁，无输出时为 `[]` |
| `trace` | array，可省略 | 自定义规则和操作的执行状态、命中数及耗时 |
| `diagnostics` | array，可省略 | 稳定、脱敏的输入或依赖诊断 |

`value` 仅用于读取历史记录及内置处理器的内部 DTO；新结果不写 `value` 或最终 `values`。
按处理器顺序应用补丁可得到临时有效视图，原始 Event 不改变。详细目标白名单和顺序见[自定义丰富规格](custom-enrichment.md)。

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
| 实例／租户／模型响应身份不匹配 | 已确认模型上下文时 `partial`，否则 `failed`；保留 `dependency_invalid=onemodel` | 仅保留独立验证的模型和来源业务，不使用错误响应、不生成实例标签 |
| 实例未命中 | 已确认模型上下文时 `partial`，否则 `failed`；保留 `dependency_invalid=onemodel` | 已确认模型上下文、业务名称、可信主机主键的独立拓扑补偿、Display 回退；不生成实例绑定 |
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
strategy_kind
is_default
strategy_instance
data_source
```

以上为新事件的 9 个输出字段；`strategy_instance` 保存对象模型、CMDB 对象、实例和模型分类的身份。
历史结果中的 `url` 仅继续解码、重放，新丰富不生成 URL。

当前策略查询身份仅由事件的 `bk_tenant_id + labels.strategy_id + labels.strategy_version` 构成，查询和 Strategy 关联信息生成不要求 `labels.bk_biz_id`。鲸眼策略仅从 Kingeye MySQL `alarm_strategy_execution_revision` 的不可变快照读取，按租户、`strategy_id` 与 Event `strategy_version` 对应的 `strategy_revision` 精确定位，并校验唯一性、schema、字节数、SHA-256 和快照身份；不查询 SplitRecord 当前态、旧 Set/Config 或 latest。管理业务取自唯一历史记录；查询投影保留快照中的业务值（包括非 BKCC 空间对应的负值），缺失时透传为 0，不作为查询投影前置条件。Resource、Display、Metric 不再统一要求来源业务，也不要求策略管理业务与资源业务相等；K8s 和 canonical 实例按租户及自身身份定位。只有 APM 应用、服务实例等实际使用业务作查询条件的分支校验正整数业务；业务名称补充没有有效业务时跳过查询。APM、Log 先判断场景，不适用时跳过。Linkd 输出策略关联事实，KAC 在读取时生成导航 URL。

**2026-10-19 用户确认：`strategy_name` 和 `data_source` 都保留在 Linkd，不再继续拆分到 KAC，也不列为后续纯展示转换的迁移项。**

- `strategy_name` 继续由 Linkd 生成并冻结：有模板名称时，首个查询配置含 PromQL 则使用模板名，否则有别名时使用“模板名-别名”，无别名时使用模板名；没有模板名称时回退 `spec.name`，结果仍为空则回退声明式资源名。该名称还用于策略条件读取、合并模板和 `item` 缺失时的回退，不能仅在 KAC 页面读取时生成。
- `data_source` 继续原样取自 `strategy.Spec.DataSource`，作为数据来源标签投影到 KAC；保留该字段不表示它可作为合并字段。
- KAC 继续根据关联事实组装产品导航链接；策略执行身份和关联事实仍由 Linkd 冻结、投影。

实现见 [Strategy Processor](../../internal/enrich/processors/strategy.go)、
[处理侧字段读取](../../internal/policy/view.go)和 [KAC 输出适配](../../internal/lifecycle/kachook/message.go)。

### 6.2 monitor-resource

适用场景：BaseTarget、DATA、Cloud/APM 等需要资源身份的链路。Resource 只在取得可信模型和实例身份后生成实例级标签；场景级 Processor 可在 Resource 之外补充专用资源投影。

```text
bk_obj_id
bk_inst_id
bk_host_id
bk_host_name
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

`dynamic_group_id` 的代码类型为 `[]string`。配置 `resources.dynamic_group` 后，Resource 按租户、
`model_id` 和 `model_inst_id` 读取当前 Kingeye 写入端的 Redis 投影，将整数 ID 转为十进制字符串；
未配置或未命中时为空数组。旧迁移设计曾记录 `[]int64`，目前以已实现的输出类型为准。

Resource 对同时含主机 IP 和云区域的 Event 独立补充 `bk_host_id`、`bk_host_name`，接受
`bk_target_ip` / `bk_target_cloud_id` 与 `ip` / `bk_cloud_id`，云区域 `0` 有效。查询必须命中
同租户唯一 `cw-Host`；主机名只取真实 `bk_host_name` 属性。主机未命中、响应无效、别名或
显式身份冲突以及主机名缺失均保留诊断，已有资源结果降为 `partial`，不以展示名代替主机名。
这两个字段不改变 MySQL 等原对象的模型、实例、业务或展示字段。主机告警自身还应解析实例绑定；
DATA 的模型由目录确认后统一进入主机解析，不再以声明中的物理表名为前提，目录引用可不带表名。
上述内容是新丰富结果的生成规则，不回填历史 Event 或 Alert。

### 6.3 display

适用场景：所有需要统一标题、内容、对象和维度展示的链路。日志、Cloud、K8s、APM 可以在 Display 前后提供场景字段，Display 本身继续保持 Alert 原始字段只读。

```text
title
content
object
dimensions
dimension_text
```

结构化 dimensions 保留场景生成顺序；`dimension_text` 使用副本排序和格式化。当前大部分场景的 content 直接继承 `Event.Content`。

PromQL 的展示维度取自 Event 实际返回的标签及附加维度，不依赖可能为空的 `agg_dimension`，也不补造被表达式聚合掉的标签。规范模型身份条目保持原顺序，其余结果标签按键排序；名称优先取指标元数据，未知或空名称保留原键，展示值仍使用已确认的资源名称转换。`__NO_DATA_DIMENSION__` 控制标记不作为结果标签展示。普通指标继续按声明分组维度选择展示项，所有来源均不再由 Linkd 生成 PromQL 查询参数；历史参数不再读取或输出。

EventSource 的 `enrich.content_mode=bkmonitor_description` 在创建准备阶段及持续触发更新时调用内容构建器，用本次 Event 及其精确策略历史版本生成 `Alert.content`；普通 Processor 的 Result 仍只保存 Status/Data。该模式的 Display 不生成 content 覆盖补丁，不再裁剪日志、拼接对象名或重复转换单位，避免 Event 快照覆盖后续生成的 Alert 内容。`source` 模式维持既有来源文本处理。实现、事实缺口和验收范围集中见[告警内容生成方案](alert-content-generation.md)，后续 Enrich 不覆盖已有核心字段。

### 6.4 metric

适用场景：DATA、日志和其他带指标查询的链路。Metric 读取策略和指标目录投影；Cloud/K8s/APM 的专用指标仍以真实契约为准。

```text
display_name
metric_name
unit
result_table_id
metric_unique_id
aggregate_func
time_interval
where_condition
指标查询参数不属于 Metric 丰富结果，所有来源均不生成或输出
anomaly_begin_time
```

Metric 支持 StrategyItem 投影、单/多 query config 和部分衍生指标的事实丰富，不再翻译或生成取数参数。`anomaly_begin_time` 从 `Event.ExtraData` 的同名可选字段读取。

### 6.5 source

适用场景：需要告警源元数据的链路。Source 查询告警源名称失败时保留稳定来源 ID，并按公共失败语义处理。

```text
source_id
source_name
meta_info
```

`source_id` 来自 `Event.EventSourceID`，`source_name` 按租户和来源 ID 查询 Kingeye，`meta_info` 来自 `Event.SourceEventID`。

### 6.6 已注册的专用分组

`log`、`cloud_resource`、`k8s`、`apm` 已有 Processor、类型化输出、Router 注册和派生测试。LogTheme、CloudResource、K8s 和 APM Application 已接入生产 Reader；是否运行由各 EventSource 的 Processor 列表决定。

K8s Reader 按模型将来源维度映射到 OneModel 身份：Node 的 `node`、Service 的 `service`、
Workload 的 `workload_name`、Pod 的 `pod_name`、Container 的 `container_name` 分别对应实例 `name`，
并保留集群、命名空间、工作负载类型和 Pod 名等所属条件。独立 Pod 的
`workload_kind=Pod + workload_name` 表达仍受支持。缺少必要维度时在本地拒绝查询，错误链保留
具体维度；不能把其他模型的名称维度当作兜底。Display 从 Node 维度取得的名称不会被 Basic 分支
返回的空 `subject_name` 清除；其他资源分支仍遵循自己的展示覆盖规则。

## 7. DataSource 现状

### 7.1 已装配

| Reader | 后端 | 状态 |
| --- | --- | --- |
| `CWStrategyReader` | Kingeye MySQL Strategy revision 历史快照 | 已实现并装配；事件版本精确匹配，声明式上下文来自 runtime_config |
| `BusinessReader` | Kingeye MySQL | 已实现并装配 |
| `MetricReader` | Kingeye MySQL | 已实现并装配 |
| `AlarmSourceReader` | Kingeye MySQL | 已实现并装配 |
| `CollectConfigReader` | Kingeye MySQL | 已实现并装配 |
| `UptimeReader` | Kingeye MySQL | 已实现并装配 |
| `UptimeNodeReader` | Kingeye MySQL | 已实现并装配 |
| `OneModelReader.FindInstance` | Elasticsearch `kingeye_all_instance` | 已实现并装配 |
| `ModelReader` | Kingeye MySQL `object_model_v2` | 已实现并装配 |
| `CollectTopologyReader` | Elasticsearch `kingeye_topo` 与 CMDB 业务拓扑索引 | 已实现并装配 |
| `APMApplicationReader` | Kingeye MySQL `kapm_namespace` | 已实现并装配 |

所有查询应显式携带 `bk_tenant_id`，区分 found、not found、查询错误和非法响应，并传播 Context。

### 指标定义的数据源

2026-10-09 按 Kingeye 当前源码接入 metricset 指标目录。Kingeye `base/domains/metric/models.py` 中，`metricset` 仅登记来源，`metric` 持有定义和主引用；Python 消费方经 `kingeye.base.metric.get_metric/list_metrics_by_ids` 按租户和主键读取。Linkd 的 `MetricClient` 在既有只读 MySQL 连接上读取同一张 `metric` 表，不回退旧 `home_application_monitormetriclibrary`。

- 发布材料 `query_configs[].metric_ref_id` 对应目录 `metric.id`，优先用于定义点查；检测面的字符串 `metric_id` 仍只是检测标识，不转换为目录 ID。ID 未命中不回退同名或同物理字段。
- 没有目录 ID 的发布材料按 `kind=native`、`physical_field`、`result_table_id` 或 `data_label` 反查；有可用 CMDB 业务时按 `bkcc__<业务 ID>` 限定；否则使用同一不可变策略快照的 `space_uid`（支持非 BKCC 空间），同时可见平台层 `*`。没有可信空间时拒绝反查，不拼接 `bkcc__-255` 或退化为全租户查询。显式模型必须属于 `metric_object` 的适用对象集合，不用 `metric.model_id` 派生列过滤，也不沿用旧库跨模型 first() 回退。无 ID 的旧衍生材料按 `kind=derived + metric_name` 反查。最多读取两条，多义返回依赖失败。
- `metric_object` 是适用对象集合 `model_ids` 的权威来源，零个或多个对象时派生 `ObjectModelCode` 为空，恰好一个时等于该模型；不以旧 `metric.model_id` 补关系。关系读取按事件租户和命中的指标 ID 隔离，上限 8192 条，超限、重复、跨租户或指标身份不匹配均失败，不截断集合。`ref_metricset_code` 与 `record_rule_id` 同时投影，分别保留实际取数来源和物化规则身份。
- `metric_name/display_name/description/unit/value_mapping` 映射为既有丰富元数据；`dimensions[].id/name` 映射为维度 key/name。定义全名与来源编码不用于推断指标身份，不从带点编码拆取来源。
- 目录适用对象使用 `model_ids` 集合。成功命中的未关联定义不构成目录故障：没有策略或 Event 对象上下文时 `monitor-resource` 返回 `skipped`，有明确上下文时独立校验 OneModel；不生成空资源字段覆盖已有身份。K8s 来源（包括 `k8s_cluster.<集群标识>`）的资源由既有维度和已配置 Processor 定位，来源编码不代表资源类型。不新增 `pod/container` 到 `pod_name/container_name` 的映射。
- DATA 资源检查全部输入的适用对象集合；显式对象必须属于每个非空集合，没有显式上下文时只采用唯一公共模型。集合冲突、策略与 Event 冲突、不同位置的标准身份冲突使用 `identity_conflict`，不改报目录故障，也不任取第一个输入或模型。当前 evaluation 的标准身份读取 `labels`、`extra_data`、`dimensions`，完整的一对来自同一位置；非法身份不回退采集主机。
- APM 来源识别优先使用目录 `ref_metricset_code`，没有主引用时使用定义来源；识别 `apm` 和 `apm_application.<标识>`，旧的无目录 ID 查询保留表名识别。APM 与其他来源混合、或多个不同单应用来源的输入不能选择首条定位应用。应用/服务/服务实例在策略和 Event 的明确上下文、适用集合约束下定位；`cw-interface` 仅接收并校验完整标准身份，不从 `span_name` 猜测缺少 category/span_kind 的接口实例 ID。K8s 的重复模型声明必须一致，原生维度查出的实例不得覆盖显式声明的另一实例。

- 定义查询不按 `enabled/ref_status` 过滤，已触发告警仍可展示停用或失效指标的定义。时序取数继续使用已发布的检测语义；已有参数继续插件协议，无参数内置告警由 KAC 按准确 Event 与执行历史重建，外部来源由所属插件承接。
- 显式目录 ID 不要求来源业务。`native`、已绑定的 `predefined` 以及 `record_rule_id>0` 的物化型 `derived` 按有效主引用保留物理表、别名和字段；引用不完整时失败。没有物化规则和物理主引用的展开计算指标使用父定义名和单位，不能把执行面的成员位置当作父指标主引用；物理引用与物化规则冲突时失败。目录 `kind=derived` 的兼容标识仍按下述规则处理；`metric_unique_id` 对齐 KAC `develop/4.5.16` 的首查询行为，从事件租户、策略 ID 与版本定位的同一不可变快照中读取 `items[0].query_configs[0]`，按 `result_table_id + "." + metric_field` 输出，空表名保留前导点。PromQL 查询缺少定位字段时沿用现有身份解析；首查询缺失、非法或无法确定字段时 Metric 失败，不改用父指标名、后续查询或当前配方。该兼容字段不代表衍生父指标的目录身份，也不改变 `result_table_id`、父定义、取数参数或其他指标的生成规则。声明未携带 `field_tag` 时，同一历史执行快照中一致绑定的 `metric_snapshot.parent_metric_id + derived.metric_id` 可证明父指标的衍生类型，用于描述生成；不能把展开成员的 ID、表达式或过滤条件覆盖回声明。单 catalog 查询的无函数恒等别名可省略外层表达式，实际计算与函数仍保留。
- 历史 `query_config_type=dataset` 声明按同一不可变执行快照中的唯一别名、`dataset_code` 和 `dataset_semantic.metric_field` 绑定物理表/字段，再按既有租户、空间和模型条件反查指标目录，不猜目录 ID，也不查询当前数据集配置。当前只支持 `bk_monitor/time_series` 且维度、条件无需改名的直接映射；声明、语义绑定与执行查询的维度及条件必须一致，执行查询不能额外携带 `filter_dict`、未绑定或无效来源。缺少、歧义或无法证明等价的绑定返回 `strategy_revision_dataset_binding_invalid`。只改写读取视图中的物理定位与来源类型，保留原声明、表达式、函数、过滤条件及原始快照。
- Scope 强制事件租户，按指标 ID 或完整反查条件复用单次丰富结果；Client 使用 5 秒超时、两条候选上限，两个 JSON 列分别限 1 MiB，并精确复核返回身份。NULL、畸形或超限 JSON 按 `metric_catalog` 依赖失败处理。

2026-10-21 使用策略 147 的 v1/v2 真实事件验证完整九 Processor 链与描述生成：名称、策略、指标及描述 13/13 成功；5 条整个 Enrich 成功，8 条因 OneModel 容器实例未命中保留 Resource/K8s partial。见 [策略 147 只读验证](../research/2026-10-21-strategy147-enrich-validation.md)，不代表部署或线上告警修复。

日志查询没有目录 ID、物理表或衍生定义引用时，展示沿用日志配置与原始维度，不发起目录查询；显式目录引用的失败仍按依赖失败处理。其他指标不放宽完整引用要求。

2026-10-09 的验证覆盖数据库驱动边界的真实 GORM SQL/Scan，并在 test-bkee5 对 22 条活跃告警的最新事件只读重放 Strategy/Resource/Display/Metric：16 条全部成功，4 条缺少主机身份使 Resource 部分成功，2 条数据集策略缺少目录 ID/物理表导致 Display/Metric 失败。命中的 16 次定义读取（含真实 `metric_ref_id=2162`）与独立 SQL 快照一致。详细证据和边界见 [真实环境验证](../research/2026-10-09-metric-catalog-live-validation.md)。2026-10-21 补齐历史 dataset 绑定后，策略 18/v1、73/v1 的最新真实事件在本地完整九 Processor 链中均成功，描述也成功生成，见 [dataset 只读验证](../research/2026-10-21-dataset-enrich-validation.md)。无 ID 且同物理引用有多个候选的材料仍需要上游补齐 `metric_ref_id`，不自动挑选一行；这些验证不代表服务部署或完整生命周期验证。

### 7.2 接口存在，生产能力待补

| 能力 | 当前事实 | 影响 |
| --- | --- | --- |
| DynamicGroup Redis | Reader 与生产装配已实现，本地测试已覆盖；目标环境未验证 | 未命中时为空数组，读取失败使 Resource 部分成功 |
| LogTheme 生产 Reader | 已装配 | MySQL Reader 查询 `log_theme_logtheme`，使用调用方传入的 `bk_tenant_id`，只投影 `bk_tenant_id`、`log_theme_id`、`log_theme_name`；生产装配接入 `enrich.Sources.LogTheme` | |
| CloudResource | 已装配 | MySQL Reader 已接入 `enrich.Sources.CloudResource`，按接口入参租户和 `cloud_id + type + instanceid` 查询并关联云平台名称；当前 CloudResource 表为空 |
| CloudPlugin / SysSetting | 已移出当前 Cloud 主链 | 当前 Cloud/VMWARE 契约不依赖 CloudPlugin、SysSetting，不阻塞资源丰富 |
| K8s Reader | 已装配 | `OneModelK8sReader` 复用 `onemodel.Client` 查询 `kingeye_all_instance`，按租户、模型和 K8s 属性过滤并复核实例身份；不直接读取 `kmc_k8s_*` |
| APM Application Reader | 已装配 | MySQL Reader 按租户、业务范围和应用名精确查询 `kapm_namespace` 未删除记录，并复核返回身份；全局业务在同租户内要求名称唯一 |

### 7.3 OneModel 实例协议

`resources.onemodel.index_prefix` 配置 CMDB 业务拓扑索引前缀，默认 `bk_monitor_base_`；实例 alias `kingeye_all_instance` 与投影边 `kingeye_topo` 使用固定契约名。

当前 `onemodel.Client` 固定查询：

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
- Model、指标目录、CollectConfig、UptimeTask/Node、APMApplication；
- RelatedHost 与 HostTopology；
- Collect/Uptime/BaseTarget 场景结果。

`InstanceQuery.AttributeFilters` 排序后编码，字段顺序不影响缓存键；父 Context 已取消的查询结果不写缓存。`Scope.Scenario` 也不缓存 `context.Canceled` / `context.DeadlineExceeded` 错误，同一 Scope 后续调用能够重新解析。

`collect.Enrich` 与 `uptime.Enrich` 使用 `Scope.Scenario`，使 Resource 与 Display 共享同一次解析和外部查询结果。当前不提供跨 Event TTL 缓存、预热或刷新机制。

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
→ 显式规范资源身份（有无数据标记时为 NO_DATA，否则为 MONITOR_SOURCE）
→ MONITOR_SOURCE（旧实例身份）
→ COLLECT_TASK
→ NO_DATA
→ SYSTEM_METRIC
→ BASIC
```

Kingeye 原始 `event.tags` 与 Linkd 标准事件属于两个协议层。已通过本地 `RawEventMessage → StandardCleaner → Event → Lifecycle Event Enrich → Alert` 测试验证：当上游把 `__NO_DATA_DIMENSION__=true` 写入标准 `dimensions` 时，标记传至 `Event.Dimensions` 并命中 NoData；原始 `event.tags` 单独携带该标记时，StandardCleaner 仅在 `SourceRawData` 留存原文，Enrich 仍按后续 BaseTarget 规则分类。真实上游的 tags → 标准 dimensions 映射契约和端到端样本仍待确认。

触发条件：

| 分支 | 条件 |
| --- | --- |
| UptimeCheck | 非 DATA 且策略 `object_model_code == cw-web_service` |
| MonitorSource | 显式提供 `model_id` 或 `model_inst_id` 且无 NoData 真值标记；规范身份未提供时，`bk_inst_id` 有值，或 `obj_model_id + obj_model_inst_id` 同时有值 |
| CollectTask | 规范身份两个键均未提供，且未命中旧 MonitorSource 身份，`bk_collect_config_id` 有值 |
| NoData | `__NO_DATA_DIMENSION__` 为真值；优先验证 `model_id + model_inst_id`，主机在规范身份均未提供时允许原生主机 ID、明确的 CMDB 主机 Subject 或带云区域的 IP 定位 |
| SystemMetric | 策略 `object_model_code == cw-Host`，按主机 ID 或 IP + 云区域定位 |
| Basic | 其余 BaseTarget；保留模型和来源业务并跳过实例查询 |

Resource 与 Display 共享同一个场景解析结果。MonitorSource、NoData、SystemMetric、Basic 进入 `basetarget.Enrich`，CollectTask 和 UptimeCheck 分别进入 `collect.Enrich` 与 `uptime.Enrich`。新增信息只写入 Processor Value，Event 来源字段保持只读。

BaseTarget 显式规范身份的优先级高于旧实例字段和采集任务：验证模型与策略模型一致，并按事件租户读取确定实例；任一字段缺失、非法、模型冲突或实例读取失败时保留诊断，不回退采集主机。未提供规范身份的旧事件仍沿用原分类顺序。策略实例导航也优先使用相同规范身份。`model_id/model_inst_id` 本身不表示无数据；只有 NoData 真值标记决定该场景分类。

本地回归覆盖了策略 136 样本所具有的 `cw-MySQL|22` 与采集任务 43、44、48 同时出现的形态，验证资源身份、实例展示名称和权限标签不改绑采集主机，以及输入失败和同租户实例校验。该回归不包含线上部署、历史 Alert 回填或策略依赖故障处理。

### 9.2 覆盖矩阵

`__NO_DATA_DIMENSION__` 是 NoData 分类标记，`model_id + model_inst_id` 只承担资源定位。`basic_inst_no_data_report` 的 KAC 样例名称描述告警来源语义，其维度缺少 NoData 标记，按 KAC 与 Linkd 分类规则进入 SystemMetric。NoData 另有携带标记和规范身份字段的独立脱敏 fixture。

| 场景 | 当前完成度 | 已实现 | 主要差距 |
| --- | ---: | --- | --- |
| `BASE_COLLECT/MONITOR_SOURCE` | 90% | 独立 Resolver、两类身份、模型/实例/拓扑、Resource/Display、`cw_labels`、五 Processor payload、KAC `base_collect_monitor_source_01` 脱敏差异 fixture | 服务实例真实样本 |
| `BASE_COLLECT/COLLECT_TASK` | 90% | CollectConfig、实例定位、模型/拓扑生产 Reader、OneModel 执行主机云区域回填、Resource/Display、`cw_labels`、动态分组 Redis Reader、KAC 远程 MySQL 脱敏 fixture | 动态分组目标 Redis 实测、其他采集类型样本 |
| `BASE_COLLECT/NO_DATA` | 90% | `__NO_DATA_DIMENSION__` 分类、`model_id + model_inst_id` 规范身份、策略模型一致性校验、实例/拓扑、通用 KAC title/content/object、五 Processor payload 与失败矩阵 | 真实来源样本复核 |
| `BASE_COLLECT/SYSTEM_METRIC` | 90% | 主机 ID/地址定位、实例唯一性、模型/拓扑、多 membership 稳定首路径、Resource/Display、`cw_labels`、两份 KAC 脱敏差异 fixture | 组合 `event.target` 差异样本 |
| BaseTarget `UPTIME_CHECK` | 85%～90% | Task/Node 双 ID、协议维度、删除任务回退、OneModel 业务名称与可信目标主机云区域名称、业务/任务 `cw_labels`、两份 KAC 脱敏 fixture | 非主机目标的云区域名称、真实外部样本 |
| 顶层 `UPTIME_CHECK` | 65%～70% | 分类和共享 Uptime 模块 | RawEvent 闭环与入口差异验证 |
| `BASE_COLLECT/BASIC` | 90% | 跳过实例查询、模型/来源业务、Resource/Display、业务 `cw_labels`、派生 KAC 通用 Cleaner 五 Processor fixture、模型未命中/错误边界 | 真实可达 KAC 样本 |
| `DATA` | 90%～95% | Standard Kafka → Lifecycle → Strategy/Resource/Display/Metric/Source 主流程、`basic_data` KAC 对照、system 主机与 uptimecheck、普通／hardware_／多模型 canonical OneModel 身份、枚举／衍生／多指标／PromQL／函数指标、特殊查询翻译、内容算法和派生失败矩阵 | 真实普通及多模型 KAC 样例、双租户与真实外部失败样本 |
| `LOG_METRIC` | 75%～85% | 分类、标题、`log` Processor、`log_theme_logtheme` Reader、接口入参租户查询、主题/查询字段、空资源、日志标签、Router 注册、内容裁剪和 KAC 样例字段对照 | 真实数据库记录命中、专用查询参数细节和完整 JSON fixture |
| `LOG_KEYWORD` | 75%～85% | 分类、标题、`log` Processor、`log_theme_logtheme` Reader、接口入参租户查询、查询语句、关联信息、空资源、日志标签、命中/无数据裁剪和 KAC 样例字段对照 | 真实数据库记录命中、专用 URL/查询细节和完整 JSON fixture |
| `VMWARE/Cloud` | 65%～75% | Cloud 类型、期望 Kafka 输入、旧 KAC 表结构与读取字段、策略读取、CloudResource MySQL Reader、复合身份、资源投影、业务标签、响应身份校验、Router 注册和主流程验证；已验证空数据查询 | 真实 CloudResource 记录和完整 fixture | |
| K8s 横切能力 | 75%～85% | 输出模型、Processor、Router/配置注册、`OneModelK8sReader` 生产装配、`kingeye_all_instance` 实际数据核验、Cluster/Namespace/Service/Workload/Pod/Container/Node 身份映射、Workload-Pod 特殊映射、实例/业务/集群名称和 `cw_labels` 投影、响应身份校验、Context 输出和完整 Processor 链测试 | 真实 Kafka Event 回放、Cluster/Namespace 多业务优先级、PV/PVC 接入和完整 JSON fixture |
| APM 横切能力 | 75%～85% | 输出模型、APM 表判断、`apm` Processor、真实 `additional_dimensions.app_name` 输入、`kapm_namespace` ApplicationReader、精确匹配、租户/业务校验、别名、application/service/service-instance 身份、Resource 联动、维度、业务/应用标签、请求内缓存、KAC 映射、Reader 错误/重复匹配矩阵和完整链路测试 | 真实数据库命中、重复应用最终产品决策和完整 JSON fixture |

### 9.3 Collect

生产调用链：

```text
Resource ─┐
          ├─ collect.Enrich
Display ──┘
```

当前链路：

```text
Event.Dimensions.bk_collect_config_id
→ CollectConfigReader
→ bk_object_code + bk_inst_id
→ OneModelReader.FindInstance
→ monitor-resource + display
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
- Event 不可变、未命中和依赖故障测试。

生产限制：

- 云区域名称全部来自 OneModel 资源实例或执行主机实例；不引入 CMDB API。身份不完整、主机未命中、查询错误或区域 ID 冲突时保持空名称；
- 动态分组需要用目标环境真实成员样本验证；
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
Event.Dimensions.task_id
→ UptimeTaskReader
→ 数据库 task_id 查询
→ 记录主键 id 作为 model_inst_id
→ UptimeNodeReader(node_id=plat_id:ip)
→ monitor-resource + display
```

已实现：

- Task/Node 租户隔离；
- Uptime 双 ID 语义；
- HTTP、TCP、UDP、ICMP 专用目标维度；
- 业务名称通过任务 `bk_biz_id`（任务缺失时用 Event 来源业务）读取同租户 OneModel `cw-biz` 实例；
- `bk_target_ip/target_host + bk_target_cloud_id` 同时提供可信 IP 和云区域时，读取同租户 OneModel `cw-Host`，校验 IP/区域后补充云区域名称；云区域 `0` 保留；
- `cw_labels` 使用有效业务 ID，以及已定位任务的 `cw-web_service|<数据库任务主键>`；删除任务时只输出业务范围标签；
- 两份 KAC 拨测脱敏 fixture 覆盖有任务和缺失任务的五 Processor payload；
- 删除任务 `<task_id>（该拨测任务已被删除）` 回退；
- Resource/Display 场景复用和 Event 不可变测试。

生产限制：

- 非主机目标或仅有云区域 ID 的拨测无法从主机实例取得云区域名称，保留 ID 和原维度；
- DATA uptimecheck 由 DATA 场景迁移阶段接入；
- `task_id=0` 输入语义保留真实样本确认；
- 完整 RawEvent 到 Lifecycle 的入口差异验证继续补充。

### 9.5 DATA

当前已覆盖普通时序 `cw-Others`、system 主机与 uptimecheck 的基础资源定位：

- `MetricReader` 投影 `object_model_code`，用于 DATA 对象分类；
- 指标目录同步读取 `value_mapping`，DATA 单指标且无函数时将匹配数值追加枚举名称，多指标、函数和衍生指标保留原始内容；
- 策略无 `object_model_code` 时，按指标库模型回退；
- 无实例身份的 `cw-Others` 告警保留模型代码、模型名称、来源业务和 `cw_labels`；
- 业务名称可从同租户 OneModel `cw-biz` 实例读取；
- Display 使用空 object，保留 Alert 内容和指标标题；
- KAC `basic_data` 转换 fixture 验证完整五 Processor payload；
- system 主机按 `bk_target_host_id` 优先，随后以 `bk_target_ip + bk_target_cloud_id` 查询 OneModel `cw-Host`；
- uptimecheck 结果表复用 `uptime.Enrich`，按 `task_id` 定位任务数据库主键；
- 普通 OneModel 单模型在标准 Event 明确提供 `model_id + model_inst_id` 且模型与指标目录一致时查询 OneModel；`model_inst_id` 按不透明字符串原值使用，实例成功才生成实例标签；
- `data_ordinary_onemodel` 是依据统一实例身份规格构造的派生 fixture，真实普通实例 KAC 样例仍待取证；
- 上游缺少显式 canonical 模型／实例身份时保留模型和业务并返回 Resource partial；身份冲突或查询未命中时 Resource failed。

当前仍缺：

- system 主机和 uptimecheck 的身份规则来自 Kingeye Converter 源码，目前由合成五 Processor 测试保护；现有 KAC `source_data` 只有 `basic_data` 一份 DATA 原始样例；
- hardware_ 与多模型结果表已经收敛为 canonical `model_id + model_inst_id` 定位；旧数值模型主键字段只作迁移诊断，真实样例仍待补充；
- Cloud、APM、K8s 的专用资源定位进入各自场景；
- 枚举指标 `value_mapping` 已从指标目录读取，并在 DATA 单指标、无函数场景中为展示 content 的数值追加 `(mapped_value)`；多指标、函数和衍生指标保持原始内容；
- 当前值、阈值、算法等级和单位生成展示内容已支持静态阈值场景：算法级别的 `algorithmUnit` 优先，`NONE` 清空单位，指标目录 `bytes/percent/percentunit` 使用 KAC 映射；枚举映射继续按精确数值处理；
- 环比、同比和无数据内容已按 Kingeye 的算法分支建立确定性转换：环比/同比只处理可定位的数值片段，无数据文案保留原文；
- custom event、alert、FTA 的 Metric 查询投影已覆盖字段、结果表和事件过滤器翻译；
- 所有来源的 PromQL 取数参数均由消费侧处理，Linkd 不再生成 `metric_query_params` 或省略表达式；事件级结果表与字段按 Kingeye 冒号段规则从表达式内唯一的序列名派生，混合普通名称序列或无名选择器时不派生单指标身份；
- PromQL 的 Resource、Display、Metric 不再要求单指标目录主引用：声明缺 `data_source_label` 时由 `query_config_type=prometheus` 识别来源，目录仅在派生身份能唯一命中时补充单位、中文名、维度和对象模型，派生不出或目录缺数据都不再使 Processor 失败；无显式 `metric_ref_id` 的可选反查多义时保留声明名称和可信资源身份，标记相关 Processor 为 `partial`，保留 `metric_catalog` 诊断与原始错误日志。显式引用失败、身份不匹配、数据库故障和取消不会按目录未命中降级；
- PromQL 无对象模型时 Resource 只接受事件已确认的身份（规范 `model_id` 或 CMDB 主机 Subject），主机仍按目标主机 ID、IP+云区域定位 OneModel；身份完全缺失时保留业务并返回 Resource partial，不回退采集任务或猜测模型；
- `promql_host_system_metric` fixture 记录 `linkd-93f26184-8acd-5fdc-bf3a-c5cd11fcd4ec` 的真实发布快照：`171/PromQL` 模板下 `avg_over_time(bkmonitor:system:cpu_summary:usage{bk_biz_id="2"}[60s])` 既无 `metric_ref_id` 也无表名和字段，整条五 Processor 链路成功生成主机资源、`CPU使用率` 标题与 `percent` 单位；
- PromQL 表达式解析覆盖函数、标签匹配器、时间窗口、`by` 列表、`offset`、标签值内冒号、多序列和结构残缺输入。
- 函数指标保留策略级和 query 级 `functions`，Metric name 为空，并关闭函数场景的枚举映射；
- 派生失败矩阵覆盖策略未命中／错误、指标目录未命中／错误、模型未命中、实例依赖错误、非法 canonical 身份、混合 Processor 最终 partial 和 Context 取消；
- 普通时序、多指标、衍生指标、custom event、alert、FTA、日志查询等 fixture。


### 9.6 LOG_METRIC / LOG_KEYWORD

已完成第一轮日志纵切：

- 新增 `log` Processor 和 Router 注册；
- 从策略 `source_config` 读取 `log_theme_id/log_theme_name/query_string`；有 `LogThemeReader` 时按租户读取主题名称并复核主题 ID，Reader 未配置时保留策略名称回退；
- 日志指标使用查询语句作为 metric 展示名，日志关键字为空查询时回退 `--`；
- 日志场景使用空 Resource 语义，Display 使用空 object；
- 日志指标名称沿用旧 KAC `LogMetricCLear.clean_name`：优先使用完整告警别名 `alarm_alias`，否则生成“日志主题名 + 发生了 + 监控项展示名 + 告警”。主题名使用租户限定查询结果，未命中时回退发布材料中的 `log_theme_name`，仍为空才使用原始 `SubjectName`；监控项展示名优先 `alias_name`，否则沿用通用 `cleanItem`，不以检索语句或 `--` 覆盖。无数据事件使用相同名称规则。Display 与 Log 复用 Scope 缓存的主题查询，原始 Event 保持不变；
- 关键字从 `Event.ExtraData.log_related_info` 读取关联信息；
- 日志 Display 内容先拼接 `Alert.SubjectName`，再执行日志内容裁剪：日志指标移除“关联信息”尾部；日志关键字命中内容生成“匹配到【query】关键字次数 ...”，无数据内容生成“【query】关键字 ...”；`Alert.SubjectName` 为空时保持原内容形态；
- 输出日志主题、查询语句、关联信息和 `cw_labels`；
- 已读取 KAC `log_metric_data.json` 与 `log_keyword.json` 的真实输入/清洗输出，补充同字段语义的完整 Processor 链测试；
- 派生输入测试覆盖日志 Processor 的正常、关键字空查询和主题缺失边界。

仍需：

- `LogThemeReader` 已接入 MySQL 主流程，按接口入参租户与 `log_theme_id` 查询 `log_theme_logtheme`，投影主题 ID和名称；本次使用 system 租户进行验证；Strategy 仍沿用当前 `CWStrategyReader` 逻辑；真实记录命中待数据库连接恢复后验证；
- 专用日志查询参数、URL 和真实 KAC fixture。

### 9.7 Cloud、K8s、APM

三类专用 Processor 已完成第一轮代码接入、Router 注册、类型化输出和派生测试。生产装配现状以第 7 节为准：LogTheme、CloudResource、APM Application 和 K8s 已进入 MySQL/OneModel Sources。

APM 第一轮纵切已完成：

- 新增 `APMApplicationReader`，按租户、业务范围和应用名称读取候选应用；生产实现从 Kingeye `kapm_namespace` 表按 `namespace` 或 `normalized_name` 精确查询未删除应用，返回名称保留请求拼写，并复用 Enrich MySQL 连接池；
- APM 应用定位通过 `Scope.IsGlobalBusiness` 读取 `metadata_space.is_global`：普通业务精确限定 `bk_biz_id`；全局业务只放开同租户内的业务限制，最多读取两条以检查名称唯一性。业务不存在、读取失败或同租户应用重名时保留诊断，不任选一条，不跨租户；
- 命中应用后，APM、Resource 的 `bk_biz_id`、业务名称和 `cw_labels` 使用应用记录的实际业务。原始 Event 的业务标签与策略历史快照保持不变；全局业务未命中应用时，不把管理业务当成资源业务兜底；
- 精确匹配应用名称后投影应用 ID、别名和业务；
- 生成 application/service/service-instance 模型身份与 `cw_labels`；

- 新增 `apm` Processor 与 Router/配置注册；
- 资源、APM 和展示处理统一识别 `query_configs[].metricset_code=apm` 或 `bkapm` 结果表。APM 目录衍生指标可以没有物理结果表和目录 `model_id`，直接按 APM 应用维度定位资源，不进入要求目录模型的普通 DATA 分支；
- 应用名称读取 `dimensions` 与 `extra_data.additional_dimensions` 合并视图中的 `app_name`，两处冲突时拒绝处理；缺失时再按 `data_source=应用-<name>` 或 `_bkapm_metric_<name>` 推导；
- application 身份为 `<app_id>`，service 身份为 `<app_id>|<service>`，service-instance 身份为 `<app_id>|<service>|<instance>`；
- 投影 application/service/instance/span/net_peer 字段；
- 应用未命中、非精确匹配、租户或业务不匹配返回 partial；旧 `dimensions.apm_app_id` 仅保留为兼容回退；
- `service_name` 统一从 `dimensions` 与 `extra_data.additional_dimensions` 的合并视图读取，供 Resource、Display、APM 共同使用；重名或非法附加维度产生 `invalid_field`，不静默选择一份。原始 Event 的两组维度保持不变；
- Resource Processor 与 APM Processor 共用应用解析和请求内缓存，按 `app_id + service_name + bk_instance_id` 生成 application/service/service-instance 资源身份，并补齐业务/应用标签；即使 EventSource 尚未配置 `apm` Processor，现有 `monitor-resource` Processor 也能生成 APM 对象身份；
- APM Resource 按已确认的模型代码读取同租户模型注册表中的名称，补齐 `model_name`；模型未命中、身份不匹配、名称缺失或读取失败时保留 APM 资源身份并标记部分成功，取消继续向调用方传播；
- 已覆盖 ApplicationReader 未命中、非精确匹配、租户不匹配、Reader 错误、缺少应用 ID、重复精确匹配和完整 Processor 链测试；
- 已用 alarmd 真实 APM 字段布局补充回归测试，覆盖 `additional_dimensions.app_name`、`service_name`、`bk_instance_id` 和 `net_peer_name`；
- 已补完整 Processor 链测试、application/service/service-instance 三种身份 payload、应用非精确匹配、租户不匹配、Reader 错误和重复精确匹配矩阵；普通业务的重复匹配沿用 Reader 返回顺序的第一条精确结果，全局业务的重名匹配拒绝生成身份。

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

2026-10-28 修复约定：主实例查验与 Namespace / Cluster 上下文查询独立完成。
所有查询结果均复核租户、模型和非空实例身份；无效结果不能参与名称、业务或标签计算。
主实例失败时不发布新实例身份，仍按原规则保留已有完整身份或清理残缺身份；
上下文查验成功只补充业务与集群名称，不能把主实例失败改成成功。
任一必需查询失败保留 `partial / dependency_invalid / onemodel`，`fields` 列出失败模型，
不把未命中、查询异常与身份不符臆断成容器已删除；底层错误仍由依赖边界日志记录。
取消必须向调用方传播。缺少主实例定位维度仍走原 missing_field 分支，不发残缺查询。

集群名称独立于业务选择：仅 Cluster 可用 `name/display_name` 回退，Namespace 与叶实例
只能贡献显式 `cluster_name`，避免将命名空间或容器名当成集群名称；名称缺失不清空输入名称。
业务优先级保持 Namespace → Cluster → 已校验主实例 → 来源业务。

K8s 已完成 OneModel Reader 主流程接入，复用统一实例索引 `kingeye_all_instance`；不直接读取 `kmc_k8s_*` 专用索引。

已完成：

- `k8s` Processor、Router/配置注册和 `OneModelK8sReader` 生产装配；
- K8s Reader 返回真实实例后，投影 `model_id`、`model_inst_id`、`cluster_name`、`bk_biz_id`、`bk_biz_name` 和确定的 `cw_labels`；
- 业务选择对齐旧 KAC：Namespace `bk_biz_id` 优先，Cluster `bk_biz_id` 回退，随后使用 `bk_biz_ids` 第一个值，最后回退 Kafka `labels.bk_biz_id`；
- `OneModelK8sReader` 通过 `onemodel.Client` 按租户、对象模型和类型化属性过滤；
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
2. **动态分组**：Redis Reader、租户隔离、key 查询、类型与失败语义已有本地实现和测试；目标环境的写入键、隔离边界及真实成员样本仍需验证。
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
指标目录
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
- DATA `basic_data` 原始 KAC 样例转换 fixture 验证 `metric.model_id` 模型回退、`cw-Others` 空实例和业务标签；`data_ordinary_onemodel` 为基于统一实例规格的派生五 Processor fixture；
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
| MonitorMetric / MonitorMetricLibrary 表 | 使用新指标目录的 metric 表 |
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

SystemMetric 主机支持通过主机 ID 或 `bk_target_ip + bk_target_cloud_id` 定位。地址查询成功并通过租户、模型及非空实例身份复核后，不再因为输入没有主机 ID 返回 `missing_field`；模型元数据和拓扑也成功时 Resource 为 `succeeded`。查询失败、身份无效以及其他依赖异常仍保留相应诊断。

已验证的降级行为：模型元数据异常保留合法实例并返回 Resource partial；核心实例身份不匹配或未命中时，已验证模型上下文可使 Resource partial；没有可用模型上下文时仍 failed，禁止生成实例绑定和实例标签；关联主机或拓扑缺失保留实例并返回 Resource partial；Context 取消中止 Enrich 链，同一 Scope 中的取消结果不会污染后续重试。单个 Resource failed、其余 Processor succeeded 时最终 Alert.EnrichStatus 依现有聚合算法为 partial。标准事件内的 NoData 标记已通过本地全链路验证；真实 Kingeye tags 到标准事件的上游映射仍待确认。

Basic fixture 已完成：

1. 现有 KAC source_data 没有可达 Basic 样例；所有 BaseTarget 样例均进入 MonitorSource、CollectTask 或 SystemMetric；
2. 已基于 KAC 通用 Cleaner 契约建立 `basic_model_context` 派生 fixture，覆盖模型上下文、来源业务标签、SubjectName object 回退和完整五 Processor payload；
3. 已增加 Basic 模型未命中／Reader 错误、MonitorSource 实例未命中和 SystemMetric 拓扑非法响应测试。

Uptime 业务名称、云区域名称和 `cw_labels` 已完成：

1. 任务业务 ID 优先，删除任务回退 Event 来源业务；业务名称读取同租户 OneModel `cw-biz` 实例；
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
3. 显式 `model_id` 必须与策略 `object_model_code` 一致；规范身份不完整或非法时不回退，主机规范身份与原生主机 ID／CMDB Subject 冲突时拒绝实例定位；K8s 未提供规范身份时复用 K8s Reader 按集群、node/pod 等自然维度定位，不额外要求整数实例 ID；
4. NoData 沿用 KAC 通用展示规则：content 保留 Alert 原文，object 使用实例展示名并回退 SubjectName，title 使用 `AlarmAlias` 或“对象发生了监控项告警”；
5. 控制维度 `__NO_DATA_DIMENSION__` 和无数据事件中用于资源定位的规范 `model_id/model_inst_id` 不参与 Metric 查询筛选与 `where_condition`；普通事件的查询维度保持既有语义；标准 Cleaner 可保留标准输入 `dimensions` 中的标记，真实上游 tag 映射仍待验证；
6. 已增加规范身份的完整五 Processor fixture，以及普通身份、缺字段、非法身份、模型冲突、实例未命中和依赖失败测试。

### DATA 已完成纵切：普通时序、system 主机、uptimecheck、hardware_、多模型身份、枚举与查询语义

已完成：

1. `basic_data` KAC 样例转换 fixture，保留 `cw-Others`、空实例和业务标签语义；
2. 指标目录数据源读取 `metric.model_id`，并用于 DATA 资源模型回退；
3. DATA Resource 保留模型、业务和标签，不伪造实例身份；
4. system 主机使用目标主机 ID 或 IP+云区域定位 OneModel；uptimecheck 复用 `task_id` 任务定位；
5. 普通 OneModel 仅按显式 `model_id + model_inst_id` 原值查询，已覆盖成功、模型冲突、非法数字、未命中和响应身份不匹配；
6. `data_ordinary_onemodel` 派生 fixture 验证完整五 Processor payload，真实普通 DATA 样例仍待取证；
7. `hardware_` 与多模型结果表统一要求 canonical `model_id + model_inst_id`，指标目录按模型精确投影，OneModel 按实例原值查询；
8. 旧 `object_model_id + obj_model_inst_id`、`cw_object_model_id + cw_object_model_inst_id` 只保留为待迁移输入诊断字段，不恢复 Meta API 或数值主键推断；
9. 指标目录读取 `value_mapping`，DATA 单指标无函数时对告警内容中的匹配数值追加枚举名称；多指标、函数、衍生指标和无法精确匹配的内容保持原值。
10. 衍生指标优先按租户和目录 ID 读取定义；没有 ID 的旧声明按 `field_tag=derived_metric` 与定义名反查。Metric 保留衍生类型及实际表达式，catalog 单别名的无函数恒等引用省略外层表达式，不要求衍生定义具有物理表。
11. 单个监控项包含多条 query 时，Linkd 保留发布材料，不再为任何来源生成取数参数；无函数的普通查询沿用旧 KAC 的首查询字段作为兼容 Metric name，Metric unique ID 仍使用首查询的物理位置。该兼容字段不表示组合结果具有首成员的指标身份或单位；函数场景继续保留空 Metric name，内置组合取数由 KAC 读取准确执行历史，外部来源组合取数由所属插件承接，历史已存参数不再读取或输出。见 [多查询兼容验证](../research/2026-10-21-multi-query-legacy-compatibility.md)。
12. DATA 静态阈值展示优先使用告警等级对应算法的 `algorithmUnit`；`NONE` 表示清除单位；算法未覆盖时使用指标目录单位映射。阈值和当前值支持数值单位追加。
13. 环比／同比内容按 Kingeye 分支识别可定位数值并进行枚举映射，无数据周期文案保留来源文本；custom event、alert、FTA 查询类型使用对应字段和结果表翻译。
14. 所有来源均不再生成 PromQL 取数参数；事件级结果表与字段按 `segment:...:metric` 约定从表达式内唯一序列名派生。多序列、只出现在标签匹配器中的指标名和结构残缺表达式不产生单指标身份。
15. PromQL 目录依赖是可选补充：派生身份能唯一命中目录时补齐单位、中文名、维度和对象模型，未命中或无法派生时 Resource、Display、Metric 继续输出已确认内容；主机资源模型按 CMDB 主机 Subject 独立解析，不依赖目录。
16. 函数指标保留策略级和 query 级函数配置；Metric name 为空，函数和多指标场景关闭枚举映射，函数计算结果由上游查询链提供。
17. DATA 派生失败矩阵覆盖策略、指标目录、模型、实例、身份校验和 Context 取消；单个 Resource failed 与其他 Processor 成功时最终状态为 partial，Event 输入保持不变。

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

第一轮派生迁移及 MySQL Application Reader 已完成，并按 alarmd 真实字段布局补充回归测试。后续验证真实数据库命中、重复应用产品决策和完整 JSON fixture；application/service/service-instance 的派生身份、Resource 联动和失败矩阵已由测试保护。

### 迁移尾声待定：跨场景真实契约与历史边界验证

专用 Processor 的派生迁移已经完成第一轮。以下事项需要真实样本或外部契约后再确认验收口径和接入顺序：

1. 真实上游 NoData `event.tags → dimensions` 映射契约与端到端样本；
2. 双租户、关联主机恢复及其余外部失败样本；
3. 其他采集类型 KAC fixture；
4. 动态分组 Redis 的目标环境键和双租户实测。

## 15. 每个场景的完成标准

每新增或收口一个场景，至少具备：

1. 明确的 Event 输入字段和类型；
2. 类型化分类结果和 Processor 适用条件；
3. 真实 DataSource Adapter 或明确复用的现有 Adapter；
4. 每次外部查询显式携带 `bk_tenant_id`；
5. 正常、边界、未命中、非法响应、依赖失败和取消测试；
6. 与受影响分组一致的 partial/failed diagnostics；
7. 类型化完整 Value；
8. RawEvent 或 Alert fixture 到最终 payload 的业务测试；
9. Event 来源字段保持不变；
10. Observation 标签保持低基数；
11. 配置、模块和示例文档同步更新。

## 16. 后续方向：共享读模型与 Enrich v2

当前生产实现直接读取 Kingeye MySQL 和 OneModel ES。跨 Event 缓存、Namespace、规则 DSL、可配置提取和版本化共享读模型均未实现。

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
- 自定义规则与开发验证：[自定义丰富开发规格](custom-enrichment.md)

本文维护内置 Enrich 场景、迁移状态和 Observation；自定义规则与补丁协议以[自定义丰富开发规格](custom-enrichment.md)为准。研究文档保留带版本的证据，不覆盖实现和已确认规格。


### 2026-10-15：无数据主机定位与旧 KAC 降级补偿

用户确认迁移旧 KAC 的资源补偿，并允许其他类型先使用构造数据验证。以下行为以当前实现和回归测试为边界，不表示已部署或完成全部类型的真实验证：

1. NoData 分类只表达无数据语义。策略模型为 `cw-Host` 且规范模型身份两个键均未提供时，按 `bk_inst_id`、`bk_target_host_id`、`bk_host_id`，再按 `subject_system=cmdb + subject_type=host + subject_id` 定位；最后允许可信 IP 与非负云区域的组合，支持 `ip/bk_cloud_id` 别名。显式非法身份、规范身份不完整和 Subject 冲突不能触发替代身份回退。
2. MonitorSource、NoData、SystemMetric、Basic 的模型上下文独立于实例查询；合法模型信息可作为 partial 补丁输出。主机文档未命中或暂不可读时，以已确认的输入主机主键独立查询拓扑；错误响应、跨身份结果不能触发这条补偿，也不生成未验证的实例绑定或实例权限标签。
3. DATA system 主机复用 BaseTarget 的解析与补偿；采集任务在实例失败时保留已确认模型、配置业务和主机拓扑。业务名称缺失时按最终业务 ID 查询同租户 OneModel `cw-biz`，不使用策略的全局业务覆盖资源业务；模型与实例均不可读时，独立验证的业务名称仍可作为 partial 输出。
4. BaseTarget 与 Collect 共用关联主机云区域补偿：资源自身区域优先，只为同一云区域补名称；关联主机必须属于当前租户。拨测已有任务删除回退、业务名称与可信地址云区域补偿继续沿用。
5. 实例展示名缺失时保留来源 Subject、主机 IP 或 K8s node 回退。DATA 已确认实例的展示名不再被空结果清除。所有补偿保留原始 Event 维度，不从 SourceRawData 读兜底，也不向指标维度注入身份。
6. 已增加原生/规范身份、冲突/非法/部分身份、跨租户、实例缺失、模型保留、拓扑与云区域补偿、DATA/Collect 展示回退、K8s 自然维度与单次查询复用、无数据查询条件隔离的构造数据回归。当前真实回放以指定 test-bkee5-vpn Event 为准，其他类型与 Redis 展示/动态分组仍需要真实验证；不重写历史 Alert。
## 通用资源身份解析（2026-10-24）

`resource` 只解析对象模型和实例，不读取监控策略、采集任务、指标目录，不生成业务拓扑、权限标签或动态分组。
原监控资源处理迁至 `monitor-resource`，保留已有监控场景与输出；新监控来源显式发布该处理器。
已发布 `built_in_bk` 的旧链仍含 `strategy/resource` 时，装配边界保留旧监控语义，避免改变冻结 Release。

通用定位依次选择同一位置的 canonical `model_id/model_inst_id`、`subject`、旧 `bk_obj_id/bk_inst_id`。
字段位置按有效视图的 `labels`、`extra_data`、`dimensions` 排序，不跨位置拼接。已经声明但不完整、非法、查询失败的身份不得降级换对象。
完整声明解析后冲突时记录诊断，不提交新资源身份。只有 `subject.name` 不构成身份声明。
OneModel subject 的 type 是模型编码；CMDB subject 的 type 按同租户 `bk_cmdb_obj_id` 反查唯一模型。
实例统一走 OneModel，CMDB 主机/业务/集群/模块按各自 CMDB 主键属性查询，其余 CMDB 对象按 `bk_inst_id` 查询。
模型和实例返回后复核租户、模型、实例及 entity_uid，不猜模型名、不将未查到的来源 ID 当成定位结果。
模型已确认而实例未命中时仅提交已确认模型字段并记录部分成功；来源 subject 和原始数据保持不变。

KAC REST 默认发布 `source/resource`，再执行自定义 CMDB/字段规则，最后执行已有 `cmdb-access`。
REST 接入只归一化顶层旧资源字段到 extra_data，不查询依赖、不扩大为旧完整报文协议。
本次不回填历史 Alert 或 KAC 记录；部署验收必须与本地测试、只读数据重放区分。

部署顺序：先升级支持 monitor-resource 且保留冻结监控 Release 语义的 Linkd，再升级 KAC 并发布来源配置；旧 Linkd 不认识新监控处理器名称。
