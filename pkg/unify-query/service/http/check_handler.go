// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
)

// --- 响应体

// CheckQueryTsDataResponse check 接口成功响应体。
type CheckQueryTsDataResponse struct {
	// Data 每项为 QueryCheckPreview.GetRequestBody() 序列化结果。直查 VM 常为单元素 VmQueryCheckBody；非直查若子查询预览均为占位 nil 则 400。不下发真实 TSDB。
	Data []any `json:"data"`
	// TraceID 链路 ID（与 trace span 一致）。
	TraceID string `json:"trace_id"`
}

// --- HTTP Handlers

// HandlerCheckQueryTs
// @Summary	query ts monitor check by ts
// @ID		check_query_ts
// @Produce  json
// @Param    traceparent            header    string                          false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    X-Bk-Scope-Space-Uid   header    string                        false  "空间UID" default(bkcc__2)
// @Param	 X-Bk-Scope-Skip-Space  header	  string						false  "是否跳过空间验证" default()
// @Param    data                  	body      structured.QueryTs  			true   "json data"
// @Success  200                   	{object}  CheckQueryTsDataResponse
// @Failure  400                   	{object}  ErrResponse
// @Router   /check/query/ts [post]
func HandlerCheckQueryTs(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		resp = &response{c: c}
		err  error
	)

	ctx, span := trace.NewSpan(ctx, "check-query-ts")
	defer span.End(&err)

	queryTs := &structured.QueryTs{}
	err = json.NewDecoder(c.Request.Body).Decode(queryTs)
	if err != nil {
		resp.failed(ctx, metadata.NewMessage(
			metadata.MsgQueryTs,
			"json 格式解析异常",
		).Error(ctx, err))
		return
	}

	user := metadata.GetUser(ctx)
	if user.SpaceUID != "" {
		queryTs.SpaceUid = user.SpaceUID
	}

	data, err := checkQueryTsData(ctx, queryTs)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	resp.success(ctx, CheckQueryTsDataResponse{
		Data:    data,
		TraceID: span.TraceID(),
	})
}

// HandlerCheckQueryPromQL
// @Summary	query promql monitor check by ts
// @ID		check_query_promql
// @Produce  json
// @Param    traceparent            header    string                          false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    X-Bk-Scope-Space-Uid   header    string                        false  "空间UID" default(bkcc__2)
// @Param	 X-Bk-Scope-Skip-Space  header	  string						false  "是否跳过空间验证" default()
// @Param    data                  	body      structured.QueryPromQL  		true   "json data"
// @Success  200                   	{object}  CheckQueryTsDataResponse
// @Failure  400                   	{object}  ErrResponse
// @Router   /check/query/ts/promql [post]
func HandlerCheckQueryPromQL(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		resp = &response{c: c}
		err  error
	)

	ctx, span := trace.NewSpan(ctx, "check-query-promql")
	defer span.End(&err)

	queryPromQL := &structured.QueryPromQL{}
	err = json.NewDecoder(c.Request.Body).Decode(queryPromQL)
	if err != nil {
		resp.failed(ctx, metadata.NewMessage(
			metadata.MsgParserPromQL,
			"json 格式解析异常",
		).Error(ctx, err))
		return
	}

	queryTs, err := promQLToStruct(ctx, queryPromQL)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	user := metadata.GetUser(ctx)
	if user.SpaceUID != "" {
		queryTs.SpaceUid = user.SpaceUID
	}

	data, err := checkQueryTsData(ctx, queryTs)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	resp.success(ctx, CheckQueryTsDataResponse{
		Data:    data,
		TraceID: span.TraceID(),
	})
}

// --- 编排：QueryReference → 直查 / 非直查预览

// checkQueryTsData 将 QueryTs 转为 QueryReference，再按直查/非直查组装预览（不调真实 TSDB）。
// 直查 VM：metricql 由 vmCheckMetricql 生成（ToPromExpr 空 PromExprOption + MetricFilterCondition 替换变量）。
// 非直查：不生成 VM 预览，仅按子查询追加其它存储预览（待扩展）todo: 未来扩展
func checkQueryTsData(ctx context.Context, q *structured.QueryTs) ([]any, error) {
	qr, err := checkQueryTsToReference(ctx, q)
	if err != nil {
		return nil, err
	}

	qb := metadata.GetQueryParams(ctx)

	if qb.IsDirectQuery() {
		// 与 queryTsToInstanceAndStmt 直查分支一致：ToVmExpand + SetExpand，后续与 DirectQuery 同源读 GetExpand。
		vmExpand := query.ToVmExpand(ctx, qr)
		metadata.SetExpand(ctx, vmExpand)

		promQL, err := vmCheckMetricql(ctx, q, qr)
		if err != nil {
			return nil, err
		}
		iface, err := vmCheckPreviewIface(ctx, qr, promQL)
		if err != nil {
			return nil, err
		}
		return appendCheckPreview(ctx, nil, iface)
	}

	// 非直查：不涉及 VM 与 ToPromExpr；校验实例并按存储类型追加预览（Doris/ES 当前跳过）todo: 未来扩展
	var out []any
	var rangeErr error
	qr.Range("", func(qry *metadata.Query) {
		if rangeErr != nil {
			return
		}
		if prometheus.GetTsDbInstance(ctx, qry) == nil {
			rangeErr = fmt.Errorf("instance is null, with storageID %s", qry.StorageID)
			return
		}
		iface, err := checkTsdbPreviewForSubQuery(ctx, qry)
		if err != nil {
			rangeErr = err
			return
		}
		out, rangeErr = appendCheckPreview(ctx, out, iface)
	})
	if rangeErr != nil {
		return nil, rangeErr
	}
	if len(out) == 0 {
		return nil, metadata.NewMessage(
			metadata.MsgQueryReference,
			"未解析到可路由的查询",
		).Error(ctx, fmt.Errorf("empty check query reference"))
	}
	return out, nil
}

// --- QueryReference（与 queryTsToInstanceAndStmt 前置对齐）

// checkQueryTsToReference 在 ToQueryReference 前的处理与 queryTsToInstanceAndStmt 内联逻辑一致（复制，避免改动 query.go）。
func checkQueryTsToReference(ctx context.Context, q *structured.QueryTs) (metadata.QueryReference, error) {
	var err error
	if DefaultQueryListLimit > 0 {
		if len(q.QueryList) > DefaultQueryListLimit {
			err = fmt.Errorf("the number of query lists cannot be greater than %d", DefaultQueryListLimit)
		}
	}
	for _, ql := range q.QueryList {
		ql.NotPromFunc = false
		ql.AlignInfluxdbResult = AlignInfluxdbResult && !q.Reference && !q.NotTimeAlign
		ql.OrderBy = q.OrderBy
		if ql.Step == "" {
			ql.Step = q.Step
		}
		if ql.Limit == 0 && q.Limit > 0 {
			ql.Limit = q.Limit
		}
		if ql.From == 0 && q.From > 0 {
			ql.From = q.From
		}
	}
	if q.LookBackDelta != "" {
		if _, e := time.ParseDuration(q.LookBackDelta); e != nil {
			return nil, e
		}
	}
	if q.Step == "" {
		q.Step = promql.GetDefaultStep().String()
	}
	qr, err2 := q.ToQueryReference(ctx)
	if err2 != nil {
		return nil, err2
	}
	_ = err
	return qr, nil
}

// --- 直查 VM：VmExpand、MetricQL 预览、VmQueryCheckBody

// vmExpandForCheck 优先 metadata.GetExpand(ctx)（直查 Check 在 checkQueryTsData 内已 SetExpand，与 queryTsToInstanceAndStmt 一致）；
// ctx 未写入时退回 ToVmExpand(qr)，便于单测直接调用 vmCheckMetricql / vmCheckPreviewIface。
func vmExpandForCheck(ctx context.Context, qr metadata.QueryReference) *metadata.VmExpand {
	if v := metadata.GetExpand(ctx); v != nil {
		return v
	}
	return query.ToVmExpand(ctx, qr)
}

// vmCheckMetricql 直查 VM 的 MetricQL 预览（内存拼装）；q 与 qr 须同源，一般 qr 来自 q.ToQueryReference。
func vmCheckMetricql(ctx context.Context, q *structured.QueryTs, qr metadata.QueryReference) (string, error) {
	// 过滤串与真实 VM 侧 metric_filter_condition 同源；无展开或无条件则无法预览。
	vmExpand := vmExpandForCheck(ctx, qr)
	if vmExpand == nil || len(vmExpand.MetricFilterCondition) == 0 {
		return "", metadata.NewMessage(
			metadata.MsgQueryReference,
			"vm 展开或 metric_filter_condition 为空",
		).Error(ctx, fmt.Errorf("vm expand metric filter empty"))
	}

	// 空 PromExprOption 时 ToPromExpr 不会校验 Conditions；此处与 ToPromQL 路径对齐，避免无法 ToProm 的条件仍生成预览。
	for _, ql := range q.QueryList {
		if _, _, err := ql.Conditions.ToProm(); err != nil {
			return "", err
		}
	}

	// 不填 ReferenceNameMetric / LabelMatcher，表达式叶子仍为 reference 名（a、b），便于下一步文本替换
	promExprOpt := &structured.PromExprOption{}
	expr, err := q.ToPromExpr(ctx, promExprOpt)
	if err != nil {
		return "", err
	}
	out := expr.String()

	// 按词边界把 ref 换成 {MetricFilterCondition[ref]}；ref 名按长度降序，避免 a 误替换 ab
	refs := make([]string, 0, len(vmExpand.MetricFilterCondition))
	for ref := range vmExpand.MetricFilterCondition {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool { return len(refs[i]) > len(refs[j]) })

	for _, ref := range refs {
		filter := vmExpand.MetricFilterCondition[ref]
		if filter == "" {
			continue
		}
		// QuoteMeta：ref 中的正则元字符按字面匹配；\b：整词替换，避免误伤更长标识符
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(ref) + `\b`)
		out = re.ReplaceAllString(out, "{"+filter+"}")
	}
	return out, nil
}

// vmCheckPreviewIface 基于 QueryReference 的 VM 展开构造 VmQueryCheckBody（实现 QueryCheckPreview）。
func vmCheckPreviewIface(ctx context.Context, qr metadata.QueryReference, promQL string) (tsdb.QueryCheckPreview, error) {
	vmExpand := vmExpandForCheck(ctx, qr)
	if vmExpand == nil || len(vmExpand.ResultTableList) == 0 {
		return nil, metadata.NewMessage(
			metadata.MsgQueryReference,
			"vm 展开结果为空",
		).Error(ctx, fmt.Errorf("vm expand is empty"))
	}
	return &tsdb.VmQueryCheckBody{
		StorageType:     metadata.VictoriaMetricsStorageType,
		MetricQL:        promQL,
		ResultTableList: append([]string(nil), vmExpand.ResultTableList...),
	}, nil
}

// --- 非直查：按子查询 storage 的预览占位

// checkTsdbPreviewForSubQuery 非直查路径按子查询 storage_type 返回预览；不涉及 VM（VM 仅直查路径处理）。
// Doris/ES/BkSql 当前返回 nil 待扩展；VM 若出现则视为路由异常并返回错误；其它类型暂不支持。
func checkTsdbPreviewForSubQuery(ctx context.Context, qry *metadata.Query) (tsdb.QueryCheckPreview, error) {
	switch qry.StorageType {
	case metadata.DorisStorageType, metadata.BkSqlStorageType, metadata.ElasticsearchStorageType: // todo: 未来支持其他存储
		return nil, nil
	default:
		return nil, metadata.NewMessage(
			metadata.MsgQueryReference,
			"check 暂不支持该存储类型",
		).Error(ctx, fmt.Errorf("unsupported storage_type %q for check preview", qry.StorageType))
	}
}

// --- 工具

// appendCheckPreview 将 iface.GetRequestBody() 追加到 out；iface 为 nil 时跳过（不报错），并打 Warn 便于排查占位未实现。
func appendCheckPreview(ctx context.Context, out []any, iface tsdb.QueryCheckPreview) ([]any, error) {
	if iface == nil {
		log.Warnf(ctx, "check: skip nil QueryCheckPreview (no preview body for this subquery, e.g. doris/es placeholder)")
		return out, nil
	}
	item, err := iface.GetRequestBody()
	if err != nil {
		return out, err
	}
	return append(out, item), nil
}
