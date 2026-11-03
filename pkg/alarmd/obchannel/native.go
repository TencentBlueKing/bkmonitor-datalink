// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// The scopes of strategy.list: the strategies needing attention, as fact
// rows hold them, or every strategy the published catalog accepted.
const (
	strategyScopeAttention = "attention"
	strategyScopeAccepted  = "accepted"
)

// NativeOptions are the facts the native operations add to an answer from
// this process alone, never from the route they read.
type NativeOptions struct {
	// Rejections is the latest refusal this replica kept for a Query Group,
	// with the words of the provider's answer (fleet.KeptRejection): served
	// on strategy.get, which a session reads, and on no route. Nil adds none.
	Rejections func(queryGroup string) (fleet.KeptRejection, bool)
}

// NativeOperationsWith adapts only registered reads to the existing Fleet
// handlers, with what options add. ServeHTTP is called in-process: there is
// no localhost HTTP request, arbitrary route forwarding or second Fleet
// aggregation. The existing Leader hop stays owned by Fleet. This preserves
// its evolving domain decisions without copying them into another service or
// asking every handler to change its public API.
func NativeOperationsWith(handler http.Handler, options NativeOptions) []Operation {
	text := func(description, source string) Field {
		return Field{Type: "string", Description: description, Source: source, MinLength: 1, MaxLength: 256}
	}
	integer := func(description string, min, max int64) Field {
		return Field{Type: "integer", Description: description, Minimum: &min, Maximum: &max}
	}
	limits := map[string]any{"timeout_ms": RequestTimeout.Milliseconds(), "response_bytes": MaxResponseBytes(), "population": "existing Fleet snapshots; list limit bounds returned rows, not snapshot reads"}
	makeOp := func(id, summary string, fields map[string]Field, required []string, output any, route func(Params) (string, url.Values)) Operation {
		return Operation{ID: id, Summary: summary, EvidenceScope: "deployment", Fields: fields, Required: required, OutputSchema: SchemaOf(output), Limits: limits, Run: func(ctx context.Context, p Params) Outcome {
			path, query := route(p)
			out := invokeNative(ctx, handler, path, query)
			if result, ok := out.Value.(map[string]any); ok {
				if line, ok := result["line"].(string); ok && line != "" {
					out.Summary = line
				}
				if id == "strategy.get" {
					strategyNext(&out, p, result)
					strategyGuardWarming(&out, result)
					strategyRejections(result, options.Rejections)
				}
				if id == "object.get" {
					objectSlotNext(&out, p, result)
					if line := objectExtentLine(result); line != "" && out.Summary == "" {
						out.Summary = line
					}
					if clearing := objectWindowClears(result); clearing != nil {
						result["window_clears"] = clearing
						if out.Summary != "" {
							out.Summary += "；"
						}
						out.Summary += clearing.Line
					}
					if anomaly, ok := resultAnomaly(result["anomaly"]); ok {
						if warming := fleet.GuardWarmingOf(anomaly); warming != nil {
							result["guard_warming"] = warming
							if out.Summary != "" {
								out.Summary += "；"
							}
							out.Summary += warming.Line
						}
					}
				}
				if id == "strategy.board" {
					if code, ok := result["error"].(string); ok && code != "" {
						reason, _ := result["reason"].(string)
						return Outcome{Error: &Failure{Code: "diagnosis_refused", Message: strings.TrimSpace(code + " " + reason)}}
					}
					if cursor, _ := result["next_cursor"].(string); cursor != "" {
						next := Params{"cursor": cursor, "limit": p.Int("limit", fleet.BoardDefaultRows)}
						for _, facet := range []string{"state", "action", "capability", "punctuality", "urgency", "strategy_id", "area", "reason"} {
							if value := p.String(facet); value != "" {
								next[facet] = value
							}
						}
						out.Next = append(out.Next, Call{Operation: "strategy.board", Params: next,
							Reason: "下一页；计算换过（约 30 秒）时答 reread，从第一页重读。"})
					}
				}
				if id == "strategy.list" && p.String("scope") != strategyScopeAccepted {
					// This endpoint lists fact rows, not every source strategy.
					out.Limitations = append(out.Limitations, "Population is strategies represented by current Fleet fact rows, not the complete source catalog.")
				}
				if id == "strategy.list" && p.String("scope") == strategyScopeAccepted {
					if cursor, _ := result["next_cursor"].(string); cursor != "" {
						out.Next = append(out.Next, Call{Operation: "strategy.list", Params: Params{"scope": strategyScopeAccepted, "limit": p.Int("limit", 50), "cursor": cursor},
							Reason: "已接受策略目录的下一页；游标对应的发布被替换后返回 CURSOR_STALE，从第一页重读。"})
					}
				}
			}
			return out
		}}
	}
	ops := []Operation{
		makeOp("fleet.get", "读取部署健康、覆盖、依赖和执行能力；业务异常与取证失败分开。"+
			"rebalance 是 Leader 上一轮再平衡（planned_at 那一轮，不是实时数）：滚动发布时 replaced 是这一轮从不再合格的副本移走的对象数、replacements_deferred 是因每轮交接上限（batch）留到后面轮次的、unplaceable 是这一轮没有就绪副本能接而留在原处的；replacements_deferred 与 unplaceable 都降到 0 才算换完（只有一个为 0 不算）。"+
			"unplaceable 包含 declined_everywhere：每个合格副本都因执行卡死放弃过的对象（开始提交之前超过这次执行自己的期限一分钟仍没返回，代码没响应取消；见 declined_everywhere_sample 的副本与步骤），它们哪儿都不跑、没有对象行，原因与\"没有就绪副本\"不同，要修代码；"+
			"per_replica[].declines 是该副本放弃中的对象与执行位（total/fanout，各占一个执行位直到卡住的执行返回；total 等于 fanout 时这个副本什么都不执行而就绪照常）。", map[string]Field{}, nil, fleet.HealthResponse{}, func(Params) (string, url.Values) { return "/api/health", nil }),
		makeOp("overview.get", "读取部署大盘：一句判定与它指向的环节、策略漏斗与能力矩阵（按策略计、按方面与处理结果分格，前 5 个拒绝原因）、拉取→判定→状态→输出四段的量/失败/延迟与挂在各段的依赖、资源占上限的比例、副本运行态。"+
			"服务端每 15 秒最多算一次（generated_at、max_age_seconds），同时到达的请求共用一次计算；读不到的数带 measured=false，不是 0；依赖带地址（实例名）、最近一次应答与失败，查询后端与运行态 Redis 另带只量它的 P95/失败，Linkd 控制台带它对本部署事件源的 event_source_id；不含键、查询组和 Slot，细节用其它操作读。"+
			"与 fleet.get 相同，只在承载 control 或 worker 角色的进程上回答；只承载 channel 的进程回答 business_waiting。",
			map[string]Field{}, nil, fleet.Overview{}, func(Params) (string, url.Values) { return "/api/overview", nil }),
		makeOp("trends.get", "读取趋势：本部署自己的指标（判定延迟 P95、每秒查询数、各段每分钟失败数）与容器 CPU/内存及上限，按所选时间窗从平台采集结果经查询后端读回；表达式固定在服务端，带在每条线上。"+
			"空间按 bk-monitor-operator 的 DataID 推出（自身指标取公共自定义指标 DataID、容器指标取系统 k8s 指标 DataID 的业务，多个候选逐个探测，第一个有样本的就是），写在 spaces；推不出时该空间的图为 space_unresolved 并写明原因。"+
			"no_series 表示读到了但窗口内没有样本，即没有在采集；每个时间窗的读取 30 秒内共用。",
			map[string]Field{"window": enumField("时间窗，默认 1h；速率窗口为 4 个步长且不少于 5 分钟。", []string{"1h", "6h", "24h"})}, nil, fleet.TrendsAnswer{},
			func(p Params) (string, url.Values) { return "/api/series", url.Values{"window": {p.String("window")}} }),
		makeOp("strategy.list", "按服务端状态/行动词列出需要关注的策略（scope 不填或 attention），不是全量源目录；"+
			"scope=accepted 时改为分页列出已发布目录接受的全部策略，每行是策略ID、租户、业务和它的全部 Plan（query_group 与 group_by），一条策略只出现在一页里；用 next_call 的 cursor 翻页。", map[string]Field{
			"scope":  enumField("attention（默认）列需要关注的策略；accepted 分页列出已接受的全部策略。", []string{strategyScopeAttention, strategyScopeAccepted}),
			"state":  enumField("状态词，从 words 或 describe 获取；只用于 attention。", fleet.StateWords),
			"action": enumField("行动词，从 words 或 describe 获取；只用于 attention。", fleet.ActionWords),
			"limit":  integer("返回策略行上限；默认50，不限制既有快照聚合量。", 1, 200),
			"cursor": text("accepted 的下一页游标，不自行构造。", "上一页 next_call 的 cursor"),
		}, nil, fleet.StrategyListResponse{}, func(p Params) (string, url.Values) {
			if p.String("scope") == strategyScopeAccepted {
				query := url.Values{"scope": {"strategy_catalog"}, "limit": {strconv.Itoa(p.Int("limit", 50))}}
				if cursor := p.String("cursor"); cursor != "" {
					query.Set("cursor", cursor)
				}
				return "/api/objects", query
			}
			return "/api/strategies", url.Values{"state": {p.String("state")}, "action": {p.String("action")}, "limit": {strconv.Itoa(p.Int("limit", 50))}}
		}),
		makeOp("strategy.board", "策略检测看板：全部策略（含能力上被拒的）每条一行——检测状态、谁来处理、能力类别、是否按时（取其检测对象里最坏的：超期>迟到>未知>还没开始>按时）、处于当前状态多久，"+
			"每行带紧急度 urgency（需要处理 / 可选优化 / 无需处理 / 状态未知，默认按它排序）与原因 reason（检查码、一句带数字的话、写这句话用的数字），"+
			"分面计数按全集算；late_objects / late_strategies 是“N 个检测对象、M 条策略”迟到或超期，任一实例名单被截断时带 at_least 与原因，不是确数。"+
			"与首屏计数同一次计算，由 Leader 算、30 秒内共用（generated_at、max_age_seconds）；游标绑定那次计算与那组筛选，换了计算或筛选都答 reread、从第一页重读。"+
			"各分面可填逗号分隔的多个词；不含查询组、副本、修订号，研发细节用 diagnose.environment。", map[string]Field{
			"state":       {Type: "string", Description: "检测状态词，逗号分隔多个；取自 fleet 词表（含 UNKNOWN）。", MinLength: 1, MaxLength: 512},
			"action":      {Type: "string", Description: "处理方词，逗号分隔多个。", MinLength: 1, MaxLength: 256},
			"capability":  {Type: "string", Description: "能力类别：supported、partial、unsupported_by_design、not_yet_supported，逗号分隔。", MinLength: 1, MaxLength: 256},
			"punctuality": {Type: "string", Description: "按时：on_time、late、overdue、not_started、not_detecting、unknown，逗号分隔。", MinLength: 1, MaxLength: 256},
			"urgency":     {Type: "string", Description: "紧急度：ACTION_NEEDED（需要处理）、OPTIONAL（可选优化）、NONE（无需处理）、UNKNOWN（状态未知），逗号分隔。", MinLength: 1, MaxLength: 128},
			"strategy_id": text("只看这一条策略。", "用户给定或此前的看板行"),
			"area":        text("能力方面（source、target、algorithm、output、config），与 capability 一起用：按该方面的处理结果筛，没有该方面记录的算支持，与总览能力矩阵同一算法。", "overview.get capability.matrix[].drill"),
			"reason":      text("拒绝原因码：只看带这个码的策略。", "overview.get capability.reasons[].drill"),
			"limit":       integer("每页行数；默认50，上限与 diagnose.environment 一页相同。分面总是按全集。", 1, fleet.BoardMaxRows),
			"cursor":      text("上一页 next_call 的 cursor，不自行构造。", "上一页 next_call 的 cursor"),
		}, nil, fleet.BoardResponse{}, func(p Params) (string, url.Values) {
			query := url.Values{"board": {"1"}, "limit": {strconv.Itoa(p.Int("limit", fleet.BoardDefaultRows))}}
			for _, facet := range []string{"state", "action", "capability", "punctuality", "urgency", "strategy_id", "area", "reason", "cursor"} {
				if value := p.String(facet); value != "" {
					query.Set(facet, value)
				}
			}
			return "/api/diagnose", query
		}),
		makeOp("strategy.get", "按策略ID读取接纳/拒绝、发布版本、运行对象和下一步证据入口。", map[string]Field{
			"strategy_id": text("外部策略ID。", "用户给定或 strategy.list"), "tenant": text("可选租户筛选，不是授权范围。", "用户上下文或策略行"), "business": text("可选业务筛选。", "用户上下文或策略行"),
		}, []string{"strategy_id"}, fleet.StrategyStanding{}, func(p Params) (string, url.Values) {
			return "/api/strategies/" + url.PathEscape(p.String("strategy_id")), url.Values{"tenant": {p.String("tenant")}, "business": {p.String("business")}}
		}),
		makeOp("object.get", "读取指定运行对象事实与已保留生命周期记录；保留检查上下文。", map[string]Field{
			"query_group": text("运行对象ID，不自行构造。", "strategy.get plans[].query_group"), "check": enumField("可选原始检查项，从已返回的 finding.check 获取。", fleet.Checks()), "group": text("可选检查分组，仅与check一起使用。", "此前对象列表返回的检查分组"), "records": integer("生命周期记录数；默认20、最大200，0表示不请求。", 0, 200),
		}, []string{"query_group"}, fleet.DetailResponse{}, func(p Params) (string, url.Values) {
			q := url.Values{"check": {p.String("check")}, "group": {p.String("group")}}
			if n := p.Int("records", 20); n > 0 {
				q.Set("records", strconv.Itoa(n))
			}
			return "/api/objects/" + url.PathEscape(p.String("query_group")), q
		}),
		makeOp("observation.get", "读取当前窗口或判据采样启用状态；不会开窗。", map[string]Field{"mode": {Type: "string", Description: "windows读取生命周期窗口；sample读取可选判据采样状态和预算。", Enum: []string{"windows", "sample"}}}, nil, map[string]any{}, func(p Params) (string, url.Values) {
			q := url.Values{}
			if p.String("mode") == "sample" {
				q.Set("mode", "sample")
			}
			return "/api/windows", q
		}),
		makeOp("outage.list", "列出本部署自己的 Redis 存储不可用造成的漏检：每次一行（“Redis 不可用 T1–T2：N 个对象、M 条策略丢了这些窗口”），下列每个对象被放弃的那段 Slot（策略、首尾 Slot、Slot 数、归因的存储失败 dependency_reason 及其时刻、该 Slot 自己最后一次查询失败 reason）。"+
			"归因规则：一个对象被放弃的跨度，若同一对象的轮次在跨度首次放弃前一个回放窗口（tolerance_seconds）到这次放弃之间报过 codes 里的存储失败（失败或重试），就归到这次存储不可用；时间相距不超过一个回放窗口的跨度合成一次。"+
			"没有就是 state=none_seen，只表示 coverage.since 以来没见到，不是从来没有：存储失败只在副本内存里，副本重启前的看不到，更早的构建不归因，一个对象只保留最近一段跨度；没有副本给出启动时刻时答 coverage_unknown。coverage.unattributed_spans 是被放弃但未归因到存储的跨度数，与结论并列说出（另一原因，或存储失败发生在对象换手前的进程里）；SNAPSHOT_UNAVAILABLE 只在恢复期限内读不到 Snapshot（SNAPSHOT_UNAVAILABLE_PAST_BOUND）时算存储失败。"+
			"不新增存储；rows 超出范围按名字拒绝（invalid_rows）。", map[string]Field{
			"rows":        integer("每次停机下列出的对象行上限；默认50。", 1, int64(fleet.StoreOutageMaxRows)),
			"strategy_id": text("只看这条策略的对象。", "用户给定或 strategy.board 行"),
		}, nil, fleet.StoreOutagesResponse{}, func(p Params) (string, url.Values) {
			q := url.Values{"rows": {strconv.Itoa(p.Int("rows", fleet.StoreOutageDefaultRows))}}
			if value := p.String("strategy_id"); value != "" {
				q.Set("strategy_id", value)
			}
			return "/api/outages", q
		}),
		makeOp("absent.list", "列出 leader 最近一轮缺失关闭的候选策略：link 名册成员数、本轮决定与时刻、最近一次关闭找到的告警归属（自己/别人/不明）、身份来源与业务和版本、源里现在有没有它、本任期发出关闭的次数与首次时刻（sends、first_sent_at）。"+
			"发出不等于关掉：execution.active_after_send 是已发出过关闭之后、这次读 link reconcile 仍列为活动的本部署告警数，大于 0 就是 link 没有关掉已发给它的关闭。"+
			"表只记循环每轮已做的事，读页面不调 link；每页对快照不列的行做一次文档是否存在的检查。"+
			"局限：alarmd 分不出策略是被删了，还是这个 ID 只在拆分前的旧策略表里存在过，要拿 ID 和业务去 SaaS 策略表查；"+
			"执行事实只对本任期被决定过关闭的候选才有，重启后要宽限加 ⌈候选数/8⌉ 轮 × 5 分钟才填满；source_now=document 只说明文档键还在，不说明写方为什么不列它；"+
			"被拒绝的轮次不动表，table.at 早于 last_round.at 时行是旧的。"+
			"refused_for 非空的行是源里还列着、但发布因本版本缺少的能力拒绝了的策略（改配置也恢复不了），同样按缺失关闭，关闭原因是 strategy_unsupported；配置或瞬时原因的拒绝不进候选。", map[string]Field{
			"limit":       integer("返回行上限；默认50。", 1, int64(fleet.AbsentPageRows)),
			"cursor":      text("上一页的 next_cursor。", "absent.list next_cursor"),
			"outcome":     enumField("按本轮决定过滤。", fleet.AbsentOutcomes),
			"execution":   enumField("按最近一次关闭的结果过滤。", fleet.AbsentExecutions),
			"strategy_id": text("只看这个策略 ID（所有租户下的）。", "用户给定或 absent.list 行"),
		}, nil, fleet.AbsentCandidatesResponse{}, func(p Params) (string, url.Values) {
			q := url.Values{"limit": {strconv.Itoa(p.Int("limit", fleet.AbsentPageDefaultRows))}}
			for _, name := range []string{"cursor", "outcome", "execution", "strategy_id"} {
				if value := p.String(name); value != "" {
					q.Set(name, value)
				}
			}
			return "/api/absent", q
		}),
		makeOp("sample.get", "读取已保留判据样例；无样例不证明没有执行，也不证明输出ACK。", map[string]Field{"query_group": text("运行对象ID。", "strategy.get plans[].query_group"), "limit": integer("样例条数，默认10。", 1, 50)}, []string{"query_group"}, map[string]any{}, func(p Params) (string, url.Values) {
			return "/api/objects/" + url.PathEscape(p.String("query_group")), url.Values{"samples": {strconv.Itoa(p.Int("limit", 10))}}
		}),
	}
	ops = append(ops, objectListOperation(handler, limits))
	ops = append(ops, strategyViewOperation(handler, limits))
	for i := range ops {
		if ops[i].ID == "sample.get" || ops[i].ID == "observation.get" {
			ops[i].EvidenceScope = "shared_records_with_process_diagnostics"
			ops[i].Targetable = true
		}
		if ops[i].ID == "object.get" {
			// A healthy object's row lives only on the replica tracking it:
			// targetable by its lease holder, and routed there on its own
			// when the answering replica can only say which one holds it.
			ops[i].Targetable = true
			ops[i].RouteWhenElsewhere = objectElsewhere
		}
		if ops[i].ID == "strategy.get" {
			ops[i].Examples = []Params{{"strategy_id": "101"}}
			f := ops[i].Fields["strategy_id"]
			f.Pattern = "^[1-9][0-9]*$"
			ops[i].Fields["strategy_id"] = f
		}
		if f, ok := ops[i].Fields["query_group"]; ok {
			f.Pattern = "^[A-Za-z0-9_.:-]+$"
			ops[i].Fields["query_group"] = f
		}
		if ops[i].ID == "object.get" {
			ops[i].InputRules = []any{map[string]any{"if": map[string]any{"required": []string{"group"}}, "then": map[string]any{"required": []string{"check"}}}}
			ops[i].Validate = func(p Params) error {
				if p.String("group") != "" && p.String("check") == "" {
					return errors.New("group requires check")
				}
				return nil
			}
		}
	}
	// strategy.list answers in one of two shapes, by its scope.
	for index := range ops {
		if ops[index].ID == "strategy.list" {
			ops[index].OutputSchema = map[string]any{"oneOf": []any{SchemaOf(fleet.StrategyListResponse{}), SchemaOf(fleet.StrategyCatalogResponse{})}}
		}
	}
	return ops
}

func enumField[T ~string](description string, values []T) Field {
	words := make([]string, 0, len(values))
	for _, v := range values {
		words = append(words, string(v))
	}
	return Field{Type: "string", Description: description, Enum: words}
}

func strategyNext(out *Outcome, p Params, result map[string]any) {
	id := p.String("strategy_id")
	out.Next = append(out.Next, Call{Operation: "strategy.config", Params: Params{"view": "source", "strategy_id": id}, Reason: "核对当前源配置具体值；它不代表已经生效。"})
	plans, _ := result["plans"].([]any)
	for _, entry := range plans {
		plan, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		qg, _ := plan["query_group"].(string)
		if qg == "" {
			continue
		}
		out.Next = append(out.Next, Call{Operation: "object.get", Params: Params{"query_group": qg, "records": 20}, Reason: "读取该策略关联运行对象和保留记录。"})
		digest, _ := plan["object_digest"].(string)
		if digest != "" {
			out.Next = append(out.Next, Call{Operation: "strategy.config", Params: Params{"view": "published", "strategy_id": id, "tenant": plan["tenant"], "business": plan["business"], "query_group": qg, "object_digest": digest}, Reason: "按当前返回的不可变对象引用读取发布配置；运行采用版本仍需核对。"})
		}
		if plan["existence"] == "unknown" {
			out.Complete = false
			out.Limitations = append(out.Limitations, "A published plan has unknown runtime existence.")
		}
		if len(out.Next) >= 13 {
			out.Limitations = append(out.Limitations, "Next-call suggestions are limited; remaining object references stay in result.plans.")
			break
		}
	}
}

// objectExtentLine says how much of the object the first fact covers, from
// the fact's own coverage: a DEGRADED_RUN held by ten series of twenty
// thousand and one held by all of them read the same at the top of the
// result, and only the counts under coverage tell them apart. Levels counts
// Level windows - one per series and Level - so the line names windows, not
// series. It counts only the series this round summarised a window for:
// resumed and constrained series are the rest of the object (see
// observability.HistoryCoverage), so they are named beside it, and a round
// that summarised nothing - every series resumed, or none loadable - is the
// one the line most has to describe. Nothing is said when the fact carries no
// coverage: a count that is absent is not a count of zero.
func objectExtentLine(result map[string]any) string {
	fact, _ := result["anomaly"].(map[string]any)
	if fact == nil {
		return ""
	}
	coverage, _ := fact["coverage"].(map[string]any)
	if coverage == nil {
		return ""
	}
	levels, _ := numberField(coverage, "levels")
	resumed, _ := numberField(coverage, "resumed")
	constrained, _ := numberField(coverage, "constrained")
	if levels+resumed+constrained <= 0 {
		return ""
	}
	short, _ := numberField(coverage, "short")
	guarded, _ := numberField(coverage, "guarded")
	kind, _ := fact["kind"].(string)
	reason, _ := fact["cause_reason"].(string)
	if reason == "" {
		reason, _ = fact["reason_code"].(string)
	} else if origin, _ := fact["cause_origin"].(map[string]any); origin != nil && origin["origin"] == "carried" {
		// A stored guard's reason, not this round's: say so, and from
		// which Slot when it was recorded.
		if slot, known := numberField(origin, "slot"); known && slot > 0 {
			reason += fmt.Sprintf("，carried from slot %d", int64(slot))
		} else {
			reason += "，carried, origin unknown"
		}
	}
	line := fmt.Sprintf("%s（%s）：", kind, reason)
	switch {
	case levels == 0:
		line += "本轮未汇总任何 Level 窗口"
	case short == 0 && guarded == 0:
		// Every window full: the degradation is not the history's, and a
		// "0/N" would read as nothing affected when the whole round was.
		line += fmt.Sprintf("%d 个 Level 窗口全满，降级不来自历史窗口", levels)
	default:
		line += fmt.Sprintf("%d/%d 个 Level 窗口未满，其中 %d 个处于守卫中", short, levels, guarded)
	}
	if resumed > 0 || constrained > 0 {
		line += fmt.Sprintf("；另有 %d 个序列续跑、%d 个未能加载状态，不在上述窗口内", resumed, constrained)
	}
	if total, ok := numberField(result, "facts_total"); ok && total > 1 {
		line += fmt.Sprintf("；此为 %d 条事实中的第 1 条", total)
	}
	return line
}

// objectWindowClears is, for an object whose result is not yet trusted
// because its windows are filling, when the last listed hole slides out of
// its window: the same computation the diagnosis rows carry, over the
// first fact as the fleet API returned it. Nil when the fact does not say
// so or lacks the windows or the interval to compute it from.
func objectWindowClears(result map[string]any) *fleet.WindowClearing {
	anomaly, ok := resultAnomaly(result["anomaly"])
	if !ok || anomaly.Standing == nil {
		return nil
	}
	return fleet.WindowClearsOf(anomaly, *anomaly.Standing)
}

// resultAnomaly is a fact row of a fleet answer back in its own type, for
// the computations the fleet package owns; false when it is not one.
func resultAnomaly(raw any) (fleet.Anomaly, bool) {
	fields, ok := raw.(map[string]any)
	if !ok {
		return fleet.Anomaly{}, false
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return fleet.Anomaly{}, false
	}
	var anomaly fleet.Anomaly
	if json.Unmarshal(encoded, &anomaly) != nil {
		return fleet.Anomaly{}, false
	}
	return anomaly, true
}

// strategyGuardWarming puts the guard reading on every plan row whose round
// was filed under GAP_GUARD_WARMING, and says in the summary how many there
// are and what the guard withholds: the word alone read as a strategy
// nothing evaluates.
func strategyGuardWarming(out *Outcome, result map[string]any) {
	warming := 0
	plans, _ := result["plans"].([]any)
	for _, entry := range plans {
		plan, _ := entry.(map[string]any)
		rows, _ := plan["rows"].([]any)
		for _, rawRow := range rows {
			row, _ := rawRow.(map[string]any)
			anomaly, ok := resultAnomaly(row)
			if !ok {
				continue
			}
			if reading := fleet.GuardWarmingOf(anomaly); reading != nil {
				row["guard_warming"] = reading
				warming++
			}
		}
	}
	if warming == 0 {
		return
	}
	if out.Summary != "" {
		out.Summary += "；"
	}
	out.Summary += fmt.Sprintf("%d 个运行对象处于 GAP_GUARD_WARMING：守卫只挡 NORMAL，ABNORMAL 和 RECOVERY 照常判；"+
		"缺的分钟归谁见各行 guard_warming", warming)
}

func numberField(m map[string]any, key string) (int64, bool) {
	switch v := m[key].(type) {
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	case float64:
		return int64(v), true
	}
	return 0, false
}

// Only suggest Slots whose identity was actually captured. A wall-clock log
// timestamp or a range summary is not an execution Slot.
func objectSlotNext(out *Outcome, p Params, result map[string]any) {
	rows, _ := result["records"].([]any)
	seen := map[int64]bool{}
	for _, row := range rows {
		record, ok := row.(map[string]any)
		if !ok || record["slot_identity_known"] != true || record["query_group_key"] != p.String("query_group") {
			continue
		}
		number, ok := record["evaluation_time"].(json.Number)
		if !ok {
			continue
		}
		slot, err := number.Int64()
		if err != nil || slot <= 0 || seen[slot] {
			continue
		}
		seen[slot] = true
		out.Next = append(out.Next, Call{Operation: "slot.get", Params: Params{"query_group": p.String("query_group"), "evaluation_time": slot}, Reason: "查看这轮Slot的历史查询条件、保留输入证据及重查入口。"})
		if len(seen) == 3 {
			break
		}
	}
}

type capture struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (w *capture) Header() http.Header { return w.header }
func (w *capture) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *capture) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	if w.body.Len()+len(b) > MaxResponseBytes() {
		w.overflow = true
		return 0, errors.New("OB response limit")
	}
	return w.body.Write(b)
}

func invokeNative(ctx context.Context, handler http.Handler, path string, query url.Values) Outcome {
	if handler == nil {
		return Outcome{Error: &Failure{Code: "not_configured", Message: "Fleet evidence API is not configured."}}
	}
	req := &http.Request{Method: http.MethodGet, URL: &url.URL{Path: path, RawQuery: query.Encode()}, Header: make(http.Header)}
	// PathEscape is deliberately undone into URL.Path, the decoded path
	// contract of net/http. An identifier containing '/' is not a valid object.
	decoded, err := url.PathUnescape(path)
	if err != nil {
		return Outcome{Error: &Failure{Code: "invalid_input", Message: "Invalid object path."}}
	}
	req.URL.Path = decoded
	w := &capture{header: make(http.Header)}
	handler.ServeHTTP(w, req.WithContext(ctx))
	if w.overflow {
		return Outcome{Error: &Failure{Code: "response_budget_exceeded", Message: "Native evidence exceeded the byte limit."}}
	}
	var result map[string]any
	dec := json.NewDecoder(bytes.NewReader(w.body.Bytes()))
	dec.UseNumber()
	if err := dec.Decode(&result); err != nil || result == nil {
		return Outcome{Error: &Failure{Code: "invalid_upstream_response", Message: "The evidence handler did not return a JSON object."}}
	}
	out := Outcome{Value: result, Complete: true}
	if complete, ok := result["complete"].(bool); ok && !complete {
		out.Complete = false
		out.Limitations = append(out.Limitations, "Native evidence is incomplete.")
	}
	if complete, ok := result["view_complete"].(bool); ok && !complete {
		out.Complete = false
		out.Limitations = append(out.Limitations, "The native Fleet view is incomplete.")
	}
	if total, ok := result["facts_total"].(json.Number); ok {
		count, _ := total.Int64()
		facts, _ := result["facts"].([]any)
		if count > int64(len(facts)) {
			out.Complete = false
			out.Limitations = append(out.Limitations, "Object facts are truncated; facts_total exceeds returned facts.")
		}
	}
	if result["truncated"] == true {
		out.Complete = false
		out.Limitations = append(out.Limitations, "Result rows are truncated.")
	}
	if gaps, ok := result["gaps"].([]any); ok && len(gaps) > 0 {
		out.Complete = false
		out.Limitations = append(out.Limitations, "Fleet reports coverage gaps; inspect result.gaps.")
	}
	if result["records_status"] == "unavailable" {
		out.Complete = false
		out.Limitations = append(out.Limitations, "Retained lifecycle records could not be read.")
	}
	if result["records_status"] == "empty" {
		out.Limitations = append(out.Limitations, "No retained records were returned. Inspect diagnostics and observation.get before concluding nothing executed.")
		out.Next = append(out.Next, Call{Operation: "observation.get", Params: Params{}, Reason: "核对观测窗口与保留状态，空记录不证明没有执行。"})
	}
	if w.status >= 400 {
		out.Complete = false
		// Missing retained data and partial domain responses are evidence,
		// whereas a bare handler error is a failed call. Neither means healthy.
		if _, domain := result["query_group"]; !domain {
			out.Error = &Failure{Code: "evidence_unavailable", Message: fmt.Sprintf("Evidence handler returned HTTP %d; inspect result for the precise reason.", w.status)}
		} else {
			out.Limitations = append(out.Limitations, fmt.Sprintf("Native HTTP status %d; object or evidence may be absent or unavailable.", w.status))
		}
	}
	return out
}

// objectElsewhere is the Query Group to route an object read to: one whose
// answer names the replica holding it, which the object route does only when
// it has neither a listed row nor a row of its own for it.
func objectElsewhere(p Params, result any) (string, bool) {
	body, _ := result.(map[string]any)
	holder, _ := body["tracked_by"].(string)
	group := p.String("query_group")
	return group, holder != "" && group != ""
}

// strategyRejections gives each Plan whose Query Group this replica kept a
// refusal for that refusal, with the sanitized start of the provider's
// answer. Only the replica tracking the group keeps it: a Plan held
// elsewhere carries none here, and is read there by targeting strategy.get
// at its plans[].replica.
func strategyRejections(result map[string]any, rejections func(string) (fleet.KeptRejection, bool)) {
	if rejections == nil {
		return
	}
	plans, _ := result["plans"].([]any)
	for _, entry := range plans {
		plan, _ := entry.(map[string]any)
		group, _ := plan["query_group"].(string)
		if group == "" {
			continue
		}
		if kept, ok := rejections(group); ok {
			plan["rejection"] = kept
		}
	}
}
