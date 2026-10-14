# 告警源 Event 丰富窗口查询

本接口供 KAC 告警源“开始调试 / 结果预览”观察实际处理结果。所有来源在 Event 层汇合；读取不执行丰富、不写消息、不触发 Hook，不要求事件已经产生 Alert。

## 列表

`POST /api/v1/events/enrich-records/query`，沿用管理 JWT。请求包含 `bk_tenant_id`、`event_source_id`、RFC3339 `received_from` / `received_to`、可选 `after`、`limit` 和 `filters`。时间范围最多一小时；分页边界固定，刷新重新开始查询。`limit` 表示候选 Event 数量（默认 50，最多 200），一个事件的全部等级在同页返回。

返回 `items`、`next`、`scanned`。每项包含事件身份、来源版本、接收时间、等级、动作、实际丰富状态、业务方案名称、变化字段数、展示字段及诊断。筛选作用于有效展示字段和方案名，支持精确 / 包含匹配；未命中页仍可能返回 `next`，调用方继续请求，不把该页当作整个窗口无匹配。无精确总数承诺。

## 详情

`POST /api/v1/events/{id}/enrich-record`，请求包含同一租户、来源、时间范围及 `severity`。读取事件原始事实和该等级已提交的丰富结果，按事件引用的 Release 恢复规则名称及字段展示名；返回原始展示字段和 `field_changes`。

事件状态 `pending` / `succeeded` / `partial` / `failed` / `skipped` 原样保留。缺历史 Release、旧数据缺已提交补丁证据等情况通过 `unavailable` 和诊断明确表达，不重新执行当前规则补造历史结果。被抑制而没有 Alert 的事件仍可读取。

KAC 保存一小时调试会话，并将租户、来源和时间窗传入上述接口。窗口结束后继续读取同一时间范围；窗口重开不复用旧会话。业务数据以 Linkd 的保留策略为准。

验收：各存储适配器的租户 / 来源 / 时间 / 游标边界，多等级、无 Alert、待处理、失败、部分成功、规则未命中、冻结规则名称、字段空值及页面筛选。不增加新的摘要校验或一致性账本。
