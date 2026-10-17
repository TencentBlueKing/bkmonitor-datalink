# KAC 告警丰富事实查询

2026-10-13 确认的 KAC 页面契约。丰富与来源恢复详情均只读取已有事实；关闭审计上下文保存在 KAC 稳定命令中，不扩展 Linkd 关闭协议或 AlertLog 展示副本。

`POST /api/v1/alerts/{id}/enrich-records` 使用现有管理 JWT；请求包含 `bk_tenant_id`、`event_ids`（1–20 项）和可选 `detail`。同请求 EventID 去重。单请求正文最多 8 KiB，最多两个并发查询、五秒 deadline，响应最多 1 MiB；没有初始化 schema、写业务数据或重跑规则。

先读取指定租户的 Alert，再核对每个 Event 的租户、来源及 `related_alert_ids`。按 Alert 的准确等级取冻结丰富结果，不任选其他等级；缺失和不可读项列在 `unavailable`，不能用当前配置补造结果。来源发布 ID、实际丰富版本、租户以及丰富配置摘要均需匹配。实际版本取 `enrich_source_version`，缺少该字段的既有记录沿用 `event_source_version`；读取展示元信息不会触发丰富或修改接入事实。

响应 `items` 每项包含 `alert_id/event_id/severity/enriched_at/status/enrichment_count/changed_field_count/enrichments`；`detail=true` 另带 `field_changes`。措施包含 `id/type/name/status/code`，来自 CMDB/fields 的实际规则 trace；来源规则 `name` 和 `field_labels` 属于不可变发布配置展示元信息，不复制到 AlertLog。内部字段目录冻结规则不作为业务丰富措施。

字段变化来自已提交补丁，按顺序从原 Event 重放赋值，包含前序处理器有效贡献；绝不重新查 CMDB 或执行表达式。失败和回滚的尝试不冒充生效。相同路径只展示第一次业务修改前与最后提交后值，未发生净变化的不计入数量；关联全部参与措施。`field/display/before_present/before/after_present/after/measures` 区分未设置、null、空字符串、0、false。字段值只在详情返回，超响应预算明确失败，不截断后声称完整。新 API 不返回原始 Event 或发布配置正文。

KAC 对首次触发/恢复的丰富生成独立派生节点；持续更新丰富仅附加到对应明细，保留分组。新弹窗仅展示措施结果和字段对比，屏蔽/合并/抑制和更新明细仍不增加详情入口。未准入告警不因此获得处置或关闭动作，不回填历史日志。

## 来源恢复信息

`POST /api/v1/alerts/{id}/recovery-record` 使用相同管理 JWT。请求包含 `bk_tenant_id/event_id/severity`，单请求 2 KiB，与丰富查询共用两个并发槽位、五秒超时，响应上限 1 MiB。租户、来源、Alert/Event 关联及准确等级必须匹配，仅接受当次 resolved/closed 来源评估（唯一全等级 closed 评估使用其保留标记）。复用准确发布版本和丰富摘要校验，读取失败明确返回错误。

恢复详情的 `metric_query_params` 与正式 KAC 输出共用 JSON object 字符串编码：Event 中的结构化对象编码一次，已有 JSON 字符串先解析再规范编码；显式 null 输出 `{}`，未提供则不补字段，非法 JSON、数组和非对象标量返回错误。该转换仅处理已保存事实，不重跑丰富或修改 Event。

响应 `alert_id/event_id/severity/fields` 的 fields 由原 Event 应用已保存的生效补丁后转换；不重跑规则、不读取当前 Alert 正文、不返回原始 payload 或完整配置。复用 KAC 业务字段转换，只展开已知字段和发布时声明的自定义目录，保留 false/0。KAC 按已持久的流转引用请求，并转换成旧面板 data/category。未准入动作资格、人工/策略/合并系统关闭和历史日志均不因该读取接口改变。
