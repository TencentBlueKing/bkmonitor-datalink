// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package aicli

import "slices"

// parameter 描述 HTTP 边界参数；领域对象的完整业务校验仍由 Console/控制面负责。
type parameter struct {
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	Required    bool        `json:"required"`
	Description string      `json:"description"`
	Min         int64       `json:"min,omitempty"`
	Max         int64       `json:"max,omitempty"`
	Enum        []string    `json:"enum,omitempty"`
	Format      string      `json:"format,omitempty"`
	Fields      []parameter `json:"fields,omitempty"`
}

type operation struct {
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	Method        string         `json:"method"`
	Path          string         `json:"path"`
	RequiresWrite bool           `json:"requires_write"`
	Impact        string         `json:"impact,omitempty"`
	PathParams    []parameter    `json:"path_parameters"`
	QueryParams   []parameter    `json:"query_parameters"`
	BodyParams    []parameter    `json:"body_parameters"`
	BodyLimit     int64          `json:"body_limit_bytes,omitempty"`
	Pagination    string         `json:"pagination,omitempty"`
	Example       map[string]any `json:"example"`
}

func stringParam(name string, required bool, max int64, description string) parameter {
	return parameter{Name: name, Type: "string", Required: required, Max: max, Description: description}
}

func integerParam(name string, required bool, min, max int64, description string) parameter {
	return parameter{Name: name, Type: "integer", Required: required, Min: min, Max: max, Description: description}
}

func formatParam(name, format string, required bool, max int64, description string) parameter {
	p := stringParam(name, required, max, description)
	p.Format = format
	return p
}

func objectParam(name string, required bool, description string, fields ...parameter) parameter {
	return parameter{Name: name, Type: "object", Required: required, Description: description, Fields: fields}
}

// catalog 是唯一调用白名单；新接口必须显式决定读写语义，不能从 HTTP 方法推断。
// 每次返回独立值，测试或 Cobra 实例之间不共享可变注册状态。
func catalog() []operation {
	var result []operation
	add := func(name, description, method, path string, query []parameter) {
		result = append(result, operation{Name: name, Description: description, Method: method, Path: "/local-api/" + path,
			PathParams: []parameter{}, QueryParams: query, BodyParams: []parameter{}, Example: map[string]any{}})
	}
	for _, entry := range []struct{ name, path, description string }{
		{"server.version", "version", "Console 构建版本"},
		{"server.capabilities", "capabilities", "可用数据源、过滤器与查询预算"},
		{"server.config", "config", "Console 启动配置的脱敏副本；不证明其他进程已加载相同配置"},
		{"scheduling.get", "scheduling", "Worker、任务分配与来源路由"},
		{"dynamic-config.get", "dynamic-config", "动态配置快照、同步状态与 Worker digest"},
		{"runtime.processes", "runtime/processes", "进程观测状态"},
		{"runtime.cleaner", "runtime/cleaner", "Cleaner 状态、指标和 Kafka 消费"},
		{"kafka.inspect", "infrastructure/kafka", "Kafka 元数据与消费积压"},
		{"elasticsearch.topology", "elasticsearch/topology", "ES 索引、别名、模板及集群拓扑"},
		{"elasticsearch.performance", "elasticsearch/performance", "ES CPU、堆、写入队列及合并状态"},
		{"metrics.catalog", "metrics/catalog", "当前控制面二进制包含的指标定义"},
		{"strategy-index.targets", "strategy-index/targets", "策略索引 Hook 查询目标"},
		{"strategy-audits.latest", "strategy-index/audits", "最近一份审计结果；null 表示没有任务"},
	} {
		add(entry.name, entry.description, "GET", entry.path, []parameter{})
	}
	source := stringParam("event_source_id", false, 128, "来源身份；省略时服务端可能选择首个来源，定位指定来源时应显式提供")
	add("runtime.lifecycle", "Lifecycle 状态、指标和来源 Redis", "GET", "runtime/lifecycle", []parameter{source})
	add("runtime.control-plane", "控制面任务状态及指标", "GET", "runtime/control-plane", []parameter{
		integerParam("range_seconds", false, 60, 604800, "观测窗口，默认 3600 秒，仍受服务端上限约束"), stringParam("instance", false, 512, "实例过滤")})
	add("redis.inspect", "来源 Signal Stream、Group、PEL 与 lag", "GET", "infrastructure/redis", []parameter{source})
	add("redis.pending", "有界查看 PEL，不认领消息", "GET", "infrastructure/redis/pending", []parameter{source, stringParam("group", false, 256, "消费组"), integerParam("limit", false, 1, 100, "默认 50")})
	for _, kind := range []string{"mailboxes", "leases"} {
		add("redis."+kind, "有界查看 Redis "+kind, "GET", "infrastructure/redis/"+kind, []parameter{source, stringParam("query", false, 128, "身份筛选"), integerParam("limit", false, 1, 100, "默认 50；保留扫描截断标识")})
	}
	add("metrics.query", "有界指标时间窗口", "GET", "metrics", []parameter{
		formatParam("from", "datetime", true, 64, "起始 UTC 时间"), formatParam("to", "datetime", true, 64, "结束 UTC 时间"), integerParam("step", true, 1, 3600, "采样间隔秒"), integerParam("calculation_window_seconds", false, 15, 3600, "计算窗口，默认 60 秒"), stringParam("instance", false, 512, "实例"), source, integerParam("partition", false, 0, 2147483647, "Kafka 分区")})
	result[len(result)-1].Example = map[string]any{"query": map[string]string{"from": "2026-09-24T00:00:00Z", "to": "2026-09-24T01:00:00Z", "step": "60"}}
	for _, entity := range []string{"events", "alerts", "alert-logs"} {
		add(entity+".list", "按租户、身份、时间和状态查询 "+entity, "GET", entity, entityQuery())
		result[len(result)-1].Pagination = "传回 nextCursor 到 cursor；保持租户和过滤条件不变，不自动翻页。省略租户将使用服务端跨租户查询。"
		result[len(result)-1].Example = map[string]any{"query": map[string]string{"bk_tenant_id": "tenant-a", "limit": "20"}}
		add(entity+".stats", "查询 "+entity+" 的统计、时间分布及 facets", "GET", entity+"/stats", entityQuery())
		add(entity+".get", "按明确租户和身份读取 "+entity, "GET", entity+"/{id}", []parameter{stringParam("bk_tenant_id", true, 1024, "目标租户，不能猜测")})
		result[len(result)-1].PathParams = []parameter{stringParam("id", true, 1024, "对象身份")}
		result[len(result)-1].Example = map[string]any{"path": map[string]string{"id": "object-id"}, "query": map[string]string{"bk_tenant_id": "tenant-a"}}
	}
	sourceID := formatParam("id", "source-id", true, 32, "EventSource 身份")
	add("event-sources.list", "查询脱敏来源配置（包含删除或未发布记录）", "GET", "event-sources", []parameter{stringParam("after", false, 256, "上页最后一项 id"), integerParam("limit", false, 1, 1000, "默认 100")})
	result[len(result)-1].Pagination = "after 使用上页最后一项 id；不足 limit 表示结束。不自动翻页。"
	for _, op := range []struct{ name, path, description string }{
		{"event-sources.get", "event-sources/{id}", "来源编辑版本、revision 及已发布版本，凭据脱敏"},
		{"enrich.config", "enrich/config/{id}", "来源已发布丰富规则，非未发布编辑版本"},
	} {
		add(op.name, op.description, "GET", op.path, []parameter{})
		result[len(result)-1].PathParams = []parameter{sourceID}
	}
	for _, op := range []struct{ name, method, description string }{
		{"event-sources.apply", "PUT", "创建或更新来源；服务端验证、持久化并推进发布"},
		{"event-sources.delete", "DELETE", "标记来源删除，调度与来源处理可能停止"},
	} {
		add(op.name, op.description, op.method, "event-sources/{id}", []parameter{})
		o := &result[len(result)-1]
		o.PathParams = []parameter{sourceID}
		o.RequiresWrite = true
		o.Impact = op.description
		o.BodyLimit = 1 << 20
		o.BodyParams = []parameter{integerParam("expected_revision", true, 0, 9223372036854775807, "必须来自已核对的当前版本；创建为 0，冲突不能自动重试覆盖")}
		if op.method == "PUT" {
			o.BodyParams = append(o.BodyParams, objectParam("spec", true, "完整 EventSource 对象，嵌套领域字段由服务端验证；省略凭据遵循服务端保留规则"))
		}
		o.Example = map[string]any{"path": map[string]string{"id": "source-a"}, "body": map[string]any{"expected_revision": 1}}
		if op.method == "PUT" {
			o.Example["body"].(map[string]any)["spec"] = map[string]any{"event_source_id": "source-a", "enabled": false, "cleaner": map[string]string{"type": "standard"}, "storage": map[string]any{}, "scheduling": map[string]any{}}
		}
	}
	add("alerts.close", "显式人工关闭 active 告警，重试必须保持全部命令字段不变", "POST", "alerts/{id}/close", []parameter{})
	closeOp := &result[len(result)-1]
	closeOp.PathParams = []parameter{stringParam("id", true, 160, "Alert ID")}
	closeOp.RequiresWrite = true
	closeOp.BodyLimit = 4096
	closeOp.Impact = "关闭状态可能已保存并执行当前发布来源的 FinalHook；失败不保证回滚。operator_id 由 Console 认证身份提供。"
	closeOp.BodyParams = []parameter{stringParam("bk_tenant_id", true, 64, "目标租户"), formatParam("operation_id", "uuid", true, 36, "经确认的稳定 UUID，重试复用"), stringParam("reason", true, 256, "关闭原因（UTF-8 字节上限）"), formatParam("effective_at", "datetime", true, 64, "经确认的 UTC 操作时间，重试保持原值")}
	closeOp.Example = map[string]any{"path": map[string]string{"id": "alert-a"}, "body": map[string]any{"bk_tenant_id": "tenant-a", "operation_id": "581e3d13-c28b-45be-b06e-bc3f21d233cc", "reason": "人工确认结束", "effective_at": "2026-09-24T00:00:00Z"}}
	strategyQuery := []parameter{formatParam("event_source_id", "source-id", true, 32, "来源"), formatParam("hook_name", "hook-name", true, 64, "策略索引 Hook 名称")}
	add("strategy-index.browse", "有界扫描策略索引；可能跨目标中的多个租户", "GET", "strategy-index/browse", append(slices.Clone(strategyQuery), stringParam("cursor", false, 2048, "扫描游标"), integerParam("count", false, 10, 200, "默认 50")))
	result[len(result)-1].Pagination = "传回 nextCursor 到 cursor，保持目标不变；扫描不保证事务快照。"
	add("strategy-index.reconcile", "只读检查单租户策略差异，不修复索引", "GET", "strategy-index/reconcile", append(slices.Clone(strategyQuery), stringParam("bk_tenant_id", true, 256, "租户"), stringParam("strategy_id", true, 1024, "策略身份")))
	add("strategy-audits.start", "启动后台只读全量审计任务", "POST", "strategy-index/audits", []parameter{})
	o := &result[len(result)-1]
	o.RequiresWrite = true
	o.BodyLimit = 1 << 20
	o.BodyParams = strategyQuery
	o.Impact = "对来源及 Hook 对应的全部租户执行有界扫描，占用查询资源；不修改业务数据。"
	o.Example = map[string]any{"body": map[string]string{"event_source_id": "source-a", "hook_name": "active-index"}}
	for _, action := range []string{"get", "cancel"} {
		method, path := "GET", "strategy-index/audits/{id}"
		if action == "cancel" {
			method = "POST"
			path += "/cancel"
		}
		add("strategy-audits."+action, "审计任务 "+action, method, path, []parameter{})
		o := &result[len(result)-1]
		o.PathParams = []parameter{formatParam("id", "uuid", true, 36, "审计任务 UUID")}
		if action == "cancel" {
			o.RequiresWrite = true
			o.Impact = "取消指定审计任务，结果可能不完整，不修改业务数据。"
		}
	}
	add("enrich.preview", "只读模拟丰富，访问已配置外部资源，不保存告警、不触发 Hook", "POST", "enrich/preview", []parameter{})
	o = &result[len(result)-1]
	o.BodyLimit = 1 << 20
	o.BodyParams = []parameter{stringParam("bk_tenant_id", true, 64, "租户"), formatParam("event_source_id", "source-id", true, 32, "来源"), objectParam("input", true, "alert_id 与 alert 必须二选一", stringParam("alert_id", false, 256, "已有告警"), objectParam("alert", false, "临时告警对象，租户及来源必须匹配")), objectParam("enrich", false, "临时丰富规则，仅本次模拟；省略使用已发布规则")}
	o.Example = map[string]any{"body": map[string]any{"bk_tenant_id": "tenant-a", "event_source_id": "source-a", "input": map[string]string{"alert_id": "alert-a"}}}
	for _, action := range []string{"search", "related", "close"} {
		add("onemodel."+action, "OneModel 只读调试；close 仅释放查询快照", "POST", "onemodel/"+action, []parameter{})
		o := &result[len(result)-1]
		o.BodyLimit = 1 << 20
		o.BodyParams = []parameter{stringParam("bk_tenant_id", true, 64, "租户")}
		switch action {
		case "search":
			o.BodyParams = append(o.BodyParams, stringParam("model_id", true, 128, "模型"), objectParam("where", false, "类型化 all/any/not 条件；空对象查询全部实例"), integerParam("limit", false, 1, 200, "默认 50"), stringParam("cursor", false, 16384, "查询游标"))
			o.Pagination = "next_cursor 回传 cursor；租户、模型、条件和页大小保持不变。结束自动释放，放弃查询使用 onemodel.close。"
			o.Example = map[string]any{"body": map[string]any{"bk_tenant_id": "tenant-a", "model_id": "cw-Host", "where": map[string]any{}, "limit": 20}}
		case "related":
			roots := parameter{Name: "roots", Type: "array", Required: true, Min: 1, Max: 1024, Description: "起点实例身份", Fields: []parameter{stringParam("model_id", true, 128, "模型"), stringParam("model_inst_id", true, 1024, "实例身份")}}
			direction := stringParam("direction", true, 4, "关联方向")
			direction.Enum = []string{"out", "in", "both"}
			o.BodyParams = append(o.BodyParams, roots, stringParam("relation", true, 256, "关联标识"), direction, objectParam("query", true, "目标模型过滤", stringParam("model_id", true, 128, "模型"), objectParam("where", false, "类型化过滤"), integerParam("limit", false, 1, 1024, "默认 1024，超限失败，不截断成功")))
			o.Example = map[string]any{"body": map[string]any{"bk_tenant_id": "tenant-a", "roots": []any{map[string]string{"model_id": "cw-Host", "model_inst_id": "101"}}, "relation": "belongs", "direction": "out", "query": map[string]string{"model_id": "cw-Biz"}}}
		case "close":
			o.BodyParams = append(o.BodyParams, stringParam("cursor", true, 16384, "待释放的查询快照游标"))
			o.Example = map[string]any{"body": map[string]string{"bk_tenant_id": "tenant-a", "cursor": "返回的 next_cursor"}}
		}
	}
	slices.SortFunc(result, func(a, b operation) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	return result
}

func entityQuery() []parameter {
	var result []parameter
	for _, name := range []string{"bk_tenant_id", "id", "event_source_id", "related_alert_id", "fingerprint", "alert_id", "operation_kind", "operator_kind", "outcome", "subject_id", "source_event_id", "source_alert_id"} {
		result = append(result, stringParam(name, false, 1024, "与 Console 同名过滤条件；省略租户允许跨租户查询"))
	}
	result = append(result, formatParam("from", "datetime", false, 64, "UTC 起始时间；省略使用服务端默认窗口"), formatParam("to", "datetime", false, 64, "UTC 结束时间"), stringParam("state", false, 64, "状态"), stringParam("status", false, 64, "处理状态"), stringParam("severity", false, 255, "等级"), integerParam("limit", false, 1, 200, "页大小，仍受服务端上限约束"), stringParam("cursor", false, 16384, "服务端返回的 nextCursor"))
	order := stringParam("order", false, 4, "排序方向")
	order.Enum = []string{"asc", "desc"}
	enrich := stringParam("enrich_status", false, 16, "丰富状态")
	enrich.Enum = []string{"pending", "succeeded", "partial", "failed", "skipped"}
	return append(result, order, enrich)
}
