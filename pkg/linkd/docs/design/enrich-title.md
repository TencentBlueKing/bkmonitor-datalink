# Enrich 的 title 生成规则

核对日期：2026-11-02。代码基线：主仓 `feat/linkd-dev`，`1452e0b4d7e53b9fed29b90e8ad64097a773bb1d`。

本文说明告警名称 `title` 的当前取值，以及本次提出的跨策略类型默认规则。
**新规则尚未实现，本次只更新文档，不表示已部署或已更新历史告警。**

## 1. 字段与输出

- `title`：Linkd 告警名称；KAC 的 `name` 取 Alert 有效视图中的 `title`。
- `display.value.title`：内置 `display` 计算的标题，生成 `$.title` 补丁。
- `display.value.object`：同一步骤计算的对象名称，生成 `$.subject_name` 补丁。
- `spec.alarm_alias`：完整告警别名，当前优先于默认拼接。
- `spec.alias_name`：监控项别名，与完整告警别名不同。
- `item`：KAC 监控项展示文本，按自己的覆盖规则输出，不是所有标题分支的直接输入。

对象名称的输入字段、查询条件和输出见 [object 补全规则](enrich-object.md)；
监控项名称与指标库查询见 [item 补全规则](enrich-item.md)。

依据：[Display](../../internal/enrich/processors/display.go)、
[补丁映射](../../internal/enrich/kingeye/projection.go)、
[KAC 转换](../../internal/lifecycle/kachook/message.go)。

## 2. 当前代码怎样生成 title

`display` 读取原始 Event、策略执行快照和前序内置资源步骤的查询结果，计算对象名称与监控项名称。
`spec.alarm_alias` 非空时直接用它；否则按以下场景拼接。

### 2.1 普通 DATA

- **输入字段**：`spec.config_type = data`，以及 `spec.name`、首条 `strategy_item.query_configs`。
- **处理规则**：非 PromQL 通常使用 `spec.name`；PromQL 命中非空指标库名称时使用该名称，否则取 `spec.name`。
- **输出字段**：`display.value.title = 监控项名称 + 发生了告警`。
  即使 `display.value.object` 已有主机、K8s、APM 或拨测对象名称，也不会拼入标题。

### 2.2 日志指标

- **输入字段**：`spec.alias_name`、`spec.name`、`spec.source_config.log_theme_id / log_theme_name` 和原始 `subject_name`。
- **处理规则**：主题名称按同租户查询，未命中时用配置的 `log_theme_name`，仍为空再用原始 `subject_name`。
  监控项名称优先用 `spec.alias_name`，否则调用通用 `cleanItem`。
- **输出字段**：`display.value.title = 日志主题名 + 发生了 + 监控项名称 + 告警`。
  标题中的日志主题名与 `display.value.object` 分别计算。

### 2.3 日志关键字

- **输入字段**：原始 `subject_name`、`spec.source_config.log_theme_name / query_string`。
- **处理规则**：主题文本优先用原始 `subject_name`，为空再用配置的 `log_theme_name`。
- **输出字段**：`display.value.title = 主题文本 + 发生了【query_string】关键字告警`。
  这里使用检索关键字，不使用通用监控项名称。

### 2.4 其他类型

- **输入字段**：`display` 解析出的对象名称和监控项名称，包括目标类、目标类拨测和云资源。
- **处理规则**：沿用各场景的对象名称查询及回退；监控项名称调用 `cleanItem`。
- **输出字段**：`display.value.title = 对象名称 + 发生了 + 监控项名称 + 告警`。
  当前对象名称为空时仍会拼接，不会另行补出对象名称。

### 2.5 与旧 KAC 的关系

已对照旧 KAC 提交 `052f526e9f` 的 `alarm_callback/cleaner` 和 `common/alarm_callback`：
上述默认句式及 `alarm_alias` 优先规则在旧链路中也存在，DATA 不含对象名称并非 Linkd 新增的行为。
具体名称并不完全一致：旧 KAC 的通用 `clean_item` 优先使用 `alias_name`，
Linkd 通用 `cleanItem` 没有这个优先分支，仅日志指标标题单独处理了 `alias_name`。
这项名称取值差异与第 3 节的拼接规则分开记录。

## 3. 跨策略类型的新规则（待实现）

本次要求：**能丰富出告警对象名称时，统一生成 `对象名称 + 发生了 + 监控项名称 + 告警`。**
不再因 DATA、目标类、日志、APM、K8s、拨测或云资源类型不同而跳过对象名称。

按默认命名规则处理，优先级为：

1. `spec.alarm_alias` 非空：保留显式配置的完整告警别名。
2. `display` 得到非空对象名称：使用该名称和监控项名称统一拼接。
3. 对象名称为空：保留第 2 节各类型的现有规则。
4. 后置自定义规则仍可赋值 `$.title`，按配置顺序覆盖。

### 3.1 对象名称

- 与同次 `display.value.object` 使用同一个值，不再另外按策略类型选择标题前缀。
- 沿用已有实例名称、主机 IP、服务名、任务名及来源 `subject_name` 的选择和回退。
- 不要求 `model_inst_id` 非空，也不要求所有丰富步骤都成功；`display` 的合法 `partial` 补丁仍可生效。
- `subject_id`、`model_inst_id` 或单独查到的 `log_theme_name` 不自动当成新规则的对象名称。
  只有它们按对象补全规则得到可用名称时，才参与拼接。

### 3.2 监控项名称

- 使用 `displayItem / cleanItem` 得到的名称；日志指标保留当前 `spec.alias_name` 优先规则。
- 日志关键字有对象名称时，也使用监控项名称，不把 `query_string` 当作监控项名称。
  只有没有对象名称时，才保留旧的“【query_string】关键字告警”句式。
- 本次不同时修改通用 `cleanItem` 的别名优先级，也不将最终 KAC `extra_data.item` 覆盖值自动带入标题。

例如，DATA 已得到 `display.value.object = host-1`、监控项名称为“CPU使用率”时，
标题由“CPU使用率发生了告警”改为“host-1发生了CPU使用率告警”。
同样的对象和监控项名称，应跨策略类型得到相同的默认标题。

## 4. 实现前需要处理的地方

- `cleanDisplayTitle` 应在各类型兜底分支之前判断对象名称，不能只修改 DATA 分支。
- 日志关键字当前提前把 `itemName` 改为 `query_string` 或 `--`；实现时须保留通用监控项名称供新规则使用。
- DATA 的 APM 分支当前直接用 `dimensions.service_name` 覆盖先前解析出的对象名称。
  `service_name` 为空时，应保留已有的非空对象名称，避免漏用新规则。
- `display` 当前读取原始 Event 和内置资源上下文，不会自动使用所有前置自定义 `subject_name` 补丁。
  后置 `cmdb / fields` 修改 `subject_name`、`extra_data.object` 或 `extra_data.item`，也不会自动重算 `title`。
  若要求标题跟随这些最终覆盖值，需要另行调整执行顺序或增加标题计算步骤；本次不将它写成已支持。

验证至少覆盖：不同策略类型的同值输入得到同一默认标题、对象名称为空、完整告警别名、
日志关键字有对象时使用监控项名称、APM 缺少 `service_name`、可选依赖失败保留对象名称、
后置自定义规则覆盖 `title`，以及监控项名称为空时的现有行为。

## 5. 保存结果与历史告警

- 只有 `succeeded / partial` 的合法补丁生效；`failed / skipped` 不覆盖来源标题。
  未配置 `display` 时也不会自动执行上述拼接。
- Event 按等级保存丰富结果；重投已冻结的 Event 不重新生成标题。
- 创建 Alert 时复制 opening Event 对应等级的丰富结果；持续触发不会自动用最新 Event 的丰富结果替换旧快照。
- 修改命名代码不会自动更新已有 Event、Alert 和 KAC 文档，需要单独安排历史更新及验收。

依据：[处理链](../../internal/enrich/enricher.go)、[有效视图](../../internal/enrich/view/view.go)、
[Event 丰富](../../internal/lifecycle/enrich.go)、[Alert 创建](../../internal/lifecycle/plan.go)。
