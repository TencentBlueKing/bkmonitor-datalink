// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package obchannel

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// fleet.get answers the deployment's verdict and counts, not its rows: which
// objects are in the demoted pool, since when, and what the last probe said
// was readable only one strategy at a time through strategy.get. object.list
// pages one column of the object route -- the pool among them -- so the whole
// population is read in pages, each row as the route serves it.
//
// The route answers each page beside the whole deployment view; object.list
// keeps the rows, the page and the counts that say what the rows are part of,
// so a page is paid for in rows rather than in the view around them.

// MaxObjectListLimit bounds one page. The response budget is spent on the
// route's whole answer, the deployment view around the rows included, before
// the view is dropped; a page that does not fit is refused by name and the
// answer offers the same page at half the rows.
const MaxObjectListLimit = 200

// objectListKept are the route's keys a page keeps besides its rows: the
// echo of what was asked, the page, and the counts the rows belong to.
var objectListKept = []string{
	"column", "order", "page", "filtered", "replica", "strategy", "business", "summary", "health",
	"anomalies_total", "demoted_total", "undecidable_total", "by_design_total",
	"demotion_entries", "demotion_extensions", "demotion_exits", "last_demotion_exit",
	"demoted_due", "demoted_due_oldest_seconds", "gaps",
}

func objectListOperation(handler http.Handler, limits map[string]any) Operation {
	text := func(description, source string) Field {
		return Field{Type: "string", Description: description, Source: source, MinLength: 1, MaxLength: 256}
	}
	zero, maxOffset, one, maxLimit := int64(0), int64(100000), int64(1), int64(MaxObjectListLimit)
	return Operation{
		ID:            "object.list",
		Summary:       "分页列出一栏运行对象（降级池 demoted、异常 anomalies、无法判定 undecidable、按设计 by_design），每行即对象路由原样返回的行；附该栏总数与入池/出池计数。",
		EvidenceScope: "deployment",
		Fields: map[string]Field{
			"column":   {Type: "string", Description: "要列的一栏；降级池是 demoted。", Enum: fleet.ObjectColumns},
			"order":    {Type: "string", Description: "oldest 从最早的开始（默认），newest 从最新的开始；翻页时保持不变。", Enum: []string{fleet.OrderOldest, fleet.OrderNewest}},
			"offset":   {Type: "integer", Description: "从第几行开始，默认 0；下一页用上一页的 next_call。", Minimum: &zero, Maximum: &maxOffset},
			"limit":    {Type: "integer", Description: "本页行数，默认 50。", Minimum: &one, Maximum: &maxLimit},
			"replica":  text("只看这个副本持有的对象；副本名不存在时拒绝。", "fleet.get per_replica[].replica"),
			"strategy": text("只看这条策略的对象。", "用户给定或 strategy.list"),
			"business": text("只看这个业务的对象。", "用户上下文或策略行"),
		},
		Required:     []string{"column"},
		OutputSchema: SchemaOf(map[string]any{}),
		Limits:       limits,
		Examples:     []Params{{"column": fleet.ColumnDemoted}, {"column": fleet.ColumnDemoted, "offset": 50, "limit": 50}},
		Run: func(ctx context.Context, p Params) Outcome {
			offset, limit := p.Int("offset", 0), p.Int("limit", 50)
			query := url.Values{"column": {p.String("column")}, "offset": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(limit)}}
			for _, name := range []string{"order", "replica", "strategy", "business"} {
				if value := p.String(name); value != "" {
					query.Set(name, value)
				}
			}
			out := invokeNative(ctx, handler, "/api/objects", query)
			if out.Error != nil && out.Error.Code == "response_budget_exceeded" && limit > 1 {
				params := objectListParams(p, offset, limit/2)
				out.Next = append(out.Next, Call{Operation: "object.list", Params: params, Reason: "这一页连同部署视图超出应答上限；同一页减半行数再读。"})
			}
			result, ok := out.Value.(map[string]any)
			if !ok || out.Error != nil {
				return out
			}
			page := map[string]any{"objects": result["anomalies"]}
			for _, key := range objectListKept {
				if value, present := result[key]; present {
					page[key] = value
				}
			}
			out.Value = page
			total := numberOf(result["page"], "total")
			rows, _ := result["anomalies"].([]any)
			out.Summary = objectListLine(p.String("column"), result, offset, len(rows), total)
			if next := offset + len(rows); len(rows) > 0 && next < total {
				out.Next = append(out.Next, Call{Operation: "object.list", Params: objectListParams(p, next, limit), Reason: fmt.Sprintf("下一页：共 %d 行，已读到第 %d 行。", total, next)})
			}
			// Every page is its own read of the view as it is then: an object
			// that leaves the pool between two pages moves the rest up by one,
			// and one that enters may or may not be read.
			out.Limitations = append(out.Limitations, "Each page is a separate read of the current view; if demotion_entries or demotion_exits differ between pages, the pages are not one moment of the population and rows may be skipped or repeated.")
			if result["filtered"] == true {
				out.Limitations = append(out.Limitations, "Rows are filtered; totals other than page.total are deployment-wide.")
			}
			return out
		},
	}
}

// objectListParams is a call for the page at offset, carrying the column,
// the order and the filters of the page it follows.
func objectListParams(p Params, offset, limit int) Params {
	params := Params{"column": p.String("column"), "offset": offset, "limit": limit}
	for _, name := range []string{"order", "replica", "strategy", "business"} {
		if value := p.String(name); value != "" {
			params[name] = value
		}
	}
	return params
}

// objectListLine is the page in one line: which column, how many in it, which
// rows this page is, and for the pool its entries and exits since the
// counted replicas started.
func objectListLine(column string, result map[string]any, offset, rows, total int) string {
	names := map[string]string{fleet.ColumnDemoted: "降级池", fleet.ColumnAnomalies: "异常", fleet.ColumnUndecidable: "无法判定", fleet.ColumnByDesign: "按设计"}
	line := fmt.Sprintf("%s %d 个", names[column], total)
	if rows > 0 {
		line += fmt.Sprintf("，本页第 %d–%d 个", offset+1, offset+rows)
	} else if total > 0 {
		line += fmt.Sprintf("，第 %d 个之后没有了", offset)
	}
	if column == fleet.ColumnDemoted {
		line += fmt.Sprintf("；进程启动以来入池 %d、延长 %d、出池 %d", numberOf(result, "demotion_entries"), numberOf(result, "demotion_extensions"), numberOf(result, "demotion_exits"))
		if due := numberOf(result, "demoted_due"); due > 0 {
			line += fmt.Sprintf("；%d 个已过冷却期未出池", due)
		}
	}
	return line
}

// numberOf reads an integer field of a decoded object, zero when absent.
func numberOf(value any, key string) int {
	object, _ := value.(map[string]any)
	n, _ := numberField(object, key)
	return int(n)
}
